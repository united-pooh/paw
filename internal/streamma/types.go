package streamma

import (
	"context"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"sort"
	"strings"
	"time"
)

const (
	DefaultBoundary = "END_STEP"

	ProtocolStream = "stream"
)

type ModelStreamer interface {
	StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error)
}

type AgentStreamer interface {
	StreamAgent(ctx context.Context, invocation AgentInvocation) (<-chan model.StreamEvent, error)
}

type AgentInvocation struct {
	RunID           string
	AgentID         string
	Role            string
	SystemPrompt    string
	Problem         string
	InvocationIndex int
	Attempt         int
	StepCountHint   int
	StartStepIndex  int
	Boundary        string
	RequireBoundary bool
	InputEvents     []string
	InboundFrom     string
	InboundStep     *StepPacket
	Transcript      []TranscriptEntry
}

type AgentInvokePolicy string

const (
	InvokeOnArrival AgentInvokePolicy = "arrival"
	InvokeOnEOF     AgentInvokePolicy = "eof"
)

type GraphSpec struct {
	RunID      string      `json:"run_id,omitempty"`
	Protocol   string      `json:"protocol,omitempty"`
	StepPolicy StepPolicy  `json:"step_policy,omitempty"`
	Agents     []AgentSpec `json:"agents"`
	Edges      []EdgeSpec  `json:"edges,omitempty"`
}

type StepPolicy struct {
	Boundary        string `json:"boundary,omitempty"`
	MaxStepBytes    int    `json:"max_step_bytes,omitempty"`
	RequireBoundary bool   `json:"require_boundary,omitempty"`
	MaxAttempts     int    `json:"max_attempts,omitempty"`
}

type AgentSpec struct {
	ID            string `json:"id"`
	Role          string `json:"role,omitempty"`
	SystemPrompt  string `json:"system_prompt,omitempty"`
	InvokePolicy  string `json:"invoke_policy,omitempty"`
	StepCountHint int    `json:"step_count_hint,omitempty"`
}

type EdgeSpec struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type RunStatus string

const (
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

type RunResult struct {
	RunID  string             `json:"run_id"`
	Status RunStatus          `json:"status"`
	Final  *FinalAnswerPacket `json:"final,omitempty"`
	Events []Event            `json:"events"`
	Error  string             `json:"error,omitempty"`
}

type EventType string

const (
	EventProblem       EventType = "problem"
	EventStepCommitted EventType = "agent.step.committed"
	EventAgentRetry    EventType = "agent.retry"
	EventUpstreamEOF   EventType = "control.upstream_eof"
	EventFinalAnswer   EventType = "agent.final"
	EventRunFailed     EventType = "run.failed"
)

type Event struct {
	EventID         string             `json:"event_id"`
	RunID           string             `json:"run_id"`
	Type            EventType          `json:"event_type"`
	Timestamp       time.Time          `json:"timestamp,omitempty"`
	ProducerAgentID string             `json:"producer_agent_id,omitempty"`
	TargetAgentID   string             `json:"target_agent_id,omitempty"`
	EdgeID          string             `json:"edge_id,omitempty"`
	Seq             int                `json:"seq,omitempty"`
	Trace           map[string]string  `json:"trace,omitempty"`
	Problem         *ProblemPacket     `json:"problem,omitempty"`
	Step            *StepPacket        `json:"step,omitempty"`
	Control         *ControlPacket     `json:"control,omitempty"`
	Final           *FinalAnswerPacket `json:"final,omitempty"`
	Error           string             `json:"error,omitempty"`
}

type ProblemPacket struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

type StepPacket struct {
	SchemaVersion string           `json:"schema_version"`
	RunID         string           `json:"run_id"`
	AgentID       string           `json:"agent_id"`
	StepID        string           `json:"step_id"`
	StepIndex     int              `json:"step_index"`
	Attempt       int              `json:"attempt"`
	Content       StepContent      `json:"content"`
	Boundary      StepBoundary     `json:"boundary"`
	Usage         StepUsage        `json:"usage,omitempty"`
	Dependencies  StepDependencies `json:"dependencies,omitempty"`
}

type StepContent struct {
	Format              string `json:"format"`
	Text                string `json:"text"`
	PublicReasoningOnly bool   `json:"public_reasoning_only"`
}

type StepBoundary struct {
	SourceSentinel    string `json:"source_sentinel"`
	Closed            bool   `json:"closed"`
	BoundaryRecovered bool   `json:"boundary_recovered,omitempty"`
}

type StepUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	CachedTokens int `json:"cached_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type StepDependencies struct {
	InputEvents []string `json:"input_events,omitempty"`
}

type ControlPacket struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	ControlType   string `json:"control_type"`
	AgentID       string `json:"agent_id"`
	TargetAgentID string `json:"target_agent_id,omitempty"`
	EdgeID        string `json:"edge_id,omitempty"`
	FinalStep     int    `json:"final_step_index,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type FinalAnswerPacket struct {
	SchemaVersion string             `json:"schema_version"`
	RunID         string             `json:"run_id"`
	AgentID       string             `json:"agent_id"`
	Answer        FinalAnswerContent `json:"answer"`
	Support       FinalAnswerSupport `json:"support"`
}

