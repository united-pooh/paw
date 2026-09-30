package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
)

func TestStreamRecoveryHintDoesNotWriteIntoHistoryCapacity(t *testing.T) {
	backing := []message.Message{
		{Role: message.RoleUser, Content: "original task"},
		{Role: message.RoleAssistant, Content: "reserved history entry"},
	}
	history := backing[:1]
	recovery := streamRecoveryHistory(history)
	if len(recovery) != 2 || recovery[0].Content != "original task" || recovery[1].Content != streamRecoveryInstruction {
		t.Fatalf("recovery history = %#v", recovery)
	}
	if backing[1].Content != "reserved history entry" {
		t.Fatal("temporary recovery hint overwrote the original history backing array")
	}
}

func TestStreamRecoveryAdmitsSteerAndContinuesAfterNextEmptyFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		n := requests.Add(1)
		if n == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"partial reasoning\"}\n\n")
			return
		}
		for _, want := range []string{"original task", "new constraint", "[Paw stream recovery]"} {
			if !strings.Contains(string(request.Input), want) {
				t.Errorf("next request omitted %q: %s", want, request.Input)
			}
		}
		if strings.Contains(string(request.Input), "partial reasoning") {
			t.Error("next request treated interrupted reasoning as completed context")
		}
		if n == 3 {
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n")
		}
	}))
	defer server.Close()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := model.NewClient(model.Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test",
		RetryCount: 0, RetryCountSet: true,
	})
	output := &fakeUI{}
	runner := NewEngineWithInstructionRoot(client, output, tool.NewRegistry(), store, "boundary", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	steerSubmitted := false
	runner.waitForStreamRecovery = func(ctx context.Context, _ time.Duration) error {
		if !steerSubmitted {
			if !runner.SubmitSteer("new constraint") {
				t.Fatal("steer was not accepted during recovery backoff")
			}
			steerSubmitted = true
		}
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := runner.RunTurn(ctx, "original task")
	if err != nil || response.Content != "finished" {
		t.Fatalf("response = %q, error = %v, want recovery episode to continue after no-output transport failure", response.Content, err)
	}
	if requests.Load() != 3 || countSystemEventsByTitle(output.systemEvents(), "stream-recovery") != 2 {
		t.Fatalf("requests=%d notices=%#v, want published failure, empty failure, and completed response", requests.Load(), output.systemEvents())
	}
	snapshot, err := store.LoadSnapshot(context.Background(), "boundary")
	if err != nil {
		t.Fatal(err)
	}
	if !messagesContainContent(snapshot.ActiveHistory, "new constraint") || messagesContainContent(snapshot.ActiveHistory, "[Paw stream recovery]") {
		t.Fatalf("persistent history lost steer or gained temporary recovery hint: %#v", snapshot.ActiveHistory)
	}
}

func TestStreamRecoveryEmptyCompletedResponseResetsFailureEpisode(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 11:
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
		case 13:
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"finished\"}]}]}}\n\n")
		default:
			fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"partial reasoning\"}\n\n")
		}
	}))
	defer server.Close()
	client := model.NewClient(model.Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test",
		RetryCount: 0, RetryCountSet: true,
	})
	output := &fakeUI{}
	runner := NewEngineWithInstructionRoot(client, output, tool.NewRegistry(), nil, "", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.waitForStreamRecovery = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := runner.RunTurn(ctx, "complete the answer")
	if err != nil || response.Content != "finished" {
		t.Fatalf("response=%q error=%v, want fresh episode after the empty completed response", response.Content, err)
	}
	var notices []string
	for _, event := range output.systemEvents() {
		if event.Title == "stream-recovery" {
			notices = append(notices, event.Body)
		}
	}
	if requests.Load() != 13 || len(notices) != 11 || !strings.Contains(notices[9], "10/10") || !strings.Contains(notices[10], "1/10") {
		t.Fatalf("requests=%d notices=%#v, want ten recoveries, empty completion, then a fresh 1/10 recovery", requests.Load(), notices)
	}
}
