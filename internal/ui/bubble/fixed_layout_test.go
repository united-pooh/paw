package bubble

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/settings"
	"paw/internal/task"
	selecttool "paw/internal/tool/select"
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

	// /setting 向导已全屏化（与 /config 一致），不再作为 docked frame 上的 overlay，
	// 因此不参与 frame-invariant 断言；见 TestConfigCenterSettingWizardRoutesFullscreen。

	model.sessionPicker = newSessionPicker()
	assertFixedFrame(t, model.View(), 80, 24)
	model.sessionPicker = nil

	model.openActivity(activityTabTodo)
	assertFixedFrame(t, model.View(), 80, 24)
}

func TestVisualGeometryFullViewUsesUnifiedCellPipeline(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	profiles := []struct {
		name    string
		profile termenv.Profile
	}{
		{name: "truecolor", profile: termenv.TrueColor},
		{name: "no-color", profile: termenv.Ascii},
	}
	sizes := []struct {
		width  int
		height int
	}{
		{width: 40, height: 50},
		{width: 80, height: 36},
		{width: 100, height: 30},
	}

	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			lipgloss.SetColorProfile(profile.profile)
			for _, size := range sizes {
				model := newTestModel(&fakeRunner{})
				model.ready = true
				model.width = size.width
				model.height = size.height
				model.transcript = []transcriptEntry{
					{
						kind: entryAssistant,
						body: "| 文件 | 改动 |\n|---|---|\n" +
							"| `responses.go` / `openai_compatible_adapter.go` / `deepseek_adapter.go` | 中文 👩‍💻 é हिन्दी العربية |\n\n" +
							"```go\nfunc render() { fmt.Println(\"cell\") }\n```",
					},
					{
						kind:          entryTool,
						toolName:      "Write",
						toolStatus:    "ok",
						toolTarget:    "internal/ui/bubble/openai_compatible_adapter.go",
						toolResult:    "@@ -1,2 +1,2 @@\n-old 中文背景\n+new 👨‍👩‍👧‍👦 background",
						toolExpanded:  true,
						toolGroupOpen: true,
					},
				}
				model.relayout()
				model.refreshViewport()
				assertVisualGeometryView(t, model.View(), size.width, size.height)

				model.selectionDock = newSelectionDock(selecttool.Request{
					ID:     "visual-geometry",
					Prompt: "选择包含中文、emoji 👩‍💻 与组合字符 é 的操作",
					Mode:   selecttool.ModeMultiple,
					Options: []selecttool.Option{
						{ID: "logs", Label: "查看日志", Description: "保留 TrueColor 样式与完整 grapheme"},
						{ID: "diff", Label: "检查 diff", Description: "竖边框保持同一 cell 列"},
					},
				})
				model.relayout()
				assertVisualGeometryView(t, model.View(), size.width, size.height)

				model.selectionDock = nil
				model.completion = &completion{kind: completionKindCommand, items: []string{"/model", "/tasks"}}
				model.relayout()
				assertVisualGeometryView(t, model.View(), size.width, size.height)
			}
		})
	}
}

func assertVisualGeometryView(t *testing.T, view string, width, height int) {
	t.Helper()
	for index, line := range strings.Split(view, "\n") {
		assertTerminalSequencesComplete(t, line)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("line %d styled width=%d, want %d: raw=%q plain=%q", index, got, width, line, ansi.Strip(line))
		}
		plain := ansi.Strip(line)
		if got := terminalCellWidth(plain); got != width {
			t.Fatalf("line %d stripped width=%d, want %d: raw=%q plain=%q", index, got, width, line, plain)
		}
		for _, leaked := range []string{";52m", "48;2;", "38;5;"} {
			if strings.Contains(plain, leaked) {
				t.Fatalf("line %d leaked ANSI payload %q: %q", index, leaked, plain)
			}
		}
	}
	assertFixedFrame(t, view, width, height)
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
		&fakeTaskController{tasks: []task.TaskSnapshot{{
			ID:              "worker-1",
			ParentSessionID: "session-1",
			Status:          task.TaskRunning,
		}}},
		nil,
		newTerminalCursorAnchor(),
	)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	handled, cmd := model.handleCommand("/tasks")
	if !handled || cmd != nil || !model.activity.visible || model.activity.tab != activityTabTasks {
		t.Fatalf("/tasks handled=%v cmd=%v activity=%#v", handled, cmd, model.activity)
	}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if model.activity.tab != activityTabTodo {
		t.Fatalf("tab=%v, want Pipeline", model.activity.tab)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = next.(appModel)
	if model.activity.tab != activityTabTasks {
		t.Fatalf("shift+tab=%v, want Tasks", model.activity.tab)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.activity.visible {
		t.Fatalf("Esc activity=%#v, want nil", model.activity)
	}

	handled, cmd = model.handleCommand("/todo")
	if !handled || cmd != nil || !model.activity.visible || model.activity.tab != activityTabTodo {
		t.Fatalf("/todo handled=%v cmd=%v activity=%#v", handled, cmd, model.activity)
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

	// 输入区上方只保留完整 context progress bar；模式位于下边框左侧。
	top := ansi.Strip(model.renderDockStatusLine(80))
	if strings.Contains(top, "chat") || strings.Trim(top, tokenFreeGlyph+tokenCacheGlyph+tokenUsedGlyph) != "" {
		t.Fatalf("top context bar = %q, want only progress glyphs", top)
	}
	bottom := ansi.Strip(model.renderBottomDockLine(80))
	if !strings.Contains(bottom, "chat") {
		t.Fatalf("bottom border = %q, want chat indicator", bottom)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(top, unwanted) || strings.Contains(bottom, unwanted) {
			t.Fatalf("dock lines should not contain %q: top=%q bottom=%q", unwanted, top, bottom)
		}
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
	model.modelWizard.selectedIndex = len(model.modelWizard.providerOptions) - 1
	if got := ansi.Strip(model.renderProviderStep()); !strings.Contains(got, model.modelWizard.providerOptions[len(model.modelWizard.providerOptions)-1].label) {
		t.Fatalf("short model modal hid selected provider:\n%s", got)
	}

	model.settingWizard = newSettingWizard(settings.DefaultConfig())
	model.settingWizard.selected[settingWizardContext] = len(settingOptions(settingWizardContext)) - 1
	if got := ansi.Strip(model.renderSettingChoiceStep()); !strings.Contains(got, "fork") {
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
			lastUsedAt:   time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
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
