package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"paw/internal/message"
	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
	"paw/internal/ui"
)

type permissionGateFunc func(context.Context, PermissionRequest) (PermissionDecision, error)

func (f permissionGateFunc) Decide(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
	return f(ctx, request)
}

type permissionProbeTool struct{ calls int }

type permissionUI struct{}

func (permissionUI) OnAssistantDelta(string) error         { return nil }
func (permissionUI) OnToolCall(ui.ToolCallEvent) error     { return nil }
func (permissionUI) OnToolResult(ui.ToolResultEvent) error { return nil }
func (permissionUI) OnDone() error                         { return nil }

func (t *permissionProbeTool) Name() string        { return "Probe" }
func (t *permissionProbeTool) Description() string { return "probe" }
func (t *permissionProbeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *permissionProbeTool) IsConcurrencySafe(json.RawMessage) bool { return true }
func (t *permissionProbeTool) Run(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "probe", nil
}

func TestToolBatchPreflightsEveryOutsideReadBeforeStartingAnyTool(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	first := filepath.Join(outside, "a.txt")
	second := filepath.Join(outside, "b.txt")
	if err := os.WriteFile(first, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.ReadTool{Root: workspace})
	probe := &permissionProbeTool{}
	registry.Register(probe)
	engine := NewEngine(nil, permissionUI{}, registry, nil, "session-a")
	var requests []PermissionRequest
	engine.SetPermissionGate(permissionGateFunc(func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
		if probe.calls != 0 {
			t.Fatalf("tool started before permission preflight completed")
		}
		requests = append(requests, request)
		return PermissionAllowOnce, nil
	}))
	ctx := WithTurnOwner(context.Background(), "session-a", "turn-a")
	result, err := engine.runToolCalls(ctx, []message.ToolCall{
		{ID: "read-a", Name: "Read", Input: json.RawMessage(`{"file_path":"` + first + `"}`)},
		{ID: "probe", Name: "Probe", Input: json.RawMessage(`{}`)},
		{ID: "read-b", Name: "Read", Input: json.RawMessage(`{"file_path":"` + second + `"}`)},
	})
	if err != nil {
		t.Fatalf("runToolCalls: %v", err)
	}
	if len(requests) != 2 || requests[0].ToolCallID != "read-a" || requests[1].ToolCallID != "read-b" {
		t.Fatalf("requests = %#v", requests)
	}
	canonicalFirst, err := filepath.EvalSymlinks(first)
	if err != nil {
		t.Fatal(err)
	}
	if requests[0].TurnID != "turn-a" || requests[0].CanonicalPath != canonicalFirst {
		t.Fatalf("first request = %#v", requests[0])
	}
	results := toolResultsFromMessage(result)
	if len(results) != 3 || results[0].IsError || results[1].IsError || results[2].IsError {
		t.Fatalf("results = %#v", results)
	}
}

func TestOutsideReadDenyReturnsToolErrorWithoutReading(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.ReadTool{Root: workspace})
	engine := NewEngine(nil, permissionUI{}, registry, nil, "session-a")
	engine.SetPermissionGate(permissionGateFunc(func(context.Context, PermissionRequest) (PermissionDecision, error) {
		return PermissionDeny, nil
	}))
	result, err := engine.runToolCalls(WithTurnOwner(context.Background(), "session-a", "turn-a"), []message.ToolCall{{ID: "read", Name: "Read", Input: json.RawMessage(`{"file_path":"` + outside + `"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	toolResult := toolResultsFromMessage(result)[0]
	canonicalOutside, evalErr := filepath.EvalSymlinks(outside)
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if !toolResult.IsError || toolResult.Content != "Read permission denied for "+canonicalOutside {
		t.Fatalf("result = %#v", toolResult)
	}
}

func TestYoloOutsideReadBypassesPermissionGate(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.ReadTool{Root: workspace, AllowOutsideRoot: true})
	engine := NewEngine(nil, permissionUI{}, registry, nil, "session-a")
	engine.SetPermissionGate(permissionGateFunc(func(context.Context, PermissionRequest) (PermissionDecision, error) {
		t.Fatal("permission gate called in yolo mode")
		return PermissionDeny, nil
	}))
	result, err := engine.runToolCalls(WithTurnOwner(context.Background(), "session-a", "turn-a"), []message.ToolCall{{ID: "read", Name: "Read", Input: json.RawMessage(`{"file_path":"` + outside + `"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := toolResultsFromMessage(result)[0]; got.IsError || got.Content != "secret" {
		t.Fatalf("result = %#v", got)
	}
}
