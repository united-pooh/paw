// 本文件覆盖 Bubble Tea TUI 的提交、布局、历史、模型向导、Markdown 和光标锚点行为。
package bubble

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gocode/internal/message"
	modelcfg "gocode/internal/model"
	"gocode/internal/ui"
)

// fakeRunner 记录测试中的提交输入，并模拟对话 runner。
type fakeRunner struct {
	inputs     []string
	resetCalls int
	err        error
}

// RunTurn 记录输入并返回固定 assistant 消息。
func (r *fakeRunner) RunTurn(ctx context.Context, input string) (message.Message, error) {
	r.inputs = append(r.inputs, input)
	return message.Message{Role: message.RoleAssistant, Content: "ok"}, r.err
}

// ResetHistory 记录历史重置调用次数。
func (r *fakeRunner) ResetHistory() {
	r.resetCalls++
}

// fakeModelConfigController 模拟模型配置控制器的读取、保存和应用行为。
type fakeModelConfigController struct {
	current modelcfg.Config
	applied []modelcfg.Config
	saved   []modelcfg.Config
	err     error
	saveErr error
}

// CurrentModelConfig 返回测试控制器中的当前配置。
func (c *fakeModelConfigController) CurrentModelConfig() modelcfg.Config {
	return c.current
}

// ApplyModelConfig 记录应用过的模型配置，并可模拟应用失败。
func (c *fakeModelConfigController) ApplyModelConfig(cfg modelcfg.Config) error {
	if c.err != nil {
		return c.err
	}
	c.applied = append(c.applied, cfg)
	c.current = cfg
	return nil
}

// SaveModelConfig 记录持久化过的模型配置，并可模拟保存失败。
func (c *fakeModelConfigController) SaveModelConfig(cfg modelcfg.Config) error {
	if c.saveErr != nil {
		return c.saveErr
	}
	c.saved = append(c.saved, cfg)
	return nil
}

// newTestModel 创建带默认 custom 配置的测试用 appModel。
func newTestModel(runner Runner) appModel {
	return newModel(context.Background(), runner, "session-1", &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      modelcfg.ProviderCustom,
			APIBaseURL:    modelcfg.CustomAPIBaseURL,
			APIPath:       modelcfg.CustomChatPath,
			APIKey:        "custom-secret",
			APIKeyEnvName: modelcfg.CustomAPIKeyEnvName,
			Model:         modelcfg.CustomDefaultModel,
			Timeout:       time.Minute,
		},
	}, newTerminalCursorAnchor())
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSubmitRunsTurnAndRecordsUserEntry 验证普通输入会写入 transcript 并启动一轮模型调用。
func TestSubmitRunsTurnAndRecordsUserEntry(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("hello")

	updatedModel, cmd := model.handleSubmit()
	updated := updatedModel.(appModel)
	if cmd == nil {
		t.Fatalf("handleSubmit() cmd is nil")
	}
	if !updated.running {
		t.Fatalf("updated.running = false, want true")
	}
	if got := updated.transcript[len(updated.transcript)-1]; got.kind != entryUser || got.body != "hello" {
		t.Fatalf("last transcript entry = %#v", got)
	}

	msg := cmd()
	if _, ok := msg.(turnFinishedMsg); !ok {
		t.Fatalf("cmd() = %#v, want turnFinishedMsg", msg)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != "hello" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
}

// TestCommandsHandleStatusAndClear 验证 /status 和 /clear 命令的 transcript 与 runner 副作用。
func TestCommandsHandleStatusAndClear(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)

	handled, cmd := model.handleCommand("/status")
	if !handled || cmd != nil {
		t.Fatalf("/status handled/cmd = %v/%v", handled, cmd)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "session-1") {
		t.Fatalf("status body = %q", got)
	}

	handled, cmd = model.handleCommand("/clear")
	if !handled || cmd != nil {
		t.Fatalf("/clear handled/cmd = %v/%v", handled, cmd)
	}
	if runner.resetCalls != 1 {
		t.Fatalf("resetCalls = %d", runner.resetCalls)
	}
	if len(model.transcript) != 1 || model.transcript[0].body != "history cleared" {
		t.Fatalf("transcript = %#v", model.transcript)
	}
}

