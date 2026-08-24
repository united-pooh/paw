// 本文件覆盖 Bubble Tea TUI 的提交、布局、历史、模型向导、Markdown 和光标锚点行为。
package bubble

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"paw/internal/loop"
	"paw/internal/message"
	modelcfg "paw/internal/model"
	"paw/internal/settings"
	"paw/internal/skill"
	taskpkg "paw/internal/task"
	"paw/internal/theme"
	"paw/internal/ui"
)

// fakeRunner 记录测试中的提交输入，并模拟对话 runner。
type fakeRunner struct {
	inputs           []string
	resetCalls       int
	loadHistoryCalls []string
	loadHistoryMsgs  []message.Message
	loadHistoryErr   error
	err              error
	stats            loop.ContextStats
	lastDraft        string
	lastLimit        int
	supplements      []string
	contextLimits    []int
	contextModes     []string
	yoloModes        []bool
	compactFocus     string
	compactResult    loop.ContextCompactionResult
	compactErr       error
	newSessionCalls  int
	newSessionErr    error
}

type ctrlCRunner struct {
	fakeRunner
	runCtx          context.Context
	activeTool      bool
	cancelToolCalls int
	cancelTurnCalls int
}

func (r *ctrlCRunner) CancelCurrentTool() bool {
	r.cancelToolCalls++
	return r.activeTool
}

func (r *ctrlCRunner) CancelTurn() {
	r.cancelTurnCalls++
}

func (r *ctrlCRunner) RunTurn(ctx context.Context, input string) (message.Message, error) {
	r.runCtx = ctx
	<-ctx.Done()
	return message.Message{}, ctx.Err()
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
func (r *fakeRunner) LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	r.loadHistoryCalls = append(r.loadHistoryCalls, sessionID)
	return append([]message.Message(nil), r.loadHistoryMsgs...), r.loadHistoryErr
}

func (r *fakeRunner) NewSession(context.Context) (string, loop.SessionLoadResult, error) {
	r.newSessionCalls++
	if r.newSessionErr != nil {
		return "", loop.SessionLoadResult{}, r.newSessionErr
	}
	return fmt.Sprintf("new-session-%d", r.newSessionCalls), loop.SessionLoadResult{}, nil
}

func (r *fakeRunner) SubmitSupplement(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	r.supplements = append(r.supplements, input)
	return true
}

func (r *fakeRunner) PendingSupplementCount() int {
	return len(r.supplements)
}

func (r *fakeRunner) SetContextLimitTokens(limit int) {
	r.contextLimits = append(r.contextLimits, limit)
}

func (r *fakeRunner) SetContextMode(mode string) {
	r.contextModes = append(r.contextModes, mode)
}

func (r *fakeRunner) SetYoloMode(enabled bool) (bool, error) {
	r.yoloModes = append(r.yoloModes, enabled)
	return enabled, nil
}

func (r *fakeRunner) CompactContext(ctx context.Context, focus string) (loop.ContextCompactionResult, error) {
	r.compactFocus = focus
	return r.compactResult, r.compactErr
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

// UpdateRuntime 只改内存、不落盘：与真实 Controller 行为一致，saved 保持
// 为空可用于断言「动态开关未写配置文件」。
func (c *fakeSettingsController) UpdateRuntime(cfg settings.Config) {
	c.current = cfg
}

type fakeTaskController struct {
	runResult      taskpkg.Result
	runErr         error
	launchTask     taskpkg.TaskSnapshot
	launchErr      error
	tasks          []taskpkg.TaskSnapshot
	runRequests    []taskpkg.Request
	launchRequests []taskpkg.Request
}

func (c *fakeTaskController) Run(ctx context.Context, req taskpkg.Request) (taskpkg.Result, error) {
	c.runRequests = append(c.runRequests, req)
	if c.runErr != nil {
		return taskpkg.Result{}, c.runErr
	}
	return c.runResult, nil
}

func (c *fakeTaskController) Launch(ctx context.Context, req taskpkg.Request) (taskpkg.TaskSnapshot, error) {
	c.launchRequests = append(c.launchRequests, req)
	if c.launchErr != nil {
		return taskpkg.TaskSnapshot{}, c.launchErr
	}
	task := c.launchTask
	if task.ID == "" {
		task.ID = "task-1"
	}
	if task.SessionID == "" {
		task.SessionID = task.ID
	}
	if task.ParentSessionID == "" {
		task.ParentSessionID = req.ParentSessionID
	}
	if task.TranscriptPath == "" {
		task.TranscriptPath = "/tmp/" + task.ID + ".jsonl"
	}
	if task.Status == "" {
		task.Status = taskpkg.TaskRunning
	}
	if task.ContextMode == "" {
		task.ContextMode = req.ContextMode
	}
	if task.RunMode == "" {
		task.RunMode = settings.RunModeBackground
	}
	task.Prompt = req.Prompt
	task.Description = req.Description
	c.tasks = append([]taskpkg.TaskSnapshot(nil), append(c.tasks, task)...)
	return task, nil
}

func (c *fakeTaskController) ListTasks() []taskpkg.TaskSnapshot {
	return append([]taskpkg.TaskSnapshot(nil), c.tasks...)
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

// newTestModel 创建带默认配置 profile 的测试用 appModel。
func newTestModel(runner Runner) appModel {
	return newModel(context.Background(), runner, "session-1", &fakeModelConfigController{
		current: modelcfg.Config{
			ProfileID:     "gateway",
			ProfileName:   "Gateway",
			Provider:      "gateway",
			Transport:     "openai-compatible",
			APIBaseURL:    "http://127.0.0.1:8317/v1",
			APIPath:       "/chat/completions",
			APIKey:        "custom-secret",
			APIKeyEnvName: "TEST_API_KEY",
			Model:         "model-a",
			Models:        []string{"model-a"},
			Profiles: []modelcfg.Profile{
				{
					ID:            "gateway",
					Name:          "Gateway",
					Provider:      "gateway",
					Transport:     "openai-compatible",
					APIBaseURL:    "http://127.0.0.1:8317/v1",
					APIPath:       "/chat/completions",
					APIKey:        "custom-secret",
					APIKeyEnvName: "TEST_API_KEY",
					Model:         "model-a",
					Models:        []string{"model-a"},
				},
				{
					ID:         "backup",
					Name:       "Backup",
					Provider:   "backup",
					Transport:  "openai-compatible",
					APIBaseURL: "https://backup.example/v1",
					APIPath:    "/chat/completions",
					Model:      "model-b",
					Models:     []string{"model-b"},
				},
			},
			Timeout: time.Minute,
		},
	}, nil, nil, nil, newTerminalCursorAnchor())
}

func writeBubbleTestSkill(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, skill.SkillFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertRenderedLineWidthsAtMost(t *testing.T, rendered string, width int) {
	t.Helper()
	for i, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q\n%s", i+1, got, width, line, ansi.Strip(rendered))
		}
	}
}

func maxRenderedLineWidth(rendered string) int {
	maxWidth := 0
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		maxWidth = maxInt(maxWidth, lipgloss.Width(line))
	}
	return maxWidth
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
	if !handled || cmd == nil {
		t.Fatalf("/clear handled/cmd = %v/%v, want async new-session command", handled, cmd)
	}
	msg, ok := cmd().(sessionRestoredMsg)
	if !ok {
		t.Fatalf("/clear cmd returned unexpected message")
	}
	next, _ := model.Update(msg)
	model = next.(appModel)
	if runner.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, /clear must not reuse the current session", runner.resetCalls)
	}
	if runner.newSessionCalls != 1 || model.sessionID != "new-session-1" {
		t.Fatalf("newSessionCalls/sessionID = %d/%q", runner.newSessionCalls, model.sessionID)
	}
	if len(model.transcript) != 1 || !strings.Contains(model.transcript[0].body, "new-session-1") {
		t.Fatalf("transcript = %#v, want fresh-session status only", model.transcript)
	}
}

func TestClearAndNewShareFreshSessionBehavior(t *testing.T) {
	for _, command := range []string{"/clear", "/new"} {
		t.Run(command, func(t *testing.T) {
			runner := &fakeRunner{}
			model := newTestModel(runner)
			model.transcript = []transcriptEntry{{kind: entryUser, body: "dirty history"}}
			model.inputHistory = []inputDraft{{Text: "dirty prompt"}}

			handled, cmd := model.handleCommand(command)
			if !handled || cmd == nil {
				t.Fatalf("%s handled/cmd = %v/%v", command, handled, cmd)
			}
			msg, ok := cmd().(sessionRestoredMsg)
			if !ok || msg.source != sessionRestoreNew || msg.err != nil {
				t.Fatalf("%s result = %#v, want successful new-session restore", command, msg)
			}
			next, _ := model.Update(msg)
			model = next.(appModel)
			if model.sessionID != "new-session-1" || runner.newSessionCalls != 1 {
				t.Fatalf("%s session = %q calls=%d", command, model.sessionID, runner.newSessionCalls)
			}
			if len(model.inputHistory) != 0 {
				t.Fatalf("%s retained input history: %#v", command, model.inputHistory)
			}
			for _, entry := range model.transcript {
				if strings.Contains(entry.body, "dirty history") {
					t.Fatalf("%s retained dirty transcript: %#v", command, model.transcript)
				}
			}
		})
	}
}

func TestCompactCommandPassesFocusAndShowsMessageCounts(t *testing.T) {
	runner := &fakeRunner{compactResult: loop.ContextCompactionResult{
		BeforeMessages: 18,
		AfterMessages:  7,
		FoldedMessages: 12,
		ArchivePaths:   []string{".paw/sessions/session-1/compactions/fold.jsonl"},
		Mechanical:     true,
	}}
	model := newTestModel(runner)
	handled, cmd := model.handleCommand("/compact prioritize parser failures")
	if !handled || cmd == nil {
		t.Fatalf("/compact handled/cmd = %v/%v", handled, cmd)
	}
	if !model.isModelWorkRunning() {
		t.Fatal("/compact did not mark model work running")
	}
	msg := cmd()
	finished, ok := msg.(contextCompactionFinishedMsg)
	if !ok {
		t.Fatalf("cmd() = %#v", msg)
	}
	if runner.compactFocus != "prioritize parser failures" {
		t.Fatalf("focus = %q", runner.compactFocus)
	}
	next, _ := model.Update(finished)
	model = next.(appModel)
	if model.isModelWorkRunning() {
		t.Fatal("compaction completion left model work running")
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{"compacted 12 messages", "18 → 7", "full journal preserved", "archive:", "fold.jsonl", "folded mechanically"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
	}
}

func TestNewModelUsesConfiguredModelContextLimit(t *testing.T) {
	runner := &fakeRunner{}
	controller := &fakeModelConfigController{current: modelcfg.Config{
		Model:                   "model-b",
		ModelContextLimitTokens: map[string]int{"model-b": 131072},
	}}
	_ = newModel(context.Background(), runner, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	if len(runner.contextLimits) == 0 || runner.contextLimits[len(runner.contextLimits)-1] != 131072 {
		t.Fatalf("context limits = %#v", runner.contextLimits)
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
		"Commands\n",
		"/help",
		"show available commands",
		"/model [status|<profile>|<model>]",
		"open the model switcher",
		"/export [filename]",
		"export the current conversation",
		"/setting",
		"settings: wizard, or runtime toggle (translate on|off)",
		"/task [--fork|--empty] [--background|--sync] <prompt>",
		"launch a task",
		"/streamma <prompt>",
		"run a prompt through StreamMA taskController",
		"/streamma-trace <prompt>",
		"run StreamMA with live event trace",
		"/tasks",
		"show background task tasks",
		"/skills",
		"show discovered skills",
		"/token-tracer (aliases: /tt)",
		"show the live Token Tracer dashboard URL",
		"/exit (aliases: /quit, exit, quit)",
		"quit the TUI",
		"Shortcuts\n",
		"!             toggle terminal mode",
		"!<command>     run a shell command once",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("help body = %q, want %q", body, want)
		}
	}
	if strings.Contains(body, " - ") {
		t.Fatalf("help body still uses noisy dash separators: %q", body)
	}
}

func TestSkillsCommandListsDiscoveredSkills(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "design", `---
description: Design discipline
---
Design body.`)
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})

	handled, cmd := model.handleCommand("/skills")
	if !handled || cmd != nil {
		t.Fatalf("/skills handled/cmd = %v/%v", handled, cmd)
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{"Skills:", "design - Design discipline", path} {
		if !strings.Contains(body, want) {
			t.Fatalf("skills body = %q, want %q", body, want)
		}
	}
}

func TestSlashSkillSubmitRewritesToSkillReference(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "multi-agent-pipeline", "# Pipeline\n")
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("/multi-agent-pipeline implement staged fix")

	updatedModel, cmd := model.handleSubmit()
	updated := updatedModel.(appModel)
	if cmd == nil {
		t.Fatalf("handleSubmit() cmd is nil")
	}
	if !updated.running {
		t.Fatalf("updated.running = false, want true")
	}
	msg := cmd()
	if _, ok := msg.(turnFinishedMsg); !ok {
		t.Fatalf("cmd() = %#v, want turnFinishedMsg", msg)
	}
	want := "[$multi-agent-pipeline](" + path + ") implement staged fix"
	if len(runner.inputs) != 1 || runner.inputs[0] != want {
		t.Fatalf("runner.inputs = %#v, want %q", runner.inputs, want)
	}
}

func TestStreamMACommandStartsTurn(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("/streamma design the runtime")

	updatedModel, cmd := model.handleSubmit()
	updated := updatedModel.(appModel)
	if cmd == nil {
		t.Fatalf("/streamma command returned nil cmd")
	}
	if !updated.running {
		t.Fatalf("updated.running = false, want true")
	}
	last := updated.transcript[len(updated.transcript)-1]
	if last.kind != entryUser || last.body != "/streamma design the runtime" {
		t.Fatalf("last transcript entry = %#v", last)
	}
	msg := cmd()
	if _, ok := msg.(turnFinishedMsg); !ok {
		t.Fatalf("cmd() = %#v, want turnFinishedMsg", msg)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != "/streamma design the runtime" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
	if len(updated.inputHistory) != 1 || updated.inputHistory[0].Text != "/streamma design the runtime" {
		t.Fatalf("inputHistory = %#v", updated.inputHistory)
	}
}

func TestStreamMATraceCommandStartsTurn(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("/streamma-trace design the runtime")

	updatedModel, cmd := model.handleSubmit()
	updated := updatedModel.(appModel)
	if cmd == nil {
		t.Fatalf("/streamma-trace command returned nil cmd")
	}
	msg := cmd()
	if _, ok := msg.(turnFinishedMsg); !ok {
		t.Fatalf("cmd() = %#v, want turnFinishedMsg", msg)
	}
	if len(runner.inputs) != 1 || runner.inputs[0] != "/streamma-trace design the runtime" {
		t.Fatalf("runner.inputs = %#v", runner.inputs)
	}
	if len(updated.inputHistory) != 1 || updated.inputHistory[0].Text != "/streamma-trace design the runtime" {
		t.Fatalf("inputHistory = %#v", updated.inputHistory)
	}
}

func TestStreamMACommandWithoutPromptShowsUsage(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)

	handled, cmd := model.handleCommand("/streamma")
	if !handled || cmd != nil {
		t.Fatalf("/streamma handled/cmd = %v/%v, want handled with nil cmd", handled, cmd)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner.inputs = %#v, want empty", runner.inputs)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "streamma" || !strings.Contains(last.body, "usage: /streamma <prompt>") {
		t.Fatalf("last transcript entry = %#v", last)
	}
}

func TestStreamMATraceCommandWithoutPromptShowsUsage(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)

	handled, cmd := model.handleCommand("/streamma-trace")
	if !handled || cmd != nil {
		t.Fatalf("/streamma-trace handled/cmd = %v/%v, want handled with nil cmd", handled, cmd)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner.inputs = %#v, want empty", runner.inputs)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "streamma-trace" || !strings.Contains(last.body, "usage: /streamma-trace <prompt>") {
		t.Fatalf("last transcript entry = %#v", last)
	}
}

// TestModelCommandVariantsKeepWizardAndShortcuts verifies wizard and direct subcommands coexist.
func TestModelCommandVariantsKeepWizardAndShortcuts(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			ProfileID:     "gateway",
			ProfileName:   "Gateway",
			Provider:      "gateway",
			Transport:     "openai-compatible",
			APIBaseURL:    "http://127.0.0.1:8317/v1",
			APIPath:       "/chat/completions",
			APIKey:        "custom-secret",
			APIKeyEnvName: "TEST_API_KEY",
			Model:         "model-a",
			Models:        []string{"model-a"},
			Profiles: []modelcfg.Profile{
				{ID: "gateway", Name: "Gateway", Provider: "gateway", Transport: "openai-compatible", APIBaseURL: "http://127.0.0.1:8317/v1", APIPath: "/chat/completions", APIKey: "custom-secret", Model: "model-a", Models: []string{"model-a"}},
				{ID: "backup", Name: "Backup", Provider: "backup", Transport: "openai-compatible", APIBaseURL: "https://backup.example/v1", APIPath: "/chat/completions", APIKey: "backup-secret", Model: "model-b", Models: []string{"model-b"}},
			},
			Timeout: time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/model status")
	if !handled || cmd != nil {
		t.Fatalf("/model status handled/cmd = %v/%v", handled, cmd)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "provider=gateway") {
		t.Fatalf("status body = %q", got)
	}

	handled, cmd = model.handleCommand("/model gateway")
	if !handled || cmd != nil {
		t.Fatalf("/model gateway handled/cmd = %v/%v", handled, cmd)
	}
	if len(controller.applied) != 1 || controller.applied[0].Provider != "gateway" {
		t.Fatalf("gateway applied configs = %#v", controller.applied)
	}

	handled, cmd = model.handleCommand("/model backup")
	if !handled || cmd != nil {
		t.Fatalf("/model backup handled/cmd = %v/%v", handled, cmd)
	}
	if len(controller.applied) != 2 || controller.applied[1].Provider != "backup" {
		t.Fatalf("applied configs = %#v", controller.applied)
	}

	switchEntry := model.transcript[len(model.transcript)-1]
	if !isModelCardBlock(switchEntry.body) {
		t.Fatalf("switch entry body not a model card block: %q", switchEntry.body)
	}
	switchInfo, ok := parseModelCardBlock(switchEntry.body)
	if !ok || switchInfo.Model != "model-b" || switchInfo.Provider != "backup" {
		t.Fatalf("switch card info = %#v, ok=%v", switchInfo, ok)
	}
}

// TestModelCommandSwitchesConfiguredModel verifies that one endpoint can expose
// multiple persisted model names and switch the active one without changing
// the provider connection settings.
func TestModelCommandSwitchesConfiguredModel(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      "gateway",
			APIBaseURL:    "https://example.test/v1",
			APIPath:       "/chat/completions",
			APIKey:        "custom-secret",
			APIKeyEnvName: "TEST_API_KEY",
			Model:         "gpt-5.6-sol",
			Models:        []string{"gpt-5.6-sol", "gpt-5.6-luna"},
			Timeout:       time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/model gpt-5.6-luna")
	if !handled || cmd != nil {
		t.Fatalf("/model gpt-5.6-luna handled/cmd = %v/%v", handled, cmd)
	}
	if len(controller.applied) != 1 {
		t.Fatalf("applied configs = %#v", controller.applied)
	}
	got := controller.applied[0]
	if got.Model != "gpt-5.6-luna" || got.APIBaseURL != "https://example.test/v1" || !equalStrings(got.Models, []string{"gpt-5.6-sol", "gpt-5.6-luna"}) {
		t.Fatalf("applied config = %#v", got)
	}
}

