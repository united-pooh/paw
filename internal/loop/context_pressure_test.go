package loop

import (
	"context"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/tool"
	"paw/internal/ui"
)

func TestMaintainContextProjectionThresholds(t *testing.T) {
	tests := []struct {
		name          string
		ratio         float64
		wantSnip      int
		wantPrune     int
		wantSummaries int
	}{
		{name: "below soft", ratio: 0.49},
		{name: "soft only", ratio: 0.50},
		{name: "snip", ratio: 0.60, wantSnip: 1},
		{name: "prune clears pressure", ratio: 0.80, wantPrune: 1},
		{name: "force compact", ratio: 0.90, wantPrune: 1, wantSummaries: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			history := pressureFixture(strings.Repeat("tool output line\n", 1200))
			estimated := estimateMessageTokens(history)
			limit := int(float64(estimated) / test.ratio)
			if limit <= 0 {
				t.Fatal("invalid test limit")
			}
			modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "summary"}, {Done: true}}}}}
			runner := newPressureTestRunner(t, modelClient, limit)

			got, err := runner.maintainContextProjection(context.Background(), history, true)
			if err != nil {
				t.Fatal(err)
			}
			if got.snippedResults != test.wantSnip || got.prunedResults != test.wantPrune {
				t.Fatalf("maintenance = %+v, want snip=%d prune=%d", got, test.wantSnip, test.wantPrune)
			}
			if len(modelClient.calls) != test.wantSummaries {
				t.Fatalf("summary calls = %d, want %d", len(modelClient.calls), test.wantSummaries)
			}
		})
	}
}

func TestPruneAvoidsSummaryWhenReestimatedBelowThreshold(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "old", Name: "Read"}}),
		buildToolResultMessage("old", strings.Repeat("large stale result\n", 1500), false),
		{Role: message.RoleAssistant, Content: "recent answer"},
		{Role: message.RoleUser, Content: "continue"},
	}
	estimated := estimateMessageTokens(history)
	modelClient := &fakeModel{}
	limit := int(float64(estimated) / 0.82)
	runner := newPressureTestRunner(t, modelClient, limit)

	got, err := runner.maintainContextProjection(context.Background(), history, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.prunedResults != 1 || got.summaryPerformed || len(modelClient.calls) != 0 {
		t.Fatalf("maintenance = %+v calls=%d", got, len(modelClient.calls))
	}
	if estimateMessageTokens(got.history) >= int(float64(limit)*runner.contextMaintenance.compactRatio) {
		t.Fatal("prune did not clear context pressure")
	}
}

func TestNonForcedCompactionSkipsUneconomicFold(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		{Role: message.RoleAssistant, Content: strings.Repeat("small ", 120)},
		{Role: message.RoleAssistant, Content: "recent one"},
		{Role: message.RoleUser, Content: "recent two"},
	}
	modelClient := &fakeModel{}
	runner := newPressureTestRunner(t, modelClient, 1000)
	runner.usage = model.Usage{PromptTokens: 850}
	runner.usageKnown = true

	got, err := runner.maintainContextProjection(context.Background(), history, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.summaryPerformed || len(modelClient.calls) != 0 {
		t.Fatalf("uneconomic fold summarized: %+v calls=%d", got, len(modelClient.calls))
	}
}

func TestForcedCompactionBypassesEconomics(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		{Role: message.RoleAssistant, Content: strings.Repeat("small ", 120)},
		{Role: message.RoleAssistant, Content: "recent one"},
		{Role: message.RoleUser, Content: "recent two"},
	}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "forced summary"}, {Done: true}}}}}
	runner := newPressureTestRunner(t, modelClient, 1000)
	runner.usage = model.Usage{PromptTokens: 950}
	runner.usageKnown = true

	got, err := runner.maintainContextProjection(context.Background(), history, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.summaryPerformed || len(modelClient.calls) != 1 {
		t.Fatalf("forced maintenance = %+v calls=%d", got, len(modelClient.calls))
	}
}