// TestHelpComesFromCommandRegistry 验证 /help 内容由注册表元数据生成并包含别名。
func TestHelpComesFromCommandRegistry(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	handled, cmd := model.handleCommand("/help")
	if !handled || cmd != nil {
		t.Fatalf("/help handled/cmd = %v/%v", handled, cmd)
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{"/help", "/model", "/status", "/clear", "/exit (/quit)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("help body = %q, want %q", body, want)
		}
	}
}

// TestUnknownSlashCommandDoesNotRunTurn 验证未知命令不会进入模型。
func TestUnknownSlashCommandDoesNotRunTurn(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("/unknown")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd != nil {
		t.Fatalf("unknown command returned cmd")
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner.inputs = %#v, want empty", runner.inputs)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.kind != entryError || !strings.Contains(last.body, "/unknown") {
		t.Fatalf("last transcript entry = %#v", last)
	}
	if len(model.inputHistory) != 0 {
		t.Fatalf("inputHistory = %#v, want empty", model.inputHistory)
	}
}

// TestRunningCommandPolicyAllowsStatusAndBlocksClear 验证运行中命令策略。
func TestRunningCommandPolicyAllowsStatusAndBlocksClear(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	if !model.queryGuard.StartModel() {
		t.Fatalf("StartModel() failed")
	}
	model.syncRunningFlags()

	handled, cmd := model.handleCommand("/status")
	if !handled || cmd != nil {
		t.Fatalf("/status handled/cmd = %v/%v", handled, cmd)
	}
	if got := model.transcript[len(model.transcript)-1].title; got != "status" {
		t.Fatalf("last title = %q, want status", got)
	}

	handled, cmd = model.handleCommand("/clear")
	if !handled || cmd != nil {
		t.Fatalf("/clear handled/cmd = %v/%v", handled, cmd)
	}
	if runner.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, want 0", runner.resetCalls)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "busy" || !strings.Contains(last.body, "/clear") {
		t.Fatalf("last transcript entry = %#v", last)
	}
}

// TestRunningModelQueuesChatFIFO 验证模型运行中普通聊天按 FIFO 排队且不重入 runner。
func TestRunningModelQueuesChatFIFO(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.input.SetValue("second")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued submit returned cmd")
	}
	model.input.SetValue("third")
	next, queuedCmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("second queued submit returned cmd")
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner.inputs before first cmd = %#v", runner.inputs)
	}
	if got := model.chatQueue.Len(); got != 2 {
		t.Fatalf("queue len = %d, want 2", got)
	}

	_ = cmd()
	model.queryGuard.FinishModel()
	queuedCmd = model.startNextQueuedTurn()
	if queuedCmd == nil {
		t.Fatalf("startNextQueuedTurn() cmd is nil")
	}
	_ = queuedCmd()
	model.queryGuard.FinishModel()
	queuedCmd = model.startNextQueuedTurn()
	if queuedCmd == nil {
		t.Fatalf("second startNextQueuedTurn() cmd is nil")
	}
	_ = queuedCmd()

	if want := []string{"first", "second", "third"}; !equalStrings(runner.inputs, want) {
		t.Fatalf("runner.inputs = %#v, want %#v", runner.inputs, want)
	}
}

// TestExitClearsQueuedWork 验证退出后不会继续启动 queued turn。
func TestExitClearsQueuedWork(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if !model.queryGuard.StartModel() {
		t.Fatalf("StartModel() failed")
	}
	model.syncRunningFlags()
	model = model.queueChatInput("queued")
	if got := model.chatQueue.Len(); got != 1 {
		t.Fatalf("queue len = %d, want 1", got)
	}

	handled, cmd := model.handleCommand("/exit")
	if !handled || cmd == nil {
		t.Fatalf("/exit handled/cmd = %v/%v", handled, cmd)
	}
	if !model.queryGuard.IsCanceled() {
		t.Fatalf("query guard not canceled")
	}
	if queuedCmd := model.startNextQueuedTurn(); queuedCmd != nil {
		t.Fatalf("startNextQueuedTurn after exit returned cmd")
	}
}

// TestAssistantAndToolMessagesUpdateTranscript 验证 assistant 增量和工具事件会正确更新聊天历史。
func TestAssistantAndToolMessagesUpdateTranscript(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	next, _ := model.Update(assistantDeltaMsg("he"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("llo"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)

	if got := model.transcript[len(model.transcript)-1]; got.kind != entryAssistant || got.body != "hello" {
		t.Fatalf("assistant entry = %#v", got)
	}

	next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{Name: "Read", Content: "module gocode"}))
	model = next.(appModel)

	rendered := renderTranscript(model.transcript, 80)
	for _, want := range []string{"Read", "file_path", "module gocode"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript = %q, want %q", rendered, want)
		}
	}
}

