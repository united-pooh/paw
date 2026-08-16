package bubble

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// CommandHandler mutates appModel for a slash command and may return a tea.Cmd.
type CommandHandler func(m *appModel, invocation string) tea.Cmd

// Command describes one registered slash command.
type Command struct {
	Name              string
	Aliases           []string
	Description       string
	ArgumentHint      string
	AllowWhileRunning bool
	Handler           CommandHandler
}

// CommandRegistry resolves and dispatches registered slash commands.
type CommandRegistry struct {
	commands map[string]Command
	order    []string
}

// NewCommandRegistry creates the default TUI command registry.
func NewCommandRegistry() *CommandRegistry {
	registry := &CommandRegistry{}
	registry.Register(Command{
		Name:              "/help",
		Description:       "show available commands",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.addEntry(transcriptEntry{kind: entrySystem, title: "help", body: m.commandRegistry.HelpText()})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/yolo",
		Description:       "toggle dangerous Read access outside the workspace",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			controller, ok := m.runner.(interface {
				SetYoloMode(bool) (bool, error)
				YoloMode() bool
			})
			if !ok {
				m.addEntry(transcriptEntry{kind: entryError, title: "yolo", body: "yolo mode is unavailable"})
				return nil
			}
			enabled, err := controller.SetYoloMode(!controller.YoloMode())
			if err != nil {
				m.addEntry(transcriptEntry{kind: entryError, title: "yolo", body: err.Error()})
				return nil
			}
			state := "disabled"
			if enabled {
				state = "enabled: Read may access files outside the workspace"
			}
			m.addEntry(transcriptEntry{kind: entrySystem, title: "yolo", body: "yolo mode " + state})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/model",
		Description:       "open the model switcher",
		ArgumentHint:      "[status|<profile>|<model>]",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleModelCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/export",
		Description:       "export the current conversation",
		ArgumentHint:      "[filename]",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.handleExportCommand(invocation)
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/theme",
		Description:       "preview and select a color theme",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.openThemePicker()
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/setting",
		Description:       "settings: wizard, or runtime toggle (translate on|off)",
		ArgumentHint:      "[translate on|off]",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleSettingCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/config",
		Description:       "open or inspect the configuration center",
		ArgumentHint:      "[reload|status|path]",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.handleConfigCommand(invocation)
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/task",
		Description:       "launch a task",
		ArgumentHint:      "[--fork|--empty] [--background|--sync] <prompt>",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleTaskCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/streamma",
		Description:       "run a prompt through StreamMA taskController",
		ArgumentHint:      "<prompt>",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleStreamMACommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/streamma-trace",
		Description:       "run StreamMA with live event trace",
		ArgumentHint:      "<prompt>",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleStreamMACommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/tasks",
		Description:       "show background task tasks",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.openActivity(activityTabTasks)
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/todo",
		Description:       "show the current todo list",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.openActivity(activityTabTodo)
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/skills",
		Description:       "show discovered skills",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.handleSkillsCommand()
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/token-tracer",
		Aliases:           []string{"/tt"},
		Description:       "show the live Token Tracer dashboard URL",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			url := ""
			if provider, ok := m.runner.(tokenTracerURLProvider); ok {
				url = strings.TrimSpace(provider.TokenTracerURL())
			}
			if url == "" {
				m.addEntry(transcriptEntry{kind: entrySystem, title: "token-tracer", body: "Token Tracer dashboard is not running."})
				return nil
			}
			m.addEntry(transcriptEntry{kind: entrySystem, title: "token-tracer", body: "Token Tracer dashboard: " + url})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/status",
		Description:       "show session status",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			sessionID := m.sessionID
			if sessionID == "" {
				sessionID = "<none>"
			}
			m.addEntry(transcriptEntry{kind: entrySystem, title: "status", body: m.statusText(sessionID)})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/goal",
		Description:       "run and control a long-lived session goal",
		ArgumentHint:      "[start <objective>|status|pause|resume|stop|budget]",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleGoalCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/plan",
		Description:       "author an independent plan document (spec/scope)",
		ArgumentHint:      "[new <requirement>|status|list|show <id>|stop]",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handlePlanCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/mcp",
		Description:       "show MCP server status",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.addEntry(transcriptEntry{kind: entrySystem, title: "mcp", body: m.mcpStatusText()})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/compact",
		Description:       "compact the model context while preserving the full journal",
		ArgumentHint:      "[focus]",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			if m.runner == nil {
				m.addEntry(transcriptEntry{kind: entryError, title: "compact", body: "runner is unavailable"})
				return nil
			}
			if !m.queryGuard.StartModel() {
				m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "/compact is unavailable while a turn is running"})
				return nil
			}
			m.syncRunningFlags()
			m.addEntry(transcriptEntry{kind: entrySystem, title: "compact", body: "compacting model context…"})
			ctx := m.beginModelWorkContext()
			return runContextCompactionCmd(ctx, m.runner, commandArgs(invocation))
		},
	})
	registry.Register(Command{
		Name:              "/clear",
		Description:       "clear in-process chat history",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			if m.runner != nil {
				m.runner.ResetHistory()
			}
			m.transcript = nil
			m.resetToolInspect()
			m.pending = nil
			m.chatQueue.Clear()
			m.activeAssistant = -1
			m.syncInputMode()
			m.clearNewMessageNotice()
			m.addEntry(transcriptEntry{kind: entrySystem, title: "system", body: "history cleared"})
			m.clearNewMessageNotice()
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/resume",
		Description:       "browse and restore previous sessions",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.sessionPicker = newSessionPicker()
			m.pending = nil
			return loadSessionsCmd(m.ctx, m.sessionStore)
		},
	})
	registry.Register(Command{
		Name:              "/exit",
		Aliases:           []string{"/quit", "exit", "quit"},
		Description:       "quit the TUI",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.cancelModelWork()
			m.queryGuard.Cancel()
			m.chatQueue.Clear()
			return tea.Quit
		},
	})
	return registry
}

