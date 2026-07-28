package bubble

import (
	"fmt"
	"sort"
	"strings"

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
		Name:              "/model",
		Description:       "open the model switcher",
		ArgumentHint:      "[status|custom|deepseek|<model>]",
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
		Name:              "/setting",
		Description:       "open settings wizard",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.settingWizard = newSettingWizard(m.currentSettings())
			m.pending = nil
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/subagent",
		Description:       "launch a subagent",
		ArgumentHint:      "[--fork|--empty] [--background|--sync] <prompt>",
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			return m.handleSubagentCommand(invocation)
		},
	})
	registry.Register(Command{
		Name:              "/streamma",
		Description:       "run a prompt through StreamMA subagents",
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
		Description:       "show background subagent tasks",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.openActivity(activityTabSubagents)
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/pipeline",
		Description:       "show the current pipeline activity",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.openActivity(activityTabPipeline)
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
		Name:              "/mcp",
		Description:       "show MCP server status",
		AllowWhileRunning: true,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.addEntry(transcriptEntry{kind: entrySystem, title: "mcp", body: m.mcpStatusText()})
			return nil
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
			m.addEntry(transcriptEntry{kind: entrySystem, title: "system", body: "history cleared"})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/sessions",
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

// HelpText renders command metadata in a deterministic order.
func (r *CommandRegistry) HelpText() string {
	if r == nil {
		return "No commands registered."
	}
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	lines := []string{"Commands:"}
	for _, name := range names {
		command, ok := r.commands[name]
		if !ok {
			continue
		}
		label := command.Name
		if strings.TrimSpace(command.ArgumentHint) != "" {
			label += " " + strings.TrimSpace(command.ArgumentHint)
		}
		if len(command.Aliases) > 0 {
			aliases := append([]string(nil), command.Aliases...)
			sort.Strings(aliases)
			label += " (" + strings.Join(aliases, ", ") + ")"
		}
		lines = append(lines, fmt.Sprintf("%s - %s", label, command.Description))
	}
	lines = append(lines, "Type ! to toggle terminal mode or !<command> to run bash once.")
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
			body:  "usage: /streamma <prompt>\nRuns an adaptive StreamMA DAG with real subagent workers and END_STEP step exchange.",
		})
		return nil
	}
	m.rememberInputHistory(invocation)
	updated, cmd := m.startChatTurn(invocation)
	*m = updated
	return cmd
}
