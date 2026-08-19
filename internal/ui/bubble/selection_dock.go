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

type selectionPageState struct {
	selected        map[string]bool
	customLabel     string
	customInput     textinput.Model
	focus           selectionFocus
	firstVisible    int
	lastAnswerIndex int
	errorText       string
}

type selectionDock struct {
	request         selecttool.Request
	questions       []selecttool.Question
	questionStates  []selectionPageState
	questionIndex   int
	review          bool
	reviewFocus     int
	focus           selectionFocus
	selected        map[string]bool
	customLabel     string
	customInput     textinput.Model
	editingCustom   bool
	firstVisible    int
	lastAnswerIndex int
	errorText       string
}

func newSelectionDock(request selecttool.Request) *selectionDock {
	questions := questionListForUI(request)
	states := make([]selectionPageState, len(questions))
	for i, question := range questions {
		selected := make(map[string]bool, len(question.InitialSelectedIDs))
		for _, id := range question.InitialSelectedIDs {
			selected[id] = true
		}
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = "Type a custom answer"
		input.CharLimit = 0
		input.Width = 40
		states[i] = selectionPageState{selected: selected, customInput: input}
		if question.Mode == selecttool.ModeSingle && len(question.InitialSelectedIDs) > 0 {
			for optionIndex, option := range question.Options {
				if option.ID == question.InitialSelectedIDs[0] {
					states[i].focus.answerIndex = optionIndex
					states[i].lastAnswerIndex = optionIndex
					break
				}
			}
		}
	}
	d := &selectionDock{request: request.Clone(), questions: questions, questionStates: states}
	d.loadQuestionState(0)
	return d
}

func questionListForUI(r selecttool.Request) []selecttool.Question {
	if len(r.Questions) > 0 {
		return append([]selecttool.Question(nil), r.Questions...)
	}
	return []selecttool.Question{{Prompt: r.Prompt, Mode: r.Mode, Options: append([]selecttool.Option(nil), r.Options...), InitialSelectedIDs: append([]string(nil), r.InitialSelectedIDs...), MinSelect: r.MinSelect, MaxSelect: r.MaxSelect}}
}

func (d *selectionDock) loadQuestionState(index int) {
	if d == nil || index < 0 || index >= len(d.questions) {
		return
	}
	d.questionIndex = index
	question := d.questions[index]
	state := &d.questionStates[index]
	d.request.Prompt = question.Prompt
	d.request.Mode = question.Mode
	d.request.Options = append([]selecttool.Option(nil), question.Options...)
	d.request.InitialSelectedIDs = append([]string(nil), question.InitialSelectedIDs...)
	d.request.MinSelect = question.MinSelect
	d.request.MaxSelect = question.MaxSelect
	d.selected = state.selected
	d.customLabel = state.customLabel
	d.customInput = state.customInput
	d.focus = state.focus
	d.firstVisible = state.firstVisible
	d.lastAnswerIndex = state.lastAnswerIndex
	d.errorText = state.errorText
}

func (d *selectionDock) saveQuestionState() {
	if d == nil || d.questionIndex < 0 || d.questionIndex >= len(d.questionStates) {
		return
	}
	state := &d.questionStates[d.questionIndex]
	state.selected = d.selected
	state.customLabel = d.customLabel
	state.customInput = d.customInput
	state.focus = d.focus
	state.firstVisible = d.firstVisible
	state.lastAnswerIndex = d.lastAnswerIndex
	state.errorText = d.errorText
}

func (d *selectionDock) moveQuestion(delta int) {
	if d == nil || d.review {
		return
	}
	next := clampInt(d.questionIndex+delta, 0, len(d.questions)-1)
	if next == d.questionIndex {
		return
	}
	d.saveQuestionState()
	d.loadQuestionState(next)
}

func (d *selectionDock) enterReview() {
	if d == nil || len(d.questions) == 0 {
		return
	}
	d.saveQuestionState()
	d.review = true
	d.reviewFocus = 0
}

