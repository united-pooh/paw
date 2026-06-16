package loop

import (
	"context"
	"encoding/json"
	"errors"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/tool"
	"gocode/internal/ui"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRound struct {
	events []model.StreamEvent
	err    error
}

type fakeModel struct {
	rounds []fakeRound
	calls  [][]message.Message
}

func (m *fakeModel) StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error) {
	copied := append([]message.Message(nil), messages...)
	m.calls = append(m.calls, copied)

	index := len(m.calls) - 1
	if index >= len(m.rounds) {
		return nil, errors.New("unexpected model call")
	}

	round := m.rounds[index]
	if round.err != nil {
		return nil, round.err
	}

	ch := make(chan model.StreamEvent, len(round.events))
	for _, ev := range round.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

type blockingModel struct {
	streams []chan model.StreamEvent
	started chan int
	calls   [][]message.Message
}

func (m *blockingModel) StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error) {
	copied := append([]message.Message(nil), messages...)
	m.calls = append(m.calls, copied)

	index := len(m.calls) - 1
	if index >= len(m.streams) {
		return nil, errors.New("unexpected model call")
	}
	if m.started != nil {
		m.started <- index
	}
	return m.streams[index], nil
}

type fakeUI struct {
	deltas      []string
	thinking    []string
	toolCalls   []ui.ToolCallEvent
	toolResults []ui.ToolResultEvent
	doneCount   int
}

func (u *fakeUI) OnAssistantDelta(text string) error {
	u.deltas = append(u.deltas, text)
	return nil
}

func (u *fakeUI) OnThinkingDelta(text string) error {
	u.thinking = append(u.thinking, text)
	return nil
}

func (u *fakeUI) OnToolCall(event ui.ToolCallEvent) error {
	u.toolCalls = append(u.toolCalls, event)
	return nil
}

func (u *fakeUI) OnToolResult(event ui.ToolResultEvent) error {
	u.toolResults = append(u.toolResults, event)
	return nil
}

func (u *fakeUI) OnDone() error {
	u.doneCount++
	return nil
}

type fakeTool struct {
	name   string
	output string
	err    error
	input  json.RawMessage
}

type fakeStore struct {
	history   []message.Message
	loadCalls int
	appends   [][]message.Message
}

func (s *fakeStore) Append(ctx context.Context, sessionID string, msgs ...message.Message) error {
	copied := append([]message.Message(nil), msgs...)
	s.appends = append(s.appends, copied)
	return nil
}

func (s *fakeStore) LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	s.loadCalls++
	return append([]message.Message(nil), s.history...), nil
}

func (t *fakeTool) Name() string {
	return t.name
}

func (t *fakeTool) Description() string {
	return "fake tool"
}

func (t *fakeTool) InputSchema() json.RawMessage {
	return nil
}

func (t *fakeTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	t.input = append(json.RawMessage(nil), input...)
	return t.output, nil
}

func TestRunTurnStreamsAndReturnsFinalMessage(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "he"},
					{Delta: "llo"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Role != message.RoleAssistant {
		t.Fatalf("msg.Role = %q, want %q", msg.Role, message.RoleAssistant)
	}
	if msg.Content != "hello" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "hello")
	}
	if got := len(ui.deltas); got != 2 {
		t.Fatalf("len(ui.deltas) = %d, want 2", got)
	}
	if ui.deltas[0] != "he" || ui.deltas[1] != "llo" {
		t.Fatalf("ui.deltas = %#v, want [\"he\", \"llo\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnUsesPromptBuilderWithProjectInstructions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectInstructionFile), []byte("prefer concise answers"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "ok"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunnerWithInstructionRoot(model, ui, tool.NewRegistry(), nil, "", root)

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if len(model.calls) != 1 || len(model.calls[0]) == 0 {
		t.Fatalf("model.calls = %#v", model.calls)
	}
	system := model.calls[0][0]
	if system.Role != message.RoleSystem {
		t.Fatalf("first role = %q, want system", system.Role)
	}
	for _, want := range []string{"Project instructions from AGENTS.md", "prefer concise answers", "Answer with plain text."} {
		if !strings.Contains(system.Content, want) {
			t.Fatalf("system prompt = %q, want %q", system.Content, want)
		}
	}
}

func TestRunTurnReturnsSyncModelError(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{err: errors.New("boom")},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "hello")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("RunTurn() error = %v, want boom", err)
	}
	if len(ui.deltas) != 0 {
		t.Fatalf("len(ui.deltas) = %d, want 0", len(ui.deltas))
	}
	if ui.doneCount != 0 {
		t.Fatalf("ui.doneCount = %d, want 0", ui.doneCount)
	}
}