func TestModelWizardSelectsConfiguredModelUnderProvider(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      "gateway",
			APIBaseURL:    "https://example.test/v1",
			APIPath:       "/chat/completions",
			APIKey:        "custom-secret",
			APIKeyEnvName: "TEST_API_KEY",
			Model:         "gpt-5.6-sol",
			Models:        []string{"gpt-5.6-sol", "gpt-5.6-luna"},
			Timeout:       time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.modelWizard.step != modelWizardModel || !equalStrings(model.modelWizard.modelOptions, []string{"gpt-5.6-sol", "gpt-5.6-luna"}) {
		t.Fatalf("wizard model step = %#v", model.modelWizard)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if got := model.modelWizard.selectedModelName(); got != "gpt-5.6-luna" {
		t.Fatalf("selected model = %q, want gpt-5.6-luna", got)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard != nil {
		t.Fatalf("model wizard remains open: %#v", model.modelWizard)
	}
	if len(controller.applied) != 1 || controller.applied[0].Model != "gpt-5.6-luna" {
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
	exports, err := filepath.Glob(filepath.Join(root, ".paw", "exports", "conversation-*.txt"))
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

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	// translate → confirm，直接穿过到 confirm 并保存。
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.settingWizard != nil {
		t.Fatalf("settingWizard = %#v, want nil after save", model.settingWizard)
	}
	if len(settingsController.saved) != 1 {
		t.Fatalf("saved settings = %#v", settingsController.saved)
	}
	got := settingsController.saved[0]
	if got.Task.DefaultContextMode != settings.ContextModeEmpty ||
		got.Task.DefaultRunMode != settings.RunModeBackground ||
		got.UI.ContextMeterLocation != settings.MeterLocationInputAbove ||
		got.UI.TranslateOnDoubleClick {
		t.Fatalf("saved config = %#v", got)
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{
		"task.context=empty",
		"task.run=background",
		"ui.translate_on_double_click=off",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings summary = %q, want %q", body, want)
		}
	}
}

// TestSettingWizardTranslateSessionOnly 验证 /setting 面板里也能做「动态开关」：
// Translate 步骤选中 on (session only) 后确认，只更新内存（currentSettings
// 立即生效）、不写配置文件（controller.saved 保持为空），摘要标注 apply mode。
func TestSettingWizardTranslateSessionOnly(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/setting")
	if !handled || cmd != nil {
		t.Fatalf("/setting handled/cmd = %v/%v", handled, cmd)
	}
	if model.settingWizard == nil {
		t.Fatal("/setting should open wizard")
	}

	// context → runmode → translate。
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.settingWizard.step != settingWizardTranslate {
		t.Fatalf("wizard step = %v, want translate", model.settingWizard.step)
	}

	// 选项 [on, off, on (session only)]：默认配置 off → 初始选中索引 1，
	// 向下一次选中 session-only（索引 2）。
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if got := model.settingWizard.selected[settingWizardTranslate]; got != 2 {
		t.Fatalf("translate selected index = %d, want 2 (on session only)", got)
	}

	// translate → confirm（sessionOnly 仍由 translate 选项保持）。
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.settingWizard.step != settingWizardConfirm || !model.settingWizard.sessionOnly {
		t.Fatalf("confirm step = step:%v sessionOnly:%v, want flagged session-only", model.settingWizard.step, model.settingWizard.sessionOnly)
	}
	if confirm := model.renderSettingConfirmStep(); !strings.Contains(confirm, "session only") {
		t.Fatalf("confirm step = %q, want session-only hint", confirm)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.settingWizard != nil {
		t.Fatalf("settingWizard = %#v, want nil after apply", model.settingWizard)
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("session-only apply wrote to disk: saved = %#v", settingsController.saved)
	}
	if !model.currentSettings().UI.TranslateOnDoubleClick {
		t.Fatal("session-only apply did not enable the runtime setting")
	}
	body := model.transcript[len(model.transcript)-1].body
	for _, want := range []string{
		"ui.translate_on_double_click=on",
		"session only",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings summary = %q, want %q", body, want)
		}
	}
}

// TestSettingCommandTranslateRuntimeToggle 验证 /setting translate on|off 是
// 运行期动态开关：只更新内存配置（currentSettings 立即生效）、不写配置文件
// （controller.saved 保持为空），并给出用法反馈。
func TestSettingCommandTranslateRuntimeToggle(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())

	if model.currentSettings().UI.TranslateOnDoubleClick {
		t.Fatal("default translate setting should be off")
	}

	handled, cmd := model.handleCommand("/setting translate on")
	if !handled || cmd != nil {
		t.Fatalf("/setting translate on handled/cmd = %v/%v", handled, cmd)
	}
	if !model.currentSettings().UI.TranslateOnDoubleClick {
		t.Fatal("/setting translate on did not enable the runtime setting")
	}
	if model.settingWizard != nil {
		t.Fatal("/setting translate on must not open the wizard")
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("runtime toggle wrote to disk: saved = %#v", settingsController.saved)
	}
	last := model.transcript[len(model.transcript)-1]
	if last.kind != entrySystem || last.title != "setting" || !strings.Contains(last.body, "translate_on_double_click=on") {
		t.Fatalf("feedback entry = %#v, want translate_on_double_click=on system entry", last)
	}

	handled, _ = model.handleCommand("/setting translate off")
	if !handled {
		t.Fatal("/setting translate off not handled")
	}
	if model.currentSettings().UI.TranslateOnDoubleClick {
		t.Fatal("/setting translate off did not disable the runtime setting")
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("runtime toggle wrote to disk: saved = %#v", settingsController.saved)
	}

	// 非法参数：给出用法提示且不改变当前设置。
	handled, _ = model.handleCommand("/setting translate bogus")
	if !handled {
		t.Fatal("/setting translate bogus not handled")
	}
	if model.currentSettings().UI.TranslateOnDoubleClick {
		t.Fatal("invalid translate value changed the runtime setting")
	}
	if last := model.transcript[len(model.transcript)-1]; last.kind != entryError || !strings.Contains(last.body, "usage: /setting translate on|off") {
		t.Fatalf("invalid value feedback = %#v, want usage error", last)
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("runtime toggle wrote to disk: saved = %#v", settingsController.saved)
	}

	// 无参数仍打开持久化向导（回归）。
	handled, _ = model.handleCommand("/setting")
	if !handled || model.settingWizard == nil {
		t.Fatalf("/setting should open the wizard (handled=%v wizard=%v)", handled, model.settingWizard)
	}
}

// TestSettingCommandTranslateRuntimeAffectsDoubleClick 验证动态开关开启后
// 双击立即触发翻译（完整链路），关闭后双击只选词。
func TestSettingCommandTranslateRuntimeAffectsDoubleClick(t *testing.T) {
	oldRun := runTranslateRequest
	var gotWord string
	runTranslateRequest = func(ctx context.Context, cfg modelcfg.Config, systemPrompt, word string) (string, error) {
		gotWord = word
		return `{"word":"world","phonetic":"/wɜːld/","pos":"n.","translation":"世界"}`, nil
	}
	defer func() {
		runTranslateRequest = oldRun
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	settingsController := &fakeSettingsController{current: settings.DefaultConfig()}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	// 关闭时双击：只选词，不发请求。
	model, cmd := doubleClickWord(model, "world")
	if cmd != nil || model.translatePanel != nil {
		t.Fatalf("translate disabled: cmd=%v panel=%+v, want no request", cmd, model.translatePanel)
	}

	// /setting translate on 动态开启（不落盘）。
	handled, _ := model.handleCommand("/setting translate on")
	if !handled {
		t.Fatal("/setting translate on not handled")
	}
	if len(settingsController.saved) != 0 {
		t.Fatalf("runtime toggle wrote to disk: saved = %#v", settingsController.saved)
	}

	// 模拟两次双击之间的真实间隔（重置点击跟踪），否则紧挨着的第二次
	// 按下会被判定为「三击」而行选择，不会触发翻译。
	model.lastClickAt = time.Time{}
	model.lastClickPoint = selectionPoint{}

	// 开启后双击同一词：立即发起翻译请求并弹出 loading 面板。
	model, cmd = doubleClickWord(model, "world")
	if cmd == nil {
		t.Fatal("double-click after runtime enable returned no request command")
	}
	if model.translatePanel == nil || model.translatePanel.state != translatePanelLoading {
		t.Fatalf("translate panel = %+v, want loading", model.translatePanel)
	}
	msg := cmd()
	result, ok := msg.(translateResultMsg)
	if !ok {
		t.Fatalf("cmd() = %#v, want translateResultMsg", msg)
	}
	if gotWord != "world" {
		t.Fatalf("request word = %q, want world", gotWord)
	}
	next, _ := model.Update(result)
	model = next.(appModel)
	if model.translatePanel == nil || model.translatePanel.state != translatePanelDone || model.translatePanel.translation != "世界" {
		t.Fatalf("translate panel after result = %+v, want done with 世界", model.translatePanel)
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

// TestRunningModelQueuesPlainText 验证模型运行中普通文本也会进入 queue，而不是作为当前 turn 的补充输入。
func TestRunningModelQueuesPlainText(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.input.SetValue("queued plain text")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued submit returned cmd")
	}
	if got := model.chatQueue.Len(); got != 1 {
		t.Fatalf("queue len = %d, want 1", got)
	}
	if len(runner.supplements) != 0 {
		t.Fatalf("runner.supplements = %#v, want none", runner.supplements)
	}
	if len(model.transcript) != 1 {
		t.Fatalf("transcript len = %d, want 1 while queued input is pending: %#v", len(model.transcript), model.transcript)
	}
	if got := model.transcript[0]; got.kind != entryUser || got.body != "first" {
		t.Fatalf("active turn transcript = %#v", got)
	}
	if got := model.input.Value(); got != "" {
		t.Fatalf("input value = %q, want cleared", got)
	}
	_ = cmd()
}

// TestRunningModelEnterSubmitsSupplement 验证旧的 supplement runner 能力不再被普通文本 Enter 隐式调用。
func TestRunningModelEnterSubmitsSupplement(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.input.SetValue("supplement this turn")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued submit returned cmd")
	}
	if got := model.chatQueue.Len(); got != 1 {
		t.Fatalf("queue len = %d, want 1", got)
	}
	if got := runner.supplements; len(got) != 0 {
		t.Fatalf("runner.supplements = %#v, want no implicit supplement", got)
	}
	if len(model.transcript) != 1 {
		t.Fatalf("transcript len = %d, want 1 while queued input is pending: %#v", len(model.transcript), model.transcript)
	}
	if got := model.transcript[0]; got.kind != entryUser || got.body != "first" {
		t.Fatalf("active turn transcript = %#v", got)
	}
	if got := model.input.Value(); got != "" {
		t.Fatalf("input value = %q, want cleared", got)
	}
	_ = cmd()
}

func TestRunningModelRejectsTerminalModeSubmit(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.terminalMode = true
	model.input.SetValue("ls")
	next, terminalCmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if terminalCmd != nil {
		t.Fatalf("terminal submit returned cmd while model running")
	}
	last := model.transcript[len(model.transcript)-1]
	if last.title != "busy" || !strings.Contains(last.body, "terminal commands are unavailable") {
		t.Fatalf("last transcript = %#v, want terminal busy message", last)
	}
	if got := len(runner.inputs); got != 0 {
		t.Fatalf("runner.inputs before first cmd = %#v, want none", runner.inputs)
	}
	_ = cmd()
}

// TestRunningModelQueuesChatFIFOWithTab 验证模型运行中 Tab 按 FIFO 排队且不重入 runner。
func TestRunningModelQueuesChatFIFOWithTab(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("first submit cmd is nil")
	}

	model.input.SetValue("second")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued submit returned cmd")
	}
	model.input.SetValue("third")
	next, queuedCmd = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("second queued submit returned cmd")
	}
	if got := runner.supplements; len(got) != 0 {
		t.Fatalf("runner.supplements = %#v, want none for queued inputs", got)
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

func TestExitCommandVariantsQuitTUI(t *testing.T) {
	for _, input := range []string{"exit", "/exit", "quit", "/quit"} {
		model := newTestModel(&fakeRunner{})
		model.input.SetValue(input)

		_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("%q did not return quit cmd", input)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%q returned non-quit cmd", input)
		}
	}
}

func TestExitCommandWorksBeforeTerminalModeShellDispatch(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.terminalMode = true
	model.input.SetValue("exit")

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("terminal-mode exit did not return quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("terminal-mode exit returned non-quit cmd")
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %#v, want no chat turn", runner.inputs)
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
	runningRendered := renderTranscript(model.transcript, 80, model.showThinking)
	for _, want := range []string{"hello", "◌ Read: go.mod  运行中"} {
		if !strings.Contains(runningRendered, want) {
			t.Fatalf("running transcript = %q, want %q", runningRendered, want)
		}
	}
	for _, hidden := range []string{"[Read]", "file_path=", "file_path  "} {
		if strings.Contains(runningRendered, hidden) {
			t.Fatalf("running transcript = %q, should not show duplicate citation or field label %q", runningRendered, hidden)
		}
	}
	for _, hidden := range []string{"\n  tool\n", "\n  result\n"} {
		if strings.Contains(runningRendered, hidden) {
			t.Fatalf("running transcript = %q, should not show %q title", runningRendered, strings.TrimSpace(hidden))
		}
	}

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{Name: "Read", Content: "module paw"}))
	model = next.(appModel)
	okRendered := renderTranscript(model.transcript, 80, model.showThinking)
	for _, want := range []string{"hello", "✓ Read: go.mod  完成"} {
		if !strings.Contains(okRendered, want) {
			t.Fatalf("ok transcript = %q, want %q", okRendered, want)
		}
	}
	if strings.Contains(okRendered, "[Read]") || strings.Contains(okRendered, "◌ Read: go.mod  运行中") || strings.Contains(okRendered, "file_path=") || strings.Contains(okRendered, "file_path  ") {
		t.Fatalf("ok transcript = %q, should replace running status", okRendered)
	}
	if !strings.Contains(okRendered, "✓ Read: go.mod  完成") {
		t.Fatalf("ok transcript = %q, want completed tool block", okRendered)
	}
	for _, hidden := range []string{"\n  tool\n", "\n  result\n"} {
		if strings.Contains(okRendered, hidden) {
			t.Fatalf("ok transcript = %q, should not show %q title", okRendered, strings.TrimSpace(hidden))
		}
	}
	next, _ = model.Update(assistantDeltaMsg("loaded go.mod"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)

	rendered := renderTranscript(model.transcript, 80, model.showThinking)
	for _, want := range []string{"loaded go.mod", "✓ Read: go.mod  完成"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript = %q, want %q", rendered, want)
		}
	}
	for _, hidden := range []string{"\n  tool\n", "\n  result\n", "[Read]", "file_path=", "file_path  ", "◌ Read: go.mod  运行中", "module paw", "tool cites"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("rendered transcript = %q, should not contain old tool block marker/content %q", rendered, hidden)
		}
	}
}

func TestAssistantDeltaBuffersTailAndRefreshesCompletedLineImmediately(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()
	model.refreshViewport()
	model.lastTranscriptRefreshAt = time.Now().Add(transcriptStreamingRefreshInterval)

	next, _ := model.Update(assistantDeltaMsg("streamed text"))
	model = next.(appModel)

	if model.transcriptRefreshPending {
		t.Fatalf("transcriptRefreshPending = true, want false for hidden tail")
	}
	if got := model.transcript[len(model.transcript)-1].body; got != "" {
		t.Fatalf("assistant body = %q, want hidden tail", got)
	}
	if strings.Contains(model.viewport.View(), "streamed text") {
		t.Fatalf("viewport leaked incomplete tail: %q", model.viewport.View())
	}

	next, _ = model.Update(assistantDeltaMsg("\n"))
	model = next.(appModel)

	// 完整行也进入 33ms 帧窗口合并（增量渲染后每次重建成本已足够低，
	// 不再为单个换行插队全量刷新）；窗口到期后由 cursorFrame 驱动 flush。
	if !model.transcriptRefreshPending {
		t.Fatalf("transcriptRefreshPending = false, want frame-window merge for completed line")
	}
	if got := model.transcript[len(model.transcript)-1].body; got != "streamed text\n" {
		t.Fatalf("assistant body = %q, want completed line", got)
	}
	if strings.Contains(model.viewport.View(), "streamed text") {
		t.Fatalf("viewport leaked content before flush window: %q", model.viewport.View())
	}

	next, _ = model.Update(cursorFrameMsg(model.transcriptRefreshPendingAt.Add(transcriptStreamingRefreshInterval)))
	model = next.(appModel)
	if model.transcriptRefreshPending {
		t.Fatalf("transcriptRefreshPending = true, want false after flush window")
	}
	if !strings.Contains(model.viewport.View(), "streamed text") {
		t.Fatalf("viewport = %q, want streamed text after flush window", model.viewport.View())
	}
}

func newTranscriptScrollTestModel() appModel {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	for i := 0; i < 30; i++ {
		model.transcript = append(model.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
			body:  fmt.Sprintf("history line %02d", i),
		})
	}
	model.refreshViewport()
	return model
}

func TestAssistantStreamingPreservesManualTranscriptScroll(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	wantOffset := model.viewport.YOffset

	next, _ := model.Update(assistantDeltaMsg("streamed line\n"))
	model = next.(appModel)
	if model.viewport.YOffset != wantOffset {
		t.Fatalf("YOffset = %d after streaming update, want preserved offset %d", model.viewport.YOffset, wantOffset)
	}

	model.viewport.GotoBottom()
	next, _ = model.Update(assistantDeltaMsg("followed line\n"))
	model = next.(appModel)
	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom after streaming while already at bottom, offset=%d", model.viewport.YOffset)
	}
}

func TestAssistantStreamingFollowsBottomWithActiveSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	model.selectionActive = true
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: 0, col: 1}

	next, _ := model.Update(assistantDeltaMsg("followed with selection\n"))
	model = next.(appModel)

	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom with active selection, offset=%d", model.viewport.YOffset)
	}
}

func TestThinkingStreamingPreservesManualTranscriptScroll(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	wantOffset := model.viewport.YOffset
	model.lastTranscriptRefreshAt = time.Time{}

	next, _ := model.Update(thinkingDeltaMsg("thinking line\n"))
	model = next.(appModel)
	if model.viewport.YOffset != wantOffset {
		t.Fatalf("YOffset = %d after thinking update, want preserved offset %d", model.viewport.YOffset, wantOffset)
	}
}

func TestCompletedTurnPreservesManualTranscriptScroll(t *testing.T) {
	model := newTranscriptScrollTestModel()
	next, _ := model.Update(assistantDeltaMsg("answer line\n"))
	model = next.(appModel)
	model.viewport.SetYOffset(3)
	wantOffset := model.viewport.YOffset

	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	if model.viewport.YOffset != wantOffset {
		t.Fatalf("YOffset = %d after completed turn, want preserved offset %d", model.viewport.YOffset, wantOffset)
	}
}

func TestResizeKeepsBottomFollowAfterViewportShrinks(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()

	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	model = next.(appModel)
	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom after resize from bottom, offset=%d", model.viewport.YOffset)
	}
}

func TestResizeKeepsBottomFollowWithActiveSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	model.selectionActive = true
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: 0, col: 1}

	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	model = next.(appModel)

	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom after resize with active selection, offset=%d", model.viewport.YOffset)
	}
}

func TestRenderTranscriptContentCachesUnchangedEntries(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.viewport.Width = 80
	model.transcript = []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "first **bold**", version: 1},
		{kind: entryAssistant, title: "assistant", body: "second", version: 1},
	}

	first := model.renderTranscriptContent()
	if !strings.Contains(ansi.Strip(first), "first bold") || !strings.Contains(first, "second") {
		t.Fatalf("first render = %q", first)
	}
	if len(model.transcriptRenderCache) != len(model.transcript) {
		t.Fatalf("cache len = %d, want %d", len(model.transcriptRenderCache), len(model.transcript))
	}
	cachedFirst := model.transcriptRenderCache[0]
	cachedSecond := model.transcriptRenderCache[1]

	model.transcript[1].body = "second updated"
	model.touchTranscriptEntryAt(1)
	second := model.renderTranscriptContent()

	if !strings.Contains(second, "second updated") {
		t.Fatalf("second render = %q, want updated body", second)
	}
	if model.transcriptRenderCache[0] != cachedFirst {
		t.Fatalf("first cache entry changed for an untouched transcript entry")
	}
	if model.transcriptRenderCache[1].key == cachedSecond.key {
		t.Fatalf("second cache key did not change after transcript entry mutation")
	}
}

func TestTranscriptInteractionCachesReuseRenderedLongTranscript(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	for i := 0; i < 400; i++ {
		model.transcript = append(model.transcript, transcriptEntry{
			kind:    entryAssistant,
			title:   "assistant",
			body:    fmt.Sprintf("history line %03d", i),
			version: 1,
		})
	}
	model.refreshViewport()

	if !model.transcriptContentCached {
		t.Fatal("refreshViewport did not retain the rendered transcript content")
	}
	firstLines := model.transcriptLineSnapshots()
	if len(firstLines) == 0 || !model.transcriptLineCacheReady {
		t.Fatal("transcript line snapshot cache was not populated")
	}
	firstLocations := model.transcriptEntryLocationsAt()
	if len(firstLocations) != len(model.transcript) || !model.transcriptLocationsReady {
		t.Fatalf("location cache len = %d, want %d", len(firstLocations), len(model.transcript))
	}

	if secondLines := model.transcriptLineSnapshots(); len(secondLines) != len(firstLines) || &secondLines[0] != &firstLines[0] {
		t.Fatal("repeated transcript hit testing rebuilt line snapshots")
	}
	if secondLocations := model.transcriptEntryLocationsAt(); len(secondLocations) != len(firstLocations) || &secondLocations[0] != &firstLocations[0] {
		t.Fatal("repeated transcript hit testing rebuilt entry locations")
	}

	model.transcript[0].body = "updated history"
	model.touchTranscriptEntryAt(0)
	model.refreshViewportPreservingOffset()
	if model.transcriptLineCacheReady || model.transcriptLocationsReady {
		t.Fatal("transcript refresh did not invalidate interaction caches")
	}
	updatedLines := model.transcriptLineSnapshots()
	if len(updatedLines) == 0 || &updatedLines[0] == &firstLines[0] {
		t.Fatal("line snapshot cache was not rebuilt after transcript refresh")
	}
}

func TestAssistantCitationRendersAsBlockquoteUnderMessage(t *testing.T) {
	entry := transcriptEntry{
		kind:  entryAssistant,
		title: "assistant",
		body:  "README loaded",
		citations: []toolCitation{
			{name: "Read", target: "file_path=README.md", status: "ok"},
		},
	}

	rendered := renderEntry(entry, 58)
	for _, want := range []string{"README loaded", "[Read]", "ok", "file_path=README.md"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("assistant citation = %q, want %q", rendered, want)
		}
	}
	for _, hidden := range []string{"tool cites", "[^tool-1]"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("assistant citation = %q, should not contain previous citation marker %q", rendered, hidden)
		}
	}
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if strings.Contains(line, "[Read]") {
			if !strings.Contains(line, "│ ") {
				t.Fatalf("citation line = %q, want stable left quote edge", line)
			}
			if got := lipgloss.Width(line); got > 58 {
				t.Fatalf("citation line width = %d, want <= 58: %q", got, line)
			}
		}
	}
}

func TestToolResultEntryMatchesByToolUseID(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call_1", Name: "Read", Input: []byte(`{"file_path":"first.go"}`)}))
	model = next.(appModel)
	next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call_2", Name: "Read", Input: []byte(`{"file_path":"second.go"}`)}))
	model = next.(appModel)

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call_1", Name: "Read", Content: "first result"}))
	model = next.(appModel)

	rendered := renderTranscript(model.transcript, 80, model.showThinking)
	if !strings.Contains(rendered, "✓ Read: first.go  完成") {
		t.Fatalf("rendered transcript = %q, want call_1 completed", rendered)
	}
	if !strings.Contains(rendered, "◌ Read: second.go  运行中") {
		t.Fatalf("rendered transcript = %q, want call_2 still running", rendered)
	}
}

