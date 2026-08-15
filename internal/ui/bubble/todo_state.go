package bubble

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/todo"
)

type todoBrokerEventMsg struct {
	snapshot todo.Snapshot
	err      error
}

func waitTodoBrokerEventCmd(ctx context.Context, broker *todo.Broker) tea.Cmd {
	if broker == nil {
		return nil
	}
	return func() tea.Msg {
		snapshot, err := broker.Next(ctx)
		return todoBrokerEventMsg{snapshot: snapshot, err: err}
	}
}

func (m *appModel) applyTodoSnapshot(snapshot todo.Snapshot, live bool) bool {
	if m == nil {
		return false
	}
	normalized, err := todo.ValidateSnapshot(snapshot)
	if err != nil {
		return false
	}
	copy := normalized.Clone()
	if copy.Cleared() {
		m.currentTodo = todo.Snapshot{}
		m.hasCurrentTodo = false
		m.todoWasCleared = true
	} else {
		m.currentTodo = copy.Clone()
		m.hasCurrentTodo = true
		m.todoWasCleared = false
	}
	return true
}

func (m appModel) assistantFinalAnswerVisible(index int) bool {
	if index < 0 || index >= len(m.transcript) {
		return false
	}
	entry := m.transcript[index]
	return entry.kind == entryAssistant && strings.TrimSpace(sanitizeAssistantVisibleBody(entry.body)) != ""
}

func todoBrokerEventCommand(m appModel, msg todoBrokerEventMsg) (appModel, tea.Cmd) {
	if errors.Is(msg.err, todo.ErrBrokerClosed) || errors.Is(msg.err, context.Canceled) {
		return m, nil
	}
	if msg.err == nil {
		m.applyTodoSnapshot(msg.snapshot, true)
	}
	return m, waitTodoBrokerEventCmd(m.ctx, m.todoBroker)
}