type FinalAnswerContent struct {
	Format string `json:"format"`
	Text   string `json:"text"`
}

type FinalAnswerSupport struct {
	UsedSteps []string `json:"used_steps,omitempty"`
}

type ReplaySummary struct {
	Status          RunStatus          `json:"status"`
	Events          []Event            `json:"events,omitempty"`
	Steps           []StepPacket       `json:"steps,omitempty"`
	AgentStates     []ReplayAgentState `json:"agent_states,omitempty"`
	Final           *FinalAnswerPacket `json:"final,omitempty"`
	Error           string             `json:"error,omitempty"`
	FailureEventID  string             `json:"failure_event_id,omitempty"`
	FailureSequence int                `json:"failure_sequence,omitempty"`
}

type ReplayAgentState struct {
	AgentID         string                  `json:"agent_id"`
	Transcript      []ReplayTranscriptEntry `json:"transcript,omitempty"`
	OwnStepIDs      []string                `json:"own_step_ids,omitempty"`
	ReceivedEOF     []string                `json:"received_eof,omitempty"`
	Completed       bool                    `json:"completed,omitempty"`
	Final           bool                    `json:"final,omitempty"`
	Failed          bool                    `json:"failed,omitempty"`
	LastEventID     string                  `json:"last_event_id,omitempty"`
	LastStepID      string                  `json:"last_step_id,omitempty"`
	LastStepIndex   int                     `json:"last_step_index,omitempty"`
	LastStepText    string                  `json:"last_step_text,omitempty"`
	FailureEventID  string                  `json:"failure_event_id,omitempty"`
	FailureSequence int                     `json:"failure_sequence,omitempty"`
}

type ReplayTranscriptEntry struct {
	Kind          TranscriptEntryKind `json:"kind"`
	AgentID       string              `json:"agent_id"`
	From          string              `json:"from,omitempty"`
	Text          string              `json:"text"`
	StepID        string              `json:"step_id,omitempty"`
	SourceEventID string              `json:"source_event_id,omitempty"`
}

type PromptSegment struct {
	Key         string       `json:"key"`
	Role        message.Role `json:"role"`
	Content     string       `json:"content"`
	CacheStable bool         `json:"cache_stable"`
}

type compiledGraph struct {
	runID           string
	boundary        string
	maxStepBytes    int
	requireBoundary bool
	maxAttempts     int
	agents          map[string]AgentSpec
	agentOrder      []string
	predecessors    map[string][]string
	successors      map[string][]string
	edgeIDs         map[string]string
	sources         []string
}