// Register adds or replaces a slash command.
func (r *CommandRegistry) Register(command Command) {
	if r == nil || command.Name == "" || command.Handler == nil {
		return
	}
	if r.commands == nil {
		r.commands = make(map[string]Command)
	}
	name := strings.TrimSpace(command.Name)
	command.Name = name
	if _, exists := r.commands[name]; !exists {
		r.order = append(r.order, name)
	}
	r.commands[name] = command
	for _, alias := range command.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			r.commands[alias] = command
		}
	}
}

// Lookup resolves a command token to registered metadata.
func (r *CommandRegistry) Lookup(token string) (Command, bool) {
	if r == nil {
		return Command{}, false
	}
	command, ok := r.commands[strings.TrimSpace(token)]
	return command, ok
}

// Dispatch executes a registered slash command or writes stable feedback for
// malformed and unknown command inputs.
func (r *CommandRegistry) Dispatch(m *appModel, line string) (bool, tea.Cmd) {
	if m == nil || strings.TrimSpace(line) == "" {
		return false, nil
	}
	token := commandToken(line)
	if token == "" || token == "/" {
		m.addEntry(transcriptEntry{kind: entryError, title: "command", body: "invalid slash command"})
		return true, nil
	}

	command, ok := r.Lookup(token)
	if !ok {
		m.addEntry(transcriptEntry{kind: entryError, title: "command", body: fmt.Sprintf("unknown command: %s", token)})
		return true, nil
	}
	if m.isWorkRunning() && !command.AllowWhileRunning {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: fmt.Sprintf("%s is unavailable while a turn is running", token)})
		return true, nil
	}
	return true, command.Handler(m, line)
}

// HelpText renders command metadata as a compact, deterministic command table.
func (r *CommandRegistry) HelpText() string {
	if r == nil || len(r.order) == 0 {
		return "No commands registered."
	}

	names := append([]string(nil), r.order...)
	sort.Strings(names)

	type helpRow struct {
		label       string
		description string
	}
	rows := make([]helpRow, 0, len(names))
	labelWidth := 0
	for _, name := range names {
		command, ok := r.commands[name]
		if !ok {
			continue
		}
		label := command.Name
		if hint := strings.TrimSpace(command.ArgumentHint); hint != "" {
			label += " " + hint
		}
		if len(command.Aliases) > 0 {
			aliases := append([]string(nil), command.Aliases...)
			sort.Strings(aliases)
			label += " (aliases: " + strings.Join(aliases, ", ") + ")"
		}
		rows = append(rows, helpRow{label: label, description: strings.TrimSpace(command.Description)})
		labelWidth = max(labelWidth, terminalCellWidth(label))
	}
	if len(rows) == 0 {
		return "No commands registered."
	}

	lines := []string{"Commands"}
	for _, row := range rows {
		padding := strings.Repeat(" ", labelWidth-terminalCellWidth(row.label))
		lines = append(lines, "  "+row.label+padding+"  "+row.description)
	}
	lines = append(lines,
		"",
		"Shortcuts",
		"  !             toggle terminal mode",
		"  !<command>     run a shell command once",
	)
	return strings.Join(lines, "\n")
}

