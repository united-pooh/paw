package loop

import (
	"strings"
	"testing"

	"paw/internal/message"
)

func TestKeepIndexesPreserveErrorToolCallGroup(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "a", Name: "Read"}, {ID: "b", Name: "Bash"},
		}},
		{Role: message.RoleUser, ToolResults: []message.ToolResult{
			{ToolUseID: "a", Content: "ok"},
			{ToolUseID: "b", Content: "error: build failed", IsError: true},
		}},
	}
	keep := keepMessageIndexes(history, keepPolicy{errors: true, userMarked: true})
	if !keep[0] || !keep[1] {
		t.Fatalf("tool group not preserved: %v", keep)
	}
}

func TestKeepIndexesOnlyApplyAfterLatestSummary(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: "old", Name: "Bash"}},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "old", Content: "error: old", IsError: true}},
		{Role: message.RoleUser, Content: compactionSummaryOpen + "\nsummary\n" + compactionSummaryClose},
		{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: "new", Name: "Bash"}},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "new", Content: "blocked: new"}},
	}
	keep := keepMessageIndexes(history, keepPolicy{errors: true})
	if keep[0] || keep[1] {
		t.Fatalf("pre-summary group was pinned: %v", keep)
	}
	if !keep[3] || !keep[4] {
		t.Fatalf("post-summary group was not pinned: %v", keep)
	}
}

func TestIsUserMarkedMessage(t *testing.T) {
	for _, text := range []string{" [[KEEP]] fact", "[keep] fact", "<keep>fact", "<!-- keep --> fact"} {
		if !isUserMarkedMessage(message.Message{Role: message.RoleUser, Content: text}) {
			t.Fatalf("not marked: %q", text)
		}
	}
	for _, text := range []string{"keep this", "prefix [keep] fact", ""} {
		if isUserMarkedMessage(message.Message{Role: message.RoleUser, Content: text}) {
			t.Fatalf("unexpected marker: %q", text)
		}
	}
}

func TestProtectedTailStartMovesBeforeToolCallGroup(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleSystem, Content: "sys"},
		{Role: message.RoleUser, Content: "task"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{{ID: "a", Name: "Read"}}},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "a", Content: strings.Repeat("x", 2000)}},
		{Role: message.RoleAssistant, Content: "done"},
	}
	start := protectedTailStart(history, 1, 1, 2)
	if start != 2 {
		t.Fatalf("tail start = %d, want assistant tool call at 2", start)
	}
}

func TestPartitionCompactionRegionWithPolicyKeepsMarkedAndErrorGroups(t *testing.T) {
	region := []message.Message{
		{Role: message.RoleAssistant, Content: strings.Repeat("old", 200)},
		{Role: message.RoleUser, Content: "[keep] exact constraint"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{{ID: "a", Name: "Read"}, {ID: "b", Name: "Bash"}}},
		{Role: message.RoleUser, ToolResults: []message.ToolResult{{ToolUseID: "a", Content: "ok"}, {ToolUseID: "b", Content: "error: failed"}}},
	}
	kept, fold := partitionCompactionRegionWithPolicy(region, 1000, keepPolicy{errors: true, userMarked: true})
	if len(kept) != 3 {
		t.Fatalf("kept = %#v", kept)
	}
	if len(fold) != 1 || fold[0].Role != message.RoleAssistant || fold[0].Content == "" {
		t.Fatalf("fold = %#v", fold)
	}
}
