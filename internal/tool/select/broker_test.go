package selecttool

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func brokerRequest(prompt string) Request {
	return Request{Prompt: prompt, Mode: ModeSingle, Options: []Option{{ID: "a", Label: "A"}}, MinSelect: 1, MaxSelect: 1}
}

func TestBrokerPublishesAndCompletesRequest(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() { r, e := b.Ask(ctx, brokerRequest("Pick")); resultCh <- r; errCh <- e }()
	event, err := b.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventRequest || event.Request.ID == "" {
		t.Fatalf("event=%#v", event)
	}
	if !b.Complete(event.Request.ID, Result{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}}) {
		t.Fatal("complete false")
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if got := <-resultCh; !reflect.DeepEqual(got.SelectedOptions, []SelectedOption{{ID: "a", Label: "A"}}) {
		t.Fatalf("got=%#v", got)
	}
}

func TestBrokerQueuesRequestsFIFO(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errs := make(chan error, 2)
	go func() { _, e := b.Ask(ctx, brokerRequest("First")); errs <- e }()
	first, e := b.NextEvent(ctx)
	if e != nil {
		t.Fatal(e)
	}
	go func() { _, e := b.Ask(ctx, brokerRequest("Second")); errs <- e }()
	short, c := context.WithTimeout(ctx, 30*time.Millisecond)
	defer c()
	if _, e = b.NextEvent(short); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("event before complete: %v", e)
	}
	b.Complete(first.Request.ID, Result{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}})
	second, e := b.NextEvent(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if second.Request.Prompt != "Second" || second.Request.ID == first.Request.ID {
		t.Fatalf("second=%#v", second)
	}
	b.Complete(second.Request.ID, Result{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}})
	for range 2 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
}

func TestBrokerCompleteAndWrongID(t *testing.T) {
	b := NewBroker()
	done := make(chan error, 1)
	go func() { _, e := b.Ask(context.Background(), brokerRequest("Pick")); done <- e }()
	e, _ := b.NextEvent(context.Background())
	if b.Complete("wrong", Result{}) {
		t.Fatal("wrong id accepted")
	}
	if !b.Complete(e.Request.ID, Result{}) {
		t.Fatal("complete false")
	}
	if b.Complete(e.Request.ID, Result{}) {
		t.Fatal("duplicate accepted")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestBrokerContextCancellationInvalidatesActiveRequest(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := b.Ask(ctx, brokerRequest("Pick")); done <- e }()
	req, _ := b.NextEvent(context.Background())
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatalf("err=%v", e)
	}
	inv, e := b.NextEvent(context.Background())
	if e != nil || inv.Kind != EventInvalidated || inv.RequestID != req.Request.ID {
		t.Fatalf("event=%#v err=%v", inv, e)
	}
}

func TestBrokerCancelledQueuedRequestDoesNotBlockNext(t *testing.T) {
	b := NewBroker()
	aDone := make(chan error, 1)
	go func() { _, e := b.Ask(context.Background(), brokerRequest("A")); aDone <- e }()
	a, _ := b.NextEvent(context.Background())
	bctx, bcancel := context.WithCancel(context.Background())
	bDone := make(chan error, 1)
	go func() { _, e := b.Ask(bctx, brokerRequest("B")); bDone <- e }()
	cDone := make(chan error, 1)
	go func() { _, e := b.Ask(context.Background(), brokerRequest("C")); cDone <- e }()
	time.Sleep(10 * time.Millisecond)
	bcancel()
	if e := <-bDone; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	b.Complete(a.Request.ID, Result{})
	<-aDone
	c, e := b.NextEvent(context.Background())
	if e != nil || c.Request.Prompt != "C" {
		t.Fatalf("event=%#v err=%v", c, e)
	}
	b.Complete(c.Request.ID, Result{})
	<-cDone
}

func TestBrokerCloseReleasesRequestsAndNextEventCloses(t *testing.T) {
	b := NewBroker()
	errs := make(chan error, 2)
	go func() { _, e := b.Ask(context.Background(), brokerRequest("A")); errs <- e }()
	_, _ = b.NextEvent(context.Background())
	go func() { _, e := b.Ask(context.Background(), brokerRequest("B")); errs <- e }()
	time.Sleep(10 * time.Millisecond)
	b.Close()
	b.Close()
	for range 2 {
		if e := <-errs; !errors.Is(e, ErrBrokerClosed) {
			t.Fatalf("err=%v", e)
		}
	}
	for {
		e, err := b.NextEvent(context.Background())
		if errors.Is(err, ErrBrokerClosed) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = e
	}
}

func TestBrokerCompleteCancelRace(t *testing.T) {
	for range 100 {
		b := NewBroker()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, e := b.Ask(ctx, brokerRequest("race")); done <- e }()
		e, _ := b.NextEvent(context.Background())
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); b.Complete(e.Request.ID, Result{}) }()
		go func() { defer wg.Done(); cancel() }()
		wg.Wait()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked")
		}
		b.Close()
	}
}
