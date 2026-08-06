package bubble

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/subagent"
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
		m.modelWizard = newModelWizard(m.currentModelConfig())
		m.pending = nil
	case "status":
		cfg := m.currentModelConfig()
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "model",
			body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s models=%s context=%d retries=%d key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, strings.Join(model.AvailableModels(cfg), ","), model.EffectiveContextLimitTokens(cfg), cfg.RetryCount, cfg.APIKeyEnvName),
		})
	default:
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
//	/setting                 → 打开持久化设置向导（写入 ~/.paw/settings.json）
//	/setting translate on|off → 运行期动态开关，只改内存、不写配置文件
//
// 动态开关立即生效，重启后失效；需要持久化请用 /setting 向导。
func (m *appModel) handleSettingCommand(invocation string) tea.Cmd {
	args := commandArgs(invocation)
	if args == "" {
		m.settingWizard = newSettingWizard(m.currentSettings())
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
		m.addEntry(transcriptEntry{kind: entryError, title: "subagent", body: err.Error()})
		return nil
	}
	if m.subagents == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "subagent", body: "subagent controller is unavailable"})
		return nil
	}
	if req.RunMode == settings.RunModeBackground {
		task, err := m.subagents.Launch(m.ctx, req)
		if err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "subagent", body: err.Error()})
			return nil
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "subagent", body: renderTask(task)})
		return nil
	}
	if !m.queryGuard.StartModel() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "assistant is already running"})
		return nil
	}
	m.syncRunningFlags()
	m.addEntry(transcriptEntry{kind: entrySystem, title: "subagent", body: "started sync subagent"})
	workCmd := runSubagentCmd(m.beginModelWorkContext(), m.subagents, req)
	frameCmd := m.scheduleUIAnimationFrame()
	return tea.Batch(workCmd, frameCmd)
}

func (m appModel) parseSubagentCommand(invocation string) (subagent.Request, error) {
	cfg := m.currentSettings()
	req := subagent.Request{
		ParentSessionID: m.sessionID,
		ContextMode:     cfg.Subagent.DefaultContextMode,
		RunMode:         cfg.Subagent.DefaultRunMode,
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

func runSubagentCmd(ctx context.Context, controller SubagentController, req subagent.Request) tea.Cmd {
	return func() tea.Msg {
		result, err := controller.Run(ctx, req)
		return subagentFinishedMsg{result: result, err: err}
	}
}

func (m *appModel) handleTasksCommand() {
	if m.subagents == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "tasks", body: "subagent controller is unavailable"})
		return
	}
	tasks := m.subagentTasks()
	if len(tasks) == 0 {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "tasks", body: "no subagent tasks"})
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
		cfg.Subagent.DefaultContextMode,
		cfg.Subagent.DefaultRunMode,
		cfg.UI.ContextMeterLocation,
		m.theme.ID,
		m.chatQueue.Len(),
		taskCount,
		stats.UsedTokens,
		stats.CacheTokens,
		stats.LimitTokens,
	)
}

func (m appModel) subagentTasks() []subagent.TaskSnapshot {
	if m.subagents == nil {
		return nil
	}
	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" {
		return nil
	}
	all := m.subagents.ListTasks()
	tasks := make([]subagent.TaskSnapshot, 0, len(all))
	for _, task := range all {
		if strings.TrimSpace(task.ParentSessionID) == sessionID {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func renderSubagentResult(result subagent.Result) string {
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

func renderTask(task subagent.TaskSnapshot) string {
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

func taskDisplayName(task subagent.TaskSnapshot) string {
	if name := strings.TrimSpace(task.Name); name != "" {
		return name
	}
	return "subagent"
}

func resultDisplayName(result subagent.Result) string {
	if name := strings.TrimSpace(result.AgentName); name != "" {
		return name
	}
	return "subagent"
}

func (m *appModel) syncRunnerModelContextLimit(cfg model.Config) {
	if setter, ok := m.runner.(interface{ SetContextLimitTokens(int) }); ok {
		setter.SetContextLimitTokens(model.EffectiveContextLimitTokens(cfg))
	}
}
