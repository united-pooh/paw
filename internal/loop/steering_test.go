package loop

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

func TestPromptStateSteerAdmissionLifecycle(t *testing.T) {
	var state promptState

	if state.admitSteer("before") {
		t.Fatal("admitSteer() before admission = true, want false")
	}
	if got := state.pendingCount(); got != 0 {
		t.Fatalf("pendingCount() = %d, want 0", got)
	}

	state.beginSteerAdmission()
	if !state.admitSteer("first") || !state.admitSteer("second") {
		t.Fatal("admitSteer() during admission = false, want true")
	}
	if state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() with pending steer = true, want false")
	}
	if got := state.drain(); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("drain() = %#v, want FIFO steers", got)
	}
	if !state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() after drain = false, want true")
	}
	if state.admitSteer("after") {
		t.Fatal("admitSteer() after seal = true, want false")
	}
}

func TestPromptStateSteerPreservesMixedAdmissionOrder(t *testing.T) {
	var state promptState
	state.appendSupplement("generic-before")
	state.beginSteerAdmission()
	if !state.admitSteer("steer") {
		t.Fatal("admitSteer() = false, want true")
	}
	state.appendSupplement("generic-after")

	if state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() with mixed pending steer = true, want false")
	}
	if got := state.drain(); !reflect.DeepEqual(got, []string{"generic-before", "steer", "generic-after"}) {
		t.Fatalf("drain() = %#v, want global admission order", got)
	}
}

func TestPromptStateGenericSupplementDoesNotBlockSeal(t *testing.T) {
	var state promptState
	state.beginSteerAdmission()
	state.appendSupplement("background context")

	if !state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() with only generic supplement = false, want true")
	}
	if got := state.drain(); !reflect.DeepEqual(got, []string{"background context"}) {
		t.Fatalf("drain() = %#v, want generic supplement preserved", got)
	}
}

func TestPromptStateResetClearsPendingAndClosesAdmission(t *testing.T) {
	var state promptState
	state.beginSteerAdmission()
	if !state.admitSteer("steer") {
		t.Fatal("admitSteer() = false, want true")
	}
	state.appendSupplement("generic")

	state.resetSupplements()

	if got := state.pendingCount(); got != 0 {
		t.Fatalf("pendingCount() after reset = %d, want 0", got)
	}
	if state.admitSteer("after reset") {
		t.Fatal("admitSteer() after reset = true, want false")
	}
}

func TestEngineSubmitSteerUsesAdmissionGate(t *testing.T) {
	var nilRunner *Engine
	if nilRunner.SubmitSteer("steer") {
		t.Fatal("nil runner SubmitSteer() = true, want false")
	}

	runner := &Engine{}
	if runner.SubmitSteer("   ") {
		t.Fatal("SubmitSteer(blank) = true, want false")
	}
	if runner.SubmitSteer("before") {
		t.Fatal("SubmitSteer() before admission = true, want false")
	}

	runner.promptCtx.beginSteerAdmission()
	if !runner.SubmitSteer("  accepted  ") {
		t.Fatal("SubmitSteer() during admission = false, want true")
	}
	if got := runner.drainSupplements(); !reflect.DeepEqual(got, []string{"accepted"}) {
		t.Fatalf("drainSupplements() = %#v, want trimmed steer", got)
	}
}

func TestPreparedSteerBlocksCompletionUntilCommitted(t *testing.T) {
	state := &promptState{}
	state.beginSteerAdmission()
	admission, ok := state.reserveSteer("later")
	if !ok {
		t.Fatal("reserveSteer() = false")
	}
	if state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() sealed while steer reservation was pending")
	}
	admission.Commit()
	if got := state.drain(); !reflect.DeepEqual(got, []string{"later"}) {
		t.Fatalf("drain() = %#v", got)
	}
}

func TestPreparedSteerAbortReleasesCompletionGate(t *testing.T) {
	state := &promptState{}
	state.beginSteerAdmission()
	admission, ok := state.reserveSteer("discard")
	if !ok {
		t.Fatal("reserveSteer() = false")
	}
	admission.Abort()
	if !state.trySealSteerAdmission() {
		t.Fatal("trySealSteerAdmission() stayed open after abort")
	}
	if got := state.pendingCount(); got != 0 {
		t.Fatalf("pendingCount() = %d", got)
	}
}

