package tokentracer

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type Timeline struct {
	StartTime       string        `json:"start_time,omitempty"`
	EndTime         string        `json:"end_time,omitempty"`
	GeneratedAt     string        `json:"generated_at,omitempty"`
	DurationMS      int64         `json:"duration_ms"`
	MaxConcurrency  int           `json:"max_concurrency"`
	OverlapMS       int64         `json:"overlap_ms"`
	TokenTotal      Usage         `json:"token_total"`
	TokenGrandTotal int           `json:"token_grand_total"`
	Error           string        `json:"error,omitempty"`
	Rows            []TimelineRow `json:"rows"`
}

type TimelineRow struct {
	ID              string           `json:"id"`
	Kind            string           `json:"kind"`
	StageID         string           `json:"stage_id,omitempty"`
	StageName       string           `json:"stage_name,omitempty"`
	AgentID         string           `json:"agent_id,omitempty"`
	Name            string           `json:"name"`
	DisplayName     string           `json:"display_name,omitempty"`
	Role            string           `json:"role,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	InvocationIndex int              `json:"invocation_index,omitempty"`
	StartTime       string           `json:"start_time"`
	EndTime         string           `json:"end_time"`
	DurationMS      int64            `json:"duration_ms"`
	Status          string           `json:"status"`
	Error           string           `json:"error,omitempty"`
	Usage           Usage            `json:"usage"`
	TokenGrandTotal int              `json:"token_grand_total"`
	TokenShare      float64          `json:"token_share"`
	Calls           int              `json:"calls"`
	Markers         []TimelineMarker `json:"markers,omitempty"`
}

type TimelineMarker struct {
	Type   string `json:"type"`
	Time   string `json:"time"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
	Usage  *Usage `json:"usage,omitempty"`
}

type timelineBuilder struct {
	pipeline      Pipeline
	events        []Event
	now           time.Time
	rows          map[string]*TimelineRow
	order         []string
	sessionRows   map[string]string
	agentRows     map[string]string
	stageNames    map[string]string
	stageErrors   map[string]string
	agentNames    map[string]string
	taskNames     map[string]string
	minTime       time.Time
	maxTime       time.Time
	totalUsage    Usage
	grandTotal    int
	timelineError string
}

func buildTimeline(pipeline Pipeline, events []Event, now time.Time) Timeline {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b := &timelineBuilder{
		pipeline:    pipeline,
		events:      events,
		now:         now,
		rows:        make(map[string]*TimelineRow),
		sessionRows: make(map[string]string),
		agentRows:   make(map[string]string),
		stageNames:  make(map[string]string),
		stageErrors: make(map[string]string),
		agentNames:  make(map[string]string),
		taskNames:   make(map[string]string),
		totalUsage:  pipeline.Total.Normalized(),
	}
	b.collectPipelineRows()
	b.collectEventRows()
	b.finishRows()
	return b.timeline()
}

func (b *timelineBuilder) collectPipelineRows() {
	for _, stage := range b.pipeline.Stages {
		if stage == nil {
			continue
		}
		b.stageNames[stage.ID] = stage.Name
		row := b.ensureRow(stageRowID(stage.ID), "stage", stage.ID, "", 0)
		row.Name = defaultName(stage.Name, stage.ID)
		row.StageName = row.Name
		row.StartTime = stage.StartTime
		row.EndTime = stage.EndTime
		row.Status = defaultStatus(stage.Status)
		row.Usage = stage.Subtotal.Normalized()
		row.Calls = stage.Calls
		for _, agent := range stage.Agents {
			if agent != nil {
				b.agentNames[agentKey(stage.ID, agent.ID)] = agent.Name
			}
		}
	}
}

