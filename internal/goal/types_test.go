package goal

import (
	"context"
	"testing"
	"time"
)

func TestGoalBudgetNormalize(t *testing.T) {
	got := (GoalBudget{MaxTurns: -1, MaxToolCalls: -2, MaxContinuations: -3, MaxNoProgress: -4}).Normalize()
	if got.MaxTurns != 0 || got.MaxToolCalls != 0 || got.MaxContinuations != 0 || got.MaxNoProgress != 0 {
		t.Fatalf("negative limits should normalize to zero: %+v", got)
	}
}

func TestGoalSnapshotIsCopy(t *testing.T) {
	goal := Goal{ID: "g1", SessionID: "s1", Objective: "ship it", Status: GoalRunning, Budget: GoalBudget{MaxTurns: 3}}
	snapshot := goal.Snapshot()
	snapshot.Objective = "changed"
	if goal.Objective != "ship it" {
		t.Fatal("snapshot mutation changed goal")
	}
}

func TestMemoryStoreCRUDAndSessionList(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	created := Goal{ID: "g1", SessionID: "s1", Objective: "first", Status: GoalDraft, CreatedAt: time.Now()}
	if err := store.Create(ctx, created); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, created); err == nil {
		t.Fatal("duplicate create should fail")
	}
	got, ok, err := store.Get(ctx, "g1")
	if err != nil || !ok || got.Objective != "first" {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}
	got.Objective = "updated"
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, "s1")
	if err != nil || len(list) != 1 || list[0].Objective != "updated" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := store.Delete(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(ctx, "g1"); ok {
		t.Fatal("deleted goal still exists")
	}
}