func (m *appModel) mcpStatusText() string {
	if m == nil || m.mcpController == nil {
		return "MCP status controller is unavailable."
	}
	lines := []string{"config: " + m.mcpController.ConfigPath()}
	statuses := m.mcpController.Status()
	if len(statuses) == 0 {
		lines = append(lines, "servers: none")
		return strings.Join(lines, "\n")
	}
	for _, status := range statuses {
		line := fmt.Sprintf("%s state=%s", status.Name, status.State)
		if status.PID > 0 {
			line += fmt.Sprintf(" pid=%d", status.PID)
		}
		line += fmt.Sprintf(" tools=%d resources=%d templates=%d prompts=%d", status.Tools, status.Resources, status.Templates, status.Prompts)
		if status.BlockedTools > 0 {
			line += fmt.Sprintf(" blocked_tools=%d", status.BlockedTools)
		}
		if status.Command != "" {
			line += " command=" + status.Command
		}
		if status.WorkDir != "" {
			line += " cwd=" + status.WorkDir
		}
		if status.LastError != "" {
			line += " error=" + status.LastError
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func commandToken(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return line
	}
	return fields[0]
}

func (m *appModel) handleGoalCommand(invocation string) tea.Cmd {
	if m == nil || m.goalController == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: "goal controller is unavailable"})
		return nil
	}
	args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(invocation), commandToken(invocation)))
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "status" {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal", body: m.goalController.Status()})
		return nil
	}
	switch fields[0] {
	case "start":
		objective := strings.TrimSpace(strings.TrimPrefix(args, "start"))
		if objective == "" {
			m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: "usage: /goal start <objective>"})
			return nil
		}
		id, err := m.goalController.Start(objective)
		if err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: err.Error()})
			return nil
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal", body: "started goal " + id})
		m.goalWorking = true
		m.turnStartedAt = time.Now()
		m.turnID = id
		m.applyCursorAnimation()
		return m.scheduleUIAnimationFrame()
	case "pause":
		if err := m.goalController.Pause(); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: err.Error()})
			return nil
		}
		m.goalWorking = false
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal", body: "goal paused"})
	case "resume":
		if err := m.goalController.Resume(); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: err.Error()})
			return nil
		}
		m.goalWorking = true
		m.turnStartedAt = time.Now()
		m.applyCursorAnimation()
		return m.scheduleUIAnimationFrame()
	case "budget":
		body := m.goalController.Budget()
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal budget", body: body})
	case "stop", "cancel":
		if err := m.goalController.Cancel(); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: err.Error()})
			return nil
		}
		m.goalWorking = false
		m.goalMode = false
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal", body: "goal cancelled"})
	default:
		m.addEntry(transcriptEntry{kind: entryError, title: "goal", body: "usage: /goal start <objective>|status|pause|resume|stop|budget"})
	}
	return nil
}

// handlePlanCommand 处理独立的 Plan 会话命令。Plan 不依赖 Goal：/plan new
// 直接启动一个文档创作会话，其余子命令查看或停止当前会话。
func (m *appModel) handlePlanCommand(invocation string) tea.Cmd {
	if m == nil || m.planController == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: "plan controller is unavailable"})
		return nil
	}
	args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(invocation), commandToken(invocation)))
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "status" {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan", body: m.planController.Status()})
		return nil
	}
	switch fields[0] {
	case "new":
		requirement := strings.TrimSpace(strings.TrimPrefix(args, "new"))
		if requirement == "" {
			m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: "usage: /plan new <requirement>"})
			return nil
		}
		id, err := m.planController.Start(requirement)
		if err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: err.Error()})
			return nil
		}
		m.planWorking = true
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan", body: "started " + id + "\nrequirement: " + requirement})
	case "list":
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan list", body: m.planController.List()})
	case "show":
		if len(fields) < 2 {
			m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: "usage: /plan show <id>"})
			return nil
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan show", body: m.planController.Show(fields[1])})
	case "stop", "cancel":
		if err := m.planController.Cancel(); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: err.Error()})
			return nil
		}
		m.planWorking = false
		m.planMode = false
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan", body: "plan session stopped"})
	default:
		m.addEntry(transcriptEntry{kind: entryError, title: "plan", body: "usage: /plan new <requirement>|status|list|show <id>|stop"})
	}
	return nil
}

func (m *appModel) handleStreamMACommand(invocation string) tea.Cmd {
	invocation = strings.TrimSpace(invocation)
	task := strings.TrimSpace(strings.TrimPrefix(invocation, commandToken(invocation)))
	if task == "" {
		if commandToken(invocation) == "/streamma-trace" {
			m.addEntry(transcriptEntry{
				kind:  entrySystem,
				title: "streamma-trace",
				body:  "usage: /streamma-trace <prompt>\nRuns the same StreamMA DAG and prints live runtime events for step fanout inspection.",
			})
			return nil
		}
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "streamma",
			body:  "usage: /streamma <prompt>\nRuns an adaptive StreamMA DAG with real task workers and END_STEP step exchange.",
		})
		return nil
	}
	m.rememberInputHistory(invocation)
	updated, cmd := m.startChatTurn(invocation)
	*m = updated
	return cmd
}
