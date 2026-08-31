package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResourceGovernorBoundsSharedWorkerCapacity(t *testing.T) {
	governor := NewResourceGovernor(2)
	releaseFirst, err := governor.AcquireWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := governor.AcquireWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()

	acquired := make(chan func(), 1)
	go func() {
		release, err := governor.AcquireWorker(context.Background())
		if err != nil {
			return
		}
		acquired <- release
	}()
	select {
	case <-acquired:
		t.Fatal("third worker acquired before a shared slot was released")
	case <-time.After(30 * time.Millisecond):
	}

	releaseFirst()
	select {
	case releaseThird := <-acquired:
		releaseThird()
		releaseThird()
	case <-time.After(time.Second):
		t.Fatal("third worker did not acquire after release")
	}
}

func TestResourceGovernorCancelReturnsCapacityError(t *testing.T) {
	governor := NewResourceGovernor(1)
	release, err := governor.AcquireWorker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = governor.AcquireWorker(ctx)
	if !errors.Is(err, ErrResourceCapacity) || !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireWorker() error = %v", err)
	}
}
