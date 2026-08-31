package app

import (
	"errors"
	"testing"
	"time"
)

func TestEventHubSubscribeAtomicallySwitchesReplayToLive(t *testing.T) {
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	first := publishTestEvent(t, hub, "first")
	entered := make(chan struct{})
	release := make(chan struct{})
	hub.subscribeHook = func() {
		close(entered)
		<-release
	}

	subscribed := make(chan Subscription, 1)
	go func() {
		subscription, err := hub.Subscribe(EventCursor{StreamID: "stream", Sequence: first.Sequence})
		if err != nil {
			t.Errorf("Subscribe() error = %v", err)
			return
		}
		subscribed <- subscription
	}()
	<-entered
	published := make(chan AppEvent, 1)
	go func() { published <- publishTestEvent(t, hub, "live") }()
	select {
	case <-published:
		t.Fatal("Publish completed while Subscribe held the atomic transition lock")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	subscription := <-subscribed
	defer subscription.Close()
	if len(subscription.Replay) != 0 {
		t.Fatalf("Replay = %#v, want empty after cursor", subscription.Replay)
	}
	live := <-published
	select {
	case got := <-subscription.Events:
		if got.Sequence != live.Sequence || string(got.Payload) != string(live.Payload) {
			t.Fatalf("live event = %#v, want %#v", got, live)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber missed concurrent live event")
	}
}

func TestEventHubReplayAndCursorResets(t *testing.T) {
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream", MaxEvents: 2})
	publishTestEvent(t, hub, "one")
	second := publishTestEvent(t, hub, "two")
	third := publishTestEvent(t, hub, "three")

	replay, err := hub.Subscribe(EventCursor{StreamID: "stream", Sequence: second.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if len(replay.Replay) != 1 || replay.Replay[0].Sequence != third.Sequence {
		t.Fatalf("replay = %#v", replay.Replay)
	}

	for _, test := range []struct {
		name   string
		cursor EventCursor
		want   ResetReason
	}{
		{name: "stream mismatch", cursor: EventCursor{StreamID: "old", Sequence: 1}, want: ResetStreamMismatch},
		{name: "cursor too old", cursor: EventCursor{StreamID: "stream", Sequence: 0}, want: ResetCursorTooOld},
		{name: "cursor ahead", cursor: EventCursor{StreamID: "stream", Sequence: 99}, want: ResetCursorAhead},
	} {
		t.Run(test.name, func(t *testing.T) {
			subscription, err := hub.Subscribe(test.cursor)
			if err != nil {
				t.Fatal(err)
			}
			if got := <-subscription.Reset; got != test.want {
				t.Fatalf("reset = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEventHubSlowConsumerGetsResetInsteadOfDroppedEvents(t *testing.T) {
	hub := newTestEventHub(t, EventHubConfig{
		WorkspaceID: "workspace", StreamID: "stream", SubscriberMaxEvents: 1, SubscriberMaxBytes: 1 << 20,
	})
	subscription, err := hub.Subscribe(EventCursor{StreamID: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	publishTestEvent(t, hub, "one")
	publishTestEvent(t, hub, "two")
	publishTestEvent(t, hub, "three")
	select {
	case reason := <-subscription.Reset:
		if reason != ResetSlowConsumer {
			t.Fatalf("reset reason = %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("slow consumer did not receive reset")
	}
}

func TestEventHubRejectsOversizedPayloadAndCloseIsIdempotent(t *testing.T) {
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream", PayloadMaxBytes: 8})
	event, err := NewAppEvent("workspace", "", "", EventSystemMessage, time.Time{}, 0, map[string]string{"body": "too long"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Publish(event); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("Publish() error = %v, want event too large", err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Publish(event); !errors.Is(err, ErrEventHubClosed) {
		t.Fatalf("Publish(after close) error = %v", err)
	}
}

func newTestEventHub(t *testing.T, cfg EventHubConfig) *EventHub {
	t.Helper()
	hub, err := NewEventHub(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	return hub
}

func publishTestEvent(t *testing.T, hub *EventHub, text string) AppEvent {
	t.Helper()
	event, err := NewAppEvent("workspace", "session", "turn", EventAssistantDelta, time.Now(), 0, AssistantDeltaPayload{PartID: "part", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	published, err := hub.Publish(event)
	if err != nil {
		t.Fatal(err)
	}
	return published
}
