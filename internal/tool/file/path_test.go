package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolAllowsConfiguredSkillRoot(t *testing.T) {
	workspace := t.TempDir()
	skillRoot := t.TempDir()
	skillFile := filepath.Join(skillRoot, "multi-agent-pipeline", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# Pipeline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadTool{Root: workspace, ReadRoots: []string{skillRoot}}
	output, err := tool.Run(context.Background(), []byte(`{"file_path":"`+skillFile+`"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, "# Pipeline") {
		t.Fatalf("output = %q, want skill content", output)
	}
}

func TestReadToolRejectsAbsolutePathOutsideAllowedRoots(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadTool{Root: workspace}
	_, err := tool.Run(context.Background(), []byte(`{"file_path":"`+outside+`"}`))
	if err == nil || !strings.Contains(err.Error(), "escapes allowed roots") {
		t.Fatalf("Run() error = %v, want allowed roots escape error", err)
	}
}

func TestGlobToolAllowsConfiguredSkillRoot(t *testing.T) {
	workspace := t.TempDir()
	skillRoot := t.TempDir()
	skillFile := filepath.Join(skillRoot, "multi-agent-pipeline", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# Pipeline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &GlobTool{Root: workspace, ReadRoots: []string{skillRoot}}
	output, err := tool.Run(context.Background(), []byte(`{"path":"`+skillRoot+`","pattern":"**/*.md"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, filepath.ToSlash(skillFile)) {
		t.Fatalf("output = %q, want %q", output, skillFile)
	}
}

func TestGrepToolAllowsConfiguredSkillRoot(t *testing.T) {
	workspace := t.TempDir()
	skillRoot := t.TempDir()
	skillFile := filepath.Join(skillRoot, "multi-agent-pipeline", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# Pipeline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &GrepTool{Root: workspace, ReadRoots: []string{skillRoot}}
	output, err := tool.Run(context.Background(), []byte(`{"path":"`+skillRoot+`","pattern":"Pipeline","literal":true}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, filepath.ToSlash(skillFile)+":1:# Pipeline") {
		t.Fatalf("output = %q, want match in %q", output, skillFile)
	}
}

func TestDisplayPathUsesWorkspaceRelativeSlashPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "internal", "ui", "bubble", "transcript.go")

	if got := DisplayPath(root, target); got != "internal/ui/bubble/transcript.go" {
		t.Fatalf("DisplayPath() = %q, want workspace-relative slash path", got)
	}
}

func TestDisplayPathResolvesRelativeTargetAgainstWorkspace(t *testing.T) {
	root := t.TempDir()

	if got := DisplayPath(root, "internal/ui"); got != "internal/ui" {
		t.Fatalf("DisplayPath() = %q, want internal/ui", got)
	}
}

func TestDisplayPathKeepsOutsideTargetAbsolute(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.txt")

	if got := DisplayPath(root, target); got != filepath.ToSlash(target) {
		t.Fatalf("DisplayPath() = %q, want outside absolute path %q", got, filepath.ToSlash(target))
	}
}
