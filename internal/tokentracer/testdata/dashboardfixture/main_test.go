package main

import (
	"testing"

	"paw/internal/tokentracer"
)

func TestSeedProvidesDashboardScaleAndFailureSignals(t *testing.T) {
	tracer := tokentracer.New("Token Tracer E2E")
	seed(tracer)

	snapshot := tracer.Snapshot()
	if got := len(snapshot.Pipeline.Stages); got != 3 {
		t.Fatalf("stages = %d, want 3", got)
	}
	if got := len(snapshot.Events); got != fixtureEventCount {
		t.Fatalf("retained events = %d, want %d", got, fixtureEventCount)
	}
	if snapshot.Pipeline.Total.CacheCreation == 0 {
		t.Fatalf("pipeline total = %#v, want cache creation tokens", snapshot.Pipeline.Total)
	}
	failed := false
	for _, stage := range snapshot.Pipeline.Stages {
		if stage != nil && stage.Status == "failed" {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatalf("pipeline stages = %#v, want one failed stage", snapshot.Pipeline.Stages)
	}
}
