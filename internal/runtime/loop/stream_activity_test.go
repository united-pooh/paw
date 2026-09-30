package loop

import (
	"context"
	"errors"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
)

type streamActivityUI struct {
	fakeUI
	order      []string
	activities []model.StreamActivity
	requestIDs []string
	ended      []time.Time
}

func (u *streamActivityUI) OnAssistantDelta(text string) error {
	u.order = append(u.order, "display")
	return u.fakeUI.OnAssistantDelta(text)
}

func (u *streamActivityUI) OnModelStreamActivity(id string, activity model.StreamActivity) {
	u.order = append(u.order, "arrival")
	u.requestIDs = append(u.requestIDs, id)
	u.activities = append(u.activities, activity)
}

func (u *streamActivityUI) OnModelRequestEnd(id string, at time.Time) {
	u.order = append(u.order, "end")
	u.requestIDs = append(u.requestIDs, id)
	u.ended = append(u.ended, at)
}

func TestStreamActivityPrecedesBufferedDisplayAndPreservesArrivalTime(t *testing.T) {
	at := time.Unix(100, 0)
	output := &streamActivityUI{}
	streamer := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: " ", Activity: &model.StreamActivity{At: at, Text: " "}},
		{Delta: "hello", Activity: &model.StreamActivity{At: at.Add(time.Second), Text: "hello"}},
		{Done: true, ReceivedAt: at.Add(2 * time.Second)},
	}}}}
	runner := NewEngine(streamer, output, tool.NewRegistry(), &fakeStore{}, "activity-session")
	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(output.order) < 4 || output.order[0] != "arrival" || output.order[1] != "arrival" || output.order[2] != "display" {
		t.Fatalf("buffered display changed arrival ordering: %v", output.order)
	}
	if len(output.activities) != 2 || output.activities[0].At != at || output.activities[1].At != at.Add(time.Second) {
		t.Fatalf("arrival samples replaced by flush time: %+v", output.activities)
	}
	if len(output.ended) != 1 || output.ended[0] != at.Add(2*time.Second) {
		t.Fatalf("request end lost provider arrival time: %v", output.ended)
	}
	for _, id := range output.requestIDs {
		if id == "" || id != output.requestIDs[0] {
			t.Fatalf("request identity changed: %v", output.requestIDs)
		}
	}
}

func TestStreamActivityDoesNotInferRatesFromLegacyDisplayAndFinishesFailures(t *testing.T) {
	for _, ending := range []string{"done", "error", "eof"} {
		t.Run(ending, func(t *testing.T) {
			output := &streamActivityUI{}
			at := time.Now()
			events := []model.StreamEvent{{Thinking: "completed-only reasoning"}, {Delta: "completed-only text"}}
			switch ending {
			case "done":
				events = append(events, model.StreamEvent{Done: true, ReceivedAt: at})
			case "error":
				events = append(events, model.StreamEvent{Err: errors.New("fixture transport failure"), ReceivedAt: at})
			}
			runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: events}}}, output, tool.NewRegistry(), &fakeStore{}, "activity-session")
			_, _ = runner.runModelTurn(context.Background(), nil, nil)
			if len(output.activities) != 0 || len(output.ended) != 1 {
				t.Fatalf("fallback synthesized live activity or did not finish: activity=%v ends=%v", output.activities, output.ended)
			}
			if ending == "eof" {
				if output.ended[0].Before(at) || output.ended[0].After(time.Now()) {
					t.Fatalf("EOF completion timestamp = %v", output.ended[0])
				}
			} else if output.ended[0] != at {
				t.Fatalf("terminal packet arrival time was lost: %v", output.ended[0])
			}
		})
	}
}
