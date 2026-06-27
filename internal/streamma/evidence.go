package streamma

import (
	"math"
	"strings"
	"time"
)

const (
	EvidenceFileName = "evidence.jsonl"

	EvidenceLevelMeasured  = "measured"
	EvidenceLevelPrior     = "prior"
	EvidenceLevelHeuristic = "heuristic"

	StepProfileCaseStreamIa   = "I.a_stream_advantage"
	StepProfileCaseStreamIb   = "I.b_stream_advantage"
	StepProfileCaseSerialIIa  = "II.a_serial_advantage"
	StepProfileCaseSerialIIb  = "II.b_serial_advantage"
	StepProfileCaseSingleIIIa = "III.a_single_advantage"
	StepProfileCaseSingleIIIb = "III.b_single_advantage"
	StepProfileCaseUnknown    = "unknown"
)

type TaskDecompositionEvidence struct {
	DecomposableScore float64 `json:"decomposable_score"`
	TaskFamily        string  `json:"task_family,omitempty"`
	EvidenceSource    string  `json:"evidence_source,omitempty"`
}

type StepCorrectnessEvidence struct {
	PHead             float64 `json:"p_head"`
	PTail             float64 `json:"p_tail"`
	PAvg              float64 `json:"p_avg"`
	Threshold         float64 `json:"threshold"`
	ProfileCase       string  `json:"profile_case"`
	Advantage         string  `json:"advantage"`
	EvidenceSource    string  `json:"evidence_source,omitempty"`
	StepProfileSource string  `json:"step_profile_source,omitempty"`
	KnownSteps        int     `json:"known_steps,omitempty"`
}

type ScalingEvidence struct {
	TopologyShape          string  `json:"topology_shape,omitempty"`
	AgentCount             int     `json:"agent_count,omitempty"`
	StepCount              int     `json:"step_count,omitempty"`
	ObservedOverlapSpeedup float64 `json:"observed_overlap_speedup,omitempty"`
	WallTime               float64 `json:"wall_time,omitempty"`
	APITime                float64 `json:"api_time,omitempty"`
	AccuracyProxy          float64 `json:"accuracy_proxy,omitempty"`
	StepDeviation          int     `json:"step_deviation,omitempty"`
	EvidenceSource         string  `json:"evidence_source,omitempty"`
}

type CriticalPathEvidence struct {
	AgentCount             int     `json:"agent_count,omitempty"`
	CriticalPathAgentCount int     `json:"critical_path_agent_count,omitempty"`
	CriticalPathTime       float64 `json:"critical_path_time,omitempty"`
	EvidenceSource         string  `json:"evidence_source,omitempty"`
}

type CacheEvidence struct {
	KVCacheHitRatio float64 `json:"kv_cache_hit_ratio,omitempty"`
	CachedTokens    int     `json:"cached_tokens,omitempty"`
	PrefillTokens   int     `json:"prefill_tokens,omitempty"`
	DecodeTokens    int     `json:"decode_tokens,omitempty"`
	CacheSignal     string  `json:"cache_signal,omitempty"`
	EvidenceSource  string  `json:"evidence_source,omitempty"`
}

type GraphSelectionEvidence struct {
	EvidenceLevel     string                    `json:"evidence_level"`
	SelectedTopology  string                    `json:"selected_topology"`
	ForcedTopology    bool                      `json:"forced_topology,omitempty"`
	ExpectedAgents    int                       `json:"expected_agents,omitempty"`
	ExpectedSteps     int                       `json:"expected_steps,omitempty"`
	RecommendedAgents int                       `json:"recommended_agents,omitempty"`
	RecommendedSteps  int                       `json:"recommended_steps,omitempty"`
	TaskDecomposition TaskDecompositionEvidence `json:"task_decomposition"`
	StepCorrectness   StepCorrectnessEvidence   `json:"step_correctness"`
	Scaling           *ScalingEvidence          `json:"scaling,omitempty"`
	CriticalPath      CriticalPathEvidence      `json:"critical_path"`
	Cache             CacheEvidence             `json:"cache"`
	Rationale         []string                  `json:"rationale,omitempty"`
	Warnings          []string                  `json:"warnings,omitempty"`
}

type GraphRouterInput struct {
	Understanding     TaskUnderstanding
	RequestedTopology string
	TopologyForced    bool
	AgentCount        int
	StepCount         int
	StepCorrectness   *StepCorrectnessEvidence
	ScalingHistory    []ScalingEvidence
	Cache             CacheEvidence
}

