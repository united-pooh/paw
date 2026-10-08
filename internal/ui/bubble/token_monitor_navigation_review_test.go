package bubble

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTokenMonitorNavigationPassiveSidebarKeepsTypingInInput(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.width, m.height, m.ready = width, 30, true
			m.tokenMonitor.reader = &fakeTokenMonitorReader{}
			m.closeActivity()
			m.input.SetValue("draft ")
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			m = next.(appModel)
			if !m.activity.visible || !m.input.Focused() {
				t.Fatal("opening passive sidebar stole input focus")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fjkh")})
			m = next.(appModel)
			if m.input.Value() != "draft fjkh" || m.taskPreview != nil {
				t.Fatalf("former sidebar shortcuts did not remain ordinary input: draft=%q activity=%+v", m.input.Value(), m.activity)
			}
			before := m.activity.tab
			for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, {Type: tea.KeyEsc}} {
				next, _ = m.Update(key)
				m = next.(appModel)
				if m.activity.tab != before || !m.input.Focused() {
					t.Fatal("ordinary input key changed sidebar source or focus")
				}
			}
			if m.currentLayout().workspaceWidth == 0 {
				t.Fatal("passive sidebar hid workspace input")
			}
		})
	}
}

func TestTokenMonitorNavigationCtrlWEditsInputWithSidebarVisible(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height, m.ready = 120, 30, true
	m.tokenMonitor.reader = &fakeTokenMonitorReader{}
	m.openActivity(activityTabTasks)
	m.input.SetValue("keep drop")
	m.input.CursorEnd()
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlW}, {Type: tea.KeyRunes, Runes: []rune{'l'}}} {
		next, _ := m.Update(key)
		m = next.(appModel)
	}
	if m.input.Value() != "keep l" || !m.input.Focused() {
		t.Fatalf("Ctrl+W remained a panel command: input=%q activity=%+v", m.input.Value(), m.activity)
	}
}

func TestTokenMonitorNavigationLatePollDoesNotReopenOrReschedule(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height, m.ready = 120, 30, true
	m.tokenMonitor.reader = &fakeTokenMonitorReader{}
	m.openActivity(activityTabTasks)
	cmd := m.tokenMonitorCommand()
	if cmd == nil {
		t.Fatal("fixture did not begin always-visible token query")
	}
	result := cmd()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = next.(appModel)
	next, followup := m.Update(result)
	m = next.(appModel)
	if m.activity.visible || followup != nil || m.tokenMonitor.loading || m.tokenMonitor.scheduled {
		t.Fatalf("late token query reopened sidebar or resumed polling: activity=%+v monitor=%+v", m.activity, m.tokenMonitor)
	}
}