func TestRunTurnSteerContinuesPlainTextTurn(t *testing.T) {
	first := make(chan model.StreamEvent, 2)
	second := make(chan model.StreamEvent, 2)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{first, second},
		started: make(chan int, 2),
	}
	runner := NewEngine(blocking, &fakeUI{}, tool.NewRegistry(), nil, "")

	type turnResult struct {
		msg message.Message
		err error
	}
	done := make(chan turnResult, 1)
	go func() {
		msg, err := runner.RunTurn(context.Background(), "original request")
		done <- turnResult{msg: msg, err: err}
	}()

	waitForModelCall(t, blocking.started, 0)
	if !runner.SubmitSteer("first correction") || !runner.SubmitSteer("second correction") {
		t.Fatal("SubmitSteer() during first stream = false, want true")
	}
	first <- model.StreamEvent{Delta: "draft answer"}
	first <- model.StreamEvent{Done: true}
	close(first)

	waitForModelCall(t, blocking.started, 1)
	if len(blocking.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(blocking.calls))
	}
	for _, want := range []string{"draft answer", "first correction", "second correction"} {
		if !messagesContainContent(blocking.calls[1], want) {
			t.Fatalf("second model call = %#v, want %q", blocking.calls[1], want)
		}
	}
	second <- model.StreamEvent{Delta: "corrected answer"}
	second <- model.StreamEvent{Done: true}
	close(second)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("RunTurn() error = %v", result.err)
		}
		if result.msg.Content != "corrected answer" {
			t.Fatalf("RunTurn() content = %q, want corrected answer", result.msg.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() = %d, want 0", got)
	}

	history := runner.currentHistory()
	if len(history) != 5 {
		t.Fatalf("history length = %d, want 5: %#v", len(history), history)
	}
	wantContents := []string{"original request", "draft answer", "first correction", "second correction", "corrected answer"}
	for i, want := range wantContents {
		if !messagesContainContent([]message.Message{history[i]}, want) {
			t.Fatalf("history[%d] = %#v, want %q", i, history[i], want)
		}
	}
}

func TestRunTurnSteerSubmittedDuringToolRunsAtNextModelBoundary(t *testing.T) {
	first := make(chan model.StreamEvent, 1)
	second := make(chan model.StreamEvent, 2)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{first, second},
		started: make(chan int, 2),
	}
	blockingCall := &blockingTool{
		name:    "Blocking",
		output:  "tool output",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	registry := tool.NewRegistry()
	registry.Register(blockingCall)
	runner := NewEngine(blocking, &fakeUI{}, registry, nil, "")

	type turnResult struct {
		msg message.Message
		err error
	}
	done := make(chan turnResult, 1)
	go func() {
		msg, err := runner.RunTurn(context.Background(), "original request")
		done <- turnResult{msg: msg, err: err}
	}()

	waitForModelCall(t, blocking.started, 0)
	first <- model.StreamEvent{
		ToolCalls: []message.ToolCall{{ID: "call-1", Name: "Blocking", Input: json.RawMessage(`{}`)}},
		Done:      true,
	}
	close(first)

	select {
	case <-blockingCall.started:
	case <-time.After(time.Second):
		t.Fatal("tool call did not start")
	}
	if !runner.SubmitSteer("adjust after tool") {
		t.Fatal("SubmitSteer() during tool = false, want true")
	}
	close(blockingCall.release)

	waitForModelCall(t, blocking.started, 1)
	for _, want := range []string{"tool output", "adjust after tool"} {
		if !messagesContainContent(blocking.calls[1], want) {
			t.Fatalf("second model call = %#v, want %q", blocking.calls[1], want)
		}
	}
	second <- model.StreamEvent{Delta: "adjusted answer"}
	second <- model.StreamEvent{Done: true}
	close(second)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("RunTurn() error = %v", result.err)
		}
		if result.msg.Content != "adjusted answer" {
			t.Fatalf("RunTurn() content = %q, want adjusted answer", result.msg.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
}

func TestFailedTurnKeepsPendingSteerForNextTurn(t *testing.T) {
	stream := make(chan model.StreamEvent, 1)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{stream},
		started: make(chan int, 1),
	}
	runner := NewEngine(blocking, &fakeUI{}, tool.NewRegistry(), nil, "")
	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "first")
		done <- err
	}()

	waitForModelCall(t, blocking.started, 0)
	if !runner.SubmitSteer("preserve after failure") {
		t.Fatal("SubmitSteer() = false, want true")
	}
	stream <- model.StreamEvent{Err: errors.New("provider failed")}
	close(stream)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "provider failed") {
			t.Fatalf("RunTurn() error = %v, want provider failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failed RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 1 {
		t.Fatalf("PendingSupplementCount() after failure = %d, want 1", got)
	}
}

