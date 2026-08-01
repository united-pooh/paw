package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"paw/internal/message"
	"paw/internal/tool"
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

func TestMaintainToolResultsSnipsOnlyStaleLargeResults(t *testing.T) {
	old := strings.Repeat("旧结果\n", 600)
	recent := strings.Repeat("recent\n", 600)
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "old", Name: "Read"}}),
		buildToolResultMessage("old", old, false),
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "recent", Name: "Read"}}),
		buildToolResultMessage("recent", recent, false),
		{Role: message.RoleAssistant, Content: "done"},
	}
	archive, err := newCompactionArchive(t.TempDir(), "maintenance", true)
	if err != nil {
		t.Fatal(err)
	}
	got, stats, err := maintainToolResults(history, maintenanceRequest{
		mode: maintenanceSnip, tailStart: 3, minBytes: 1024,
		policy:  keepPolicy{errors: true, userMarked: true},
		archive: archive, registry: tool.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.results != 1 || !strings.HasPrefix(toolResultsFromMessage(got[2])[0].Content, snippedToolResultMarker) {
		t.Fatalf("stats=%+v history=%+v", stats, got)
	}
	if toolResultsFromMessage(got[4])[0].Content != recent {
		t.Fatal("recent result was rewritten")
	}
	if toolResultsFromMessage(history[2])[0].Content != old {
		t.Fatal("input history was mutated")
	}
}

func TestMaintainToolResultsPreservesMessageShapeAndKeepErrors(t *testing.T) {
	large := strings.Repeat("line\n", 600)
	history := []message.Message{
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "single", Name: "Read"}}),
		buildToolResultMessage("single", large, false),
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "a", Name: "Read"}, {ID: "b", Name: "Bash"}}),
		buildToolResultsMessage([]message.ToolResult{{ToolUseID: "a", Content: large}, {ToolUseID: "b", Content: "error: failed", IsError: true}}),
		{Role: message.RoleAssistant, Content: "done"},
	}
	archive, err := newCompactionArchive(t.TempDir(), "shape", true)
	if err != nil {
		t.Fatal(err)
	}
	got, stats, err := maintainToolResults(history, maintenanceRequest{
		mode: maintenancePrune, tailStart: 4, minBytes: 1024,
		policy: keepPolicy{errors: true, userMarked: true}, archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.results != 1 || got[1].ToolResult == nil || len(got[1].ToolResults) != 0 {
		t.Fatalf("single shape/stats changed: %+v %+v", got[1], stats)
	}
	if len(got[3].ToolResults) != 2 || got[3].ToolResult != nil || got[3].ToolResults[0].Content != large {
		t.Fatalf("protected batch changed: %+v", got[3])
	}
}

func TestSnipToolResultKeepsValidUTF8(t *testing.T) {
	got := snipToolResult("Read", strings.Repeat("你好世界", 1000), "archive.jsonl", snipStrategy{headChars: 11, tailChars: 9})
	if !utf8.ValidString(got) {
		t.Fatal("invalid UTF-8")
	}
}

func TestMaintainToolResultsUpgradesSnipToPruneWithoutRearchive(t *testing.T) {
	original := strings.Repeat("output\n", 600)
	history := []message.Message{
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "call", Name: "Read"}}),
		buildToolResultMessage("call", original, false),
		{Role: message.RoleAssistant, Content: "done"},
	}
	archive, err := newCompactionArchive(t.TempDir(), "upgrade", true)
	if err != nil {
		t.Fatal(err)
	}
	snipped, first, err := maintainToolResults(history, maintenanceRequest{mode: maintenanceSnip, tailStart: 2, minBytes: 1024, archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	pruned, second, err := maintainToolResults(snipped, maintenanceRequest{mode: maintenancePrune, tailStart: 2, minBytes: 1024, archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	marker, ok := parseToolResultMarker(toolResultsFromMessage(pruned[1])[0].Content)
	if !ok || marker.kind != maintenancePrune || marker.originalBytes != len([]byte(original)) {
		t.Fatalf("marker = %+v ok=%v", marker, ok)
	}
	if len(first.archives) != 1 || len(second.archives) != 1 || first.archives[0] != second.archives[0] {
		t.Fatalf("archive not reused: %+v %+v", first, second)
	}
}

type maintenanceTestTool struct {
	name     string
	readOnly bool
	hint     tool.SnipHint
}

func (t maintenanceTestTool) Name() string                                       { return t.name }
func (maintenanceTestTool) Description() string                                  { return "test" }
func (maintenanceTestTool) Run(context.Context, json.RawMessage) (string, error) { return "", nil }
func (maintenanceTestTool) InputSchema() json.RawMessage                         { return json.RawMessage(`{}`) }
func (t maintenanceTestTool) ReadOnly() bool                                     { return t.readOnly }
func (t maintenanceTestTool) SnipHint() tool.SnipHint                            { return t.hint }

func TestSnipStrategyForUsesHintThenCategoryFallback(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(maintenanceTestTool{name: "hinted", readOnly: true, hint: tool.SnipHint{Head: 3, Tail: 2, HeadChars: 100, TailChars: 50}})
	registry.Register(maintenanceTestTool{name: "invalid", readOnly: true, hint: tool.SnipHint{Head: -1}})
	if got := snipStrategyFor(registry, "hinted"); got != (snipStrategy{head: 3, tail: 2, headChars: 100, tailChars: 50}) {
		t.Fatalf("hinted = %+v", got)
	}
	if got := snipStrategyFor(registry, "invalid"); got != defaultReadOnlySnip {
		t.Fatalf("invalid hint fallback = %+v", got)
	}
	if got := snipStrategyFor(registry, "missing"); got != defaultSideEffectingSnip {
		t.Fatalf("unknown fallback = %+v", got)
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