func (b *timelineBuilder) collectEventRows() {
	for _, event := range b.events {
		eventTime, ok := parseRFC3339(event.Timestamp)
		if !ok {
			eventTime = b.now
		}
		data := event.Data
		stageID := stringValue(data, "stage_id")
		agentID := firstNonEmpty(stringValue(data, "agent_id"), stringValue(data, "producer_agent_id"))
		sessionID := stringValue(data, "session_id")
		invocation := intValue(data, "invocation_index")

		switch event.Type {
		case "stage_start":
			row := b.ensureRow(stageRowID(stageID), "stage", stageID, "", 0)
			row.Name = defaultName(stringValue(data, "name"), stageID)
			row.StageName = row.Name
			row.StartTime = event.Timestamp
			row.Status = "live"
		case "stage_end":
			row := b.ensureRow(stageRowID(stageID), "stage", stageID, "", 0)
			row.EndTime = event.Timestamp
			row.Status = defaultStatus(stringValue(data, "status"))
			row.Error = stringValue(data, "error")
			if row.Error != "" {
				b.stageErrors[stageID] = row.Error
				b.setTimelineError(row.Error)
				b.addMarker(row, eventTime, "failure", "failed", row.Error, "failed", nil)
			}
		case "agent_start":
			if agentID == "" {
				agentID = "assistant"
			}
			row := b.ensureRow(agentRowID(stageID, agentID, "", 0), "agent", stageID, agentID, 0)
			row.Name = defaultName(stringValue(data, "name"), agentID)
			row.DisplayName = row.Name
			row.StartTime = event.Timestamp
			row.Status = "live"
			b.rememberAgent(stageID, agentID, row.ID)
		case "agent_end":
			if agentID == "" {
				agentID = "assistant"
			}
			row := b.ensureRow(agentRowID(stageID, agentID, "", 0), "agent", stageID, agentID, 0)
			row.EndTime = event.Timestamp
			row.Status = defaultStatus(stringValue(data, "status"))
			row.Error = stringValue(data, "error")
			if row.Error != "" {
				b.setTimelineError(row.Error)
				b.addMarker(row, eventTime, "failure", "failed", row.Error, "failed", nil)
			}
		case "streamma.subagent_start":
			row := b.ensureRow(agentRowID(stageID, agentID, sessionID, invocation), "agent", stageID, agentID, invocation)
			row.Role = stringValue(data, "role")
			row.SessionID = sessionID
			row.Name = defaultName(agentID, "agent")
			row.DisplayName = displayAgentName(row.Name, row.Role, b.taskNames[sessionID])
			row.StartTime = event.Timestamp
			row.Status = "live"
			b.rememberSession(sessionID, row.ID)
			b.rememberAgent(stageID, agentID, row.ID)
			b.addMarker(row, eventTime, "start", "start", row.Role, "live", nil)
		case "streamma.subagent_end":
			row := b.findRow(stageID, agentID, sessionID, invocation)
			if row == nil {
				row = b.ensureRow(agentRowID(stageID, agentID, sessionID, invocation), "agent", stageID, agentID, invocation)
			}
			row.EndTime = event.Timestamp
			if row.Status == "" || row.Status == "live" {
				row.Status = "completed"
			}
			b.rememberSession(sessionID, row.ID)
			b.rememberAgent(stageID, agentID, row.ID)
			b.addMarker(row, eventTime, "end", "end", row.Role, row.Status, nil)
		case "subagent_task_start":
			row := b.ensureSubagentTaskRow(stageID, agentID, sessionID, invocation)
			b.applySubagentTaskMetadata(row, data, sessionID)
			b.setRowStartTime(row, firstNonEmpty(stringValue(data, "started_at"), event.Timestamp))
		case "subagent_task_end":
			row := b.ensureSubagentTaskRow(stageID, agentID, sessionID, invocation)
			b.applySubagentTaskMetadata(row, data, sessionID)
			if row.StartTime == "" {
				b.setRowStartTime(row, firstNonEmpty(stringValue(data, "started_at"), stringValue(data, "finished_at"), event.Timestamp))
			}
			row.EndTime = firstNonEmpty(stringValue(data, "finished_at"), event.Timestamp)
			row.Status = defaultStatus(stringValue(data, "status"))
			row.Error = stringValue(data, "error")
			if usage := usageValue(data["usage"]).Normalized(); !usage.Empty() && row.Usage.Empty() {
				row.Usage = usage
			}
			b.applySubagentUsedTokens(row, intValue(data, "used_tokens"))
			if row.Error != "" || row.Status == "failed" {
				if row.Error == "" {
					row.Error = "subagent failed"
				}
				b.setTimelineError(row.Error)
				b.addMarker(row, eventTime, "failure", "failed", row.Error, "failed", nil)
			}
		case "api_call":
			usage := usageValue(data["usage"]).Normalized()
			row := b.findRow(stageID, agentID, sessionID, invocation)
			if row == nil {
				row = b.ensureRow(agentRowID(stageID, agentID, sessionID, invocation), "agent", stageID, agentID, invocation)
				row.SessionID = sessionID
				row.Role = stringValue(data, "role")
				if row.StartTime == "" {
					row.StartTime = event.Timestamp
				}
				b.rememberAgent(stageID, agentID, row.ID)
			}
			row.Usage = row.Usage.Add(usage)
			row.Calls++
			b.addMarker(row, eventTime, "api_call", "api", usageSummary(usage), "usage", &usage)
		case "streamma.agent.step.committed":
			row := b.findRow(stageID, agentID, sessionID, invocation)
			if row == nil && agentID != "" {
				row = b.ensureRow(agentRowID(stageID, agentID, sessionID, invocation), "agent", stageID, agentID, invocation)
			}
			if row != nil {
				b.addMarker(row, eventTime, "step", "step", stringValue(data, "step_id"), "step", nil)
			}
		case "streamma.run.failed":
			errText := stringValue(data, "error")
			b.setTimelineError(errText)
			row := b.ensureRow(stageRowID(stageID), "stage", stageID, "", 0)
			row.Status = "failed"
			row.Error = errText
			if row.EndTime == "" {
				row.EndTime = event.Timestamp
			}
			b.addMarker(row, eventTime, "failure", "failed", errText, "failed", nil)
		}
	}
}

