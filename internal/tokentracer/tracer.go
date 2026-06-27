package tokentracer

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"codex-agent-go/internal/model"
)

const maxEventHistory = 2000

type Usage struct {
	Input         int     `json:"input"`
	Output        int     `json:"output"`
	CacheRead     int     `json:"cache_read"`
	CacheCreation int     `json:"cache_creation"`
	TotalContext  int     `json:"total_context"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
}

func UsageFromModelUsage(usage model.Usage) Usage {
	cacheRead := maxInt(0, usage.CacheHitTokens())
	cacheCreation := directCacheCreationTokens(usage)
	output := maxInt(0, usage.CompletionTokenCount())
	prompt := usage.PromptTokenCount()
	if prompt == 0 && usage.TotalTokens != 0 {
		prompt = maxInt(0, usage.TotalTokens-output)
	}

	input := maxInt(0, prompt)
	if usage.InputTokens != 0 && (usage.CacheReadInputTokens != 0 || usage.CacheCreationInputTokens != 0) {
		input = maxInt(0, usage.InputTokens)
	} else if cacheRead > 0 {
		input = maxInt(0, prompt-cacheRead)
	}

	return Usage{
		Input:         input,
		Output:        output,
		CacheRead:     cacheRead,
		CacheCreation: cacheCreation,
	}.Normalized()
}

func directCacheCreationTokens(usage model.Usage) int {
	for _, value := range []int{
		usage.CacheCreationInputTokens,
		usage.PromptTokensDetails.CacheCreationTokens,
		usage.InputTokensDetails.CacheCreationTokens,
		usage.PromptTokensDetails.CacheCreationInputTokens,
		usage.InputTokensDetails.CacheCreationInputTokens,
	} {
		if value != 0 {
			return maxInt(0, value)
		}
	}
	return 0
}

func (u Usage) Normalized() Usage {
	u.Input = maxInt(0, u.Input)
	u.Output = maxInt(0, u.Output)
	u.CacheRead = maxInt(0, u.CacheRead)
	u.CacheCreation = maxInt(0, u.CacheCreation)
	u.TotalContext = u.Input + u.CacheRead + u.CacheCreation
	if u.TotalContext > 0 {
		u.CacheHitRate = math.Round((float64(u.CacheRead)/float64(u.TotalContext))*1000) / 10
	} else {
		u.CacheHitRate = 0
	}
	return u
}

func (u Usage) Add(other Usage) Usage {
	u = u.Normalized()
	other = other.Normalized()
	return Usage{
		Input:         u.Input + other.Input,
		Output:        u.Output + other.Output,
		CacheRead:     u.CacheRead + other.CacheRead,
		CacheCreation: u.CacheCreation + other.CacheCreation,
	}.Normalized()
}

func (u Usage) Delta(previous Usage) Usage {
	u = u.Normalized()
	previous = previous.Normalized()
	return Usage{
		Input:         maxInt(0, u.Input-previous.Input),
		Output:        maxInt(0, u.Output-previous.Output),
		CacheRead:     maxInt(0, u.CacheRead-previous.CacheRead),
		CacheCreation: maxInt(0, u.CacheCreation-previous.CacheCreation),
	}.Normalized()
}

func (u Usage) Empty() bool {
	u = u.Normalized()
	return u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheCreation == 0
}

type Agent struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Calls        int     `json:"calls"`
	CallsHistory []Usage `json:"calls_history"`
	Total        Usage   `json:"total"`
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	Status       string  `json:"status,omitempty"`
	LastEvent    string  `json:"last_event,omitempty"`
}

type Stage struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	StartTime string   `json:"start_time"`
	EndTime   string   `json:"end_time,omitempty"`
	Agents    []*Agent `json:"agents"`
	Subtotal  Usage    `json:"subtotal"`
	Calls     int      `json:"calls"`
	Status    string   `json:"status,omitempty"`
}

type Pipeline struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	StartTime string   `json:"start_time"`
	EndTime   string   `json:"end_time,omitempty"`
	Stages    []*Stage `json:"stages"`
	Total     Usage    `json:"total"`
	Calls     int      `json:"calls"`
	Status    string   `json:"status"`
}

type Event struct {
	Seq       int            `json:"seq"`
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

type Snapshot struct {
	Pipeline  Pipeline `json:"pipeline"`
	RunID     string   `json:"run_id"`
	SessionID string   `json:"session_id,omitempty"`
	Workspace string   `json:"workspace,omitempty"`
	ServerURL string   `json:"server_url,omitempty"`
	Timeline  Timeline `json:"timeline"`
	Events    []Event  `json:"events"`
}

type Tracer struct {
	mu          sync.RWMutex
	pipeline    Pipeline
	sessionID   string
	workspace   string
	serverURL   string
	stageIndex  map[string]*Stage
	agentIndex  map[string]*Agent
	events      []Event
	subscribers map[chan Event]struct{}
	turnSeq     int
	eventSeq    int
}

func New(name string) *Tracer {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "GoCode"
	}
	now := time.Now().UTC()
	runID := fmt.Sprintf("run-%s", now.Format("20060102-150405"))
	t := &Tracer{
		pipeline: Pipeline{
			ID:        runID,
			Name:      name,
			StartTime: now.Format(time.RFC3339Nano),
			Status:    "live",
		},
		stageIndex:  make(map[string]*Stage),
		agentIndex:  make(map[string]*Agent),
		subscribers: make(map[chan Event]struct{}),
	}
	t.publishLocked("pipeline_start", map[string]any{"id": runID, "name": name})
	return t
}

func (t *Tracer) SetSessionID(sessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = strings.TrimSpace(sessionID)
	t.publishLocked("session", map[string]any{"session_id": t.sessionID})
}

func (t *Tracer) SetWorkspace(workspace string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.workspace = strings.TrimSpace(workspace)
}

func (t *Tracer) SetServerURL(url string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.serverURL = strings.TrimSpace(url)
	t.publishLocked("server_start", map[string]any{"url": t.serverURL})
}

func (t *Tracer) ServerURL() string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.serverURL
}

func (t *Tracer) StartTurn(input, mode string) (string, string) {
	if t == nil {
		return "", ""
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "conversation"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turnSeq++
	stageID := fmt.Sprintf("turn-%d", t.turnSeq)
	stageName := fmt.Sprintf("Turn %d", t.turnSeq)
	if mode == "streamma" {
		stageName = fmt.Sprintf("StreamMA %d", t.turnSeq)
	}
	stage := t.ensureStageLocked(stageID, stageName)
	agent := t.ensureAgentLocked(stage, "assistant", "assistant")
	t.publishLocked("stage_start", map[string]any{"stage_id": stage.ID, "name": stage.Name, "mode": mode})
	t.publishLocked("agent_start", map[string]any{"stage_id": stage.ID, "agent_id": agent.ID, "name": agent.Name})
	t.publishLocked("turn_start", map[string]any{
		"stage_id":        stage.ID,
		"agent_id":        agent.ID,
		"mode":            mode,
		"input":           input,
		"input_bytes":     len([]byte(input)),
		"input_runes":     len([]rune(input)),
		"pipeline_run_id": t.pipeline.ID,
	})
	return stage.ID, agent.ID
}

func (t *Tracer) FinishTurn(stageID, agentID string, err error) {
	if t == nil || strings.TrimSpace(stageID) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	status := "completed"
	data := map[string]any{"stage_id": stageID, "agent_id": agentID, "status": status}
	if err != nil {
		status = "failed"
		data["status"] = status
		data["error"] = err.Error()
	}
	if agent := t.agentIndex[agentKey(stageID, agentID)]; agent != nil {
		agent.Status = status
	}
	if stage := t.stageIndex[stageID]; stage != nil {
		stage.Status = status
		stage.EndTime = time.Now().UTC().Format(time.RFC3339Nano)
	}
	t.publishLocked("agent_end", data)
	t.publishLocked("stage_end", data)
	t.publishLocked("turn_end", data)
}

func (t *Tracer) RecordAPICall(stageID, stageName, agentID, agentName, provider, modelName string, usage Usage, data map[string]any) {
	if t == nil {
		return
	}
	usage = usage.Normalized()
	if usage.Empty() {
		return
	}
	stageID = defaultID(stageID, "default")
	agentID = defaultID(agentID, "assistant")
	stageName = defaultName(stageName, stageID)
	agentName = defaultName(agentName, agentID)
	t.mu.Lock()
	defer t.mu.Unlock()
	stage := t.ensureStageLocked(stageID, stageName)
	agent := t.ensureAgentLocked(stage, agentID, agentName)
	agent.Calls++
	agent.CallsHistory = append(agent.CallsHistory, usage)
	agent.Total = agent.Total.Add(usage)
	agent.Provider = strings.TrimSpace(provider)
	agent.Model = strings.TrimSpace(modelName)
	agent.LastEvent = "api_call"
	stage.Calls++
	stage.Subtotal = stage.Subtotal.Add(usage)
	t.pipeline.Calls++
	t.pipeline.Total = t.pipeline.Total.Add(usage)

	payload := map[string]any{
		"stage_id": stage.ID,
		"agent_id": agent.ID,
		"provider": strings.TrimSpace(provider),
		"model":    strings.TrimSpace(modelName),
		"usage":    usage,
	}
	for key, value := range data {
		payload[key] = value
	}
	t.publishLocked("api_call", payload)
}

func (t *Tracer) RecordEvent(eventType string, data map[string]any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.publishLocked(strings.TrimSpace(eventType), data)
}

func (t *Tracer) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	pipeline := clonePipeline(t.pipeline)
	events := append([]Event(nil), t.events...)
	return Snapshot{
		Pipeline:  pipeline,
		RunID:     t.pipeline.ID,
		SessionID: t.sessionID,
		Workspace: t.workspace,
		ServerURL: t.serverURL,
		Timeline:  buildTimeline(pipeline, events, time.Now().UTC()),
		Events:    events,
	}
}

func (t *Tracer) Subscribe(replayHistory bool) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	if t == nil {
		close(ch)
		return ch, func() {}
	}
	t.mu.Lock()
	if replayHistory {
		for _, event := range t.events {
			select {
			case ch <- event:
			default:
			}
		}
	}
	t.subscribers[ch] = struct{}{}
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		if _, ok := t.subscribers[ch]; ok {
			delete(t.subscribers, ch)
			close(ch)
		}
		t.mu.Unlock()
	}
}

func (t *Tracer) ensureStageLocked(id, name string) *Stage {
	id = defaultID(id, "stage")
	if stage := t.stageIndex[id]; stage != nil {
		if strings.TrimSpace(name) != "" {
			stage.Name = name
		}
		if stage.Status == "" {
			stage.Status = "live"
		}
		return stage
	}
	stage := &Stage{
		ID:        id,
		Name:      defaultName(name, id),
		StartTime: time.Now().UTC().Format(time.RFC3339Nano),
		Status:    "live",
	}
	t.stageIndex[id] = stage
	t.pipeline.Stages = append(t.pipeline.Stages, stage)
	return stage
}

func (t *Tracer) ensureAgentLocked(stage *Stage, id, name string) *Agent {
	if stage == nil {
		return nil
	}
	id = defaultID(id, "agent")
	key := agentKey(stage.ID, id)
	if agent := t.agentIndex[key]; agent != nil {
		if strings.TrimSpace(name) != "" {
			agent.Name = name
		}
		if agent.Status == "" {
			agent.Status = "live"
		}
		return agent
	}
	agent := &Agent{ID: id, Name: defaultName(name, id), Status: "live"}
	t.agentIndex[key] = agent
	stage.Agents = append(stage.Agents, agent)
	return agent
}

func (t *Tracer) publishLocked(eventType string, data map[string]any) {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = "event"
	}
	t.eventSeq++
	event := Event{
		Seq:       t.eventSeq,
		Type:      eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Data:      cloneMap(data),
	}
	t.events = append(t.events, event)
	if len(t.events) > maxEventHistory {
		t.events = append([]Event(nil), t.events[len(t.events)-maxEventHistory:]...)
	}
	for ch := range t.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func clonePipeline(p Pipeline) Pipeline {
	p.Total = p.Total.Normalized()
	stages := p.Stages
	p.Stages = make([]*Stage, 0, len(stages))
	for _, stage := range stages {
		if stage == nil {
			continue
		}
		clonedStage := *stage
		clonedStage.Subtotal = clonedStage.Subtotal.Normalized()
		clonedStage.Agents = make([]*Agent, 0, len(stage.Agents))
		for _, agent := range stage.Agents {
			if agent == nil {
				continue
			}
			clonedAgent := *agent
			clonedAgent.Total = clonedAgent.Total.Normalized()
			clonedAgent.CallsHistory = append([]Usage(nil), agent.CallsHistory...)
			clonedStage.Agents = append(clonedStage.Agents, &clonedAgent)
		}
		p.Stages = append(p.Stages, &clonedStage)
	}
	return p
}

func cloneMap(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func agentKey(stageID, agentID string) string {
	return stageID + "\x00" + agentID
}

func defaultID(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func defaultName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