// TestEnterKeySubmitsThroughUpdate 验证 Enter 按键经过 Update 路径提交输入。
func TestEnterKeySubmitsThroughUpdate(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("hello")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(appModel)
	if cmd == nil {
		t.Fatalf("Update(enter) cmd is nil")
	}
	if !updated.running {
		t.Fatalf("updated.running = false")
	}
	_ = cmd()

	if len(runner.inputs) != 1 || runner.inputs[0] != "hello" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
}

// TestInputHistoryUsesArrowKeysAndPreservesDraft 验证上下键历史切换会保留当前草稿。
func TestInputHistoryUsesArrowKeysAndPreservesDraft(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)

	model.input.SetValue("first")
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.running = false
	model.input.SetValue("second")
	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("second submit cmd is nil")
	}

	model.running = false
	model.input.SetValue("draft")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("first up value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("second up value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("third up value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "first" {
		t.Fatalf("fourth up value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("down value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("second down value = %q", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("final down value = %q", got)
	}
}

// TestInputHistoryWaitsForMultilineBoundary 验证多行输入中上键先移动光标，抵达开头后才切历史。
func TestInputHistoryWaitsForMultilineBoundary(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.inputHistory = []string{"previous"}
	model.input.SetValue("one\ntwo")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "one\ntwo" {
		t.Fatalf("first up value = %q", got)
	}
	if got := model.input.Line(); got != 0 {
		t.Fatalf("line after first up = %d", got)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "one\ntwo" {
		t.Fatalf("second up value = %q", got)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "previous" {
		t.Fatalf("third up value = %q", got)
	}
	if !textareaCursorAtEnd(model.input) {
		t.Fatalf("history recall should leave cursor at end")
	}
}

// TestCtrlJInsertsNewlineAndEnterSubmitsMultiline 验证换行快捷键插入多行内容，Enter 提交完整文本。
func TestCtrlJInsertsNewlineAndEnterSubmitsMultiline(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	model = next.(appModel)

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("enter after multiline input returned nil cmd")
	}
	_ = cmd()

	if len(runner.inputs) != 1 || runner.inputs[0] != "first\nsecond" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
}

// TestInputHistorySkipsControlCommands 验证控制命令不会污染普通输入历史。
func TestInputHistorySkipsControlCommands(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	model.input.SetValue("/model")
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if len(model.inputHistory) != 0 {
		t.Fatalf("inputHistory = %#v", model.inputHistory)
	}
	model.modelWizard = nil

	model.input.SetValue("!")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if len(model.inputHistory) != 0 {
		t.Fatalf("inputHistory = %#v", model.inputHistory)
	}

	model.input.SetValue("!pwd")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if len(model.inputHistory) != 1 || model.inputHistory[0] != "!pwd" {
		t.Fatalf("inputHistory = %#v", model.inputHistory)
	}
}

// TestViewUsesMinimalPlaceholderAndOmitsHelpLine 验证输入框只展示最简 > 占位符并移除帮助行。
func TestViewUsesMinimalPlaceholderAndOmitsHelpLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if model.input.Placeholder != ">" {
		t.Fatalf("placeholder = %q", model.input.Placeholder)
	}
	if model.input.Prompt != "" {
		t.Fatalf("prompt = %q", model.input.Prompt)
	}

	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	rendered := model.View()
	for _, unwanted := range []string{"enter send", "trailing \\", "/status /clear /help /exit", "esc quit"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("view = %q, should not contain %q", rendered, unwanted)
		}
	}
}

// TestViewFramesTranscriptHistoryPanel 验证聊天历史区域有边框并包含系统启动消息。
func TestViewFramesTranscriptHistoryPanel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	rendered := model.View()
	for _, want := range []string{"╭", "╰", "Interactive mode is running", "Input"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("view = %q, want %q", rendered, want)
		}
	}
}

