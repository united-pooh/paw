package plan

import (
	"context"
	"testing"
)

func TestPlanConversationStatesCanWaitSaveAndRevise(t *testing.T) {
	for _, transition := range [][2]SessionStatus{
		{SessionClarifying, "awaiting_input"},
		{"awaiting_input", SessionDrafting},
		{SessionDrafting, "ready"},
		{"ready", SessionDrafting},
		{"ready", SessionPaused},
		{SessionPaused, "ready"},
		{"ready", SessionApproved},
	} {
		if !CanTransition(transition[0], transition[1]) {
			t.Errorf("conversation transition %s -> %s rejected", transition[0], transition[1])
		}
	}
	if SessionStatus("ready").Terminal() || SessionStatus("awaiting_input").Terminal() {
		t.Fatal("ready and awaiting_input must remain attached to the conversation")
	}
}

func TestReadyPlanEventStoreSupportsRevisionAndReload(t *testing.T) {
	docsDir, eventsDir := t.TempDir(), t.TempDir()
	store, err := NewEventStore(docsDir, eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	doc := PlanDoc{ID: "conversation", Title: "Conversation", Content: "# Plan\n\nFirst draft", Status: PlanDraft}
	if err := store.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}
	for _, status := range []PlanStatus{"ready", PlanDraft, "ready", PlanApproved} {
		doc, _, err = store.Get(ctx, doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		doc.Status = status
		if err := store.Update(ctx, doc); err != nil {
			t.Fatalf("update to %s: %v", status, err)
		}
		reopened, err := NewEventStore(docsDir, eventsDir)
		if err != nil {
			t.Fatal(err)
		}
		restored, ok, err := reopened.Get(ctx, doc.ID)
		if err != nil || !ok || restored.Status != status {
			t.Fatalf("reload %s: doc=%+v present=%v err=%v", status, restored, ok, err)
		}
	}
	doc.Status = PlanDraft
	if err := store.Update(ctx, doc); err == nil {
		t.Fatal("approved historical plan must remain immutable in status")
	}
}
