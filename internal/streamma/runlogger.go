package streamma

import (
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const RunLoggerFileName = "streamma-runlogger.json"

type RunLogger struct {
	Summary RunLoggerSummary `json:"summary"`
}

type RunLoggerSummary struct {
	RunID              string                           `json:"run_id,omitempty"`
	Agents             map[string]RunLoggerAgentSummary `json:"agents"`
	AgentCount         int                              `json:"agent_count"`
	TotalPrefillTokens int                              `json:"total_prefill_tokens"`
	TotalCachedTokens  int                              `json:"total_cached_tokens"`
	TotalDecodeTokens  int                              `json:"total_decode_tokens"`
	TotalTokens        int                              `json:"total_tokens"`
	SpeedupAnalysis    RunLoggerSpeedupAnalysis         `json:"speedup_analysis"`
	PaperAlignment     *RunLoggerPaperAlignment         `json:"paper_alignment,omitempty"`
}

type RunLoggerAgentSummary struct {
	Segments        int     `json:"segments"`
	PrefillTokens   int     `json:"prefill_tokens"`
	CachedTokens    int     `json:"cached_tokens"`
	KVCacheHitRatio float64 `json:"kv_cache_hit_ratio"`
	DecodeTokens    int     `json:"decode_tokens"`
	APITime         float64 `json:"api_time"`
}

type RunLoggerSpeedupAnalysis struct {
	APITime                float64  `json:"api_time"`
	WallTime               float64  `json:"wall_time"`
	Speedup                float64  `json:"speedup"`
	ObservedOverlapSpeedup float64  `json:"observed_overlap_speedup"`
	TotalKVCacheHitRatio   float64  `json:"total_kv_cache_hit_ratio"`
	CriticalPathTime       float64  `json:"critical_path_time"`
	StreamingSpeedup       float64  `json:"streaming_speedup"`
	Timeline               []string `json:"timeline"`
}

type RunLoggerPaperAlignment struct {
	TopologyShape          string         `json:"topology_shape,omitempty"`
	ExpectedAgents         int            `json:"expected_agents,omitempty"`
	ExpectedSteps          int            `json:"expected_steps,omitempty"`
	ActualSegmentsByAgent  map[string]int `json:"actual_segments_by_agent,omitempty"`
	StepDeviation          map[string]int `json:"step_deviation,omitempty"`
	CriticalPathAgentCount int            `json:"critical_path_agent_count,omitempty"`
}

type AgentRunSpan struct {
	AgentID    string    `json:"agent_id"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Usage      StepUsage `json:"usage,omitempty"`
}

type runLoggerAgentAccumulator struct {
	id string

	segments int

	eventPrefill int
	eventCached  int
	eventDecode  int

	spanPrefill int
	spanCached  int
	spanDecode  int
	hasSpan     bool

	intervals []runLoggerInterval
	first     time.Time
	last      time.Time
}

type runLoggerInterval struct {
	start time.Time
	end   time.Time
}

func BuildRunLogger(graph GraphSpec, events []Event, spans []AgentRunSpan, selections ...GraphSelectionEvidence) RunLogger {
	accumulators := map[string]*runLoggerAgentAccumulator{}
	var order []string
	ensure := func(agentID string) *runLoggerAgentAccumulator {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			agentID = "unknown"
		}
		if acc, ok := accumulators[agentID]; ok {
			return acc
		}
		acc := &runLoggerAgentAccumulator{id: agentID}
		accumulators[agentID] = acc
		order = append(order, agentID)
		return acc
	}
	for _, agent := range graph.Agents {
		ensure(agent.ID)
	}
	for _, event := range events {
		agentID := event.ProducerAgentID
		if agentID == "" && event.Step != nil {
			agentID = event.Step.AgentID
		}
		if agentID == "" && event.Final != nil {
			agentID = event.Final.AgentID
		}
		if agentID == "" {
			continue
		}
		acc := ensure(agentID)
		acc.observe(event.Timestamp)
		if event.Type == EventStepCommitted && event.Step != nil {
			acc.segments++
			acc.eventPrefill += maxInt(0, event.Step.Usage.InputTokens)
			acc.eventCached += maxInt(0, event.Step.Usage.CachedTokens)
			acc.eventDecode += maxInt(0, event.Step.Usage.OutputTokens)
		}
	}
	for _, span := range spans {
		acc := ensure(span.AgentID)
		acc.hasSpan = true
		acc.spanPrefill += maxInt(0, span.Usage.InputTokens)
		acc.spanCached += maxInt(0, span.Usage.CachedTokens)
		acc.spanDecode += maxInt(0, span.Usage.OutputTokens)
		if !span.StartedAt.IsZero() || !span.FinishedAt.IsZero() {
			start := span.StartedAt
			end := span.FinishedAt
			if start.IsZero() {
				start = end
			}
			if end.IsZero() {
				end = start
			}
			if end.Before(start) {
				end = start
			}
			acc.intervals = append(acc.intervals, runLoggerInterval{start: start, end: end})
			acc.observe(start)
			acc.observe(end)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		return graphAgentIndex(graph, order[i]) < graphAgentIndex(graph, order[j])
	})
	summary := RunLoggerSummary{
		RunID:      strings.TrimSpace(graph.RunID),
		Agents:     map[string]RunLoggerAgentSummary{},
		AgentCount: len(order),
	}
	var runStart, runEnd time.Time
	for _, agentID := range order {
		acc := accumulators[agentID]
		prefill, cached, decode := acc.tokenTotals()
		apiTime := acc.apiSeconds()
		summary.Agents[agentID] = RunLoggerAgentSummary{
			Segments:        acc.segments,
			PrefillTokens:   prefill,
			CachedTokens:    cached,
			KVCacheHitRatio: roundFloat(ratio(cached, prefill), 4),
			DecodeTokens:    decode,
			APITime:         roundFloat(apiTime, 2),
		}
		summary.TotalPrefillTokens += prefill
		summary.TotalCachedTokens += cached
		summary.TotalDecodeTokens += decode
		summary.SpeedupAnalysis.APITime += apiTime
		runStart = earlierTime(runStart, acc.first)
		runEnd = laterTime(runEnd, acc.last)
	}
	summary.TotalTokens = summary.TotalPrefillTokens + summary.TotalDecodeTokens
	wallTime := secondsBetween(runStart, runEnd)
	apiTime := summary.SpeedupAnalysis.APITime
	speedup := 0.0
	if wallTime > 0 {
		speedup = apiTime / wallTime
	} else if apiTime > 0 {
		wallTime = apiTime
		speedup = 1
	}
	summary.SpeedupAnalysis = RunLoggerSpeedupAnalysis{
		APITime:                roundFloat(apiTime, 2),
		WallTime:               roundFloat(wallTime, 2),
		Speedup:                roundFloat(speedup, 2),
		ObservedOverlapSpeedup: roundFloat(speedup, 2),
		TotalKVCacheHitRatio:   roundFloat(ratio(summary.TotalCachedTokens, summary.TotalPrefillTokens), 4),
		CriticalPathTime:       roundFloat(criticalPathSeconds(graph, accumulators), 2),
		StreamingSpeedup:       roundFloat(speedup, 2),
		Timeline:               buildRunLoggerTimeline(order, accumulators, runStart, runEnd),
	}
	if len(selections) > 0 {
		summary.PaperAlignment = buildRunLoggerPaperAlignment(summary, selections[0])
	}
	return RunLogger{Summary: summary}
}

func RunLoggerArtifactPath() string {
	return filepath.ToSlash(filepath.Join(".pipeline-workspace", "streamma-routing", RunLoggerFileName))
}

func RunLoggerRunArtifactPath(runID string) string {
	return filepath.ToSlash(filepath.Join(".pipeline-workspace", "streamma-routing", "runs", SafeArtifactID(runID), RunLoggerFileName))
}

func buildRunLoggerPaperAlignment(summary RunLoggerSummary, selection GraphSelectionEvidence) *RunLoggerPaperAlignment {
	actual := map[string]int{}
	deviation := map[string]int{}
	for agentID, agent := range summary.Agents {
		actual[agentID] = agent.Segments
		if selection.ExpectedSteps > 0 {
			deviation[agentID] = agent.Segments - selection.ExpectedSteps
		}
	}
	return &RunLoggerPaperAlignment{
		TopologyShape:          selection.SelectedTopology,
		ExpectedAgents:         selection.ExpectedAgents,
		ExpectedSteps:          selection.ExpectedSteps,
		ActualSegmentsByAgent:  actual,
		StepDeviation:          deviation,
		CriticalPathAgentCount: selection.CriticalPath.CriticalPathAgentCount,
	}
}

func SafeArtifactID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "._-")
	if out == "" {
		return "unknown"
	}
	return out
}

func (a *runLoggerAgentAccumulator) observe(t time.Time) {
	if t.IsZero() {
		return
	}
	a.first = earlierTime(a.first, t)
	a.last = laterTime(a.last, t)
}

func (a *runLoggerAgentAccumulator) tokenTotals() (prefill, cached, decode int) {
	if a.hasSpan {
		return a.spanPrefill, a.spanCached, a.spanDecode
	}
	return a.eventPrefill, a.eventCached, a.eventDecode
}

func (a *runLoggerAgentAccumulator) apiSeconds() float64 {
	if len(a.intervals) > 0 {
		total := 0.0
		for _, interval := range a.intervals {
			total += secondsBetween(interval.start, interval.end)
		}
		return total
	}
	return secondsBetween(a.first, a.last)
}

func buildRunLoggerTimeline(order []string, accumulators map[string]*runLoggerAgentAccumulator, start, end time.Time) []string {
	const width = 50
	lines := []string{"[Timeline]"}
	wall := secondsBetween(start, end)
	for _, agentID := range order {
		acc := accumulators[agentID]
		cells := []rune(strings.Repeat("░", width))
		intervals := acc.intervals
		if len(intervals) == 0 && !acc.first.IsZero() {
			intervals = []runLoggerInterval{{start: acc.first, end: acc.last}}
		}
		for _, interval := range intervals {
			fillTimelineCells(cells, start, wall, interval)
		}
		lines = append(lines, "  Agent "+agentID+":"+string(cells)+" "+formatSeconds1(acc.apiSeconds()))
	}
	lines = append(lines, "            "+strings.Repeat("─", width))
	lines = append(lines, "            0.0s"+timelineEndLabel(width, wall))
	lines = append(lines, "")
	lines = append(lines, "  Legend: █ = processing, ░ = idle")
	return lines
}

func fillTimelineCells(cells []rune, runStart time.Time, wall float64, interval runLoggerInterval) {
	if len(cells) == 0 {
		return
	}
	startIndex := 0
	endIndex := 1
	if wall > 0 && !runStart.IsZero() {
		startIndex = int(math.Floor(secondsBetween(runStart, interval.start) / wall * float64(len(cells))))
		endIndex = int(math.Ceil(secondsBetween(runStart, interval.end) / wall * float64(len(cells))))
	}
	startIndex = clampInt(startIndex, 0, len(cells)-1)
	endIndex = clampInt(endIndex, startIndex+1, len(cells))
	for i := startIndex; i < endIndex; i++ {
		cells[i] = '█'
	}
}

func timelineEndLabel(width int, wall float64) string {
	label := formatSeconds1(wall)
	spaces := maxInt(1, width-len("0.0s")-len(label))
	return strings.Repeat(" ", spaces) + label
}

func graphAgentIndex(graph GraphSpec, agentID string) int {
	for i, agent := range graph.Agents {
		if agent.ID == agentID {
			return i
		}
	}
	return len(graph.Agents) + 1
}

func criticalPathSeconds(graph GraphSpec, accumulators map[string]*runLoggerAgentAccumulator) float64 {
	compiled, err := compileGraph(graph)
	if err != nil {
		return 0
	}
	order := topologicalAgentOrder(compiled)
	longest := map[string]float64{}
	best := 0.0
	for _, id := range order {
		own := 0.0
		if acc := accumulators[id]; acc != nil {
			own = acc.apiSeconds()
		}
		total := own
		for _, pred := range compiled.predecessors[id] {
			if candidate := longest[pred] + own; candidate > total {
				total = candidate
			}
		}
		longest[id] = total
		if total > best {
			best = total
		}
	}
	return best
}

func secondsBetween(start, end time.Time) float64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Seconds()
}

func earlierTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func laterTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || candidate.After(current) {
		return candidate
	}
	return current
}

func ratio(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func roundFloat(value float64, digits int) float64 {
	scale := math.Pow10(maxInt(0, digits))
	return math.Round(value*scale) / scale
}

func formatSeconds1(seconds float64) string {
	return strconvFormatFloat(roundFloat(seconds, 1)) + "s"
}

func strconvFormatFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', 1, 64)
	return text
}