func TestRunTurnFlushesOnStreamErrorAfterOutput(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "he"},
					{Err: errors.New("boom")},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "hello")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("RunTurn() error = %v, want boom", err)
	}
	if got := len(ui.deltas); got != 1 || ui.deltas[0] != "he" {
		t.Fatalf("ui.deltas = %#v, want [\"he\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnAllowsEmptyAssistantContent(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Role != message.RoleAssistant {
		t.Fatalf("msg.Role = %q, want %q", msg.Role, message.RoleAssistant)
	}
	if msg.Content != "" {
		t.Fatalf("msg.Content = %q, want empty", msg.Content)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunnerForwardsThinkingEventsToUI(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Thinking: "plan"},
			{Delta: "answer"},
			{Done: true},
		}},
	}}
	output := &fakeUI{}
	runner := NewRunner(streamer, output, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if got := strings.Join(output.thinking, ""); got != "plan" {
		t.Fatalf("thinking = %q, want plan", got)
	}
	if got := strings.Join(output.deltas, ""); got != "answer" {
		t.Fatalf("deltas = %q, want answer", got)
	}
}

func TestContextStatsUsesOnlyRealUsageAndKeepsLastKnownDuringNextTurn(t *testing.T) {
	ui := &fakeUI{}
	first := make(chan model.StreamEvent, 1)
	first <- model.StreamEvent{
		Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5, PromptCacheHitTokens: 12},
		Done:  true,
	}
	close(first)
	second := make(chan model.StreamEvent, 1)
	streamer := &blockingModel{
		streams: []chan model.StreamEvent{first, second},
		started: make(chan int, 2),
	}
	runner := NewRunner(streamer, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if got := <-streamer.started; got != 0 {
		t.Fatalf("first turn started index = %d, want 0", got)
	}
	firstStats := runner.ContextStats(1024, "")
	if firstStats.UsedTokens != 105 || firstStats.CacheTokens != 12 {
		t.Fatalf("first ContextStats() = %#v, want real usage 105/cache 12", firstStats)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "second")
		errCh <- err
	}()
	if got := <-streamer.started; got != 1 {
		t.Fatalf("second turn started index = %d, want 1", got)
	}

	stats := runner.ContextStats(1024, "draft")
	if stats.UsedTokens != 105 || stats.CacheTokens != 12 {
		t.Fatalf("ContextStats() = %#v, want last real usage without draft estimate", stats)
	}

	second <- model.StreamEvent{Done: true}
	close(second)
	if err := <-errCh; err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}
}

func TestContextStatsAccumulatesSessionUsageAcrossModelRequests(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{InputTokens: 100, CacheReadInputTokens: 40}},
					{Usage: &model.Usage{OutputTokens: 5}},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{PromptTokens: 150, CompletionTokens: 7, TotalTokens: 157, PromptCacheHitTokens: 30}},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if _, err := runner.RunTurn(context.Background(), "second"); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 157 || stats.CacheTokens != 30 {
		t.Fatalf("ContextStats() = %#v, want latest context usage 157/cache 30", stats)
	}
}

func TestContextStatsDoesNotDoubleCountCumulativeStreamUsageUpdates(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{InputTokens: 100, CacheReadInputTokens: 10}},
					{Usage: &model.Usage{OutputTokens: 2}},
					{Usage: &model.Usage{OutputTokens: 5}},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 115 || stats.CacheTokens != 10 {
		t.Fatalf("ContextStats() = %#v, want one request usage 115/cache 10", stats)
	}
}

func TestContextStatsDoesNotEstimateWhenUsageIsUnknown(t *testing.T) {
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")

	stats := runner.ContextStats(1024, "draft prompt")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 || stats.LimitTokens != 1024 {
		t.Fatalf("ContextStats() = %#v, want zero usage until provider reports usage", stats)
	}
}