// TestRelayoutReservesSpaceForTranscriptFrame 验证布局会为历史面板边框保留空间。
func TestRelayoutReservesSpaceForTranscriptFrame(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()

	rendered := model.View()
	if got := lipgloss.Height(rendered); got > model.height {
		t.Fatalf("rendered height = %d, want <= %d", got, model.height)
	}
	if model.viewport.Width != model.width-transcriptPanelHorizontalFrame {
		t.Fatalf("viewport width = %d, want %d", model.viewport.Width, model.width-transcriptPanelHorizontalFrame)
	}
}

// TestRenderInputBoxDoesNotDuplicatePrompt 验证输入框不会渲染重复的 > 提示符。
func TestRenderInputBoxDoesNotDuplicatePrompt(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("hello")

	rendered := model.renderInputBox()
	for _, unwanted := range []string{">hello", "> hello", "> >hello"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("input box = %q, should not contain %q", rendered, unwanted)
		}
	}
	if !strings.Contains(rendered, "hello") {
		t.Fatalf("input box = %q, want typed text", rendered)
	}
}

// TestTextareaInputDoesNotSetTextBackground 验证输入文字和当前行不设置背景色。
func TestTextareaInputDoesNotSetTextBackground(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	noColor := lipgloss.NoColor{}

	for name, style := range map[string]lipgloss.Style{
		"focused base":        model.input.FocusedStyle.Base,
		"focused cursor line": model.input.FocusedStyle.CursorLine,
		"focused text":        model.input.FocusedStyle.Text,
		"blurred base":        model.input.BlurredStyle.Base,
		"blurred cursor line": model.input.BlurredStyle.CursorLine,
		"blurred text":        model.input.BlurredStyle.Text,
	} {
		if got := style.GetBackground(); got != noColor {
			t.Fatalf("%s background = %#v, want no color", name, got)
		}
	}
}

// TestBangValuePreviewsTerminalPanel 验证输入 ! 时会预览终端模式面板样式。
func TestBangValuePreviewsTerminalPanel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("!")
	model.syncInputMode()
	model.applyCursorAnimation()

	rendered := model.renderInputBox()
	if !strings.Contains(rendered, "Terminal") {
		t.Fatalf("input box = %q", rendered)
	}
}

// TestViewAnchorsTerminalCursorOnInputCell 验证 View 会把真实终端光标锚定到输入单元格。
func TestViewAnchorsTerminalCursorOnInputCell(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.input.SetValue("hello")

	_ = model.View()

	position, ok := anchor.consume()
	if !ok {
		t.Fatalf("anchor did not receive a cursor position")
	}
	if !position.active {
		t.Fatalf("cursor position = %#v", position)
	}
	if position.column <= 2 {
		t.Fatalf("column = %d, want input cell position", position.column)
	}
}

// TestViewClearsTerminalCursorAnchorWhenWizardOpen 验证打开模型向导时会清除输入光标锚点。
func TestViewClearsTerminalCursorAnchorWhenWizardOpen(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	_ = model.View()
	if position, ok := anchor.consume(); !ok || !position.active {
		t.Fatalf("initial anchor = %#v/%v", position, ok)
	}

	model.modelWizard = newModelWizard(modelcfg.Config{Provider: modelcfg.ProviderCustom})
	_ = model.View()

	position, ok := anchor.consume()
	if !ok {
		t.Fatalf("anchor did not receive a clear position")
	}
	if position.active {
		t.Fatalf("cursor position = %#v, want inactive", position)
	}
}

// TestMouseWheelScrollsTranscriptViewport 验证鼠标滚轮会滚动聊天历史 viewport。
func TestMouseWheelScrollsTranscriptViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.transcript = nil
	for i := 0; i < 30; i++ {
		model.transcript = append(model.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
			body:  fmt.Sprintf("line %02d", i),
		})
	}
	model.refreshViewport()
	bottomOffset := model.viewport.YOffset

	next, _ := model.Update(tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	if model.viewport.YOffset >= bottomOffset {
		t.Fatalf("YOffset = %d, want less than %d", model.viewport.YOffset, bottomOffset)
	}
}

