package streamma

import (
	"fmt"
	"sort"
	"strings"
)

const (
	PlanProtocolStream = "stream"
	PlanProtocolSerial = "serial"
	PlanProtocolSingle = "single"

	TaskTypeReasoning      = "reasoning"
	TaskTypeImplementation = "implementation"
	TaskTypeReview         = "review"
	TaskTypeCreative       = "creative"
	TaskTypeSingleStep     = "single_step"

	StepProfileHeadStrong      = "head_strong"
	StepProfileTailWeak        = "tail_weak"
	StepProfileNonDecomposable = "non_decomposable"
	StepProfileSingleStep      = "single_step"
	StepProfileUnknown         = "unknown"

	StrategyReasoningBaseline      = "baseline.reasoning"
	StrategyImplementationBaseline = "baseline.implementation"
	StrategyReviewBaseline         = "baseline.review"
	StrategySingleAgentFallback    = "fallback.single"
	StrategySerialFallback         = "fallback.serial"
)

type TaskUnderstanding struct {
	Task                string   `json:"task"`
	TaskType            string   `json:"task_type"`
	Decomposable        bool     `json:"decomposable"`
	ExpectedStepProfile string   `json:"expected_step_profile"`
	RecommendedProtocol string   `json:"recommended_protocol"`
	GraphKind           string   `json:"graph_kind"`
	AgentCountHint      int      `json:"agent_count_hint"`
	StepCountHint       int      `json:"step_count_hint"`
	RiskFlags           []string `json:"risk_flags,omitempty"`
	NeedsTools          bool     `json:"needs_tools"`
	NeedsHumanBoundary  bool     `json:"needs_human_boundary"`
	Rationale           []string `json:"rationale,omitempty"`
}

type ToolPolicy struct {
	Enabled                   bool     `json:"enabled"`
	Allowed                   []string `json:"allowed,omitempty"`
	Denied                    []string `json:"denied,omitempty"`
	RequiresHumanConfirmation []string `json:"requires_human_confirmation,omitempty"`
	Rationale                 []string `json:"rationale,omitempty"`
}

type NodePolicy struct {
	AgentID         string     `json:"agent_id"`
	InvokePolicy    string     `json:"invoke_policy"`
	RequireBoundary bool       `json:"require_boundary"`
	MaxAttempts     int        `json:"max_attempts"`
	StepCountHint   int        `json:"step_count_hint,omitempty"`
	Tools           ToolPolicy `json:"tools"`
	Rationale       []string   `json:"rationale,omitempty"`
}

type StrategyDecision struct {
	StrategyID             string                  `json:"strategy_id"`
	Protocol               string                  `json:"protocol"`
	GraphKind              string                  `json:"graph_kind"`
	SelectedTemplate       string                  `json:"selected_template,omitempty"`
	DynamicGraph           bool                    `json:"dynamic_graph"`
	Understanding          TaskUnderstanding       `json:"understanding"`
	Graph                  GraphSpec               `json:"graph"`
	GraphSelectionEvidence *GraphSelectionEvidence `json:"graph_selection_evidence,omitempty"`
	NodePolicies           []NodePolicy            `json:"node_policies,omitempty"`
	Fallback               *FallbackDecision       `json:"fallback,omitempty"`
	Replan                 *ReplanDecision         `json:"replan,omitempty"`
	Candidates             []ProtocolCandidate     `json:"candidates,omitempty"`
	Warnings               []string                `json:"warnings,omitempty"`
	Rationale              []string                `json:"rationale,omitempty"`
}

type ProtocolCandidate struct {
	Protocol  string `json:"protocol"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

type FallbackDecision struct {
	Used     bool     `json:"used"`
	Reason   string   `json:"reason,omitempty"`
	Strategy string   `json:"strategy,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type ReplanDecision struct {
	Triggered bool     `json:"triggered"`
	Trigger   string   `json:"trigger,omitempty"`
	Action    string   `json:"action,omitempty"`
	Rationale []string `json:"rationale,omitempty"`
}

type TopologyConfig struct {
	Agents []TopologyAgent `json:"agents"`
}

type TopologyDict map[string]TopologyNode

type TopologyNode struct {
	Role          string   `json:"role,omitempty"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	Next          []string `json:"next,omitempty"`
	InvokePolicy  string   `json:"invoke_policy,omitempty"`
	StepCountHint int      `json:"step_count_hint,omitempty"`
}

type TopologyAgent struct {
	ID            string   `json:"id"`
	Role          string   `json:"role,omitempty"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	Next          []string `json:"next,omitempty"`
	InvokePolicy  string   `json:"invoke_policy,omitempty"`
	StepCountHint int      `json:"step_count_hint,omitempty"`
}