func TestContextStatsUsesActualUsageWhenKnown(t *testing.T) {
	tests := []struct {
		name      string
		usage     model.Usage
		wantUsed  int
		wantCache int
	}{
		{
			name: "anthropic",
			usage: model.Usage{
				InputTokens:              100,
				CacheCreationInputTokens: 4,
				CacheReadInputTokens:     40,
				OutputTokens:             6,
			},
			wantUsed:  150,
			wantCache: 40,
		},
		{
			name: "openai-compatible",
			usage: model.Usage{
				PromptTokens:          100,
				CompletionTokens:      2,
				TotalTokens:           102,
				PromptCacheHitTokens:  40,
				PromptCacheMissTokens: 60,
			},
			wantUsed:  102,
			wantCache: 40,
		},
		{
			name: "openai-nested-cache-details",
			usage: model.Usage{
				PromptTokens:     100,
				CompletionTokens: 2,
				TotalTokens:      102,
				PromptTokensDetails: model.TokenDetails{
					CachedTokens: 32,
				},
			},
			wantUsed:  102,
			wantCache: 32,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")
			runner.usage = tt.usage
			runner.usageKnown = true

			stats := runner.ContextStats(1024, "draft should not be estimated")
			if stats.UsedTokens != tt.wantUsed {
				t.Fatalf("UsedTokens = %d, want %d", stats.UsedTokens, tt.wantUsed)
			}
			if stats.CacheTokens != tt.wantCache {
				t.Fatalf("CacheTokens = %d, want %d", stats.CacheTokens, tt.wantCache)
			}
		})
	}
}

func TestResetHistoryClearsContextUsage(t *testing.T) {
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")
	runner.usage = model.Usage{PromptTokens: 100, CompletionTokens: 20, PromptCacheHitTokens: 10}
	runner.usageKnown = true
	runner.sessionUsage = model.Usage{PromptTokens: 200, CompletionTokens: 40, PromptCacheHitTokens: 20}
	runner.sessionUsageKnown = true

	runner.ResetHistory()

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 {
		t.Fatalf("ContextStats() = %#v, want reset usage", stats)
	}
}

func TestRunTurnExecutesToolAndReturnsFinalAnswer(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "go.mod loaded"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module gocode"})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read go.mod")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "go.mod loaded" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "go.mod loaded")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if got := len(ui.deltas); got != 1 || ui.deltas[0] != "go.mod loaded" {
		t.Fatalf("ui.deltas = %#v, want [\"go.mod loaded\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
	if got := len(ui.toolCalls); got != 1 {
		t.Fatalf("len(ui.toolCalls) = %d, want 1", got)
	}
	if ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls[0].Name = %q, want %q", ui.toolCalls[0].Name, "Read")
	}
	if got := len(ui.toolResults); got != 1 {
		t.Fatalf("len(ui.toolResults) = %d, want 1", got)
	}
	if ui.toolResults[0].Name != "Read" || ui.toolResults[0].IsError {
		t.Fatalf("ui.toolResults[0] = %#v, want successful Read result", ui.toolResults[0])
	}

	foundToolResult := false
	for _, msg := range model.calls[1] {
		if strings.Contains(msg.Content, "TOOL_RESULT:") && strings.Contains(msg.Content, "module gocode") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second round messages do not include tool result: %#v", model.calls[1])
	}
}

func TestRunTurnExecutesToolUseWrappedInMarkdownFence(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "```json\n"},
					{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
					{Delta: "\n```"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "go.mod loaded"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module gocode"})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read go.mod")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "go.mod loaded" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "go.mod loaded")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls = %#v, want single Read call", ui.toolCalls)
	}
	if len(ui.deltas) != 1 || ui.deltas[0] != "go.mod loaded" {
		t.Fatalf("ui.deltas = %#v, want only final answer output", ui.deltas)
	}
}

func TestRunTurnExecutesToolUseWrappedInMarkdownFenceWithPreamble(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "我将依次使用所有可用的工具来展示它们的功能：\n\n## 1. 首先使用 `LS` 查看当前目录结构\n\n```json\n"},
					{Delta: `{"type":"tool_use","id":"call_22","name":"LS","input":{"path":"."}}`},
					{Delta: "\n```"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "list complete"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "LS", output: ".\n.."})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "尝试使用你所有的工具")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "list complete" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "list complete")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "LS" {
		t.Fatalf("ui.toolCalls = %#v, want single LS call", ui.toolCalls)
	}
	if len(ui.toolResults) != 1 || ui.toolResults[0].Name != "LS" {
		t.Fatalf("ui.toolResults = %#v, want single LS result", ui.toolResults)
	}
}