// TestTaskWaitRendersRunningLineAndDisappears 验证 TaskWait 以
// "worker <名字> 正在运行 Ns" 状态行展示：运行中每秒刷新秒数，工具完成后
// 整行直接消失，不留下可折叠的 Tools 调用块。
func TestTaskWaitRendersRunningLineAndDisappears(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
		{ID: "task-light", Name: "高松灯", Status: taskpkg.TaskRunning},
	}}
	model.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "wait"}}

	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "wait_1", Name: "TaskWait", Input: []byte(`{"task_ids":["task-light"],"timeout_ms":600000}`)}))
	model = next.(appModel)

	last := model.transcript[len(model.transcript)-1]
	if !last.taskWaitRunning || last.toolUseID != "wait_1" {
		t.Fatalf("last transcript entry = %#v, want TaskWait status line", last)
	}
	if got := last.body; got != "worker 高松灯 正在运行 0s" {
		t.Fatalf("status line body = %q, want worker 高松灯 正在运行 0s", got)
	}

	running := renderTranscript(model.transcript, 80, model.showThinking)
	for _, want := range []string{"worker 高松灯 正在运行 0s"} {
		if !strings.Contains(running, want) {
			t.Fatalf("running transcript = %q, want %q", running, want)
		}
	}
	for _, hidden := range []string{"TaskWait", "Tools", "task_ids", "task-light", "◌", "运行中"} {
		if strings.Contains(running, hidden) {
			t.Fatalf("running transcript = %q, should not render tool block content %q", running, hidden)
		}
	}

	// 秒数随帧刷新：13 秒后状态行更新。
	started := model.transcript[len(model.transcript)-1].toolStartedAt
	model.refreshRunningToolProgress(started.Add(13 * time.Second))
	last = model.transcript[len(model.transcript)-1]
	if got := last.body; got != "worker 高松灯 正在运行 13s" {
		t.Fatalf("status line body after refresh = %q, want worker 高松灯 正在运行 13s", got)
	}

	// 完成：状态行消失，不折叠为工具块。
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "wait_1", Name: "TaskWait", Content: `{"task_ids":["task-light"],"status":"completed"}`}))
	model = next.(appModel)

	for _, entry := range model.transcript {
		if entry.taskWaitRunning {
			t.Fatalf("TaskWait status line survived completion: %#v", entry)
		}
	}
	done := renderTranscript(model.transcript, 80, model.showThinking)
	for _, hidden := range []string{"TaskWait", "正在运行", "Tools", "task-light"} {
		if strings.Contains(done, hidden) {
			t.Fatalf("done transcript = %q, should not contain %q", done, hidden)
		}
	}
	if !strings.Contains(done, "wait") {
		t.Fatalf("done transcript = %q, want user message preserved", done)
	}
}

// TestTaskWaitErrorReplacesLineWithErrorEntry 验证 TaskWait 失败时
// 状态行消失并转为错误行，而不是折叠成工具调用块。
func TestTaskWaitErrorReplacesLineWithErrorEntry(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
		{ID: "task-1", Name: "弦卷心", Status: taskpkg.TaskRunning},
	}}

	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "wait_1", Name: "TaskWait", Input: []byte(`{"task_ids":["task-1"]}`)}))
	model = next.(appModel)

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "wait_1", Name: "TaskWait", Content: "timed out after 60s", IsError: true}))
	model = next.(appModel)

	last := model.transcript[len(model.transcript)-1]
	if last.kind != entryError || last.title != "task" || !strings.Contains(last.body, "timed out after 60s") {
		t.Fatalf("last transcript entry = %#v, want task error entry", last)
	}
	rendered := renderTranscript(model.transcript, 80, model.showThinking)
	if strings.Contains(rendered, "正在运行") || strings.Contains(rendered, "Tools") {
		t.Fatalf("error transcript = %q, should not keep status line or tool block", rendered)
	}
	if !strings.Contains(rendered, "timed out after 60s") {
		t.Fatalf("error transcript = %q, want visible error", rendered)
	}
}

// TestMarkRunningToolsErrorRemovesTaskWaitLine 验证 turn 失败时悬挂的
// TaskWait 状态行被移除。
func TestMarkRunningToolsErrorRemovesTaskWaitLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "wait"},
		{
			kind:            entrySystem,
			title:           "",
			body:            "worker 高松灯 正在运行 5s",
			taskWaitRunning: true,
			taskWaitNames:   []string{"高松灯"},
			toolUseID:       "wait_1",
			toolName:        "TaskWait",
			toolStatus:      "running",
			toolStartedAt:   time.Now().Add(-5 * time.Second),
		},
	}
	model.markRunningToolsError(errors.New("stream error"))

	for _, entry := range model.transcript {
		if entry.taskWaitRunning {
			t.Fatalf("TaskWait status line survived turn failure: %#v", entry)
		}
	}
	if got := model.transcript[len(model.transcript)-1]; got.kind != entryUser {
		t.Fatalf("last transcript entry = %#v, want original user message", got)
	}
}

func TestToolCallWithoutAssistantTextRendersRunningEntry(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{Name: "LS", Input: []byte(`{"path":"."}`)}))
	model = next.(appModel)
	rendered := renderTranscript(model.transcript, 54, model.showThinking)
	for _, want := range []string{"◌ LS: .  运行中"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("running-only tool entry = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "assistant") || strings.Contains(rendered, "[LS]") || strings.Contains(rendered, "path=") || strings.Contains(rendered, "path  ") {
		t.Fatalf("running-only tool entry = %q, should not create assistant citation", rendered)
	}

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{Name: "LS", Content: "README.md"}))
	model = next.(appModel)
	rendered = renderTranscript(model.transcript, 54, model.showThinking)
	for _, want := range []string{"✓ LS: .  完成"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("completed-only tool entry = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "[LS]") || strings.Contains(rendered, "path=") || strings.Contains(rendered, "path  ") || strings.Contains(rendered, "◌ LS: .  运行中") || strings.Contains(rendered, "README.md") {
		t.Fatalf("completed-only tool entry = %q, should hide duplicate citation/running state/long output", rendered)
	}
}

func TestAssistantToolUseJSONIsHiddenFromTranscript(t *testing.T) {
	entry := transcriptEntry{
		kind:  entryAssistant,
		title: "assistant",
		body:  `Let me inspect the project.{"id":"call_1","input":{"path":"."},"name":"LS","type":"tool_use"}`,
	}
	rendered := renderEntry(entry, 80)
	if !strings.Contains(rendered, "Let me inspect the project.") {
		t.Fatalf("assistant entry = %q, want visible prose", rendered)
	}
	for _, hidden := range []string{"tool_use", "call_1", `"path"`, `"name"`} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("assistant entry = %q, should hide %q", rendered, hidden)
		}
	}
}

func TestTaskToolCallBodySummarizesPrompt(t *testing.T) {
	body := formatToolCallBody("Task", []byte(`{
		"context_mode":"empty",
		"description":"读取并分析项目结构",
		"prompt":"读取并分析当前项目。\n1. 读取 go.mod\n2. 读取 README.md",
		"run_mode":"sync"
	}`), "")
	for _, want := range []string{"Task  sync  empty", "description", "读取并分析项目结构", "prompt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("task body = %q, want %q", body, want)
		}
	}
	for _, hidden := range []string{`{"`, `\n`} {
		if strings.Contains(body, hidden) {
			t.Fatalf("task body = %q, should not contain raw JSON marker %q", body, hidden)
		}
	}
}

func TestBashToolCallBodyHidesDefaultCWD(t *testing.T) {
	body := formatToolCallBody("Bash", []byte(`{"command":"rm multi_line_file.txt","cwd":"."}`), "")
	if strings.Contains(body, "\n.") {
		t.Fatalf("bash body = %q, should hide default cwd dot", body)
	}
	for _, want := range []string{"Bash", "rm multi_line_file.txt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("bash body = %q, want %q", body, want)
		}
	}
}

func TestWriteToolCallBodyRendersDiffPreview(t *testing.T) {
	body := formatToolCallBody("Write", []byte(`{"file_path":"main.py","content":"def hanoi():\n    pass\n"}`), "")
	for _, want := range []string{"Write", "main.py", "  1 + │ def hanoi():", "  2 + │     pass"} {
		if !strings.Contains(body, want) {
			t.Fatalf("write body = %q, want %q", body, want)
		}
	}
	for _, hidden := range []string{"file_path=", "file_path  ", "content  ", "---", "+++", "@@", `{"file_path"`} {
		if strings.Contains(body, hidden) {
			t.Fatalf("write body = %q, should not contain raw write field %q", body, hidden)
		}
	}
}

func TestUpdateToolCallBodyRendersOldNewDiffPreview(t *testing.T) {
	body := formatToolCallBody("Update", []byte(`{"file_path":"main.py","old_string":"return 1\n","new_string":"return 2\n"}`), "")
	for _, want := range []string{"Update", "main.py", "1   - │ return 1", "  1 + │ return 2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("update body = %q, want %q", body, want)
		}
	}
	for _, hidden := range []string{"file_path=", "file_path  ", "---", "+++", "@@", "old_string", "new_string"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("update body = %q, should not contain removed field/header %q", body, hidden)
		}
	}
}

func TestToolDiffDetailLinesUseBackgroundColors(t *testing.T) {
	lines := []string{"1 - │ old", "1 + │ new"}
	rendered := renderToolDetailLines(lines, 40)
	rowWidth := diffDetailRowsWidth(lines, 40)
	if rowWidth != 40 {
		t.Fatalf("diff row width = %d, want detail width 40", rowWidth)
	}
	deleted := lipgloss.NewStyle().Foreground(lipgloss.Color("224")).Background(lipgloss.Color("52")).Bold(true).Render(fitStyledCellLine("1 - │ old", rowWidth))
	added := lipgloss.NewStyle().Foreground(lipgloss.Color("194")).Background(lipgloss.Color("22")).Bold(true).Render(fitStyledCellLine("1 + │ new", rowWidth))
	for _, want := range []string{deleted, added} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered diff = %q, want colored line %q", rendered, want)
		}
	}
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if got := lipgloss.Width(line); got != rowWidth {
			t.Fatalf("diff line width = %d, want safe row width %d: %q", got, rowWidth, line)
		}
	}
}

func TestToolDetailUsesBackgroundColor(t *testing.T) {
	if got := toolDetailStyle.GetBackground(); got != lipgloss.Color(colorManager.Hex(colorToolDetailBackground)) {
		t.Fatalf("tool detail background = %#v, want %q", got, colorManager.Hex(colorToolDetailBackground))
	}
}

func TestMarkdownListDetailLinesKeepDetailBackground(t *testing.T) {
	lines := []string{"- first item", "- second item"}
	rendered := renderToolDetailLines(lines, 40)
	for _, line := range lines {
		want := toolDetailStyle.Width(40).Render("  " + line)
		if !strings.Contains(rendered, want) {
			t.Fatalf("markdown list line = %q, want detail style without diff background %q", rendered, want)
		}
	}
}

func TestToolDetailLinesPreserveCommandOutputIndentation(t *testing.T) {
	lines := []string{
		"package main",
		"    if ready {",
		"        go test ./...",
	}
	rendered := ansi.Strip(renderToolDetailLines(lines, 48))
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) != len(lines) {
		t.Fatalf("rendered lines = %#v, want %d lines", renderedLines, len(lines))
	}
	for index, line := range lines {
		want := "  " + line
		if !strings.HasPrefix(renderedLines[index], want) {
			t.Fatalf("line %d = %q, want prefix %q", index+1, renderedLines[index], want)
		}
	}
}

func TestGroupedBashResultPreservesSedAndGoTestIndentation(t *testing.T) {
	result := "package main\n    func main() {\n\tgo test ./...\n    }"
	rendered := ansi.Strip(renderToolsGroup([]transcriptEntry{{
		kind:          entryTool,
		toolName:      "Bash",
		toolStatus:    "ok",
		toolTarget:    "sed -n '1,4p' main.go && go test ./...",
		toolResult:    result,
		toolGroupOpen: true,
	}}, 80, time.Time{}, true, true))

	var packageColumn, funcColumn, testColumn int = -1, -1, -1
	for _, line := range strings.Split(rendered, "\n") {
		switch {
		case strings.Contains(line, "package main"):
			packageColumn = strings.Index(line, "package main")
		case strings.Contains(line, "func main() {"):
			funcColumn = strings.Index(line, "func main() {")
		case strings.Contains(line, "go test ./..."):
			testColumn = strings.Index(line, "go test ./...")
		}
	}
	if packageColumn < 0 || funcColumn < 0 || testColumn < 0 {
		t.Fatalf("grouped Bash output is incomplete:\n%s", rendered)
	}
	if funcColumn-packageColumn != 4 {
		t.Fatalf("sed source indentation changed: package column=%d func column=%d\n%s", packageColumn, funcColumn, rendered)
	}
	if testColumn-packageColumn != streamTabWidth {
		t.Fatalf("go test tab indentation changed: package column=%d test column=%d want delta=%d\n%s", packageColumn, testColumn, streamTabWidth, rendered)
	}
}

func TestUnifiedDiffLinesUseBackgroundColorsOnlyWithinHunk(t *testing.T) {
	lines := []string{"@@ -1,2 +1,2 @@", " - keep this markdown item", "-old", "+new"}
	rendered := renderToolDetailLines(lines, 40)
	rowWidth := diffDetailRowsWidth(lines, 40)
	contextLine := toolDetailStyle.Width(40).Render("  - keep this markdown item")
	if !strings.Contains(rendered, contextLine) {
		t.Fatalf("unified diff context line = %q, want detail style %q", rendered, contextLine)
	}
	deleted := lipgloss.NewStyle().Foreground(lipgloss.Color("224")).Background(lipgloss.Color("52")).Bold(true).Render(fitStyledCellLine("-old", rowWidth))
	added := lipgloss.NewStyle().Foreground(lipgloss.Color("194")).Background(lipgloss.Color("22")).Bold(true).Render(fitStyledCellLine("+new", rowWidth))
	for _, want := range []string{deleted, added} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unified diff = %q, want colored line %q", rendered, want)
		}
	}
}

func TestToolEntryContentWidthLeavesTranscriptNoWrapCell(t *testing.T) {
	width := 80
	want := width - toolResultBorderStyle.GetHorizontalFrameSize() - transcriptPanelHorizontalFrame - 1
	if got := toolEntryContentWidth(width, toolResultBorderStyle); got != want {
		t.Fatalf("tool content width = %d, want %d", got, want)
	}
}

func TestToolDiffRowsAlignLineNumberWithStatusMarker(t *testing.T) {
	longLine := strings.Repeat("这是一个测试文件的内容。", 20)
	rendered := ansi.Strip(renderEntry(transcriptEntry{
		kind:         entryTool,
		title:        "tool",
		toolName:     "Write",
		toolStatus:   "ok",
		toolTarget:   "test.txt",
		toolResult:   "1 + │ " + longLine,
		toolExpanded: true,
	}, 80))
	lines := strings.Split(rendered, "\n")
	var headerLine, diffLine string
	for _, line := range lines {
		if strings.Contains(line, "✓ Write: test.txt  完成") {
			headerLine = line
		}
		if strings.Contains(line, "1 + │") {
			diffLine = line
		}
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("rendered line width = %d, want <= 80: %q\n%s", got, line, rendered)
		}
	}
	if headerLine == "" || diffLine == "" {
		t.Fatalf("rendered tool entry missing header or diff line:\n%s", rendered)
	}
	if got, want := strings.Index(diffLine, "1"), strings.Index(headerLine, "✓"); got != want {
		t.Fatalf("line number column = %d, want status marker column %d\nheader: %q\ndiff:   %q", got, want, headerLine, diffLine)
	}
	if got := lipgloss.Width(diffLine); got >= 80 {
		t.Fatalf("diff line width = %d, want right margin to avoid terminal wrap: %q", got, diffLine)
	}
	if !strings.Contains(diffLine, "…") {
		t.Fatalf("diff line = %q, want long diff content truncated to avoid soft wrap", diffLine)
	}
}

func TestToolEntryShowsCompleteLargeDetailBlocks(t *testing.T) {
	lines := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	rendered := ansi.Strip(renderEntry(transcriptEntry{
		kind:         entryTool,
		title:        "tool",
		toolName:     "Bash",
		toolStatus:   "ok",
		toolTarget:   "go test ./...",
		toolResult:   strings.Join(lines, "\n"),
		toolExpanded: true,
	}, 80))

	if !strings.Contains(rendered, "line-79") {
		t.Fatalf("rendered tool entry should show tail detail lines:\n%s", rendered)
	}
	if strings.Contains(rendered, "lines hidden") {
		t.Fatalf("rendered tool entry unexpectedly hid detail lines:\n%s", rendered)
	}
}

