package todo

import (
	"reflect"
	"testing"
	"time"
)

func TestSnapshotCloneDoesNotShareItems(t *testing.T) {
	original := Snapshot{
		Explanation: "start",
		Items:       []Item{{ID: "inspect", Content: "Inspect code", Status: StatusInProgress}},
		UpdatedAt:   time.Unix(10, 0).UTC(),
	}
	cloned := original.Clone()
	cloned.Items[0].Content = "changed"

	if original.Items[0].Content != "Inspect code" {
		t.Fatalf("Clone() shared Items: %#v", original.Items)
	}
}

func TestSnapshotProgress(t *testing.T) {
	snapshot := Snapshot{Items: []Item{
		{ID: "a", Content: "A", Status: StatusCompleted},
		{ID: "b", Content: "B", Status: StatusInProgress},
		{ID: "c", Content: "C", Status: StatusPending},
	}}
	if got := snapshot.CompletedCount(); got != 1 {
		t.Fatalf("CompletedCount() = %d, want 1", got)
	}
	if snapshot.AllCompleted() {
		t.Fatal("AllCompleted() = true for incomplete snapshot")
	}
}

func TestEmptySnapshotIsClearedNotCompleted(t *testing.T) {
	snapshot := Snapshot{Items: []Item{}}
	if !snapshot.Cleared() {
		t.Fatal("Cleared() = false")
	}
	if snapshot.AllCompleted() {
		t.Fatal("AllCompleted() = true for empty list")
	}
}

func TestUpdateResultClone(t *testing.T) {
	result := UpdateResult{Accepted: true, Snapshot: Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}}}
	cloned := result.Clone()
	cloned.Snapshot.Items[0].ID = "b"
	if reflect.DeepEqual(result, cloned) {
		t.Fatal("Clone() still aliases nested snapshot")
	}
}

func TestStatusValuesAreStable(t *testing.T) {
	if StatusPending != "pending" || StatusInProgress != "in_progress" || StatusCompleted != "completed" {
		t.Fatalf("status values changed: %q %q %q", StatusPending, StatusInProgress, StatusCompleted)
	}
}

func TestAllCompleted(t *testing.T) {
	snapshot := Snapshot{Items: []Item{
		{ID: "a", Content: "A", Status: StatusCompleted},
		{ID: "b", Content: "B", Status: StatusCompleted},
	}}
	if !snapshot.AllCompleted() || snapshot.CompletedCount() != 2 || snapshot.TotalCount() != 2 {
		t.Fatalf("unexpected progress: %d/%d all=%v", snapshot.CompletedCount(), snapshot.TotalCount(), snapshot.AllCompleted())
	}
}
