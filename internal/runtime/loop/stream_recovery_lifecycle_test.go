package loop

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
	"paw/internal/ui"
)

type recoveryLifecycleUI struct{ orderedDisplayUI }

func (u *recoveryLifecycleUI) OnSystemMessage(event ui.SystemEvent) error {
	u.events = append(u.events, "system:"+event.Title)
	return nil
}

func TestInterruptedThinkingClosesBeforeRecoveryNotice(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []model.StreamEvent
		want   []string
	}{
		{
			name:   "legacy reasoning",
			events: []model.StreamEvent{{Thinking: "interrupted thought"}},
			want:   []string{"thinking:interrupted thought", "done"},
		},
		{
			name: "structured reasoning",
			events: []model.StreamEvent{
				{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "reasoning", BlockIndex: 0}},
				{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleDelta, Type: "reasoning", BlockIndex: 0, Delta: "interrupted thought"}},
			},
			want: []string{"reasoning-start:0:false", "reasoning-delta:0:interrupted thought", "done"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("stream interrupted")
			first := append(append([]model.StreamEvent(nil), test.events...), model.StreamEvent{Err: failure})
			streamer := &fakeModel{rounds: []fakeRound{
				{events: first},
				{events: []model.StreamEvent{{Thinking: "new thought"}, {Delta: "new answer"}, {Done: true}}},
			}}
			output := &recoveryLifecycleUI{}
			runner := NewEngineWithInstructionRoot(streamer, output, tool.NewRegistry(), nil, "", t.TempDir())
			history := []message.Message{{Role: message.RoleUser, Content: "finish work"}}
			turn := &TurnState{}
			if _, err := runner.runModelTurn(context.Background(), history, turn); !errors.Is(err, failure) {
				t.Fatalf("first request error = %v, want original failure", err)
			}
			runner.notifySystem("stream-recovery", "starting a separate response")
			if _, err := runner.runModelTurn(context.Background(), history, turn); err != nil {
				t.Fatal(err)
			}
			want := append(append([]string(nil), test.want...), "system:stream-recovery", "thinking:new thought", "assistant:new answer", "done")
			if !reflect.DeepEqual(output.events, want) {
				t.Fatalf("events = %#v, want distinct interrupted/recovered streams %#v", output.events, want)
			}
		})
	}
}

func TestInterruptedThinkingPersistsForDisplayOnly(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("stream interrupted")
	streamer := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Thinking: "interrupted thought"},
		{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "tool_call", BlockIndex: 1, ToolCallID: "unfinished", ToolName: "Write"}},
		{Err: failure},
	}}}}
	runner := NewEngineWithInstructionRoot(streamer, &fakeUI{}, tool.NewRegistry(), store, "thinking-partial", t.TempDir())
	if _, err := runner.RunTurn(context.Background(), "finish work"); !errors.Is(err, failure) {
		t.Fatalf("RunTurn error = %v, want original failure", err)
	}
	records, err := store.LoadResolvedRecords(context.Background(), "thinking-partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Kind != session.JournalAssistantPartial {
		t.Fatalf("record count = %d, want user and display-only partial reasoning", len(records))
	}
	partial := records[1].Message
	if partial.Content != "" || len(partial.AssistantParts) != 2 {
		t.Fatalf("partial = %#v, want reasoning and uncommitted tool part", partial)
	}
	for _, part := range partial.AssistantParts {
		if part.Status != message.AssistantPartPartial {
			t.Fatalf("unfinished part committed: %#v", part)
		}
	}
	if partial.AssistantParts[0].Reasoning == nil || partial.AssistantParts[0].Reasoning.Text != "interrupted thought" {
		t.Fatalf("partial reasoning = %#v", partial.AssistantParts[0])
	}
	snapshot, err := store.LoadSnapshot(context.Background(), "thinking-partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 || len(snapshot.ActiveHistory) != 1 || snapshot.ActiveHistory[0].Content != "finish work" {
		t.Fatalf("snapshot display=%#v model=%#v", snapshot.Messages, snapshot.ActiveHistory)
	}
	if snapshot.Recovery == nil || snapshot.Recovery.Error != failure.Error() || len(snapshot.Recovery.DroppedToolCalls) != 0 {
		t.Fatalf("recovery = %#v, want original error and no committed unfinished tool", snapshot.Recovery)
	}
}

func TestThinkingFailureClosesOnlyPublishedOutputOnce(t *testing.T) {
	for _, published := range []bool{false, true} {
		output := &fakeUI{}
		runner := NewEngine(&fakeModel{}, output, tool.NewRegistry(), nil, "")
		state := &turnState{}
		if published {
			if err := runner.appendThinking(state, "interrupted thought"); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			_, err := runner.failModelTurnWithPartial(state, context.Canceled)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("failure = %v, want unchanged cancellation", err)
			}
		}
		want := 0
		if published {
			want = 1
		}
		if output.doneCount != want {
			t.Fatalf("published=%v done=%d, want %d", published, output.doneCount, want)
		}
	}
}
