package todo

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrokerPublishesSnapshotsInOrder(t *testing.T) {
	broker := NewBroker()
	first := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusInProgress}}}
	second := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusCompleted}}}

	if !broker.Publish(first) || !broker.Publish(second) {
		t.Fatal("Publish() rejected an open broker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	gotFirst, err := broker.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotSecond, err := broker.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotFirst.Items[0].Status != StatusInProgress || gotSecond.Items[0].Status != StatusCompleted {
		t.Fatalf("events out of order: %#v %#v", gotFirst, gotSecond)
	}
}

func TestBrokerCopiesPublishedSnapshot(t *testing.T) {
	broker := NewBroker()
	snapshot := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}}
	broker.Publish(snapshot)
	snapshot.Items[0].Content = "mutated"

	got, err := broker.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got.Items[0].Content = "consumer mutation"
	latest, ok := broker.Latest()
	if !ok || latest.Items[0].Content != "A" {
		t.Fatalf("broker state mutated: %#v", latest)
	}
}

func TestBrokerNextHonorsContext(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := broker.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() error = %v", err)
	}
}

func TestBrokerCloseWakesNext(t *testing.T) {
	broker := NewBroker()
	errCh := make(chan error, 1)
	go func() {
		_, err := broker.Next(context.Background())
		errCh <- err
	}()
	broker.Close()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrBrokerClosed) {
			t.Fatalf("Next() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next() remained blocked after Close")
	}
}

func TestBrokerCloseDrainsQueuedEvents(t *testing.T) {
	broker := NewBroker()
	broker.Publish(Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}})
	broker.Close()
	if _, err := broker.Next(context.Background()); err != nil {
		t.Fatalf("Next() queued event error = %v", err)
	}
	if _, err := broker.Next(context.Background()); !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("Next() after drain error = %v", err)
	}
	if broker.Publish(Snapshot{}) {
		t.Fatal("Publish() accepted event after Close")
	}
	broker.Close()
}

func TestBrokerPublishDoesNotWaitForConsumer(t *testing.T) {
	broker := NewBroker()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			broker.Publish(Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish() blocked without a consumer")
	}
}