func PlanStrategy(task string, baseline GraphSpec) StrategyDecision {
	understanding := UnderstandTask(task)
	candidates := ProtocolCandidates(understanding)
	strategyID := strategyIDForUnderstanding(understanding)
	graph := cloneGraphSpec(baseline)
	if strings.TrimSpace(graph.Protocol) == "" {
		graph.Protocol = ProtocolStream
	}
	applyStepCountHint(&graph, understanding.StepCountHint)
	policies := NodePoliciesForGraph(graph, understanding)
	decision := StrategyDecision{
		StrategyID:       strategyID,
		Protocol:         understanding.RecommendedProtocol,
		GraphKind:        understanding.GraphKind,
		SelectedTemplate: strategyID,
		DynamicGraph:     false,
		Understanding:    understanding,
		Graph:            graph,
		NodePolicies:     policies,
		Candidates:       candidates,
		Rationale:        append([]string(nil), understanding.Rationale...),
	}
	if understanding.RecommendedProtocol != PlanProtocolStream {
		decision.Fallback = &FallbackDecision{
			Used:     true,
			Reason:   "runtime supports stream GraphSpec only; logical protocol is represented as a safe stream-compatible baseline for this phase",
			Strategy: strategyID,
		}
		decision.Warnings = append(decision.Warnings, "logical protocol "+understanding.RecommendedProtocol+" mapped to stream-compatible GraphSpec")
	}
	if err := ValidateStrategyDecision(decision); err != nil {
		fallbackGraph := cloneGraphSpec(baseline)
		if _, graphErr := compileGraph(fallbackGraph); graphErr != nil {
			fallbackGraph = minimalFallbackGraph(baseline, understanding)
		}
		decision.Graph = fallbackGraph
		decision.NodePolicies = NodePoliciesForGraph(fallbackGraph, understanding)
		decision.Fallback = &FallbackDecision{
			Used:     true,
			Reason:   err.Error(),
			Strategy: strategyIDForGraphKind(understanding.GraphKind),
		}
		decision.Warnings = append(decision.Warnings, "strategy validation failed; using baseline graph")
	}
	return decision
}

func minimalFallbackGraph(baseline GraphSpec, understanding TaskUnderstanding) GraphSpec {
	runID := strings.TrimSpace(baseline.RunID)
	if runID == "" {
		runID = "streamma-routing-fallback"
	}
	stepPolicy := baseline.StepPolicy
	if stepPolicy.Boundary == "" {
		stepPolicy.Boundary = DefaultBoundary
	}
	if stepPolicy.MaxAttempts < 1 {
		stepPolicy.MaxAttempts = 1
	}
	return GraphSpec{
		RunID:      runID,
		Protocol:   ProtocolStream,
		StepPolicy: stepPolicy,
		Agents: []AgentSpec{{
			ID:            "finalizer",
			Role:          "fallback",
			SystemPrompt:  "Answer the task directly while preserving the StreamMA boundary contract.",
			StepCountHint: firstPositive(understanding.StepCountHint, 1),
		}},
	}
}

