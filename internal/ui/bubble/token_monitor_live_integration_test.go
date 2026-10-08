package bubble

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/runtime/loop"
)

func TestTokenMonitorNativeReasoningAndToolArgumentsArriveBeforeCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	advance := make(chan struct{}, 3)
	var requests atomic.Int32
	const arguments = `{"query":"fixture"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(event any) {
			data, err := json.Marshal(event)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		delta := func(value map[string]any) {
			write(map[string]any{"choices": []any{map[string]any{"delta": value}}})
		}
		wait := func() bool {
			select {
			case <-advance:
				return true
			case <-r.Context().Done():
				return false
			}
		}
		finish := "stop"
		if requests.Add(1) == 1 {
			delta(map[string]any{"reasoning_content": strings.Repeat("思", 8)})
			if !wait() {
				return
			}
			delta(map[string]any{"reasoning_content": strings.Repeat("考", 8)})
			if !wait() {
				return
			}
			for i, fragment := range []string{`{"query":`, `"fixture"}`} {
				call := map[string]any{"index": 0, "function": map[string]any{"arguments": fragment}}
				if i == 0 {
					call["id"], call["type"] = "fixture-native", "function"
					call["function"].(map[string]any)["name"] = "FixtureLookup"
				}
				delta(map[string]any{"tool_calls": []any{call}})
			}
			if !wait() {
				return
			}
			finish = "tool_calls"
		} else {
			delta(map[string]any{"content": "finished"})
		}
		write(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}})
		write(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 25}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(func() { cancel(); server.Close() })
	client := model.NewClient(model.Config{Provider: "synthetic", Transport: "openai-compatible", APIBaseURL: server.URL, Model: "arrival-fixture", Stream: true, Timeout: 5 * time.Second, RetryCountSet: true, RetryCount: 0})
	messages := make(chan tea.Msg, 128)
	output := New()
	output.sendMsg = func(msg tea.Msg) { messages <- msg }
	registry := tool.NewRegistry()
	registry.Register(tokenMonitorFixtureTool{})
	runner := loop.NewEngineWithInstructionRoot(client, output, registry, nil, "arrival-fixture", t.TempDir())
	m := newModel(ctx, runner, "arrival-fixture", client, nil, nil, nil, nil)
	command := runTurnCmd(ctx, runner, inputDraft{Text: "Use the local fixture lookup."}, "arrival-turn", time.Now())
	go func() { messages <- command() }()
	var firstReasoning time.Time
	var firstRequest string
	reasoningChunks, argumentChunks, finishes := 0, 0, 0
	for {
		var msg tea.Msg
		select {
		case msg = <-messages:
		case <-ctx.Done():
			t.Fatal("native stream did not finish before timeout")
		}
		next, _ := m.Update(msg)
		m = next.(appModel)
		switch msg := msg.(type) {
		case tokenMonitorStartMsg:
			if firstRequest == "" {
				firstRequest = msg.id
			}
		case tokenMonitorActivityMsg:
			activity := msg.activity
			if activity.Reasoning != "" {
				reasoningChunks++
				live := m.tokenMonitor.live.view(activity.At)
				if !live.Active || live.Current <= 0 || live.FirstTextKnown {
					t.Fatalf("reasoning did not update the live graph before final text: %+v", live)
				}
				if reasoningChunks == 1 {
					firstReasoning = activity.At
					timer := time.NewTimer(1100 * time.Millisecond)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						t.Fatal(ctx.Err())
					}
				} else if activity.At.Unix() == firstReasoning.Unix() {
					t.Fatal("separate reasoning arrivals collapsed into the completion bucket")
				}
				advance <- struct{}{}
			}
			if activity.ToolArguments != "" {
				argumentChunks++
				if argumentChunks == 2 {
					want := 16 + float64(len(arguments))/4
					live := m.tokenMonitor.live.view(activity.At)
					if !live.Active || live.Current <= 0 || live.ToolCalls != 0 || m.tokenMonitor.live.total != want {
						t.Fatalf("native arguments were missing or waited for execution: %+v total=%v want=%v", live, m.tokenMonitor.live.total, want)
					}
					advance <- struct{}{}
				}
			}
		case tokenMonitorEndMsg:
			finishes++
			if msg.id == firstRequest {
				live := m.tokenMonitor.live.view(time.Now())
				want := m.tokenMonitor.live.total / msg.at.Sub(m.tokenMonitor.live.started).Seconds()
				if live.Active || math.Abs(live.Average-want) > 1e-9 || live.Average > 30 {
					t.Fatalf("first native request retained a completion spike: %+v want=%v", live, want)
				}
			}
		case turnFinishedMsg:
			if msg.err != nil || reasoningChunks != 2 || argumentChunks != 2 || requests.Load() != 2 || finishes != 2 {
				t.Fatalf("native lifecycle incomplete: error=%v reasoning=%d args=%d requests=%d finishes=%d", msg.err, reasoningChunks, argumentChunks, requests.Load(), finishes)
			}
			return
		}
	}
}
