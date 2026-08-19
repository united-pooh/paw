package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/tool"
	"paw/internal/ui"
)

type cancelableStreamTool struct {
	started chan struct{}
}

func (*cancelableStreamTool) Name() string        { return "Stream" }
func (*cancelableStreamTool) Description() string { return "streaming test tool" }
func (*cancelableStreamTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (*cancelableStreamTool) Run(context.Context, json.RawMessage) (string, error) {
	return "run fallback", nil
}
func (t *cancelableStreamTool) Stream(ctx context.Context, _ json.RawMessage) (string, bool, error) {
	close(t.started)
	<-ctx.Done()
	return "partial", true, nil
}

type toolOutputUI struct {
	results []ui.ToolResultEvent
}

func (*toolOutputUI) OnAssistantDelta(string) error     { return nil }
func (*toolOutputUI) OnToolCall(ui.ToolCallEvent) error { return nil }
func (u *toolOutputUI) OnToolResult(event ui.ToolResultEvent) error {
	u.results = append(u.results, event)
	return nil
}
func (*toolOutputUI) OnDone() error { return nil }

func TestRunnerCancelCurrentToolPreservesInterruptedStreamResult(t *testing.T) {
	stream := &cancelableStreamTool{started: make(chan struct{})}
	registry := tool.NewRegistry()
	registry.Register(stream)
	output := &toolOutputUI{}
	runner := &Engine{enginePorts: enginePorts{registry: registry, display: newUIDisplayBus(output)}}

	resultCh := make(chan message.ToolResult, 1)
	go func() {
		result, _ := runner.runToolCall(context.Background(), message.ToolCall{ID: "stream-1", Name: "Stream", Input: []byte(`{}`)})
		resultCh <- result
	}()

	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("streaming tool did not start")
	}
	if !runner.CancelCurrentTool() {
		t.Fatal("CancelCurrentTool() = false, want true")
	}

	select {
	case result := <-resultCh:
		if !result.IsError || !strings.HasPrefix(result.Content, "interrupted\n") || !strings.Contains(result.Content, "partial") {
			t.Fatalf("result = %+v, want interrupted partial result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled streaming tool did not return")
	}
	if runner.CancelCurrentTool() {
		t.Fatal("CancelCurrentTool() = true after tool cleanup")
	}
	if len(output.results) != 1 || !output.results[0].IsError {
		t.Fatalf("final tool results = %#v", output.results)
	}
}

func TestRunnerOrdinaryToolUsesCompatibilityRunPath(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Plain", output: "ordinary"})
	runner := &Engine{enginePorts: enginePorts{registry: registry, display: newUIDisplayBus(&toolOutputUI{})}}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "plain-1", Name: "Plain", Input: []byte(`{}`)})
	if err != nil || result.IsError || result.Content != "ordinary" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if runner.CancelCurrentTool() {
		t.Fatal("ordinary tool unexpectedly registered as cancelable")
	}
}

func TestRunnerStreamToolsRunOutsideConcurrentBatch(t *testing.T) {
	stream := &cancelableStreamTool{started: make(chan struct{})}
	registry := tool.NewRegistry()
	registry.Register(stream)
	runner := &Engine{enginePorts: enginePorts{registry: registry, display: newUIDisplayBus(&toolOutputUI{})}}

	resolved := runner.resolveToolCall(message.ToolCall{Name: "Stream"})
	if isToolCallConcurrencySafe(resolved) {
		t.Fatal("streaming tool must not enter concurrent batch")
	}
}

var _ tool.StreamTool = (*cancelableStreamTool)(nil)