func TestAutomaticCompactionStuckPausesAndClears(t *testing.T) {
	runner := newPressureTestRunner(t, &fakeModel{}, 1000)
	if !runner.automaticSummaryAllowed() {
		t.Fatal("summary unexpectedly blocked initially")
	}
	runner.recordAutomaticCompaction(true, false)
	if !runner.automaticSummaryAllowed() {
		t.Fatal("summary blocked after one compaction")
	}
	runner.recordAutomaticCompaction(true, false)
	if runner.automaticSummaryAllowed() {
		t.Fatal("summary not paused after two consecutive compactions")
	}
	runner.recordAutomaticCompaction(false, true)
	if !runner.automaticSummaryAllowed() {
		t.Fatal("summary did not resume below compact threshold")
	}
}

func TestSoftPressureNoticeOnlyOnceAndResetsBelowThreshold(t *testing.T) {
	output := &fakeUI{}
	runner := NewRunnerWithInstructionRoot(&fakeModel{}, output, tool.NewRegistry(), nil, "notice", t.TempDir())
	runner.SetContextLimitTokens(1000)
	history := []message.Message{{Role: message.RoleUser, Content: "small"}}

	runner.usage = model.Usage{PromptTokens: 550}
	runner.usageKnown = true
	if _, err := runner.maintainContextProjection(context.Background(), history, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.maintainContextProjection(context.Background(), history, false); err != nil {
		t.Fatal(err)
	}
	if got := countSystemEvents(output.systemEvents(), "context pressure reached"); got != 1 {
		t.Fatalf("soft notices = %d, want 1: %#v", got, output.system)
	}

	runner.usage = model.Usage{PromptTokens: 400}
	if _, err := runner.maintainContextProjection(context.Background(), history, false); err != nil {
		t.Fatal(err)
	}
	runner.usage = model.Usage{PromptTokens: 550}
	if _, err := runner.maintainContextProjection(context.Background(), history, false); err != nil {
		t.Fatal(err)
	}
	if got := countSystemEvents(output.systemEvents(), "context pressure reached"); got != 2 {
		t.Fatalf("soft notices after reset = %d, want 2: %#v", got, output.system)
	}
}

func TestAutomaticCompactionStuckNotificationOnlyOnTransition(t *testing.T) {
	output := &fakeUI{}
	runner := NewRunnerWithInstructionRoot(&fakeModel{}, output, tool.NewRegistry(), nil, "stuck", t.TempDir())
	if runner.recordAutomaticCompaction(true, false) {
		t.Fatal("entered stuck after first compaction")
	}
	if !runner.recordAutomaticCompaction(true, false) {
		t.Fatal("did not enter stuck after second compaction")
	}
	if runner.recordAutomaticCompaction(true, false) {
		t.Fatal("reported repeated stuck transition")
	}
}

func countSystemEvents(events []ui.SystemEvent, text string) int {
	count := 0
	for _, event := range events {
		if strings.Contains(event.Body, text) {
			count++
		}
	}
	return count
}

func TestFoldEconomics(t *testing.T) {
	if foldEconomics([]message.Message{{Role: message.RoleAssistant, Content: "tiny"}}) {
		t.Fatal("tiny fold considered economic")
	}
	if !foldEconomics([]message.Message{{Role: message.RoleAssistant, Content: strings.Repeat("large history ", 200)}}) {
		t.Fatal("large fold considered uneconomic")
	}
}

func pressureFixture(toolOutput string) []message.Message {
	return []message.Message{
		{Role: message.RoleUser, Content: "initial task"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "old", Name: "Read"}}),
		buildToolResultMessage("old", toolOutput, false),
		{Role: message.RoleAssistant, Content: strings.Repeat("older reasoning ", 500)},
		{Role: message.RoleAssistant, Content: "recent answer"},
		{Role: message.RoleUser, Content: "continue"},
	}
}

func newPressureTestRunner(t *testing.T, modelClient ModelStreamer, limit int) *Runner {
	t.Helper()
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "pressure", t.TempDir())
	cfg, err := contextMaintenanceConfigFromSettings(settings.DefaultContextMaintenanceConfig())
	if err != nil {
		t.Fatal(err)
	}
	archive, err := newCompactionArchive(runner.workRoot, runner.sessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	runner.contextMaintenance = cfg
	runner.compactionArchive = archive
	runner.contextLimitTokens = limit
	return runner
}
