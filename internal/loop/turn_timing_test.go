package loop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

type timingStore struct {
	fakeStore
	metadata      []session.TurnMetadata
	metadataCalls int
	metadataErr   error
}

func (s *timingStore) AppendWithSequences(ctx context.Context, sessionID string, msgs ...message.Message) (int64, int64, error) {
	if err := s.Append(ctx, sessionID, msgs...); err != nil {
		return -1, -1, err
	}
	first := int64(0)
	for _, batch := range s.appends[:len(s.appends)-1] {
		first += int64(len(batch))
	}
	return first, first + int64(len(msgs)) - 1, nil
}

func (s *timingStore) AppendTurnMetadata(_ context.Context, _ string, metadata session.TurnMetadata) error {
	s.metadataCalls++
	if s.metadataErr != nil {
		return s.metadataErr
	}
	s.metadata = append(s.metadata, metadata)
	return nil
}

func (s *timingStore) LoadTurnMetadata(_ context.Context, _ string) ([]session.TurnMetadata, error) {
	return append([]session.TurnMetadata(nil), s.metadata...), nil
}

func TestRunTurnWithTimingPersistsAfterAssistantCommit(t *testing.T) {
	started := time.Date(2026, 7, 30, 7, 45, 0, 0, time.FixedZone("test", 8*60*60))
	response := started.Add(95*time.Second + 700*time.Millisecond)
	store := &timingStore{}
	model := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "hello"}, {Done: true}}}}}
	runner := NewEngine(model, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	runner.nowFn = func() time.Time { return response }

	execution, err := runner.RunTurnWithTiming(context.Background(), "hi", "turn-1", started)
	if err != nil {
		t.Fatalf("RunTurnWithTiming() error = %v", err)
	}
	if execution.Message.Content != "hello" {
		t.Fatalf("message = %#v", execution.Message)
	}
	if execution.Metadata.DurationMS != 95700 || execution.Metadata.AssistantSeq == nil || *execution.Metadata.AssistantSeq != 1 {
		t.Fatalf("metadata = %#v, want 95700ms and assistant seq 1", execution.Metadata)
	}
	if execution.Metadata.ResponseAt == nil || !execution.Metadata.ResponseAt.Equal(response.UTC()) {
		t.Fatalf("response_at = %v, want %v", execution.Metadata.ResponseAt, response.UTC())
	}
	if len(store.appends) != 1 || len(store.appends[0]) != 2 || len(store.metadata) != 1 {
		t.Fatalf("append order = messages %#v metadata %#v", store.appends, store.metadata)
	}
	if store.metadata[0].AssistantSeq == nil || *store.metadata[0].AssistantSeq != 1 {
		t.Fatalf("persisted metadata = %#v", store.metadata[0])
	}
	if prompt := promptTextForTest(model.calls[0]); strings.Contains(prompt, "1m35s") || strings.Contains(prompt, "response_at") {
		t.Fatalf("timing metadata leaked into model prompt: %q", prompt)
	}
}

func TestRunTurnWithTimingMetadataFailureDoesNotFailAnswer(t *testing.T) {
	store := &timingStore{metadataErr: errors.New("sidecar unavailable")}
	runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "ok"}, {Done: true}}}}}, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	started := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	runner.nowFn = func() time.Time { return started.Add(time.Second) }

	execution, err := runner.RunTurnWithTiming(context.Background(), "hi", "turn-1", started)
	if err != nil || execution.Message.Content != "ok" {
		t.Fatalf("answer = %#v, err = %v", execution.Message, err)
	}
	if execution.MetadataPersistErr == nil || store.metadataCalls != 3 {
		t.Fatalf("metadata error = %v, calls = %d, want 3 attempts", execution.MetadataPersistErr, store.metadataCalls)
	}
}

func TestRunTurnWithTimingUsesJournalAssistantSequence(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "journal answer"}, {Done: true}}}}}
	runner := NewEngine(model, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	started := time.Date(2026, 7, 30, 7, 45, 0, 0, time.UTC)
	runner.nowFn = func() time.Time { return started.Add(95 * time.Second) }

	execution, err := runner.RunTurnWithTiming(context.Background(), "journal question", "turn-journal", started)
	if err != nil {
		t.Fatalf("RunTurnWithTiming() error = %v", err)
	}
	if execution.Metadata.AssistantSeq == nil {
		t.Fatalf("metadata = %#v, want assistant sequence", execution.Metadata)
	}
	records, err := store.LoadResolvedRecords(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	var assistantSeq int64 = -1
	for _, record := range records {
		if record.Message.Role == message.RoleAssistant {
			assistantSeq = record.Seq
		}
	}
	if *execution.Metadata.AssistantSeq != assistantSeq || assistantSeq < 0 {
		t.Fatalf("metadata assistant seq = %d, resolved assistant seq = %d, records = %#v", *execution.Metadata.AssistantSeq, assistantSeq, records)
	}
	metadata, err := store.LoadTurnMetadata(context.Background(), "session-1")
	if err != nil || len(metadata) != 1 || metadata[0].AssistantSeq == nil || *metadata[0].AssistantSeq != assistantSeq {
		t.Fatalf("persisted metadata = %#v, err = %v", metadata, err)
	}
}

func TestRunTurnWithTimingRecordsTokenDelta(t *testing.T) {
	store := &timingStore{}
	first := model.Usage{PromptTokens: 1000, CompletionTokens: 100}
	second := model.Usage{PromptTokens: 2200, CompletionTokens: 260}
	runner := NewEngine(&fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Usage: &first}, {Delta: "one"}, {Done: true}}},
		{events: []model.StreamEvent{{Usage: &second}, {Delta: "two"}, {Done: true}}},
	}}, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	started := time.Date(2026, 7, 30, 7, 45, 0, 0, time.UTC)
	runner.nowFn = func() time.Time { return started.Add(time.Second) }

	// 首个回合：会话尚无用量记录，零基线下的全量即为增量。
	if _, err := runner.RunTurnWithTiming(context.Background(), "one", "turn-1", started); err != nil {
		t.Fatalf("turn-1 error = %v", err)
	}
	if len(store.metadata) != 1 || store.metadata[0].InputTokens != 1000 || store.metadata[0].OutputTokens != 100 {
		t.Fatalf("turn-1 metadata tokens = %#v, want 1000/100", store.metadata)
	}

	// 第二个回合：provider 按请求上报实际用量（整个会话历史会重新计入 prompt），
	// 因此本轮增量等于该请求的全量 2200/260。
	if _, err := runner.RunTurnWithTiming(context.Background(), "two", "turn-2", started); err != nil {
		t.Fatalf("turn-2 error = %v", err)
	}
	if len(store.metadata) != 2 || store.metadata[1].InputTokens != 2200 || store.metadata[1].OutputTokens != 260 {
		t.Fatalf("turn-2 metadata tokens = %#v, want 2200/260", store.metadata)
	}
}

func TestRunTurnLegacyPathStillReturnsMessage(t *testing.T) {
	runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "legacy"}, {Done: true}}}}}, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "session-1")
	msg, err := runner.RunTurn(context.Background(), "hi")
	if err != nil || msg.Content != "legacy" {
		t.Fatalf("message = %#v, err = %v", msg, err)
	}
}
