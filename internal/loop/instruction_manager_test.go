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
	if err := os.WriteFile(filepath.Join(root, projectInstructionFileName), []byte("project rules\n"), 0o644); err != nil {
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
	path := filepath.Join(root, projectInstructionFileName)
	if err := os.WriteFile(path, []byte("cached rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewInstructionManager(root)
	manager.homeDir = t.TempDir()
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

func TestInstructionManagerMatchesProjectFileCaseInsensitively(t *testing.T) {
	for _, name := range []string{"Agent.md", "AGENT.MD", "aGeNt.mD"} {
		root := t.TempDir()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("rules from "+name), 0o644); err != nil {
			t.Fatal(err)
		}

		manager := NewInstructionManager(root)
		if got := manager.ProjectInstructions(); got != "rules from "+name {
			t.Fatalf("ProjectInstructions() with %s = %q", name, got)
		}
	}
}

func TestInstructionManagerMatchesGlobalFileCaseInsensitively(t *testing.T) {
	for _, name := range []string{"agent.md", "Agent.md", "AGENT.MD"} {
		home := t.TempDir()
		pawDir := filepath.Join(home, globalInstructionDir)
		if err := os.MkdirAll(pawDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pawDir, name), []byte("global rules from "+name), 0o644); err != nil {
			t.Fatal(err)
		}

		manager := NewInstructionManager(t.TempDir())
		manager.homeDir = home
		if got := manager.GlobalInstructions(); got != "global rules from "+name {
			t.Fatalf("GlobalInstructions() with %s = %q", name, got)
		}
	}
}

func TestInstructionManagerMissingGlobalFileReturnsEmpty(t *testing.T) {
	manager := NewInstructionManager(t.TempDir())
	manager.homeDir = t.TempDir()
	if got := manager.GlobalInstructions(); got != "" {
		t.Fatalf("GlobalInstructions() = %q, want empty", got)
	}
}

func TestInstructionManagerCombinesGlobalAndProject(t *testing.T) {
	home := t.TempDir()
	pawDir := filepath.Join(home, globalInstructionDir)
	if err := os.MkdirAll(pawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pawDir, "AGENT.md"), []byte("global rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Agent.md"), []byte("project rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewInstructionManager(root)
	manager.homeDir = home
	if got := manager.GlobalInstructions(); got != "global rules" {
		t.Fatalf("GlobalInstructions() = %q, want %q", got, "global rules")
	}
	if got := manager.ProjectInstructions(); got != "project rules" {
		t.Fatalf("ProjectInstructions() = %q, want %q", got, "project rules")
	}
}