type RoutingEvidenceRecord struct {
	Version                string                 `json:"version"`
	RunID                  string                 `json:"run_id"`
	CreatedAt              time.Time              `json:"created_at"`
	Task                   string                 `json:"task"`
	GraphSelectionEvidence GraphSelectionEvidence `json:"graph_selection_evidence"`
	ActualSegmentsByAgent  map[string]int         `json:"actual_segments_by_agent,omitempty"`
	StepDeviation          map[string]int         `json:"step_deviation,omitempty"`
	Scaling                ScalingEvidence        `json:"scaling"`
	CriticalPath           CriticalPathEvidence   `json:"critical_path"`
	Cache                  CacheEvidence          `json:"cache"`
}

func SelectGraphTopology(input GraphRouterInput) GraphSelectionEvidence {
	understanding := input.Understanding
	if input.AgentCount <= 0 {
		input.AgentCount = firstPositive(understanding.AgentCountHint, len(strings.Fields(understanding.Task)), 1)
	}
	if input.StepCount <= 0 {
		input.StepCount = firstPositive(understanding.StepCountHint, 1)
	}
	step := priorStepCorrectness(understanding)
	if input.StepCorrectness != nil {
		step = *input.StepCorrectness
		if step.EvidenceSource == "" {
			step.EvidenceSource = EvidenceLevelMeasured
		}
		if step.StepProfileSource == "" {
			step.StepProfileSource = "judge"
		}
	}
	decomposition := TaskDecompositionEvidence{
		DecomposableScore: boolScore(understanding.Decomposable),
		TaskFamily:        strings.TrimSpace(understanding.TaskType),
		EvidenceSource:    EvidenceLevelPrior,
	}
	selected := NormalizeTopologyShape(input.RequestedTopology)
	evidenceLevel := EvidenceLevelPrior
	rationale := []string{}
	warnings := []string{}
	if input.TopologyForced && selected != TopologyShapeAdaptive {
		rationale = append(rationale, "user-specified topology overrides evidence-aware router")
	} else if !understanding.Decomposable || understanding.RecommendedProtocol == PlanProtocolSingle {
		selected = TopologyShapeAdaptive
		evidenceLevel = EvidenceLevelHeuristic
		rationale = append(rationale, "task is not decomposable enough for paper Chain/Tree/Graph selection")
	} else if independentSubproblemTask(understanding.Task) {
		selected = TopologyShapeTree
		rationale = append(rationale, "task text suggests independent subproblems; tree reduces critical path")
	} else if shortcutOrCrossCheckTask(understanding.Task) {
		selected = TopologyShapeGraph
		rationale = append(rationale, "task text suggests cross-checking or shortcut paths; graph preserves chain plus root shortcuts")
	} else if step.Advantage == PlanProtocolStream && step.PHead > step.Threshold && step.PTail < step.Threshold {
		selected = TopologyShapeChain
		rationale = append(rationale, "head-strong/tail-weak evidence favors Stream on a linear chain")
	} else if selected == TopologyShapeAdaptive {
		selected = TopologyShapeChain
		evidenceLevel = EvidenceLevelHeuristic
		rationale = append(rationale, "no measured topology evidence; defaulting paper profile to chain")
	}
	if step.EvidenceSource == EvidenceLevelPrior {
		warnings = append(warnings, "step correctness uses task-family prior, not an online judge measurement")
		if evidenceLevel == EvidenceLevelPrior {
			evidenceLevel = EvidenceLevelHeuristic
		}
	} else if step.EvidenceSource == EvidenceLevelMeasured {
		evidenceLevel = EvidenceLevelMeasured
	}
	bestScaling, ok := SelectBestScaling(input.ScalingHistory)
	if ok {
		rationale = append(rationale, "historical A/S sweep evidence is available")
		if !input.TopologyForced && strings.TrimSpace(bestScaling.TopologyShape) != "" {
			selected = NormalizeTopologyShape(bestScaling.TopologyShape)
			evidenceLevel = EvidenceLevelMeasured
		}
	}
	cache := input.Cache
	if cache.CacheSignal == "" {
		cache.CacheSignal = "unknown"
	}
	if cache.CacheSignal == "present" && cache.KVCacheHitRatio < 0.2 && cache.PrefillTokens > cache.DecodeTokens*3 && !input.TopologyForced {
		selected = TopologyShapeTree
		rationale = append(rationale, "low cache hit rate with prefill-heavy usage favors shorter critical path")
	}
	if evidenceLevel == "" {
		evidenceLevel = EvidenceLevelHeuristic
	}
	return GraphSelectionEvidence{
		EvidenceLevel:     evidenceLevel,
		SelectedTopology:  selected,
		ForcedTopology:    input.TopologyForced,
		ExpectedAgents:    input.AgentCount,
		ExpectedSteps:     input.StepCount,
		RecommendedAgents: input.AgentCount,
		RecommendedSteps:  input.StepCount,
		TaskDecomposition: decomposition,
		StepCorrectness:   step,
		Scaling:           optionalScaling(bestScaling, ok),
		CriticalPath: CriticalPathEvidence{
			AgentCount:             input.AgentCount,
			CriticalPathAgentCount: criticalPathCountForShape(selected, input.AgentCount),
			EvidenceSource:         EvidenceLevelHeuristic,
		},
		Cache:     cache,
		Rationale: rationale,
		Warnings:  warnings,
	}
}

