package loop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/tool"
)

func TestSetContextMaintenanceConfigInitializesArchive(t *testing.T) {
	runner := NewRunnerWithInstructionRoot(nil, nil, tool.NewRegistry(), nil, "session/unsafe", t.TempDir())
	cfg := settings.DefaultContextMaintenanceConfig()
	cfg.TailTokens = 4096
	if err := runner.SetContextMaintenanceConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if runner.compact.currentArchive() == nil || !runner.compact.currentMaintenance().archiveEnabled {
		t.Fatalf("runner not configured: %+v", runner)
	}
	if runner.compact.currentMaintenance().tailTokens != 4096 {
		t.Fatalf("tail tokens = %d, want 4096", runner.compact.currentMaintenance().tailTokens)
	}
	if archive := runner.compact.currentArchive(); archive == nil || strings.Contains(filepath.ToSlash(archive.dir), "session/unsafe") {
		t.Fatalf("archive path is unsafe: %v", archive)
	}
}

func TestColdResumePrunesBeforeFirstModelRequestAndKeepsJournal(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "resume-1"
	original := strings.Repeat("tool-output-line\n", 2000)
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect the repository"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "read-old", Name: "Read"}}),
		buildToolResultMessage("read-old", original, false),
		{Role: message.RoleAssistant, Content: "inspection complete"},
		{Role: message.RoleUser, Content: "preserve behavior"},
	}
	if err := store.Append(context.Background(), sessionID, history...); err != nil {
		t.Fatal(err)
	}

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextLimitTokens(int(float64(estimateMessageTokens(history)) / 0.82))
	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(modelClient.calls))
	}
	prompt := promptTextForTest(modelClient.calls[0])
	if strings.Contains(prompt, original[:512]) {
		t.Fatal("model received unmaintained tool result")
	}
	if !strings.Contains(prompt, prunedToolResultMarker) {
		t.Fatalf("model prompt lacks prune marker: %s", prompt)
	}

	persisted, err := store.LoadResolvedHistory(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !messagesContainToolResultContent(persisted, original[:512]) {
		t.Fatal("journal lost original tool result")
	}
	if messagesContainToolResultContent(persisted, prunedToolResultMarker) {
		t.Fatal("journal persisted maintenance marker")
	}
}

func messagesContainToolResultContent(messages []message.Message, want string) bool {
	for _, msg := range messages {
		for _, result := range toolResultsFromMessage(msg) {
			if strings.Contains(result.Content, want) {
				return true
			}
		}
	}
	return false
}
