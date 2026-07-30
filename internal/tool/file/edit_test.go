package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newEditTool(t *testing.T) (*EditTool, string) {
	t.Helper()
	root := t.TempDir()
	return &EditTool{Root: root}, root
}

func writeExistingFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func runEdit(t *testing.T, tool *EditTool, input string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), []byte(input))
}

func TestEditReplacesUniqueMatch(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "package a\n\nfunc run() int { return 1 }\n")

	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q, want replacement count", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(got), "return 2") || strings.Contains(string(got), "return 1") {
		t.Fatalf("file not updated: %q", string(got))
	}
}

func TestEditReplaceAllReplacesEveryMatch(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\nx\nx\n")

	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"y","replace_all":true}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "3 replacements") {
		t.Fatalf("output = %q, want 3 replacements", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != "y\ny\ny\n" {
		t.Fatalf("file = %q, want y\\ny\\ny\\n", string(got))
	}
}

func TestEditRejectsAmbiguousMatchWithoutReplaceAll(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\nx\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "matches 2 locations") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != "x\nx\n" {
		t.Fatalf("file must be unchanged on error, got %q", string(got))
	}
}

func TestEditRejectsMissingOldString(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "nothing here\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "old_string not found") {
		t.Fatalf("err = %v, want not-found error", err)
	}
}

func TestEditRejectsIdenticalOldAndNew(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("err = %v, want must-differ error", err)
	}
}

func TestEditRejectsMissingFile(t *testing.T) {
	tool, _ := newEditTool(t)
	_, err := runEdit(t, tool, `{"file_path":"missing.go","old_string":"x","new_string":"y"}`)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEditRejectsPathOutsideRoot(t *testing.T) {
	tool, _ := newEditTool(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runEdit(t, tool, `{"file_path":"`+outside+`","old_string":"x","new_string":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "escapes allowed roots") {
		t.Fatalf("err = %v, want escape error", err)
	}
}

func TestEditSupportsMultilineOldString(t *testing.T) {
	tool, root := newEditTool(t)
	original := "func a() {\n\treturn 1\n}\nfunc b() {\n\treturn 1\n}\n"
	writeExistingFile(t, root, "a.go", original)
	multiline := "func b() {\n\treturn 1\n}"
	replacement := "func b() {\n\treturn 2\n}"
	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":`+jsonString(multiline)+`,"new_string":`+jsonString(replacement)+`}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(got), "func b() {\n\treturn 2\n}") || strings.Contains(string(got), "func a() {\n\treturn 2") {
		t.Fatalf("multiline edit wrong: %q", string(got))
	}
}

// jsonString encodes s as a JSON string literal so multi-line old_string/
// new_string values can be embedded directly into the JSON input passed to Run.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestEditRejectsStaleWriteAfterExternalModification(t *testing.T) {
	root := t.TempDir()
	full := writeExistingFile(t, root, "a.go", "func a() int { return 1 }\n")

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.go"}`))

	// External modification after the model's Read.
	_ = os.WriteFile(full, []byte("func a() int { return 9 }\n"), 0o644)

	tool := &EditTool{Root: root, ReadState: readState}
	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-write error", err)
	}
}

func TestEditSucceedsAfterUnchangedRead(t *testing.T) {
	root := t.TempDir()
	writeExistingFile(t, root, "a.go", "func a() int { return 1 }\n")

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.go"}`))

	tool := &EditTool{Root: root, ReadState: readState}
	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q", out)
	}
}
