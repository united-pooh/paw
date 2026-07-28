package bubble

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex-agent-go/internal/settings"
	"codex-agent-go/internal/subagent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestComputeTUILayoutKeepsOuterFrameStable(t *testing.T) {
	base := computeTUILayout(100, 30, 1)
	for requested := 1; requested <= 14; requested++ {
		got := computeTUILayout(100, 30, requested)
		if got.frameWidth != base.frameWidth || got.frameHeight != base.frameHeight ||
			got.contentWidth != base.contentWidth || got.contentHeight != base.contentHeight {
			t.Fatalf("requested=%d layout=%+v, want stable outer geometry %+v", requested, got, base)
		}
		if got.inputHeight < 1 || got.inputHeight > inputMaxVisibleLines {
			t.Fatalf("requested=%d inputHeight=%d, want 1..%d", requested, got.inputHeight, inputMaxVisibleLines)
		}
		if got.headerHeight+got.transcriptHeight+got.statusHeight+got.worktreeHeight+got.inputHeight != got.contentHeight {
			t.Fatalf("requested=%d internal heights do not fill content: %+v", requested, got)
		}
	}
}

func TestViewFrameInvariantAcrossContentAndOverlays(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	assertFixedFrame(t, model.View(), 80, 24)

	model.transcript = append(model.transcript, transcriptEntry{
		kind:  entryAssistant,
		title: "assistant",
		body:  "中文 👨‍👩‍👧‍👦 e\u0301\n### 混合语言\n中文 English 日本語 한국어 Русский العربية हिन्दी ภาษาไทย\n```go\nfmt.Println(\"stream\")\n```",
	})
	model.refreshViewport()
	assertFixedFrame(t, model.View(), 80, 24)

	model.completion = &completion{kind: completionKindCommand, items: []string{"/model", "/tasks"}, loading: false}
	assertFixedFrame(t, model.View(), 80, 24)
	model.completion = nil

	model.modelWizard = newModelWizard(model.currentModelConfig())
	assertFixedFrame(t, model.View(), 80, 24)
	model.modelWizard = nil

	model.settingWizard = newSettingWizard(settings.DefaultConfig())
	assertFixedFrame(t, model.View(), 80, 24)
	model.settingWizard = nil

	model.sessionPicker = newSessionPicker()
	assertFixedFrame(t, model.View(), 80, 24)
	model.sessionPicker = nil

	model.openActivity(activityTabPipeline)
	assertFixedFrame(t, model.View(), 80, 24)
}

func TestMixedLanguageTranscriptKeepsRendererWidthWhileScrolling(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 160
	model.height = 60
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body: strings.Repeat("before\n", 70) +
			"### 混合语言\n中文 English 日本語 한국어 Русский العربية हिन्दी ภาษาไทย\n" +
			strings.Repeat("after\n", 70),
		renderMode: transcriptRenderFormatted,
	}}
	model.relayout()
	model.refreshViewport()

	model.viewport.GotoTop()
	for {
		offset := model.viewport.YOffset
		for row, line := range strings.Split(model.View(), "\n") {
			if got := ansi.StringWidth(line); got != model.width {
				t.Fatalf("offset=%d row=%d renderer width=%d, want %d; line=%q", offset, row, got, model.width, ansi.Strip(line))
			}
		}
		if model.viewport.AtBottom() {
			break
		}
		model.viewport.ScrollDown(1)
		if model.viewport.YOffset == offset {
			t.Fatalf("viewport stopped before reaching bottom at offset=%d", offset)
		}
	}
}

func TestInputGrowthOnlyChangesInternalAllocation(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 90
	model.height = 30

	model.input.SetValue("one")
	model.relayout()
	oneLine := model.currentLayout()
	assertFixedFrame(t, model.View(), 90, 30)

	model.input.SetValue(strings.Join([]string{"1", "2", "3", "4", "5"}, "\n"))
	model.relayout()
	fiveLines := model.currentLayout()
	assertFixedFrame(t, model.View(), 90, 30)
	if fiveLines.inputHeight != 5 || fiveLines.transcriptHeight != oneLine.transcriptHeight-4 {
		t.Fatalf("one=%+v five=%+v, want input growth to consume transcript rows only", oneLine, fiveLines)
	}

	model.input.SetValue(strings.Join([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, "\n"))
	model.relayout()
	twelveLines := model.currentLayout()
	assertFixedFrame(t, model.View(), 90, 30)
	if twelveLines.inputHeight != inputMaxVisibleLines {
		t.Fatalf("inputHeight=%d, want cap %d", twelveLines.inputHeight, inputMaxVisibleLines)
	}
}

func TestCompletionAndModalDoNotRelayoutViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	wantHeight := model.viewport.Height

	model.completion = &completion{kind: completionKindCommand, items: []string{"/help"}, loading: false}
	model.relayout()
	if model.viewport.Height != wantHeight {
		t.Fatalf("completion viewport height=%d, want %d", model.viewport.Height, wantHeight)
	}

	model.completion = nil
	model.modelWizard = newModelWizard(model.currentModelConfig())
	model.relayout()
	if model.viewport.Height != wantHeight {
		t.Fatalf("modal viewport height=%d, want %d", model.viewport.Height, wantHeight)
	}
}

