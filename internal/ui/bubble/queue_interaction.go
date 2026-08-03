package bubble

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type queueInteractionMode uint8

const (
	queueModeInactive queueInteractionMode = iota
	queueModeSelecting
	queueModeEditing
)

type inputSource uint8

const (
	inputSourceFresh inputSource = iota
	inputSourceHistory
	inputSourceQueueEdit
)

type queueEditState struct {
	item       queuedChatItem
	originalAt int
}

func (m appModel) queueSelectedIndex() int {
	if m.queueSelectedID == "" {
		return -1
	}
	for i, item := range m.chatQueue.Items() {
		if item.ID == m.queueSelectedID {
			return i
		}
	}
	return -1
}

func (m appModel) queueSelectedItem() (queuedChatItem, bool) {
	index := m.queueSelectedIndex()
	if index < 0 {
		return queuedChatItem{}, false
	}
	items := m.chatQueue.Items()
	return items[index], true
}

func (m *appModel) setQueueSelection(id string) {
	if m == nil {
		return
	}
	m.queueSelectedID = id
}

func (m *appModel) normalizeQueueSelection() {
	if m == nil {
		return
	}
	items := m.chatQueue.Items()
	if len(items) == 0 {
		m.queueSelectedID = ""
		return
	}
	if m.queueSelectedIndex() >= 0 {
		return
	}
	m.queueSelectedID = items[len(items)-1].ID
}

func (m appModel) inputCursorAtEnd() bool {
	return textareaCursorAtEnd(m.input)
}

func (m appModel) canEnterQueueSelection() bool {
	if m.queueMode != queueModeInactive || m.chatQueue.Len() == 0 || !m.inputCursorAtEnd() {
		return false
	}
	if m.historyIndex != -1 || m.historyDownLock || m.inputSource == inputSourceHistory {
		return false
	}
	if m.completion != nil || m.selectionDock != nil || m.terminalMode || m.isTerminalWorkRunning() {
		return false
	}
	return m.inputSource == inputSourceFresh
}

func (m *appModel) leaveQueueSelection() {
	if m == nil {
		return
	}
	m.queueMode = queueModeInactive
	m.queueSelectedID = ""
	m.input.Focus()
	m.relayout()
}

func (m *appModel) clearQueueEditState() {
	if m == nil {
		return
	}
	m.queueEdit = nil
	m.inputSource = inputSourceFresh
}

func (m appModel) enterQueueSelection() (tea.Model, tea.Cmd) {
	m.queueMode = queueModeSelecting
	items := m.chatQueue.Items()
	if len(items) > 0 {
		m.queueSelectedID = items[len(items)-1].ID
	}
	m.relayout()
	return m, nil
}

func (m appModel) moveSelectedQueueItem(delta int) (tea.Model, tea.Cmd) {
	if m.queueSelectedID != "" {
		m.chatQueue.Move(m.queueSelectedID, delta)
	}
	m.normalizeQueueSelection()
	m.relayout()
	return m, nil
}

func (m appModel) beginQueueEdit() (tea.Model, tea.Cmd) {
	index := m.queueSelectedIndex()
	if index < 0 {
		return m, nil
	}
	item, ok := m.chatQueue.RemoveAt(index)
	if !ok {
		return m, nil
	}
	m.queueEdit = &queueEditState{item: item, originalAt: index}
	m.queueMode = queueModeEditing
	m.queueSelectedID = ""
	m.inputSource = inputSourceQueueEdit
	m.setInputDraft(item.Draft)
	m.inputPasteFoldActive = false
	m.resetHistoryNavigation()
	m.clearCompletionAndRelayout()
	m.input.Focus()
	m.relayout()
	return m, nil
}

func (m appModel) cancelQueueEdit() (tea.Model, tea.Cmd) {
	if m.queueEdit != nil {
		m.chatQueue.InsertAt(m.queueEdit.originalAt, m.queueEdit.item)
	}
	m.input.SetValue("")
	m.clearInputTokens()
	m.queueMode = queueModeInactive
	m.clearQueueEditState()
	m.resetHistoryNavigation()
	m.input.Focus()
	m.relayout()
	return m, nil
}

func (m appModel) saveQueueEdit() (tea.Model, tea.Cmd) {
	if m.queueEdit == nil {
		return m, nil
	}
	draft := trimInputDraft(m.currentInputDraft())
	if strings.TrimSpace(draft.Text) == ":wq" {
		draft = cloneInputDraft(m.queueEdit.item.Draft)
	}
	if strings.TrimSpace(draft.Text) != "" {
		m.chatQueue.EnqueueDraft(draft)
	}
	m.input.SetValue("")
	m.clearInputTokens()
	m.queueMode = queueModeInactive
	m.clearQueueEditState()
	m.resetHistoryNavigation()
	m.input.Focus()
	m.relayout()
	return m, nil
}

func (m appModel) handleQueueEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		model, cmd := m.cancelQueueEdit()
		return model, cmd
	case "enter":
		model, cmd := m.saveQueueEdit()
		return model, cmd
	}
	model, cmd := m.updateInputWithKey(msg)
	return model, cmd
}

func (m appModel) handleQueueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		items := m.chatQueue.Items()
		index := m.queueSelectedIndex()
		if index > 0 {
			m.queueSelectedID = items[index-1].ID
		}
		m.relayout()
		return m, nil
	case "down":
		items := m.chatQueue.Items()
		index := m.queueSelectedIndex()
		if index >= 0 && index+1 < len(items) {
			m.queueSelectedID = items[index+1].ID
		}
		m.relayout()
		return m, nil
	case "alt+k", "cmd+k":
		model, cmd := m.moveSelectedQueueItem(-1)
		return model, cmd
	case "alt+j", "cmd+j":
		model, cmd := m.moveSelectedQueueItem(1)
		return model, cmd
	case "i":
		model, cmd := m.beginQueueEdit()
		return model, cmd
	case "d":
		if m.queueSelectedID != "" {
			m.chatQueue.Remove(m.queueSelectedID)
		}
		m.normalizeQueueSelection()
		if m.chatQueue.Len() == 0 {
			m.leaveQueueSelection()
		} else {
			m.relayout()
		}
		return m, nil
	case "c":
		m.chatQueue.Clear()
		m.leaveQueueSelection()
		return m, nil
	case "esc":
		m.leaveQueueSelection()
		return m, nil
	}
	return m, nil
}
