package bubble

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/task"
	"strings"
	"time"
)

func commandArgs(invocation string) string {
	token := commandToken(invocation)
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(invocation), token))
}

func (m *appModel) handleModelCommand(invocation string) tea.Cmd {
	args := strings.TrimSpace(commandArgs(invocation))
	switch args {
	case "":
		if m.configCenterController != nil {
			m.modelWizard = newModelWizard(m.currentModelConfig(), m.configCenterController.Snapshot())
		} else {
			m.modelWizard = newModelWizard(m.currentModelConfig())
		}
		m.pending = nil
	case "status":
		cfg := m.currentModelConfig()
		stableID := ""
		body := ""
		if m.configCenterController != nil {
			snapshot := m.configCenterController.Snapshot()
			stableID = snapshot.ActiveModelID
			body = fmt.Sprintf("\ncatalog registered=%d effective=%d\n%s", len(snapshot.Document.Models), len(snapshot.EffectiveModels), discoveryStatusSummary(snapshot.Discovery, time.Now()))
		}
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "model",
			body:  fmt.Sprintf("id=%s provider=%s base=%s path=%s model=%s models=%s context=%d retries=%d key=%s", stableID, cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, strings.Join(model.AvailableModels(cfg), ","), model.EffectiveContextLimitTokens(cfg), cfg.RetryCount, cfg.APIKeyEnvName) + body,
		})
	default:
		if m.configCenterController != nil {
			snapshot := m.configCenterController.Snapshot()
			if _, ok := snapshot.EffectiveModels[args]; ok {
				// A direct textual command resolves and activates one immediate
				// current-snapshot ID. Interactive selectors use the stronger
				// revision-and-identity-pinned ActivateCatalogSelection path.
				if err := m.configCenterController.SetActiveModelID(args); err != nil {
					m.addEntry(transcriptEntry{kind: entryError, title: "model", body: err.Error()})
					return nil
				}
				cfg := m.currentModelConfig()
				m.syncRunnerModelContextLimit(cfg)
				m.addEntry(transcriptEntry{kind: entrySystem, title: "model", body: fmt.Sprintf("id=%s provider=%s model=%s", args, cfg.Provider, cfg.Model)})
				return nil
			}
		}
		cfg := m.currentModelConfig()
		profiles := model.ConfiguredProfiles(cfg)
		for _, profile := range profiles {
			if args == profile.ID || args == profile.Name || args == profile.Provider {
				selected := profile.Config()
				selected.Profiles = profiles
				m.applyModelConfigFromCommand(selected)
				return nil
			}
			if model.SupportsModel(profile.Config(), args) {
				selected := profile.Config()
				selected.Profiles = profiles
				selected.Model = args
				m.applyModelConfigFromCommand(selected)
				return nil
			}
		}
		m.addEntry(transcriptEntry{kind: entryError, title: "model", body: fmt.Sprintf("unknown model command: %s", args)})
	}
	return nil
}

func (m *appModel) applyModelConfigFromCommand(cfg model.Config) {
	if m.modelConfig == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "model", body: "model config controller is unavailable"})
		return
	}
	if saver, ok := m.modelConfig.(ModelConfigSaver); ok {
		if err := saver.SaveModelConfig(cfg); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "model", body: err.Error()})
			return
		}
	}
	if err := m.modelConfig.ApplyModelConfig(cfg); err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "model", body: err.Error()})
		return
	}
	m.syncRunnerModelContextLimit(cfg)
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "model",
		body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s models=%s context=%d retries=%d key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, strings.Join(model.AvailableModels(cfg), ","), model.EffectiveContextLimitTokens(cfg), cfg.RetryCount, cfg.APIKeyEnvName),
	})
}

