package bubble

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	selecttool "paw/internal/tool/select"
)

type selectionBrokerEventMsg struct {
	event selecttool.Event
	err   error
}

type selectionFocusKind uint8

const (
	selectionFocusAnswer selectionFocusKind = iota
	selectionFocusCustom
	selectionFocusChat
)

type selectionFocus struct {
	kind        selectionFocusKind
	answerIndex int
}

type selectionDock struct {
	request       selecttool.Request
	focus         selectionFocus
	selected      map[string]bool
	customLabel   string
	customInput   textinput.Model
	editingCustom bool
	firstVisible  int
	errorText     string

	// highlighted is retained until the task 4 renderer is migrated to focus.
	highlighted int
}

func newSelectionDock(request selecttool.Request) *selectionDock {
	selected := make(map[string]bool, len(request.InitialSelectedIDs))
	for _, id := range request.InitialSelectedIDs {
		selected[id] = true
	}
	customInput := textinput.New()
	customInput.Prompt = ""
	customInput.Placeholder = "Type a custom answer"
	customInput.CharLimit = 0
	customInput.Width = 40
	d := &selectionDock{
		request:     request.Clone(),
		selected:    selected,
		customInput: customInput,
	}
	if request.Mode == selecttool.ModeSingle && len(request.InitialSelectedIDs) > 0 {
		for i, option := range request.Options {
			if option.ID == request.InitialSelectedIDs[0] {
				d.focus.answerIndex = i
				d.highlighted = i
				break
			}
		}
	}
	return d
}

func (d *selectionDock) focusPosition() int {
	if d == nil {
		return 0
	}
	switch d.focus.kind {
	case selectionFocusCustom:
		return len(d.request.Options)
	case selectionFocusChat:
		return len(d.request.Options) + 1
	default:
		if len(d.request.Options) == 0 {
			return 0
		}
		return clampInt(d.focus.answerIndex, 0, len(d.request.Options)-1)
	}
}

func (d *selectionDock) setFocusPosition(position int) {
	if d == nil {
		return
	}
	last := len(d.request.Options) + 1
	position = clampInt(position, 0, last)
	switch {
	case position < len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: position}
		d.highlighted = position
	case position == len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusCustom}
		if len(d.request.Options) > 0 {
			d.highlighted = len(d.request.Options) - 1
		}
	default:
		d.focus = selectionFocus{kind: selectionFocusChat}
		if len(d.request.Options) > 0 {
			d.highlighted = len(d.request.Options) - 1
		}
	}
	d.errorText = ""
}

func (d *selectionDock) move(delta int) { d.setFocusPosition(d.focusPosition() + delta) }
func (d *selectionDock) home()          { d.setFocusPosition(0) }
func (d *selectionDock) end() {
	if d != nil {
		d.setFocusPosition(len(d.request.Options) + 1)
	}
}

func (d *selectionDock) selectedCount() int {
	if d == nil {
		return 0
	}
	count := len(d.selected)
	if strings.TrimSpace(d.customLabel) != "" {
		count++
	}
	return count
}

func (d *selectionDock) selectedOptions() []selecttool.SelectedOption {
	out := make([]selecttool.SelectedOption, 0, d.selectedCount())
	for _, option := range d.request.Options {
		if d.selected[option.ID] {
			out = append(out, selecttool.SelectedOption{ID: option.ID, Label: option.Label})
		}
	}
	if label := strings.TrimSpace(d.customLabel); label != "" {
		out = append(out, selecttool.SelectedOption{ID: selecttool.CustomOptionID, Label: label})
	}
	return out
}

