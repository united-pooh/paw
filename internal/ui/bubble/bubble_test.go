// 本文件覆盖 Bubble Tea TUI 的提交、布局、历史、模型向导、Markdown 和光标锚点行为。
package bubble

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"gocode/internal/loop"
	"gocode/internal/message"
	modelcfg "gocode/internal/model"
	"gocode/internal/settings"
	"gocode/internal/subagent"
	"gocode/internal/ui"
)

// fakeRunner 记录测试中的提交输入，并模拟对话 runner。
type fakeRunner struct {
	inputs           []string
	resetCalls       int
	loadHistoryCalls []string
	loadHistoryErr   error
	err              error
	stats            loop.ContextStats
	lastDraft        string
	lastLimit        int
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

// LoadHistory 记录加载历史的调用，并可模拟失败。
func (r *fakeRunner) LoadHistory(ctx context.Context, sessionID string) error {
	r.loadHistoryCalls = append(r.loadHistoryCalls, sessionID)
	return r.loadHistoryErr
}

// ContextStats returns deterministic context-usage data for meter tests.
func (r *fakeRunner) ContextStats(limitTokens int, draft string) loop.ContextStats {
	r.lastLimit = limitTokens
	r.lastDraft = draft
	stats := r.stats
	if stats.LimitTokens == 0 {
		stats.LimitTokens = limitTokens
	}
	return stats
}

// fakeModelConfigController 模拟模型配置控制器的读取、保存和应用行为。
type fakeModelConfigController struct {
	current modelcfg.Config
	applied []modelcfg.Config
	saved   []modelcfg.Config
	err     error
	saveErr error
}

type fakeSettingsController struct {
	current settings.Config
	saved   []settings.Config
	err     error
}

func (c *fakeSettingsController) CurrentSettings() settings.Config {
	return c.current
}

func (c *fakeSettingsController) SaveSettings(cfg settings.Config) error {
	if c.err != nil {
		return c.err
	}
	c.saved = append(c.saved, cfg)
	c.current = cfg
	return nil
}

type fakeSubagentController struct {
	runResult      subagent.Result
	runErr         error
	launchTask     subagent.TaskSnapshot
	launchErr      error
	tasks          []subagent.TaskSnapshot
	runRequests    []subagent.Request
	launchRequests []subagent.Request
}

func (c *fakeSubagentController) Run(ctx context.Context, req subagent.Request) (subagent.Result, error) {
	c.runRequests = append(c.runRequests, req)
	if c.runErr != nil {
		return subagent.Result{}, c.runErr
	}
	return c.runResult, nil
}

func (c *fakeSubagentController) Launch(ctx context.Context, req subagent.Request) (subagent.TaskSnapshot, error) {
	c.launchRequests = append(c.launchRequests, req)
	if c.launchErr != nil {
		return subagent.TaskSnapshot{}, c.launchErr
	}
	task := c.launchTask
	if task.ID == "" {
		task.ID = "task-1"
	}
	if task.SessionID == "" {
		task.SessionID = task.ID
	}
	if task.TranscriptPath == "" {
		task.TranscriptPath = "/tmp/" + task.ID + ".jsonl"
	}
	if task.Status == "" {
		task.Status = subagent.TaskRunning
	}
	if task.ContextMode == "" {
		task.ContextMode = req.ContextMode
	}
	if task.RunMode == "" {
		task.RunMode = settings.RunModeBackground
	}
	task.Prompt = req.Prompt
	task.Description = req.Description
	c.tasks = append([]subagent.TaskSnapshot(nil), append(c.tasks, task)...)
	return task, nil
}

func (c *fakeSubagentController) ListTasks() []subagent.TaskSnapshot {
	return append([]subagent.TaskSnapshot(nil), c.tasks...)
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
	}, nil, nil, nil, newTerminalCursorAnchor())
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
	for _, want := range []string{
		"/help - show available commands",
		"/model [status|custom|deepseek] - open the model switcher",
		"/export [filename] - export the current conversation",
		"/setting - open settings wizard",
		"/subagent [--fork|--empty] [--background|--sync] <prompt> - launch a subagent",
		"/tasks - show background subagent tasks",
		"/exit (/quit) - quit the TUI",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("help body = %q, want %q", body, want)
		}
	}
}

// TestModelCommandVariantsKeepWizardAndShortcuts verifies wizard and direct subcommands coexist.
func TestModelCommandVariantsKeepWizardAndShortcuts(t *testing.T) {
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
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/model status")
	if !handled || cmd != nil {
		t.Fatalf("/model status handled/cmd = %v/%v", handled, cmd)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "provider=custom") {
		t.Fatalf("status body = %q", got)
	}

	handled, cmd = model.handleCommand("/model custom")
	if !handled || cmd != nil {
		t.Fatalf("/model custom handled/cmd = %v/%v", handled, cmd)
	}
	if len(controller.applied) != 1 || controller.applied[0].Provider != modelcfg.ProviderCustom {
		t.Fatalf("custom applied configs = %#v", controller.applied)
	}

	handled, cmd = model.handleCommand("/model deepseek")
	if !handled || cmd != nil {
		t.Fatalf("/model deepseek handled/cmd = %v/%v", handled, cmd)
	}
	if len(controller.applied) != 2 || controller.applied[1].Provider != modelcfg.ProviderDeepSeek {
		t.Fatalf("applied configs = %#v", controller.applied)
	}
}

// TestExportCommandWritesExplicitAndDefaultTranscriptFiles verifies /export paths and content.
func TestExportCommandWritesExplicitAndDefaultTranscriptFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	model := newTestModel(&fakeRunner{})
	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "hello"},
		{kind: entryAssistant, title: "assistant", body: "world"},
	}
	explicitPath := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(explicitPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed export file: %v", err)
	}

	handled, cmd := model.handleCommand("/export notes")
	if !handled || cmd != nil {
		t.Fatalf("/export notes handled/cmd = %v/%v", handled, cmd)
	}
	data, err := os.ReadFile(explicitPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", explicitPath, err)
	}
	if got := string(data); !strings.Contains(got, "you:\nhello") || !strings.Contains(got, "assistant:\nworld") {
		t.Fatalf("exported content = %q", got)
	}
	info, err := os.Stat(explicitPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", explicitPath, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("explicit export mode = %#o, want 0600", got)
	}

	handled, cmd = model.handleCommand("/export")
	if !handled || cmd != nil {
		t.Fatalf("/export handled/cmd = %v/%v", handled, cmd)
	}
	exports, err := filepath.Glob(filepath.Join(root, ".ccagent", "exports", "conversation-*.txt"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("exports = %#v, want 1 generated transcript", exports)
	}
	info, err = os.Stat(exports[0])
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", exports[0], err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("generated export mode = %#o, want 0600", got)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "export" || !strings.Contains(last.body, exports[0]) {
		t.Fatalf("last transcript entry = %#v, want generated export path", last)
	}
}

