package loop

import (
	"context"
	"reflect"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
	"paw/internal/todo"
)

func TestTimedTurnPersistsWithAutoContinueEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, err := session.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			usage := model.Usage{PromptTokens: 75703, CompletionTokens: 4318}
			runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
				{Usage: &usage}, {Delta: "answer"}, {Done: true},
			}}}}, &fakeUI{}, tool.NewRegistry(), store, "s1")
			broker := todo.NewBroker()
			defer broker.Close()
			runner.SetTodoBroker(broker)
			config := DefaultAutoContinueConfig()
			config.Enabled = enabled
			runner.SetAutoContinueConfig(config)
			started := time.Date(2026, 9, 8, 8, 45, 37, 0, time.UTC)
			runner.nowFn = func() time.Time { return started.Add(14474 * time.Millisecond) }
			execution, err := runner.RunTurnWithTiming(ctx, "question", "requested-turn", started)
			if err != nil || execution.Message.Content != "answer" {
				t.Fatalf("answer=%q err=%v", execution.Message.Content, err)
			}
			metadata, err := store.LoadTurnMetadata(ctx, "s1")
			if err != nil || len(metadata) != 1 {
				t.Fatalf("saved metadata=%+v err=%v, want one record", metadata, err)
			}
			got := metadata[0]
			if got.TurnID != "requested-turn" || got.InputTokens != 75703 || got.OutputTokens != 4318 || got.DurationMS != 14474 {
				t.Fatalf("metadata=%+v", got)
			}
			assertTimingMatchesJournal(t, store, "s1", metadata)
		})
	}
}

type advancingTimingModel struct {
	fakeModel
	now     *time.Time
	elapsed []time.Duration
}

func (m *advancingTimingModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	*m.now = m.now.Add(m.elapsed[len(m.calls)])
	return m.fakeModel.StreamMessage(ctx, messages, tools)
}

func TestTimedAutoContinuePersistsIndependentRounds(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	broker := todo.NewBroker()
	defer broker.Close()
	registry := tool.NewRegistry()
	registry.Register(todo.NewTool(broker))
	started := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	now := started
	client := &advancingTimingModel{
		now: &now, elapsed: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second},
		fakeModel: fakeModel{rounds: []fakeRound{
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 1000, CompletionTokens: 100}}, {ToolCalls: []message.ToolCall{{ID: "todo-1", Name: "update_todo", Input: []byte(`{"items":[{"id":"work","content":"finish work","status":"pending"}]}`)}}, Done: true}}},
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 1200, CompletionTokens: 120}}, {Delta: "partial answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 2000, CompletionTokens: 200}}, {ToolCalls: []message.ToolCall{{ID: "todo-2", Name: "update_todo", Input: []byte(`{"items":[{"id":"work","content":"finish work","status":"completed"}]}`)}}, Done: true}}},
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 2400, CompletionTokens: 240}}, {Delta: "final answer"}, {Done: true}}},
		}},
	}
	runner := NewEngine(client, &fakeUI{}, registry, store, "s1")
	runner.SetTodoBroker(broker)
	runner.nowFn = func() time.Time { return now }
	execution, err := runner.RunTurnWithTiming(ctx, "finish work", "first-turn", started)
	if err != nil || execution.Message.Content != "final answer" {
		t.Fatalf("answer=%q err=%v", execution.Message.Content, err)
	}
	metadata, err := store.LoadTurnMetadata(ctx, "s1")
	if err != nil || len(metadata) != 2 {
		t.Fatalf("metadata=%+v err=%v, want two rounds", metadata, err)
	}
	first, second := metadata[0], metadata[1]
	if first.TurnID != "first-turn" || second.TurnID == "" || second.TurnID == first.TurnID {
		t.Fatalf("round IDs: %q, %q", first.TurnID, second.TurnID)
	}
	for i, want := range []struct {
		input, output int
		duration      int64
	}{{2200, 220, 3000}, {4400, 440, 7000}} {
		got := metadata[i]
		if got.InputTokens != want.input || got.OutputTokens != want.output || got.DurationMS != want.duration || got.Status != session.TurnStatusCompleted {
			t.Errorf("round %d metadata=%+v want=%+v", i, got, want)
		}
	}
	if !first.StartedAt.Equal(started) || !second.StartedAt.Equal(started.Add(3*time.Second)) || !reflect.DeepEqual(execution.Metadata, second) {
		t.Fatalf("first=%+v second=%+v returned=%+v", first, second, execution.Metadata)
	}
	assertTimingMatchesJournal(t, store, "s1", metadata)
}

func TestExecuteTurnTimingCapturesEachUsageBaseline(t *testing.T) {
	store := &timingStore{}
	now := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	client := &advancingTimingModel{
		now: &now, elapsed: []time.Duration{time.Second, 2 * time.Second},
		fakeModel: fakeModel{rounds: []fakeRound{
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 1000, CompletionTokens: 100}}, {Delta: "one"}, {Done: true}}},
			{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 2000, CompletionTokens: 200}}, {Delta: "two"}, {Done: true}}},
		}},
	}
	runner := NewEngine(client, &fakeUI{}, tool.NewRegistry(), store, "s1")
	runner.nowFn = func() time.Time { return now }
	timing := &TurnTiming{}
	for i := 1; i <= 2; i++ {
		result, err := runner.ExecuteTurn(context.Background(), message.Message{Role: message.RoleUser, Content: "question"}, timing)
		if err != nil {
			t.Fatal(err)
		}
		if result.Metadata.InputTokens != 1000*i || result.Metadata.OutputTokens != 100*i || result.Metadata.DurationMS != int64(1000*i) || result.Metadata.TurnID == "" {
			t.Errorf("round %d metadata=%+v", i, result.Metadata)
		}
	}
	if !reflect.DeepEqual(*timing, TurnTiming{}) {
		t.Fatalf("caller timing was mutated: %+v", timing)
	}
}

func assertTimingMatchesJournal(t *testing.T, store *session.JSONLStore, sessionID string, metadata []session.TurnMetadata) {
	t.Helper()
	records, err := store.LoadResolvedJournalRecords(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range metadata {
		matched := false
		for _, record := range records {
			if item.AssistantSeq != nil && record.Seq == *item.AssistantSeq && record.TurnID == item.TurnID && record.Kind == session.JournalAssistant {
				matched = true
			}
		}
		if !matched {
			t.Errorf("metadata has no matching assistant: %+v", item)
		}
	}
}