func (d *selectionDock) leaveReview() {
	if d == nil || !d.review || len(d.questions) == 0 {
		return
	}
	d.review = false
	d.loadQuestionState(len(d.questions) - 1)
}

func (d *selectionDock) reviewResults() []selecttool.Result {
	if d == nil {
		return nil
	}
	d.saveQuestionState()
	results := make([]selecttool.Result, len(d.questionStates))
	for i := range d.questionStates {
		state := &d.questionStates[i]
		options := make([]selecttool.SelectedOption, 0, len(state.selected)+1)
		for _, option := range d.questions[i].Options {
			if state.selected[option.ID] {
				options = append(options, selecttool.SelectedOption{ID: option.ID, Label: option.Label})
			}
		}
		if label := strings.TrimSpace(state.customLabel); label != "" {
			options = append(options, selecttool.SelectedOption{ID: selecttool.CustomOptionID, Label: label})
		}
		results[i] = selecttool.Result{SelectedOptions: options}
	}
	return results
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
	if d.request.OptionsOnly {
		last = maxInt(0, len(d.request.Options)-1)
	}
	position = clampInt(position, 0, last)
	switch {
	case d.request.OptionsOnly || position < len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: position}
		d.lastAnswerIndex = position
	case position == len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusCustom}
	default:
		d.focus = selectionFocus{kind: selectionFocusChat}
	}
	d.errorText = ""
}

func (d *selectionDock) move(delta int) { d.setFocusPosition(d.focusPosition() + delta) }
func (d *selectionDock) home()          { d.setFocusPosition(0) }
func (d *selectionDock) end() {
	if d != nil {
		last := len(d.request.Options) + 1
		if d.request.OptionsOnly {
			last = maxInt(0, len(d.request.Options)-1)
		}
		d.setFocusPosition(last)
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

func (d *selectionDock) selectFocusedAnswer() bool {
	if d == nil || d.focus.kind != selectionFocusAnswer || len(d.request.Options) == 0 {
		return false
	}
	id := d.request.Options[d.focus.answerIndex].ID
	if d.request.Mode == selecttool.ModeSingle {
		clear(d.selected)
		d.selected[id] = true
		d.customLabel = ""
		d.errorText = ""
		return true
	}
	if d.selected[id] {
		delete(d.selected, id)
		d.errorText = ""
		return true
	}
	if d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return false
	}
	d.selected[id] = true
	d.errorText = ""
	return true
}

func (d *selectionDock) toggleHighlighted() bool {
	if d == nil || d.request.Mode != selecttool.ModeMultiple {
		return false
	}
	return d.selectFocusedAnswer()
}

func (d *selectionDock) submit() (selecttool.Result, bool) {
	if d == nil {
		return selecttool.Result{}, false
	}
	count := d.selectedCount()
	minSelect := d.request.MinSelect
	if d.request.Mode == selecttool.ModeSingle && minSelect < 1 {
		minSelect = 1
	}
	if count < minSelect {
		d.errorText = fmt.Sprintf("Select at least %d %s.", minSelect, optionNoun(minSelect))
		return selecttool.Result{}, false
	}
	if count > d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return selecttool.Result{}, false
	}
	d.errorText = ""
	return selecttool.Result{SelectedOptions: d.selectedOptions()}, true
}

func (d *selectionDock) cancel() selecttool.Result {
	return selecttool.Result{Cancelled: true, SelectedOptions: []selecttool.SelectedOption{}}
}

func (d *selectionDock) beginCustomEdit() {
	if d == nil || d.request.OptionsOnly {
		return
	}
	if d.request.Mode == selecttool.ModeMultiple && d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
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
	if d.request.Mode == selecttool.ModeMultiple && d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return selecttool.Result{}, false
	}
	d.customLabel = label
	if d.request.Mode == selecttool.ModeSingle {
		clear(d.selected)
	}
	d.cancelCustomEdit()
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
		if d.request.OptionsOnly {
			return selecttool.Result{}, false
		}
		return d.cancel(), true
	case selectionFocusCustom:
		if d.request.OptionsOnly {
			return selecttool.Result{}, false
		}
		d.beginCustomEdit()
		return selecttool.Result{}, false
	case selectionFocusAnswer:
		d.selectFocusedAnswer()
		return selecttool.Result{}, false
	default:
		return selecttool.Result{}, false
	}
}

