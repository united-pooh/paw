package bubble

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	selecttool "paw/internal/tool/select"
)

type selectionBrokerEventMsg struct {
	event selecttool.Event
	err   error
}

type selectionDock struct {
	request      selecttool.Request
	highlighted  int
	selected     map[string]bool
	firstVisible int
	errorText    string
}

func newSelectionDock(request selecttool.Request) *selectionDock {
	selected := make(map[string]bool, len(request.InitialSelectedIDs))
	for _, id := range request.InitialSelectedIDs {
		selected[id] = true
	}
	d := &selectionDock{request: request.Clone(), selected: selected}
	if request.Mode == selecttool.ModeSingle && len(request.InitialSelectedIDs) > 0 {
		for i, option := range request.Options {
			if option.ID == request.InitialSelectedIDs[0] {
				d.highlighted = i
				break
			}
		}
	}
	return d
}

func (d *selectionDock) move(delta int) {
	if d == nil || len(d.request.Options) == 0 {
		return
	}
	d.highlighted = clampInt(d.highlighted+delta, 0, len(d.request.Options)-1)
	d.errorText = ""
}
func (d *selectionDock) home() {
	if d != nil && len(d.request.Options) > 0 {
		d.highlighted = 0
		d.errorText = ""
	}
}
func (d *selectionDock) end() {
	if d != nil && len(d.request.Options) > 0 {
		d.highlighted = len(d.request.Options) - 1
		d.errorText = ""
	}
}
func (d *selectionDock) toggleHighlighted() bool {
	if d == nil || d.request.Mode != selecttool.ModeMultiple || len(d.request.Options) == 0 {
		return false
	}
	id := d.request.Options[d.highlighted].ID
	if d.selected[id] {
		delete(d.selected, id)
		d.errorText = ""
		return true
	}
	if len(d.selected) >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d options.", d.request.MaxSelect)
		return false
	}
	d.selected[id] = true
	d.errorText = ""
	return true
}
func (d *selectionDock) submit() (selecttool.Result, bool) {
	if d == nil || len(d.request.Options) == 0 {
		return selecttool.Result{}, false
	}
	if d.request.Mode == selecttool.ModeSingle {
		return selecttool.Result{SelectedIDs: []string{d.request.Options[d.highlighted].ID}}, true
	}
	count := len(d.selected)
	if count < d.request.MinSelect {
		d.errorText = fmt.Sprintf("Select at least %d options.", d.request.MinSelect)
		return selecttool.Result{}, false
	}
	if count > d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d options.", d.request.MaxSelect)
		return selecttool.Result{}, false
	}
	ids := make([]string, 0, count)
	for _, o := range d.request.Options {
		if d.selected[o.ID] {
			ids = append(ids, o.ID)
		}
	}
	return selecttool.Result{SelectedIDs: ids}, true
}
func (d *selectionDock) cancel() selecttool.Result {
	return selecttool.Result{Cancelled: true, SelectedIDs: []string{}}
}

func (d *selectionDock) visibleRange(heights []int, budget int) (int, int) {
	if d == nil || len(heights) == 0 || budget <= 0 {
		return 0, 0
	}
	d.highlighted = clampInt(d.highlighted, 0, len(heights)-1)
	start := clampInt(d.firstVisible, 0, d.highlighted)
	for {
		used := 0
		end := start
		for end < len(heights) && (used+maxInt(1, heights[end]) <= budget || end == start) {
			used += maxInt(1, heights[end])
			end++
		}
		if d.highlighted < end {
			d.firstVisible = start
			return start, end
		}
		start++
	}
}

func waitSelectionBrokerEventCmd(ctx context.Context, broker *selecttool.Broker) tea.Cmd {
	if broker == nil {
		return nil
	}
	return func() tea.Msg {
		event, err := broker.NextEvent(ctx)
		return selectionBrokerEventMsg{event: event, err: err}
	}
}

func (m appModel) handleSelectionDockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg.String() {
	case "up", "k":
		m.selectionDock.move(-1)
	case "down", "j":
		m.selectionDock.move(1)
	case "home":
		m.selectionDock.home()
	case "end":
		m.selectionDock.end()
	case " ", "space":
		m.selectionDock.toggleHighlighted()
	case "enter":
		if result, ok := m.selectionDock.submit(); ok {
			cmd = m.completeSelection(result)
		}
	case "esc", "ctrl+c":
		cmd = m.completeSelection(m.selectionDock.cancel())
	}
	m.relayout()
	return m, cmd
}
func (m *appModel) completeSelection(result selecttool.Result) tea.Cmd {
	if m.selectionDock == nil || m.selectionBroker == nil {
		return nil
	}
	id := m.selectionDock.request.ID
	if !m.selectionBroker.Complete(id, result) {
		return nil
	}
	m.selectionDock = nil
	m.relayout()
	return m.input.Focus()
}
