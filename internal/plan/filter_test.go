package plan

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestModeFilterAllowlist(t *testing.T) {
	dir := t.TempDir()
	filter := ModeFilter(dir)

	for _, name := range []string{"Read", "Glob", "Grep", "LS", "WebFetch", "question", "plan_finalize", "codegraph__search"} {
		if err := filter(name, nil); err != nil {
			t.Fatalf("read-only tool %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"Bash", "Edit", "Subagent", "todo", "mcp__exec"} {
		if err := filter(name, nil); err == nil {
			t.Fatalf("mutating tool %q allowed", name)
		}
	}
}

func TestModeFilterWritePathRestriction(t *testing.T) {
	dir := t.TempDir()
	filter := ModeFilter(dir)

	// Advertisement (nil input) is allowed; the path is checked at call time.
	if err := filter("Write", nil); err != nil {
		t.Fatalf("Write advertisement rejected: %v", err)
	}

	inside := filepath.Join(dir, "2026-01-01-x-plan.md")
	payload := json.RawMessage(`{"file_path":"` + inside + `","content":"x"}`)
	if err := filter("Write", payload); err != nil {
		t.Fatalf("Write inside plans dir rejected: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "app.go")
	payload = json.RawMessage(`{"file_path":"` + outside + `","content":"x"}`)
	if err := filter("Write", payload); err == nil {
		t.Fatal("Write outside plans dir allowed")
	}

	rel := json.RawMessage(`{"file_path":"../escape.go","content":"x"}`)
	if err := filter("Write", rel); err == nil {
		t.Fatal("Write with traversal path allowed")
	}
}

func TestEnsureUnder(t *testing.T) {
	dir := t.TempDir()
	if err := ensureUnder(filepath.Join(dir, "a.md"), dir); err != nil {
		t.Fatalf("under dir rejected: %v", err)
	}
	if err := ensureUnder(dir, dir); err != nil {
		t.Fatalf("dir itself rejected: %v", err)
	}
	if err := ensureUnder(filepath.Join(dir, "..", "b.md"), dir); err == nil {
		t.Fatal("escape accepted")
	}
	if err := ensureUnder("", dir); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestModeFilterRejectsBadWriteInput(t *testing.T) {
	filter := ModeFilter(t.TempDir())
	err := filter("Write", json.RawMessage(`{"content":"no path"}`))
	if err == nil || !strings.Contains(err.Error(), "Write") {
		t.Fatalf("bad Write input err = %v", err)
	}
}
