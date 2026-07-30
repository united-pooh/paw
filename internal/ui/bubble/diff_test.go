package bubble

import (
	"encoding/json"
	"strings"
	"testing"
)

func editInputJSON(t *testing.T, file, oldStr, newStr string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": file, "old_string": oldStr, "new_string": newStr})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeInputJSON(t *testing.T, file, content string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": file, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFileMutationDiffPreviewReplaceRegion(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if !strings.Contains(got, "- │ return 1") {
		t.Fatalf("missing removed line in %q", got)
	}
	if !strings.Contains(got, "+ │ return 2") {
		t.Fatalf("missing added line in %q", got)
	}
}

func TestFileMutationDiffPreviewNewFile(t *testing.T) {
	input := writeInputJSON(t, "new.go", "package p\n")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if !strings.Contains(got, "+ │ package p") {
		t.Fatalf("new-file diff missing + line: %q", got)
	}
	if strings.Contains(got, " - ") {
		t.Fatalf("new-file diff must have no removed lines: %q", got)
	}
}

func TestFileMutationDiffPreviewFullFileViaOldContent(t *testing.T) {
	input := writeInputJSON(t, "a.go", "a\nb\nx\n")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "a\nb\nc\n")
	if !strings.Contains(got, "- │ c") || !strings.Contains(got, "+ │ x") {
		t.Fatalf("full-file diff wrong: %q", got)
	}
	// context lines preserved
	if !strings.Contains(got, "  │ a") && !strings.Contains(got, "│ a") {
		t.Fatalf("context line a missing: %q", got)
	}
}

func TestFileMutationDiffPreviewCollapsesUnchangedRuns(t *testing.T) {
	// 1 change in the middle of a long file: the collapsed-run marker must
	// appear before the first visible line (it only appears when the changed
	// region is not at the very start of the file).
	old := strings.Repeat("line\n", 20)
	newS := strings.Repeat("line\n", 10) + "changed\n" + strings.Repeat("line\n", 9)
	input := writeInputJSON(t, "a.go", newS)
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, old)
	if !strings.Contains(got, "···") {
		t.Fatalf("expected collapse marker in %q", got)
	}
}

func TestFileMutationDiffPreviewEmpty(t *testing.T) {
	input := []byte(`{"file_path":"a.go"}`)
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if got != "" {
		t.Fatalf("expected empty diff, got %q", got)
	}
}

func TestStructuredDiffNumbersRemoveBlockRewind(t *testing.T) {
	old := []string{"a", "b", "c", "d"}
	newS := []string{"a", "d"} // remove b,c
	lines := structuredDiff(old, newS)
	// Expect: ' ' a(1), '-' b(2), '-' c(3), ' ' d(2) — the remove block
	// advances through b,c then rewinds by numRemoved, so the line after
	// the block keeps the removal-start number (2), matching old behavior.
	if len(lines) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(lines), lines)
	}
	if lines[1].Kind != '-' || lines[1].Number != 2 || lines[1].Text != "b" {
		t.Fatalf("line[1] = %+v, want - b @2", lines[1])
	}
	if lines[2].Kind != '-' || lines[2].Number != 3 || lines[2].Text != "c" {
		t.Fatalf("line[2] = %+v, want - c @3", lines[2])
	}
	if lines[3].Kind != ' ' || lines[3].Number != 2 || lines[3].Text != "d" {
		t.Fatalf("line[3] = %+v, want ' ' d @2 (rewound)", lines[3])
	}
}

func TestStructuredDiffAddDoesNotAdvanceNumber(t *testing.T) {
	old := []string{"a", "c"}
	newS := []string{"a", "b", "c"} // insert b at line 2
	lines := structuredDiff(old, newS)
	// Expect: ' ' a(1), '+' b(2), ' ' c(2)
	if lines[1].Kind != '+' || lines[1].Number != 2 || lines[1].Text != "b" {
		t.Fatalf("line[1] = %+v, want + b @2", lines[1])
	}
	if lines[2].Kind != ' ' || lines[2].Number != 2 || lines[2].Text != "c" {
		t.Fatalf("line[2] = %+v, want ' ' c @2 (not advanced)", lines[2])
	}
}

func TestDiffCounts(t *testing.T) {
	lines := structuredDiff([]string{"a", "b", "c"}, []string{"a", "x", "c"})
	added, removed := diffCounts(lines)
	if added != 1 || removed != 1 {
		t.Fatalf("counts = +%d -%d, want +1 -1", added, removed)
	}
}
