package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	return NewFileStore(dir)
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Fix the Login Flow!": "fix-the-login-flow",
		"  你好 world  ":        "world",
		"!!!!":                "plan",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	id, err := store.NextID(ctx, "Fix the Login Flow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(id), "-fix-the-login-flow-plan") {
		t.Fatalf("id = %q, want date-prefixed slug stem", id)
	}
	doc := PlanDoc{ID: id, Title: "Fix the Login Flow", Content: "# Plan\n\n- step 1\n", Status: PlanDraft}
	if err := store.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, id)
	if err != nil || !ok {
		t.Fatalf("Get = ok:%v err:%v", ok, err)
	}
	if got.Title != doc.Title || got.Content != doc.Content || got.Status != PlanDraft {
		t.Fatalf("Get = %#v", got)
	}
	if got.Path != filepath.Join(store.Dir(), string(id)+".md") {
		t.Fatalf("path = %q", got.Path)
	}

	approved, err := store.MarkApproved(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != PlanApproved {
		t.Fatalf("status = %s, want approved", approved.Status)
	}
	got, _, _ = store.Get(ctx, id)
	if got.Status != PlanApproved {
		t.Fatalf("persisted status = %s, want approved", got.Status)
	}

	docs, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != id {
		t.Fatalf("List = %#v", docs)
	}
}

func TestFileStorePreservesSessionIDInFrontMatter(t *testing.T) {
	store := newTestStore(t)
	doc := PlanDoc{ID: "session-plan", SessionID: "session-a", Title: "Scoped", Content: "# Scoped\n", Status: PlanDraft}
	if err := store.Create(context.Background(), doc); err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, ok, err := store.Get(context.Background(), doc.ID)
	if err != nil || !ok {
		t.Fatalf("Get = ok:%t err:%v", ok, err)
	}
	if loaded.SessionID != "session-a" {
		t.Fatalf("SessionID = %q, want session-a", loaded.SessionID)
	}
}

func TestFileStoreNextIDAppendsSuffix(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, err := store.NextID(ctx, "same title")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, PlanDoc{ID: first, Content: "a"}); err != nil {
		t.Fatal(err)
	}
	second, err := store.NextID(ctx, "same title")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || !strings.HasSuffix(string(second), "-2") {
		t.Fatalf("second id = %q, want suffixed variant of %q", second, first)
	}
}

func TestFileStoreRejectsTraversalIDs(t *testing.T) {
	store := newTestStore(t)
	if err := store.Create(context.Background(), PlanDoc{ID: "../evil", Content: "x"}); err == nil {
		t.Fatal("traversal id accepted")
	}
}

func TestFileStoreMissingFile(t *testing.T) {
	store := newTestStore(t)
	_, ok, err := store.Get(context.Background(), "2026-01-01-nope-plan")
	if err != nil || ok {
		t.Fatalf("Get = ok:%v err:%v, want not found", ok, err)
	}
	_ = os.RemoveAll(store.Dir())
	docs, err := store.List(context.Background())
	if err != nil || len(docs) != 0 {
		t.Fatalf("List on missing dir = %#v err:%v", docs, err)
	}
}
