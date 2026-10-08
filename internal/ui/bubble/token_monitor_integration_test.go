package bubble

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/runtime/loop"
	"paw/internal/tokentracer"
)

func TestTokenMonitorProviderEngineLedgerIntegration(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport string
		tool      bool
	}{
		{name: "chat", transport: "openai-compatible"},
		{name: "responses", transport: "openai-responses"},
		{name: "responses tool roundtrip", transport: "openai-responses", tool: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			release := make(chan struct{})
			var released sync.Once
			unblock := func() { released.Do(func() { close(release) }) }
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				defer request.Body.Close()
				index := requestCount.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				writeEvent := func(event any) {
					data, err := json.Marshal(event)
					if err != nil {
						t.Errorf("encode fixture: %v", err)
						return
					}
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					w.(http.Flusher).Flush()
				}
				if test.transport == "openai-compatible" {
					writeEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Synthetic visible text."}}}})
				} else {
					writeEvent(map[string]any{"type": "response.output_text.delta", "item_id": "fixture-message", "output_index": 0, "content_index": 0, "delta": "Synthetic visible text."})
				}
				if index == 1 {
					select {
					case <-release:
					case <-request.Context().Done():
						return
					}
				}
				input, output := 100, 12
				if test.tool && index == 1 {
					output = 5
				} else if index > 1 {
					input = 140
				}
				if test.transport == "openai-compatible" {
					writeEvent(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": input, "completion_tokens": output, "prompt_tokens_details": map[string]any{"cached_tokens": 60}}})
					writeEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}})
					_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				outputs := []any{map[string]any{"type": "message", "id": "fixture-message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Synthetic visible text."}}}}
				if test.tool && index == 1 {
					outputs = append(outputs, map[string]any{"type": "function_call", "id": "fixture-function", "call_id": "fixture-call", "name": "FixtureLookup", "arguments": `{"query":"fixture"}`})
				}
				writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("fixture-response-%d", index), "status": "completed", "output": outputs, "usage": map[string]any{"input_tokens": input, "output_tokens": output, "input_tokens_details": map[string]any{"cached_tokens": 60}}}})
			}))
			t.Cleanup(func() {
				cancel()
				unblock()
				server.Close()
			})
			recorder, err := tokentracer.NewRecorder(tokentracer.RecorderConfig{Home: home, Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = recorder.Close() })
			client := model.NewClient(model.Config{Provider: "synthetic", Transport: test.transport, APIBaseURL: server.URL, Model: "token-fixture", Stream: true, Timeout: 5 * time.Second, RetryCountSet: true, RetryCount: 0})
			recordErrors := make(chan error, 16)
			client.SetRequestObserver(func(event model.RequestEvent) {
				if err := recorder.Record(event); err != nil {
					recordErrors <- err
				}
			})
			messages := make(chan tea.Msg, 128)
			output := New()
			output.sendMsg = func(msg tea.Msg) { messages <- msg }
			registry := tool.NewRegistry()
			if test.tool {
				registry.Register(tokenMonitorFixtureTool{})
			}
			const sessionID = "token-monitor-synthetic-session"
			runner := loop.NewEngineWithInstructionRoot(client, output, registry, nil, sessionID, workspace)
			m := newModel(ctx, runner, sessionID, client, nil, nil, nil, nil)
			m.width, m.height, m.ready = 120, 40, true
			m.tokenMonitor.reader = tokentracer.NewLedgerReader(home)
			m.openActivity(activityTabTasks)
			m.relayout()
			poll := func() {
				t.Helper()
				next, cmd := m.Update(tokenMonitorTickMsg{})
				m = next.(appModel)
				if cmd == nil {
					t.Fatal("visible monitor did not schedule ledger read")
				}
				result, ok := cmd().(tokenMonitorResultMsg)
				if !ok || result.err != nil {
					t.Fatalf("ledger poll = %#v", result)
				}
				next, _ = m.Update(result)
				m = next.(appModel)
			}
			command := runTurnCmd(ctx, runner, inputDraft{Text: "Reply briefly with the synthetic fixture result."}, "synthetic-turn", time.Now())
			go func() { messages <- command() }()
			starts, texts, usages, calls, results := 0, 0, 0, 0, 0
			firstTextChecked := false
			finished := false
			for !finished {
				var msg tea.Msg
				select {
				case msg = <-messages:
				case <-ctx.Done():
					t.Fatalf("integration timed out: %v", ctx.Err())
				}
				next, cmd := m.Update(msg)
				m = next.(appModel)
				switch msg := msg.(type) {
				case tokenMonitorStartMsg:
					starts++
					if cmd != nil {
						next, _ = m.Update(cmd())
						m = next.(appModel)
					}
				case tokenMonitorActivityMsg:
					if msg.activity.Text == "" {
						continue
					}
					texts++
					if firstTextChecked {
						continue
					}
					firstTextChecked = true
					poll()
					live := m.tokenMonitorView()
					if !live.Rates.Active || live.Rates.Current <= 0 || !live.Rates.FirstTextKnown || live.Rates.RequestID == "" {
						t.Fatalf("provider text did not reach live sampler: %+v", live.Rates)
					}
					if live.Context.Known || live.Snapshot.Summary.Unknown != 1 || live.Snapshot.Summary.Running != 1 || live.Snapshot.Summary.Reported != 0 {
						t.Fatalf("unreported stream became known: context=%+v ledger=%+v", live.Context, live.Snapshot.Summary)
					}
					if len(live.Snapshot.Requests) != 1 || live.Snapshot.Requests[0].ID != live.Rates.RequestID {
						t.Fatalf("UI and ledger request identity diverged: %+v", live.Snapshot.Requests)
					}
					assertFixedFrame(t, m.View(), 120, 40)
					unblock()
				case tokenMonitorUsageMsg:
					usages++
				case toolCallMsg:
					calls++
				case toolResultMsg:
					results++
				case turnFinishedMsg:
					if msg.err != nil {
						t.Fatalf("real Engine turn failed: %v", msg.err)
					}
					finished = true
				}
			}
			poll()
			wantRequests, wantInput, wantOutput, wantCache := 1, 100, 12, 60
			if test.tool {
				wantRequests, wantInput, wantOutput, wantCache = 2, 240, 17, 120
				if calls != 1 || results != 1 || len(m.tokenMonitor.snapshot.Tools) != 1 || m.tokenMonitor.snapshot.Tools[0].Results <= 0 {
					t.Fatalf("tool roundtrip not attributed: callbacks=%d/%d tools=%+v", calls, results, m.tokenMonitor.snapshot.Tools)
				}
			}
			summary := m.tokenMonitor.snapshot.Summary
			if int(requestCount.Load()) != wantRequests || starts != wantRequests || texts < wantRequests || usages < wantRequests || !firstTextChecked {
				t.Fatalf("missing real callbacks: http=%d starts=%d texts=%d usage=%d", requestCount.Load(), starts, texts, usages)
			}
			if summary.Requests != wantRequests || summary.Input != wantInput || summary.Output != wantOutput || summary.CacheRead != wantCache || summary.Unknown != 0 || summary.Running != 0 {
				t.Fatalf("final JSONL monitor summary = %+v", summary)
			}
			view := m.tokenMonitorView()
			later := m.tokenMonitor.live.view(time.Now().Add(time.Second))
			if view.Rates.Active || view.Rates.Current != 0 || !view.Rates.FirstTextKnown || later.FirstText != view.Rates.FirstText || later.Current != 0 || !view.Context.InputKnown || !view.Context.OutputKnown || !view.Context.CacheKnown {
				t.Fatalf("completion lost final usage or kept live TPS: %+v context=%+v", view.Rates, view.Context)
			}
			if test.tool && (view.Rates.ToolCalls != 1 || view.Rates.ToolActive) {
				t.Fatalf("tool timing did not settle: %+v", view.Rates)
			}
			plain := ansi.Strip(m.View())
			for _, want := range []string{"TOKEN HISTORY", "Input", "Output", "Cache hit", "≈0.0 tok/s"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("rendered View missing %q:\n%s", want, plain)
				}
			}
			assertFixedFrame(t, m.View(), 120, 40)
			paths, err := filepath.Glob(filepath.Join(home, "tracer", "instances", "*", "events-*.jsonl"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("fixture ledger files = %v, error = %v", paths, err)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil || !strings.Contains(string(data), `"request_end"`) || !strings.Contains(string(data), `"attempt_usage"`) {
				t.Fatalf("missing persisted request lifecycle: %v", err)
			}
			select {
			case err := <-recordErrors:
				t.Fatalf("recorder failed: %v", err)
			default:
			}
		})
	}
}

type tokenMonitorFixtureTool struct{}

func (tokenMonitorFixtureTool) Name() string        { return "FixtureLookup" }
func (tokenMonitorFixtureTool) Description() string { return "Read a synthetic fixture value" }
func (tokenMonitorFixtureTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)
}
func (tokenMonitorFixtureTool) Run(context.Context, json.RawMessage) (string, error) {
	return "synthetic fixture payload", nil
}
func (tokenMonitorFixtureTool) ReadOnly() bool { return true }
