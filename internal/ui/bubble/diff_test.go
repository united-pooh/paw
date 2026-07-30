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

func TestFormatFileMutationToolCallBodyEditSummary(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatFileMutationToolCallBody("Edit", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Edit · +1 -1") {
		t.Fatalf("first line = %q, want summary Edit · +1 -1", first)
	}
	if !strings.Contains(body, "a.go") {
		t.Fatalf("missing target in body: %q", body)
	}
	if !strings.Contains(body, "+ │ return 2") || !strings.Contains(body, "- │ return 1") {
		t.Fatalf("missing diff lines: %q", body)
	}
}

func TestFormatFileMutationToolCallBodyWriteNewFileSummary(t *testing.T) {
	input := writeInputJSON(t, "new.go", "package p\n")
	body := formatFileMutationToolCallBody("Write", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Write · +1 -0") {
		t.Fatalf("first line = %q, want Write · +1 -0", first)
	}
}

func TestFormatFileMutationToolCallBodyNoDiffNoSummary(t *testing.T) {
	// Neither old nor new content: no summary token, just the name.
	input := []byte(`{"file_path":"a.go"}`)
	body := formatFileMutationToolCallBody("Write", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if first != "Write" {
		t.Fatalf("first line = %q, want Write (no counts)", first)
	}
}

func TestFormatRunningToolCallBodyInsertsStatusBeforeCounts(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatRunningToolCallBody("Edit", input, "")
	first := firstToolEntryLine(body)
	// status inserted as 2nd part: Edit · running · +1 -1
	if !strings.Contains(first, "Edit · running · +1 -1") {
		t.Fatalf("first line = %q, want Edit · running · +1 -1", first)
	}
	// Completing the tool replaces running with ok; counts survive.
	completed := completeRunningToolCallBody(body, "ok")
	firstCompleted := firstToolEntryLine(completed)
	if !strings.Contains(firstCompleted, "Edit · ok · +1 -1") {
		t.Fatalf("completed first line = %q, want Edit · ok · +1 -1", firstCompleted)
	}
}

func TestEditRunningBodyRendersRegionDiff(t *testing.T) {
	old := "func run() int {\n\treturn 1\n}\n"
	input := editInputJSON(t, "internal/foo.go", "\treturn 1", "\treturn 2")
	body := formatRunningToolCallBody("Edit", input, old)

	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Edit · running · +1 -1") {
		t.Fatalf("summary line = %q", first)
	}
	if !strings.Contains(body, "internal/foo.go") {
		t.Fatalf("missing target: %q", body)
	}
	if !strings.Contains(body, "- │ \treturn 1") || !strings.Contains(body, "+ │ \treturn 2") {
		t.Fatalf("missing region diff lines: %q", body)
	}
}

func TestWriteRunningBodyRendersFullFileDiff(t *testing.T) {
	old := "package p\n\nfunc a() int { return 1 }\n"
	newContent := "package p\n\nfunc a() int { return 2 }\n"
	input := writeInputJSON(t, "internal/foo.go", newContent)
	body := formatRunningToolCallBody("Write", input, old)

	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Write · running · +1 -1") {
		t.Fatalf("summary line = %q", first)
	}
	// Full-file diff: unchanged context lines + the changed line.
	if !strings.Contains(body, "- │ func a() int { return 1 }") {
		t.Fatalf("missing removed line: %q", body)
	}
	if !strings.Contains(body, "+ │ func a() int { return 2 }") {
		t.Fatalf("missing added line: %q", body)
	}
}

func TestRenderToolDetailLinesHandlesNumberedDiff(t *testing.T) {
	old := "a\nreturn 1\nb\n"
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatRunningToolCallBody("Edit", input, old)
	// Drop the first two lines (summary + target) to isolate diff lines.
	parts := strings.SplitN(body, "\n", 3)
	diffLines := []string{}
	if len(parts) == 3 {
		diffLines = strings.Split(parts[2], "\n")
	}
	rendered := renderToolDetailLines(diffLines, 80)
	// Text content must survive styling (lipgloss escape sequences wrap it).
	if !strings.Contains(rendered, "return 1") || !strings.Contains(rendered, "return 2") {
		t.Fatalf("rendered diff lost text: %q", rendered)
	}
}

func TestFormatFileMutationToolCallBodyNoSummaryOnIdenticalWrite(t *testing.T) {
	content := "package p\n\nfunc a() int { return 1 }\n"
	input := writeInputJSON(t, "a.go", content)
	// oldContent identical to content: zero added, zero removed → no summary token.
	body := formatFileMutationToolCallBody("Write", toolInputFields(input), content)
	first := firstToolEntryLine(body)
	if strings.Contains(first, "+0") || strings.Contains(first, "-0") {
		t.Fatalf("first line = %q, want no +0/-0 summary on identical content", first)
	}
	if first != "Write" {
		t.Fatalf("first line = %q, want bare Write", first)
	}
}