func TestRunTurnExecutesInvokeDSMLToolUseWithPreamble(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "好的，我来读出所有代码文件的内容。\n\n"},
					{Delta: `<invoke name="Bash">`},
					{Delta: "\n<｜｜DSML｜｜ name=\"command\" string=\"true\">cd . && find . -type f -name '*.go' | sort</｜｜DSML｜｜>\n"},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "代码文件列表已读取"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	bash := &fakeTool{name: "Bash", output: "internal/loop/runner.go"}
	registry.Register(bash)
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "读代码文件内容并cat 输出")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "代码文件列表已读取" {
		t.Fatalf("msg.Content = %q, want final answer", msg.Content)
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Bash" {
		t.Fatalf("ui.toolCalls = %#v, want single Bash call", ui.toolCalls)
	}

	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bash.input, &input); err != nil {
		t.Fatalf("Bash input unmarshal error = %v; input=%s", err, bash.input)
	}
	if input.Command != "cd . && find . -type f -name '*.go' | sort" {
		t.Fatalf("input.Command = %q", input.Command)
	}
	if len(ui.deltas) != 1 || ui.deltas[0] != "代码文件列表已读取" {
		t.Fatalf("ui.deltas = %#v, want only final answer output", ui.deltas)
	}
}

func TestRunTurnParsesInvokeDSMLNonStringParameters(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `<invoke name="Grep">`},
					{Delta: `<parameter name="pattern" string="true">cache|Cache</parameter>`},
					{Delta: `<parameter name="path" string="true">internal</parameter>`},
					{Delta: `<parameter name="max_results" string="false">80</parameter>`},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "search complete"}, {Done: true}},
			},
		},
	}
	registry := tool.NewRegistry()
	grep := &fakeTool{name: "Grep", output: "match"}
	registry.Register(grep)
	runner := NewRunner(model, ui, registry, nil, "")

	if _, err := runner.RunTurn(context.Background(), "查找 cache"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var input struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(grep.input, &input); err != nil {
		t.Fatalf("Grep input unmarshal error = %v; input=%s", err, grep.input)
	}
	if input.Pattern != "cache|Cache" || input.Path != "internal" || input.MaxResults != 80 {
		t.Fatalf("input = %#v", input)
	}
}

func TestRunTurnParsesInvokeDSMLValueAttribute(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `<invoke name="Grep">`},
					{Delta: `<parameter name="pattern">cache</parameter>`},
					{Delta: `<parameter name="max_results" string="false" value="80"></parameter>`},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "search complete"}, {Done: true}},
			},
		},
	}
	registry := tool.NewRegistry()
	grep := &fakeTool{name: "Grep", output: "match"}
	registry.Register(grep)
	runner := NewRunner(model, ui, registry, nil, "")

	if _, err := runner.RunTurn(context.Background(), "查找 cache"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var input struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(grep.input, &input); err != nil {
		t.Fatalf("Grep input unmarshal error = %v; input=%s", err, grep.input)
	}
	if input.MaxResults != 80 {
		t.Fatalf("input.MaxResults = %d, want 80", input.MaxResults)
	}
}

func TestRunTurnCarriesHistoryAcrossTurns(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "first answer"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "second answer"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first question"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if _, err := runner.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}

	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}

	secondCall := model.calls[1]
	foundFirstQuestion := false
	foundFirstAnswer := false
	foundSecondQuestion := false
	for _, msg := range secondCall {
		switch msg.Content {
		case "first question":
			foundFirstQuestion = true
		case "first answer":
			foundFirstAnswer = true
		case "second question":
			foundSecondQuestion = true
		}
	}

	if !foundFirstQuestion || !foundFirstAnswer || !foundSecondQuestion {
		t.Fatalf("second round did not include committed history: %#v", secondCall)
	}
}

