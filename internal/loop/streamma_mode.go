package loop

import (
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/pipeline"
	"codex-agent-go/internal/streamma"
	"codex-agent-go/internal/tokentracer"
	"codex-agent-go/internal/ui"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	streamMACommand      = "/streamma"
	streamMATraceCommand = "/streamma-trace"

	streamMATaskMaxBytes                = 4096
	streamMAConversationContextMaxBytes = 2048
	streamMAProblemMaxBytes             = 6144
	streamMAInboundStepMaxBytes         = 6144
	streamMAReviewSnapshotMaxBytes      = 8192
)

type StreamMASubagentRequest struct {
	RunID           string
	SessionID       string
	SessionKey      string
	InvocationIndex int
	AgentID         string
	Role            string
	Description     string
	SystemPrompt    string
	Problem         string
	InboundFrom     string
	InboundStep     *streamma.StepPacket
	Boundary        string
	RequireBoundary bool
	Prompt          string
	ContextMode     string
	DisableTools    bool
}

type StreamMASubagentStream struct {
	Events         <-chan model.StreamEvent
	AgentName      string
	AgentColor     string
	SessionID      string
	TranscriptPath string
	OutputPath     string
}

type StreamMASubagentRunner interface {
	StreamSubagent(ctx context.Context, req StreamMASubagentRequest) (StreamMASubagentStream, error)
}

func (runner *Runner) SetStreamMASubagentRunner(subagents StreamMASubagentRunner) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.streamMASubagents = subagents
}

func (runner *Runner) SetStreamMAEnabled(enabled bool) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.streamMAEnabled = enabled
}

func (runner *Runner) SetSubagentTokensProvider(p SubagentTokensProvider) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.subagentTokensProvider = p
}

func (runner *Runner) SetSystemSupplement(supplement string) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.systemSupplement = strings.TrimSpace(supplement)
}

func (runner *Runner) SetCompactToolPrompt(enabled bool) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.compactToolPrompt = enabled
}

type streamMAInvocation struct {
	Task           string
	Trace          bool
	Profile        string
	Topology       string
	TopologyForced bool
	Agents         int
	Steps          int
	Protocol       string
	ParseError     string
}

func parseStreamMAInvocation(input string) (streamMAInvocation, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return streamMAInvocation{}, false
	}
	for _, command := range []string{streamMACommand, streamMATraceCommand} {
		if trimmed == command {
			return streamMAInvocation{Trace: command == streamMATraceCommand, Profile: "adaptive"}, true
		}
		prefix := command + " "
		if strings.HasPrefix(trimmed, prefix) {
			invocation := parseStreamMAFlags(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)))
			invocation.Trace = command == streamMATraceCommand
			return invocation, true
		}
	}
	return streamMAInvocation{}, false
}

func parseStreamMAFlags(input string) streamMAInvocation {
	invocation := streamMAInvocation{
		Profile:  "adaptive",
		Protocol: streamma.PlanProtocolStream,
	}
	fields := strings.Fields(input)
	var task []string
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if !strings.HasPrefix(field, "--") {
			task = append(task, fields[i:]...)
			break
		}
		name, value, hasInline := strings.Cut(strings.TrimPrefix(field, "--"), "=")
		if !hasInline {
			if i+1 >= len(fields) {
				invocation.ParseError = "missing value for --" + name
				break
			}
			i++
			value = fields[i]
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "profile":
			invocation.Profile = strings.ToLower(strings.TrimSpace(value))
		case "topology":
			invocation.Topology = streamma.NormalizeTopologyShape(value)
			invocation.TopologyForced = true
		case "agents", "a":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				invocation.ParseError = "invalid --agents value: " + value
				break
			}
			invocation.Agents = parsed
		case "steps", "s":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				invocation.ParseError = "invalid --steps value: " + value
				break
			}
			invocation.Steps = parsed
		case "protocol":
			invocation.Protocol = strings.ToLower(strings.TrimSpace(value))
		default:
			invocation.ParseError = "unknown /streamma option: --" + name
		}
		if invocation.ParseError != "" {
			break
		}
	}
	invocation.Task = strings.TrimSpace(strings.Join(task, " "))
	return invocation
}