func TestActivityCommandsAndTabs(t *testing.T) {
	model := newModel(
		context.Background(),
		&fakeRunner{},
		"session-1",
		&fakeModelConfigController{},
		nil,
		&fakeSubagentController{tasks: []subagent.TaskSnapshot{{
			ID:              "worker-1",
			ParentSessionID: "session-1",
			Status:          subagent.TaskRunning,
		}}},
		nil,
		newTerminalCursorAnchor(),
	)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	handled, cmd := model.handleCommand("/tasks")
	if !handled || cmd != nil || model.subagentPicker == nil || model.subagentPicker.tab != activityTabSubagents {
		t.Fatalf("/tasks handled=%v cmd=%v activity=%#v", handled, cmd, model.subagentPicker)
	}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if model.subagentPicker.tab != activityTabPipeline {
		t.Fatalf("tab=%v, want Pipeline", model.subagentPicker.tab)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = next.(appModel)
	if model.subagentPicker.tab != activityTabSubagents {
		t.Fatalf("shift+tab=%v, want Subagents", model.subagentPicker.tab)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.subagentPicker != nil {
		t.Fatalf("Esc activity=%#v, want nil", model.subagentPicker)
	}

	handled, cmd = model.handleCommand("/pipeline")
	if !handled || cmd != nil || model.subagentPicker == nil || model.subagentPicker.tab != activityTabPipeline {
		t.Fatalf("/pipeline handled=%v cmd=%v activity=%#v", handled, cmd, model.subagentPicker)
	}
}

func TestDockStatusLineFitsNarrowWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Now()
	for _, width := range []int{8, 16, 32, 80} {
		line := model.renderDockStatusLine(width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d status display width=%d line=%q", width, got, ansi.Strip(line))
		}
	}

	// 状态行按信息优先级排列：ready · chat · token info。
	line := ansi.Strip(model.renderDockStatusLine(80))
	if ready, chat := strings.Index(line, "ready"), strings.Index(line, "chat"); ready < 0 || chat < 0 || ready >= chat {
		t.Fatalf("status order = %q", line)
	}
}

func TestOpaqueOverlayPreservesTranscriptOutsideItsRect(t *testing.T) {
	base := strings.Join([]string{
		strings.Repeat("a", 20),
		strings.Repeat("b", 20),
		strings.Repeat("c", 20),
		strings.Repeat("d", 20),
		strings.Repeat("e", 20),
	}, "\n")
	got := ansi.Strip(placeOpaqueOverlay(base, "XXX\nYYY", 20, 5, overlayAlignCenter))
	lines := strings.Split(got, "\n")
	if lines[0] != strings.Repeat("a", 20) || lines[4] != strings.Repeat("e", 20) {
		t.Fatalf("overlay changed rows outside its rect:\n%s", got)
	}
	if lines[1] != strings.Repeat("b", 8)+"XXX"+strings.Repeat("b", 9) {
		t.Fatalf("center overlay row=%q", lines[1])
	}
}

func TestModalListsKeepSelectionVisibleInShortTranscript(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 60
	model.height = 10
	model.relayout()

	model.modelWizard = newModelWizard(model.currentModelConfig())
	model.modelWizard.selectedIndex = len(modelProviderOptions) - 1
	if got := ansi.Strip(model.renderProviderStep()); !strings.Contains(got, modelProviderOptions[len(modelProviderOptions)-1].label) {
		t.Fatalf("short model modal hid selected provider:\n%s", got)
	}

	model.settingWizard = newSettingWizard(settings.DefaultConfig())
	model.settingWizard.step = settingWizardLimit
	model.settingWizard.selected[settingWizardLimit] = len(settingOptions(settingWizardLimit)) - 1
	if got := ansi.Strip(model.renderSettingChoiceStep()); !strings.Contains(got, "128000") {
		t.Fatalf("short settings modal hid selected option:\n%s", got)
	}

	model.sessionPicker = &sessionPicker{selectedIndex: 9}
	for i := 0; i < 10; i++ {
		model.sessionPicker.sessions = append(model.sessionPicker.sessions, sessionSummaryItem{
			sessionID:    "sess000" + string(rune('a'+i)),
			firstMessage: "session-" + string(rune('a'+i)),
		})
	}
	if got := ansi.Strip(model.renderSessionPickerContent()); !strings.Contains(got, "session-j") {
		t.Fatalf("short sessions modal hid selected session:\n%s", got)
	}
}

func TestSessionPickerModalKeepsBottomBorderWhenSummaryIsLong(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.sessionPicker = &sessionPicker{
		loading: false,
		sessions: []sessionSummaryItem{{
			sessionID:    "session-1",
			createdAt:    time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			firstMessage: strings.Repeat("long summary ", 12),
		}},
	}

	lines := strings.Split(ansi.Strip(model.renderSessionPickerBox()), "\n")
	if len(lines) < 2 {
		t.Fatalf("session picker panel has %d lines, want a border and body", len(lines))
	}
	topBorder := strings.TrimRight(lines[0], " ")
	if !strings.HasPrefix(topBorder, "┌") || !strings.HasSuffix(topBorder, "┐") {
		t.Fatalf("top border=%q, want normal corners", lines[0])
	}
	bottomBorder := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.HasPrefix(bottomBorder, "└") || !strings.HasSuffix(bottomBorder, "┘") {
		t.Fatalf("bottom border=%q, want normal corners\n%s", lines[len(lines)-1], strings.Join(lines, "\n"))
	}
}

func assertFixedFrame(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) != height {
		t.Fatalf("frame height=%d, want %d\n%s", len(lines), height, ansi.Strip(view))
	}
	for i, line := range lines {
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("line %d width=%d, want %d: %q", i, got, width, line)
		}
	}
	if strings.TrimSpace(lines[0]) == "" || !strings.Contains(strings.TrimSpace(lines[0]), "─") {
		t.Fatalf("top border=%q, want a hairline rule", lines[0])
	}
	if strings.TrimSpace(lines[len(lines)-1]) == "" || !strings.Contains(strings.TrimSpace(lines[len(lines)-1]), "─") {
		t.Fatalf("bottom border=%q, want a hairline rule", lines[len(lines)-1])
	}
}
