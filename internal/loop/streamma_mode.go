package loop

import (
	"context"
	"fmt"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/streamma"
	"gocode/internal/ui"
	"strings"
	"time"
)

const streamMACommand = "/streamma"

type StreamMASubagentRequest struct {
	AgentID     string
	Role        string
	Description string
	Prompt      string
	ContextMode string
}

type StreamMASubagentResult struct {
	Content        string
	SessionID      string
	TranscriptPath string
	OutputPath     string
}

type StreamMASubagentRunner interface {
	RunStreamMASubagent(ctx context.Context, req StreamMASubagentRequest) (StreamMASubagentResult, error)
}

func (runner *Runner) SetStreamMASubagentRunner(subagents StreamMASubagentRunner) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.streamMASubagents = subagents
}

func parseStreamMAInvocation(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}
	if trimmed == streamMACommand {
		return "", true
	}
	prefix := streamMACommand + " "
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), true
}

func (runner *Runner) runStreamMATurn(ctx context.Context, input, task string) (message.Message, error) {
	if strings.TrimSpace(task) == "" {
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

	spec := defaultStreamMAGraph(task)
	runner.notifySystem("streamma", fmt.Sprintf("running %s with subagent-backed Exact+END_STEP workers", describeStreamMAGraph(spec)))
	result, err := streamma.RunGraph(ctx, spec, newStreamMASubagentModel(subagents, runner.notifySystem), buildStreamMAProblem(task, history))
	if err != nil {
		return message.Message{}, err
	}
	if result.Status != streamma.RunCompleted {
		return message.Message{}, fmt.Errorf("streamma run %s ended with status %s: %s", result.RunID, result.Status, result.Error)
	}

	finalText := finalStreamMAText(result)
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
	return assistant, nil
}

func (runner *Runner) currentStreamMASubagents() StreamMASubagentRunner {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.streamMASubagents
}

func defaultStreamMAGraph(task string) streamma.GraphSpec {
	if streamMATaskLooksImplementationHeavy(task) {
		return streamma.GraphSpec{
			RunID:    fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano()),
			Protocol: streamma.ProtocolStream,
			StepPolicy: streamma.StepPolicy{
				Boundary:     streamma.DefaultBoundary,
				MaxStepBytes: 64 * 1024,
			},
			Agents: []streamma.AgentSpec{
				{ID: "planner", Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Create a concise implementation plan and success criteria. Do not modify files.")},
				{ID: "scout", Role: "workspace_scout", SystemPrompt: streamMAAgentPrompt("scout", "workspace_scout", "Inspect the workspace and identify the safest place and stack for the requested implementation. Prefer read-only investigation.")},
				{ID: "builder", Role: "implementation_builder", SystemPrompt: streamMAAgentPrompt("builder", "implementation_builder", "Use the inbound plan and workspace facts to implement the requested artifact with available tools. For temporary demos, create files under a clearly named temporary workspace directory.")},
				{ID: "verifier", Role: "runtime_verifier", SystemPrompt: streamMAAgentPrompt("verifier", "runtime_verifier", "Run focused verification for the builder output, report failures, and fix only when necessary.")},
				{ID: "finalizer", Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Synthesize the current implemented state, paths, commands, verification results, and remaining caveats for the user.")},
			},
			Edges: []streamma.EdgeSpec{
				{From: "planner", To: "builder"},
				{From: "scout", To: "builder"},
				{From: "builder", To: "verifier"},
				{From: "builder", To: "finalizer"},
				{From: "verifier", To: "finalizer"},
			},
		}
	}
	return streamma.GraphSpec{
		RunID:    fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano()),
		Protocol: streamma.ProtocolStream,
		StepPolicy: streamma.StepPolicy{
			Boundary:     streamma.DefaultBoundary,
			MaxStepBytes: 32 * 1024,
		},
		Agents: []streamma.AgentSpec{
			{ID: "planner", Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Break down the task into early useful reasoning steps.")},
			{ID: "solver", Role: "reasoning_solver", SystemPrompt: streamMAAgentPrompt("solver", "reasoning_solver", "Consume planner steps and solve the task incrementally.")},
			{ID: "critic", Role: "quality_critic", SystemPrompt: streamMAAgentPrompt("critic", "quality_critic", "Check solver steps for gaps, contradictions, and missing evidence.")},
			{ID: "finalizer", Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Produce the final user-facing answer from the best available inbound steps.")},
		},
		Edges: []streamma.EdgeSpec{
			{From: "planner", To: "solver"},
			{From: "planner", To: "critic"},
			{From: "solver", To: "critic"},
			{From: "critic", To: "finalizer"},
			{From: "solver", To: "finalizer"},
		},
	}
}

func streamMATaskLooksImplementationHeavy(task string) bool {
	lower := strings.ToLower(task)
	for _, marker := range []string{
		"implement", "build", "create", "make", "code", "app", "game", "test", "run",
		"实现", "制作", "创建", "写", "代码", "项目", "游戏", "测试", "运行", "修复", "临时",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func streamMAAgentPrompt(agentID, role, mission string) string {
	return fmt.Sprintf("streamma_agent_id=%s\nstreamma_role=%s\n%s\n%s", agentID, role, mission, streamMAOutputContract)
}

const streamMAOutputContract = `Output contract:
- Use natural language only.
- End every reasoning step with a line that contains exactly END_STEP.
- END_STEP must be on its own line with no spaces, punctuation, indentation, JSON, or Markdown fences.
- You are a real subagent worker with your normal tools available.
- Use tools only when your role needs them and after you have enough verified plan/context.
- Keep each step public and concise; do not reveal private chain-of-thought.`

func buildStreamMAProblem(task string, history []message.Message) string {
	var builder strings.Builder
	builder.WriteString("Current user task:\n")
	builder.WriteString(strings.TrimSpace(task))
	if context := streamMAConversationContext(history); context != "" {
		builder.WriteString("\n\nConversation context:\n")
		builder.WriteString(context)
	}
	return builder.String()
}

func streamMAConversationContext(history []message.Message) string {
	const maxRunes = 6000
	var lines []string
	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if strings.HasPrefix(content, streamMACommand) {
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
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[len(runes)-maxRunes:])
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
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == streamma.DefaultBoundary {
			continue
		}
		lines = append(lines, line)
	}
	cleaned := strings.TrimSpace(strings.Join(lines, "\n"))
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
	subagents StreamMASubagentRunner
	notify    func(string, string)
}

func newStreamMASubagentModel(subagents StreamMASubagentRunner, notify func(string, string)) *streamMASubagentModel {
	return &streamMASubagentModel{subagents: subagents, notify: notify}
}

func (m *streamMASubagentModel) StreamMessage(ctx context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	if m == nil || m.subagents == nil {
		return nil, fmt.Errorf("streamma subagent backend is nil")
	}
	agentID, role := streamMAAgentMetadata(messages)
	prompt := buildStreamMASubagentPrompt(agentID, role, messages)
	if m.notify != nil {
		m.notify("streamma", fmt.Sprintf("subagent %s (%s) started", agentID, role))
	}
	result, err := m.subagents.RunStreamMASubagent(ctx, StreamMASubagentRequest{
		AgentID:     agentID,
		Role:        role,
		Description: "streamma " + agentID + " " + role,
		Prompt:      prompt,
		ContextMode: "empty",
	})
	if err != nil {
		return nil, err
	}
	content := ensureStreamMABoundary(result.Content)
	if m.notify != nil {
		detail := fmt.Sprintf("subagent %s finished", agentID)
		if strings.TrimSpace(result.SessionID) != "" {
			detail += " session=" + result.SessionID
		}
		m.notify("streamma", detail)
	}
	ch := make(chan model.StreamEvent, 2)
	ch <- model.StreamEvent{Delta: content}
	ch <- model.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func streamMAAgentMetadata(messages []message.Message) (string, string) {
	agentID := "agent"
	role := "worker"
	if len(messages) == 0 {
		return agentID, role
	}
	system := messages[0].Content
	for _, line := range strings.Split(system, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "streamma_agent_id=") {
			agentID = strings.TrimSpace(strings.TrimPrefix(line, "streamma_agent_id="))
		}
		if strings.HasPrefix(line, "streamma_role=") {
			role = strings.TrimSpace(strings.TrimPrefix(line, "streamma_role="))
		}
	}
	if agentID == "" {
		agentID = "agent"
	}
	if role == "" {
		role = "worker"
	}
	return agentID, role
}

func buildStreamMASubagentPrompt(agentID, role string, messages []message.Message) string {
	var builder strings.Builder
	builder.WriteString("You are a StreamMA subagent worker.\n")
	builder.WriteString("Agent ID: ")
	builder.WriteString(agentID)
	builder.WriteString("\nRole: ")
	builder.WriteString(role)
	builder.WriteString("\n\n")
	builder.WriteString("Follow the conversation below as your complete task context. Produce one or more public StreamMA steps. Each step must end with a standalone END_STEP line.\n\n")
	for _, msg := range messages {
		builder.WriteString(strings.ToUpper(string(msg.Role)))
		builder.WriteString(":\n")
		builder.WriteString(msg.Content)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func ensureStreamMABoundary(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "No useful subagent output.\n" + streamma.DefaultBoundary + "\n"
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == streamma.DefaultBoundary {
			return content + "\n"
		}
	}
	return content + "\n" + streamma.DefaultBoundary + "\n"
}

func describeStreamMAGraph(spec streamma.GraphSpec) string {
	ids := make([]string, 0, len(spec.Agents))
	for _, agent := range spec.Agents {
		ids = append(ids, agent.ID)
	}
	return strings.Join(ids, " -> ")
}
