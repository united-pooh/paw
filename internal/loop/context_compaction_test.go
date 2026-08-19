package loop

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

func TestPlanHistoryCompactionKeepsRecentToolPairTogether(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "initial task"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old analysis ", 80)},
		{Role: message.RoleUser, Content: "old follow-up"},
		{Role: message.RoleAssistant, Content: strings.Repeat("more old work ", 80)},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "read-1", Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)}}),
		buildToolResultMessage("read-1", strings.Repeat("recent result ", 40), false),
		{Role: message.RoleAssistant, Content: "recent conclusion"},
		{Role: message.RoleUser, Content: "continue"},
	}

	head, tail := planHistoryCompaction(history, 240)
	if head != 1 {
		t.Fatalf("head = %d, want first user turn pinned", head)
	}
	if tail >= len(history) {
		t.Fatalf("tail = %d, want an older region selected", tail)
	}
	if len(toolResultsFromMessage(history[tail])) > 0 {
		t.Fatalf("tail starts with orphaned tool result at %d", tail)
	}
}

func TestPartitionCompactionRegionKeepsSmallUserFactsAndPriorSummary(t *testing.T) {
	prior := message.Message{Role: message.RoleUser, Content: compactionSummaryOpen + "\nprior facts\n" + compactionSummaryClose}
	userFact := message.Message{Role: message.RoleUser, Content: "Never change the public API."}
	toolResult := buildToolResultMessage("call-1", strings.Repeat("large output ", 100), false)
	assistant := message.Message{Role: message.RoleAssistant, Content: "implementation notes"}

	kept, fold := partitionCompactionRegion([]message.Message{prior, userFact, toolResult, assistant}, 1000)
	if len(kept) != 2 || kept[0].Content != prior.Content || kept[1].Content != userFact.Content {
		t.Fatalf("kept = %#v, want prior summary and user fact", kept)
	}
	if len(fold) != 2 || len(toolResultsFromMessage(fold[0])) == 0 || fold[1].Role != message.RoleAssistant {
		t.Fatalf("fold = %#v, want tool result and assistant work", fold)
	}
}