func TestMarkdownCodeBlockClampsLargeBlocks(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	rendered := ansi.Strip(renderCodeBlock("go", strings.Join(lines, "\n"), 80))

	if strings.Contains(rendered, "line-79") {
		t.Fatalf("rendered code block should hide tail lines:\n%s", rendered)
	}
	if !strings.Contains(rendered, "... 48 more lines hidden") {
		t.Fatalf("rendered code block = %q, want hidden line summary", rendered)
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

	if len(model.transcript) < 2 || model.transcript[len(model.transcript)-2].kind != entryReasoning {
		t.Fatalf("transcript = %#v, want reasoning entry before answer", model.transcript)
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
	next, _ = model.Update(systemEventMsg(ui.SystemEvent{Title: "task", Body: "background task finished"}))
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
	if last.kind != entrySystem || last.title != "task" {
		t.Fatalf("last transcript entry = %#v, want task system event", last)
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
	model.inputHistory = []inputDraft{{Text: "previous"}}
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

func TestCtrlJSecondNewlineKeepsEarlierLinesVisible(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 100
	model.height = 30
	model.relayout()
	model.input.SetValue("first")
	model.input.CursorEnd()
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = next.(appModel)

	if got := model.input.Height(); got != 3 {
		t.Fatalf("input height = %d, want 3", got)
	}
	if got := model.input.Value(); got != "first\nsecond\n" {
		t.Fatalf("input value = %q", got)
	}
	if got := model.input.Line(); got != 2 {
		t.Fatalf("cursor line = %d, want third logical line", got)
	}
	if got := visibleTextareaCursorRow(model.input); got != 2 {
		t.Fatalf("visible cursor row = %d, want third rendered row", got)
	}
	rendered := ansi.Strip(model.renderInputBox())
	first := strings.Index(rendered, "first")
	second := strings.Index(rendered, "second")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("input box should keep first and second lines in order:\n%s", rendered)
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
	if len(model.inputHistory) != 1 || model.inputHistory[0].Text != "!pwd" {
		t.Fatalf("inputHistory = %#v", model.inputHistory)
	}
}

// TestViewUsesMinimalPlaceholderAndOmitsHelpLine 验证输入框只展示最简 > 占位符并移除帮助行。
func TestViewUsesMinimalPlaceholderAndOmitsHelpLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if model.input.Placeholder != "Ask anything…" {
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

// TestViewFramesTranscriptHistoryPanel 验证聊天历史区域使用细线结构并展示空状态。
func TestViewFramesTranscriptHistoryPanel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	rendered := model.View()
	for _, want := range []string{"─", "アトリ高性能ですから!", "› Ask anything…"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("view = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "SIGNAL / READY") {
		t.Fatalf("view = %q, contains retired empty-state/input text", rendered)
	}
	for _, unwanted := range []string{"Input", "Waiting", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("view = %q, should not contain %q", rendered, unwanted)
		}
	}
}

func TestInputHintIsDimAndDisappearsAfterFirstSubmission(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	initial := model.View()
	if !strings.Contains(initial, "Ask anything…") {
		t.Fatalf("initial view = %q, want landing hint", initial)
	}

	model.input.SetValue("hello")
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if !model.hasInteracted {
		t.Fatal("submission did not mark the input as interacted")
	}
	if strings.Contains(model.View(), "Ask anything…") {
		t.Fatalf("post-submit view = %q, hint should be hidden", model.View())
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
	wantViewportWidth := maxInt(1, model.width-mainFrameHorizontalFrame-mainContentPadding*2)
	if model.viewport.Width != wantViewportWidth {
		t.Fatalf("viewport width = %d, want %d", model.viewport.Width, wantViewportWidth)
	}
}

func TestLeftPanelsRenderWithSameOuterWidth(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.input.SetValue("aligned")
	model.relayout()

	transcriptWidth := lipgloss.Width(model.renderTranscriptBox())
	inputWidth := lipgloss.Width(model.renderInputBox())
	if transcriptWidth != inputWidth {
		t.Fatalf("transcript box width = %d, input box width = %d; want aligned", transcriptWidth, inputWidth)
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
	lines := strings.Split(ansi.Strip(rendered), "\n")
	searchStart := maxInt(0, len(lines)-4)
	bottomVisible := false
	for _, line := range lines[searchStart:] {
		if strings.Contains(line, "bottom") {
			bottomVisible = true
			break
		}
	}
	if !bottomVisible {
		t.Fatalf("bottom input not visible near frame bottom:\n%s", ansi.Strip(rendered))
	}
	lastLine := lines[len(lines)-1]
	if strings.TrimSpace(lastLine) == "" || !strings.Contains(lastLine, "─") {
		t.Fatalf("last rendered line = %q, want one fixed main-frame bottom rule", lastLine)
	}
}

func TestRelayoutIgnoresLegacyInputTitleMeter(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		Task: settings.TaskConfig{
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

	wantHeight := model.currentLayout().transcriptHeight
	if model.viewport.Height != wantHeight {
		t.Fatalf("viewport height = %d, want %d", model.viewport.Height, wantHeight)
	}
	if got := lipgloss.Height(model.View()); got != model.height {
		t.Fatalf("rendered height = %d, want %d", got, model.height)
	}
	if titleHeight := model.inputEmbeddedTitleHeight(); titleHeight != 0 {
		t.Fatalf("inputEmbeddedTitleHeight() = %d, want 0 for legacy input-title", titleHeight)
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

// TestTextareaInputUsesThemeBackground verifies input cells are painted by the active theme.
func TestTextareaInputUsesThemeBackground(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	want := model.styles.Colors.LipglossColor(colorTerminalBackground)

	for name, style := range map[string]lipgloss.Style{
		"focused base":        model.input.FocusedStyle.Base,
		"focused cursor line": model.input.FocusedStyle.CursorLine,
		"focused text":        model.input.FocusedStyle.Text,
		"blurred base":        model.input.BlurredStyle.Base,
		"blurred cursor line": model.input.BlurredStyle.CursorLine,
		"blurred text":        model.input.BlurredStyle.Text,
	} {
		if got := style.GetBackground(); got != want {
			t.Fatalf("%s background = %#v, want %#v", name, got, want)
		}
	}
}

// TestFirstTypedLineUsesThemeBackground verifies textarea's internal active
// style pointer belongs to the model copy. Focusing the temporary textarea
// before assigning it into appModel leaves that pointer attached to the old
// copy, so the first typed row keeps the library's default ANSI-black
// CursorLine background instead of the selected theme. The cursor line must
// also carry the body foreground (regression: it used to have background only,
// falling back to the terminal default foreground color).
func TestFirstTypedLineUsesThemeBackground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	model := newTestModel(&fakeRunner{})
	model.width = 80

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = next.(appModel)
	rendered := model.renderInputContent()

	wantBG := sgrParamSegment(t, lipgloss.NewStyle().
		Background(model.styles.Colors.LipglossColor(colorTerminalBackground)).Render("x"), "48;2;")
	if !strings.Contains(rendered, wantBG) {
		t.Fatalf("first input render = %q, want themed background %q", rendered, wantBG)
	}
	wantFG := sgrParamSegment(t, bodyStyle.Render("x"), "38;2;")
	if !strings.Contains(rendered, wantFG) {
		t.Fatalf("first input render = %q, want body foreground %q", rendered, wantFG)
	}
	if strings.Contains(rendered, "\x1b[40m") {
		t.Fatalf("first input render retained textarea default ANSI-black background: %q", rendered)
	}
}

// sgrParamSegment 提取 SGR 参数段（如 "38;2;201;194;183"），
// 到下一个 ';' 或 'm' 为止，用于匹配 lipgloss 合并后的 SGR 输出。
func sgrParamSegment(t *testing.T, text, prefix string) string {
	t.Helper()
	index := strings.Index(text, prefix)
	if index < 0 {
		t.Fatalf("missing %q in %q", prefix, text)
	}
	rest := text[index+len(prefix):]
	end := strings.IndexAny(rest, "m;")
	if end < 0 {
		t.Fatalf("unterminated %q in %q", prefix, text)
	}
	return prefix + rest[:end]
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

	input := model.input
	applyTextareaTerminalStyle(&input)
	wantColor := colorManager.LipglossColor(colorInputTerminal)
	if got := input.FocusedStyle.Text.GetForeground(); got != wantColor {
		t.Fatalf("terminal text foreground = %#v, want %#v", got, wantColor)
	}
	if got := input.FocusedStyle.Placeholder.GetForeground(); got != wantColor {
		t.Fatalf("terminal placeholder foreground = %#v, want %#v", got, wantColor)
	}
}

func TestPersistentTerminalModeColorsEmptyInputPlaceholder(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("!")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if !model.terminalMode || model.input.Value() != "" {
		t.Fatalf("terminal mode = %v input = %q", model.terminalMode, model.input.Value())
	}
	rendered := model.renderInputBox()
	want := terminalInputLabelStyle.Render("$")
	if !strings.Contains(rendered, want) {
		t.Fatalf("terminal input box = %q, want styled placeholder %q", rendered, want)
	}
}

// TestTaskCommandUsesDefaultsAndTasksOpenActivity verifies /task defaults and /tasks Activity entry.
func TestTaskCommandUsesDefaultsAndTasksRender(t *testing.T) {
	settingsController := &fakeSettingsController{current: settings.Config{
		Task: settings.TaskConfig{
			DefaultContextMode: settings.ContextModeFork,
			DefaultRunMode:     settings.RunModeBackground,
		},
		UI: settings.UIConfig{
			ContextLimitTokens:   settings.DefaultContextLimitTokens,
			ContextMeterLocation: settings.MeterLocationInputAbove,
		},
	}}
	taskController := &fakeTaskController{
		launchTask: taskpkg.TaskSnapshot{
			ID:             "task-42",
			Name:           "worker",
			SessionID:      "task-42",
			Status:         taskpkg.TaskRunning,
			ContextMode:    settings.ContextModeFork,
			RunMode:        settings.RunModeBackground,
			TranscriptPath: "/tmp/task-42.jsonl",
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, settingsController, taskController, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	handled, cmd := model.handleCommand("/task summarize recent changes")
	if !handled || cmd != nil {
		t.Fatalf("/task handled/cmd = %v/%v", handled, cmd)
	}
	if len(taskController.launchRequests) != 1 {
		t.Fatalf("launch requests = %#v", taskController.launchRequests)
	}
	req := taskController.launchRequests[0]
	if req.ContextMode != settings.ContextModeFork || req.RunMode != settings.RunModeBackground || req.ParentSessionID != "session-1" || req.Prompt != "summarize recent changes" {
		t.Fatalf("launch request = %#v", req)
	}
	if got := model.transcript[len(model.transcript)-1].body; !strings.Contains(got, "worker  running  fork") {
		t.Fatalf("launch transcript = %q", got)
	}
	if strings.Contains(model.transcript[len(model.transcript)-1].body, "id  ") {
		t.Fatalf("launch transcript = %q, should not show task id / session id", model.transcript[len(model.transcript)-1].body)
	}

	handled, cmd = model.handleCommand("/tasks")
	if !handled || cmd != nil {
		t.Fatalf("/tasks handled/cmd = %v/%v", handled, cmd)
	}
	if model.taskPicker == nil || model.taskPicker.tab != activityTabTasks {
		t.Fatalf("activity = %#v, want Tasks tab", model.taskPicker)
	}
	if got := model.renderActivityBox(); !strings.Contains(got, "worker") {
		t.Fatalf("Activity modal = %q, want worker", got)
	}
}

// TestSyncTaskCompletionStartsQueuedTurn verifies sync task completion drains queued chat work.
func TestSyncTaskCompletionStartsQueuedTurn(t *testing.T) {
	runner := &fakeRunner{}
	taskController := &fakeTaskController{
		runResult: taskpkg.Result{
			AgentID:        "agent-7",
			SessionID:      "agent-7",
			ContextMode:    settings.ContextModeEmpty,
			RunMode:        settings.RunModeSync,
			TranscriptPath: "/tmp/agent-7.jsonl",
			Content:        "task complete",
		},
	}
	model := newModel(context.Background(), runner, "session-1", &fakeModelConfigController{}, nil, taskController, nil, newTerminalCursorAnchor())

	handled, cmd := model.handleCommand("/task --sync inspect this")
	if !handled || cmd == nil {
		t.Fatalf("/task handled/cmd = %v/%v", handled, cmd)
	}
	if !model.running {
		t.Fatalf("model.running = false, want true after sync task starts")
	}

	model.input.SetValue("queued follow-up")
	next, queuedCmd := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if queuedCmd != nil {
		t.Fatalf("queued chat returned cmd")
	}

	msg := cmd()
	next, followCmd := model.Update(msg)
	model = next.(appModel)
	if followCmd == nil {
		t.Fatalf("sync task completion should start queued turn")
	}
	var taskEntry transcriptEntry
	foundTask := false
	for _, entry := range model.transcript {
		if entry.title == "task" && strings.Contains(entry.body, "done  depth 0") {
			taskEntry = entry
			foundTask = true
			break
		}
	}
	if !foundTask || !strings.Contains(taskEntry.body, "/tmp/agent-7.jsonl") {
		t.Fatalf("task transcript = %#v", model.transcript)
	}
	if strings.Contains(taskEntry.body, "id  ") {
		t.Fatalf("task transcript = %q, should not show task id / session id", taskEntry.body)
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
	wantUpFromBottom := 1
	if position.upFromBottom != wantUpFromBottom {
		t.Fatalf("upFromBottom = %d, want input content row %d", position.upFromBottom, wantUpFromBottom)
	}
	wantColumn := inputDockStyle.GetPaddingLeft() + terminalCellWidth("hello")
	if position.column != wantColumn {
		t.Fatalf("column = %d, want input cell position %d", position.column, wantColumn)
	}
}

func TestViewAnchorsTerminalCursorWithEmptyPlaceholder(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.input.SetValue("")

	_ = model.View()

	position, ok := anchor.consume()
	if !ok || !position.active {
		t.Fatalf("anchor = %#v/%v, want active", position, ok)
	}
	wantColumn := inputDockStyle.GetPaddingLeft()
	if position.column != wantColumn {
		t.Fatalf("column = %d, want input cell start %d", position.column, wantColumn)
	}
}

func TestViewAnchorsTerminalCursorWhileModelRunning(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.running = true
	model.runningTerminal = false
	model.relayout()
	model.input.SetValue("queued follow-up")

	_ = model.View()

	position, ok := anchor.consume()
	if !ok || !position.active {
		t.Fatalf("anchor = %#v/%v, want active while model is streaming", position, ok)
	}
}

func TestViewClearsTerminalCursorAnchorWhileTerminalRunning(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.running = true
	model.runningTerminal = true
	model.relayout()
	model.input.SetValue("echo hi")

	_ = model.View()

	position, ok := anchor.consume()
	if !ok {
		t.Fatalf("anchor did not receive a clear position")
	}
	if position.active {
		t.Fatalf("cursor position = %#v, want inactive while terminal is running", position)
	}
}

// TestViewAnchorsTerminalCursorWithLegacyInputTitle 验证旧 input-title 配置不会改变输入光标锚点。
func TestViewAnchorsTerminalCursorWithLegacyInputTitle(t *testing.T) {
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
	wantUpFromBottom := 1
	if position.upFromBottom != wantUpFromBottom {
		t.Fatalf("upFromBottom = %d, want input content row %d", position.upFromBottom, wantUpFromBottom)
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

	model.modelWizard = newModelWizard(modelcfg.Config{Provider: "gateway"})
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
	if delta := bottomOffset - model.viewport.YOffset; delta != 3 {
		t.Fatalf("wheel delta = %d (YOffset %d -> %d), want 3", delta, bottomOffset, model.viewport.YOffset)
	}
}

func TestMouseWheelOverTranscriptDoesNotScrollInputViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 14
	model.input.SetValue("input 01\ninput 02\ninput 03\ninput 04\ninput 05\ninput 06\ninput 07\ninput 08")
	model.input.CursorEnd()
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
	beforeInput := model.input.View()

	next, _ := model.Update(tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	if got := model.input.View(); got != beforeInput {
		t.Fatalf("input view changed after transcript wheel:\nbefore:\n%s\nafter:\n%s", beforeInput, got)
	}
}

func TestHorizontalMouseWheelDoesNotEnterTranscriptOrInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.input.SetValue("draft")
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
	beforeOffset := model.viewport.YOffset

	for _, button := range []tea.MouseButton{tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight} {
		next, _ := model.Update(tea.MouseMsg{
			X:      10,
			Y:      3,
			Type:   tea.MouseEventType(button),
			Button: button,
			Action: tea.MouseActionPress,
		})
		model = next.(appModel)
	}
	if model.viewport.YOffset != beforeOffset {
		t.Fatalf("horizontal wheel changed transcript offset from %d to %d", beforeOffset, model.viewport.YOffset)
	}
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want draft unchanged", got)
	}
}

func TestArrowKeysAfterTranscriptWheelScrollViewportNotInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.inputHistory = []inputDraft{{Text: "old input"}}
	model.input.SetValue("draft")
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

	next, _ := model.Update(tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	offsetAfterWheel := model.viewport.YOffset
	if !model.transcriptKeyScrollActive {
		t.Fatalf("transcriptKeyScrollActive = false after transcript wheel")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want draft unchanged after transcript up", got)
	}
	if model.historyIndex != -1 {
		t.Fatalf("historyIndex = %d, want -1 after transcript up", model.historyIndex)
	}
	if model.viewport.YOffset >= offsetAfterWheel {
		t.Fatalf("YOffset = %d, want less than %d after transcript up", model.viewport.YOffset, offsetAfterWheel)
	}
}

func TestTypingClearsTranscriptKeyScrollFocus(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.input.SetValue("draft")
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

	next, _ := model.Update(tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	if !model.transcriptKeyScrollActive {
		t.Fatalf("transcriptKeyScrollActive = false after transcript wheel")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = next.(appModel)
	if model.transcriptKeyScrollActive {
		t.Fatalf("transcriptKeyScrollActive = true after typing")
	}
	if got := model.input.Value(); got != "draftx" {
		t.Fatalf("input value = %q, want draftx", got)
	}
}

// TestTypingScrollKeysDoesNotScrollTranscript 回归：输入框打字（空格/j/k/u/d/f/b
// 等字符）不得滚动 transcript。bubbles viewport 默认把这些字符键绑定为滚动键，
// 必须从 viewport KeyMap 中移除，避免输入字符时 transcript 被连带滚动。
func TestTypingScrollKeysDoesNotScrollTranscript(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.input.SetValue("")
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
	model.viewport.ScrollUp(5)
	startOffset := model.viewport.YOffset
	if startOffset <= 0 {
		t.Fatalf("test setup: YOffset = %d, want > 0", startOffset)
	}

	for _, key := range []string{" ", "j", "k", "u", "d", "f", "b"} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		model = next.(appModel)
		if model.viewport.YOffset != startOffset {
			t.Fatalf("typing %q scrolled transcript: YOffset %d -> %d", key, startOffset, model.viewport.YOffset)
		}
	}
	if got := model.input.Value(); got != " jkudfb" {
		t.Fatalf("input value = %q, want %q", got, " jkudfb")
	}
}

// TestClickTranscriptDoesNotActivateKeyScroll 回归：单击 transcript（选择/打开
// 等意图）不再把 ↑/↓ 置为 transcript 滚动焦点；只有鼠标滚轮滚动才进入该模式。
func TestClickTranscriptDoesNotActivateKeyScroll(t *testing.T) {
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

	model = mouseClickAt(model, 10, 3)
	if model.transcriptKeyScrollActive {
		t.Fatalf("transcript click must not activate key scroll mode")
	}
}

// TestClickInputDockExitsKeyScrollMode 回归：鼠标滚轮滚动 transcript 进入键盘
// 滚动焦点后，点击底部输入框应退出该模式，让 ↑/↓ 恢复为输入框内光标移动。
func TestClickInputDockExitsKeyScrollMode(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.input.SetValue("draft")
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

	next, _ := model.Update(tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	if !model.transcriptKeyScrollActive {
		t.Fatalf("transcriptKeyScrollActive = false after transcript wheel")
	}
	offsetAfterWheel := model.viewport.YOffset

	model = mouseClickAt(model, 10, model.height-2)
	if model.transcriptKeyScrollActive {
		t.Fatalf("transcriptKeyScrollActive = true after input dock click")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if model.viewport.YOffset != offsetAfterWheel {
		t.Fatalf("YOffset = %d, want unchanged %d after input up", model.viewport.YOffset, offsetAfterWheel)
	}
}

func TestFullWidthTranscriptMouseWheelScrollsViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.input.SetValue("keep me")
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
	beforeInput := model.input.Value()

	next, _ := model.Update(tea.MouseMsg{
		X:      model.width - 3,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)
	if model.viewport.YOffset >= bottomOffset {
		t.Fatalf("YOffset = %d, want less than %d when wheel is over full-width transcript", model.viewport.YOffset, bottomOffset)
	}
	if model.input.Value() != beforeInput {
		t.Fatalf("input value = %q, want unchanged %q", model.input.Value(), beforeInput)
	}
}

func TestRawMouseEscapeSequenceDoesNotEnterInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<64;59;25M[<64;59;25M[<65;56;40M")})
	model = next.(appModel)
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want raw mouse sequence ignored", got)
	}
}

func TestFragmentedRawMouseEscapeSequenceDoesNotEnterInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for _, chunk := range []string{"[", "<64;59;", "25M"} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chunk)})
		model = next.(appModel)
	}
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want fragmented raw mouse sequence ignored", got)
	}
}

func TestNormalBracketTextSurvivesRawMouseFragmentFilter(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for _, chunk := range []string{"[", "a"} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chunk)})
		model = next.(appModel)
	}
	if got := model.input.Value(); got != "draft[a" {
		t.Fatalf("input value = %q, want normal bracket text preserved", got)
	}
}

func TestRepeatedRawMouseBracketsDoNotEnterInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for i := 0; i < 12; i++ {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
		model = next.(appModel)
	}
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want repeated raw mouse brackets ignored", got)
	}
}

// Real trackpad scroll delivers leaked '[' fragments with irregular gaps that
// exceed any fixed burst window. The held prefix must accumulate across the
// gaps and be cleared by the next mouse event, never replayed as text.
func TestGappedRawMouseBracketBurstDoesNotEnterInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for i := 0; i < 6; i++ {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
		model = next.(appModel)
	}
	// A trailing MouseMsg (always present during real scroll) clears the
	// held prefix without flushing.
	next, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 10, Y: 10})
	model = next.(appModel)

	if got := model.input.Value(); got != "draft" {
		t.Fatalf("input value = %q, want gapped raw mouse brackets ignored", got)
	}
}

// A held '[' followed by ordinary typing replays the bracket so real input
// like "[a" survives the filter.
func TestHeldBracketReplayedBeforeOrdinaryText(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for _, chunk := range []string{"[", "a"} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chunk)})
		model = next.(appModel)
	}
	if got := model.input.Value(); got != "draft[a" {
		t.Fatalf("input value = %q, want held bracket replayed before ordinary text", got)
	}
}

func TestRawMouseEscapeDetectorDoesNotRejectNormalText(t *testing.T) {
	for _, text := range []string{"<tag>", "[not-mouse]", "<64;not;mouseM", "hello"} {
		if isRawMouseEscapeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}) {
			t.Fatalf("text %q was detected as raw mouse escape", text)
		}
		if isRawMouseEscapePrefix(text) {
			t.Fatalf("text %q was detected as raw mouse prefix", text)
		}
	}
}