// handleSettingCommand 处理 /setting 指令：
//
//	/setting                  → 打开统一配置中心
//	/setting translate on|off → 运行期动态开关，只改内存、不写配置文件
//
// 动态开关立即生效，重启后失效；需要持久化请用 /setting 向导。
func (m *appModel) handleSettingCommand(invocation string) tea.Cmd {
	args := commandArgs(invocation)
	if args == "" {
		m.openConfigCenter()
		m.pending = nil
		return nil
	}
	fields := strings.Fields(args)
	if len(fields) == 2 && fields[0] == "translate" {
		var enabled bool
		switch fields[1] {
		case "on":
			enabled = true
		case "off":
			enabled = false
		default:
			m.addEntry(transcriptEntry{kind: entryError, title: "setting", body: fmt.Sprintf("unknown translate value: %s (usage: /setting translate on|off)", fields[1])})
			return nil
		}
		cfg := m.currentSettings()
		cfg.UI.TranslateOnDoubleClick = enabled
		if m.settingsConfig == nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "setting", body: "settings controller is unavailable"})
			return nil
		}
		m.settingsConfig.UpdateRuntime(cfg)
		state := "off"
		if enabled {
			state = "on"
		}
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "setting",
			body:  fmt.Sprintf("translate_on_double_click=%s (session only, not written to disk; /setting wizard persists it)", state),
		})
		return nil
	}
	m.addEntry(transcriptEntry{kind: entryError, title: "setting", body: "usage: /setting [translate on|off]"})
	return nil
}

func (m *appModel) handleExportCommand(invocation string) {
	path, err := m.exportPath(commandArgs(invocation))
	if err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "export", body: err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "export", body: err.Error()})
		return
	}
	content := exportTranscriptText(m.transcript)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "export", body: err.Error()})
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "export", body: err.Error()})
		return
	}
	m.addEntry(transcriptEntry{kind: entrySystem, title: "export", body: "conversation exported to " + path})
}

func (m appModel) exportPath(arg string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		name := "conversation-" + time.Now().Format("2006-01-02-150405") + ".txt"
		return filepath.Join(root, ".paw", "exports", name), nil
	}
	if !strings.HasSuffix(arg, ".txt") {
		arg = strings.TrimSuffix(arg, filepath.Ext(arg)) + ".txt"
	}
	target := arg
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("export path escapes workspace root: %s", arg)
	}
	return absTarget, nil
}

func exportTranscriptText(entries []transcriptEntry) string {
	var out strings.Builder
	for _, entry := range entries {
		if strings.TrimSpace(entry.body) == "" {
			continue
		}
		out.WriteString(entry.title)
		out.WriteString(":\n")
		out.WriteString(strings.TrimRight(entry.body, "\n"))
		out.WriteString("\n\n")
	}
	return out.String()
}

func (m *appModel) handleSubagentCommand(invocation string) tea.Cmd {
	req, err := m.parseSubagentCommand(invocation)
	if err != nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "task", body: err.Error()})
		return nil
	}
	if m.subagents == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "task", body: "task controller is unavailable"})
		return nil
	}
	if req.RunMode == settings.RunModeBackground {
		task, err := m.subagents.Launch(m.ctx, req)
		if err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "task", body: err.Error()})
			return nil
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "task", body: renderTask(task)})
		return nil
	}
	if !m.queryGuard.StartModel() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "assistant is already running"})
		return nil
	}
	m.syncRunningFlags()
	m.addEntry(transcriptEntry{kind: entrySystem, title: "task", body: "started sync task"})
	workCmd := runSubagentCmd(m.beginModelWorkContext(), m.subagents, req)
	frameCmd := m.scheduleUIAnimationFrame()
	return tea.Batch(workCmd, frameCmd)
}

