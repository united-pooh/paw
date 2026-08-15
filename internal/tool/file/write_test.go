package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileMutationTargetRelativeAndAbsoluteInsideRoot(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "nested", "a.txt")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &WriteTool{Root: root}

	for _, path := range []string{"nested/a.txt", full} {
		t.Run(path, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"file_path": path})
			if err != nil {
				t.Fatal(err)
			}
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

func TestWriteFileMutationTargetDistinguishesMissingFile(t *testing.T) {
	root := t.TempDir()
	got, err := (&WriteTool{Root: root}).FileMutationTarget([]byte(`{"file_path":"new.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != filepath.Join(root, "new.txt") || got.BeforeExists {
		t.Fatalf("target = %+v, want workspace path and missing before", got)
	}
}

func TestWriteFileMutationTargetRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	raw, err := json.Marshal(map[string]string{"file_path": outside})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&WriteTool{Root: root}).FileMutationTarget(raw)
	if err == nil || !strings.Contains(err.Error(), "path escapes allowed roots") {
		t.Fatalf("err = %v, want path escape rejection", err)
	}
}

func TestWriteFileMutationTargetRejectsInvalidInput(t *testing.T) {
	tool := &WriteTool{Root: t.TempDir()}
	for _, raw := range [][]byte{[]byte(`{`), []byte(`{}`), []byte(`{"file_path":"  "}`)} {
		if _, err := tool.FileMutationTarget(raw); err == nil {
			t.Fatalf("input %q unexpectedly accepted", raw)
		}
	}
}

func TestWriteRejectsSymlinkEscapeInCapabilityAndRun(t *testing.T) {
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
		{name: "intermediate symlink for missing target", linkPath: "escape-dir", input: "escape-dir/new.txt", want: "through symlink"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			secret := filepath.Join(outside, "secret.txt")
			if err := os.WriteFile(secret, []byte("secret\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			linkTarget := secret
			if tc.linkPath == "escape-dir" {
				linkTarget = outside
			}
			if err := os.Symlink(linkTarget, filepath.Join(root, tc.linkPath)); err != nil {
				t.Fatal(err)
			}

			tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
			raw, err := json.Marshal(map[string]string{"file_path": tc.input, "content": "owned\n"})
			if err != nil {
				t.Fatal(err)
			}
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
			if string(got) != "secret\n" {
				t.Fatalf("outside file changed: %q", got)
			}
			if _, err := os.Stat(filepath.Join(outside, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("outside new file exists or stat failed: %v", err)
			}
		})
	}
}

func TestWriteRejectsFinalWorkspaceInternalSymlinkInCapabilityAndRun(t *testing.T) {
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
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	raw := []byte(`{"file_path":"link.txt","content":"new\n"}`)
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

func TestWriteAllowsSymlinkThatResolvesInsideWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges on Windows")
	}

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	raw := []byte(`{"file_path":"link/new.txt","content":"inside\n"}`)
	if _, err := tool.FileMutationTarget(raw); err != nil {
		t.Fatalf("FileMutationTarget rejected workspace-internal symlink: %v", err)
	}
	if _, err := tool.Run(context.Background(), raw); err != nil {
		t.Fatalf("Run rejected workspace-internal symlink: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(realDir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "inside\n" {
		t.Fatalf("file = %q, want inside", got)
	}
}

func TestWriteToolCreatesNewFile(t *testing.T) {
	root := t.TempDir()
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	out, err := tool.Run(context.Background(), []byte(`{"file_path":"new.txt","content":"hi\n"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	if string(got) != "hi\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolNewFileUsesDefaultMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission semantics")
	}

	root := t.TempDir()
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	if _, err := tool.Run(context.Background(), []byte(`{"file_path":"new.sh","content":"echo hi\n"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "new.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
}

func TestWriteToolOverwriteKeepsExistingContractOfMode0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission semantics")
	}

	root := t.TempDir()
	full := filepath.Join(root, "script.sh")
	if err := os.WriteFile(full, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	readState := NewReadStateStore()
	if _, err := (&ReadTool{Root: root, ReadState: readState}).Run(context.Background(), []byte(`{"file_path":"script.sh"}`)); err != nil {
		t.Fatal(err)
	}
	tool := &WriteTool{Root: root, ReadState: readState}
	if _, err := tool.Run(context.Background(), []byte(`{"file_path":"script.sh","content":"new\n"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want existing Write behavior 0644", got)
	}
}

func TestWriteToolRejectsOverwriteWithoutPriorRead(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	if err := os.WriteFile(full, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	_, err := tool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"new\n"}`))
	if err == nil || !strings.Contains(err.Error(), "file must be read before writing: a.txt; use Read first") {
		t.Fatalf("err = %v, want read-before-write rejection", err)
	}
	got, _ := os.ReadFile(full)
	if string(got) != "old\n" {
		t.Fatalf("file = %q, want unchanged old content", string(got))
	}
}

func TestWriteToolOverwritesAfterUnchangedRead(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	if _, err := readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`)); err != nil {
		t.Fatal(err)
	}

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"new\n"}`))
	if err != nil {
		t.Fatalf("overwrite after unchanged read: %v", err)
	}
	got, _ := os.ReadFile(full)
	if string(got) != "new\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolRejectsStaleWriteAfterExternalModification(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`))

	// User/IDE changes the file after the model's Read.
	_ = os.WriteFile(full, []byte("user-changed\n"), 0o644)

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"model-rewrite\n"}`))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-write error", err)
	}
	// File must be untouched on a stale-write rejection.
	got, _ := os.ReadFile(full)
	if string(got) != "user-changed\n" {
		t.Fatalf("file = %q, want user content preserved", string(got))
	}
}