// TestTranscriptMouseDragSelectsAndCopiesAcrossScroll 验证拖拽到 transcript 底部会滚动并复制跨屏选区。
func TestTranscriptMouseDragSelectsAndCopiesAcrossScroll(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.transcript = nil
	for i := 0; i < 30; i++ {
		model.transcript = append(model.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
			body:  fmt.Sprintf("line %02d", i),
		})
	}
	model.refreshViewport()
	model.viewport.GotoTop()

	next, _ := model.Update(tea.MouseMsg{
		X:      1,
		Y:      2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if !model.selecting || model.selectionActive {
		t.Fatalf("selection state = selecting:%v active:%v", model.selecting, model.selectionActive)
	}

	next, _ = model.Update(tea.MouseMsg{
		X:      1,
		Y:      4,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if model.viewport.YOffset == 0 {
		t.Fatalf("viewport did not auto-scroll at selection edge")
	}

	next, _ = model.Update(tea.MouseMsg{
		X:      1,
		Y:      4,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if model.selecting {
		t.Fatalf("selecting = true, want false after release")
	}
	if !strings.Contains(copied, "line 00") || !strings.Contains(copied, "assistant") {
		t.Fatalf("copied selection = %q", copied)
	}
}

// TestTranscriptMouseDragCopiesCharacterRange 验证拖拽只复制选中的字符范围。
func TestTranscriptMouseDragCopiesCharacterRange(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	next, _ := model.Update(tea.MouseMsg{
		X:      4,
		Y:      3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      6,
		Y:      3,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      6,
		Y:      3,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if copied != "ell" {
		t.Fatalf("copied selection = %q, want ell", copied)
	}
}

// TestTranscriptMouseClickDoesNotCopy 验证单击不会创建一字符选区或覆盖剪贴板。
func TestTranscriptMouseClickDoesNotCopy(t *testing.T) {
	oldWriteClipboard := writeClipboard
	copied := "unchanged"
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	next, _ := model.Update(tea.MouseMsg{
		X:      4,
		Y:      3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      4,
		Y:      3,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if copied != "unchanged" {
		t.Fatalf("clipboard changed to %q", copied)
	}
	if model.selectionActive {
		t.Fatalf("selectionActive = true, want false")
	}
}

// TestTranscriptMouseClickPreservesScrollOffset 验证单击清除选区时不会把聊天历史拉回底部。
func TestTranscriptMouseClickPreservesScrollOffset(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.transcript = nil
	for i := 0; i < 30; i++ {
		model.transcript = append(model.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
			body:  fmt.Sprintf("line %02d", i),
		})
	}
	model.refreshViewport()
	model.viewport.GotoTop()

	next, _ := model.Update(tea.MouseMsg{
		X:      1,
		Y:      2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      1,
		Y:      2,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if model.viewport.YOffset != 0 {
		t.Fatalf("YOffset = %d, want 0", model.viewport.YOffset)
	}
}

// TestSelectedTranscriptTextKeepsWideGraphemesWhole 验证宽字符和 emoji 不会被半个字符复制。
func TestSelectedTranscriptTextKeepsWideGraphemesWhole(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "a你🙂b",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	model.selectionActive = true
	model.selectionStart = selectionPoint{row: 1, col: 3}
	model.selectionEnd = selectionPoint{row: 1, col: 6}

	if got := model.selectedTranscriptText(); got != "你🙂" {
		t.Fatalf("selectedTranscriptText() = %q, want 你🙂", got)
	}
}

// TestModelCommandOpensArrowSelectableProviderWizard 验证 /model 会打开可用方向键选择的 provider 向导。
func TestModelCommandOpensArrowSelectableProviderWizard(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.inputHistory = []string{"old input"}
	model.input.SetValue("/model")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.modelWizard == nil {
		t.Fatalf("/model should open wizard")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.modelWizard.selectedProvider().id != modelcfg.ProviderDeepSeek {
		t.Fatalf("selected provider = %q", model.modelWizard.selectedProvider().id)
	}
}

// TestModelWizardAppliesDeepSeekConfig 验证选择 deepseek 会保存并应用 DeepSeek 配置。
func TestModelWizardAppliesDeepSeekConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-secret")
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      modelcfg.ProviderCustom,
			APIBaseURL:    modelcfg.CustomAPIBaseURL,
			APIPath:       modelcfg.CustomChatPath,
			APIKey:        "custom-secret",
			APIKeyEnvName: modelcfg.CustomAPIKeyEnvName,
			Model:         modelcfg.CustomDefaultModel,
			Timeout:       time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 1

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard != nil {
		t.Fatalf("modelWizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 1 || len(controller.saved) != 1 {
		t.Fatalf("applied/saved = %#v / %#v", controller.applied, controller.saved)
	}
	cfg := controller.applied[0]
	if cfg.Provider != modelcfg.ProviderDeepSeek || cfg.APIBaseURL != modelcfg.DeepSeekAPIBaseURL || cfg.APIPath != modelcfg.DeepSeekChatPath || cfg.Model != modelcfg.DeepSeekDefaultModel {
		t.Fatalf("applied cfg = %#v", cfg)
	}
	if cfg.APIKey != "deepseek-secret" || cfg.APIKeyEnvName != modelcfg.DeepSeekAPIKeyEnvName {
		t.Fatalf("applied key/env = %q/%q", cfg.APIKey, cfg.APIKeyEnvName)
	}
}

// TestModelWizardAllowsCustomConfigWithoutAPIKey 验证 custom provider 在没有真实 API key 时使用默认 dummy key。
func TestModelWizardAllowsCustomConfigWithoutAPIKey(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      modelcfg.ProviderDeepSeek,
			APIBaseURL:    modelcfg.DeepSeekAPIBaseURL,
			APIPath:       modelcfg.DeepSeekChatPath,
			APIKey:        "deepseek-secret",
			APIKeyEnvName: modelcfg.DeepSeekAPIKeyEnvName,
			Model:         modelcfg.DeepSeekDefaultModel,
			Timeout:       time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 0

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard != nil {
		t.Fatalf("modelWizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 1 {
		t.Fatalf("applied = %#v", controller.applied)
	}
	if got := controller.applied[0]; got.Provider != modelcfg.ProviderCustom || got.APIKey != modelcfg.CustomDefaultAPIKey {
		t.Fatalf("applied cfg = %#v", got)
	}
}

// TestModelWizardDoesNotApplyConfigWhenSaveFails 验证配置持久化失败时不会继续应用配置。
func TestModelWizardDoesNotApplyConfigWhenSaveFails(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-secret")
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      modelcfg.ProviderCustom,
			APIBaseURL:    modelcfg.CustomAPIBaseURL,
			APIPath:       modelcfg.CustomChatPath,
			APIKey:        "custom-secret",
			APIKeyEnvName: modelcfg.CustomAPIKeyEnvName,
			Model:         modelcfg.CustomDefaultModel,
			Timeout:       time.Minute,
		},
		saveErr: errors.New("disk full"),
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 1

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard == nil || !strings.Contains(model.modelWizard.err, "disk full") {
		t.Fatalf("wizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 0 {
		t.Fatalf("applied configs = %#v", controller.applied)
	}
}

// TestRenderInputBoxShowsStatefulPanel 验证输入面板会随普通、多行和等待状态切换文案。
func TestRenderInputBoxShowsStatefulPanel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("hello")

	rendered := model.renderInputBox()
	for _, want := range []string{"Input", "hello"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("input box = %q, want %q", rendered, want)
		}
	}

	model.pending = []string{"first"}
	rendered = model.renderInputBox()
	if !strings.Contains(rendered, "Multiline") {
		t.Fatalf("multiline input box = %q", rendered)
	}

	model.running = true
	rendered = model.renderInputBox()
	if !strings.Contains(rendered, "Waiting") || !strings.Contains(rendered, "waiting for assistant") {
		t.Fatalf("waiting input box = %q", rendered)
	}
}

// TestAssistantEntryRendersMarkdown 验证 assistant 消息会经过 Markdown 渲染。
func TestAssistantEntryRendersMarkdown(t *testing.T) {
	rendered := renderEntry(transcriptEntry{
		kind:  entryAssistant,
		title: "assistant",
		body:  "## Title\n\n- one with `code`\n- two\n\n> quoted\n\n```go\nfmt.Println(\"hi\")\n```",
	}, 80)

	for _, want := range []string{"assistant", "## Title", "•", "one", "code", "│", "quoted", "code go", "fmt.Println"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown = %q, want %q", rendered, want)
		}
	}
}

// TestMarkdownCodeBlockKeepsNestedMarkdownFencesInsideBlock 验证 markdown 代码块中的嵌套 fence 不会被错误拆出。
func TestMarkdownCodeBlockKeepsNestedMarkdownFencesInsideBlock(t *testing.T) {
	rendered := renderMarkdown("```markdown\n# 水獭的问候\n\n```go\nfunc hello() {\nfmt.Println(\"Hello!\")\n}\n```\n```", 80)

	for _, want := range []string{"code markdown", "# 水獭的问候", "```go", "func hello()", "fmt.Println", "```"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "code go") {
		t.Fatalf("rendered markdown = %q, should not render inner go fence", rendered)
	}
}

// TestMarkdownTableRendersAsAlignedTable 验证 Markdown 表格会渲染为终端对齐表格。
func TestMarkdownTableRendersAsAlignedTable(t *testing.T) {
	rendered := renderMarkdown("| Name | Value |\n| :--- | ---: |\n| alpha | 1 |\n| beta | two |", 80)

	for _, want := range []string{"Name", "Value", "alpha", "beta", "│", "─┼─"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered table = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{":---", "---:"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("rendered table = %q, should not contain %q", rendered, unwanted)
		}
	}
}

// TestMarkdownCodeBlockKeepsLanguageLabelOutsideBlock 验证代码块语言标签显示在代码块外部。
func TestMarkdownCodeBlockKeepsLanguageLabelOutsideBlock(t *testing.T) {
	rendered := renderMarkdown("```json\n{\"ok\": true}\n```", 80)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "code json") {
		t.Fatalf("rendered code block = %q", rendered)
	}
	for _, line := range lines[1:] {
		if strings.Contains(line, "code json") {
			t.Fatalf("rendered code block = %q, should not include label inside body", rendered)
		}
	}
	if !strings.Contains(rendered, "{\"ok\": true}") {
		t.Fatalf("rendered code block = %q, want json body", rendered)
	}
}

// TestCursorIntensityCycleIsContinuous 验证光标动画周期首尾都是暗态，避免循环边界突兀闪烁。
func TestCursorIntensityCycleIsContinuous(t *testing.T) {
	if got := cursorIntensityAt(0); got != 0 {
		t.Fatalf("cursorIntensityAt(0) = %f, want 0", got)
	}
	if got := cursorIntensityAt(cursorCycleDuration / 12); got != 0 {
		t.Fatalf("cursorIntensityAt(0.25s) = %f, want 0", got)
	}
	if got := cursorIntensityAt(cursorCycleDuration / 4); got != 1 {
		t.Fatalf("cursorIntensityAt(0.75s) = %f, want 1", got)
	}
	if got := cursorIntensityAt(cursorCycleDuration / 3); got != 1 {
		t.Fatalf("cursorIntensityAt(1s) = %f, want 1", got)
	}
	if got := cursorIntensityAt(cursorCycleDuration / 2); got != 0 {
		t.Fatalf("cursorIntensityAt(1.5s) = %f, want 0", got)
	}
	if got := cursorIntensityAt(3 * cursorCycleDuration / 4); got != 1 {
		t.Fatalf("cursorIntensityAt(2.25s) = %f, want 1", got)
	}
	if got := cursorIntensityAt(cursorCycleDuration - time.Nanosecond); got > 0.001 {
		t.Fatalf("cursorIntensityAt(cycle end) = %f, want nearly 0", got)
	}
}

// TestCursorDimColorMatchesTerminalBackground 验证光标最暗时融入终端主题背景色。
func TestCursorDimColorMatchesTerminalBackground(t *testing.T) {
	background := colorManager.Hex(colorTerminalBackground)
	if got := cursorColor(0, false); got != background {
		t.Fatalf("normal dim cursor color = %q, want %q", got, background)
	}
	if got := cursorColor(0, true); got != background {
		t.Fatalf("terminal dim cursor color = %q, want %q", got, background)
	}
}

// TestAnchoredOutputRestoresBeforeNextWrite 验证下一帧写入前会恢复终端光标位置不变量。
func TestAnchoredOutputRestoresBeforeNextWrite(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "anchored-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	anchor := newTerminalCursorAnchor()
	output := newAnchoredOutput(file, anchor)

	anchor.set(terminalCursorPosition{active: true, upFromBottom: 1, column: 7})
	if _, err := output.Write([]byte("frame1\r")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	anchor.set(terminalCursorPosition{active: true, upFromBottom: 2, column: 3})
	if _, err := output.Write([]byte("frame2\r")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "frame1\r\x1b[1A\x1b[7C\x1b[1B\rframe2\r\x1b[2A\x1b[3C"
	if got := string(data); got != want {
		t.Fatalf("anchored output = %q, want %q", got, want)
	}
}