func (runner *Runner) runStreamMATurn(ctx context.Context, input string, invocation streamMAInvocation) (msg message.Message, err error) {
	if strings.TrimSpace(invocation.ParseError) != "" {
		return message.Message{}, fmt.Errorf("%s", invocation.ParseError)
	}
	task := invocation.Task
	if strings.TrimSpace(task) == "" {
		if invocation.Trace {
			return message.Message{}, fmt.Errorf("usage: /streamma-trace <task>")
		}
		return message.Message{}, fmt.Errorf("usage: /streamma <task>")
	}
	subagents := runner.currentStreamMASubagents()
	if subagents == nil {
		return message.Message{}, fmt.Errorf("streamma requires subagent backend")
	}

	history, injectedSupplements := runner.buildTurnHistory(input)
	committed := false
	defer func() {
		if !committed && len(injectedSupplements) > 0 {
			runner.prependSupplements(injectedSupplements)
		}
	}()

	baselineKind, baselineSpec, graphSelection := streamMAGraphForInvocation(task, invocation)
	decision := streamma.PlanStrategy(task, baselineSpec)
	decision.GraphSelectionEvidence = graphSelection
	if graphSelection != nil {
		if graphSelection.ExpectedAgents > 0 {
			decision.Understanding.AgentCountHint = graphSelection.ExpectedAgents
		}
		if graphSelection.ExpectedSteps > 0 {
			decision.Understanding.StepCountHint = graphSelection.ExpectedSteps
		}
	}
	graphKind := streamMAGraphKindFromPlan(decision.GraphKind, baselineKind)
	spec := decision.Graph
	toolsPolicy := streamMAToolsPolicy(streamma.ToolEnabledFunc(decision.NodePolicies))
	if len(decision.NodePolicies) == 0 {
		toolsPolicy = streamMAToolsPolicyForGraph(graphKind)
	}

	trace := runner.beginTraceTurn(input, "streamma")
	defer func() {
		runner.finishTraceTurn(trace, err)
	}()
	runner.recordTraceEvent("streamma_start", map[string]any{
		"stage_id": trace.stageID,
		"run_id":   spec.RunID,
		"task":     task,
		"agents":   streamMAAgentIDs(spec),
		"edges":    spec.Edges,
		"strategy": decision.StrategyID,
		"profile":  invocation.Profile,
		"topology": invocation.Topology,
	})
	if invocation.Trace {
		runner.notifySystem("streamma-trace", fmt.Sprintf("routing %s", streamma.FormatRoutingTrace(decision)))
		runner.notifySystem("streamma-trace", fmt.Sprintf("running %s with live runtime events", describeStreamMAGraph(spec)))
	} else {
		runner.notifySystem("streamma", fmt.Sprintf("running %s with subagent-backed Exact+END_STEP workers strategy=%s", describeStreamMAGraph(spec), decision.StrategyID))
	}
	problem := buildStreamMAProblem(task, history)
	if graphKind == streamMAGraphReview {
		problem = strings.TrimRight(problem, "\n") + "\n\n" + streamMAReviewWorkspaceSnapshot(runner.workRoot)
	}
	subagentModel := newStreamMASubagentModel(
		subagents,
		runner.notifySystem,
		invocation.Trace,
		toolsPolicy,
		runner.currentSkillContext(),
		runner.currentTokenTracer(),
		trace.stageID,
		runner.currentModelLabels,
	)
	result, err := streamma.RunGraphWithAgentEventSink(
		ctx,
		spec,
		subagentModel,
		problem,
		mergeStreamMASinks(runner.streamMATraceSink(invocation.Trace), runner.streamMATokenTraceSink(trace.stageID)),
	)
	if err != nil {
		return message.Message{}, err
	}
	if result.Status != streamma.RunCompleted {
		return message.Message{}, fmt.Errorf("streamma run %s ended with status %s: %s", result.RunID, result.Status, result.Error)
	}

	finalText := finalStreamMAText(result)
	rationale := streamma.BuildRoutingRationale(decision, result.Events)
	if writeErr := runner.writeStreamMARoutingRationale(rationale); writeErr != nil && invocation.Trace {
		runner.notifySystem("streamma-trace", "routing rationale write failed: "+writeErr.Error())
	}
	var logger streamma.RunLogger
	if decision.GraphSelectionEvidence != nil {
		logger = streamma.BuildRunLogger(spec, result.Events, subagentModel.runLoggerSpans(), *decision.GraphSelectionEvidence)
	} else {
		logger = streamma.BuildRunLogger(spec, result.Events, subagentModel.runLoggerSpans())
	}
	if writeErr := runner.writeStreamMARunLogger(result.RunID, logger); writeErr != nil && invocation.Trace {
		runner.notifySystem("streamma-trace", "runlogger write failed: "+writeErr.Error())
	}
	if writeErr := runner.appendStreamMAEvidence(streamma.BuildRoutingEvidenceRecord(task, decision, logger)); writeErr != nil && invocation.Trace {
		runner.notifySystem("streamma-trace", "evidence write failed: "+writeErr.Error())
	}
	if graphKind == streamMAGraphReview {
		artifacts, artifactErr := pipeline.WriteReviewArtifacts(ctx, pipeline.ReviewArtifactsOptions{
			Root:        runner.workRoot,
			Task:        task,
			RunID:       result.RunID,
			FinalText:   finalText,
			MaxAttempts: 2,
		})
		if artifactErr != nil {
			return message.Message{}, artifactErr
		}
		runner.notifySystem("pipeline", fmt.Sprintf("wrote strict tree grading artifacts to %s (score=%d verdict=%s)", artifacts.WorkspaceDir, artifacts.Assessment.AggregateScore, artifacts.Assessment.Verdict))
	}
	assistant := buildAssistantMessage(finalText)
	history = append(history, assistant)
	if err := runner.commitHistory(ctx, history); err != nil {
		return message.Message{}, err
	}
	committed = true

	if err := runner.ui.OnAssistantDelta(finalText); err != nil {
		return message.Message{}, err
	}
	if err := runner.ui.OnDone(); err != nil {
		return message.Message{}, err
	}
	runner.recordTraceEvent("streamma_end", map[string]any{
		"stage_id": trace.stageID,
		"run_id":   result.RunID,
		"status":   result.Status,
	})
	return assistant, nil
}

func (runner *Runner) writeStreamMARoutingRationale(rationale streamma.RoutingRationale) error {
	return runner.writeStreamMAJSONArtifact(streamma.RoutingRationaleFileName, rationale)
}

func (runner *Runner) writeStreamMARunLogger(runID string, logger streamma.RunLogger) error {
	if err := runner.writeStreamMAJSONArtifact(streamma.RunLoggerFileName, logger); err != nil {
		return err
	}
	return runner.writeStreamMARunJSONArtifact(runID, streamma.RunLoggerFileName, logger)
}

func (runner *Runner) appendStreamMAEvidence(record streamma.RoutingEvidenceRecord) error {
	dir := runner.streamMAArtifactDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(filepath.Join(dir, streamma.EvidenceFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(data)
	return err
}

func (runner *Runner) writeStreamMAJSONArtifact(fileName string, value interface{}) error {
	return runner.writeStreamMAJSONArtifactInDir(runner.streamMAArtifactDir(), fileName, value)
}

func (runner *Runner) writeStreamMARunJSONArtifact(runID, fileName string, value interface{}) error {
	return runner.writeStreamMAJSONArtifactInDir(filepath.Join(runner.streamMAArtifactDir(), "runs", streamma.SafeArtifactID(runID)), fileName, value)
}

func (runner *Runner) streamMAArtifactDir() string {
	root := ""
	if runner != nil {
		root = strings.TrimSpace(runner.workRoot)
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err == nil {
			root = cwd
		}
	}
	return filepath.Join(root, ".pipeline-workspace", "streamma-routing")
}

func (runner *Runner) writeStreamMAJSONArtifactInDir(dir, fileName string, value interface{}) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, fileName), data, 0o600)
}

