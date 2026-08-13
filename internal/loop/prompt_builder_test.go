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

func TestPromptBuilderWithSelectAddsStructuredQuestionPolicy(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{
		"Read: read files",
		"Select: Ask the user a structured single- or multiple-choice question.",
	})
	for _, want := range []string{
		"User interaction policy:",
		"two or more concrete options",
		"call the Select tool",
		"Do not ask a structured multiple-choice question in plain text",
		"genuinely open-ended questions",
		"repository, existing context, or a reasonable default",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

func TestPromptBuilderWithoutSelectOmitsStructuredQuestionPolicy(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{"Read: read files"})
	if strings.Contains(prompt, "User interaction policy:") || strings.Contains(prompt, "call the Select tool") {
		t.Fatalf("prompt contains Select policy without Select tool: %q", prompt)
	}
}

func TestPromptBuilderOrdersDefaultProjectAndTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectInstructionFileName), []byte("project-only rule"), 0o644); err != nil {
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

func TestPromptBuilderOrdersGlobalBeforeProject(t *testing.T) {
	home := t.TempDir()
	pawDir := filepath.Join(home, globalInstructionDir)
	if err := os.MkdirAll(pawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pawDir, "AGENT.md"), []byte("global-only rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Agent.md"), []byte("project-only rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewInstructionManager(root)
	manager.homeDir = home
	prompt := NewPromptBuilder(manager).Build([]string{"LS: list files"})
	defaultIndex := strings.Index(prompt, defaultSystemPrompt)
	globalIndex := strings.Index(prompt, "global-only rule")
	projectIndex := strings.Index(prompt, "project-only rule")
	toolIndex := strings.Index(prompt, "Available tools:")
	if defaultIndex == -1 || globalIndex == -1 || projectIndex == -1 || toolIndex == -1 {
		t.Fatalf("prompt missing sections: %q", prompt)
	}
	if !(defaultIndex < globalIndex && globalIndex < projectIndex && projectIndex < toolIndex) {
		t.Fatalf("section order default/global/project/tools = %d/%d/%d/%d in %q", defaultIndex, globalIndex, projectIndex, toolIndex, prompt)
	}
}

func TestPromptBuilderWithUpdateTodoAddsProgressPolicy(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{
		"Read: read files",
		"update_todo: Maintain a full todo snapshot for complex multi-step work.",
	})
	for _, want := range []string{
		"Progress tracking policy:",
		"call update_todo before substantial execution",
		"update_todo is the in-session tracking mechanism",
		"cross-session archive",
		"Do not call update_todo for simple questions or one-step edits",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

func TestPromptBuilderWithoutUpdateTodoOmitsProgressPolicy(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{"Read: read files"})
	if strings.Contains(prompt, "Progress tracking policy:") || strings.Contains(prompt, "call update_todo") {
		t.Fatalf("prompt contains progress policy without update_todo tool: %q", prompt)
	}
}