func TestFullWidthTranscriptMouseClickStartsSelection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello",
	}}
	model.refreshViewport()

	next, _ := model.Update(tea.MouseMsg{
		X:      model.width - 3,
		Y:      3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	if !model.selecting || model.selectionActive {
		t.Fatalf("selection state = selecting:%v active:%v, want full-width transcript selection start", model.selecting, model.selectionActive)
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
	topY := 1 + model.currentLayout().headerHeight
	bottomY := 1 + model.currentLayout().headerHeight + maxInt(0, model.viewport.Height-1)

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
	if !strings.Contains(copied, "line 00") || !strings.Contains(copied, "✦") {
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
		X:      5,
		Y:      1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      7,
		Y:      1,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      7,
		Y:      1,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if copied != "llo" {
		t.Fatalf("copied selection = %q, want llo with the frameless transcript origin", copied)
	}
}

// TestTranscriptMouseDragSelectsExactCells 验证拖选按下点即选中点：点击哪个
// 字符就从哪个字符开始，不会多选左侧一个字符；中文宽字符按 grapheme 完整
// 选中，不会少选半个字符。回归：transcriptPointForMouse 曾把坐标左移一格，
// 导致拖选总是多带按下点左边一个字符（中英文都受影响）。
func TestTranscriptMouseDragSelectsExactCells(t *testing.T) {
	run := func(t *testing.T, body, want string, pressX, motionX int) {
		t.Helper()
		var copied string
		oldWriteClipboard := writeClipboard
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
			body:  body,
		}}
		model.refreshViewport()
		model.viewport.GotoTop()

		next, _ := model.Update(tea.MouseMsg{X: pressX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		model = next.(appModel)
		next, _ = model.Update(tea.MouseMsg{X: motionX, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
		model = next.(appModel)
		next, _ = model.Update(tea.MouseMsg{X: motionX, Y: 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
		model = next.(appModel)

		if copied != want {
			t.Fatalf("copied selection = %q, want %q", copied, want)
		}
	}

	// 英文：点击 'c'（cell 4）拖到 'e'（cell 6），恰好选中 cde，不多选 'b'。
	run(t, "abcde", "cde", mainContentPadding+4, mainContentPadding+6)
	// 中文：点击 '好'（cell 4-5）拖到 '世'（cell 6-7），完整选中 好世，
	// 不多选 '你'、不切开宽字符。
	run(t, "你好世界", "好世", mainContentPadding+5, mainContentPadding+7)
}

// TestTranscriptDragSelectsThroughLineEnd 验证拖到行尾时最后一个字符能被
// 选中（不会因为列坐标 clamp 到 width-1 再左移而少选行尾一个符号宽度）。
func TestTranscriptDragSelectsThroughLineEnd(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	const body = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" // 74 chars
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  body,
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	// 从第一个 'x'（cell 2）拖到视口最右列（行尾）。
	pressX := mainContentPadding + 2
	endX := mainContentPadding + model.viewport.Width - 1

	next, _ := model.Update(tea.MouseMsg{X: pressX, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: endX, Y: 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: endX, Y: 1, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	model = next.(appModel)

	if got := strings.Count(copied, "x"); got != len(body) {
		t.Fatalf("drag to line end copied %d x's, want %d (selection=%q)", got, len(body), copied)
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

// TestTranscriptDoubleClickSelectsWord 验证双击在按下瞬间建立词选区（按词边界
// 吸附、不切开中文/emoji），且双击不覆盖剪贴板。
func TestTranscriptDoubleClickSelectsWord(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour // 任意两次按下都落在双击窗口内
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	offset := strings.Index(lines[0].plain, "world")
	if offset < 0 {
		t.Fatalf("snapshot plain = %q, want world", lines[0].plain)
	}
	x := mainContentPadding + offset + 3 // 点击 "world" 内部（col = offset+2）
	y := model.transcriptScreenTop()

	press := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	next, _ := model.Update(press)
	model = next.(appModel)
	next, _ = model.Update(release) // 第一次单击：动作被延迟，这里丢弃
	model = next.(appModel)
	next, _ = model.Update(press) // 双击：建立词选区
	model = next.(appModel)
	if !model.selectionActive || model.selectionMode != selectionModeWord {
		t.Fatalf("double-click state = active:%v mode:%v", model.selectionActive, model.selectionMode)
	}
	next, _ = model.Update(release)
	model = next.(appModel)
	if got := model.selectedTranscriptText(); got != "world" {
		t.Fatalf("double-click selected %q, want world", got)
	}
	if copied != "" {
		t.Fatalf("double-click without drag must not copy, clipboard = %q", copied)
	}
}

// TestTranscriptDoubleClickSurvivesJitter 验证真实终端里双击第一次按下到抬起
// 之间常见的 1 格抖动（motion 事件、press/release 相差 1 格）不会触发复制，
// 也不会重置双击计数：第二次按下仍识别为双击并建立词选区。
func TestTranscriptDoubleClickSurvivesJitter(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour // 任意两次按下都落在双击窗口内
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	offset := strings.Index(lines[0].plain, "world")
	if offset < 0 {
		t.Fatalf("snapshot plain = %q, want world", lines[0].plain)
	}
	x := mainContentPadding + offset + 3 // 点击 "world" 内部（col = offset+2）
	y := model.transcriptScreenTop()

	// 第一次单击：press → motion(+1 格) → release(+1 格)，模拟按下期间手抖。
	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)

	if copied != "" {
		t.Fatalf("jittery single click copied %q, want no copy", copied)
	}
	if model.selectionActive || model.selectionMoved {
		t.Fatalf("jittery single click created selection: active=%v moved=%v", model.selectionActive, model.selectionMoved)
	}

	// 第二次按下：仍在双击窗口内且位置重合，应识别为双击并选中整个词。
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	if !model.selectionActive || model.selectionMode != selectionModeWord {
		t.Fatalf("double-click after jitter = active:%v mode:%v", model.selectionActive, model.selectionMode)
	}
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if got := model.selectedTranscriptText(); got != "world" {
		t.Fatalf("double-click after jitter selected %q, want world", got)
	}
	if copied != "" {
		t.Fatalf("double-click after jitter copied %q, want no copy", copied)
	}
}

// TestTranscriptDoubleClickRendersSelectionImmediately 验证双击建立词选区后
// 视口立即刷新，选区高亮立刻可见，不需要等下一次鼠标移动才出现。
func TestTranscriptDoubleClickRendersSelectionImmediately(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	offset := strings.Index(lines[0].plain, "world")
	if offset < 0 {
		t.Fatalf("snapshot plain = %q, want world", lines[0].plain)
	}
	x := mainContentPadding + offset + 3
	y := model.transcriptScreenTop()

	press := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	next, _ := model.Update(press)
	model = next.(appModel)
	next, _ = model.Update(release)
	model = next.(appModel)
	next, _ = model.Update(press) // 双击
	model = next.(appModel)
	next, _ = model.Update(release)
	model = next.(appModel)

	selSGR := sgrPrefixOf(selectedTranscriptLineStyle.Render(" "))
	if selSGR == "" {
		t.Fatal("selection SGR is empty under truecolor profile")
	}
	if !strings.Contains(model.viewport.View(), selSGR) {
		t.Fatalf("viewport after double-click = %q, want selection SGR %q visible immediately", model.viewport.View(), selSGR)
	}
}

// TestTranscriptDoubleClickDragExtendsByWord 验证双击选词后继续拖拽会按词边界
// 扩展选区，释放时复制完整词序列。
func TestTranscriptDoubleClickDragExtendsByWord(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world foo",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	worldOffset := strings.Index(lines[0].plain, "world")
	fooOffset := strings.Index(lines[0].plain, "foo")
	if worldOffset < 0 || fooOffset < 0 {
		t.Fatalf("snapshot plain = %q, want world and foo", lines[0].plain)
	}
	y := model.transcriptScreenTop()
	press := tea.MouseMsg{X: mainContentPadding + worldOffset + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: mainContentPadding + worldOffset + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	next, _ := model.Update(press)
	model = next.(appModel)
	next, _ = model.Update(release)
	model = next.(appModel)
	next, _ = model.Update(press) // 双击 "world"
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      mainContentPadding + fooOffset + 3,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionMotion,
	})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{
		X:      mainContentPadding + fooOffset + 3,
		Y:      y,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionRelease,
	})
	model = next.(appModel)

	if copied != "world foo" {
		t.Fatalf("word drag copied %q, want world foo", copied)
	}
	if !model.selectionActive {
		t.Fatal("word drag selection did not stay active")
	}
}

// TestTranscriptTripleClickSelectsLine 验证三击建立整行选区，释放不复制。
func TestTranscriptTripleClickSelectsLine(t *testing.T) {
	oldWriteClipboard := writeClipboard
	var copied string
	writeClipboard = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	oldInterval := doubleClickInterval
	doubleClickInterval = time.Hour
	defer func() {
		doubleClickInterval = oldInterval
	}()

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryUser,
		title: "you",
		body:  "alpha beta",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	x := mainContentPadding + 3
	y := model.transcriptScreenTop()
	press := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	for i := 0; i < 3; i++ {
		next, _ := model.Update(press)
		model = next.(appModel)
		next, _ = model.Update(release)
		model = next.(appModel)
	}

	if !model.selectionActive || model.selectionMode != selectionModeLine {
		t.Fatalf("triple-click state = active:%v mode:%v", model.selectionActive, model.selectionMode)
	}
	lines := model.transcriptLineSnapshots()
	if model.selectionStart != (selectionPoint{row: 0, col: 0}) || model.selectionEnd != (selectionPoint{row: 0, col: lines[0].width}) {
		t.Fatalf("line selection = %+v..%+v, want full line 0..%d", model.selectionStart, model.selectionEnd, lines[0].width)
	}
	if got := model.selectedTranscriptText(); !strings.Contains(got, "alpha beta") {
		t.Fatalf("triple-click selected %q, want line content", got)
	}
	if copied != "" {
		t.Fatalf("triple-click without drag must not copy, clipboard = %q", copied)
	}
}

// TestTranscriptDragWritesOSC52Clipboard 验证拖拽释放时除本地剪贴板外还会
// 通过 OSC 52 双写终端剪贴板（SSH/远程场景）。
func TestTranscriptDragWritesOSC52Clipboard(t *testing.T) {
	oldWriteClipboard := writeClipboard
	writeClipboard = func(text string) error { return nil }
	defer func() {
		writeClipboard = oldWriteClipboard
	}()

	oldOSC52 := writeClipboardOSC52
	var oscText string
	writeClipboardOSC52 = func(text string) tea.Cmd {
		oscText = text
		return nil
	}
	defer func() {
		writeClipboardOSC52 = oldOSC52
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

	next, _ := model.Update(tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: 7, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: 7, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)

	if oscText != "llo" {
		t.Fatalf("OSC 52 clipboard = %q, want llo", oscText)
	}
}

// TestTranscriptDragShowsCopyToast 验证拖拽复制后状态栏短暂显示
// 「已复制 N 字符」，到期后自动清除。
func TestTranscriptDragShowsCopyToast(t *testing.T) {
	oldWriteClipboard := writeClipboard
	writeClipboard = func(text string) error { return nil }
	defer func() {
		writeClipboard = oldWriteClipboard
	}()
	oldOSC52 := writeClipboardOSC52
	writeClipboardOSC52 = func(text string) tea.Cmd { return nil }
	defer func() {
		writeClipboardOSC52 = oldOSC52
	}()
	oldExpiry := scheduleCopyToastExpiry
	scheduleCopyToastExpiry = func() tea.Cmd {
		return func() tea.Msg { return copyToastExpiredMsg{} }
	}
	defer func() {
		scheduleCopyToastExpiry = oldExpiry
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

	next, _ := model.Update(tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: 7, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	next, cmd := model.Update(tea.MouseMsg{X: 7, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)

	if got := model.renderStatusLeftSegment(); !strings.Contains(ansi.Strip(got), "已复制 3 字符") {
		t.Fatalf("status after copy = %q, want 已复制 3 字符", got)
	}
	if cmd == nil {
		t.Fatalf("copy release returned no expiry command")
	}
	next, _ = model.Update(cmd())
	model = next.(appModel)
	got := ansi.Strip(model.renderStatusLeftSegment())
	if strings.Contains(got, "已复制") {
		t.Fatalf("status after expiry = %q, want toast cleared", got)
	}
	if got != "" {
		t.Fatalf("status after expiry = %q, want empty left segment", got)
	}
}

// TestSelectionStyleFallsBackToReverseIn16Color 验证 16 色及以下终端里选区
// 样式降级为反色渲染，与终端原生选区观感一致。
func TestSelectionStyleFallsBackToReverseIn16Color(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()

	if !selectedTranscriptLineStyle.GetReverse() {
		t.Fatalf("16-color selection style = %#v, want reverse video", selectedTranscriptLineStyle)
	}
	if _, hasBackground := selectedTranscriptLineStyle.GetBackground().(lipgloss.NoColor); !hasBackground {
		t.Fatalf("16-color selection background = %#v, want none", selectedTranscriptLineStyle.GetBackground())
	}
	if !toolFocusedStyle.GetReverse() || !toolFocusedStyle.GetBold() {
		t.Fatalf("16-color focused tool style = %#v, want reverse + bold", toolFocusedStyle)
	}
}

// TestSelectionStyleUsesThemeColorsInTrueColor 验证真彩色终端里选区样式使用
// 主题的 selection.background / selection.foreground，且不启用反色。
func TestSelectionStyleUsesThemeColorsInTrueColor(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()

	wantBG := colorManager.LipglossColor(colorSelectionBackground)
	wantFG := colorManager.LipglossColor(colorSelectionForeground)
	if got := selectedTranscriptLineStyle.GetBackground(); got != wantBG {
		t.Fatalf("selection background = %#v, want %#v", got, wantBG)
	}
	if got := selectedTranscriptLineStyle.GetForeground(); got != wantFG {
		t.Fatalf("selection foreground = %#v, want %#v", got, wantFG)
	}
	if selectedTranscriptLineStyle.GetReverse() {
		t.Fatal("true-color selection style must not use reverse video")
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
	model.selectionStart = selectionPoint{row: 0, col: 4}
	model.selectionEnd = selectionPoint{row: 0, col: 7}

	if got := model.selectedTranscriptText(); got != "你🙂" {
		t.Fatalf("selectedTranscriptText() = %q, want 你🙂", got)
	}
}

// TestModelCommandOpensArrowSelectableProviderWizard 验证 /model 会打开可用方向键选择的 provider 向导。
func TestModelCommandOpensArrowSelectableProviderWizard(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.inputHistory = []inputDraft{{Text: "old input"}}
	model.input.SetValue("/model")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.modelWizard == nil {
		t.Fatalf("/model should open wizard")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.modelWizard.selectedProvider().id != "backup" {
		t.Fatalf("selected provider = %q", model.modelWizard.selectedProvider().id)
	}
}

// TestModelWizardAppliesConfiguredProfile 验证向导会保存并应用配置文件中的 profile。
func TestModelWizardAppliesConfiguredProfile(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			ProfileID: "gateway",
			Provider:  "gateway",
			Model:     "gateway-model",
			Models:    []string{"gateway-model"},
			Profiles: []modelcfg.Profile{
				{ID: "gateway", Name: "Gateway", Provider: "gateway", Transport: "openai-compatible", APIBaseURL: "https://gateway.example/v1", APIPath: "/chat/completions", APIKey: "gateway-secret", Model: "gateway-model", Models: []string{"gateway-model"}},
				{ID: "anthropic", Name: "Anthropic Gateway", Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: "https://anthropic.example", APIPath: "/v1/messages", APIKey: "anthropic-secret", APIKeyEnvName: "ANTHROPIC_API_KEY", Model: "anthropic-model", Models: []string{"anthropic-model"}},
			},
			Timeout: time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 1

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard != nil {
		t.Fatalf("modelWizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 1 || len(controller.saved) != 1 {
		t.Fatalf("applied/saved = %#v / %#v", controller.applied, controller.saved)
	}
	cfg := controller.applied[0]
	if cfg.Provider != "anthropic-gateway" || cfg.APIBaseURL != "https://anthropic.example" || cfg.APIPath != "/v1/messages" || cfg.Model != "anthropic-model" {
		t.Fatalf("applied cfg = %#v", cfg)
	}
	if cfg.APIKey != "anthropic-secret" || cfg.APIKeyEnvName != "ANTHROPIC_API_KEY" {
		t.Fatalf("applied key/env = %q/%q", cfg.APIKey, cfg.APIKeyEnvName)
	}
}

// TestModelWizardUsesConfiguredAPIKey 验证向导使用配置文件中的 API key。
func TestModelWizardUsesConfiguredAPIKey(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			ProfileID:     "gateway",
			Provider:      "gateway",
			APIBaseURL:    "https://gateway.example/v1",
			APIPath:       "/chat/completions",
			APIKey:        "gateway-secret",
			APIKeyEnvName: "GATEWAY_API_KEY",
			Model:         "gateway-model",
			Models:        []string{"gateway-model"},
			Timeout:       time.Minute,
		},
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 0

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard != nil {
		t.Fatalf("modelWizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 1 {
		t.Fatalf("applied = %#v", controller.applied)
	}
	if got := controller.applied[0]; got.Provider != "gateway" || got.APIKey != "gateway-secret" {
		t.Fatalf("applied cfg = %#v", got)
	}
}

// TestModelWizardDoesNotApplyConfigWhenSaveFails 验证配置持久化失败时不会继续应用配置。
func TestModelWizardDoesNotApplyConfigWhenSaveFails(t *testing.T) {
	controller := &fakeModelConfigController{
		current: modelcfg.Config{
			Provider:      "gateway",
			APIBaseURL:    "https://gateway.example/v1",
			APIPath:       "/chat/completions",
			APIKey:        "custom-secret",
			APIKeyEnvName: "GATEWAY_API_KEY",
			Model:         "gateway-model",
			Models:        []string{"gateway-model"},
			Timeout:       time.Minute,
		},
		saveErr: errors.New("disk full"),
	}
	model := newModel(context.Background(), &fakeRunner{}, "session-1", controller, nil, nil, nil, newTerminalCursorAnchor())
	model.modelWizard = newModelWizard(controller.current)
	model.modelWizard.selectedIndex = 0

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)

	if model.modelWizard == nil || !strings.Contains(model.modelWizard.err, "disk full") {
		t.Fatalf("wizard = %#v", model.modelWizard)
	}
	if len(controller.applied) != 0 {
		t.Fatalf("applied configs = %#v", controller.applied)
	}
}

// TestRenderInputBoxShowsPureTextarea 验证输入面板只渲染 textarea 内容，不承载 context meter 或等待文案。
func TestRenderInputBoxShowsPureTextarea(t *testing.T) {
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

	model.pending = []inputDraft{{Text: "first"}}
	rendered = model.renderInputBox()
	if !strings.Contains(rendered, "hello") || strings.Contains(rendered, "0%(0.0%)") {
		t.Fatalf("multiline input box = %q", rendered)
	}

	model.running = true
	model.input.SetValue("keep typing")
	rendered = model.renderInputBox()
	for _, want := range []string{"keep typing"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("running input box = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{"waiting for assistant", "0%(0.0%)", "free(", "Input", "Waiting", "Terminal"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("running input box = %q, should not contain %q", rendered, unwanted)
		}
	}
}

func TestInputBoxExpandsForLongWrappedInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()

	if got := model.input.Height(); got != inputMinVisibleLines {
		t.Fatalf("empty input height = %d, want %d", got, inputMinVisibleLines)
	}

	singleLineHeight := lipgloss.Height(model.renderInputBox())
	wrappedWidth := maxInt(1, model.input.Width())
	model.input.SetValue(strings.Repeat("x", wrappedWidth*3+5))
	model.relayout()

	if got := model.input.Height(); got < 4 {
		t.Fatalf("input height = %d, want expansion for soft-wrapped long input", got)
	}
	if got := lipgloss.Height(model.renderInputBox()); got <= singleLineHeight {
		t.Fatalf("input box height = %d, want > %d", got, singleLineHeight)
	}

	model.input.SetValue(strings.Repeat("line\n", inputMaxVisibleLines+5))
	model.relayout()
	if got := model.input.Height(); got != inputMaxVisibleLines {
		t.Fatalf("input height = %d, want capped at %d", got, inputMaxVisibleLines)
	}
}

func TestInputMaxVisibleLinesRemainsTen(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.input.SetValue(strings.Join([]string{
		"line 01", "line 02", "line 03", "line 04", "line 05",
		"line 06", "line 07", "line 08", "line 09", "line 10",
		"line 11", "line 12", "line 13", "line 14", "line 15",
	}, "\n"))
	model.relayout()

	if inputMaxVisibleLines != 10 {
		t.Fatalf("inputMaxVisibleLines = %d, want 10", inputMaxVisibleLines)
	}
	if got := model.input.Height(); got != 10 {
		t.Fatalf("input height = %d, want 10", got)
	}
}

func TestInputAllowsMoreLogicalLinesThanVisibleLimit(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()

	for i := 1; i <= inputMaxVisibleLines+3; i++ {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fmt.Sprintf("line %02d", i))})
		model = next.(appModel)
		if i < inputMaxVisibleLines+3 {
			next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
			model = next.(appModel)
		}
	}

	if got := model.input.LineCount(); got != inputMaxVisibleLines+3 {
		t.Fatalf("line count = %d, want %d", got, inputMaxVisibleLines+3)
	}
	if got := model.input.Height(); got != inputMaxVisibleLines {
		t.Fatalf("input height = %d, want capped at %d", got, inputMaxVisibleLines)
	}
	if !strings.Contains(model.input.Value(), "line 13") {
		t.Fatalf("input value missing final logical line: %q", model.input.Value())
	}
}

func TestLargePastedInputFoldsInInputBoxButSubmitsFullText(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	pasted := strings.Join([]string{
		"line 01", "line 02", "line 03", "line 04",
		"line 05", "line 06", "line 07", "line 08",
	}, "\n")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted)})
	model = next.(appModel)

	if !model.inputPasteFoldActive {
		t.Fatalf("inputPasteFoldActive = false, want true")
	}
	rendered := ansi.Strip(model.renderInputBox())
	for _, want := range []string{"line 01", "line 03", "... 3 lines folded ...", "line 07", "line 08"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("folded input box missing %q:\n%s", want, rendered)
		}
	}
	for _, hidden := range []string{"line 04", "line 05", "line 06"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("folded input box should hide %q:\n%s", hidden, rendered)
		}
	}

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("enter after folded paste returned nil cmd")
	}
	_ = cmd()
	if len(runner.inputs) != 1 || runner.inputs[0] != pasted {
		t.Fatalf("runner.inputs = %#v, want full pasted text", runner.inputs)
	}
	if model.inputPasteFoldActive {
		t.Fatalf("inputPasteFoldActive = true after submit, want false")
	}
}

func TestFoldedPasteUnfoldsWhenCursorMovesIntoHiddenMiddle(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	pasted := strings.Join([]string{
		"line 01", "line 02", "line 03", "line 04",
		"line 05", "line 06", "line 07", "line 08",
	}, "\n")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted)})
	model = next.(appModel)
	if !model.shouldRenderFoldedInput() {
		t.Fatalf("folded input should render immediately after large paste")
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)

	if !model.inputPasteFoldActive {
		t.Fatalf("inputPasteFoldActive = false, want state retained")
	}
	if model.shouldRenderFoldedInput() {
		t.Fatalf("folded input should temporarily unfold when cursor is in hidden middle")
	}
	rendered := ansi.Strip(model.renderInputBox())
	for _, want := range []string{"line 04", "line 05", "line 06"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unfolded input box missing %q:\n%s", want, rendered)
		}
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
		body:  "## Title\n\n- one with `code`\n- **bold item**\n\n> quoted **bold quote**\n\n```go\nfmt.Println(\"hi\")\n```",
	}, 80)

	for _, want := range []string{"✦", "Title", "●", "one", "code", "bold item", "│", "quoted", "bold quote", "go", "fmt.Println"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(ansi.Strip(rendered), "## Title") {
		t.Fatalf("rendered markdown = %q, should not leave heading marker visible", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "code go") {
		t.Fatalf("rendered markdown = %q, should not prefix language labels with code", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "**bold") {
		t.Fatalf("rendered markdown = %q, should not leave bold markers visible", rendered)
	}
}

func TestMarkdownHeadingsHideSyntaxMarkers(t *testing.T) {
	const markdown = "# Level one\n## Level two\n### Level three\n#### Level four\n##### Level five\n###### Level six"
	rendered := ansi.Strip(renderMarkdown(markdown, 80))

	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Fatalf("rendered heading = %q, should not expose Markdown marker", line)
		}
	}
	for _, want := range []string{"Level one", "Level two", "Level three", "Level four", "Level five", "Level six"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered headings = %q, want %q", rendered, want)
		}
	}
}

func TestRenderInlineMarkdownBoldAndCodePrecedence(t *testing.T) {
	rendered := renderInlineMarkdown("**completion.go** uses `**raw**` and keeps **bold** text")
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"completion.go", "**raw**", "bold"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("inline markdown = %q, want %q", stripped, want)
		}
	}
	for _, hidden := range []string{"**completion.go**", "**bold**"} {
		if strings.Contains(stripped, hidden) {
			t.Fatalf("inline markdown = %q, should not contain parsed bold marker %q", stripped, hidden)
		}
	}
}

func TestRenderInlineMarkdownKeepsUnclosedBoldLiteral(t *testing.T) {
	rendered := renderInlineMarkdown("keep **unfinished marker")
	if got := ansi.Strip(rendered); got != "keep **unfinished marker" {
		t.Fatalf("inline markdown = %q, want literal unfinished marker", got)
	}
}

func TestRenderInlineMarkdownMakesBareURLClickableAndStyled(t *testing.T) {
	const target = "https://example.com/docs?q=terminal"
	rendered := renderInlineMarkdown("Open " + target + " now.")
	wantLink := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render(target) +
		ansi.ResetHyperlink()

	if !strings.Contains(rendered, wantLink) {
		t.Fatalf("inline markdown = %q, want styled OSC 8 link %q", rendered, wantLink)
	}
	if got := ansi.Strip(rendered); got != "Open "+target+" now." {
		t.Fatalf("stripped inline markdown = %q, want visible URL unchanged", got)
	}
	if got := ansi.StringWidth(rendered); got != len("Open ")+len(target)+len(" now.") {
		t.Fatalf("link display width = %d, want escape sequences to have zero width", got)
	}
	if !markdownLinkStyle.GetUnderline() || !markdownLinkStyle.GetBold() {
		t.Fatalf("link style must be underlined and bold")
	}
	if markdownLinkStyle.GetForeground() == bodyStyle.GetForeground() {
		t.Fatalf("link foreground must differ from body foreground")
	}
}

func TestRenderInlineMarkdownSupportsMarkdownLinksAndSkipsCode(t *testing.T) {
	const (
		target  = "https://example.com/guide"
		codeURL = "https://example.com/code"
	)
	rendered := renderInlineMarkdown("[terminal guide](" + target + ") and `" + codeURL + "`")
	wantLink := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render("terminal guide") +
		ansi.ResetHyperlink()

	if !strings.Contains(rendered, wantLink) {
		t.Fatalf("inline markdown = %q, want markdown OSC 8 link %q", rendered, wantLink)
	}
	if strings.Contains(rendered, ansi.SetHyperlink(codeURL)) {
		t.Fatalf("inline markdown = %q, code span URL should not be clickable", rendered)
	}
}

func TestRenderInlineMarkdownExcludesTrailingPunctuationFromURL(t *testing.T) {
	const target = "https://example.com/docs"
	rendered := renderInlineMarkdown("See " + target + ").")

	if !strings.Contains(rendered, ansi.SetHyperlink(target)) {
		t.Fatalf("inline markdown = %q, want target %q", rendered, target)
	}
	if strings.Contains(rendered, ansi.SetHyperlink(target+").")) {
		t.Fatalf("inline markdown = %q, trailing punctuation entered hyperlink", rendered)
	}
	if got := ansi.Strip(rendered); got != "See "+target+")." {
		t.Fatalf("stripped inline markdown = %q, want punctuation preserved", got)
	}
}