func (runner *Runner) streamMATraceSink(enabled bool) func(streamma.Event) {
	if !enabled {
		return nil
	}
	started := time.Now()
	return func(event streamma.Event) {
		body := formatStreamMATraceEvent(started, event)
		if strings.TrimSpace(body) != "" {
			runner.notifySystem("streamma-trace", body)
		}
	}
}

func formatStreamMATraceEvent(started time.Time, event streamma.Event) string {
	elapsed := time.Since(started).Truncate(time.Millisecond)
	parts := []string{
		fmt.Sprintf("+%s", elapsed),
		fmt.Sprintf("seq=%d", event.Seq),
		"type=" + string(event.Type),
	}
	if event.ProducerAgentID != "" {
		parts = append(parts, "producer="+event.ProducerAgentID)
	}
	if event.TargetAgentID != "" {
		parts = append(parts, "target="+event.TargetAgentID)
	}
	if event.EdgeID != "" {
		parts = append(parts, "edge="+event.EdgeID)
	}
	if event.Step != nil {
		parts = append(parts,
			"step="+event.Step.StepID,
			fmt.Sprintf("index=%d", event.Step.StepIndex),
			fmt.Sprintf("recovered=%t", event.Step.Boundary.BoundaryRecovered),
		)
	}
	if event.Control != nil {
		parts = append(parts,
			"control="+event.Control.ControlType,
			fmt.Sprintf("final_step=%d", event.Control.FinalStep),
			"reason="+event.Control.Reason,
		)
	}
	if event.Final != nil {
		parts = append(parts, "final_agent="+event.Final.AgentID)
	}
	if event.Error != "" {
		parts = append(parts, "error="+event.Error)
	}
	if len(event.Trace) > 0 {
		keys := make([]string, 0, len(event.Trace))
		for key := range event.Trace {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"="+event.Trace[key])
		}
	}
	return strings.Join(parts, " ")
}

func (runner *Runner) currentStreamMASubagents() StreamMASubagentRunner {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.streamMASubagents
}

func (runner *Runner) currentStreamMAEnabled() bool {
	if runner == nil {
		return false
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.streamMAEnabled
}

func streamMAGraphKindFromPlan(kind string, fallback streamMAGraphKind) streamMAGraphKind {
	switch strings.TrimSpace(kind) {
	case streamma.TaskTypeImplementation:
		return streamMAGraphImplementation
	case streamma.TaskTypeReview:
		return streamMAGraphReview
	case streamma.TaskTypeReasoning, streamma.TaskTypeCreative, streamma.TaskTypeSingleStep:
		return streamMAGraphReasoning
	default:
		return fallback
	}
}

func streamMAGraphForInvocation(task string, invocation streamMAInvocation) (streamMAGraphKind, streamma.GraphSpec, *streamma.GraphSelectionEvidence) {
	if strings.EqualFold(strings.TrimSpace(invocation.Profile), "paper") || streamMAInvocationRequestsTopology(invocation) {
		return streamMAPaperGraphForInvocation(task, invocation)
	}
	kind := streamMAGraphKindForTask(task)
	spec := defaultStreamMAGraph(task)
	understanding := streamma.UnderstandTask(task)
	evidence := streamma.SelectGraphTopology(streamma.GraphRouterInput{
		Understanding:     understanding,
		RequestedTopology: streamma.TopologyShapeAdaptive,
		AgentCount:        len(spec.Agents),
		StepCount:         maxInt(understanding.StepCountHint, 1),
	})
	evidence.SelectedTopology = streamma.TopologyShapeAdaptive
	evidence.EvidenceLevel = streamma.EvidenceLevelHeuristic
	evidence.CriticalPath.AgentCount = len(spec.Agents)
	evidence.CriticalPath.CriticalPathAgentCount = streamma.CriticalPathAgentCount(spec)
	evidence.Rationale = append(evidence.Rationale, "adaptive mode keeps the existing task-template graph")
	return kind, spec, &evidence
}

func streamMAInvocationRequestsTopology(invocation streamMAInvocation) bool {
	if invocation.TopologyForced && strings.TrimSpace(invocation.Topology) != "" && invocation.Topology != streamma.TopologyShapeAdaptive {
		return true
	}
	return invocation.Agents > 0 || invocation.Steps > 0
}

func streamMAPaperGraphForInvocation(task string, invocation streamMAInvocation) (streamMAGraphKind, streamma.GraphSpec, *streamma.GraphSelectionEvidence) {
	agents := invocation.Agents
	if agents <= 0 {
		agents = 32
	}
	steps := invocation.Steps
	if steps <= 0 {
		steps = 64
	}
	understanding := streamma.UnderstandTask(task)
	requested := invocation.Topology
	if strings.TrimSpace(requested) == "" {
		requested = streamma.TopologyShapeAdaptive
	}
	evidence := streamma.SelectGraphTopology(streamma.GraphRouterInput{
		Understanding:     understanding,
		RequestedTopology: requested,
		TopologyForced:    invocation.TopologyForced,
		AgentCount:        agents,
		StepCount:         steps,
		Cache:             streamma.CacheEvidence{CacheSignal: "unknown"},
	})
	shape := evidence.SelectedTopology
	if shape == streamma.TopologyShapeAdaptive || !understanding.Decomposable || understanding.RecommendedProtocol == streamma.PlanProtocolSingle {
		kind := streamMAGraphKindForTask(task)
		spec := defaultStreamMAGraph(task)
		evidence.Warnings = append(evidence.Warnings, "paper profile fell back to adaptive graph because task is not decomposable")
		return kind, spec, &evidence
	}
	runID := fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano())
	spec, err := streamma.GraphFromTopologyShape(runID, streamMAPaperStepPolicy(), streamMAPaperTopologyAgents(agents, steps), shape)
	if err != nil {
		kind := streamMAGraphKindForTask(task)
		spec := defaultStreamMAGraph(task)
		evidence.SelectedTopology = streamma.TopologyShapeAdaptive
		evidence.Warnings = append(evidence.Warnings, "paper topology validation failed: "+err.Error())
		return kind, spec, &evidence
	}
	return streamMAGraphReasoning, spec, &evidence
}