func (m appModel) parseSubagentCommand(invocation string) (task.Request, error) {
	cfg := m.currentSettings()
	req := task.Request{
		ParentSessionID: m.sessionID,
		ContextMode:     cfg.Task.DefaultContextMode,
		RunMode:         cfg.Task.DefaultRunMode,
	}
	fields := strings.Fields(commandArgs(invocation))
	var prompt []string
	for _, field := range fields {
		switch field {
		case "--fork":
			req.ContextMode = settings.ContextModeFork
		case "--empty":
			req.ContextMode = settings.ContextModeEmpty
		case "--background":
			req.RunMode = settings.RunModeBackground
		case "--sync":
			req.RunMode = settings.RunModeSync
		default:
			prompt = append(prompt, field)
		}
	}
	req.Prompt = strings.Join(prompt, " ")
	req.Description = summarizeToolContent(req.Prompt)
	if strings.TrimSpace(req.Prompt) == "" {
		return req, fmt.Errorf("prompt is required")
	}
	return req, nil
}

func runSubagentCmd(ctx context.Context, controller TaskController, req task.Request) tea.Cmd {
	return func() tea.Msg {
		result, err := controller.Run(ctx, req)
		return subagentFinishedMsg{result: result, err: err}
	}
}

func (m *appModel) handleTasksCommand() {
	if m.subagents == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "tasks", body: "task controller is unavailable"})
		return
	}
	tasks := m.subagentTasks()
	if len(tasks) == 0 {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "tasks", body: "no task tasks"})
		return
	}
	lines := make([]string, 0, len(tasks))
	for _, task := range tasks {
		lines = append(lines, renderTask(task))
	}
	m.addEntry(transcriptEntry{kind: entrySystem, title: "tasks", body: strings.Join(lines, "\n\n")})
}

func (m *appModel) handleSkillsCommand() {
	if m.skillRegistry == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "skills", body: "skill registry is unavailable"})
		return
	}
	skills := m.skillRegistry.Skills()
	if len(skills) == 0 {
		roots := m.skillRegistry.Roots()
		body := "no skills found"
		if len(roots) > 0 {
			body += "\nroots:\n- " + strings.Join(roots, "\n- ")
		}
		if err := m.skillRegistry.LastErr(); err != nil {
			body += "\nerror: " + err.Error()
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "skills", body: body})
		return
	}
	lines := make([]string, 0, len(skills))
	for _, sk := range skills {
		label := sk.Name
		if strings.TrimSpace(sk.Description) != "" {
			label += " - " + strings.TrimSpace(sk.Description)
		}
		if strings.TrimSpace(sk.Path) != "" {
			label += "\n  " + sk.Path
		}
		lines = append(lines, label)
	}
	body := "Skills:\n- " + strings.Join(lines, "\n- ")
	if err := m.skillRegistry.LastErr(); err != nil {
		body += "\nerror: " + err.Error()
	}
	m.addEntry(transcriptEntry{kind: entrySystem, title: "skills", body: body})
}

func (m appModel) statusText(sessionID string) string {
	cfg := m.currentSettings()
	modelCfg := m.currentModelConfig()
	taskCount := 0
	if m.subagents != nil {
		taskCount = len(m.subagentTasks())
	}
	stats := m.contextStats()
	return fmt.Sprintf(
		"session: %s\nmodel: %s/%s context_limit=%d\nsettings: context=%s run=%s meter=%s\ntheme: %s\nqueue: %d\nsubagent tasks: %d\ncontext: used=%d cache=%d limit=%d",
		sessionID,
		modelCfg.Provider,
		modelCfg.Model,
		model.EffectiveContextLimitTokens(modelCfg),
		cfg.Task.DefaultContextMode,
		cfg.Task.DefaultRunMode,
		cfg.UI.ContextMeterLocation,
		m.theme.ID,
		m.chatQueue.Len(),
		taskCount,
		stats.UsedTokens,
		stats.CacheTokens,
		stats.LimitTokens,
	)
}