func (d *selectionDock) toggleHighlighted() bool {
	if d == nil || d.focus.kind != selectionFocusAnswer || d.request.Mode != selecttool.ModeMultiple || len(d.request.Options) == 0 {
		return false
	}
	id := d.request.Options[d.focus.answerIndex].ID
	if d.selected[id] {
		delete(d.selected, id)
		d.errorText = ""
		return true
	}
	if d.selectedCount() >= d.request.MaxSelect {
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
		if d.focus.kind != selectionFocusAnswer {
			return selecttool.Result{}, false
		}
		option := d.request.Options[d.focus.answerIndex]
		return selecttool.Result{SelectedOptions: []selecttool.SelectedOption{{ID: option.ID, Label: option.Label}}}, true
	}
	count := d.selectedCount()
	if count < d.request.MinSelect {
		d.errorText = fmt.Sprintf("Select at least %d options.", d.request.MinSelect)
		return selecttool.Result{}, false
	}
	if count > d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d options.", d.request.MaxSelect)
		return selecttool.Result{}, false
	}
	return selecttool.Result{SelectedOptions: d.selectedOptions()}, true
}

func (d *selectionDock) cancel() selecttool.Result {
	return selecttool.Result{Cancelled: true, SelectedOptions: []selecttool.SelectedOption{}}
}

func (d *selectionDock) beginCustomEdit() {
	if d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return
	}
	d.customInput.SetValue(d.customLabel)
	d.customInput.CursorEnd()
	d.customInput.Focus()
	d.editingCustom = true
	d.errorText = ""
}

func (d *selectionDock) cancelCustomEdit() {
	d.editingCustom = false
	d.customInput.Blur()
	d.errorText = ""
}

func (d *selectionDock) confirmCustom() (selecttool.Result, bool) {
	label := strings.TrimSpace(d.customInput.Value())
	if label == "" {
		d.errorText = "Custom option cannot be empty."
		return selecttool.Result{}, false
	}
	if d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return selecttool.Result{}, false
	}
	d.customLabel = label
	d.cancelCustomEdit()
	if d.request.Mode == selecttool.ModeSingle {
		return selecttool.Result{SelectedOptions: []selecttool.SelectedOption{{ID: selecttool.CustomOptionID, Label: label}}}, true
	}
	return selecttool.Result{}, false
}

func optionNoun(count int) string {
	if count == 1 {
		return "option"
	}
	return "options"
}

func (d *selectionDock) activateFocused() (selecttool.Result, bool) {
	if d == nil {
		return selecttool.Result{}, false
	}
	switch d.focus.kind {
	case selectionFocusChat:
		return d.cancel(), true
	case selectionFocusCustom:
		d.beginCustomEdit()
		return selecttool.Result{}, false
	case selectionFocusAnswer:
		if len(d.request.Options) == 0 {
			return selecttool.Result{}, false
		}
		if d.request.Mode == selecttool.ModeSingle {
			option := d.request.Options[d.focus.answerIndex]
			return selecttool.Result{SelectedOptions: []selecttool.SelectedOption{{ID: option.ID, Label: option.Label}}}, true
		}
		d.toggleHighlighted()
		return selecttool.Result{}, false
	default:
		return selecttool.Result{}, false
	}
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
	if m.selectionDock.editingCustom {
		switch msg.String() {
		case "enter":
			if result, complete := m.selectionDock.confirmCustom(); complete {
				cmd := m.completeSelection(result)
				m.relayout()
				return m, cmd
			}
		case "esc":
			m.selectionDock.cancelCustomEdit()
		case "ctrl+c":
			cmd := m.completeSelection(m.selectionDock.cancel())
			m.relayout()
			return m, cmd
		default:
			var inputCmd tea.Cmd
			m.selectionDock.customInput, inputCmd = m.selectionDock.customInput.Update(msg)
			m.selectionDock.errorText = ""
			m.relayout()
			return m, inputCmd
		}
		m.relayout()
		return m, nil
	}

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
		if m.selectionDock.focus.kind == selectionFocusAnswer {
			m.selectionDock.toggleHighlighted()
		} else if result, complete := m.selectionDock.activateFocused(); complete {
			cmd = m.completeSelection(result)
		}
	case "enter":
		var result selecttool.Result
		var complete bool
		if m.selectionDock.focus.kind == selectionFocusAnswer && m.selectionDock.request.Mode == selecttool.ModeMultiple {
			result, complete = m.selectionDock.submit()
		} else {
			result, complete = m.selectionDock.activateFocused()
		}
		if complete {
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
