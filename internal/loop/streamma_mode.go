package loop

import (
	"context"
	"fmt"
	"gocode/internal/message"
	"gocode/internal/streamma"
	"gocode/internal/ui"
	"strings"
	"time"
)

const streamMACommand = "/streamma"

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

	history, injectedSupplements := runner.buildTurnHistory(input)
	committed := false
	defer func() {
		if !committed && len(injectedSupplements) > 0 {
			runner.prependSupplements(injectedSupplements)
		}
	}()

	runner.notifySystem("streamma", "running A -> B -> D with Exact+END_STEP; tools are disabled inside the StreamMA runtime")
	result, err := streamma.RunGraph(ctx, defaultStreamMAGraph(), runner.model, buildStreamMAProblem(task, history))
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

func defaultStreamMAGraph() streamma.GraphSpec {
	return streamma.GraphSpec{
		RunID:    fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano()),
		Protocol: streamma.ProtocolStream,
		StepPolicy: streamma.StepPolicy{
			Boundary:     streamma.DefaultBoundary,
			MaxStepBytes: 32 * 1024,
		},
		Agents: []streamma.AgentSpec{
			{
				ID:           "A",
				Role:         "source_planner",
				SystemPrompt: streamMAPlannerPrompt,
			},
			{
				ID:           "B",
				Role:         "downstream_reviewer",
				SystemPrompt: streamMAReviewerPrompt,
			},
			{
				ID:           "D",
				Role:         "finalizer",
				SystemPrompt: streamMAFinalizerPrompt,
			},
		},
		Edges: []streamma.EdgeSpec{
			{From: "A", To: "B"},
			{From: "B", To: "D"},
		},
	}
}

const streamMAOutputContract = `Output contract:
- Use natural language only.
- End every reasoning step with a line that contains exactly END_STEP.
- END_STEP must be on its own line with no spaces, punctuation, indentation, JSON, or Markdown fences.
- Do not call or request tools; StreamMA mode is reasoning-only.`

const streamMAPlannerPrompt = `You are StreamMA agent A, the source planner.
Read the user's task and produce short, useful early reasoning steps that can help downstream agents immediately.
Prefer concrete observations, decomposition, assumptions, and candidate approaches.
` + streamMAOutputContract

const streamMAReviewerPrompt = `You are StreamMA agent B, the downstream reviewer.
Consume inbound steps from A as soon as they arrive. Verify, refine, and convert them into actionable solution steps for the finalizer.
Avoid waiting for a complete upstream answer; each invocation should add one concise piece of useful progress.
` + streamMAOutputContract

const streamMAFinalizerPrompt = `You are StreamMA agent D, the finalizer.
Consume inbound steps from B and produce the best current user-facing answer.
On later invocations, replace earlier drafts with a more complete final answer.
Keep the answer concise, direct, and in the user's language when obvious.
` + streamMAOutputContract

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
		if text := strings.TrimSpace(result.Final.Answer.Text); text != "" {
			return text
		}
	}
	for i := len(result.Events) - 1; i >= 0; i-- {
		event := result.Events[i]
		if event.Step != nil {
			if text := strings.TrimSpace(event.Step.Content.Text); text != "" {
				return text
			}
		}
	}
	return "StreamMA completed without a final answer."
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
