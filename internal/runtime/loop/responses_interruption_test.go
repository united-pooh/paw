package loop

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

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/storage/session"
)

func TestResponsesInterruptionPreservesCompletedWorkAcrossExplicitContinue(t *testing.T) {
	for _, mode := range []string{"text", "thinking"} {
		t.Run(mode, func(t *testing.T) {
			var requestCount atomic.Int32
			var continuing atomic.Bool
			var inputMu sync.Mutex
			var continuationInput json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				defer request.Body.Close()
				var body struct {
					Input json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode Responses input: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				current := requestCount.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-ID", "req-interrupted")
				if continuing.Load() {
					inputMu.Lock()
					continuationInput = append(json.RawMessage(nil), body.Input...)
					inputMu.Unlock()
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished from completed work\"}]}]}}\n\n")
					return
				}
				switch current {
				case 1:
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc-committed\",\"call_id\":\"committed-write\",\"name\":\"Write\",\"arguments\":\"{}\"}]}}\n\n")
				default:
					eventType := "response.output_text.delta"
					if mode == "thinking" {
						eventType = "response.reasoning.delta"
					}
					fmt.Fprintf(w, "data: {\"type\":%q,\"delta\":\"uncommitted partial\"}\n\n", eventType)
					fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc-buffered\",\"call_id\":\"buffered-write\",\"name\":\"Write\",\"arguments\":\"{}\"}}\n\n")
				}
			}))
			defer server.Close()

			root := t.TempDir()
			store, err := session.NewJSONLStore(filepath.Join(root, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			writeTool := &mutationProbeTool{name: "Write", path: filepath.Join(root, "completed.txt"), writeOnRun: "committed once", output: "authoritative completed write"}
			registry := tool.NewRegistry()
			registry.Register(writeTool)
			client := model.NewClient(model.Config{
				Transport: "openai-responses", Model: "test", APIBaseURL: server.URL, APIPath: "/responses",
				Stream: true, StreamSet: true, RetryCount: 2, RetryCountSet: true, Timeout: 5 * time.Second,
			})
			output := &fakeUI{}
			runner := NewEngineWithInstructionRoot(client, output, registry, store, "interrupted-session", root)
			runner.SetContextLimitTokens(1_000_000)
			runner.waitForStreamRecovery = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

			_, err = runner.RunTurn(context.Background(), "write the result and explain it")
			if err == nil || !strings.Contains(err.Error(), "event=unexpected_eof") || !strings.Contains(err.Error(), "req-interrupted") {
				t.Fatalf("RunTurn() error = %v, want original interrupted request diagnostics", err)
			}
			if got := requestCount.Load(); got != 12 {
				t.Fatalf("requests = %d, want completed tool round, initial interruption, and ten separate recovery requests", got)
			}
			if writeTool.runCalls != 1 || len(output.toolCalls) != 1 || output.toolCalls[0].Name != "Write" {
				t.Fatalf("tool runs = %d, displayed calls = %#v, want only completed Write", writeTool.runCalls, output.toolCalls)
			}
			written, readErr := os.ReadFile(writeTool.path)
			if readErr != nil || string(written) != "committed once" {
				t.Fatalf("completed mutation = %q, error = %v", written, readErr)
			}
			visible := strings.Join(output.deltas, "")
			if mode == "thinking" {
				visible = strings.Join(output.thinking, "")
			}
			if visible != strings.Repeat("uncommitted partial", 11) || output.doneCount != 11 {
				t.Fatalf("visible %s = %q, done=%d, want eleven separately finalized partial responses", mode, visible, output.doneCount)
			}
			if got := countSystemEventsByTitle(output.systemEvents(), "stream-recovery"); got != 10 {
				t.Fatalf("recovery notices = %d, want ten before the bounded stop", got)
			}

			snapshot, loadErr := store.LoadSnapshot(context.Background(), "interrupted-session")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if snapshot.Recovery == nil || snapshot.Recovery.Error != err.Error() {
				t.Fatalf("recovery = %#v, want original error %q", snapshot.Recovery, err)
			}
			completedResult := false
			for _, msg := range snapshot.ActiveHistory {
				for _, result := range toolResultsFromMessage(msg) {
					if result.ToolUseID == "committed-write" && result.Content == "authoritative completed write" {
						completedResult = true
					}
				}
			}
			if !completedResult || messagesContainContent(snapshot.ActiveHistory, "uncommitted partial") {
				t.Fatalf("model history lost completed result or included partial: %#v", snapshot.ActiveHistory)
			}
			activeHistory, marshalErr := json.Marshal(snapshot.ActiveHistory)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, absent := range []string{"uncommitted partial", "buffered-write", "[Paw stream recovery]"} {
				if strings.Contains(string(activeHistory), absent) {
					t.Fatalf("active history included display-only or ephemeral data %q", absent)
				}
			}
			records, loadErr := store.LoadResolvedRecords(context.Background(), "interrupted-session")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			partialCount := 0
			for _, record := range records {
				if record.Kind == session.JournalAssistantPartial {
					partialCount++
					if mode == "text" && record.Message.Content != "uncommitted partial" {
						t.Fatalf("persisted partial = %#v", record.Message)
					}
					if mode == "thinking" {
						parts := record.Message.AssistantParts
						if record.Message.Content != "" || len(parts) != 1 || parts[0].Reasoning == nil || parts[0].Reasoning.Text != "uncommitted partial" || parts[0].Status != "partial" {
							t.Fatalf("persisted thinking partial = %#v", record.Message)
						}
					}
					if len(toolCallsFromMessage(record.Message)) != 0 || len(record.Message.ProviderData) != 0 {
						t.Fatalf("persisted partial committed buffered tool or provider state: %#v", record.Message)
					}
				}
			}
			if partialCount != 11 {
				t.Fatalf("partial records = %d, want each interrupted response preserved once for display", partialCount)
			}

			continuing.Store(true)
			response, err := runner.RunTurn(context.Background(), "continue from the completed work")
			if err != nil || response.Content != "finished from completed work" {
				t.Fatalf("continuation = %#v, error = %v", response, err)
			}
			if requestCount.Load() != 13 || writeTool.runCalls != 1 {
				t.Fatalf("requests = %d, tool runs = %d, want one explicit continuation and no repeated mutation", requestCount.Load(), writeTool.runCalls)
			}
			inputMu.Lock()
			input := string(continuationInput)
			inputMu.Unlock()
			for _, want := range []string{"committed-write", "authoritative completed write", "Paw recovery", "unexpected_eof"} {
				if !strings.Contains(input, want) {
					t.Errorf("continuation missing %q: %s", want, input)
				}
			}
			for _, absent := range []string{"uncommitted partial", "buffered-write", "different generation"} {
				if strings.Contains(input, absent) {
					t.Errorf("continuation included uncommitted data %q: %s", absent, input)
				}
			}
			finalSnapshot, err := store.LoadSnapshot(context.Background(), "interrupted-session")
			if err != nil || finalSnapshot.Recovery != nil {
				t.Fatalf("completed recovery = %#v, error = %v", finalSnapshot.Recovery, err)
			}
		})
	}
}