func (b *timelineBuilder) ensureSubagentTaskRow(stageID, agentID, sessionID string, invocation int) *TimelineRow {
	row := b.rowForSession(sessionID)
	if row == nil {
		rowAgentID := defaultID(agentID, "subagent")
		row = b.ensureRow(agentRowID(stageID, rowAgentID, sessionID, invocation), "agent", stageID, rowAgentID, invocation)
	}
	if sessionID != "" {
		row.SessionID = sessionID
		b.rememberSession(sessionID, row.ID)
	}
	b.rememberAgent(stageID, row.AgentID, row.ID)
	return row
}

func (b *timelineBuilder) applySubagentTaskMetadata(row *TimelineRow, data map[string]any, sessionID string) {
	if row == nil {
		return
	}
	taskName := firstNonEmpty(stringValue(data, "name"), stringValue(data, "description"))
	if taskName != "" && sessionID != "" {
		b.taskNames[sessionID] = taskName
	}
	taskName = firstNonEmpty(taskName, b.taskNames[sessionID])
	if row.AgentID == "subagent" || row.AgentID == "" {
		row.Name = defaultName(taskName, "subagent")
		row.DisplayName = row.Name
		return
	}
	row.DisplayName = displayAgentName(row.Name, row.Role, taskName)
}

func (b *timelineBuilder) setRowStartTime(row *TimelineRow, value string) {
	if row == nil || strings.TrimSpace(value) == "" {
		return
	}
	if row.StartTime == "" {
		row.StartTime = value
		return
	}
	current, currentOK := parseRFC3339(row.StartTime)
	next, nextOK := parseRFC3339(value)
	if currentOK && nextOK && next.Before(current) {
		row.StartTime = value
	}
}