func TestResetHistoryPreventsStoreReloadDuringProcess(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "fresh answer"},
					{Done: true},
				},
			},
		},
	}
	store := &fakeStore{
		history: []message.Message{
			{Role: message.RoleAssistant, Content: "stale answer"},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), store, "session-1")

	runner.ResetHistory()

	msg, err := runner.RunTurn(context.Background(), "new question")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "fresh answer" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "fresh answer")
	}
	if store.loadCalls != 0 {
		t.Fatalf("store.loadCalls = %d, want 0", store.loadCalls)
	}
	if len(model.calls) != 1 {
		t.Fatalf("len(model.calls) = %d, want 1", len(model.calls))
	}
	for _, callMsg := range model.calls[0] {
		if callMsg.Content == "stale answer" {
			t.Fatalf("unexpected stale history in model call: %#v", model.calls[0])
		}
	}
}

// TestContextStats_精简后三字段正确 验证 ContextStats 只暴露三个字段，且 UsedTokens = input+output。
func TestContextStats_精简后三字段正确(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 1000, OutputTokens: 100}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	if stats.UsedTokens != 1100 {
		t.Errorf("UsedTokens = %d，期望 1100", stats.UsedTokens)
	}
	if stats.CacheTokens != 0 {
		t.Errorf("CacheTokens = %d，期望 0", stats.CacheTokens)
	}
	if stats.LimitTokens != 200000 {
		t.Errorf("LimitTokens = %d，期望 200000", stats.LimitTokens)
	}
}

// TestContextStats_CacheTokens正确反映命中缓存 验证 CacheHitTokens 进入 CacheTokens。
func TestContextStats_CacheTokens正确反映命中缓存(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 500, CacheReadInputTokens: 300, OutputTokens: 50}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	// UsedTokens = 500 + 300 + 50 = 850
	if stats.UsedTokens != 850 {
		t.Errorf("UsedTokens = %d，期望 850（500 input + 300 cache_read + 50 output）", stats.UsedTokens)
	}
	if stats.CacheTokens != 300 {
		t.Errorf("CacheTokens = %d，期望 300", stats.CacheTokens)
	}
}

// TestContextStats_无usage时返回零值 验证未收到任何 usage 事件时安全返回零值。
func TestContextStats_无usage时返回零值(t *testing.T) {
	runner := NewRunner(nil, nil, nil, nil, "")
	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 {
		t.Errorf("空 runner ContextStats = %+v，期望 token 均为 0", stats)
	}
	if stats.LimitTokens != 100000 {
		t.Errorf("LimitTokens = %d，期望 100000", stats.LimitTokens)
	}
}

// TestRunTurnExecutesToolCallFormatWithJSONInput 验证 <tool_call><tool name="...">JSON</tool></tool_call> 格式。
func TestRunTurnExecutesToolCallFormatWithJSONInput(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "<tool_call>\n"},
					{Delta: `<tool name="Bash">{"command":"ls ."}</tool>`},
					{Delta: "\n</tool_call>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "done"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	bash := &fakeTool{name: "Bash", output: "ok"}
	registry.Register(bash)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "list files")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("msg.Content = %q, want done", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Bash" {
		t.Fatalf("ui.toolCalls = %#v, want single Bash call", ui.toolCalls)
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bash.input, &input); err != nil {
		t.Fatalf("unmarshal Bash input: %v", err)
	}
	if input.Command != "ls ." {
		t.Fatalf("command = %q, want ls .", input.Command)
	}
}

// TestRunTurnExecutesToolCallFormatWithXMLParams 验证 <tool_call> 中 XML 参数风格。
func TestRunTurnExecutesToolCallFormatWithXMLParams(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "<tool_call>\n<tool name=\"Read\">\n"},
					{Delta: "<file_path>README.md</file_path>\n"},
					{Delta: "</tool_call>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "读取完成"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	readTool := &fakeTool{name: "Read", output: "content"}
	registry.Register(readTool)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read readme")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "读取完成" {
		t.Fatalf("msg.Content = %q, want 读取完成", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls = %#v, want single Read call", ui.toolCalls)
	}
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(readTool.input, &input); err != nil {
		t.Fatalf("unmarshal Read input: %v", err)
	}
	if input.FilePath != "README.md" {
		t.Fatalf("file_path = %q, want README.md", input.FilePath)
	}
}