func TestRenderInlineMarkdownStopsURLAtChinesePunctuation(t *testing.T) {
	const target = "https://example.com/docs"
	rendered := renderInlineMarkdown("打开" + target + "，然后继续")

	if !strings.Contains(rendered, ansi.SetHyperlink(target)) {
		t.Fatalf("inline markdown = %q, want target %q", rendered, target)
	}
	if strings.Contains(rendered, ansi.SetHyperlink(target+"，然后继续")) {
		t.Fatalf("inline markdown = %q, Chinese punctuation entered hyperlink", rendered)
	}
	if got := ansi.Strip(rendered); got != "打开"+target+"，然后继续" {
		t.Fatalf("stripped inline markdown = %q, want punctuation preserved", got)
	}
}

func TestRenderInlineMarkdownKeepsBalancedURLParentheses(t *testing.T) {
	const target = "https://example.com/wiki/Function_(mathematics)"
	rendered := renderInlineMarkdown(target)
	if !strings.Contains(rendered, ansi.SetHyperlink(target)) {
		t.Fatalf("inline markdown = %q, want balanced-parenthesis target %q", rendered, target)
	}
}

func TestStreamingAssistantURLIsClickableBeforeMarkdownFinalization(t *testing.T) {
	const target = "https://example.com/live"
	rendered := renderEntryBody(transcriptEntry{
		kind: entryAssistant,
		body: "Live: " + target,
	}, 80)

	if !strings.Contains(rendered, ansi.SetHyperlink(target)) {
		t.Fatalf("streaming assistant body = %q, want clickable URL", rendered)
	}
}

func TestSystemOutputURLIsClickable(t *testing.T) {
	const target = "https://example.com/status"
	rendered := renderEntryBody(transcriptEntry{
		kind: entrySystem,
		body: "Status: " + target,
	}, 80)

	if !strings.Contains(rendered, ansi.SetHyperlink(target)) {
		t.Fatalf("system body = %q, want clickable URL", rendered)
	}
}

func TestTerminalHyperlinkAtPointTracksWrappedLink(t *testing.T) {
	const target = "https://example.com/wrapped"
	content := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render("first\nsecond") +
		ansi.ResetHyperlink()

	for _, point := range []selectionPoint{
		{row: 0, col: 2},
		{row: 1, col: 3},
	} {
		if got := terminalHyperlinkAtPoint(content, point); got != target {
			t.Fatalf("hyperlink at %+v = %q, want %q", point, got, target)
		}
	}
	if got := terminalHyperlinkAtPoint("plain text", selectionPoint{row: 0, col: 2}); got != "" {
		t.Fatalf("plain-text hyperlink = %q, want empty", got)
	}
}

func TestTranscriptMouseClickOpensURL(t *testing.T) {
	oldOpenTerminalURL := openTerminalURL
	var opened string
	openTerminalURL = func(target string) error {
		opened = target
		return nil
	}
	defer func() {
		openTerminalURL = oldOpenTerminalURL
	}()

	const target = "https://example.com/click"
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "Open " + target,
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	row := -1
	col := -1
	for index, line := range lines {
		if offset := strings.Index(line.plain, target); offset >= 0 {
			row = index
			col = terminalCellWidth(line.plain[:offset])
			break
		}
	}
	if row < 0 || col < 0 {
		t.Fatalf("rendered transcript does not contain %q", target)
	}

	x := mainContentPadding + col
	y := model.transcriptScreenTop() + row
	next, _ := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if cmd == nil {
		t.Fatal("URL click returned no deferred command")
	}
	// 单击动作延迟到双击窗口之后（测试里立即派发），处理后才产生打开命令。
	next, cmd = model.Update(cmd())
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("deferred URL click returned no open command")
	}
	cmd()
	if opened != target {
		t.Fatalf("opened URL = %q, want %q", opened, target)
	}
	if model.selectionActive {
		t.Fatal("URL click should not create a transcript selection")
	}
}

// TestMarkdownCodeBlockKeepsNestedMarkdownFencesInsideBlock 验证 markdown 包装 fence 内的 Markdown 会正常渲染，嵌套代码 fence 不会泄漏为普通文本。
func TestMarkdownCodeBlockKeepsNestedMarkdownFencesInsideBlock(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```markdown\n# 水獭的问候\n\n```go\nfunc hello() {\nfmt.Println(\"Hello!\")\n}\n```\n```", 80))

	for _, want := range []string{"水獭的问候", "func hello()", "fmt.Println", "╭", "╰"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "# 水獭的问候") || strings.Contains(rendered, "```go") {
		t.Fatalf("rendered markdown = %q, wrapper or nested fence syntax leaked", rendered)
	}
}

func TestMarkdownWrappedAnswerRendersHeadingsAndTaskLists(t *testing.T) {
	input := "```markdown\n### 根因\n\n- [] 检查 schema\n- [ ] 检查兼容性\n- [x] 修复问题\n```"
	rendered := ansi.Strip(renderMarkdown(input, 80))
	for _, want := range []string{"根因", "○ 检查 schema", "○ 检查兼容性", "✓ 修复问题"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("wrapped markdown = %q, want %q", rendered, want)
		}
	}
	for _, leaked := range []string{"### 根因", "- []", "- [ ]", "- [x]", "```markdown"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("wrapped markdown = %q, syntax leaked: %q", rendered, leaked)
		}
	}
}

func TestMarkdownWrappingUsesTerminalGraphemeWidth(t *testing.T) {
	const mixed = "中文 English 日本語 한국어 Русский العربية हिन्दी ภาษาไทย"
	const width = 52
	lines := wrapStyledCellLine(mixed, width)
	if len(lines) != 2 {
		t.Fatalf("wrapped lines=%q, want one trailing line at width %d", lines, width)
	}
	if got := strings.Join(lines, ""); got != mixed {
		t.Fatalf("wrapped text=%q, want original %q", got, mixed)
	}
	if got := terminalCellWidth(lines[0]); got != width {
		t.Fatalf("first line width=%d, want %d: %q", got, width, lines[0])
	}
	if got := terminalCellWidth(lines[1]); got != 2 {
		t.Fatalf("last line width=%d, want 2: %q", got, lines[1])
	}
}

// TestMarkdownTableRendersAsAlignedTable 验证 Markdown 表格会渲染为终端对齐表格。
func TestMarkdownTableRendersAsAlignedTable(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("| Name | Value |\n| :---: | ---: |\n| alpha | 1 |\n| beta | two |", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 7 {
		t.Fatalf("rendered table lines = %d, want 7:\n%s", len(lines), rendered)
	}

	for _, want := range []string{"Name", "Value", "alpha", "beta", "┌", "┬", "┼", "└", "┴", "─────"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered table = %q, want %q", rendered, want)
		}
	}
	for _, unwanted := range []string{":---", "---:"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("rendered table = %q, should not contain %q", rendered, unwanted)
		}
	}
	for i, line := range lines {
		if got := terminalCellWidth(line); got != terminalCellWidth(lines[0]) {
			t.Fatalf("table line %d width = %d, want %d: %q", i+1, got, terminalCellWidth(lines[0]), line)
		}
	}
	if !strings.Contains(lines[1], " Name ") || !strings.Contains(lines[1], " Value ") {
		t.Fatalf("header row = %q, want centered cells", lines[1])
	}
	if !strings.Contains(lines[3], " alpha ") || !strings.Contains(lines[3], " 1 ") {
		t.Fatalf("first body row = %q, want centered cells", lines[3])
	}
}

func TestMarkdownTableSeparatorVariantsDoNotLeak(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		leaked []string
	}{
		{
			name:   "em and en dash",
			input:  "| 模型 | 状态 |\n| ——— | ––– |\n| deepseek-reasoner-with-a-very-long-name | 可用且支持中文输出 |",
			leaked: []string{"———", "–––"},
		},
		{
			name:   "box drawing",
			input:  "| Name | Value |\n| ━━━ | ─── |\n| alpha-with-a-very-long-name | beta-with-a-very-long-value |",
			leaked: []string{"━━━"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const width = 24
			rendered := renderMarkdown(tc.input, width)
			for _, want := range []string{"────"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("rendered table = %q, want %q", rendered, want)
				}
			}
			for _, leaked := range tc.leaked {
				if strings.Contains(ansi.Strip(rendered), leaked) {
					t.Fatalf("rendered table = %q, should not leak separator %q", rendered, leaked)
				}
			}
			assertRenderedLineWidthsAtMost(t, rendered, width)
		})
	}
}

func TestMarkdownTableWrapsLongCellsWithoutTruncating(t *testing.T) {
	const input = "| 模型 | 说明 |\n| --- | --- |\n| Kimi k1.5 | 《Kimi k1.5: Scaling Reinforcement Learning with LLMs》论文详细介绍了其 RL 训练技术，并与 DeepSeek-R1 对比 |\n| Kimi K2 | 大规模 RL + 智能体数据合成 |"
	const width = 48

	rendered := ansi.Strip(renderMarkdown(input, width))
	if strings.Contains(rendered, "…") {
		t.Fatalf("wrapped table still contains truncation: %q", rendered)
	}
	content := strings.NewReplacer("┌", "", "─", "", "┬", "", "┐", "", "├", "", "┼", "", "┤", "", "└", "", "┴", "", "┘", "", "│", "", " ", "", "\n", "").Replace(rendered)
	for _, want := range []string{"《Kimi k1.5", "Scaling", "Reinforcement", "DeepSeek-R1", "智能体数据合成"} {
		if !strings.Contains(rendered, want) && !strings.Contains(content, strings.ReplaceAll(want, " ", "")) {
			t.Fatalf("wrapped table = %q, want full cell content containing %q", rendered, want)
		}
	}
	assertRenderedLineWidthsAtMost(t, rendered, width)
	if lines := strings.Split(rendered, "\n"); len(lines) <= 7 {
		t.Fatalf("wrapped table lines = %d, want long cells to occupy multiple lines:\n%s", len(lines), rendered)
	}
}

func TestMarkdownCodeBlockWrapsLongLinesWithinWidth(t *testing.T) {
	const width = 26
	rendered := renderMarkdown("```go\nfmt.Println(\"这是很长很长的中文内容\") // abcdefghijklmnopqrstuvwxyz\n```\n", width)
	stripped := ansi.Strip(rendered)

	for _, want := range []string{"go", "╭", "╮", "╰", "╯", "│", "fmt.Println"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered code block = %q, want %q", stripped, want)
		}
	}
	if strings.Contains(stripped, "code go") {
		t.Fatalf("rendered code block = %q, should not prefix language labels with code", stripped)
	}
	for _, unwanted := range []string{"▌"} {
		if strings.Contains(stripped, unwanted) {
			t.Fatalf("rendered code block = %q, should not contain fragile border %q", stripped, unwanted)
		}
	}
	assertRenderedLineWidthsAtMost(t, rendered, width)
	if got := maxRenderedLineWidth(stripped); got > width-4 {
		t.Fatalf("max rendered line width = %d, want <= %d to avoid terminal autowrap:\n%s", got, width-4, stripped)
	}
}

func TestMarkdownCodeBlockDoesNotForceFullViewportWidth(t *testing.T) {
	const width = 80
	rendered := renderMarkdown("```json\n{\"ok\": true}\n```", width)
	stripped := ansi.Strip(rendered)
	if got := maxRenderedLineWidth(stripped); got >= width/2 {
		t.Fatalf("max rendered line width = %d, want compact code block:\n%s", got, stripped)
	}
	for _, want := range []string{"╭", "╮", "╰", "╯", "│", "{\"ok\": true}"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered code block = %q, want %q", stripped, want)
		}
	}
}

// TestMarkdownCodeBlockKeepsLanguageLabelOnTopBorder 验证语言标签嵌入代码块顶边。
func TestMarkdownCodeBlockKeepsLanguageLabelOnTopBorder(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```json\n{\"ok\": true}\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	if !strings.Contains(lines[0], " json ") || !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Fatalf("top border = %q, want centered json label", lines[0])
	}
	if strings.Contains(lines[1], "json") {
		t.Fatalf("body line = %q, should not repeat language label", lines[1])
	}
	if !strings.Contains(rendered, "{\"ok\": true}") {
		t.Fatalf("rendered code block = %q, want json body", rendered)
	}
}

func TestMarkdownCodeBlockShowsOnlyLanguageLabel(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```python\ndef hanoi():\n    pass\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 || !strings.Contains(lines[0], " python ") {
		t.Fatalf("rendered code block = %q, want python label on top border", rendered)
	}
	if strings.Contains(rendered, "code python") {
		t.Fatalf("rendered code block = %q, should not include code prefix", rendered)
	}
}

func TestMarkdownCodeBlockCentersLanguageLabel(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```text\nhello\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	const chip = " text "
	start := strings.Index(lines[0], chip)
	if start < 0 {
		t.Fatalf("top border = %q, want %q", lines[0], chip)
	}
	leftWidth := terminalCellWidth(lines[0][:start])
	rightWidth := terminalCellWidth(lines[0][start+len(chip):])
	diff := leftWidth - rightWidth
	if diff < -1 || diff > 1 {
		t.Fatalf("top border is not centered: left=%d right=%d line=%q", leftWidth, rightWidth, lines[0])
	}
}

func TestMarkdownCodeBlockOmitsDefaultLabelWithoutLanguage(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```\nhello\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	if strings.Contains(lines[0], "code") || strings.Contains(rendered, "code hello") {
		t.Fatalf("rendered code block = %q, should not invent a code label", rendered)
	}
}

func TestMarkdownCodeBlockTruncatesLongLanguageLabelWithinWidth(t *testing.T) {
	const width = 12
	rendered := renderMarkdown("```this-is-a-very-long-language\nx\n```", width)
	stripped := ansi.Strip(rendered)
	lines := strings.Split(stripped, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), stripped)
	}
	assertRenderedLineWidthsAtMost(t, rendered, width)
	if !strings.Contains(lines[0], "…") {
		t.Fatalf("top border = %q, want truncated language marker", lines[0])
	}
}