func (b *timelineBuilder) applySubagentUsedTokens(row *TimelineRow, usedTokens int) {
	if row == nil || usedTokens <= 0 || !row.Usage.Empty() {
		return
	}
	row.Usage = Usage{Input: usedTokens}.Normalized()
}

func (b *timelineBuilder) finishRows() {
	stageUsageTotal := Usage{}
	fallbackUsageTotal := Usage{}
	for _, id := range b.order {
		row := b.rows[id]
		if row == nil {
			continue
		}
		row.Status = defaultStatus(row.Status)
		if row.StartTime == "" {
			row.StartTime = b.pipeline.StartTime
		}
		if row.StartTime == "" {
			row.StartTime = b.now.Format(time.RFC3339Nano)
		}
		if row.EndTime == "" {
			if row.Status == "completed" || row.Status == "failed" {
				row.EndTime = maxKnownTime(b.maxTime, b.now).Format(time.RFC3339Nano)
			} else {
				row.EndTime = b.now.Format(time.RFC3339Nano)
			}
		}
		if start, ok := parseRFC3339(row.StartTime); ok {
			b.noteTime(start)
			if end, ok := parseRFC3339(row.EndTime); ok {
				if end.Before(start) {
					end = start
					row.EndTime = end.Format(time.RFC3339Nano)
				}
				row.DurationMS = end.Sub(start).Milliseconds()
				b.noteTime(end)
			}
		}
		row.Usage = row.Usage.Normalized()
		row.TokenGrandTotal = usageGrandTotal(row.Usage)
		if row.Kind == "stage" {
			stageUsageTotal = stageUsageTotal.Add(row.Usage)
		} else if row.Calls == 0 && !row.Usage.Empty() {
			fallbackUsageTotal = fallbackUsageTotal.Add(row.Usage)
		}
		sort.SliceStable(row.Markers, func(i, j int) bool {
			ti, _ := parseRFC3339(row.Markers[i].Time)
			tj, _ := parseRFC3339(row.Markers[j].Time)
			return ti.Before(tj)
		})
	}
	if b.totalUsage.Empty() && !stageUsageTotal.Empty() {
		b.totalUsage = stageUsageTotal.Normalized()
	}
	if !b.totalUsage.Empty() && !fallbackUsageTotal.Empty() {
		b.totalUsage = b.totalUsage.Add(fallbackUsageTotal)
	}
	b.grandTotal = usageGrandTotal(b.totalUsage)
	if b.grandTotal == 0 {
		for _, id := range b.order {
			b.grandTotal += b.rows[id].TokenGrandTotal
		}
	}
	for _, id := range b.order {
		row := b.rows[id]
		if b.grandTotal > 0 {
			row.TokenShare = math.Round((float64(row.TokenGrandTotal)/float64(b.grandTotal))*1000) / 10
		}
	}
}

func (b *timelineBuilder) timeline() Timeline {
	rows := make([]TimelineRow, 0, len(b.order))
	for _, id := range b.order {
		if row := b.rows[id]; row != nil {
			rows = append(rows, *row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind == "stage"
		}
		ti, _ := parseRFC3339(rows[i].StartTime)
		tj, _ := parseRFC3339(rows[j].StartTime)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return rows[i].Name < rows[j].Name
	})
	start := b.minTime
	if start.IsZero() {
		start = b.now
	}
	end := b.maxTime
	if end.IsZero() || end.Before(start) {
		end = b.now
	}
	if end.Before(start) {
		end = start
	}
	maxConcurrency, overlapMS := agentConcurrency(rows)
	return Timeline{
		StartTime:       start.Format(time.RFC3339Nano),
		EndTime:         end.Format(time.RFC3339Nano),
		GeneratedAt:     b.now.Format(time.RFC3339Nano),
		DurationMS:      end.Sub(start).Milliseconds(),
		MaxConcurrency:  maxConcurrency,
		OverlapMS:       overlapMS,
		TokenTotal:      b.totalUsage.Normalized(),
		TokenGrandTotal: b.grandTotal,
		Error:           b.timelineError,
		Rows:            rows,
	}
}