func StepCorrectnessFromLabels(labels []string, threshold float64) StepCorrectnessEvidence {
	var values []float64
	for _, label := range labels {
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "correct", "true", "1", "yes", "pass":
			values = append(values, 1)
		case "incorrect", "false", "0", "no", "fail":
			values = append(values, 0)
		default:
			values = append(values, math.NaN())
		}
	}
	return StepCorrectnessFromValues(values, threshold)
}

func StepCorrectnessFromValues(values []float64, threshold float64) StepCorrectnessEvidence {
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.5
	}
	var known int
	var sum, headSum, tailSum, headWeight, tailWeight float64
	s := len(values)
	for i, value := range values {
		if math.IsNaN(value) {
			continue
		}
		known++
		sum += value
		headW := float64(s - i)
		headSum += headW * value
		headWeight += headW
		if i > 0 {
			tailW := float64(i)
			tailSum += tailW * value
			tailWeight += tailW
		}
	}
	out := StepCorrectnessEvidence{
		Threshold:         threshold,
		ProfileCase:       StepProfileCaseUnknown,
		Advantage:         "unknown",
		EvidenceSource:    EvidenceLevelMeasured,
		StepProfileSource: "judge",
		KnownSteps:        known,
	}
	if known == 0 {
		out.EvidenceSource = EvidenceLevelHeuristic
		out.StepProfileSource = "unknown"
		return out
	}
	out.PAvg = roundFloat(sum/float64(known), 4)
	if headWeight > 0 {
		out.PHead = roundFloat(headSum/headWeight, 4)
	}
	if tailWeight > 0 {
		out.PTail = roundFloat(tailSum/tailWeight, 4)
	}
	out.ProfileCase, out.Advantage = classifyStepCorrectness(out.PHead, out.PTail, out.PAvg, threshold)
	return out
}

func SelectBestScaling(records []ScalingEvidence) (ScalingEvidence, bool) {
	if len(records) == 0 {
		return ScalingEvidence{}, false
	}
	best := records[0]
	for _, record := range records[1:] {
		if record.ObservedOverlapSpeedup > best.ObservedOverlapSpeedup {
			best = record
			continue
		}
		if record.ObservedOverlapSpeedup == best.ObservedOverlapSpeedup && absInt(record.StepDeviation) < absInt(best.StepDeviation) {
			best = record
		}
	}
	return best, true
}