func TestRunTurnAutomaticallyCompactsOlderHistory(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Delta: "## Goal\nContinue the migration.\n## Files & code\n- internal/old.go was inspected."},
			{Usage: &model.Usage{PromptTokens: 300, CompletionTokens: 30, TotalTokens: 330}},
			{Done: true},
		}},
		{events: []model.StreamEvent{
			{Delta: "completed after compaction"},
			{Done: true},
		}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")
	runner.SetContextLimitTokens(1000)
	runner.setHistory([]message.Message{
		{Role: message.RoleUser, Content: "Migrate the parser and preserve compatibility."},
		{Role: message.RoleAssistant, Content: strings.Repeat("old investigation details ", 80)},
		{Role: message.RoleUser, Content: "Keep exported names unchanged."},
		{Role: message.RoleAssistant, Content: strings.Repeat("older implementation work ", 80)},
		{Role: message.RoleUser, Content: "Use the existing tests."},
		{Role: message.RoleAssistant, Content: strings.Repeat("test and command output ", 80)},
	})

	msg, err := runner.RunTurn(context.Background(), "finish the migration")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "completed after compaction" {
		t.Fatalf("msg.Content = %q", msg.Content)
	}
	if len(modelClient.calls) != 2 {
		t.Fatalf("model calls = %d, want summarizer + normal turn", len(modelClient.calls))
	}
	if len(modelClient.tools[0]) != 0 {
		t.Fatalf("summarizer received tools: %#v", modelClient.tools[0])
	}
	if !strings.Contains(modelClient.calls[0][0].Content, "Standing facts & constraints") {
		t.Fatalf("first call is not the compaction prompt: %#v", modelClient.calls[0])
	}
	prompt := promptTextForTest(modelClient.calls[1])
	if !strings.Contains(prompt, compactionSummaryOpen) || !strings.Contains(prompt, "Continue the migration") {
		t.Fatalf("normal turn did not receive compaction summary:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Migrate the parser and preserve compatibility.") || !strings.Contains(prompt, "finish the migration") {
		t.Fatalf("pinned/current user requests were lost:\n%s", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("old investigation details ", 20)) {
		t.Fatalf("old assistant history remained verbatim after compaction")
	}
	foundNotice := false
	for _, event := range ui.systemEvents() {
		if event.Title == "context-compaction" {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("missing context-compaction system notice: %#v", ui.system)
	}
}

func TestCompactionSkipsLegacyAppendOnlyStore(t *testing.T) {
	runner := NewEngine(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "legacy")
	runner.SetContextLimitTokens(100)
	runner.setHistory([]message.Message{
		{Role: message.RoleUser, Content: "task"},
		{Role: message.RoleAssistant, Content: strings.Repeat("large history ", 100)},
		{Role: message.RoleUser, Content: "continue"},
	})
	history := runner.currentHistory()
	got, compacted, err := runner.maybeCompactHistory(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	if compacted != nil || len(got) != len(history) {
		t.Fatalf("legacy store history changed: compacted=%v got=%#v", compacted, got)
	}
}

func TestManualCompactionUsesMechanicalFoldAfterSummaryFailure(t *testing.T) {
	root := t.TempDir()
	modelClient := &fakeModel{rounds: []fakeRound{{err: errors.New("provider down")}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "manual-fallback", root)
	history := []message.Message{
		{Role: message.RoleUser, Content: "migrate parser"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old investigation ", 120)},
		{Role: message.RoleUser, Content: "preserve API"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old implementation ", 120)},
		{Role: message.RoleAssistant, Content: "recent"},
		{Role: message.RoleUser, Content: "continue"},
	}
	runner.setHistory(history)

	result, err := runner.CompactContext(context.Background(), "keep build state")
	if err != nil {
		t.Fatalf("CompactContext() error = %v", err)
	}
	if !result.Mechanical || result.FoldedMessages == 0 || len(result.ArchivePaths) == 0 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Summary, "summary was unavailable") {
		t.Fatalf("summary = %q", result.Summary)
	}
}

func TestManualCompactionArchiveFailureLeavesHistoryUnchanged(t *testing.T) {
	runner := NewEngineWithInstructionRoot(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "archive-failure", t.TempDir())
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old work ", 200)},
		{Role: message.RoleAssistant, Content: "recent"},
		{Role: message.RoleUser, Content: "continue"},
	}
	runner.setHistory(history)
	runner.compact.currentArchive().syncFile = func(*os.File) error { return errors.New("disk full") }

	if _, err := runner.CompactContext(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("CompactContext() error = %v", err)
	}
	if got := runner.currentHistory(); len(got) != len(history) || got[1].Content != history[1].Content {
		t.Fatalf("history changed after archive failure: %#v", got)
	}
}

func TestRenderCompactionTranscriptUsesSnippedProjection(t *testing.T) {
	snipped := snipToolResult("Read", strings.Repeat("line\n", 200), "archive.jsonl", snipStrategy{head: 2, tail: 1, headChars: 8, tailChars: 4})
	transcript := renderCompactionTranscript([]message.Message{
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "call", Name: "Read", Input: []byte(`{"file_path":"secret","offset":1}`)}}),
		buildToolResultMessage("call", snipped, false),
	})
	if !strings.Contains(transcript, snippedToolResultMarker) || !strings.Contains(transcript, "{file_path, offset}") {
		t.Fatalf("transcript = %q", transcript)
	}
	if strings.Contains(transcript, strings.Repeat("line\n", 20)) {
		t.Fatal("transcript expanded archived content")
	}
}

func TestCompactContextSynchronizesCurrentContextUsage(t *testing.T) {
	original := []message.Message{
		{Role: message.RoleUser, Content: "migrate parser"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old investigation ", 100)},
		{Role: message.RoleUser, Content: "preserve API names"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old implementation ", 100)},
		{Role: message.RoleAssistant, Content: "recent answer"},
		{Role: message.RoleUser, Content: "latest request"},
	}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "## Goal\nMigrate parser while preserving API names."},
		{Done: true},
	}}}}
	runner := NewEngine(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "manual-usage")
	instructions := NewInstructionManager(t.TempDir())
	instructions.homeDir = t.TempDir()
	runner.prompt = NewPromptBuilder(instructions)
	runner.setHistory(original)
	runner.usage.setCurrent(model.Usage{TotalTokens: 9000, PromptCacheHitTokens: 4000})

	if _, err := runner.CompactContext(context.Background(), ""); err != nil {
		t.Fatalf("CompactContext() error = %v", err)
	}

	compacted := runner.currentHistory()
	want := estimateMessageTokens(append([]message.Message{
		buildSystemMessage(runner.buildSystemPrompt()),
	}, compacted...))
	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != want || stats.CacheTokens != 0 {
		t.Fatalf("ContextStats() = %#v, want compacted usage %d/cache 0", stats, want)
	}
	if stats.UsedTokens >= 9000 {
		t.Fatalf("UsedTokens = %d, want lower than pre-compaction usage", stats.UsedTokens)
	}
}

func TestCompactContextUsesFocusAndOnlyRewritesProjection(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := []message.Message{
		{Role: message.RoleUser, Content: "migrate parser"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old investigation ", 100)},
		{Role: message.RoleUser, Content: "preserve API names"},
		{Role: message.RoleAssistant, Content: strings.Repeat("old implementation ", 100)},
		{Role: message.RoleUser, Content: "run tests"},
		{Role: message.RoleAssistant, Content: "recent answer"},
		{Role: message.RoleUser, Content: "latest request"},
	}
	if err := store.Append(context.Background(), "manual-compact", original...); err != nil {
		t.Fatal(err)
	}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "## Goal\nMigrate parser while preserving API names."},
		{Done: true},
	}}}}
	runner := NewEngine(modelClient, &fakeUI{}, tool.NewRegistry(), store, "manual-compact")
	runner.setHistory(original)
	result, err := runner.CompactContext(context.Background(), "prioritize failed parser tests")
	if err != nil {
		t.Fatal(err)
	}
	if result.BeforeMessages != len(original) || result.AfterMessages >= result.BeforeMessages || result.FoldedMessages == 0 {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(modelClient.calls[0][0].Content, "prioritize failed parser tests") {
		t.Fatalf("focus missing from summarizer prompt: %#v", modelClient.calls[0])
	}
	if !messagesContainContent(runner.currentHistory(), compactionSummaryOpen) {
		t.Fatalf("runner projection lacks summary: %#v", runner.currentHistory())
	}
	durable, err := store.LoadResolvedHistory(context.Background(), "manual-compact")
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != len(original) {
		t.Fatalf("durable journal changed: got %d want %d", len(durable), len(original))
	}
	for i := range original {
		if durable[i].Content != original[i].Content {
			t.Fatalf("journal[%d] = %#v, want %#v", i, durable[i], original[i])
		}
	}
}
