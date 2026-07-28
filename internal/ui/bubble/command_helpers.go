package bubble

import (
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/settings"
	"codex-agent-go/internal/subagent"
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
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
			body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s models=%s key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, strings.Join(model.AvailableModels(cfg), ","), cfg.APIKeyEnvName),
		})
	case model.ProviderCustom, model.ProviderDeepSeek:
		cfg, err := m.configForProvider(args)
		if err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "model", body: err.Error()})
			return nil
		}
		m.applyModelConfigFromCommand(cfg)
	default:
		cfg := m.currentModelConfig()
		if !model.SupportsModel(cfg, args) {
			m.addEntry(transcriptEntry{kind: entryError, title: "model", body: fmt.Sprintf("unknown model command: %s", args)})
			return nil
		}
		cfg.Model = args
		m.applyModelConfigFromCommand(cfg)
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
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "model",
		body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s models=%s key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, strings.Join(model.AvailableModels(cfg), ","), cfg.APIKeyEnvName),
	})
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
		return filepath.Join(root, ".ccagent", "exports", name), nil
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
	return runSubagentCmd(m.ctx, m.subagents, req)
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
		"session: %s\nmodel: %s/%s\nsettings: context=%s run=%s meter=%s limit=%d\nqueue: %d\nsubagent tasks: %d\ncontext: used=%d cache=%d limit=%d",
		sessionID,
		modelCfg.Provider,
		modelCfg.Model,
		cfg.Subagent.DefaultContextMode,
		cfg.Subagent.DefaultRunMode,
		cfg.UI.ContextMeterLocation,
		cfg.UI.ContextLimitTokens,
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
		fmt.Sprintf("done · depth %d", result.Depth),
	}
	if result.AgentID != "" {
		lines = append(lines, "id  "+result.AgentID)
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
		fmt.Sprintf("%s · %s · %s · depth %d", taskDisplayName(task), task.Status, task.ContextMode, task.Depth),
	}
	if task.Name != "" && task.ID != "" {
		lines = append(lines, "id  "+task.ID)
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
	return shortTaskID(task.ID)
}

func resultDisplayName(result subagent.Result) string {
	if name := strings.TrimSpace(result.AgentName); name != "" {
		return name
	}
	return shortTaskID(result.AgentID)
}
