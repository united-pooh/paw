package bubble

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

type timedBubbleRunner struct {
	fakeRunner
	execution loop.TurnExecution
	turnID    string
	startedAt time.Time
}

func (r *timedBubbleRunner) RunTurnWithTiming(_ context.Context, _ string, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	r.turnID = turnID
	r.startedAt = startedAt
	return r.execution, nil
}

func (r *timedBubbleRunner) RunRichTurnWithTiming(_ context.Context, _ message.Message, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	r.turnID = turnID
	r.startedAt = startedAt
	return r.execution, nil
}

func TestFormatTurnFooter(t *testing.T) {
	response := time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC)
	got := formatTurnFooter(session.TurnMetadata{DurationMS: 95000, ResponseAt: &response})
	want := "1m35s  " + response.Local().Format("03:04:05 PM")
	if got != want {
		t.Fatalf("formatTurnFooter() = %q, want %q", got, want)
	}
	if got := formatTurnDuration(950); got != "950ms" {
		t.Fatalf("formatTurnDuration(950) = %q, want 950ms", got)
	}
	if got := formatTurnDuration(59000); got != "59s" {
		t.Fatalf("formatTurnDuration(59000) = %q, want 59s", got)
	}
}

func TestFormatTurnDurationHourBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		durationMS int64
		want       string
	}{
		{name: "before hour", durationMS: 3_599_000, want: "59m59s"},
		{name: "one hour", durationMS: 3_600_000, want: "1h00m00s"},
		{name: "nine hours", durationMS: int64((9*time.Hour + 5*time.Minute + 7*time.Second) / time.Millisecond), want: "9h05m07s"},
		{name: "beyond one day", durationMS: int64((27*time.Hour + 5*time.Minute + 7*time.Second) / time.Millisecond), want: "27h05m07s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatTurnDuration(test.durationMS); got != test.want {
				t.Fatalf("formatTurnDuration(%d) = %q, want %q", test.durationMS, got, test.want)
			}
		})
	}
}

func TestFormatTurnFooterSupportsNineHours(t *testing.T) {
	response := time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC)
	got := formatTurnFooter(session.TurnMetadata{
		DurationMS: int64(9 * time.Hour / time.Millisecond),
		ResponseAt: &response,
	})
	want := "9h00m00s  " + response.Local().Format("03:04:05 PM")
	if got != want {
		t.Fatalf("formatTurnFooter() = %q, want %q", got, want)
	}
}

func TestRenderAssistantTurnFooter(t *testing.T) {
	rebuildLegacyStyles()
	response := time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC)
	entry := transcriptEntry{
		kind: entryAssistant,
		body: "answer",
		turnMetadata: &session.TurnMetadata{
			DurationMS: 95000,
			ResponseAt: &response,
			Status:     session.TurnStatusCompleted,
		},
	}
	rendered := renderEntryAt(entry, 80, time.Time{})
	plain := ansi.Strip(rendered)
	footer := formatTurnFooter(*entry.turnMetadata)
	if !strings.Contains(plain, "answer") || !strings.Contains(plain, footer) {
		t.Fatalf("rendered = %q, want answer and footer", plain)
	}
	if strings.Index(plain, "answer") >= strings.Index(plain, footer) {
		t.Fatalf("footer should follow answer: %q", plain)
	}
	if expected := contextFreeStyle.Render(footer); !strings.Contains(rendered, expected) {
		t.Fatalf("rendered footer does not use contextFreeStyle: %q, expected fragment %q", rendered, expected)
	}
	user := renderEntryAt(transcriptEntry{kind: entryUser, body: "answer", turnMetadata: entry.turnMetadata}, 80, time.Time{})
	if strings.Contains(ansi.Strip(user), footer) {
		t.Fatalf("user entry unexpectedly contains footer: %q", user)
	}
}

func TestTranscriptRenderCacheIncludesTurnMetadata(t *testing.T) {
	entry := transcriptEntry{kind: entryAssistant, body: "answer"}
	without := transcriptRenderKey(entry, 80, time.Time{})
	response := time.Unix(10, 0).UTC()
	entry.turnMetadata = &session.TurnMetadata{DurationMS: 1000, ResponseAt: &response, Status: session.TurnStatusCompleted}
	with := transcriptRenderKey(entry, 80, time.Time{})
	if reflect.DeepEqual(without, with) {
		t.Fatalf("cache key did not change after adding turn metadata: %#v", with)
	}
}