func TestMarkdownCodeBlockLabelStyleUsesSignalCyan(t *testing.T) {
	if got := markdownCodeBlockLabelStyle.GetBackground(); got != colorManager.LipglossColor(colorSignal) {
		t.Fatalf("label background = %#v, want %#v", got, colorManager.LipglossColor(colorSignal))
	}
	if got := markdownCodeBlockLabelStyle.GetForeground(); got != colorManager.LipglossColor(colorTerminalBackground) {
		t.Fatalf("label foreground = %#v, want %#v", got, colorManager.LipglossColor(colorTerminalBackground))
	}
	if !markdownCodeBlockLabelStyle.GetBold() {
		t.Fatal("label style must be bold")
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
	defer output.Close()
	anchor.setVisual(terminalCursorVisual{color: "#7aa2f7", visible: true})

	anchor.set(terminalCursorPosition{active: true, upFromBottom: 1, column: 7, background: "#1a1b26"})
	if _, err := output.Write([]byte("frame1\r")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	anchor.set(terminalCursorPosition{active: true, upFromBottom: 2, column: 3, background: "#1a1b26"})
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
	activate1 := "\r\x1b[1A\x1b[7C" + terminalBackgroundSequence("#1a1b26") + terminalCursorColorSequence("#7aa2f7") + terminalCursorShow
	restore1 := "\x1b[1B\r" + terminalSGRReset
	activate2 := "\r\x1b[2A\x1b[3C" + terminalBackgroundSequence("#1a1b26") + terminalCursorColorSequence("#7aa2f7") + terminalCursorShow
	want := "frame1\r" + activate1 + restore1 + "frame2\r" + activate2
	if got := string(data); got != want {
		t.Fatalf("anchored output = %q, want %q", got, want)
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

// TestResumeCommandOpensPicker 验证 /resume 命令打开 sessionPicker。
func TestResumeCommandOpensPicker(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	handled, _ := model.handleCommand("/resume")
	if !handled {
		t.Fatalf("/resume not handled")
	}
	if model.sessionPicker == nil {
		t.Fatalf("sessionPicker = nil, want non-nil after /resume")
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
	runner := &fakeRunner{loadHistoryMsgs: []message.Message{
		{Role: message.RoleUser, Content: "restored prompt"},
		{Role: message.RoleAssistant, Content: "restored answer"},
	}}
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
	if len(restored.entries) != 2 || restored.entries[0].body != "restored prompt" || restored.entries[1].body != "restored answer" {
		t.Fatalf("sessionRestoredMsg.entries = %#v, want converted session history", restored.entries)
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

func TestSessionPickerRestoreReplacesTranscriptAndViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "old conversation"}}
	model.refreshViewport()

	next, _ := model.Update(sessionRestoredMsg{
		sessionID: "replacement",
		entries:   []transcriptEntry{{kind: entryUser, title: "you", body: "restored conversation"}},
	})
	model = next.(appModel)

	rendered := ansi.Strip(model.viewport.View())
	if !strings.Contains(rendered, "restored conversation") {
		t.Fatalf("viewport = %q, want restored history", rendered)
	}
	if strings.Contains(rendered, "old conversation") {
		t.Fatalf("viewport = %q, retained previous session history", rendered)
	}
}

func TestSessionPickerRestoreClearsTranscriptForEmptyHistory(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.transcript = []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "old conversation"}}
	model.refreshViewport()

	next, _ := model.Update(sessionRestoredMsg{sessionID: "empty-session"})
	model = next.(appModel)

	rendered := ansi.Strip(model.viewport.View())
	if strings.Contains(rendered, "old conversation") {
		t.Fatalf("viewport = %q, retained previous session history for empty session", rendered)
	}
	if !strings.Contains(rendered, "已切换到会话: empty-session") {
		t.Fatalf("viewport = %q, want session switch status", rendered)
	}
}

func TestSessionRestoreRebuildsInputHistoryAndDropsPreviousSession(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.inputHistory = []inputDraft{{Text: "old session input"}}

	next, _ := model.Update(sessionRestoredMsg{
		sessionID: "target-session",
		entries: []transcriptEntry{
			{kind: entryUser, title: "you", body: "restored prompt"},
			{kind: entryAssistant, title: "assistant", body: "restored answer"},
		},
	})
	model = next.(appModel)
	if len(model.inputHistory) != 1 || model.inputHistory[0].Text != "restored prompt" {
		t.Fatalf("inputHistory = %#v, want restored prompt only", model.inputHistory)
	}

	model.running = false
	next, _ = model.handleHistoryNavigation(-1)
	model = next.(appModel)
	if got := model.input.Value(); got != "restored prompt" {
		t.Fatalf("recalled input = %q, want restored prompt", got)
	}
}

func TestSessionRestoreClearsInputHistoryForEmptySession(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.inputHistory = []inputDraft{{Text: "old session input"}}

	next, _ := model.Update(sessionRestoredMsg{sessionID: "empty-session"})
	model = next.(appModel)
	if len(model.inputHistory) != 0 {
		t.Fatalf("inputHistory = %#v, want empty after restore", model.inputHistory)
	}
	next, _ = model.handleHistoryNavigation(-1)
	model = next.(appModel)
	if model.input.Value() != "" {
		t.Fatalf("input after empty-session history navigation = %q", model.input.Value())
	}
}

func TestRestoredSkillReferenceRebuildsTranscriptAndInputTokens(t *testing.T) {
	raw := "[$design](/tmp/design/SKILL.md)"
	entries := transcriptEntriesFromMessage(message.Message{Role: message.RoleUser, Content: raw}, time.Now(), "")
	if len(entries) != 1 || len(entries[0].inputTokens) != 1 {
		t.Fatalf("restored entries = %#v, want one user skill token", entries)
	}

	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(sessionRestoredMsg{sessionID: "restored", entries: entries})
	model = next.(appModel)
	if len(model.inputHistory) != 1 || len(model.inputHistory[0].Tokens) != 1 {
		t.Fatalf("restored inputHistory = %#v, want one token", model.inputHistory)
	}
	model.running = false
	next, _ = model.handleHistoryNavigation(-1)
	model = next.(appModel)
	if model.input.Value() != raw || len(model.inputTokens) != 1 || model.inputTokens[0].Label != "design" {
		t.Fatalf("recalled restored skill = value:%q tokens:%#v", model.input.Value(), model.inputTokens)
	}
}

func TestCtrlGtaskPickerEnterPreviewsSelectedtask(t *testing.T) {
	transcriptPath := filepath.Join(t.TempDir(), "agent-2.jsonl")
	transcript := strings.Join([]string{
		`{"seq":0,"message":{"role":"user","content":"task prompt"},"created_at":"2026-06-28T00:00:00Z"}`,
		`{"seq":1,"message":{"role":"assistant","content":"task answer"},"created_at":"2026-06-28T00:00:01Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
		{ID: "agent-1", SessionID: "agent-1", ParentSessionID: "session-1", Status: taskpkg.TaskCompleted, Description: "first"},
		{ID: "agent-2", SessionID: "agent-2", ParentSessionID: "session-1", Status: taskpkg.TaskRunning, Description: "second", TranscriptPath: transcriptPath},
	}}
	model.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "main draft"}}
	model.input.SetValue("main input")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = next.(appModel)
	if model.taskPicker == nil {
		t.Fatalf("taskPicker = nil, want picker")
	}
	if got := model.input.Value(); got != "main input" {
		t.Fatalf("input value after ctrl+g = %q, want preserved", got)
	}
	inputPanel := ansi.Strip(model.renderActiveInputPanel())
	for _, hidden := range []string{"Tasks", "first", "second"} {
		if strings.Contains(inputPanel, hidden) {
			t.Fatalf("input panel = %q, should not render task picker text %q", inputPanel, hidden)
		}
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.taskPicker.selectedIndex != 1 {
		t.Fatalf("selectedIndex = %d, want second task", model.taskPicker.selectedIndex)
	}

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("enter on task picker returned nil cmd")
	}
	next, _ = model.Update(cmd())
	model = next.(appModel)

	if model.sessionID != "session-1" {
		t.Fatalf("sessionID = %q, want main session", model.sessionID)
	}
	if model.taskPreview == nil || model.taskPreview.parentSessionID != "session-1" {
		t.Fatalf("taskPreview = %#v, want parent session-1", model.taskPreview)
	}
	if got := runner.loadHistoryCalls; len(got) != 0 {
		t.Fatalf("loadHistoryCalls = %#v, want no runner history switch", got)
	}
	rendered := renderTranscript(model.transcript, 80, true)
	if !strings.Contains(rendered, "task") ||
		!strings.Contains(rendered, "task prompt") ||
		!strings.Contains(rendered, "task answer") {
		t.Fatalf("rendered transcript = %q, want selected task history", rendered)
	}
	if got := model.input.Value(); got != "main input" {
		t.Fatalf("input value = %q, want preserved main input", got)
	}

	next, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatalf("main submit while previewing returned nil cmd")
	}
	_ = cmd()
	if model.taskPreview != nil {
		t.Fatalf("taskPreview = %#v, want nil after main submit", model.taskPreview)
	}
	if got := runner.inputs; !equalStrings(got, []string{"main input"}) {
		t.Fatalf("runner.inputs = %#v, want main input", got)
	}
}

func TestTaskPickerEscAndCtrlGCloseSelection(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlG} {
		model := newTestModel(&fakeRunner{})
		model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
			{ID: "agent-1", SessionID: "agent-1", ParentSessionID: "session-1", Status: taskpkg.TaskCompleted},
		}}
		model.input.SetValue("main input")

		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
		model = next.(appModel)
		if model.taskPicker == nil {
			t.Fatalf("key %v: taskPicker = nil after ctrl+g", key)
		}

		next, _ = model.Update(tea.KeyMsg{Type: key})
		model = next.(appModel)
		if model.taskPicker != nil {
			t.Fatalf("key %v: taskPicker = %#v, want nil", key, model.taskPicker)
		}
		if got := model.input.Value(); got != "main input" {
			t.Fatalf("key %v: input value = %q, want preserved", key, got)
		}
		if !model.input.Focused() {
			t.Fatalf("key %v: input should remain focused", key)
		}
	}
}

func TestEscReturnsFromtaskPreviewToMainTranscript(t *testing.T) {
	mainTranscript := []transcriptEntry{{kind: entryUser, title: "you", body: "main message"}}
	model := newTestModel(&fakeRunner{})
	model.sessionID = "session-1"
	model.viewport.Width = 80
	model.viewport.Height = 2
	model.taskPreview = &taskTranscriptPreview{
		task:             taskpkg.TaskSnapshot{ID: "agent-2", SessionID: "agent-2"},
		parentSessionID:  "session-1",
		parentTranscript: copyTranscriptEntries(mainTranscript),
	}
	model.input.SetValue("main input")
	model.transcript = []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "task message"}}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)

	if model.sessionID != "session-1" {
		t.Fatalf("sessionID = %q, want session-1", model.sessionID)
	}
	if model.taskPreview != nil {
		t.Fatalf("taskPreview = %#v, want nil", model.taskPreview)
	}
	if len(model.transcript) != 1 || model.transcript[0].body != "main message" {
		t.Fatalf("transcript = %#v, want restored main transcript", model.transcript)
	}
	if got := model.input.Value(); got != "main input" {
		t.Fatalf("input value = %q, want preserved", got)
	}
	if !model.viewport.AtBottom() {
		t.Fatalf("viewport should return to bottom")
	}
}

// TestCtrlGTogglestaskPreviewClosed 验证 task preview 中按 ctrl+g
// 直接收起面板并返回主 transcript（ctrl+g 全局 toggle 语义）。
func TestCtrlGTogglestaskPreviewClosed(t *testing.T) {
	mainTranscript := []transcriptEntry{{kind: entryUser, title: "you", body: "main message"}}
	model := newTestModel(&fakeRunner{})
	model.sessionID = "session-1"
	model.viewport.Width = 80
	model.viewport.Height = 2
	model.taskPreview = &taskTranscriptPreview{
		task:             taskpkg.TaskSnapshot{ID: "agent-2", SessionID: "agent-2"},
		parentSessionID:  "session-1",
		parentTranscript: copyTranscriptEntries(mainTranscript),
	}
	model.input.SetValue("main input")
	model.transcript = []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "task message"}}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = next.(appModel)

	if model.sessionID != "session-1" {
		t.Fatalf("sessionID = %q, want session-1", model.sessionID)
	}
	if model.taskPreview != nil {
		t.Fatalf("taskPreview = %#v, want nil after ctrl+g toggle", model.taskPreview)
	}
	if model.taskPicker != nil {
		t.Fatalf("taskPicker = %#v, want nil after ctrl+g toggle", model.taskPicker)
	}
	if len(model.transcript) != 1 || model.transcript[0].body != "main message" {
		t.Fatalf("transcript = %#v, want restored main transcript", model.transcript)
	}
	if got := model.input.Value(); got != "main input" {
		t.Fatalf("input value = %q, want preserved", got)
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

// TestDollarPrefixTriggersSkillCompletion 验证输入 $ 前缀触发 skill 补全。
func TestDollarPrefixTriggersSkillCompletion(t *testing.T) {
	root := t.TempDir()
	writeBubbleTestSkill(t, root, "design", "# Design\n")
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})

	model.input.SetValue("$")
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model = next.(appModel)

	if model.completion == nil {
		t.Fatalf("completion = nil, want skill completion after $ input")
	}
	if model.completion.kind != completionKindSkill {
		t.Fatalf("completion.kind = %v, want completionKindSkill", model.completion.kind)
	}
	if got := model.completion.visibleItems(); len(got) != 1 || got[0] != "design" {
		t.Fatalf("visible skill items = %#v, want design", got)
	}
}

func TestSlashWordBoundaryTriggersAndFiltersCommandCompletion(t *testing.T) {
	root := t.TempDir()
	writeBubbleTestSkill(t, root, "design", "# Design\n")

	tests := []struct {
		name       string
		value      string
		wantOpen   bool
		wantItems  []string
		rejectItem string
	}{
		{name: "line start", value: "/", wantOpen: true, wantItems: []string{"/help", "/design"}},
		{name: "space boundary", value: "已有内容 /", wantOpen: true, wantItems: []string{"/task", "/streamma", "/design"}, rejectItem: "/help"},
		{name: "multiple whitespace boundary", value: "已有内容 \t /", wantOpen: true, wantItems: []string{"/task", "/streamma", "/design"}, rejectItem: "/help"},
		{name: "query filters current slash word", value: "已有内容 /de", wantOpen: true, wantItems: []string{"/design"}, rejectItem: "/help"},
		{name: "ordinary word slash", value: "abc/", wantOpen: false},
		{name: "url slash", value: "https://example.com/", wantOpen: false},
		{name: "path slash", value: "path/to/", wantOpen: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.skillRegistry = skill.NewRegistry([]string{root})
			model.input.SetValue(test.value)
			model.syncCommandCompletion()

			if !test.wantOpen {
				if model.completion != nil {
					t.Fatalf("completion = %#v, want nil for %q", model.completion, test.value)
				}
				return
			}
			if model.completion == nil || model.completion.kind != completionKindCommand {
				t.Fatalf("completion = %#v, want command completion for %q", model.completion, test.value)
			}
			items := model.completion.visibleItems()
			for _, want := range test.wantItems {
				if !containsString(items, want) {
					t.Fatalf("items = %#v, want %q", items, want)
				}
			}
			if test.rejectItem != "" && containsString(items, test.rejectItem) {
				t.Fatalf("items = %#v, do not want %q", items, test.rejectItem)
			}
		})
	}
}

func TestInlineSlashCompletionKeepsPromptCommandsAndAllSkills(t *testing.T) {
	root := t.TempDir()
	writeBubbleTestSkill(t, root, "design", "# Design\n")
	writeBubbleTestSkill(t, root, "review", "# Review\n")

	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("请分析这个问题 /")
	model.syncCommandCompletion()

	if model.completion == nil {
		t.Fatal("completion = nil, want inline slash completion")
	}
	items := model.completion.visibleItems()
	for _, want := range []string{"/task", "/streamma", "/design", "/review"} {
		if !containsString(items, want) {
			t.Fatalf("items = %#v, want %q", items, want)
		}
	}
	for _, unwanted := range []string{"/help", "/model", "/export", "/setting", "/streamma-trace", "/tasks", "/skills", "/status", "/clear", "/resume", "/exit"} {
		if containsString(items, unwanted) {
			t.Fatalf("items = %#v, do not want inline command %q", items, unwanted)
		}
	}
}

func TestInlineSlashCompletionFiltersSkillsWithoutRestoringOtherCommands(t *testing.T) {
	root := t.TempDir()
	writeBubbleTestSkill(t, root, "design", "# Design\n")

	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("已有内容 /de")
	model.syncCommandCompletion()

	if model.completion == nil {
		t.Fatal("completion = nil, want matching skill")
	}
	items := model.completion.visibleItems()
	if !containsString(items, "/design") {
		t.Fatalf("items = %#v, want /design", items)
	}
	if containsString(items, "/help") || containsString(items, "/model") {
		t.Fatalf("items = %#v, inline completion restored unrelated commands", items)
	}
}

func TestSlashPrefixIncludesSkillCompletion(t *testing.T) {
	root := t.TempDir()
	writeBubbleTestSkill(t, root, "multi-agent-pipeline", "# Pipeline\n")
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})

	model.input.SetValue("/m")
	model.syncCommandCompletion()

	if model.completion == nil {
		t.Fatalf("completion = nil, want command completion")
	}
	if model.completion.kind != completionKindCommand {
		t.Fatalf("completion.kind = %v, want completionKindCommand", model.completion.kind)
	}
	items := model.completion.visibleItems()
	if !containsString(items, "/model") || !containsString(items, "/multi-agent-pipeline") {
		t.Fatalf("visible command items = %#v, want /model and /multi-agent-pipeline", items)
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

func TestCompletionEscRelayoutsImmediately(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.input.SetValue("@readme")
	model.completion = &completion{
		kind:          completionKindFile,
		filteredItems: []string{"readme.md"},
		loading:       false,
	}
	model.relayout()
	withCompletionHeight := model.viewport.Height

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.completion != nil {
		t.Fatalf("completion = %#v, want nil after esc", model.completion)
	}
	if model.viewport.Height != withCompletionHeight {
		t.Fatalf("viewport height = %d, want unchanged %d after completion closes", model.viewport.Height, withCompletionHeight)
	}
}

// TestCompletionTabAppliesSelection 验证 tab 键应用选中的补全项并清除弹窗。
func TestCompletionTabAppliesSelection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.completion = &completion{
		kind:          completionKindCommand,
		items:         []string{"/help", "/model", "/resume"},
		selectedIndex: 0,
		loading:       false,
	}
	model.relayout()
	withCompletionHeight := model.viewport.Height

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if model.completion != nil {
		t.Fatalf("completion = %#v, want nil after tab", model.completion)
	}
	if got := model.input.Value(); got != "/help " {
		t.Fatalf("input value = %q, want /help after tab completion", got)
	}
	if model.viewport.Height != withCompletionHeight {
		t.Fatalf("viewport height = %d, want unchanged %d after tab completion", model.viewport.Height, withCompletionHeight)
	}
}

func TestSkillCompletionTabAppliesMarkdownReference(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "design", "# Design\n")
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("$de")
	model.input.CursorEnd()
	model.completion = &completion{
		kind:          completionKindSkill,
		items:         []string{"design"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	if model.completion != nil {
		t.Fatalf("completion = %#v, want nil after tab", model.completion)
	}
	want := "[$design](" + path + ") "
	if got := model.input.Value(); got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

func TestSlashSkillCompletionTabAppliesMarkdownReference(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "multi-agent-pipeline", "# Pipeline\n")
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.completion = &completion{
		kind:          completionKindCommand,
		items:         []string{"/multi-agent-pipeline"},
		selectedIndex: 0,
		loading:       false,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)
	want := "[$multi-agent-pipeline](" + path + ") "
	if got := model.input.Value(); got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

func TestCompletionBoxAlignsWithInputBoxWidth(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.completion = &completion{
		kind:    completionKindCommand,
		items:   []string{"/model"},
		loading: false,
	}
	model.relayout()

	completionWidth := lipgloss.Width(model.renderCompletionBox())
	inputWidth := lipgloss.Width(model.renderInputBox())
	if completionWidth <= 0 || completionWidth > inputWidth {
		t.Fatalf("completion width = %d, want within input width %d", completionWidth, inputWidth)
	}
}

func TestCompletionBackspaceRelayoutsWhenTriggerDeleted(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		completion *completion
	}{
		{
			name:  "file trigger",
			value: "@",
			completion: &completion{
				kind:          completionKindFile,
				filteredItems: []string{"readme.md"},
				loading:       false,
			},
		},
		{
			name:       "command trigger",
			value:      "/",
			completion: newCommandCompletion("", NewCommandRegistry()),
		},
		{
			name:  "skill trigger",
			value: "$",
			completion: &completion{
				kind:    completionKindSkill,
				items:   []string{"design"},
				loading: false,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.ready = true
			model.width = 80
			model.height = 20
			model.input.SetValue(tc.value)
			model.input.CursorEnd()
			model.completion = tc.completion
			model.relayout()
			withCompletionHeight := model.viewport.Height

			next, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			model = next.(appModel)
			if model.completion != nil {
				t.Fatalf("completion = %#v, want nil after deleting trigger", model.completion)
			}
			if model.viewport.Height != withCompletionHeight {
				t.Fatalf("viewport height = %d, want unchanged %d after trigger deletion", model.viewport.Height, withCompletionHeight)
			}
		})
	}
}

// TestFormatSessionLabel_SizeDisplay 验证会话标签保留日期、大小和摘要但不展示 session ID。
func TestFormatSessionLabel_SizeDisplay(t *testing.T) {
	base := time.Date(2024, 3, 15, 9, 8, 7, 0, time.UTC)

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
				lastUsedAt:     base,
				firstMessage:   "hello",
				transcriptSize: tc.size,
			}
			label := formatSessionLabel(item)
			if !strings.Contains(label, tc.wantSize) {
				t.Fatalf("formatSessionLabel() = %q, want size %q", label, tc.wantSize)
			}
			if strings.Contains(label, "abcdef1234567890") || strings.Contains(label, "abcdef12") {
				t.Fatalf("formatSessionLabel() = %q, must not expose session ID", label)
			}
		// 时间戳按本地时区显示（落库为 UTC，换算后再断言，时区无关）。
		if !strings.Contains(label, base.Local().Format("2006-01-02 15:04:05")) {
			t.Fatalf("formatSessionLabel() = %q, want creation timestamp", label)
		}
		})
	}

	first := formatSessionLabel(sessionSummaryItem{lastUsedAt: base, firstMessage: "same"})
	second := formatSessionLabel(sessionSummaryItem{lastUsedAt: base.Add(time.Second), firstMessage: "same"})
	if first == second {
		t.Fatalf("same-summary sessions need a non-ID discriminator: %q", first)
	}
}

// TestHelpIncludesResumeCommand 验证 /help 输出包含 /resume 且不再包含 /sessions。
func TestHelpIncludesResumeCommand(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	handled, cmd := model.handleCommand("/help")
	if !handled || cmd != nil {
		t.Fatalf("/help handled/cmd = %v/%v", handled, cmd)
	}
	body := model.transcript[len(model.transcript)-1].body
	if !strings.Contains(body, "/resume") {
		t.Fatalf("help body = %q, want /resume", body)
	}
	if strings.Contains(body, "/sessions") {
		t.Fatalf("help body = %q, should not contain /sessions", body)
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
	model.ready = true
	model.width = 80
	model.height = 20
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
	model.relayout()
	withCompletionHeight := model.viewport.Height

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)

	// 文件：Tab 加尾部空格，与 Enter 一致
	if got := model.input.Value(); got != "请引用 @readme.md " {
		t.Errorf("input value = %q, want '请引用 @readme.md ' (trailing space for file)", got)
	}
	if model.completion != nil {
		t.Errorf("completion should be nil after tab on file, got %#v", model.completion)
	}
	if model.viewport.Height != withCompletionHeight {
		t.Fatalf("viewport height = %d, want unchanged %d after file tab completion", model.viewport.Height, withCompletionHeight)
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
	model.ready = true
	model.width = 80
	model.height = 20
	model.input.SetValue("请引用 @re")
	model.completion = &completion{
		kind:          completionKindFile,
		atByteIndex:   len("请引用 "),
		query:         "re",
		filteredItems: []string{"readme.md"},
		selectedIndex: 0,
		loading:       false,
	}
	model.relayout()
	withCompletionHeight := model.viewport.Height

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
	if model.viewport.Height != withCompletionHeight {
		t.Fatalf("viewport height = %d, want unchanged %d after file enter completion", model.viewport.Height, withCompletionHeight)
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

func TestEscDoesNotQuitTUI(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		return
	}
	if _, ok := cmd().(tea.QuitMsg); ok {
		t.Fatalf("esc should not quit TUI")
	}
}

// TestFilterByPrefix 验证大小写不敏感的文件名过滤。
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

func TestFilterByPrefixMatchesExtensionAndSubstring(t *testing.T) {
	items := []string{
		"README.md",
		"docs/guide.md",
		"internal/ui/bubble/completion.go",
		"internal/ui/bubble/bubble_test.go",
		"scripts/testdata/fixture.txt",
	}

	for _, item := range []string{"README.md", "docs/guide.md"} {
		if got := filterByPrefix(items, "md"); !containsString(got, item) {
			t.Fatalf("filterByPrefix(md) = %#v, want %q", got, item)
		}
	}
	if got := filterByPrefix(items, "md"); len(got) != 2 {
		t.Fatalf("filterByPrefix(md) = %#v, want exactly the two .md files", got)
	}

	for _, item := range []string{"internal/ui/bubble/bubble_test.go", "scripts/testdata/fixture.txt"} {
		if got := filterByPrefix(items, "test"); !containsString(got, item) {
			t.Fatalf("filterByPrefix(test) = %#v, want %q", got, item)
		}
	}
}

func TestFilterByPrefixFallsBackToSubstringWhenNoExtensionMatches(t *testing.T) {
	items := []string{"markdown.txt", "notes.md", "README"}
	got := filterByPrefix(items, "mark")
	want := []string{"markdown.txt"}
	if !equalStrings(got, want) {
		t.Fatalf("filterByPrefix(mark) = %#v, want %#v", got, want)
	}

	got = filterByPrefix(items, "xyz")
	if len(got) != 0 {
		t.Fatalf("filterByPrefix(xyz) = %#v, want no matches", got)
	}
}

// makeCompletionFixture 构造一个递归搜索测试目录树，返回其根路径。
// 结构：
//
//	root/
//	  test.md
//	  docs/
//	    guide.md
//	    test.md
//	    a/
//	      test.md
//	      b/
//	        test.md
//	    node_modules/
//	      pkg/index.js
//	  .hidden/
//	    hidden.txt
func makeCompletionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dirs := []string{
		"docs",
		"docs/a",
		"docs/a/b",
		"docs/node_modules/pkg",
		".hidden",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	files := []string{
		"test.md",
		"docs/guide.md",
		"docs/test.md",
		"docs/a/test.md",
		"docs/a/b/test.md",
		"docs/node_modules/pkg/index.js",
		".hidden/hidden.txt",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return root
}

// TestListFilesRecursiveCollectsNestedAndSkipsNoise 验证递归收集：
// 条目为相对路径、目录带 / 结尾；隐藏目录也应被收集，node_modules 被整棵跳过。
func TestListFilesRecursiveCollectsNestedAndSkipsNoise(t *testing.T) {
	root := makeCompletionFixture(t)
	got, err := listFilesRecursive(root)
	if err != nil {
		t.Fatalf("listFilesRecursive: %v", err)
	}
	want := []string{
		"test.md",
		".hidden/",
		"docs/",
		".hidden/hidden.txt",
		"docs/guide.md",
		"docs/test.md",
		"docs/a/",
		"docs/a/test.md",
		"docs/a/b/",
		"docs/a/b/test.md",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("listFilesRecursive = %#v, want %#v", got, want)
	}
}

// TestListFilesRecursiveSameNameShallowFirst 验证同名文件按嵌套深度排序：
// 深度最少的排在最上面，深度最大的排在最下面。
func TestListFilesRecursiveSameNameShallowFirst(t *testing.T) {
	root := makeCompletionFixture(t)
	got, err := listFilesRecursive(root)
	if err != nil {
		t.Fatalf("listFilesRecursive: %v", err)
	}
	var testFiles []string
	for _, item := range got {
		if item == "test.md" || strings.HasSuffix(item, "/test.md") {
			testFiles = append(testFiles, item)
		}
	}
	want := []string{"test.md", "docs/test.md", "docs/a/test.md", "docs/a/b/test.md"}
	if !slices.Equal(testFiles, want) {
		t.Fatalf("same-name test.md order = %#v, want shallow-first %#v", testFiles, want)
	}
}

// TestLoadFilesInDirCmdRecursiveFiltered 验证异步加载命令返回的过滤结果
// 覆盖多层目录，且同名文件浅层优先。
func TestLoadFilesInDirCmdRecursiveFiltered(t *testing.T) {
	root := makeCompletionFixture(t)
	msg := loadFilesInDirCmd(root, "test", false)().(fileCompletionLoadedMsg)
	if msg.searchDir != root {
		t.Fatalf("msg.searchDir = %q, want %q", msg.searchDir, root)
	}
	want := []string{"test.md", "docs/test.md", "docs/a/test.md", "docs/a/b/test.md"}
	if !slices.Equal(msg.filtered, want) {
		t.Fatalf("filtered = %#v, want %#v", msg.filtered, want)
	}
	// 扩展名优先匹配在递归路径上仍然成立
	msg2 := loadFilesInDirCmd(root, "md", false)().(fileCompletionLoadedMsg)
	want2 := []string{"test.md", "docs/guide.md", "docs/test.md", "docs/a/test.md", "docs/a/b/test.md"}
	if !slices.Equal(msg2.filtered, want2) {
		t.Fatalf("filtered(md) = %#v, want %#v", msg2.filtered, want2)
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

func TestCtrlCWhileModelWorkingCancelsWithoutClearingInput(t *testing.T) {
	runner := &ctrlCRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}

	// 模拟模型已经进入 generating，同时用户在输入框准备了下一段内容。
	next, _ = model.Update(assistantDeltaMsg("partial"))
	model = next.(appModel)
	model.input.SetValue("follow-up")

	next, cancelCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if cancelCmd != nil {
		t.Fatalf("working Ctrl+C returned command: %T", cancelCmd)
	}
	if got := model.input.Value(); got != "follow-up" {
		t.Fatalf("working Ctrl+C cleared input = %q, want follow-up preserved", got)
	}
	if !model.lastCtrlCAt.IsZero() {
		t.Fatal("working Ctrl+C should not start the ready-state double-press timer")
	}

	finishedMsg := cmd()
	finished, ok := finishedMsg.(turnFinishedMsg)
	if !ok {
		t.Fatalf("cancelled command returned %T, want turnFinishedMsg", finishedMsg)
	}
	if runner.runCtx == nil {
		t.Fatal("runner did not receive a context")
	}
	if runner.runCtx.Err() != context.Canceled {
		t.Fatalf("runner context error = %v, want context.Canceled", runner.runCtx.Err())
	}
	next, _ = model.Update(finished)
	model = next.(appModel)
	if model.isWorkRunning() || model.isGenerating || model.activeModelCancel != nil {
		t.Fatalf("model still working after cancellation: running=%v generating=%v cancel=%v", model.isWorkRunning(), model.isGenerating, model.activeModelCancel != nil)
	}
	for _, entry := range model.transcript {
		if entry.kind == entryError && strings.Contains(entry.body, "context canceled") {
			t.Fatalf("expected cancellation error to stay hidden, got %#v", entry)
		}
	}
}

func TestCtrlCFirstPressCancelsActiveToolBeforeTurn(t *testing.T) {
	runner := &ctrlCRunner{activeTool: true}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}

	next, cancelCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if cancelCmd != nil {
		t.Fatalf("first tool Ctrl+C returned command: %T", cancelCmd)
	}
	if runner.cancelToolCalls != 1 || runner.cancelTurnCalls != 0 {
		t.Fatalf("first Ctrl+C calls = tool:%d turn:%d, want tool:1 turn:0", runner.cancelToolCalls, runner.cancelTurnCalls)
	}
	if !model.toolCancelRequested {
		t.Fatal("first tool cancellation should mark the tool cancellation requested")
	}
	if model.lastCtrlCAt.IsZero() == false {
		t.Fatal("working Ctrl+C should not start the idle double-press timer")
	}

	// A second press escalates to cancelling the whole turn.
	next, cancelCmd = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if cancelCmd != nil {
		t.Fatalf("second tool Ctrl+C returned command: %T", cancelCmd)
	}
	if runner.cancelToolCalls != 1 || runner.cancelTurnCalls != 1 {
		t.Fatalf("second Ctrl+C calls = tool:%d turn:%d, want tool:1 turn:1", runner.cancelToolCalls, runner.cancelTurnCalls)
	}
	if !model.modelCancelRequested {
		t.Fatal("second Ctrl+C should request turn cancellation")
	}

	finishedMsg := cmd()
	finished, ok := finishedMsg.(turnFinishedMsg)
	if !ok {
		t.Fatalf("cancelled command returned %T, want turnFinishedMsg", finishedMsg)
	}
	_, _ = model.Update(finished)
}

func TestCtrlCWithoutActiveToolCancelsTurnImmediately(t *testing.T) {
	runner := &ctrlCRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}

	next, cancelCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if cancelCmd != nil {
		t.Fatalf("turn Ctrl+C returned command: %T", cancelCmd)
	}
	if runner.cancelToolCalls != 1 || runner.cancelTurnCalls != 1 {
		t.Fatalf("Ctrl+C calls = tool:%d turn:%d, want tool:1 turn:1", runner.cancelToolCalls, runner.cancelTurnCalls)
	}
	if !model.modelCancelRequested {
		t.Fatal("Ctrl+C without an active tool should request turn cancellation")
	}

	finishedMsg := cmd()
	finished, ok := finishedMsg.(turnFinishedMsg)
	if !ok {
		t.Fatalf("cancelled command returned %T, want turnFinishedMsg", finishedMsg)
	}
	_, _ = model.Update(finished)
}
func TestCtrlCWithoutModelOutputRestoresInputAndRemovesTranscript(t *testing.T) {
	runner := &ctrlCRunner{}
	model := newTestModel(runner)
	model.input.SetValue("retry this prompt")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)

	finished := cmd()
	next, _ = model.Update(finished)
	model = next.(appModel)
	if got := model.input.Value(); got != "retry this prompt" {
		t.Fatalf("input = %q, want interrupted prompt restored", got)
	}
	for _, entry := range model.transcript {
		if entry.kind == entryUser && entry.body == "retry this prompt" {
			t.Fatalf("interrupted user input remained in transcript: %#v", entry)
		}
		if strings.Contains(entry.body, "context canceled") {
			t.Fatalf("context cancellation was rendered: %#v", entry)
		}
	}
}

func TestCtrlCDoublePressWhileWorkingDoesNotQuit(t *testing.T) {
	runner := &ctrlCRunner{}
	model := newTestModel(runner)
	model.input.SetValue("first")
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}
	model.lastCtrlCAt = time.Now()

	next, secondCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if secondCmd != nil {
		msg := secondCmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("double Ctrl+C while working should not quit")
		}
	}
	if !model.lastCtrlCAt.IsZero() {
		t.Fatal("working Ctrl+C should reset the ready-state double-press timer")
	}

	finishedMsg := cmd()
	finished, ok := finishedMsg.(turnFinishedMsg)
	if !ok {
		t.Fatalf("cancelled command returned %T, want turnFinishedMsg", finishedMsg)
	}
	_, _ = model.Update(finished)
}

func TestCtrlCDoublePressWhileTerminalWorkDoesNotQuit(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if !model.queryGuard.StartTerminal() {
		t.Fatal("StartTerminal() failed")
	}
	model.syncRunningFlags()
	model.lastCtrlCAt = time.Now()
	model.input.SetValue("follow-up")

	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("double Ctrl+C while terminal work should not quit")
		}
	}
	if got := model.input.Value(); got != "follow-up" {
		t.Fatalf("terminal-work Ctrl+C changed input = %q", got)
	}
}

func TestRelayout_TranscriptUsesFullFrameWidth(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 120
	model.height = 40
	model.ready = true
	model.relayout()
	want := model.width - mainFrameHorizontalFrame - mainContentPadding*2
	if model.viewport.Width != want {
		t.Errorf("viewport.Width = %d, want %d (full frame inner width)", model.viewport.Width, want)
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

func TestRenderToolEntryBody_UsesQuietDetailLines(t *testing.T) {
	body := "Bash ls .\nfile1.go\nfile2.go"
	result := renderToolEntryBody(body, 60, 1.0)
	for _, want := range []string{"Bash ls .", "file1.go", "file2.go"} {
		if !strings.Contains(result, want) {
			t.Errorf("renderToolEntryBody = %q, want %q", result, want)
		}
	}
	if strings.Contains(result, "> ") {
		t.Errorf("renderToolEntryBody = %q, should not use old quote prefix", result)
	}
}

// TestRenderDockStatusLine_ContainsModelTokenAndFree 验证上边框保留模式与
// 进度线，token 用量移到下边框，模型名仍只在 header。
func TestRenderDockStatusLine_ContainsModelTokenAndFree(t *testing.T) {
	runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 5000, LimitTokens: 100000}}
	model := newTestModel(runner)
	model.width = 100
	model.cursorFrameAt = time.Now()
	dock := model.renderDockStatusLine(98)
	if strings.Contains(dock, "5k / 100k") {
		t.Errorf("status dock = %q, token count should live in the bottom border", dock)
	}
	if !strings.ContainsAny(dock, "░▒█") {
		t.Errorf("status dock = %q, want frontier progress glyphs", dock)
	}
	bottom := model.renderBottomDockLine(98)
	if !strings.Contains(bottom, "5k / 100k") {
		t.Errorf("bottom border = %q, want token count", bottom)
	}
	if !strings.ContainsAny(bottom, "─") {
		t.Errorf("bottom border = %q, want embedded hairline rule", bottom)
	}
	// 模型名现在在 header，不在 dock。
	if strings.Contains(dock, "model-a") {
		t.Errorf("status dock = %q, model should be in header not dock", dock)
	}
	header := model.renderHeaderLine(98)
	if !strings.Contains(header, "model-a") {
		t.Errorf("header = %q, want current model", header)
	}
}

// TestRenderTasksCard_ShowsRunningAndDone 验证 Tasks 卡片显示 running 和 completed 任务。
func TestRenderTasksCard_ShowsRunningAndDone(t *testing.T) {
	ctrl := &fakeTaskController{
		tasks: []taskpkg.TaskSnapshot{
			{ID: "worker-1", Name: "worker-1", ParentSessionID: "session-1", Status: taskpkg.TaskRunning},
			{ID: "worker-2", Name: "worker-2", ParentSessionID: "session-1", Status: taskpkg.TaskCompleted},
		},
	}
	model := newTestModel(&fakeRunner{})
	model.taskController = ctrl
	result := model.renderTasksCardContent(30)
	if !strings.Contains(result, "worker-1") {
		t.Errorf("taskController card = %q, want worker-1", result)
	}
	if !strings.Contains(result, "worker-2") {
		t.Errorf("taskController card = %q, want worker-2", result)
	}
}

func TestRenderTasksCard_UsesPersonaColorForName(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
		{
			ID:              "worker-1",
			Name:            "八潮瑠唯",
			Color:           "#669988",
			ParentSessionID: "session-1",
			Status:          taskpkg.TaskRunning,
		},
	}}

	result := model.renderTasksCardContent(40)
	expectedName := lipgloss.NewStyle().Foreground(lipgloss.Color("#669988")).Render("八潮瑠唯")
	if !strings.Contains(result, expectedName) {
		t.Fatalf("taskController card = %q, want persona-colored name %q", result, expectedName)
	}
}

func TestRenderTasksCard_HidesTasksFromOtherSessions(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: []taskpkg.TaskSnapshot{
		{ID: "old-worker", ParentSessionID: "previous-session", Status: taskpkg.TaskCompleted},
		{ID: "legacy-worker", Status: taskpkg.TaskCompleted},
	}}

	result := model.renderTasksCardContent(30)
	if !strings.Contains(result, "none") {
		t.Fatalf("taskController card = %q, want empty state for fresh session", result)
	}
	for _, hidden := range []string{"old-worker", "legacy-worker"} {
		if strings.Contains(result, hidden) {
			t.Fatalf("taskController card = %q, should not contain %q", result, hidden)
		}
	}
}

func TestRenderTasksCardContent_ClampsToHeight(t *testing.T) {
	tasks := make([]taskpkg.TaskSnapshot, 0, 12)
	for i := 0; i < 12; i++ {
		tasks = append(tasks, taskpkg.TaskSnapshot{
			ID:              fmt.Sprintf("worker-%02d-long-id", i),
			ParentSessionID: "session-1",
			Status:          taskpkg.TaskCompleted,
		})
	}
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: tasks}

	result := ansi.Strip(model.renderTasksCardContentHeight(30, 5))
	lines := strings.Split(result, "\n")
	if len(lines) != 5 {
		t.Fatalf("taskController lines = %d, want 5\n%s", len(lines), result)
	}
	if !strings.Contains(result, "more") {
		t.Fatalf("taskController card = %q, want overflow summary", result)
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

func TestFullLayout_StatusDockVisibleWithoutSidebar(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 100
	model.height = 30
	model.ready = true
	model.relayout()

	view := model.View()
	if !strings.Contains(view, "0 / 131k") {
		t.Errorf("View() = %q, want bottom token status", view)
	}
	if strings.Contains(view, "taskController") {
		t.Errorf("View() = %q, should not contain persistent taskController sidebar", view)
	}
}

// TestAltBracketMouseSplitFiltered 验证鼠标短读产生的 Alt+`[` KeyRunes 被过滤，
// 不泄漏进输入框。短读 "\x1b["（ESC+bracket，载荷未到）会被 BubbleTea 解析为
// Alt+`[` KeyRunes，是 [[[[ 泄漏的来源。
func TestAltBracketMouseSplitFiltered(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	// Alt+`[` 单独到达（模拟短读 \x1b[）。
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true})
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("Alt+[ leaked into input: %q, want draft (held as pending)", got)
	}
	// 续接载荷 <64;59;25M 到达，拼成完整鼠标序列被丢弃。
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<64;59;25M")})
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("mouse completion leaked: %q, want draft", got)
	}
}

// TestAltBracketBurstFiltered 验证连续 Alt+`[` 突发（触控板滑动）不泄漏。
func TestAltBracketBurstFiltered(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.input.SetValue("draft")
	model.relayout()

	for i := 0; i < 8; i++ {
		model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true})
	}
	// trailing MouseMsg（真实滑动中总有）清挂起。
	model.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 10, Y: 10})
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("Alt+[ burst leaked: %q, want draft", got)
	}
}

func TestInputUsesSingleRealCursorWithoutReverseVideo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("hello")
	model.input.CursorStart()
	rendered := model.renderInputContent()
	if strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("input content contains reverse-video software cursor: %q", rendered)
	}
	if got := ansi.Strip(rendered); !strings.Contains(got, "hello") {
		t.Fatalf("input content = %q, want text preserved", got)
	}
}

func TestTokenInputUsesSingleRealCursorWithoutReverseVideo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("/help rest")
	model.inputTokens = []inputToken{{Kind: inputTokenCommand, Start: 0, End: 5, Label: "/help"}}
	model.input.CursorStart()
	rendered := model.renderTokenInputContent()
	if strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("token input contains reverse-video software cursor: %q", rendered)
	}
	if got := ansi.Strip(rendered); !strings.Contains(got, "/help") {
		t.Fatalf("token input = %q, want token text preserved", got)
	}
}

func TestViewPublishesCursorThemeBackgroundAndVisual(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.applyCursorAnimation()
	_ = model.View()

	position, ok := anchor.consume()
	if !ok || !position.active {
		t.Fatalf("position = %#v/%v, want active", position, ok)
	}
	if position.background != model.theme.Colors.TerminalBackground {
		t.Fatalf("background = %q, want %q", position.background, model.theme.Colors.TerminalBackground)
	}
	visual, ok := anchor.currentVisual()
	if !ok {
		t.Fatalf("visual = %#v/%v, want published state", visual, ok)
	}
	background := strings.ToLower(model.theme.Colors.TerminalBackground)
	bright := strings.ToLower(model.theme.Colors.CursorNormalBright)
	if visual.color == "" || (visual.color != background && visual.color != bright && !strings.HasPrefix(visual.color, "#")) {
		t.Fatalf("cursor color = %q, want theme gradient color", visual.color)
	}
}

func TestSlashCompletionKeepsCursorAnchor(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.input.SetValue("/")
	model.input.CursorEnd()
	model.relayout()
	_ = model.View()
	before, ok := anchor.consume()
	if !ok || !before.active {
		t.Fatalf("before anchor = %#v/%v", before, ok)
	}

	model.syncCommandCompletion()
	if model.completion == nil {
		t.Fatal("command completion did not open")
	}
	_ = model.View()
	after, ok := anchor.consume()
	if !ok || !after.active {
		t.Fatalf("after anchor = %#v/%v", after, ok)
	}
	if before.column != after.column || before.upFromBottom != after.upFromBottom {
		t.Fatalf("completion moved input anchor: before=%#v after=%#v", before, after)
	}
}

func TestCursorThemeSwitchPublishesNewPalette(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, anchor)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.cursorFrameAt = time.Unix(0, int64(cursorCycleDuration/4))
	if err := model.applyTheme(theme.TokyoNight); err != nil {
		t.Fatal(err)
	}
	_ = model.View()
	position, ok := anchor.consume()
	if !ok || position.background != model.theme.Colors.TerminalBackground {
		t.Fatalf("position = %#v/%v, want new theme background", position, ok)
	}
	visual, ok := anchor.currentVisual()
	if !ok || !visual.visible || !strings.HasPrefix(visual.color, "#") {
		t.Fatalf("visual = %#v/%v, want new theme gradient state", visual, ok)
	}
	animation, ok := anchor.currentAnimation()
	if !ok || animation.background != strings.ToLower(model.theme.Colors.TerminalBackground) || animation.bright != strings.ToLower(model.theme.Colors.CursorNormalBright) {
		t.Fatalf("animation = %#v/%v, want new theme endpoints", animation, ok)
	}
}

// TestIdleCursorFrameSchedulesClockTickInsteadOfAnimationFrame 验证空闲帧的
// 新契约：不再续命 30fps 动画帧链，而是由 15s 空闲时钟链接手（供 header
// 时钟继续走动）。注意不要调用返回的 cmd——Batch 内的 15s tick 会阻塞测试。
func TestIdleCursorFrameSchedulesClockTickInsteadOfAnimationFrame(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.cursorFrameAt = time.Unix(10, 0)

	next, cmd := model.Update(cursorFrameMsg(time.Unix(11, 0)))
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("idle cursor frame should schedule the idle clock tick")
	}
	if !model.clockTickScheduled {
		t.Fatal("idle cursor frame should mark the clock tick scheduled")
	}
	if model.uiAnimationFrameScheduled {
		t.Fatal("idle cursor frame must not schedule another animation frame")
	}
}

func TestWorktreeRefreshDoesNotStartIdleRedrawLoop(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, cmd := model.Update(worktreeRefreshMsg{snapshot: worktreeSnapshot{name: "workspace"}})
	model = next.(appModel)
	if cmd != nil {
		t.Fatalf("worktree refresh scheduled periodic redraw command: %v", cmd)
	}
	if model.worktree.name != "workspace" {
		t.Fatalf("worktree name = %q, want refreshed snapshot", model.worktree.name)
	}
}

func TestTaskTaskUpdateMsgRefreshesActivityAndPreview(t *testing.T) {
	controller := &fakeTaskController{tasks: []taskpkg.TaskSnapshot{{
		ID: "task-1", SessionID: "task-1", ParentSessionID: "session-1", Name: "worker", Status: taskpkg.TaskRunning,
	}}}
	model := newTestModel(&fakeRunner{})
	model.taskController = controller
	model.openActivity(activityTabTasks)
	model.taskPreview = &taskTranscriptPreview{
		task:      controller.tasks[0],
		sessionID: "task-1",
	}
	model.taskPreview.liveContent = "partial result"
	model.taskUpdates = make(chan struct{})

	controller.tasks[0].Status = taskpkg.TaskCompleted
	controller.tasks[0].Content = "final result"
	controller.tasks[0].FinishedAt = ptrTime(time.Now())

	next, cmd := model.Update(taskUpdateMsg{})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("task update should re-arm the subscription wait command")
	}
	if len(model.taskPicker.tasks) != 1 || model.taskPicker.tasks[0].Status != taskpkg.TaskCompleted {
		t.Fatalf("activity tasks = %#v, want completed task", model.taskPicker.tasks)
	}
	if model.taskPreview.task.Status != taskpkg.TaskCompleted || model.taskPreview.liveContent != "" {
		t.Fatalf("preview = %#v, want refreshed terminal task without live content", model.taskPreview)
	}
	card := ansi.Strip(model.renderTaskCard(time.Now()))
	if strings.Contains(card, "running") || strings.Contains(card, "worker") {
		t.Fatalf("sidebar card = %q, want no running worker after completion", card)
	}
}

func TestTaskTaskUpdateMsgClosedStopsRearming(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	updates := make(chan struct{})
	model.taskUpdates = updates

	next, cmd := model.Update(taskUpdateMsg{closed: true})
	model = next.(appModel)
	if cmd != nil {
		t.Fatal("closed task update should not re-arm wait command")
	}
	if model.taskUpdates != nil {
		t.Fatalf("taskUpdates = %v, want nil after close", model.taskUpdates)
	}
}

func TestTaskTaskUpdateMsgRefreshesToolEntryImmediately(t *testing.T) {
	controller := &fakeTaskController{tasks: []taskpkg.TaskSnapshot{{
		ID: "task-1", SessionID: "task-1", ParentSessionID: "session-1", Name: "worker", Status: taskpkg.TaskRunning,
	}}}
	model := newTestModel(&fakeRunner{})
	model.taskController = controller
	model.replaceTranscript([]transcriptEntry{{
		kind:       entryTool,
		toolName:   "Task",
		toolStatus: "ok",
		toolResult: `{"id":"task-1","session_id":"task-1"}`,
	}})

	controller.tasks[0].Status = taskpkg.TaskCompleted
	controller.tasks[0].Content = "done"
	next, _ := model.Update(taskUpdateMsg{})
	model = next.(appModel)
	if model.transcript[0].toolStatus != "ok" {
		t.Fatalf("tool status = %q, want successful completed task result", model.transcript[0].toolStatus)
	}
	if !strings.Contains(model.transcript[0].toolResult, `"status":"completed"`) {
		t.Fatalf("tool result = %q, want refreshed completed snapshot", model.transcript[0].toolResult)
	}
}

func TestTaskUpdateMaintainsRunningIndexWithoutRebuildingHistory(t *testing.T) {
	controller := &fakeTaskController{tasks: []taskpkg.TaskSnapshot{{
		ID: "task-1", SessionID: "task-1", ParentSessionID: "session-1", Name: "worker", Status: taskpkg.TaskRunning,
	}}}
	entries := make([]transcriptEntry, 5_000, 5_001)
	for index := range entries {
		entries[index] = transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"}
	}
	entries = append(entries, transcriptEntry{
		kind: entryTool, toolUseID: "task-call", toolName: "Task", toolStatus: "running",
		toolResult: `{"id":"task-1","session_id":"task-1"}`,
	})
	model := newTestModel(&fakeRunner{})
	model.taskController = controller
	model.replaceTranscript(entries)
	model.toolRuntimeRebuildVisits = 0
	model.refreshViewport()
	refreshes := model.transcriptRefreshCount

	controller.tasks[0].Status = taskpkg.TaskCompleted
	controller.tasks[0].Content = "done"
	next, _ := model.Update(taskUpdateMsg{})
	model = next.(appModel)

	if got := model.toolRuntimeRebuildVisits; got != 0 {
		t.Fatalf("task update rebuilt %d historical runtime entries, want 0", got)
	}
	if got := model.taskToolUpdateVisits; got != 1 {
		t.Fatalf("task update visited %d transcript entries, want indexed task entry only", got)
	}
	if got := model.transcriptRefreshCount - refreshes; got != 1 {
		t.Fatalf("task update viewport refreshes = %d, want 1", got)
	}
	if len(model.toolRuntime.running) != 0 {
		t.Fatalf("completed task remained in running index: %#v", model.toolRuntime.running)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
