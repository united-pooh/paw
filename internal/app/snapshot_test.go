package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"paw/internal/actor"
	"paw/internal/session"
)

func TestSessionSnapshotIncludesStreamingStateAndEventWatermark(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.StartPart(StreamingPart{PartID: "part", SessionID: "session", TurnID: "turn", Kind: "assistant"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := coordinator.AppendPart("part", "hello"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	published := publishTestEvent(t, hub, "hello")
	service := NewSessionService(store, coordinator)

	snapshot, err := service.ConsistentSnapshot(context.Background(), "session", SnapshotRequest{}, hub)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StreamID != "stream" || snapshot.EventSequence != published.Sequence || snapshot.ActiveTurnID != "turn" {
		t.Fatalf("snapshot watermark/state = %#v", snapshot)
	}
	if len(snapshot.Parts) != 1 || snapshot.Parts[0].Text != "hello" {
		t.Fatalf("snapshot parts = %#v", snapshot.Parts)
	}
	if snapshot.SessionVersion != 1 {
		t.Fatalf("session version = %d, want 1 (delta must not increment)", snapshot.SessionVersion)
	}
}

func TestDeltaBatcherFlushesByTimeSizeAndCompletion(t *testing.T) {
	clock := actor.NewVirtualClock()
	var mu sync.Mutex
	var batches []DeltaBatch
	batcher := NewDeltaBatcher("part", clock, func(batch DeltaBatch) {
		mu.Lock()
		batches = append(batches, batch)
		mu.Unlock()
	})
	batcher.SetLimits(25*time.Millisecond, 5)
	batcher.Add("ab")
	clock.Advance(24 * time.Millisecond)
	mu.Lock()
	if len(batches) != 0 {
		t.Fatalf("early batches = %#v", batches)
	}
	mu.Unlock()
	clock.Advance(time.Millisecond)
	assertBatches(t, &mu, batches, []DeltaBatch{{PartID: "part", Offset: 0, Text: "ab"}})

	batcher.Add("12345")
	mu.Lock()
	if len(batches) != 2 || batches[1].Offset != 2 || batches[1].Text != "12345" {
		t.Fatalf("size batches = %#v", batches)
	}
	mu.Unlock()
	batcher.Add("尾")
	batcher.Close()
	mu.Lock()
	if len(batches) != 3 || batches[2].Offset != 7 || batches[2].Text != "尾" {
		t.Fatalf("close batches = %#v", batches)
	}
	mu.Unlock()
	if batcher.Offset() != 10 {
		t.Fatalf("Offset() = %d, want UTF-8 byte length 10", batcher.Offset())
	}
}

func TestPartOffsetsAreStableUTF8Bytes(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.StartPart(StreamingPart{PartID: "part", SessionID: "session", TurnID: "turn", Kind: "assistant"}); err != nil {
		t.Fatal(err)
	}
	part, _, err := coordinator.AppendPart("part", "你")
	if err != nil {
		t.Fatal(err)
	}
	if len(part.Text) != 3 {
		t.Fatalf("part text byte length = %d, want 3", len(part.Text))
	}
	part, _, err = coordinator.AppendPart("part", "好")
	if err != nil {
		t.Fatal(err)
	}
	if len(part.Text) != 6 {
		t.Fatalf("part text byte length = %d, want 6", len(part.Text))
	}
	part, _, err = coordinator.CompletePart("part")
	if err != nil || !part.Completed {
		t.Fatalf("CompletePart() = %#v, %v", part, err)
	}
}

func assertBatches(t *testing.T, mu *sync.Mutex, got, want []DeltaBatch) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("batches = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("batch[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}
