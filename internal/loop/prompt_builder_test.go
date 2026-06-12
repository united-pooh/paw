package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptBuilderNoToolsPlainTextFallback(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build(nil)
	for _, want := range []string{defaultSystemPrompt, "Answer with plain text."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "tool_use") {
		t.Fatalf("prompt = %q, should not contain tool instructions", prompt)
	}
}

func TestPromptBuilderWithToolsKeepsToolUseContract(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{"Read: read files"})
	for _, want := range []string{"You can use tools.", "Read: read files", `"type":"tool_use"`, "TOOL_RESULT"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

func TestPromptBuilderOrdersDefaultProjectAndTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectInstructionFile), []byte("project-only rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := NewPromptBuilder(NewInstructionManager(root)).Build([]string{"LS: list files"})
	defaultIndex := strings.Index(prompt, defaultSystemPrompt)
	projectIndex := strings.Index(prompt, "project-only rule")
	toolIndex := strings.Index(prompt, "Available tools:")
	if defaultIndex == -1 || projectIndex == -1 || toolIndex == -1 {
		t.Fatalf("prompt missing sections: %q", prompt)
	}
	if !(defaultIndex < projectIndex && projectIndex < toolIndex) {
		t.Fatalf("section order default/project/tools = %d/%d/%d in %q", defaultIndex, projectIndex, toolIndex, prompt)
	}
}