func UnderstandTask(task string) TaskUnderstanding {
	trimmed := strings.TrimSpace(task)
	lower := strings.ToLower(trimmed)
	u := TaskUnderstanding{
		Task:                trimmed,
		TaskType:            TaskTypeReasoning,
		Decomposable:        true,
		ExpectedStepProfile: StepProfileHeadStrong,
		RecommendedProtocol: PlanProtocolStream,
		GraphKind:           TaskTypeReasoning,
		AgentCountHint:      4,
		StepCountHint:       2,
		Rationale:           []string{"default reasoning task is decomposable and benefits from streamed intermediate steps"},
	}
	if containsAny(lower, []string{"review", "audit", "inspect current project", "review current project", "code review", "审查", "评审", "审阅", "检查当前项目", "当前项目", "项目审查", "项目评审", "项目 review"}) {
		u.TaskType = TaskTypeReview
		u.GraphKind = TaskTypeReview
		u.AgentCountHint = 3
		u.StepCountHint = 1
		u.Rationale = []string{"review task needs evidence collection, critique, and final synthesis"}
		return u
	}
	if containsAny(lower, []string{"implement", "build", "create", "code", "app", "game", "fix", "project", "run test", "run tests", "test suite", "unit test", "integration test", "add test", "add tests", "实现", "制作", "创建", "写代码", "代码", "项目", "游戏", "运行测试", "单元测试", "集成测试", "修复", "临时"}) {
		u.TaskType = TaskTypeImplementation
		u.GraphKind = TaskTypeImplementation
		u.AgentCountHint = 5
		u.StepCountHint = 2
		u.NeedsTools = true
		u.RiskFlags = append(u.RiskFlags, "workspace_mutation")
		u.Rationale = []string{"implementation task benefits from planner/scout/builder/verifier separation"}
		return u
	}
	if containsAny(lower, []string{"creative writing", "write a poem", "story", "poem", "freeform", "创作", "诗", "故事", "写一段"}) {
		u.TaskType = TaskTypeCreative
		u.Decomposable = false
		u.ExpectedStepProfile = StepProfileNonDecomposable
		u.RecommendedProtocol = PlanProtocolSingle
		u.GraphKind = TaskTypeReasoning
		u.AgentCountHint = 1
		u.StepCountHint = 1
		u.Rationale = []string{"open-ended creative tasks are outside the strongest StreamMA decomposition regime"}
		return u
	}
	if containsAny(lower, []string{"classify", "single token", "yes/no", "true or false", "分类", "判断对错", "只回答"}) {
		u.TaskType = TaskTypeSingleStep
		u.Decomposable = false
		u.ExpectedStepProfile = StepProfileSingleStep
		u.RecommendedProtocol = PlanProtocolSingle
		u.GraphKind = TaskTypeReasoning
		u.AgentCountHint = 1
		u.StepCountHint = 1
		u.Rationale = []string{"single-step tasks do not need multi-agent streaming overhead"}
		return u
	}
	if containsAny(lower, []string{"approve", "manual", "confirm", "human", "确认", "审批", "人工"}) {
		u.NeedsHumanBoundary = true
		u.RiskFlags = append(u.RiskFlags, "human_boundary")
	}
	return u
}