func compileGraph(spec GraphSpec) (*compiledGraph, error) {
	runID := strings.TrimSpace(spec.RunID)
	if runID == "" {
		runID = "run_streamma"
	}
	protocol := strings.TrimSpace(spec.Protocol)
	if protocol == "" {
		protocol = ProtocolStream
	}
	if protocol != ProtocolStream {
		return nil, fmt.Errorf("unsupported streamma protocol: %s", protocol)
	}
	boundary := strings.TrimSpace(spec.StepPolicy.Boundary)
	if boundary == "" {
		boundary = DefaultBoundary
	}
	if len(spec.Agents) == 0 {
		return nil, fmt.Errorf("streamma graph requires at least one agent")
	}

	graph := &compiledGraph{
		runID:           runID,
		boundary:        boundary,
		maxStepBytes:    spec.StepPolicy.MaxStepBytes,
		requireBoundary: spec.StepPolicy.RequireBoundary,
		maxAttempts:     spec.StepPolicy.MaxAttempts,
		agents:          map[string]AgentSpec{},
		predecessors:    map[string][]string{},
		successors:      map[string][]string{},
		edgeIDs:         map[string]string{},
	}
	if graph.maxAttempts < 1 {
		graph.maxAttempts = 1
	}
	for _, agent := range spec.Agents {
		agent.ID = strings.TrimSpace(agent.ID)
		if agent.ID == "" {
			return nil, fmt.Errorf("streamma agent id is required")
		}
		if _, ok := graph.agents[agent.ID]; ok {
			return nil, fmt.Errorf("duplicate streamma agent id: %s", agent.ID)
		}
		graph.agents[agent.ID] = agent
		graph.agentOrder = append(graph.agentOrder, agent.ID)
		graph.predecessors[agent.ID] = nil
		graph.successors[agent.ID] = nil
	}
	for _, edge := range spec.Edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if _, ok := graph.agents[from]; !ok {
			return nil, fmt.Errorf("streamma edge references unknown source agent: %s", from)
		}
		if _, ok := graph.agents[to]; !ok {
			return nil, fmt.Errorf("streamma edge references unknown target agent: %s", to)
		}
		if from == to {
			return nil, fmt.Errorf("streamma self edge is not allowed: %s", from)
		}
		key := edgeKey(from, to)
		if _, exists := graph.edgeIDs[key]; exists {
			return nil, fmt.Errorf("duplicate streamma edge: %s", key)
		}
		graph.edgeIDs[key] = key
		graph.successors[from] = append(graph.successors[from], to)
		graph.predecessors[to] = append(graph.predecessors[to], from)
	}
	for _, id := range graph.agentOrder {
		sort.Strings(graph.successors[id])
		sort.Strings(graph.predecessors[id])
		if len(graph.predecessors[id]) == 0 {
			graph.sources = append(graph.sources, id)
		}
	}
	sinkCount := 0
	for _, id := range graph.agentOrder {
		if len(graph.successors[id]) == 0 {
			sinkCount++
		}
	}
	if len(graph.sources) == 0 {
		return nil, fmt.Errorf("streamma graph has no source agent")
	}
	if sinkCount == 0 {
		return nil, fmt.Errorf("streamma graph has no sink agent")
	}
	if err := graph.validateAcyclic(); err != nil {
		return nil, err
	}
	return graph, nil
}

func (g *compiledGraph) invokePolicy(agentID string) AgentInvokePolicy {
	if g == nil {
		return InvokeOnArrival
	}
	agent, ok := g.agents[agentID]
	if !ok {
		return InvokeOnArrival
	}
	switch AgentInvokePolicy(strings.TrimSpace(agent.InvokePolicy)) {
	case InvokeOnEOF:
		return InvokeOnEOF
	default:
		return InvokeOnArrival
	}
}

func (g *compiledGraph) validateAcyclic() error {
	indegree := map[string]int{}
	for _, id := range g.agentOrder {
		indegree[id] = len(g.predecessors[id])
	}
	queue := append([]string(nil), g.sources...)
	sort.Strings(queue)
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, succ := range g.successors[id] {
			indegree[succ]--
			if indegree[succ] == 0 {
				queue = append(queue, succ)
				sort.Strings(queue)
			}
		}
	}
	if visited != len(g.agentOrder) {
		return fmt.Errorf("streamma graph must be a DAG")
	}
	return nil
}

func edgeKey(from, to string) string {
	return strings.TrimSpace(from) + "->" + strings.TrimSpace(to)
}