func streamMAPaperStepPolicy() streamma.StepPolicy {
	return streamma.StepPolicy{
		Boundary:        streamma.DefaultBoundary,
		MaxStepBytes:    64 * 1024,
		RequireBoundary: true,
		MaxAttempts:     2,
	}
}

func streamMAPaperTopologyAgents(agentCount, stepCount int) []streamma.TopologyAgent {
	agents := make([]streamma.TopologyAgent, 0, agentCount)
	for i := 1; i <= agentCount; i++ {
		id := fmt.Sprintf("Agent_%02d", i)
		role := "stream_reasoner"
		mission := "Consume inbound StreamMA steps and produce one concise public correction or continuation step."
		hint := 1
		if i == 1 {
			role = "source_reasoner"
			mission = fmt.Sprintf("Break the problem into about %d concise public reasoning steps for downstream agents.", stepCount)
			hint = stepCount
		}
		if i == agentCount {
			role = "final_response"
			mission = "Produce the final answer from the best corrected StreamMA reasoning available so far."
		}
		agents = append(agents, streamma.TopologyAgent{
			ID:            id,
			Role:          role,
			SystemPrompt:  streamMAAgentPrompt(id, role, mission),
			StepCountHint: hint,
		})
	}
	return agents
}

func defaultStreamMAGraph(task string) streamma.GraphSpec {
	kind := streamMAGraphKindForTask(task)
	runID := fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano())
	graph, err := streamma.GraphFromTopologyDict(runID, streamMAStepPolicyForGraph(kind), streamMATopologyConfig(kind))
	if err == nil {
		return graph
	}
	return streamma.GraphSpec{
		RunID:      runID,
		Protocol:   streamma.ProtocolStream,
		StepPolicy: streamMAStepPolicyForGraph(kind),
		Agents: []streamma.AgentSpec{{
			ID:           "finalizer",
			Role:         "final_response",
			SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Answer the user directly because the configured StreamMA topology failed validation."),
		}},
	}
}

func streamMAStepPolicyForGraph(kind streamMAGraphKind) streamma.StepPolicy {
	switch kind {
	case streamMAGraphImplementation:
		return streamma.StepPolicy{
			Boundary:        streamma.DefaultBoundary,
			MaxStepBytes:    64 * 1024,
			RequireBoundary: true,
			MaxAttempts:     2,
		}
	case streamMAGraphReview:
		return streamma.StepPolicy{
			Boundary:        streamma.DefaultBoundary,
			MaxStepBytes:    48 * 1024,
			RequireBoundary: false,
			MaxAttempts:     2,
		}
	default:
		return streamma.StepPolicy{
			Boundary:        streamma.DefaultBoundary,
			MaxStepBytes:    32 * 1024,
			RequireBoundary: true,
			MaxAttempts:     2,
		}
	}
}