func ProtocolCandidates(understanding TaskUnderstanding) []ProtocolCandidate {
	stream := ProtocolCandidate{Protocol: PlanProtocolStream, Score: 70, Rationale: "default for decomposable multi-step tasks with useful early steps"}
	serial := ProtocolCandidate{Protocol: PlanProtocolSerial, Score: 45, Rationale: "safer when later steps depend strongly on a complete upstream answer"}
	single := ProtocolCandidate{Protocol: PlanProtocolSingle, Score: 35, Rationale: "best for non-decomposable or single-step tasks"}
	if !understanding.Decomposable {
		stream.Score = 25
		serial.Score = 45
		single.Score = 90
	}
	if understanding.ExpectedStepProfile == StepProfileSingleStep {
		single.Score = 95
	}
	if understanding.NeedsTools || understanding.TaskType == TaskTypeImplementation {
		stream.Score += 10
		serial.Score += 5
	}
	out := []ProtocolCandidate{stream, serial, single}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

func NodePoliciesForGraph(graph GraphSpec, understanding TaskUnderstanding) []NodePolicy {
	policies := make([]NodePolicy, 0, len(graph.Agents))
	for _, agent := range graph.Agents {
		invoke := strings.TrimSpace(agent.InvokePolicy)
		if invoke == "" {
			invoke = string(InvokeOnArrival)
		}
		policy := NodePolicy{
			AgentID:         agent.ID,
			InvokePolicy:    invoke,
			RequireBoundary: graph.StepPolicy.RequireBoundary,
			MaxAttempts:     maxInt(graph.StepPolicy.MaxAttempts, 1),
			StepCountHint:   firstPositive(agent.StepCountHint, understanding.StepCountHint),
			Tools:           toolPolicyForAgent(agent.ID, understanding),
			Rationale:       []string{"derived from selected graph and task understanding"},
		}
		policies = append(policies, policy)
	}
	return policies
}

func ToolPolicyByAgent(policies []NodePolicy) map[string]ToolPolicy {
	out := map[string]ToolPolicy{}
	for _, policy := range policies {
		out[strings.TrimSpace(policy.AgentID)] = policy.Tools
	}
	return out
}

func ToolEnabledFunc(policies []NodePolicy) func(string) bool {
	byAgent := ToolPolicyByAgent(policies)
	return func(agentID string) bool {
		policy, ok := byAgent[strings.TrimSpace(agentID)]
		return ok && policy.Enabled
	}
}

func GraphFromTopologyDict(runID string, stepPolicy StepPolicy, topology TopologyDict) (GraphSpec, error) {
	return GraphFromTopology(runID, stepPolicy, TopologyConfigFromDict(topology))
}

func TopologyConfigFromDict(topology TopologyDict) TopologyConfig {
	ids := make([]string, 0, len(topology))
	for id := range topology {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	config := TopologyConfig{Agents: make([]TopologyAgent, 0, len(ids))}
	for _, id := range ids {
		node := topology[id]
		config.Agents = append(config.Agents, TopologyAgent{
			ID:            id,
			Role:          strings.TrimSpace(node.Role),
			SystemPrompt:  strings.TrimSpace(node.SystemPrompt),
			Next:          append([]string(nil), node.Next...),
			InvokePolicy:  strings.TrimSpace(node.InvokePolicy),
			StepCountHint: node.StepCountHint,
		})
	}
	return config
}

func GraphFromTopology(runID string, stepPolicy StepPolicy, topology TopologyConfig) (GraphSpec, error) {
	if len(topology.Agents) == 0 {
		return GraphSpec{}, fmt.Errorf("topology requires at least one agent")
	}
	graph := GraphSpec{
		RunID:      strings.TrimSpace(runID),
		Protocol:   ProtocolStream,
		StepPolicy: stepPolicy,
	}
	seen := map[string]bool{}
	for _, node := range topology.Agents {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			return GraphSpec{}, fmt.Errorf("topology agent id is required")
		}
		if seen[id] {
			return GraphSpec{}, fmt.Errorf("duplicate topology agent id: %s", id)
		}
		seen[id] = true
		graph.Agents = append(graph.Agents, AgentSpec{
			ID:            id,
			Role:          strings.TrimSpace(node.Role),
			SystemPrompt:  strings.TrimSpace(node.SystemPrompt),
			InvokePolicy:  strings.TrimSpace(node.InvokePolicy),
			StepCountHint: node.StepCountHint,
		})
		for _, next := range node.Next {
			to := strings.TrimSpace(next)
			if to != "" {
				graph.Edges = append(graph.Edges, EdgeSpec{From: id, To: to})
			}
		}
	}
	if _, err := compileGraph(graph); err != nil {
		return GraphSpec{}, err
	}
	return graph, nil
}

func ValidateStrategyDecision(decision StrategyDecision) error {
	if strings.TrimSpace(decision.StrategyID) == "" {
		return fmt.Errorf("strategy id is required")
	}
	if strings.TrimSpace(decision.Protocol) == "" {
		return fmt.Errorf("strategy protocol is required")
	}
	if _, err := compileGraph(decision.Graph); err != nil {
		return err
	}
	return nil
}

func ClassifyReplan(trigger string, partialCommitted bool) ReplanDecision {
	normalized := strings.TrimSpace(strings.ToLower(trigger))
	if normalized == "" {
		return ReplanDecision{Triggered: false, Action: "none"}
	}
	decision := ReplanDecision{
		Triggered: true,
		Trigger:   normalized,
		Action:    "fallback_to_baseline",
		Rationale: []string{"first-phase replan is conservative and does not rewrite committed runtime state"},
	}
	if partialCommitted {
		decision.Action = "fail_fast_preserve_committed_steps"
		decision.Rationale = append(decision.Rationale, "partial committed steps cannot be safely replayed")
	}
	return decision
}

func strategyIDForUnderstanding(u TaskUnderstanding) string {
	if !u.Decomposable || u.RecommendedProtocol == PlanProtocolSingle {
		return StrategySingleAgentFallback
	}
	if u.RecommendedProtocol == PlanProtocolSerial {
		return StrategySerialFallback
	}
	return strategyIDForGraphKind(u.GraphKind)
}

func strategyIDForGraphKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case TaskTypeImplementation:
		return StrategyImplementationBaseline
	case TaskTypeReview:
		return StrategyReviewBaseline
	default:
		return StrategyReasoningBaseline
	}
}

func toolPolicyForAgent(agentID string, understanding TaskUnderstanding) ToolPolicy {
	id := strings.TrimSpace(agentID)
	enabled := understanding.TaskType == TaskTypeImplementation && (id == "scout" || id == "builder" || id == "verifier")
	policy := ToolPolicy{
		Enabled:   enabled,
		Denied:    []string{"high_risk_unconfirmed_actions"},
		Rationale: []string{"default conservative StreamMA tool policy"},
	}
	if enabled {
		policy.Allowed = []string{"workspace_read", "workspace_write", "command_check"}
		policy.Rationale = append(policy.Rationale, "implementation scout/builder/verifier keep existing tool-enabled behavior")
	}
	return policy
}

func applyStepCountHint(graph *GraphSpec, hint int) {
	if graph == nil || hint <= 0 {
		return
	}
	for i := range graph.Agents {
		if graph.Agents[i].StepCountHint <= 0 {
			graph.Agents[i].StepCountHint = hint
		}
	}
}

func cloneGraphSpec(spec GraphSpec) GraphSpec {
	cloned := spec
	cloned.Agents = append([]AgentSpec(nil), spec.Agents...)
	cloned.Edges = append([]EdgeSpec(nil), spec.Edges...)
	return cloned
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