func TestJournaledSteerFailureDoesNotDuplicatePendingInput(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan model.StreamEvent, 2)
	second := make(chan model.StreamEvent, 1)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{first, second},
		started: make(chan int, 2),
	}
	runner := NewEngine(blocking, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	done := make(chan error, 1)
	go func() {
		_, runErr := runner.RunTurn(context.Background(), "first")
		done <- runErr
	}()

	waitForModelCall(t, blocking.started, 0)
	if !runner.SubmitSteer("preserve after journal failure") {
		t.Fatal("SubmitSteer() = false, want true")
	}
	first <- model.StreamEvent{Delta: "draft"}
	first <- model.StreamEvent{Done: true}
	close(first)

	waitForModelCall(t, blocking.started, 1)
	second <- model.StreamEvent{Err: errors.New("provider failed")}
	close(second)
	select {
	case runErr := <-done:
		if runErr == nil || !strings.Contains(runErr.Error(), "provider failed") {
			t.Fatalf("RunTurn() error = %v, want provider failure", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("failed RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() = %d, want 0 because steer is journaled", got)
	}

	records, err := store.LoadResolvedRecords(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	matchingRecords := 0
	for _, record := range records {
		if strings.Contains(record.Message.Content, "preserve after journal failure") {
			matchingRecords++
			if record.Kind != session.JournalMessage || strings.TrimSpace(record.TurnID) == "" {
				t.Fatalf("steer record = %#v, want turn-bound JournalMessage", record)
			}
		}
	}
	if matchingRecords != 1 {
		t.Fatalf("journal steer records = %d, want 1: %#v", matchingRecords, records)
	}

	recoveryModel := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "recovered"},
		{Done: true},
	}}}}
	runner.model = recoveryModel
	if _, err := runner.RunTurn(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if got := countMessageContentOccurrences(recoveryModel.calls[0], "preserve after journal failure"); got != 1 {
		t.Fatalf("recovery model steer occurrences = %d, want 1: %#v", got, recoveryModel.calls[0])
	}
}

func TestCanceledTurnKeepsPendingSteerForNextTurn(t *testing.T) {
	stream := make(chan model.StreamEvent)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{stream},
		started: make(chan int, 1),
	}
	runner := NewEngine(blocking, &fakeUI{}, tool.NewRegistry(), nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(ctx, "first")
		done <- err
	}()

	waitForModelCall(t, blocking.started, 0)
	if !runner.SubmitSteer("preserve this correction") {
		t.Fatal("SubmitSteer() = false, want true")
	}
	cancel()
	close(stream)

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunTurn() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 1 {
		t.Fatalf("PendingSupplementCount() after cancel = %d, want 1", got)
	}

	next := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "recovered answer"},
		{Done: true},
	}}}}
	runner.model = next
	if _, err := runner.RunTurn(context.Background(), "next"); err != nil {
		t.Fatalf("next RunTurn() error = %v", err)
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() after recovery = %d, want 0", got)
	}
	if !messagesContainContent(next.calls[0], "preserve this correction") {
		t.Fatalf("next model call = %#v, want preserved steer", next.calls[0])
	}
}

func countMessageContentOccurrences(messages []message.Message, want string) int {
	count := 0
	for _, msg := range messages {
		count += strings.Count(msg.Content, want)
	}
	return count
}

func waitForModelCall(t *testing.T, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("model call index = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("model call %d did not start", want)
	}
}