func (d *selectionDock) answerIndexForScroll(answerCount int) int {
	if d == nil || answerCount == 0 {
		return 0
	}
	if d.focus.kind == selectionFocusAnswer {
		d.lastAnswerIndex = clampInt(d.focus.answerIndex, 0, answerCount-1)
	}
	return clampInt(d.lastAnswerIndex, 0, answerCount-1)
}

func (d *selectionDock) visibleRange(heights []int, budget int) (int, int) {
	if d == nil || len(heights) == 0 || budget <= 0 {
		return 0, 0
	}
	focused := d.answerIndexForScroll(len(heights))
	if maxInt(1, heights[focused]) > budget {
		return 0, 0
	}
	start := clampInt(d.firstVisible, 0, focused)
	for start <= focused {
		used := 0
		end := start
		for end < len(heights) && used+maxInt(1, heights[end]) <= budget {
			used += maxInt(1, heights[end])
			end++
		}
		if focused < end {
			d.firstVisible = start
			return start, end
		}
		start++
	}
	return 0, 0
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

func (d *selectionDock) reviewResult() selecttool.Result {
	results := d.reviewResults()
	return selecttool.Result{Results: results}
}

func (d *selectionDock) reviewCancel() selecttool.Result {
	results := make([]selecttool.Result, len(d.questions))
	for i := range results {
		results[i] = selecttool.Result{Cancelled: true, SelectedOptions: []selecttool.SelectedOption{}}
	}
	return selecttool.Result{Cancelled: true, Results: results}
}

func (m appModel) handleSelectionDockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.selectionDock
	if d.review {
		var cmd tea.Cmd
		switch msg.String() {
		case "up", "k":
			d.reviewFocus = 0
		case "down", "j":
			d.reviewFocus = 1
		case "left", "h":
			d.leaveReview()
		case " ", "space", "enter":
			if d.reviewFocus == 0 {
				cmd = m.completeSelection(d.reviewResult())
			} else {
				cmd = m.completeSelection(d.reviewCancel())
			}
		case "esc", "ctrl+c":
			cmd = m.completeSelection(d.reviewCancel())
		}
		m.relayout()
		return m, cmd
	}
	if d.editingCustom {
		switch msg.String() {
		case "enter":
			d.confirmCustom()
		case "esc":
			d.cancelCustomEdit()
		case "ctrl+c":
			cmd := m.completeSelection(d.cancel())
			m.relayout()
			return m, cmd
		default:
			var inputCmd tea.Cmd
			d.customInput, inputCmd = d.customInput.Update(msg)
			d.errorText = ""
			m.relayout()
			return m, inputCmd
		}
		m.relayout()
		return m, nil
	}

	var cmd tea.Cmd
	switch msg.String() {
	case "left", "h":
		d.moveQuestion(-1)
	case "right", "l":
		if d.questionIndex == len(d.questions)-1 {
			d.enterReview()
		} else {
			d.moveQuestion(1)
		}
	case "up", "k":
		d.move(-1)
	case "down", "j":
		d.move(1)
	case "home":
		d.home()
	case "end":
		d.end()
	case " ", "space":
		d.activateFocused()
	case "enter":
		if len(d.questions) == 1 {
			if d.focus.kind == selectionFocusChat {
				cmd = m.completeSelection(d.cancel())
			} else if result, complete := d.submit(); complete {
				cmd = m.completeSelection(result)
			}
		} else if d.focus.kind == selectionFocusChat {
			cmd = m.completeSelection(d.cancel())
		} else {
			d.activateFocused()
		}
	case "esc", "ctrl+c":
		cmd = m.completeSelection(d.cancel())
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