func (b *timelineBuilder) ensureRow(id, kind, stageID, agentID string, invocation int) *TimelineRow {
	if id == "" {
		id = fmt.Sprintf("%s:%s:%s:%d", kind, stageID, agentID, invocation)
	}
	if row := b.rows[id]; row != nil {
		return row
	}
	stageName := b.stageNames[stageID]
	name := defaultName(agentID, stageID)
	if kind == "stage" {
		name = defaultName(stageName, stageID)
	}
	row := &TimelineRow{
		ID:              id,
		Kind:            kind,
		StageID:         stageID,
		StageName:       stageName,
		AgentID:         agentID,
		Name:            name,
		DisplayName:     name,
		InvocationIndex: invocation,
		Status:          "live",
	}
	if agentName := b.agentNames[agentKey(stageID, agentID)]; agentName != "" && kind == "agent" {
		row.DisplayName = agentName
	}
	b.rows[id] = row
	b.order = append(b.order, id)
	return row
}

func (b *timelineBuilder) findRow(stageID, agentID, sessionID string, invocation int) *TimelineRow {
	if row := b.rowForSession(sessionID); row != nil {
		return row
	}
	if row := b.rowForAgent(stageID, agentID); row != nil {
		return row
	}
	if agentID == "" && sessionID == "" {
		return nil
	}
	if row := b.rows[agentRowID(stageID, agentID, sessionID, invocation)]; row != nil {
		return row
	}
	if invocation != 0 {
		if row := b.rows[agentRowID(stageID, agentID, sessionID, 0)]; row != nil {
			return row
		}
	}
	return nil
}

func (b *timelineBuilder) rowForSession(sessionID string) *TimelineRow {
	if sessionID == "" {
		return nil
	}
	if rowID := b.sessionRows[sessionID]; rowID != "" {
		return b.rows[rowID]
	}
	return nil
}

func (b *timelineBuilder) rememberSession(sessionID, rowID string) {
	if sessionID != "" && rowID != "" {
		b.sessionRows[sessionID] = rowID
	}
}

func (b *timelineBuilder) rowForAgent(stageID, agentID string) *TimelineRow {
	if stageID == "" || agentID == "" {
		return nil
	}
	if rowID := b.agentRows[agentKey(stageID, agentID)]; rowID != "" {
		return b.rows[rowID]
	}
	return nil
}

func (b *timelineBuilder) rememberAgent(stageID, agentID, rowID string) {
	if stageID != "" && agentID != "" && rowID != "" {
		b.agentRows[agentKey(stageID, agentID)] = rowID
	}
}

func (b *timelineBuilder) addMarker(row *TimelineRow, ts time.Time, markerType, label, detail, status string, usage *Usage) {
	if row == nil {
		return
	}
	b.noteTime(ts)
	marker := TimelineMarker{
		Type:   markerType,
		Time:   ts.Format(time.RFC3339Nano),
		Label:  label,
		Detail: detail,
		Status: status,
	}
	if usage != nil {
		u := usage.Normalized()
		marker.Usage = &u
	}
	row.Markers = append(row.Markers, marker)
}

