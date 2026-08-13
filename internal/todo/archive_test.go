package todo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveSnapshot() Snapshot {
	return Snapshot{
		Explanation: "test task",
		Items: []Item{
			{ID: "1", Content: "first done", Status: StatusCompleted},
			{ID: "2", Content: "still pending", Status: StatusPending},
			{ID: "3", Content: "second done", Status: StatusCompleted},
		},
	}
}

func TestArchiveCreatesProgressFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory", "progress.md")
	w, err := NewArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w.ArchiveCompleted(context.Background(), archiveSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("archived = %d, want 2", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "# Progress\n\n") {
		t.Fatalf("progress.md head = %q, want # Progress header", got)
	}
	for _, want := range []string{"- [x] first done <!-- todo:1 -->", "- [x] second done <!-- todo:3 -->"} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress.md missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "- [x] still pending") {
		t.Fatalf("progress.md must not contain pending item: %q", got)
	}
}

func TestArchiveIsIdempotentAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.md")
	w1, err := NewArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.ArchiveCompleted(context.Background(), archiveSnapshot()); err != nil {
		t.Fatal(err)
	}
	// 新 writer（模拟重启）重新扫描文件后重复归档同一快照：不重复追加。
	w2, err := NewArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := w2.ArchiveCompleted(context.Background(), archiveSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("re-archive = %d, want 0 (idempotent)", n)
	}
	data, _ := os.ReadFile(path)
	if got := strings.Count(string(data), "- [x] first done"); got != 1 {
		t.Fatalf("first done appears %d times, want 1", got)
	}
}

func TestArchiveAppendsOnlyNewCompleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.md")
	w, err := NewArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ArchiveCompleted(context.Background(), archiveSnapshot()); err != nil {
		t.Fatal(err)
	}
	next := Snapshot{Items: []Item{{ID: "1", Content: "first done", Status: StatusCompleted}, {ID: "4", Content: "newly done", Status: StatusCompleted}}}
	n, err := w.ArchiveCompleted(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("appended = %d, want 1", n)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "- [x] newly done <!-- todo:4 -->") {
		t.Fatalf("missing newly done line: %q", data)
	}
}

func TestArchiveFlattensMultilineContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.md")
	w, err := NewArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{Items: []Item{{ID: "9", Content: "line1\nline2  spaced", Status: StatusCompleted}}}
	if _, err := w.ArchiveCompleted(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "- [x] line1 line2 spaced <!-- todo:9 -->") {
		t.Fatalf("multiline not flattened: %q", data)
	}
	if strings.Count(string(data), "\n- [x]") != 1 {
		t.Fatalf("archived line count wrong: %q", data)
	}
}