func streamMATopologyConfig(kind streamMAGraphKind) streamma.TopologyDict {
	switch kind {
	case streamMAGraphImplementation:
		return streamma.TopologyDict{
			"planner":   {Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Create a concise implementation plan and success criteria. Do not modify files."), Next: []string{"builder"}},
			"scout":     {Role: "workspace_scout", SystemPrompt: streamMAAgentPrompt("scout", "workspace_scout", "Inspect the workspace and identify the safest place and stack for the requested implementation. Prefer read-only investigation."), Next: []string{"builder"}},
			"builder":   {Role: "implementation_builder", SystemPrompt: streamMAAgentPrompt("builder", "implementation_builder", "Use the inbound plan and workspace facts to implement the requested artifact with available tools. For temporary demos, create files under a clearly named temporary workspace directory."), Next: []string{"verifier", "finalizer"}},
			"verifier":  {Role: "runtime_verifier", SystemPrompt: streamMAAgentPrompt("verifier", "runtime_verifier", "Run focused verification for the builder output, report failures, and fix only when necessary."), Next: []string{"finalizer"}, InvokePolicy: string(streamma.InvokeOnEOF)},
			"finalizer": {Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Synthesize the current implemented state, paths, commands, verification results, and remaining caveats for the user."), InvokePolicy: string(streamma.InvokeOnEOF)},
		}
	case streamMAGraphReview:
		return streamma.TopologyDict{
			"scout":     {Role: "workspace_scout", SystemPrompt: streamMAAgentPrompt("scout", "workspace_scout", "Review the provided bounded workspace snapshot and summarize only directly evidenced architecture, important files, and coverage gaps. Do not call tools. Do not infer vulnerabilities, versions, or runtime behavior from filenames alone."), Next: []string{"critic"}},
			"critic":    {Role: "project_reviewer", SystemPrompt: streamMAAgentPrompt("critic", "project_reviewer", "Review the scout findings for concrete bugs, risks, missing tests, unsupported claims, and evidence gaps that are directly supported by the snapshot. If evidence is partial or missing, label it unverified instead of escalating severity. Do not modify files."), Next: []string{"finalizer"}, InvokePolicy: string(streamma.InvokeOnEOF)},
			"finalizer": {Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Produce a concise project review with prioritized findings, evidence, residual risks, and next checks. Every finding must name exact evidence visible in the provided snapshot. Do not infer implementation details from filenames alone. Unsupported or partial evidence belongs under unverified risks. Do not claim tests were run unless an inbound step proves it."), InvokePolicy: string(streamma.InvokeOnEOF)},
		}
	default:
		return streamma.TopologyDict{
			"planner":   {Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Break down the task into early useful reasoning steps."), Next: []string{"solver", "critic"}},
			"solver":    {Role: "reasoning_solver", SystemPrompt: streamMAAgentPrompt("solver", "reasoning_solver", "Consume planner steps and solve the task incrementally."), Next: []string{"critic", "finalizer"}},
			"critic":    {Role: "quality_critic", SystemPrompt: streamMAAgentPrompt("critic", "quality_critic", "Check solver steps for gaps, contradictions, and missing evidence."), Next: []string{"finalizer"}, InvokePolicy: string(streamma.InvokeOnEOF)},
			"finalizer": {Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Produce the final user-facing answer from the best available inbound steps."), InvokePolicy: string(streamma.InvokeOnEOF)},
		}
	}
}

type streamMAGraphKind string

const (
	streamMAGraphReasoning      streamMAGraphKind = "reasoning"
	streamMAGraphImplementation streamMAGraphKind = "implementation"
	streamMAGraphReview         streamMAGraphKind = "review"
)

func streamMAGraphKindForTask(task string) streamMAGraphKind {
	if streamMATaskLooksReview(task) {
		return streamMAGraphReview
	}
	if streamMATaskLooksImplementationHeavy(task) {
		return streamMAGraphImplementation
	}
	return streamMAGraphReasoning
}

func streamMATaskLooksImplementationHeavy(task string) bool {
	lower := strings.ToLower(task)
	for _, marker := range []string{
		"implement", "build", "create", "code", "app", "game", "fix", "project",
		"run test", "run tests", "test suite", "unit test", "integration test", "add test", "add tests",
		"实现", "制作", "创建", "写代码", "代码", "项目", "游戏", "运行测试", "单元测试", "集成测试", "修复", "临时",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func streamMATaskLooksReview(task string) bool {
	lower := strings.ToLower(strings.TrimSpace(task))
	for _, marker := range []string{
		"review", "audit", "inspect current project", "review current project", "code review",
		"审查", "评审", "审阅", "检查当前项目", "当前项目", "项目审查", "项目评审", "项目 review",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func streamMAReviewWorkspaceSnapshot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if root == "" {
		return "Workspace snapshot unavailable: no workspace root."
	}
	var builder strings.Builder
	builder.WriteString("Bounded workspace snapshot for review. Use this as evidence; do not call tools.\n")
	builder.WriteString("Root: ")
	builder.WriteString(root)
	builder.WriteString("\n\nFiles:\n")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && shouldSkipStreamMAReviewDir(name) {
			return filepath.SkipDir
		}
		depth := strings.Count(filepath.ToSlash(rel), "/")
		if entry.IsDir() && depth >= 3 {
			return filepath.SkipDir
		}
		if !entry.IsDir() && depth >= 4 {
			return nil
		}
		line := "- " + filepath.ToSlash(rel)
		if entry.IsDir() {
			line += "/"
		}
		line += "\n"
		if builder.Len()+len(line) > streamMAReviewSnapshotMaxBytes/2 {
			return filepath.SkipAll
		}
		builder.WriteString(line)
		return nil
	})
	for _, rel := range []string{"go.mod", "README.md", "cmd/agent/main.go"} {
		appendStreamMAReviewFileExcerpt(&builder, root, rel)
	}
	if builder.Len() > streamMAReviewSnapshotMaxBytes {
		return truncateStreamMATailBytes(builder.String(), streamMAReviewSnapshotMaxBytes)
	}
	return builder.String()
}

func shouldSkipStreamMAReviewDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".ccagent", "node_modules", "vendor", "dist", "build", "tmp", "temp":
		return true
	default:
		return false
	}
}

func appendStreamMAReviewFileExcerpt(builder *strings.Builder, root, rel string) {
	if builder == nil || builder.Len() >= streamMAReviewSnapshotMaxBytes {
		return
	}
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	excerpt := truncateStreamMAHeadBytes(string(data), 1400)
	block := "\n" + filepath.ToSlash(rel) + " excerpt:\n" + excerpt + "\n"
	if builder.Len()+len(block) > streamMAReviewSnapshotMaxBytes {
		block = truncateStreamMAHeadBytes(block, maxInt(0, streamMAReviewSnapshotMaxBytes-builder.Len()))
	}
	builder.WriteString(block)
}

func streamMAAgentPrompt(agentID, role, mission string) string {
	return fmt.Sprintf("streamma_agent_id=%s\nstreamma_role=%s\n%s\n%s", agentID, role, mission, streamMAOutputContract)
}

const streamMAOutputContract = `Output contract:
- Use natural language only.
- End every reasoning step with a line that contains exactly END_STEP.
- END_STEP must be on its own line with no spaces, punctuation, indentation, JSON, or Markdown fences.
- The StreamMA runtime is strict: if your final answer does not include an exact END_STEP line, this agent invocation fails instead of being propagated.
- The final non-whitespace line of every assistant message must be exactly END_STEP. Never write any text after a closing END_STEP line.
- You are a real subagent worker with your normal tools available.
- Use tools only when your role needs them and after you have enough verified plan/context.
- Keep each step public and concise; do not reveal private chain-of-thought.`

func buildStreamMAProblem(task string, history []message.Message) string {
	var builder strings.Builder
	builder.WriteString("Current user task:\n")
	builder.WriteString(truncateStreamMAHeadBytes(strings.TrimSpace(task), streamMATaskMaxBytes))
	if context := streamMAConversationContext(history); context != "" {
		builder.WriteString("\n\nConversation context:\n")
		builder.WriteString(context)
	}
	return truncateStreamMAHeadBytes(builder.String(), streamMAProblemMaxBytes)
}

func streamMAConversationContext(history []message.Message) string {
	var lines []string
	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if strings.HasPrefix(content, streamMACommand) || strings.HasPrefix(content, streamMATraceCommand) {
			continue
		}
		switch msg.Role {
		case message.RoleUser:
			lines = append(lines, "User: "+content)
		case message.RoleAssistant:
			lines = append(lines, "Assistant: "+content)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n")
	return truncateStreamMATailBytes(text, streamMAConversationContextMaxBytes)
}

func truncateStreamMAHeadBytes(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text
	}
	suffix := "\n[truncated to fit StreamMA request budget]"
	limit := maxInt(0, maxBytes-len([]byte(suffix)))
	var builder strings.Builder
	for _, r := range text {
		if builder.Len()+len(string(r)) > limit {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String()) + suffix
}

func truncateStreamMATailBytes(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text
	}
	prefix := "[earlier conversation truncated to fit StreamMA request budget]\n"
	limit := maxInt(0, maxBytes-len([]byte(prefix)))
	var tail []rune
	used := 0
	runes := []rune(text)
	for i := len(runes) - 1; i >= 0; i-- {
		size := len(string(runes[i]))
		if used+size > limit {
			break
		}
		tail = append(tail, runes[i])
		used += size
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	return prefix + strings.TrimSpace(string(tail))
}

func finalStreamMAText(result streamma.RunResult) string {
	if result.Final != nil {
		if text := cleanStreamMAFinalText(result.Final.Answer.Text); text != "" {
			return text
		}
	}
	for i := len(result.Events) - 1; i >= 0; i-- {
		event := result.Events[i]
		if event.Step != nil {
			if text := cleanStreamMAFinalText(event.Step.Content.Text); text != "" {
				return text
			}
		}
	}
	return "StreamMA completed without a final answer."
}

func cleanStreamMAFinalText(text string) string {
	cleaned := strings.TrimSpace(text)
	for {
		trimmed := strings.TrimSpace(strings.TrimPrefix(cleaned, "Own step:"))
		if trimmed == cleaned {
			break
		}
		cleaned = trimmed
	}
	return cleaned
}

func (runner *Runner) notifySystem(title, body string) {
	if runner == nil || runner.ui == nil {
		return
	}
	notifier, ok := runner.ui.(ui.SystemNotifier)
	if !ok {
		return
	}
	_ = notifier.OnSystemMessage(ui.SystemEvent{Title: title, Body: body})
}

type streamMASubagentModel struct {
	subagents    StreamMASubagentRunner
	notify       func(string, string)
	trace        bool
	tools        streamMAToolsPolicy
	skillContext string
	tokenTracer  *tokentracer.Tracer
	traceStageID string
	modelLabels  func() (string, string)

	mu       sync.Mutex
	sessions map[string]string
	spans    []streamma.AgentRunSpan
}

type streamMAToolsPolicy func(agentID string) bool

func newStreamMASubagentModel(subagents StreamMASubagentRunner, notify func(string, string), trace bool, tools streamMAToolsPolicy, skillContext string, tokenTracer *tokentracer.Tracer, traceStageID string, modelLabels func() (string, string)) *streamMASubagentModel {
	return &streamMASubagentModel{
		subagents:    subagents,
		notify:       notify,
		trace:        trace,
		tools:        tools,
		skillContext: strings.TrimSpace(skillContext),
		tokenTracer:  tokenTracer,
		traceStageID: strings.TrimSpace(traceStageID),
		modelLabels:  modelLabels,
		sessions:     map[string]string{},
	}
}

func (m *streamMASubagentModel) toolsEnabled(agentID string) bool {
	if m == nil || m.tools == nil {
		return false
	}
	return m.tools(agentID)
}

func streamMAToolsPolicyForGraph(kind streamMAGraphKind) streamMAToolsPolicy {
	if kind == streamMAGraphReview {
		return func(string) bool { return false }
	}
	if kind != streamMAGraphImplementation {
		return func(string) bool { return false }
	}
	return func(agentID string) bool {
		switch strings.TrimSpace(agentID) {
		case "scout", "builder", "verifier":
			return true
		default:
			return false
		}
	}
}

func (m *streamMASubagentModel) StreamAgent(ctx context.Context, invocation streamma.AgentInvocation) (<-chan model.StreamEvent, error) {
	if m == nil || m.subagents == nil {
		return nil, fmt.Errorf("streamma subagent backend is nil")
	}
	agentID := strings.TrimSpace(invocation.AgentID)
	if agentID == "" {
		agentID = "agent"
	}
	role := strings.TrimSpace(invocation.Role)
	if role == "" {
		role = "worker"
	}
	sessionKey := streamMASessionKey(invocation.RunID, agentID)
	sessionID := m.sessionID(sessionKey)
	firstInvocation := sessionID == ""
	prompt := buildStreamMAIncrementalPrompt(invocation, firstInvocation)
	systemPrompt := invocation.SystemPrompt
	if strings.TrimSpace(m.skillContext) != "" {
		systemPrompt = strings.TrimRight(systemPrompt, "\n") + "\n\nSelected skill instructions for this StreamMA worker:\n" + m.skillContext + "\n"
	}
	startedAt := time.Now().UTC()
	stream, err := m.subagents.StreamSubagent(ctx, StreamMASubagentRequest{
		RunID:           invocation.RunID,
		SessionID:       sessionID,
		SessionKey:      sessionKey,
		InvocationIndex: invocation.InvocationIndex,
		AgentID:         agentID,
		Role:            role,
		Description:     "streamma " + agentID + " " + role,
		SystemPrompt:    systemPrompt,
		Problem:         invocation.Problem,
		InboundFrom:     invocation.InboundFrom,
		InboundStep:     cloneLoopStreamMAStep(invocation.InboundStep),
		Boundary:        invocation.Boundary,
		RequireBoundary: invocation.RequireBoundary,
		Prompt:          prompt,
		ContextMode:     "empty",
		DisableTools:    !m.toolsEnabled(agentID),
	})
	if err != nil {
		return nil, err
	}
	spanIndex := m.startRunLoggerSpan(agentID, startedAt)
	if assigned := strings.TrimSpace(stream.SessionID); assigned != "" {
		sessionID = m.rememberSession(sessionKey, assigned)
	}
	if m.notify != nil {
		if m.trace {
			m.notify("streamma-trace", fmt.Sprintf("subagent.started agent=%s role=%s invocation=%d attempt=%d session=%s%s", agentID, role, invocation.InvocationIndex, invocation.Attempt, sessionID, streamMAAgentTraceFields(stream)))
		} else {
			m.notify(agentID, fmt.Sprintf("(%s) started", role))
		}
	}
	if m.tokenTracer != nil {
		m.tokenTracer.RecordEvent("streamma.subagent_start", map[string]any{
			"stage_id":          m.traceStageID,
			"agent_id":          agentID,
			"role":              role,
			"invocation_index":  invocation.InvocationIndex,
			"session_id":        sessionID,
			"inbound_from":      invocation.InboundFrom,
			"input_event_count": len(invocation.InputEvents),
		})
	}
	return m.wrapStream(ctx, invocation, stream, spanIndex), nil
}

func (m *streamMASubagentModel) sessionID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionKey]
}

func (m *streamMASubagentModel) rememberSession(sessionKey, sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := strings.TrimSpace(m.sessions[sessionKey]); existing != "" {
		return existing
	}
	m.sessions[sessionKey] = sessionID
	return sessionID
}

func (m *streamMASubagentModel) startRunLoggerSpan(agentID string, startedAt time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = append(m.spans, streamma.AgentRunSpan{
		AgentID:   strings.TrimSpace(agentID),
		StartedAt: startedAt,
	})
	return len(m.spans) - 1
}

func (m *streamMASubagentModel) finishRunLoggerSpan(index int, finishedAt time.Time, usage *model.Usage) {
	if index < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= len(m.spans) {
		return
	}
	m.spans[index].FinishedAt = finishedAt
	if usage != nil {
		m.spans[index].Usage = streamma.StepUsage{
			InputTokens:  maxInt(0, usage.PromptTokenCount()),
			CachedTokens: maxInt(0, usage.CacheHitTokens()),
			OutputTokens: maxInt(0, usage.CompletionTokenCount()),
		}
	}
}

func (m *streamMASubagentModel) runLoggerSpans() []streamma.AgentRunSpan {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]streamma.AgentRunSpan(nil), m.spans...)
}

func (m *streamMASubagentModel) wrapStream(ctx context.Context, invocation streamma.AgentInvocation, stream StreamMASubagentStream, spanIndex int) <-chan model.StreamEvent {
	out := make(chan model.StreamEvent)
	go func() {
		defer close(out)
		var lastUsage *model.Usage
		defer func() {
			m.finishRunLoggerSpan(spanIndex, time.Now().UTC(), lastUsage)
			agentID := strings.TrimSpace(invocation.AgentID)
			role := strings.TrimSpace(invocation.Role)
			if m.notify == nil {
				if m.tokenTracer != nil {
					m.tokenTracer.RecordEvent("streamma.subagent_end", map[string]any{
						"stage_id":         m.traceStageID,
						"agent_id":         agentID,
						"role":             role,
						"invocation_index": invocation.InvocationIndex,
						"session_id":       stream.SessionID,
					})
				}
				return
			}
			detail := fmt.Sprintf("(%s) finished", role)
			if strings.TrimSpace(stream.SessionID) != "" {
				detail += " session=" + stream.SessionID
			}
			if m.trace {
				traceDetail := fmt.Sprintf("subagent.finished agent=%s role=%s invocation=%d", agentID, role, invocation.InvocationIndex)
				if strings.TrimSpace(stream.SessionID) != "" {
					traceDetail += " session=" + stream.SessionID
				}
				traceDetail += formatStreamMAUsageTrace(lastUsage)
				m.notify("streamma-trace", traceDetail)
			} else {
				m.notify(agentID, detail)
			}
			if m.tokenTracer != nil {
				m.tokenTracer.RecordEvent("streamma.subagent_end", map[string]any{
					"stage_id":         m.traceStageID,
					"agent_id":         agentID,
					"role":             role,
					"invocation_index": invocation.InvocationIndex,
					"session_id":       stream.SessionID,
				})
			}
		}()
		var previousUsage tokentracer.Usage
		for ev := range stream.Events {
			if ev.Usage != nil {
				usage := *ev.Usage
				lastUsage = &usage
				currentUsage := tokentracer.UsageFromModelUsage(usage)
				delta := currentUsage.Delta(previousUsage)
				previousUsage = currentUsage
				if m.tokenTracer != nil && !delta.Empty() {
					provider, modelName := "", ""
					if m.modelLabels != nil {
						provider, modelName = m.modelLabels()
					}
					agentID := strings.TrimSpace(invocation.AgentID)
					role := strings.TrimSpace(invocation.Role)
					m.tokenTracer.RecordAPICall(
						m.traceStageID,
						"",
						agentID,
						streamMATraceAgentName(agentID, role),
						provider,
						modelName,
						delta,
						map[string]any{
							"source":           "streamma_subagent",
							"run_id":           invocation.RunID,
							"role":             role,
							"invocation_index": invocation.InvocationIndex,
							"session_id":       stream.SessionID,
						},
					)
				}
			}
			if !sendStreamMAEvent(ctx, out, ev) {
				drainStreamMAEvents(stream.Events, &lastUsage)
				return
			}
		}
	}()
	return out
}

func drainStreamMAEvents(events <-chan model.StreamEvent, lastUsage **model.Usage) {
	for ev := range events {
		if ev.Usage != nil && lastUsage != nil {
			usage := *ev.Usage
			*lastUsage = &usage
		}
	}
}

func sendStreamMAEvent(ctx context.Context, out chan<- model.StreamEvent, ev model.StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func streamMASessionKey(runID, agentID string) string {
	runID = strings.TrimSpace(runID)
	agentID = strings.TrimSpace(agentID)
	if runID == "" {
		runID = "run"
	}
	if agentID == "" {
		agentID = "agent"
	}
	return runID + ":" + agentID
}

func streamMAAgentTraceFields(stream StreamMASubagentStream) string {
	var parts []string
	if name := strings.TrimSpace(stream.AgentName); name != "" {
		parts = append(parts, "name="+name)
	}
	if color := strings.TrimSpace(stream.AgentColor); color != "" {
		parts = append(parts, "color="+color)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func buildStreamMAIncrementalPrompt(invocation streamma.AgentInvocation, firstInvocation bool) string {
	var builder strings.Builder
	boundary := strings.TrimSpace(invocation.Boundary)
	if boundary == "" {
		boundary = streamma.DefaultBoundary
	}
	if firstInvocation {
		builder.WriteString("StreamMA logical agent bootstrap. Treat this subagent session as the persistent ctx_a for this logical agent.\n")
		builder.WriteString("Future invocations will append only new inbound steps to this same session; keep your prior session history as prefix context.\n\n")
	} else {
		builder.WriteString("StreamMA incremental invocation. Continue from the existing ctx_a in this same subagent session.\n")
		builder.WriteString("Do not restate the original problem or earlier inbound steps unless needed for a concise next step.\n\n")
	}
	builder.WriteString("Run ID: ")
	builder.WriteString(strings.TrimSpace(invocation.RunID))
	builder.WriteString("\nInvocation: ")
	builder.WriteString(fmt.Sprintf("%d", invocation.InvocationIndex))
	builder.WriteString("\n")
	builder.WriteString("Agent ID: ")
	builder.WriteString(strings.TrimSpace(invocation.AgentID))
	builder.WriteString("\nRole: ")
	builder.WriteString(strings.TrimSpace(invocation.Role))
	builder.WriteString("\n\n")
	if firstInvocation {
		builder.WriteString("Original problem:\n")
		builder.WriteString(truncateStreamMAHeadBytes(strings.TrimSpace(invocation.Problem), streamMAProblemMaxBytes))
		builder.WriteString("\n\n")
	}
	if invocation.InboundStep != nil {
		builder.WriteString("New inbound step from ")
		builder.WriteString(strings.TrimSpace(invocation.InboundFrom))
		builder.WriteString(":\n")
		builder.WriteString(truncateStreamMAHeadBytes(strings.TrimSpace(invocation.InboundStep.Content.Text), streamMAInboundStepMaxBytes))
		builder.WriteString("\n\n")
	} else if firstInvocation {
		builder.WriteString("Initial problem delivery. Start producing useful public reasoning steps for this agent role.\n\n")
	} else {
		builder.WriteString("No new inbound step is attached; continue only if this role can make useful progress from existing ctx_a.\n\n")
	}
	builder.WriteString("Produce one or more public StreamMA steps. Each step must end with a standalone ")
	builder.WriteString(boundary)
	builder.WriteString(" line. The boundary line must contain exactly ")
	builder.WriteString(boundary)
	builder.WriteString(".")
	if invocation.RequireBoundary {
		builder.WriteString(" This runtime is strict and will fail this invocation if the final emitted step is not closed by the exact boundary. The last non-whitespace line of your assistant message must be exactly ")
		builder.WriteString(boundary)
		builder.WriteString(", with no text after it.")
	}
	builder.WriteString("\n")
	return builder.String()
}

func cloneLoopStreamMAStep(step *streamma.StepPacket) *streamma.StepPacket {
	if step == nil {
		return nil
	}
	cloned := *step
	if cloned.Dependencies.InputEvents != nil {
		cloned.Dependencies.InputEvents = append([]string(nil), cloned.Dependencies.InputEvents...)
	}
	return &cloned
}

func formatStreamMAUsageTrace(usage *model.Usage) string {
	if usage == nil {
		return " usage=unknown"
	}
	parts := []string{
		fmt.Sprintf("input=%d", usage.PromptTokenCount()),
		fmt.Sprintf("output=%d", usage.CompletionTokenCount()),
	}
	cacheKnown := usageHasCacheSignal(*usage)
	if cacheKnown {
		parts = append(parts, fmt.Sprintf("cache_hit=%d", usage.CacheHitTokens()))
	} else {
		parts = append(parts, "cache_hit=unknown")
	}
	if usage.PromptCacheMissTokens != 0 {
		parts = append(parts, fmt.Sprintf("cache_miss=%d", usage.PromptCacheMissTokens))
	} else {
		parts = append(parts, "cache_miss=unknown")
	}
	if cacheKnown && usage.PromptCacheMissTokens != 0 {
		denominator := usage.CacheHitTokens() + usage.PromptCacheMissTokens
		if denominator > 0 {
			parts = append(parts, fmt.Sprintf("hit_rate=%.1f%%", float64(usage.CacheHitTokens())*100/float64(denominator)))
		} else {
			parts = append(parts, "hit_rate=unknown")
		}
	} else {
		parts = append(parts, "hit_rate=unknown")
	}
	return " usage(" + strings.Join(parts, " ") + ")"
}

func usageHasCacheSignal(usage model.Usage) bool {
	return usage.PromptCacheHitTokens != 0 ||
		usage.PromptCacheMissTokens != 0 ||
		usage.CacheReadInputTokens != 0 ||
		usage.CacheCreationInputTokens != 0 ||
		usage.PromptTokensDetails.CachedTokens != 0 ||
		usage.InputTokensDetails.CachedTokens != 0 ||
		usage.PromptTokensDetails.CacheReadTokens != 0 ||
		usage.InputTokensDetails.CacheReadTokens != 0 ||
		usage.PromptTokensDetails.CacheReadInputTokens != 0 ||
		usage.InputTokensDetails.CacheReadInputTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationTokens != 0 ||
		usage.InputTokensDetails.CacheCreationTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationInputTokens != 0 ||
		usage.InputTokensDetails.CacheCreationInputTokens != 0
}

func describeStreamMAGraph(spec streamma.GraphSpec) string {
	ids := make([]string, 0, len(spec.Agents))
	for _, agent := range spec.Agents {
		ids = append(ids, agent.ID)
	}
	return strings.Join(ids, " -> ")
}

func streamMAAgentIDs(spec streamma.GraphSpec) []string {
	ids := make([]string, 0, len(spec.Agents))
	for _, agent := range spec.Agents {
		ids = append(ids, agent.ID)
	}
	return ids
}