func (m appModel) subagentTasks() []task.TaskSnapshot {
	if m.subagents == nil {
		return nil
	}
	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" {
		return nil
	}
	all := m.subagents.ListTasks()
	tasks := make([]task.TaskSnapshot, 0, len(all))
	for _, task := range all {
		if strings.TrimSpace(task.ParentSessionID) == sessionID {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func renderSubagentResult(result task.Result) string {
	lines := []string{
		fmt.Sprintf("done  depth %d", result.Depth),
	}
	if result.OutputPath != "" {
		lines = append(lines, "output  "+result.OutputPath)
	}
	if result.TranscriptPath != "" {
		lines = append(lines, "transcript  "+result.TranscriptPath)
	}
	if content := summarizeToolContent(strings.TrimSpace(result.Content)); content != "" {
		lines = append(lines, "result  "+content)
	}
	return strings.Join(lines, "\n")
}

func renderTask(task task.TaskSnapshot) string {
	lines := []string{
		fmt.Sprintf("%s  %s  %s  depth %d", taskDisplayName(task), task.Status, task.ContextMode, task.Depth),
	}
	if task.PID > 0 {
		lines = append(lines, fmt.Sprintf("pid  %d", task.PID))
	}
	if task.OutputPath != "" {
		lines = append(lines, "output  "+task.OutputPath)
	}
	if task.TranscriptPath != "" {
		lines = append(lines, "transcript  "+task.TranscriptPath)
	}
	if task.Error != "" {
		lines = append(lines, "error  "+task.Error)
	}
	if task.Content != "" {
		lines = append(lines, "result  "+summarizeToolContent(task.Content))
	}
	return strings.Join(lines, "\n")
}

func taskDisplayName(task task.TaskSnapshot) string {
	if name := strings.TrimSpace(task.Name); name != "" {
		return name
	}
	return "task"
}

func resultDisplayName(result task.Result) string {
	if name := strings.TrimSpace(result.AgentName); name != "" {
		return name
	}
	return "task"
}

func (m *appModel) syncRunnerModelContextLimit(cfg model.Config) {
	if setter, ok := m.runner.(interface{ SetContextLimitTokens(int) }); ok {
		setter.SetContextLimitTokens(model.EffectiveContextLimitTokens(cfg))
	}
}

// syncRunnerCompression 把 context_compression.mode 热应用到 runner：
// 通过可选接口断言调用 SetContextMode，下一轮 maintainContextProjection 即按新
// mode 分流（state→状态压缩，summary→LLM 摘要）。未实现该接口的 runner 静默跳过。
func (m *appModel) syncRunnerCompression(cfg settings.Config) {
	if setter, ok := m.runner.(interface{ SetContextMode(string) }); ok {
		setter.SetContextMode(string(settings.NormalizeCompressionMode(cfg.ContextCompression.Mode)))
	}
}

// syncRunnerSettings 把 General 扁平列表里改动的 settings.json 字段热应用到
// runner：compression.mode/resume_recent_turns/state_compaction_ratio、
// context_maintenance.*，以及 ui.context_limit_tokens（经模型配置路径）。
// compression.mode 复用 syncRunnerCompression（SetContextMode）；其余通过可选
// 接口断言调用对应 setter，未实现某接口的 runner 静默跳过。
func (m *appModel) syncRunnerSettings(cfg settings.Config) {
	m.syncRunnerCompression(cfg)
	if setter, ok := m.runner.(interface{ SetResumeRecentTurns(int) }); ok {
		setter.SetResumeRecentTurns(cfg.ContextCompression.ResumeRecentTurns)
	}
	if setter, ok := m.runner.(interface{ SetStateCompactionRatio(float64) }); ok {
		setter.SetStateCompactionRatio(cfg.ContextCompression.StateCompactionRatio)
	}
	if setter, ok := m.runner.(interface {
		SetContextMaintenanceConfig(settings.ContextMaintenanceConfig) error
	}); ok {
		_ = setter.SetContextMaintenanceConfig(cfg.ContextMaintenance)
	}
	// ui 字段经模型配置路径（context_limit 经 syncRunnerModelContextLimit）。
	m.syncRunnerModelContextLimit(m.currentModelConfig())
}
