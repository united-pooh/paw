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
		AllowWhileRunning: false,
		Handler: func(m *appModel, invocation string) tea.Cmd {
			m.modelWizard = newModelWizard(m.currentModelConfig())
			m.pending = nil
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
			m.addEntry(transcriptEntry{kind: entrySystem, title: "status", body: fmt.Sprintf("session: %s", sessionID)})
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
			m.pending = nil
			m.chatQueue.Clear()
			m.activeAssistant = -1
			m.syncInputMode()
			m.addEntry(transcriptEntry{kind: entrySystem, title: "system", body: "history cleared"})
			return nil
		},
	})
	registry.Register(Command{
		Name:              "/exit",
		Aliases:           []string{"/quit"},
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
	if m == nil || !strings.HasPrefix(strings.TrimSpace(line), "/") {
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
