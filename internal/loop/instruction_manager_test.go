package loop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstructionManagerLoadsParentAgentsFile(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, projectInstructionFile), []byte("project rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewInstructionManager(child)
	if got := manager.ProjectInstructions(); got != "project rules" {
		t.Fatalf("ProjectInstructions() = %q", got)
	}
}

func TestInstructionManagerMissingAgentsFileReturnsEmpty(t *testing.T) {
	manager := NewInstructionManager(t.TempDir())
	if got := manager.ProjectInstructions(); got != "" {
		t.Fatalf("ProjectInstructions() = %q, want empty", got)
	}
}

func TestInstructionManagerCachesRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, projectInstructionFile)
	if err := os.WriteFile(path, []byte("cached rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewInstructionManager(root)
	reads := 0
	manager.readFile = func(name string) ([]byte, error) {
		reads++
		return os.ReadFile(name)
	}

	_ = manager.ProjectInstructions()
	_ = manager.ProjectInstructions()
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
}
