package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/session"
	"paw/internal/todo"
	"paw/internal/tool"
)

func TestToolsetsRemainIsolatedAcrossRuntimesAndRebinds(t *testing.T) {
	ctx := context.Background()
	rootA := t.TempDir()
	rootB := t.TempDir()
	storeA, err := session.NewJSONLStore(filepath.Join(rootA, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := session.NewJSONLStore(filepath.Join(rootB, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		store *session.JSONLStore
		id    string
	}{
		{storeA, "a1"}, {storeA, "a2"}, {storeB, "b1"},
	} {
		if _, err := item.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: item.id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := storeA.Append(ctx, "a2", testUserMessage("needle-a2")); err != nil {
		t.Fatal(err)
	}
	if err := storeB.Append(ctx, "b1", testUserMessage("needle-b1")); err != nil {
		t.Fatal(err)
	}

	brokerA := todo.NewBroker()
	brokerB := todo.NewBroker()
	defer brokerA.Close()
	defer brokerB.Close()
	toolsA := NewToolset(brokerA)
	toolsB := NewToolset(brokerB)
	registryA := tool.NewRegistry()
	registryB := tool.NewRegistry()
	if err := toolsA.RegisterMain(registryA); err != nil {
		t.Fatal(err)
	}
	if err := toolsB.RegisterMain(registryB); err != nil {
		t.Fatal(err)
	}
	if err := toolsA.BindSession(storeA, "a1", filepath.Join(rootA, "memory", "progress.md")); err != nil {
		t.Fatal(err)
	}
	if err := toolsB.BindSession(storeB, "b1", filepath.Join(rootB, "memory", "progress.md")); err != nil {
		t.Fatal(err)
	}
	if err := toolsA.BindSession(storeA, "a2", filepath.Join(rootA, "memory", "progress.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := toolsA.Todo().Run(ctx, json.RawMessage(`{"items":[{"id":"a","content":"A","status":"in_progress"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := toolsB.Todo().Run(ctx, json.RawMessage(`{"items":[{"id":"b","content":"B","status":"in_progress"}]}`)); err != nil {
		t.Fatal(err)
	}
	assertTodoSession(t, storeA, "a1", 0)
	assertTodoSession(t, storeA, "a2", 1)
	assertTodoSession(t, storeB, "b1", 1)

	outA, err := toolsA.SearchTranscript().Run(ctx, json.RawMessage(`{"query":"needle-a2"}`))
	if err != nil || !strings.Contains(outA, "needle-a2") {
		t.Fatalf("search A = %q, %v", outA, err)
	}
	outB, err := toolsB.SearchTranscript().Run(ctx, json.RawMessage(`{"query":"needle-b1"}`))
	if err != nil || !strings.Contains(outB, "needle-b1") {
		t.Fatalf("search B = %q, %v", outB, err)
	}

	ariadne := `{"content":"## 方向\nA\n\n## 进度\nA\n\n## 关键决策\nA\n\n## 下一步\nA\n\n## 教训\nA"}`
	if _, err := toolsA.Ariadne().Run(ctx, json.RawMessage(ariadne)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(storeA.Root(), "sessions", "a2", "ariadne.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(storeB.Root(), "sessions", "b1", "ariadne.md")); !os.IsNotExist(err) {
		t.Fatalf("runtime B ariadne should not exist: %v", err)
	}
}

func assertTodoSession(t *testing.T, store *session.JSONLStore, sessionID string, want int) {
	t.Helper()
	records, err := store.LoadResolvedJournalRecords(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, record := range records {
		if record.Kind == session.JournalTodoSnapshot {
			got++
		}
	}
	if got != want {
		t.Fatalf("session %s todo records = %d, want %d", sessionID, got, want)
	}
}