// TestSettingCommandPersistsWizardSelections verifies /setting saves normalized config through the controller.
func TestSettingCommandPersistsWizardSelections(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/setting")
	if !handled || cmd != nil {
		t.Fatalf("/setting handled/cmd = %v/%v", handled, cmd)
	}
	if model.settingWizard == nil {
		t.Fatalf("/setting should open wizard")
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.settingWizard != nil {
		t.Fatalf("settingWizard = %#v, want nil after save", model.settingWizard)
	}
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	got := settingsController.saved[0]
	if got.Subagent.DefaultContextMode != settings.ContextModeEmpty ||
		got.Subagent.DefaultRunMode != settings.RunModeBackground ||
		got.UI.ContextMeterLocation != settings.MeterLocationHeader ||
		got.UI.ContextLimitTokens != 200000 {
		t.Fatalf("saved config = %#v", got)
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{
		"subagent.context=empty",
		"subagent.run=background",
		"ui.context_limit=200000",
		"ui.context_meter=header",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings summary = %q, want %q", body, want)
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

	rendered := renderTranscript(model.transcript, 80, model.showThinking)
	for _, want := range []string{"Read", "file_path", "module gocode"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript = %q, want %q", rendered, want)
		}
	}
}

func TestToolEntryRevealsDetailsWithExpandAnimation(t *testing.T) {
	started := time.Unix(20, 0)
	entry := transcriptEntry{
		kind:      entryTool,
		title:     "tool",
		body:      "Read {\n  \"file_path\": \"go.mod\"\n}",
		createdAt: started,
	}

	early := renderEntryAt(entry, 80, started)
	if !strings.Contains(early, "Read {") {
		t.Fatalf("early tool entry = %q, want summary", early)
	}
	if strings.Contains(early, "file_path") {
		t.Fatalf("early tool entry = %q, should hide details at animation start", early)
	}

	late := renderEntryAt(entry, 80, started.Add(toolExpandDuration))
	for _, want := range []string{"Read {", "file_path", "> "} {
		if !strings.Contains(late, want) {
			t.Fatalf("late tool entry = %q, want %q", late, want)
		}
	}
}

func TestCtrlOTogglesThinkingTranscriptRendering(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()

	next, _ := model.Update(thinkingDeltaMsg("hidden thought"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("answer"))
	model = next.(appModel)

	if len(model.transcript) < 2 || model.transcript[len(model.transcript)-2].kind != entryThinking {
		t.Fatalf("transcript = %#v, want thinking entry before answer", model.transcript)
	}
	if strings.Contains(model.viewport.View(), "hidden thought") {
		t.Fatalf("viewport = %q, should hide thinking by default", model.viewport.View())
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = next.(appModel)
	if !model.showThinking {
		t.Fatalf("showThinking = false, want true")
	}
	if !strings.Contains(model.viewport.View(), "hidden thought") {
		t.Fatalf("viewport = %q, want expanded thinking", model.viewport.View())
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = next.(appModel)
	if model.showThinking {
		t.Fatalf("showThinking = true, want false")
	}
	if strings.Contains(model.viewport.View(), "hidden thought") {
		t.Fatalf("viewport = %q, should hide thinking after second ctrl+o", model.viewport.View())
	}
}

func TestSystemEventDoesNotSplitActiveAssistantStream(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	next, _ := model.Update(assistantDeltaMsg("he"))
	model = next.(appModel)
	next, _ = model.Update(systemEventMsg(ui.SystemEvent{Title: "subagent", Body: "background task finished"}))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("llo"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)

	assistantBodies := make([]string, 0, len(model.transcript))
	for _, entry := range model.transcript {
		if entry.kind == entryAssistant {
			assistantBodies = append(assistantBodies, entry.body)
		}
	}
	if got := len(assistantBodies); got != 1 {
		t.Fatalf("assistant entry count = %d, want 1: %#v", got, assistantBodies)
	}
	if assistantBodies[0] != "hello" {
		t.Fatalf("assistant body = %q, want hello", assistantBodies[0])
	}
	last := model.transcript[len(model.transcript)-1]
	if last.kind != entrySystem || last.title != "subagent" {
		t.Fatalf("last transcript entry = %#v, want subagent system event", last)
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
	for _, want := range []string{"╭", "╰", "Interactive mode is running"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("view = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{"Input", "Waiting", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("view = %q, should not contain %q", rendered, unwanted)
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
	leftColWidth := model.width - model.sidebarWidth
	wantViewportWidth := maxInt(20, leftColWidth-transcriptPanelHorizontalFrame)
	if model.viewport.Width != wantViewportWidth {
		t.Fatalf("viewport width = %d, want %d", model.viewport.Width, wantViewportWidth)
	}
}

func TestViewFillsTerminalHeightAndPinsInputToBottom(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 18
	model.input.SetValue("bottom")
	model.relayout()

	rendered := model.View()
	if got := lipgloss.Height(rendered); got != model.height {
		t.Fatalf("rendered height = %d, want %d\n%s", got, model.height, rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 || !strings.Contains(lines[len(lines)-2], "bottom") {
		t.Fatalf("last input content line = %q, want bottom", lines[maxInt(0, len(lines)-2)])
	}
}

func TestRelayoutAccountsForInputTitleMeterHeight(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		Subagent: settings.SubagentConfig{
			DefaultContextMode: settings.ContextModeEmpty,
			DefaultRunMode:     settings.RunModeSync,
		},
		UI: settings.UIConfig{
			ContextLimitTokens:   settings.DefaultContextLimitTokens,
			ContextMeterLocation: settings.MeterLocationInputTitle,
		},
	}}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()

	minimum := maxInt(1, model.height-model.headerHeight()-lipgloss.Height(model.renderInputBox())-transcriptPanelVerticalFrame)
	if model.viewport.Height < minimum {
		t.Fatalf("viewport height = %d, want at least %d", model.viewport.Height, minimum)
	}
	if got := lipgloss.Height(model.View()); got != model.height {
		t.Fatalf("rendered height = %d, want %d", got, model.height)
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
	for _, want := range []string{"!"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("input box = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{"0%(0.0%)", "100%", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("input box = %q, should not contain %q", rendered, unwanted)
		}
	}
}

// TestSubagentCommandUsesDefaultsAndTasksRender verifies /subagent defaults and /tasks output.
func TestSubagentCommandUsesDefaultsAndTasksRender(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		Subagent: settings.SubagentConfig{
			DefaultContextMode: settings.ContextModeFork,
			DefaultRunMode:     settings.RunModeBackground,
		},
		UI: settings.UIConfig{
			ContextLimitTokens:   settings.DefaultContextLimitTokens,
			ContextMeterLocation: settings.MeterLocationInputTitle,
		},
	}}
	subagents := &fakeSubagentController{
		launchTask: subagent.TaskSnapshot{
			ID:             "task-42",
			SessionID:      "task-42",
			Status:         subagent.TaskRunning,
			ContextMode:    settings.ContextModeFork,
			RunMode:        settings.RunModeBackground,
			TranscriptPath: "/tmp/task-42.jsonl",
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, subagents, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/subagent summarize recent changes")
	if !handled || cmd != nil {
		t.Fatalf("/subagent handled/cmd = %v/%v", handled, cmd)
	}
	if len(subagents.launchRequests) != 1 {
		t.Fatalf("launch requests = %#v", subagents.launchRequests)
	}
	req := subagents.launchRequests[0]
	if req.ContextMode != settings.ContextModeFork || req.RunMode != settings.RunModeBackground || req.ParentSessionID != "session-1" || req.Prompt != "summarize recent changes" {
		t.Fatalf("launch request = %#v", req)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "task-42 status=running context=fork") {
		t.Fatalf("launch transcript = %q", got)
	}

	handled, cmd = model.handleCommand("/tasks")
	if !handled || cmd != nil {
		t.Fatalf("/tasks handled/cmd = %v/%v", handled, cmd)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "task-42 status=running context=fork") {
		t.Fatalf("tasks transcript = %q", got)
	}
}

// TestSyncSubagentCompletionStartsQueuedTurn verifies sync subagent completion drains queued chat work.
func TestSyncSubagentCompletionStartsQueuedTurn(t *testing.T) {
	runner := &fakeRunner{}
	subagents := &fakeSubagentController{
		runResult: subagent.Result{
			AgentID:        "agent-7",
			SessionID:      "agent-7",
			ContextMode:    settings.ContextModeEmpty,
			RunMode:        settings.RunModeSync,
			TranscriptPath: "/tmp/agent-7.jsonl",
			Content:        "subagent complete",
		},
	}
	model := newModel(context.Background(), runner, "session-1", &fakeModelConfigController{}, nil, subagents, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/subagent inspect this")
	if !handled || cmd == nil {
		t.Fatalf("/subagent handled/cmd = %v/%v", handled, cmd)
	}
	if !model.running {
		t.Fatalf("model.running = false, want true after sync subagent starts")
	}

	model.input.SetValue("queued follow-up")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued chat returned cmd")
	}

	msg := cmd()
	next, followCmd := model.Update(msg)
	model = next.(appModel)
	if followCmd == nil {
		t.Fatalf("sync subagent completion should start queued turn")
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "subagent" || !strings.Contains(last.body, "agent=agent-7") || !strings.Contains(last.body, "/tmp/agent-7.jsonl") {
		t.Fatalf("subagent transcript = %#v", last)
	}

	finished, ok := followCmd().(turnFinishedMsg)
	if !ok {
		t.Fatalf("followCmd() = %#v, want turnFinishedMsg", followCmd())
	}
	if finished.err != nil {
		t.Fatalf("queued turn err = %v", finished.err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != "queued follow-up" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
}

// TestViewAnchorsTerminalCursorOnInputCell 验证 View 会把真实终端光标锚定到输入单元格。
func TestViewAnchorsTerminalCursorOnInputCell(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
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
	if position.upFromBottom != 1 {
		t.Fatalf("upFromBottom = %d, want input content row", position.upFromBottom)
	}
	if position.column <= 2 {
		t.Fatalf("column = %d, want input cell position", position.column)
	}
}

// TestViewAnchorsTerminalCursorWithEmbeddedInputTitle 验证 context 位于输入框 title 时仍锚定到内容行。
func TestViewAnchorsTerminalCursorWithEmbeddedInputTitle(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	settingsController := &fakeSettingsController{current: settings.Config{
		UI: settings.UIConfig{
			ContextLimitTokens:   1000,
			ContextMeterLocation: settings.MeterLocationInputTitle,
		},
	}}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.input.SetValue("hello")

	_ = model.View()

	position, ok := anchor.consume()
	if !ok || !position.active {
		t.Fatalf("anchor = %#v/%v, want active", position, ok)
	}
	if position.upFromBottom != 1 {
		t.Fatalf("upFromBottom = %d, want input content row below embedded title", position.upFromBottom)
	}
}

// TestViewClearsTerminalCursorAnchorWhenWizardOpen 验证打开模型向导时会清除输入光标锚点。
func TestViewClearsTerminalCursorAnchorWhenWizardOpen(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
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
	topY := model.headerHeight() + 1
	bottomY := model.headerHeight() + maxInt(1, model.viewport.Height)

	next, _ := model.Update(tea.MouseMsg{
		X:      1,
		Y:      topY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if !model.selecting || model.selectionActive {
		t.Fatalf("selection state = selecting:%v active:%v", model.selecting, model.selectionActive)
	}

	next, _ = model.Update(tea.MouseMsg{
		X:      1,
		Y:      bottomY,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if model.viewport.YOffset == 0 {
		t.Fatalf("viewport did not auto-scroll at selection edge")
	}

	next, _ = model.Update(tea.MouseMsg{
		X:      1,
		Y:      bottomY,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if model.selecting {
		t.Fatalf("selecting = true, want false after release")
	}
	if !strings.Contains(copied, "line 00") || !strings.Contains(copied, "assistant") {
		t.Fatalf("copied selection = %q start=%+v end=%+v active=%v yOffset=%d viewportHeight=%d lines=%d", copied, model.selectionStart, model.selectionEnd, model.selectionActive, model.viewport.YOffset, model.viewport.Height, len(model.transcriptLineSnapshots()))
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
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
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
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
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
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
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
	for _, want := range []string{"hello"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("input box = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{"0%(0.0%)", "100%", "Input", "Waiting", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("input box = %q, should not contain %q", rendered, unwanted)
		}
	}
	meter := model.contextMeterLine(maxInt(28, model.width-2))
	for _, want := range []string{"0↑", "free(100%)"} {
		if !strings.Contains(meter, want) {
			t.Fatalf("context meter line = %q, want %q", meter, want)
		}
	}

	model.pending = []string{"first"}
	rendered = model.renderInputBox()
	if !strings.Contains(rendered, "hello") || strings.Contains(rendered, "0%(0.0%)") {
		t.Fatalf("multiline input box = %q", rendered)
	}

	model.running = true
	rendered = model.renderInputBox()
	if !strings.Contains(rendered, "waiting for assistant") || strings.Contains(rendered, "0%(0.0%)") {
		t.Fatalf("waiting input box = %q", rendered)
	}
}

// TestContextMeterUsesDefaultLimitAndStableSegments verifies default limit, colors, and bar math.
func TestContextMeterUsesDefaultLimitAndStableSegments(t *testing.T) {
	runner := &fakeRunner{
		stats: loop.ContextStats{
			UsedTokens:  262144,
			CacheTokens: 104858,
		},
	}
	model := newTestModel(runner)
	model.input.SetValue("draft prompt")

	title := model.contextMeterTitle()
	if runner.lastLimit != settings.DefaultContextLimitTokens {
		t.Fatalf("lastLimit = %d, want %d", runner.lastLimit, settings.DefaultContextLimitTokens)
	}
	if runner.lastDraft != "draft prompt" {
		t.Fatalf("lastDraft = %q, want draft prompt", runner.lastDraft)
	}
	for _, want := range []string{"262k↑", "25%(10%)", "free(75%)"} {
		if !strings.Contains(title, want) {
			t.Fatalf("contextMeterTitle() = %q, want %q", title, want)
		}
	}
	bar := renderContextBar(runner.stats.UsedTokens, runner.stats.CacheTokens, settings.DefaultContextLimitTokens, 40, "")
	if strings.Count(bar, "▰") != 10 || strings.Count(bar, "▱") != 30 {
		t.Fatalf("renderContextBar() = %q, want 10 filled and 30 free cells", bar)
	}
	for role, want := range map[string]string{
		fmt.Sprint(contextCacheStyle.GetForeground()): "214",
		fmt.Sprint(contextUsedStyle.GetForeground()):  "111",
		fmt.Sprint(contextFreeStyle.GetForeground()):  "240",
	} {
		if role != want {
			t.Fatalf("context style foreground = %q, want %q", role, want)
		}
	}
}

func TestContextMeterShowsCurrentContextSize(t *testing.T) {
	runner := &fakeRunner{
		stats: loop.ContextStats{
			UsedTokens:  800,
			CacheTokens: 100,
			LimitTokens: 1000,
		},
	}
	model := newTestModel(runner)

	meter := model.contextMeterLine(48)
	for _, want := range []string{"800↑", "80%(10%)", "free(20%)"} {
		if !strings.Contains(meter, want) {
			t.Fatalf("meter = %q, want %q", meter, want)
		}
	}
}

func TestContextMeterLineStretchesAndAlignsLabels(t *testing.T) {
	runner := &fakeRunner{
		stats: loop.ContextStats{
			UsedTokens:  settings.DefaultContextLimitTokens / 4,
			CacheTokens: 0,
		},
	}
	model := newTestModel(runner)
	model.width = 64

	width := maxInt(28, model.width-2)
	meter := model.contextMeterLine(width)
	if got, want := lipgloss.Width(meter), width; got != want {
		t.Fatalf("meter width = %d, want %d: %q", got, want, meter)
	}
	usedIndex := strings.Index(meter, "↑")
	freeIndex := strings.LastIndex(meter, "free(")
	if usedIndex == -1 || freeIndex == -1 || usedIndex > freeIndex {
		t.Fatalf("meter = %q, want used token label before free token label", meter)
	}
}

func TestFormatCompactTokenCountUsesThreeDigitsAndUnits(t *testing.T) {
	tests := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1k",
		1234:    "1.23k",
		12345:   "12.3k",
		123456:  "123k",
		999000:  "999k",
		1000000: "1M",
		1234567: "1.23M",
	}
	for value, want := range tests {
		if got := formatCompactTokenCount(value); got != want {
			t.Fatalf("formatCompactTokenCount(%d) = %q, want %q", value, got, want)
		}
	}
}

// TestContextMeterAnimatesBarAndLabels 验证进度条和数字标签在动画过程中都呈插值变化。
func TestContextMeterAnimatesBarAndLabels(t *testing.T) {
	started := time.Unix(30, 0)
	runner := &fakeRunner{
		stats: loop.ContextStats{
			LimitTokens: 1000,
			UsedTokens:  100,
			CacheTokens: 0,
		},
	}
	model := newTestModel(runner)
	model.cursorFrameAt = started
	model.updateContextMeterAnimation()

	runner.stats.UsedTokens = 400
	model.cursorFrameAt = started.Add(100 * time.Millisecond)
	model.updateContextMeterAnimation()

	// 260ms：动画进行中，进度条和标签都应显示中间插值，而非立即跳到 400
	model.cursorFrameAt = started.Add(260 * time.Millisecond)
	animatedUsed, _, pulse := model.animatedContextTokens(1000)
	if animatedUsed <= 100 || animatedUsed > 400 {
		t.Fatalf("animatedUsed = %d, want value between 100 and 400 (bar animating)", animatedUsed)
	}
	if pulse <= 0 {
		t.Fatalf("pulse = %f, want active pulse during animation", pulse)
	}
	meter := model.contextMeterLine(48)
	// 标签数字应也在动画中：不等于旧值 100↑，也不等于新值 400↑
	if strings.Contains(meter, "100↑") {
		t.Fatalf("meter = %q, label should have advanced past old value 100↑", meter)
	}
	if strings.Contains(meter, "400↑") {
		t.Fatalf("meter = %q, label should not jump to final value 400↑ yet", meter)
	}

	// 动画结束（1s 后）：进度条和标签都应到达目标值
	model.cursorFrameAt = started.Add(time.Second)
	animatedUsed, _, pulse = model.animatedContextTokens(1000)
	if animatedUsed != 400 || pulse != 0 {
		t.Fatalf("animation end = used %d pulse %f, want target 400 and no pulse", animatedUsed, pulse)
	}
	finalMeter := model.contextMeterLine(48)
	if !strings.Contains(finalMeter, "400↑") {
		t.Fatalf("finalMeter = %q, want 400↑ after animation completes", finalMeter)
	}
}

func TestContextMeterShowsThinkingTimerCenteredInLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.running = true
	model.runningTerminal = false
	model.turnStartedAt = time.Unix(10, 0)
	model.cursorFrameAt = time.Unix(11, 500*int64(time.Millisecond))

	width := maxInt(28, model.width-2)
	meter := model.contextMeterLine(width)
	if !strings.Contains(meter, "<thinking 1s>") {
		t.Fatalf("meter = %q, want thinking timer", meter)
	}
	thinkingIndex := strings.Index(meter, "<thinking 1s>")
	usedIndex := strings.Index(meter, "↑")
	freeIndex := strings.LastIndex(meter, "free(")
	if thinkingIndex == -1 || usedIndex == -1 || freeIndex == -1 || thinkingIndex < usedIndex || thinkingIndex > freeIndex {
		t.Fatalf("meter = %q, want thinking timer between token labels", meter)
	}
	plain := ansi.Strip(meter)
	thinkingLeft := strings.Index(plain, "<thinking 1s>")
	thinkingCenter := lipgloss.Width(plain[:thinkingLeft]) + lipgloss.Width("<thinking 1s>")/2
	lineCenter := lipgloss.Width(plain) / 2
	if delta := thinkingCenter - lineCenter; delta < -1 || delta > 1 {
		t.Fatalf("meter = %q, want thinking timer centered in line", meter)
	}
}

// TestNarrowLayoutKeepsInputVisible verifies compact layouts render within height and keep input visible.
func TestNarrowLayoutKeepsInputVisible(t *testing.T) {
	runner := &fakeRunner{
		stats: loop.ContextStats{
			UsedTokens:  settings.DefaultContextLimitTokens / 2,
			CacheTokens: 0,
		},
	}
	model := newTestModel(runner)
	model.ready = true
	model.width = 36
	model.height = 10
	model.input.SetValue("narrow")
	model.relayout()

	rendered := model.View()
	if got := lipgloss.Height(rendered); got > model.height {
		t.Fatalf("rendered height = %d, want <= %d", got, model.height)
	}
	if !strings.Contains(rendered, "narrow") {
		t.Fatalf("view = %q, want input text visible", rendered)
	}
	for _, unwanted := range []string{"Input", "Waiting", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("view = %q, should not contain %q", rendered, unwanted)
		}
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
	want := "frame1\r\r\x1b[1A\x1b[7C\x1b[1B\rframe2\r\r\x1b[2A\x1b[3C"
	if got := string(data); got != want {
		t.Fatalf("anchored output = %q, want %q", got, want)
	}
}

// TestContextMeter_空闲时显示上箭头 验证 isGenerating=false 时标签含 ↑ 不含 ↓。
func TestContextMeter_空闲时显示上箭头(t *testing.T) {
	label := formatContextUsageLabel(1000, 0, 200000, false)
	if !strings.Contains(label, "↑") {
		t.Errorf("空闲时标签应含 ↑，实际: %q", label)
	}
	if strings.Contains(label, "↓") {
		t.Errorf("空闲时标签不应含 ↓，实际: %q", label)
	}
}

// TestContextMeter_推理输出时显示下箭头 验证 isGenerating=true（thinking/文本输出）时标签含 ↓ 不含 ↑。
func TestContextMeter_推理输出时显示下箭头(t *testing.T) {
	label := formatContextUsageLabel(1000, 0, 200000, true)
	if !strings.Contains(label, "↓") {
		t.Errorf("推理/输出时标签应含 ↓，实际: %q", label)
	}
	if strings.Contains(label, "↑") {
		t.Errorf("推理/输出时标签不应含 ↑，实际: %q", label)
	}
}

// TestContextMeter_工具调用后恢复上箭头 验证 isGenerating 清零后标签恢复 ↑。
func TestContextMeter_工具调用后恢复上箭头(t *testing.T) {
	// 先模拟推理阶段
	labelDuring := formatContextUsageLabel(1200, 0, 200000, true)
	if !strings.Contains(labelDuring, "↓") {
		t.Errorf("推理时标签应含 ↓，实际: %q", labelDuring)
	}
	// 工具调用后 isGenerating 被清零
	labelAfter := formatContextUsageLabel(1200, 0, 200000, false)
	if !strings.Contains(labelAfter, "↑") {
		t.Errorf("工具调用后标签应含 ↑，实际: %q", labelAfter)
	}
}

// TestContextMeter_零token时不崩溃 验证 UsedTokens=0 时安全返回 "0↑"。
func TestContextMeter_零token时不崩溃(t *testing.T) {
	label := formatContextUsageLabel(0, 0, 200000, false)
	if !strings.Contains(label, "0↑") {
		t.Errorf("零 token 标签应含 0↑，实际: %q", label)
	}
}

// TestContextMeter_多轮后数字只反映当前context 验证数字等于 UsedTokens 而非 session 累加。
func TestContextMeter_多轮后数字只反映当前context(t *testing.T) {
	// 第 3 轮结束，当前 context = 2000，不是 3 轮累加值 6000
	runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 2000, LimitTokens: 200000}}
	model := newTestModel(runner)
	meter := model.contextMeterLine(60)
	if !strings.Contains(meter, "2k↑") {
		t.Errorf("多轮后 meter 应含当前 context 大小 2k↑，实际: %q", meter)
	}
	if strings.Contains(meter, "6k") {
		t.Errorf("meter 不应出现 session 累加值 6k，实际: %q", meter)
	}
}

// TestContextMeter_thinkingDeltaMsg设置isGenerating 验证 app.go 里 thinkingDeltaMsg 触发 isGenerating=true。
func TestContextMeter_thinkingDeltaMsg设置isGenerating(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if model.isGenerating {
		t.Fatalf("初始 isGenerating 应为 false")
	}
	updated, _ := model.Update(thinkingDeltaMsg("思考中..."))
	next := updated.(appModel)
	if !next.isGenerating {
		t.Errorf("收到 thinkingDeltaMsg 后 isGenerating 应为 true")
	}
}

// TestContextMeter_doneMsg清除isGenerating 验证 doneMsg 触发 isGenerating=false。
func TestContextMeter_doneMsg清除isGenerating(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.isGenerating = true
	updated, _ := model.Update(doneMsg{})
	next := updated.(appModel)
	if next.isGenerating {
		t.Errorf("收到 doneMsg 后 isGenerating 应为 false")
	}
}

// TestSessionsCommandOpensPicker 验证 /sessions 命令打开 sessionPicker。
func TestSessionsCommandOpensPicker(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	handled, _ := model.handleCommand("/sessions")
	if !handled {
		t.Fatalf("/sessions not handled")
	}
	if model.sessionPicker == nil {
		t.Fatalf("sessionPicker = nil, want non-nil after /sessions")
	}
}

// TestSessionPickerEscClosesPicker 验证 esc 键关闭 sessionPicker。
func TestSessionPickerEscClosesPicker(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.sessionPicker = newSessionPicker()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.sessionPicker != nil {
		t.Fatalf("sessionPicker = %#v, want nil after esc", model.sessionPicker)
	}
}

// TestSessionPickerEnterRestoresSession 验证在选择器中按 enter 触发 loadSessionHistoryCmd。
func TestSessionPickerEnterRestoresSession(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.sessionPicker = &sessionPicker{
		loading: false,
		sessions: []sessionSummaryItem{
			{sessionID: "target-session", firstMessage: "hello"},
		},
		selectedIndex: 0,
	}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter on session picker returned nil cmd")
	}
	msg := cmd()
	restored, ok := msg.(sessionRestoredMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want sessionRestoredMsg", msg)
	}
	if restored.err != nil {
		t.Fatalf("sessionRestoredMsg.err = %v", restored.err)
	}
	if restored.sessionID != "target-session" {
		t.Fatalf("sessionRestoredMsg.sessionID = %q, want target-session", restored.sessionID)
	}
}

// TestSessionRestoredMsgUpdatesSessionID 验证 sessionRestoredMsg 更新 m.sessionID。
func TestSessionRestoredMsgUpdatesSessionID(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.sessionPicker = newSessionPicker()

	next, _ := model.Update(sessionRestoredMsg{sessionID: "new-session-id"})
	model = next.(appModel)
	if model.sessionID != "new-session-id" {
		t.Fatalf("sessionID = %q, want new-session-id", model.sessionID)
	}
	if model.sessionPicker != nil {
		t.Fatalf("sessionPicker = %#v, want nil after restore", model.sessionPicker)
	}
}

// TestSlashPrefixTriggersCommandCompletion 验证输入 / 前缀触发命令补全。
func TestSlashPrefixTriggersCommandCompletion(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	// 模拟用户输入 "/"：先设置空值，再通过 rune 键输入 "/"
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model = next.(appModel)

	if model.completion == nil {
		t.Fatalf("completion = nil, want command completion after / input")
	}
	if model.completion.kind != completionKindCommand {
		t.Fatalf("completion.kind = %v, want completionKindCommand", model.completion.kind)
	}
}

// TestAtPrefixTriggersFileCompletion 验证输入 @ 触发文件补全。
func TestAtPrefixTriggersFileCompletion(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	// 先设置 @ 前缀，然后发送一个 rune 键让 isTextEditingKey 触发补全逻辑
	model.input.SetValue("@")
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = next.(appModel)

	if model.completion == nil {
		t.Fatalf("completion = nil, want file completion after @ input")
	}
	if model.completion.kind != completionKindFile {
		t.Fatalf("completion.kind = %v, want completionKindFile", model.completion.kind)
	}
}

// TestCompletionEscClears 验证 esc 键清除补全弹窗。
func TestCompletionEscClears(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = newFileCompletion("test")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.completion != nil {
		t.Fatalf("completion = %#v, want nil after esc", model.completion)
	}
}

// TestCompletionTabAppliesSelection 验证 tab 键应用选中的补全项并清除弹窗。
func TestCompletionTabAppliesSelection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = &completion{
		kind:          completionKindCommand,
		items:         []string{"/help", "/model", "/sessions"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if model.completion != nil {
		t.Fatalf("completion = %#v, want nil after tab", model.completion)
	}
	if got := model.input.Value(); got != "/help " {
		t.Fatalf("input value = %q, want /help after tab completion", got)
	}
}

// TestFormatSessionLabel_SizeDisplay 验证 formatFileSize B/KB/MB 阈值格式化。
func TestFormatSessionLabel_SizeDisplay(t *testing.T) {
	base := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		size     int64
		wantSize string
	}{
		{"bytes", 512, "512B"},
		{"bytes boundary", 1023, "1023B"},
		{"kilobytes", 1024, "1.0KB"},
		{"kilobytes larger", 2048, "2.0KB"},
		{"kilobytes fractional", 1536, "1.5KB"},
		{"megabytes", 1024 * 1024, "1.0MB"},
		{"megabytes larger", 2 * 1024 * 1024, "2.0MB"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item := sessionSummaryItem{
				sessionID:      "abcdef1234567890",
				createdAt:      base,
				firstMessage:   "hello",
				transcriptSize: tc.size,
			}
			label := formatSessionLabel(item)
			if !strings.Contains(label, tc.wantSize) {
				t.Fatalf("formatSessionLabel() = %q, want size %q", label, tc.wantSize)
			}
			// ID should be truncated to 8 chars
			if !strings.Contains(label, "abcdef12") {
				t.Fatalf("formatSessionLabel() = %q, want ID prefix abcdef12", label)
			}
			// Date should be YYYY-MM-DD
			if !strings.Contains(label, "2024-03-15") {
				t.Fatalf("formatSessionLabel() = %q, want date 2024-03-15", label)
			}
		})
	}
}

// TestHelpIncludesSessionsCommand 验证 /help 输出包含 /sessions 命令。
func TestHelpIncludesSessionsCommand(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	handled, cmd := model.handleCommand("/help")
	if !handled || cmd != nil {
		t.Fatalf("/help handled/cmd = %v/%v", handled, cmd)
	}
	body := model.transcript[len(model.transcript)-1].body
	if !strings.Contains(body, "/sessions") {
		t.Fatalf("help body = %q, want /sessions", body)
	}
}

// TestSessionPickerKeyNavigation 验证上下键在会话选择器中移动选中项。
func TestSessionPickerKeyNavigation(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.sessionPicker = &sessionPicker{
		loading: false,
		sessions: []sessionSummaryItem{
			{sessionID: "s1"},
			{sessionID: "s2"},
			{sessionID: "s3"},
		},
		selectedIndex: 0,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.sessionPicker.selectedIndex != 1 {
		t.Fatalf("selectedIndex = %d, want 1 after down", model.sessionPicker.selectedIndex)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.sessionPicker.selectedIndex != 2 {
		t.Fatalf("selectedIndex = %d, want 2 after second down", model.sessionPicker.selectedIndex)
	}

	// 不超出边界
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.sessionPicker.selectedIndex != 2 {
		t.Fatalf("selectedIndex = %d, want 2 at boundary", model.sessionPicker.selectedIndex)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if model.sessionPicker.selectedIndex != 1 {
		t.Fatalf("selectedIndex = %d, want 1 after up", model.sessionPicker.selectedIndex)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// @ 文件补全触发边界行为测试
// ─────────────────────────────────────────────────────────────────────────────

// TestDetectAtTrigger_行首触发 验证输入以 @ 开头时能够触发。
func TestDetectAtTrigger_行首触发(t *testing.T) {
	idx, query := detectAtTrigger("@readme")
	if idx != 0 {
		t.Errorf("atByteIndex = %d, want 0", idx)
	}
	if query != "readme" {
		t.Errorf("query = %q, want readme", query)
	}
}

// TestDetectAtTrigger_空格后触发 验证 @ 前有空格时能够触发（如"可以 @文件"）。
func TestDetectAtTrigger_空格后触发(t *testing.T) {
	input := "可以 @readme"
	idx, query := detectAtTrigger(input)
	if idx < 0 {
		t.Fatalf("atByteIndex = -1, want >=0 for %q", input)
	}
	if query != "readme" {
		t.Errorf("query = %q, want readme", query)
	}
	// @ 之前的文本应保持不变
	before := input[:idx]
	if before != "可以 " {
		t.Errorf("before = %q, want '可以 '", before)
	}
}

// TestDetectAtTrigger_非词边界不触发 验证 @ 紧跟非空白字符时不触发（如"可以@文件"）。
func TestDetectAtTrigger_非词边界不触发(t *testing.T) {
	idx, _ := detectAtTrigger("可以@readme")
	if idx >= 0 {
		t.Errorf("atByteIndex = %d, want -1 for inline @ (not word boundary)", idx)
	}
}

// TestDetectAtTrigger_末尾空白不触发 验证末尾是空白时不触发（词已结束）。
func TestDetectAtTrigger_末尾空白不触发(t *testing.T) {
	idx, _ := detectAtTrigger("@readme ")
	if idx >= 0 {
		t.Errorf("atByteIndex = %d, want -1 when trailing space ends the word", idx)
	}
}

// TestDetectAtTrigger_仅At号 验证只有 @ 时 query 为空字符串。
func TestDetectAtTrigger_仅At号(t *testing.T) {
	idx, query := detectAtTrigger("@")
	if idx != 0 {
		t.Errorf("atByteIndex = %d, want 0", idx)
	}
	if query != "" {
		t.Errorf("query = %q, want empty", query)
	}
}

// TestDetectAtTrigger_路径前缀波浪号 验证 @~ 形式的 query。
func TestDetectAtTrigger_路径前缀波浪号(t *testing.T) {
	idx, query := detectAtTrigger("@~/docs")
	if idx != 0 {
		t.Errorf("atByteIndex = %d, want 0", idx)
	}
	if query != "~/docs" {
		t.Errorf("query = %q, want ~/docs", query)
	}
}

// TestDetectAtTrigger_路径前缀根目录 验证 @/ 形式的 query。
func TestDetectAtTrigger_路径前缀根目录(t *testing.T) {
	idx, query := detectAtTrigger("@/etc/hosts")
	if idx != 0 {
		t.Errorf("atByteIndex = %d, want 0", idx)
	}
	if query != "/etc/hosts" {
		t.Errorf("query = %q, want /etc/hosts", query)
	}
}

// TestResolvePathParts 验证 resolvePathParts 的路径分解逻辑。
func TestResolvePathParts(t *testing.T) {
	tests := []struct {
		base, rest, wantDir, wantPrefix string
	}{
		{"/home/user", "", "/home/user", ""},
		{"/home/user", "foo", "/home/user", "foo"},
		{"/home/user", "dir/", "/home/user/dir", ""},
		{"/home/user", "dir/foo", "/home/user/dir", "foo"},
		{"/home/user", "a/b/c", "/home/user/a/b", "c"},
	}
	for _, tt := range tests {
		dir, prefix := resolvePathParts(tt.base, tt.rest)
		if dir != tt.wantDir || prefix != tt.wantPrefix {
			t.Errorf("resolvePathParts(%q, %q) = (%q, %q), want (%q, %q)",
				tt.base, tt.rest, dir, prefix, tt.wantDir, tt.wantPrefix)
		}
	}
}

// TestAtCompletion_输入时不阻断输入框 验证补全弹窗开启时字符键仍然写入输入框。
func TestAtCompletion_输入时不阻断输入框(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	// 设置初始 @ 触发状态
	model.completion = &completion{
		kind:          completionKindFile,
		filteredItems: []string{"readme.md", "go.mod"},
		loading:       false,
	}

	// 发送普通字符键（非导航键），应该透传给输入框
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = next.(appModel)

	// 输入框应该包含该字符
	if !strings.Contains(model.input.Value(), "r") {
		t.Errorf("input value = %q, want to contain 'r' after typing while completion open", model.input.Value())
	}
}

// TestAtCompletion_Tab文件时行为同Enter 验证 Tab 选中文件时加空格并关闭候选框。
func TestAtCompletion_Tab文件时行为同Enter(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("请引用 @re")
	model.completion = &completion{
		kind:          completionKindFile,
		atByteIndex:   len("请引用 "),
		query:         "re",
		allItems:      []string{"readme.md", "readme.txt"},
		filteredItems: []string{"readme.md", "readme.txt"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)

	// 文件：Tab 加尾部空格，与 Enter 一致
	if got := model.input.Value(); got != "请引用 @readme.md " {
		t.Errorf("input value = %q, want '请引用 @readme.md ' (trailing space for file)", got)
	}
	if model.completion != nil {
		t.Errorf("completion should be nil after tab on file, got %#v", model.completion)
	}
}

// TestAtCompletion_Tab目录时候选框保持 验证 Tab 选中目录时不加空格、候选框保持开启。
func TestAtCompletion_Tab目录时候选框保持(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("@sr")
	model.completion = &completion{
		kind:          completionKindFile,
		atByteIndex:   0,
		query:         "sr",
		allItems:      []string{"src/", "scripts/"},
		filteredItems: []string{"src/"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)

	// 目录：无尾部空格
	got := model.input.Value()
	if strings.HasSuffix(got, " ") {
		t.Errorf("input value = %q, want no trailing space for directory tab", got)
	}
	if !strings.Contains(got, "@src/") {
		t.Errorf("input value = %q, want to contain @src/", got)
	}
	// 候选框应保持开启
	if model.completion == nil {
		t.Errorf("completion should remain open after tab on directory, got nil")
	}
}

// TestAtCompletion_Enter加空格结束引用 验证 Enter 确认时在路径后追加空格以结束引用。
func TestAtCompletion_Enter加空格结束引用(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("请引用 @re")
	model.completion = &completion{
		kind:          completionKindFile,
		atByteIndex:   len("请引用 "),
		query:         "re",
		filteredItems: []string{"readme.md"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	got := model.input.Value()
	// Enter：追加空格，光标在路径 + 空格之后
	if got != "请引用 @readme.md " {
		t.Errorf("input value = %q, want '请引用 @readme.md ' (trailing space after enter)", got)
	}
	if model.completion != nil {
		t.Errorf("completion should be nil after enter, got %#v", model.completion)
	}
}

// TestAtCompletion_Esc清除不退出 验证 esc 只清除补全弹窗，不退出 TUI。
func TestAtCompletion_Esc清除不退出(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = &completion{
		kind:          completionKindFile,
		filteredItems: []string{"readme.md"},
		loading:       false,
	}

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)

	if model.completion != nil {
		t.Errorf("completion should be nil after esc, got %#v", model.completion)
	}
	// cmd 不应是 tea.Quit
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Errorf("esc while completion open should not quit TUI")
		}
	}
}

// TestFilterByPrefix 验证大小写不敏感的前缀过滤。
func TestFilterByPrefix(t *testing.T) {
	items := []string{"README.md", "readme.txt", "go.mod", "go.sum", "main.go"}
	got := filterByPrefix(items, "read")
	if len(got) != 2 {
		t.Errorf("filterByPrefix = %v, want [README.md readme.txt]", got)
	}
	// 空前缀返回全部
	all := filterByPrefix(items, "")
	if len(all) != len(items) {
		t.Errorf("filterByPrefix with empty prefix = %d items, want %d", len(all), len(items))
	}
}

// TestCtrlC_首次清空输入不退出 验证第一次按 Ctrl+C 清空输入框且不退出。
func TestCtrlC_首次清空输入不退出(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("some input text")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)

	if model.input.Value() != "" {
		t.Errorf("input value = %q, want empty after first ctrl+c", model.input.Value())
	}
	// 不应触发退出
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Errorf("first ctrl+c should not quit")
		}
	}
	// lastCtrlCAt 应已记录
	if model.lastCtrlCAt.IsZero() {
		t.Errorf("lastCtrlCAt should be set after first ctrl+c")
	}
}

// TestCtrlC_双击退出 验证 1 秒内连按两次 Ctrl+C 触发退出。
func TestCtrlC_双击退出(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	// 模拟第一次按（设置 lastCtrlCAt 为刚才）
	model.lastCtrlCAt = time.Now()

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	// 应触发退出命令
	if cmd == nil {
		t.Fatalf("second ctrl+c within 1s should return a quit cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("second ctrl+c within 1s should quit, got %T", msg)
	}
}

// TestCtrlC_超时后不退出 验证超过 1 秒后再按 Ctrl+C 只清空输入、不退出。
func TestCtrlC_超时后不退出(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("hello")
	// 设置 lastCtrlCAt 为 2 秒前（超出 1 秒窗口）
	model.lastCtrlCAt = time.Now().Add(-2 * time.Second)

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)

	if model.input.Value() != "" {
		t.Errorf("input = %q, want empty", model.input.Value())
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Errorf("ctrl+c after timeout should not quit")
		}
	}
}

func TestRelayout_SidebarWidthIs30Percent(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 120
	model.height = 40
	model.ready = true
	model.relayout()
	if model.sidebarWidth != 36 {
		t.Errorf("sidebarWidth = %d, want 36 (30%% of 120)", model.sidebarWidth)
	}
	if model.viewport.Width < 70 {
		t.Errorf("viewport.Width = %d, want ≥70 (left column inner)", model.viewport.Width)
	}
}

func TestRenderToolEntryBody_BlockquoteContainsVerticalBar(t *testing.T) {
	body := "Read README.md\nfile content here"
	result := renderToolEntryBody(body, 60, 1.0)
	if !strings.Contains(result, "│") {
		t.Errorf("renderToolEntryBody = %q, want │ vertical bar (blockquote style)", result)
	}
	if !strings.Contains(result, "Read README.md") {
		t.Errorf("renderToolEntryBody = %q, want summary in output", result)
	}
}

func TestRenderToolEntryBody_PrefixesDetailLinesWithArrow(t *testing.T) {
	body := "Bash ls .\nfile1.go\nfile2.go"
	result := renderToolEntryBody(body, 60, 1.0)
	if !strings.Contains(result, "> ") {
		t.Errorf("renderToolEntryBody = %q, want > prefix on detail lines", result)
	}
}

// TestCtrlC_清空时关闭候选框 验证 Ctrl+C 同时关闭文件补全候选框。
func TestCtrlC_清空时关闭候选框(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("@readme")
	model.completion = &completion{
		kind:          completionKindFile,
		filteredItems: []string{"readme.md"},
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)

	if model.completion != nil {
		t.Errorf("completion should be nil after ctrl+c, got %#v", model.completion)
	}
	if model.input.Value() != "" {
		t.Errorf("input = %q, want empty", model.input.Value())
	}
}
