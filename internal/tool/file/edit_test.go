package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newEditTool(t *testing.T) (*EditTool, string) {
	t.Helper()
	root := t.TempDir()
	return &EditTool{Root: root, ReadState: NewReadStateStore()}, root
}

func recordEditBaseline(t *testing.T, tool *EditTool, fullPath string) {
	t.Helper()
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	tool.ReadState.Record(fullPath, data)
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
	full := writeExistingFile(t, root, "a.go", "package a\n\nfunc run() int { return 1 }\n")
	recordEditBaseline(t, tool, full)

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
	full := writeExistingFile(t, root, "a.go", "x\nx\nx\n")
	recordEditBaseline(t, tool, full)

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
	full := writeExistingFile(t, root, "a.go", "x\nx\n")
	recordEditBaseline(t, tool, full)

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
	full := writeExistingFile(t, root, "a.go", "nothing here\n")
	recordEditBaseline(t, tool, full)

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

func TestEditRejectsDirectoryTarget(t *testing.T) {
	tool, root := newEditTool(t)
	if err := os.Mkdir(filepath.Join(root, "target-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := runEdit(t, tool, `{"file_path":"target-dir","old_string":"x","new_string":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "edit target is not a regular file: target-dir") {
		t.Fatalf("err = %v, want workspace-relative non-regular-file error", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("error exposes absolute workspace path: %v", err)
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

func TestEditRejectsSymlinkEscapeInCapabilityAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}

	tests := []struct {
		name     string
		linkPath string
		input    string
		want     string
	}{
		{name: "final symlink", linkPath: "escape.txt", input: "escape.txt", want: "symbolic link target is not supported: escape.txt"},
		{name: "intermediate symlink", linkPath: "escape-dir", input: "escape-dir/secret.txt", want: "through symlink"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			secret := filepath.Join(outside, "secret.txt")
			if err := os.WriteFile(secret, []byte("secret old\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			linkTarget := secret
			if tc.linkPath == "escape-dir" {
				linkTarget = outside
			}
			if err := os.Symlink(linkTarget, filepath.Join(root, tc.linkPath)); err != nil {
				t.Fatal(err)
			}

			tool := &EditTool{Root: root, ReadState: NewReadStateStore()}
			raw := []byte(`{"file_path":` + jsonString(tc.input) + `,"old_string":"old","new_string":"new"}`)
			if _, err := tool.FileMutationTarget(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("FileMutationTarget err = %v, want %q", err, tc.want)
			}
			if _, err := tool.Run(context.Background(), raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run err = %v, want %q", err, tc.want)
			}
			got, err := os.ReadFile(secret)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "secret old\n" {
				t.Fatalf("outside file changed: %q", got)
			}
		})
	}
}

func TestEditRejectsFinalWorkspaceInternalSymlinkInCapabilityAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}

	root := t.TempDir()
	referent := filepath.Join(root, "real.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(referent, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}
	tool := &EditTool{Root: root, ReadState: NewReadStateStore()}
	raw := []byte(`{"file_path":"link.txt","old_string":"old","new_string":"new"}`)
	const want = "symbolic link target is not supported: link.txt"
	if _, err := tool.FileMutationTarget(raw); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("FileMutationTarget err = %v, want %q", err, want)
	}
	if _, err := tool.Run(context.Background(), raw); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Run err = %v, want %q", err, want)
	}
	if target, err := os.Readlink(link); err != nil || target != "real.txt" {
		t.Fatalf("link target = %q, err = %v; want unchanged real.txt", target, err)
	}
	if got, err := os.ReadFile(referent); err != nil || string(got) != "old\n" {
		t.Fatalf("referent = %q, err = %v; want unchanged", got, err)
	}
}

func TestEditSupportsMultilineOldString(t *testing.T) {
	tool, root := newEditTool(t)
	original := "func a() {\n\treturn 1\n}\nfunc b() {\n\treturn 1\n}\n"
	full := writeExistingFile(t, root, "a.go", original)
	recordEditBaseline(t, tool, full)
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

func TestEditRejectsFileThatWasNotRead(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "file must be read before editing: a.go; use Read first") {
		t.Fatalf("err = %v", err)
	}
	got, readErr := os.ReadFile(full)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "return 1\n" {
		t.Fatalf("file changed on rejection: %q", got)
	}
}

func TestEditRejectsNilReadState(t *testing.T) {
	root := t.TempDir()
	writeExistingFile(t, root, "a.go", "return 1\n")
	tool := &EditTool{Root: root}

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "file must be read before editing: a.go; use Read first") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditSuccessUpdatesBaselineForConsecutiveEdit(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")
	recordEditBaseline(t, tool, full)

	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 2","new_string":"return 3"}`); err != nil {
		t.Fatalf("second Edit without another Read: %v", err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "return 3\n" {
		t.Fatalf("file = %q, want return 3", got)
	}
}

func TestEditSucceedsAfterExternalChangeIsReadAgain(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")
	recordEditBaseline(t, tool, full)
	if err := os.WriteFile(full, []byte("return 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 9","new_string":"return 10"}`); err == nil {
		t.Fatal("stale Edit unexpectedly succeeded")
	}
	recordEditBaseline(t, tool, full)
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 9","new_string":"return 10"}`); err != nil {
		t.Fatalf("Edit after re-Read: %v", err)
	}
}

func TestEditDoesNotNormalizeWhitespaceOrLineEndings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		old     string
	}{
		{name: "spaces", content: "  return 1\n", old: "   return 1"},
		{name: "tab", content: "\treturn 1\n", old: "    return 1"},
		{name: "crlf", content: "return 1\r\n", old: "return 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, root := newEditTool(t)
			full := writeExistingFile(t, root, "a.go", tc.content)
			recordEditBaseline(t, tool, full)
			input := `{"file_path":"a.go","old_string":` + jsonString(tc.old) + `,"new_string":"return 2"}`
			_, err := runEdit(t, tool, input)
			if err == nil || !strings.Contains(err.Error(), "must match the file contents exactly") {
				t.Fatalf("err = %v", err)
			}
			got, readErr := os.ReadFile(full)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != tc.content {
				t.Fatalf("file changed on exact-match rejection: %q", got)
			}
		})
	}
}

func TestEditPreservesExistingFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission semantics")
	}

	for _, mode := range []os.FileMode{0o755, 0o600} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			tool, root := newEditTool(t)
			full := writeExistingFile(t, root, "script.sh", "echo old\n")
			if err := os.Chmod(full, mode); err != nil {
				t.Fatal(err)
			}
			recordEditBaseline(t, tool, full)
			if _, err := runEdit(t, tool, `{"file_path":"script.sh","old_string":"old","new_string":"new"}`); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(full)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != mode {
				t.Fatalf("mode = %04o, want %04o", got, mode)
			}
		})
	}
}

func TestEditDescriptionRequiresReadAndExactMatch(t *testing.T) {
	description := (&EditTool{}).Description()
	for _, want := range []string{"Read", "逐字节匹配", "replace_all=true"} {
		if !strings.Contains(description, want) {
			t.Fatalf("Description() = %q, missing %q", description, want)
		}
	}
}

func TestEditFileMutationTargetRelativeAndAbsoluteInsideRoot(t *testing.T) {
	root := t.TempDir()
	full := writeExistingFile(t, root, "nested/a.go", "x\n")
	tool := &EditTool{Root: root}

	for _, path := range []string{"nested/a.go", full} {
		t.Run(path, func(t *testing.T) {
			raw := []byte(`{"file_path":` + jsonString(path) + `}`)
			got, err := tool.FileMutationTarget(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != full || !got.BeforeExists {
				t.Fatalf("target = %+v, want path=%q exists=true", got, full)
			}
		})
	}
}

func TestEditFileMutationTargetRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	raw := []byte(`{"file_path":` + jsonString(outside) + `}`)
	_, err := (&EditTool{Root: root}).FileMutationTarget(raw)
	if err == nil || !strings.Contains(err.Error(), "path escapes allowed roots") {
		t.Fatalf("err = %v, want path escape rejection", err)
	}
}

func TestEditFileMutationTargetRequiresExistingFile(t *testing.T) {
	root := t.TempDir()
	_, err := (&EditTool{Root: root}).FileMutationTarget([]byte(`{"file_path":"missing.go"}`))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("err = %v, want not-exist error", err)
	}
}

func TestEditFileMutationTargetRejectsInvalidInput(t *testing.T) {
	tool := &EditTool{Root: t.TempDir()}
	for _, raw := range [][]byte{[]byte(`{`), []byte(`{}`), []byte(`{"file_path":"  "}`)} {
		if _, err := tool.FileMutationTarget(raw); err == nil {
			t.Fatalf("input %q unexpectedly accepted", raw)
		}
	}
}

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