func BuildRoutingEvidenceRecord(task string, decision StrategyDecision, logger RunLogger) RoutingEvidenceRecord {
	segments := map[string]int{}
	deviation := map[string]int{}
	expected := 0
	if decision.GraphSelectionEvidence != nil {
		expected = decision.GraphSelectionEvidence.ExpectedSteps
	}
	for agentID, summary := range logger.Summary.Agents {
		segments[agentID] = summary.Segments
		if expected > 0 {
			deviation[agentID] = summary.Segments - expected
		}
	}
	selection := GraphSelectionEvidence{}
	if decision.GraphSelectionEvidence != nil {
		selection = *decision.GraphSelectionEvidence
	}
	critical := CriticalPathEvidence{
		AgentCount:             logger.Summary.AgentCount,
		CriticalPathAgentCount: selection.CriticalPath.CriticalPathAgentCount,
		CriticalPathTime:       logger.Summary.SpeedupAnalysis.CriticalPathTime,
		EvidenceSource:         EvidenceLevelMeasured,
	}
	cache := CacheEvidence{
		KVCacheHitRatio: logger.Summary.SpeedupAnalysis.TotalKVCacheHitRatio,
		CachedTokens:    logger.Summary.TotalCachedTokens,
		PrefillTokens:   logger.Summary.TotalPrefillTokens,
		DecodeTokens:    logger.Summary.TotalDecodeTokens,
		CacheSignal:     cacheSignalForTotals(logger.Summary.TotalCachedTokens, logger.Summary.TotalPrefillTokens),
		EvidenceSource:  EvidenceLevelMeasured,
	}
	scaling := ScalingEvidence{
		TopologyShape:          selection.SelectedTopology,
		AgentCount:             selection.ExpectedAgents,
		StepCount:              selection.ExpectedSteps,
		ObservedOverlapSpeedup: logger.Summary.SpeedupAnalysis.ObservedOverlapSpeedup,
		WallTime:               logger.Summary.SpeedupAnalysis.WallTime,
		APITime:                logger.Summary.SpeedupAnalysis.APITime,
		EvidenceSource:         EvidenceLevelMeasured,
	}
	for _, delta := range deviation {
		scaling.StepDeviation += absInt(delta)
	}
	return RoutingEvidenceRecord{
		Version:                "1.0",
		RunID:                  logger.Summary.RunID,
		CreatedAt:              time.Now().UTC(),
		Task:                   strings.TrimSpace(task),
		GraphSelectionEvidence: selection,
		ActualSegmentsByAgent:  segments,
		StepDeviation:          deviation,
		Scaling:                scaling,
		CriticalPath:           critical,
		Cache:                  cache,
	}
}

func priorStepCorrectness(understanding TaskUnderstanding) StepCorrectnessEvidence {
	out := StepCorrectnessEvidence{
		Threshold:         0.5,
		ProfileCase:       StepProfileCaseUnknown,
		Advantage:         PlanProtocolStream,
		EvidenceSource:    EvidenceLevelPrior,
		StepProfileSource: "task_family_prior",
	}
	switch understanding.ExpectedStepProfile {
	case StepProfileNonDecomposable, StepProfileSingleStep:
		out.PHead = 0
		out.PTail = 0
		out.PAvg = 0
		out.Advantage = PlanProtocolSingle
	case StepProfileHeadStrong:
		out.PHead = 0.75
		out.PTail = 0.25
		out.PAvg = 0.5
		out.ProfileCase = StepProfileCaseStreamIb
	default:
		out.PHead = 0.5
		out.PTail = 0.5
		out.PAvg = 0.5
	}
	return out
}

func classifyStepCorrectness(pHead, pTail, pAvg, threshold float64) (string, string) {
	switch {
	case pHead > threshold && pTail < threshold && pAvg >= threshold:
		return StepProfileCaseStreamIa, PlanProtocolStream
	case pHead > threshold && pTail < threshold:
		return StepProfileCaseStreamIb, PlanProtocolStream
	case pHead > threshold && pTail > threshold:
		return StepProfileCaseSerialIIa, PlanProtocolSerial
	case pAvg > threshold && pHead < threshold:
		return StepProfileCaseSerialIIb, PlanProtocolSerial
	case pHead < threshold && pTail < threshold:
		return StepProfileCaseSingleIIIa, PlanProtocolSingle
	case pAvg < threshold && pTail > threshold:
		return StepProfileCaseSingleIIIb, PlanProtocolSingle
	default:
		return StepProfileCaseUnknown, "unknown"
	}
}

func independentSubproblemTask(task string) bool {
	return containsAny(strings.ToLower(task), []string{"independent", "parallel", "multiple subproblems", "compare", "并行", "独立", "多个子问题", "比较"})
}

func shortcutOrCrossCheckTask(task string) bool {
	return containsAny(strings.ToLower(task), []string{"cross-check", "cross check", "shortcut", "graph", "交叉检查", "互相校验", "捷径", "图"})
}

func criticalPathCountForShape(shape string, agents int) int {
	if agents <= 0 {
		return 0
	}
	switch NormalizeTopologyShape(shape) {
	case TopologyShapeTree:
		if agents == 1 {
			return 1
		}
		return 2
	case TopologyShapeChain, TopologyShapeGraph:
		return agents
	default:
		return 0
	}
}

func optionalScaling(value ScalingEvidence, ok bool) *ScalingEvidence {
	if !ok {
		return nil
	}
	return &value
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func cacheSignalForTotals(cached, prefill int) string {
	if cached > 0 {
		return "present"
	}
	if prefill > 0 {
		return "unknown"
	}
	return "unknown"
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
