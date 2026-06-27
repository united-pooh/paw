package streamma

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const RoutingRationaleFileName = "streamma-routing-rationale.json"

type RoutingRationale struct {
	Version                string                  `json:"version"`
	Artifact               string                  `json:"artifact"`
	TaskSummary            string                  `json:"task_summary"`
	Understanding          TaskUnderstanding       `json:"understanding"`
	ProtocolCandidates     []ProtocolCandidate     `json:"protocol_candidates,omitempty"`
	SelectedStrategy       string                  `json:"selected_strategy"`
	SelectedProtocol       string                  `json:"selected_protocol"`
	GraphSelectionEvidence *GraphSelectionEvidence `json:"graph_selection_evidence,omitempty"`
	GraphSummary           GraphSummary            `json:"graph_summary"`
	NodePolicies           []NodePolicy            `json:"node_policies,omitempty"`
	ToolPolicy             map[string]ToolPolicy   `json:"tool_policy,omitempty"`
	Fallback               *FallbackDecision       `json:"fallback,omitempty"`
	Replan                 *ReplanDecision         `json:"replan,omitempty"`
	EventSummary           EventSummary            `json:"event_summary,omitempty"`
	Warnings               []string                `json:"warnings,omitempty"`
	PaperAssumptions       []string                `json:"paper_assumptions,omitempty"`
	EngineeringExtensions  []string                `json:"engineering_extensions,omitempty"`
}

type GraphSummary struct {
	Protocol               string   `json:"protocol"`
	Agents                 []string `json:"agents"`
	Edges                  []string `json:"edges,omitempty"`
	Sources                []string `json:"sources,omitempty"`
	Sinks                  []string `json:"sinks,omitempty"`
	CriticalPathAgentCount int      `json:"critical_path_agent_count,omitempty"`
	RequireBoundary        bool     `json:"require_boundary"`
	MaxAttempts            int      `json:"max_attempts"`
}

type EventSummary struct {
	TotalEvents    int      `json:"total_events"`
	CommittedSteps int      `json:"committed_steps"`
	RetryEvents    int      `json:"retry_events"`
	FinalAgent     string   `json:"final_agent,omitempty"`
	Producers      []string `json:"producers,omitempty"`
}

func BuildRoutingRationale(decision StrategyDecision, events []Event) RoutingRationale {
	graphSummary := SummarizeGraph(decision.Graph)
	eventSummary := SummarizeEvents(events)
	artifact := filepath.ToSlash(filepath.Join(".pipeline-workspace", "streamma-routing", RoutingRationaleFileName))
	return RoutingRationale{
		Version:                "1.0",
		Artifact:               artifact,
		TaskSummary:            strings.TrimSpace(decision.Understanding.Task),
		Understanding:          decision.Understanding,
		ProtocolCandidates:     append([]ProtocolCandidate(nil), decision.Candidates...),
		SelectedStrategy:       decision.StrategyID,
		SelectedProtocol:       decision.Protocol,
		GraphSelectionEvidence: cloneGraphSelectionEvidence(decision.GraphSelectionEvidence),
		GraphSummary:           graphSummary,
		NodePolicies:           append([]NodePolicy(nil), decision.NodePolicies...),
		ToolPolicy:             ToolPolicyByAgent(decision.NodePolicies),
		Fallback:               decision.Fallback,
		Replan:                 decision.Replan,
		EventSummary:           eventSummary,
		Warnings:               append([]string(nil), decision.Warnings...),
		PaperAssumptions:       defaultPaperAssumptions(),
		EngineeringExtensions:  defaultEngineeringExtensions(),
	}
}

func SummarizeGraph(graph GraphSpec) GraphSummary {
	agents := make([]string, 0, len(graph.Agents))
	hasPred := map[string]bool{}
	hasSucc := map[string]bool{}
	for _, agent := range graph.Agents {
		id := strings.TrimSpace(agent.ID)
		if id != "" {
			agents = append(agents, id)
		}
	}
	edges := make([]string, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if from == "" || to == "" {
			continue
		}
		edges = append(edges, from+"->"+to)
		hasSucc[from] = true
		hasPred[to] = true
	}
	sources := make([]string, 0, len(agents))
	sinks := make([]string, 0, len(agents))
	for _, id := range agents {
		if !hasPred[id] {
			sources = append(sources, id)
		}
		if !hasSucc[id] {
			sinks = append(sinks, id)
		}
	}
	sort.Strings(agents)
	sort.Strings(edges)
	sort.Strings(sources)
	sort.Strings(sinks)
	protocol := strings.TrimSpace(graph.Protocol)
	if protocol == "" {
		protocol = ProtocolStream
	}
	return GraphSummary{
		Protocol:               protocol,
		Agents:                 agents,
		Edges:                  edges,
		Sources:                sources,
		Sinks:                  sinks,
		CriticalPathAgentCount: CriticalPathAgentCount(graph),
		RequireBoundary:        graph.StepPolicy.RequireBoundary,
		MaxAttempts:            maxInt(graph.StepPolicy.MaxAttempts, 1),
	}
}

func cloneGraphSelectionEvidence(evidence *GraphSelectionEvidence) *GraphSelectionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.Rationale = append([]string(nil), evidence.Rationale...)
	cloned.Warnings = append([]string(nil), evidence.Warnings...)
	if evidence.Scaling != nil {
		scaling := *evidence.Scaling
		cloned.Scaling = &scaling
	}
	return &cloned
}

func SummarizeEvents(events []Event) EventSummary {
	producers := map[string]bool{}
	summary := EventSummary{TotalEvents: len(events)}
	for _, event := range events {
		if event.ProducerAgentID != "" {
			producers[event.ProducerAgentID] = true
		}
		switch event.Type {
		case EventStepCommitted:
			summary.CommittedSteps++
		case EventAgentRetry:
			summary.RetryEvents++
		case EventFinalAnswer:
			if event.Final != nil {
				summary.FinalAgent = event.Final.AgentID
			}
		}
	}
	for producer := range producers {
		summary.Producers = append(summary.Producers, producer)
	}
	sort.Strings(summary.Producers)
	return summary
}

func FormatRoutingTrace(decision StrategyDecision) string {
	return fmt.Sprintf("strategy=%s protocol=%s graph=%s agents=%d fallback=%t", decision.StrategyID, decision.Protocol, decision.GraphKind, len(decision.Graph.Agents), decision.Fallback != nil && decision.Fallback.Used)
}

func defaultPaperAssumptions() []string {
	return []string{
		"StreamMA forwards completed reasoning steps to direct successors instead of waiting for the whole upstream response.",
		"Stream is best suited to decomposable multi-step tasks; serial or single-agent execution can be preferable outside that regime.",
		"Official topology can be represented as agent config with next edges and system prompts.",
	}
}

func defaultEngineeringExtensions() []string {
	return []string{
		"EOF-triggered sink agents are a local engineering extension for review/finalizer aggregation.",
		"Tool authorization is represented as conservative node policy rather than inferred at runtime.",
		"First-phase replan is a conservative fallback/stub and does not rewrite committed step history.",
	}
}