func TestRunTurnCmdCarriesTimingMetadata(t *testing.T) {
	started := time.Unix(100, 0).UTC()
	response := started.Add(time.Second)
	runner := &timedBubbleRunner{execution: loop.TurnExecution{
		Metadata:           session.TurnMetadata{TurnID: "turn-1", StartedAt: started, ResponseAt: &response, DurationMS: 1000, Status: session.TurnStatusCompleted},
		MetadataPersistErr: fmt.Errorf("sidecar unavailable"),
	}}
	msg := runTurnCmd(context.Background(), runner, inputDraft{Text: "hello"}, "turn-1", started)()
	finished, ok := msg.(turnFinishedMsg)
	if !ok || finished.err != nil || finished.metadata == nil || finished.metadataErr == nil {
		t.Fatalf("turnFinishedMsg = %#v", msg)
	}
	if runner.turnID != "turn-1" || !runner.startedAt.Equal(started) {
		t.Fatalf("timing args = %q, %v", runner.turnID, runner.startedAt)
	}
}

func TestTurnFinishedUpdateRefreshesFooterIntoViewport(t *testing.T) {
	response := time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC)
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, newTerminalCursorAnchor())
	model.width = 80
	model.height = 24
	model.ready = true
	model.transcript = []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "answer"}}
	model.doneAssistant = 0
	model.queryGuard.StartModel()
	model.refreshViewport()
	next, _ := model.Update(turnFinishedMsg{metadata: &session.TurnMetadata{DurationMS: 95000, ResponseAt: &response, Status: session.TurnStatusCompleted}})
	updated := next.(appModel)
	if updated.transcript[0].turnMetadata == nil {
		t.Fatal("turn metadata was not attached to assistant entry")
	}
	if !strings.Contains(ansi.Strip(updated.viewport.View()), formatTurnFooter(*updated.transcript[0].turnMetadata)) {
		t.Fatalf("viewport = %q, want footer", ansi.Strip(updated.viewport.View()))
	}
}

func TestTurnFooterLeavesBottomSpacerForInputDock(t *testing.T) {
	response := time.Date(2026, 7, 30, 23, 47, 47, 0, time.UTC)
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:         entryAssistant,
		title:        "assistant",
		body:         "answer",
		turnMetadata: &session.TurnMetadata{DurationMS: 95000, ResponseAt: &response, Status: session.TurnStatusCompleted},
	}}
	model.refreshViewport()
	model.viewport.GotoBottom()
	lines := strings.Split(ansi.Strip(model.viewport.View()), "\n")
	footer := formatTurnFooter(*model.transcript[0].turnMetadata)
	footerIndex := -1
	for index, line := range lines {
		if strings.Contains(line, footer) {
			footerIndex = index
			break
		}
	}
	if footerIndex < 0 {
		t.Fatalf("viewport = %q, want footer", ansi.Strip(model.viewport.View()))
	}
	if footerIndex >= len(lines)-1 || strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Fatalf("viewport bottom = %q, want one empty row after footer", ansi.Strip(model.viewport.View()))
	}
}

func TestSessionRestoreDecoratesAssistantEntriesFromSidecar(t *testing.T) {
	store := newTestStoreForBubble(t)
	ctx := context.Background()
	messages := make([]message.Message, 0, 8)
	metadata := make([]session.TurnMetadata, 0, 4)
	for index := 0; index < 4; index++ {
		messages = append(messages,
			message.Message{Role: message.RoleUser, Content: fmt.Sprintf("question-%d", index)},
			message.Message{Role: message.RoleAssistant, Content: fmt.Sprintf("answer-%d", index)},
		)
		seq := int64(index*2 + 1)
		response := time.Date(2026, 7, 30, 7, 0+index, 0, 0, time.UTC)
		metadata = append(metadata, session.TurnMetadata{TurnID: fmt.Sprintf("turn-%d", index), AssistantSeq: &seq, ResponseAt: &response, DurationMS: int64(index+1) * 1000, Status: session.TurnStatusCompleted})
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "s1", messages...); err != nil {
		t.Fatal(err)
	}
	for _, item := range metadata {
		if err := store.AppendTurnMetadata(ctx, "s1", item); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeRunner{loadHistoryMsgs: messages}
	msg := loadSessionHistoryCmd(ctx, runner, store, "s1")()
	restored, ok := msg.(sessionRestoredMsg)
	if !ok || restored.err != nil {
		t.Fatalf("restore = %#v", msg)
	}
	var got []string
	for _, entry := range restored.entries {
		if entry.kind == entryAssistant && entry.turnMetadata != nil {
			got = append(got, formatTurnFooter(*entry.turnMetadata))
		}
	}
	if len(got) != 4 {
		t.Fatalf("restored assistant footer count = %d, entries = %#v", len(got), restored.entries)
	}
}

func newTestStoreForBubble(t *testing.T) *session.JSONLStore {
	t.Helper()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
