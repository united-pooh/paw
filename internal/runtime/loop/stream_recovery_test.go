package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/storage/session"
	"paw/internal/ui"
)

type recoveryCancelUI struct {
	fakeUI
	cancel context.CancelFunc
}

func TestStreamRecoveryWaitIsBoundedAndCancellable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &Engine{}
		for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
			start := time.Now()
			if err := runner.pauseStreamRecovery(context.Background(), i+1); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed != want {
				t.Fatalf("attempt %d waited %v, want %v", i+1, elapsed, want)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		start := time.Now()
		go func() { result <- runner.pauseStreamRecovery(ctx, 10) }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("backoff cancellation lost: %v", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("cancellation waited for backoff: %v", elapsed)
		}
	})
}

func (u *recoveryCancelUI) OnSystemMessage(event ui.SystemEvent) error {
	err := u.fakeUI.OnSystemMessage(event)
	if event.Title == "stream-recovery" && u.cancel != nil {
		u.cancel()
	}
	return err
}

func TestResponsesAutomaticallyContinuesInterruptedModelRound(t *testing.T) {
	for _, tc := range []struct {
		name             string
		thinking         bool
		idle             bool
		truncated        bool
		readFailure      bool
		completeBadFrame bool
		persistent       bool
		secondEpisode    bool
		providerFail     bool
		empty            bool
		cancel           bool
		wantRequests     int
		wantNotices      int
	}{
		{name: "text EOF", wantRequests: 3, wantNotices: 1},
		{name: "thinking idle", thinking: true, idle: true, wantRequests: 3, wantNotices: 1},
		{name: "truncated text EOF", truncated: true, wantRequests: 3, wantNotices: 1},
		{name: "truncated thinking idle", truncated: true, thinking: true, idle: true, wantRequests: 3, wantNotices: 1},
		{name: "truncated thinking read", truncated: true, thinking: true, readFailure: true, wantRequests: 3, wantNotices: 1},
		{name: "framed invalid JSON stops", truncated: true, thinking: true, completeBadFrame: true, wantRequests: 2},
		{name: "bounded repeated failures", thinking: true, persistent: true, wantRequests: 12, wantNotices: 10},
		{name: "successful response resets interruption budget", thinking: true, secondEpisode: true, wantRequests: 23, wantNotices: 20},
		{name: "provider failure stops", providerFail: true, wantRequests: 2},
		{name: "no output respects provider budget", empty: true, wantRequests: 2},
		{name: "cancel during recovery", thinking: true, cancel: true, wantRequests: 2, wantNotices: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var inputMu sync.Mutex
			var recoveryInput string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				n := requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-Id", "interrupted-request")
				if n == 1 {
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"call_id\":\"committed-write\",\"name\":\"Write\",\"arguments\":\"{}\"}]}}\n\n")
					return
				}
				if n > 2 {
					inputMu.Lock()
					recoveryInput = string(body.Input)
					inputMu.Unlock()
					if tc.secondEpisode && n == 12 {
						fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"call_id\":\"committed-read\",\"name\":\"Read\",\"arguments\":\"{}\"}]}}\n\n")
						return
					}
					if !tc.persistent && (!tc.secondEpisode || n == 23) {
						fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished after recovery\"}]}]}}\n\n")
						return
					}
				}
				if tc.empty {
					return
				}
				eventType := "response.output_text.delta"
				if tc.thinking {
					eventType = "response.reasoning_summary_text.delta"
				}
				fmt.Fprintf(w, "data: {\"type\":%q,\"delta\":\"interrupted partial\"}\n\n", eventType)
				fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"buffered-write\",\"name\":\"Write\",\"arguments\":\"{}\"}}\n\n")
				if tc.truncated {
					fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","delta":"unfinished`)
					if tc.completeBadFrame {
						fmt.Fprint(w, "\n\n")
					}
				}
				if tc.readFailure {
					w.(http.Flusher).Flush()
					panic(http.ErrAbortHandler)
				}
				if tc.providerFail {
					fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"message\":\"provider failed\"}}}\n\n")
				}
				if tc.idle {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			root := t.TempDir()
			store, err := session.NewJSONLStore(filepath.Join(root, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			write := &mutationProbeTool{name: "Write", path: filepath.Join(root, "work.txt"), writeOnRun: "done", output: "authoritative completed result"}
			registry := tool.NewRegistry()
			registry.Register(write)
			read := &mutationProbeTool{name: "Read", output: "second authoritative result"}
			registry.Register(read)
			client := model.NewClient(model.Config{Transport: "openai-responses", Model: "test", APIBaseURL: server.URL, APIPath: "/responses", RetryCount: 0, RetryCountSet: true, StreamIdleTimeout: 100 * time.Millisecond})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output := &recoveryCancelUI{}
			if tc.cancel {
				output.cancel = cancel
			}
			runner := NewEngineWithInstructionRoot(client, output, registry, store, "s", root)
			runner.waitForStreamRecovery = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			runner.SetContextLimitTokens(1_000_000)
			response, err := runner.RunTurn(ctx, "write then explain")
			wantSuccess := !tc.persistent && !tc.providerFail && !tc.empty && !tc.cancel && !tc.completeBadFrame
			if wantSuccess && (err != nil || response.Content != "finished after recovery") {
				t.Fatalf("automatic continuation failed: response=%q err=%v", response.Content, err)
			}
			if !wantSuccess && err == nil {
				t.Fatal("expected failure after bounded/no recovery")
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel lost: %v", err)
			}
			if tc.persistent && (!strings.Contains(err.Error(), "10 次上限") || !model.IsRecoverableResponsesInterruption(err)) {
				t.Fatalf("exhaustion lost limit or original typed interruption: %v", err)
			}
			if requests.Load() != int32(tc.wantRequests) || write.runCalls != 1 {
				t.Fatalf("requests=%d want=%d tool executions=%d want=1", requests.Load(), tc.wantRequests, write.runCalls)
			}
			if got := countSystemEventsByTitle(output.systemEvents(), "stream-recovery"); got != tc.wantNotices {
				t.Fatalf("recovery notices=%d want=%d", got, tc.wantNotices)
			}
			if tc.secondEpisode {
				if read.runCalls != 1 {
					t.Fatalf("successful intermediate tool executions=%d want=1", read.runCalls)
				}
				var episodes []string
				for _, event := range output.systemEvents() {
					if event.Title == "stream-recovery" {
						episodes = append(episodes, event.Body)
					}
				}
				if !strings.Contains(episodes[0], "1/10") || !strings.Contains(episodes[9], "10/10") || !strings.Contains(episodes[10], "1/10") || !strings.Contains(episodes[19], "10/10") {
					t.Fatalf("recovery counters did not reset between successful responses: %#v", episodes)
				}
			}
			if tc.wantRequests > 2 {
				inputMu.Lock()
				input := recoveryInput
				inputMu.Unlock()
				for _, want := range []string{"authoritative completed result", "[Paw stream recovery]"} {
					if !strings.Contains(input, want) {
						t.Errorf("recovery input missing %q", want)
					}
				}
				if tc.secondEpisode && !strings.Contains(input, "second authoritative result") {
					t.Error("next episode lost the new completed tool result")
				}
				for _, absent := range []string{"interrupted partial", "buffered-write"} {
					if strings.Contains(input, absent) {
						t.Errorf("unsafe recovery input contains %q", absent)
					}
				}
			}
			snapshot, loadErr := store.LoadSnapshot(context.Background(), "s")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if wantSuccess && snapshot.Recovery != nil {
				t.Fatalf("successful recovery left pending state: %+v", snapshot.Recovery)
			}
			history, _ := json.Marshal(snapshot.ActiveHistory)
			for _, absent := range []string{"interrupted partial", "buffered-write", "[Paw stream recovery]"} {
				if strings.Contains(string(history), absent) {
					t.Errorf("persistent model history contains %q", absent)
				}
			}
		})
	}
}