func agentConcurrency(rows []TimelineRow) (int, int64) {
	type point struct {
		ts    time.Time
		delta int
	}
	points := make([]point, 0, len(rows)*2)
	for _, row := range rows {
		if row.Kind != "agent" || row.AgentID == "assistant" {
			continue
		}
		start, okStart := parseRFC3339(row.StartTime)
		end, okEnd := parseRFC3339(row.EndTime)
		if !okStart || !okEnd || !end.After(start) {
			continue
		}
		points = append(points, point{ts: start, delta: 1}, point{ts: end, delta: -1})
	}
	if len(points) == 0 {
		return 0, 0
	}
	sort.SliceStable(points, func(i, j int) bool {
		if !points[i].ts.Equal(points[j].ts) {
			return points[i].ts.Before(points[j].ts)
		}
		return points[i].delta < points[j].delta
	})
	active := 0
	maxActive := 0
	var overlapMS int64
	last := points[0].ts
	for _, p := range points {
		if p.ts.After(last) && active > 1 {
			overlapMS += p.ts.Sub(last).Milliseconds()
		}
		active += p.delta
		if active > maxActive {
			maxActive = active
		}
		last = p.ts
	}
	return maxActive, overlapMS
}

func (b *timelineBuilder) setTimelineError(errText string) {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		return
	}
	if b.timelineError == "" || (isWeakTimelineError(b.timelineError) && !isWeakTimelineError(errText)) {
		b.timelineError = errText
	}
}

func isWeakTimelineError(errText string) bool {
	lower := strings.ToLower(strings.TrimSpace(errText))
	return lower == "" ||
		strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "context cancelled") ||
		strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "subagent failed")
}

func (b *timelineBuilder) noteTime(ts time.Time) {
	if ts.IsZero() {
		return
	}
	if b.minTime.IsZero() || ts.Before(b.minTime) {
		b.minTime = ts
	}
	if b.maxTime.IsZero() || ts.After(b.maxTime) {
		b.maxTime = ts
	}
}

func stageRowID(stageID string) string {
	return "stage:" + defaultID(stageID, "stage")
}

func agentRowID(stageID, agentID, sessionID string, invocation int) string {
	agentID = defaultID(agentID, "agent")
	if sessionID != "" {
		return fmt.Sprintf("agent:%s:%s:%s", defaultID(stageID, "stage"), agentID, sessionID)
	}
	return fmt.Sprintf("agent:%s:%s:%d", defaultID(stageID, "stage"), agentID, invocation)
}

func displayAgentName(agentID, role, taskName string) string {
	agentID = strings.TrimSpace(agentID)
	role = strings.TrimSpace(role)
	taskName = strings.TrimSpace(taskName)
	base := defaultName(agentID, "agent")
	if role != "" && role != agentID {
		base += " / " + role
	}
	if taskName != "" && taskName != agentID {
		base += " / " + taskName
	}
	return base
}

func defaultStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "completed", "done", "success":
		return "completed"
	case "failed", "error", "cancelled", "canceled":
		return "failed"
	case "live", "running":
		return "live"
	default:
		return "live"
	}
}

func parseRFC3339(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func stringValue(data map[string]any, key string) string {
	if len(data) == 0 {
		return ""
	}
	switch value := data[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func intValue(data map[string]any, key string) int {
	if len(data) == 0 {
		return 0
	}
	switch value := data[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func usageValue(value any) Usage {
	switch usage := value.(type) {
	case Usage:
		return usage.Normalized()
	case *Usage:
		if usage == nil {
			return Usage{}
		}
		return usage.Normalized()
	case map[string]any:
		return Usage{
			Input:         intAny(usage["input"]),
			Output:        intAny(usage["output"]),
			CacheRead:     intAny(usage["cache_read"]),
			CacheCreation: intAny(usage["cache_creation"]),
		}.Normalized()
	default:
		return Usage{}
	}
}

func intAny(value any) int {
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func usageGrandTotal(usage Usage) int {
	usage = usage.Normalized()
	return usage.Input + usage.CacheRead + usage.CacheCreation + usage.Output
}

func usageSummary(usage Usage) string {
	usage = usage.Normalized()
	return fmt.Sprintf("input=%d cache=%d output=%d", usage.Input, usage.CacheRead, usage.Output)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxKnownTime(maxTime, fallback time.Time) time.Time {
	if maxTime.IsZero() {
		return fallback
	}
	return maxTime
}
