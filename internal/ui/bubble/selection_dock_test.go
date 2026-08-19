package bubble

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	selecttool "paw/internal/tool/select"
)

func selectionRequest(id string, mode selecttool.Mode) selecttool.Request {
	return selecttool.Request{ID: id, Prompt: "Choose signals", Mode: mode, Options: []selecttool.Option{{ID: "logs", Label: "Logs", Description: "Application logs"}, {ID: "metrics", Label: "Metrics"}, {ID: "traces", Label: "Traces"}}, MinSelect: 0, MaxSelect: 3}
}

func selectionKeyTestModel(t *testing.T, request selecttool.Request) (appModel, <-chan selecttool.Result) {
	t.Helper()
	broker := selecttool.NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	done := make(chan selecttool.Result, 1)
	go func() {
		result, _ := broker.Ask(ctx, request)
		done <- result
	}()
	event, err := broker.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(ctx, &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionBroker = broker
	m.selectionDock = newSelectionDock(event.Request)
	return m, done
}
func TestNewSelectionDockAndNavigation(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.InitialSelectedIDs = []string{"metrics"}
	d := newSelectionDock(r)
	if d.focus.answerIndex != 0 || d.lastAnswerIndex != 0 || !d.selected["metrics"] {
		t.Fatalf("dock=%#v", d)
	}
	d.move(-1)
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatal("above")
	}
	d.end()
	if d.lastAnswerIndex != 0 || d.focus.kind != selectionFocusChat {
		t.Fatalf("end lastAnswerIndex=%d focus=%#v", d.lastAnswerIndex, d.focus)
	}
	d.move(1)
	if d.focus.kind != selectionFocusChat {
		t.Fatal("wrapped")
	}
	d.home()
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatal("home")
	}
}
func TestSelectionDockNavigatesAnswersAndFixedActions(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatalf("initial focus=%#v", d.focus)
	}
	d.end()
	if d.focus.kind != selectionFocusChat {
		t.Fatalf("end focus=%#v, want chat action", d.focus)
	}
	d.move(-1)
	if d.focus.kind != selectionFocusCustom {
		t.Fatalf("focus=%#v, want custom action", d.focus)
	}
	d.move(-1)
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 2 {
		t.Fatalf("focus=%#v, want final answer", d.focus)
	}
	d.home()
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatalf("home focus=%#v", d.focus)
	}
}

func TestSelectionDockBuildsStableReadableOptions(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.selected["traces"] = true
	d.selected["logs"] = true
	result, ok := d.submit()
	want := []selecttool.SelectedOption{
		{ID: "logs", Label: "Logs"},
		{ID: "traces", Label: "Traces"},
	}
	if !ok || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", result, ok)
	}
}

func TestSelectionDockChatUsesCancellationResult(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.focus = selectionFocus{kind: selectionFocusChat}
	result, complete := d.activateFocused()
	if !complete || !result.Cancelled || result.SelectedOptions == nil || len(result.SelectedOptions) != 0 {
		t.Fatalf("result=%#v complete=%v", result, complete)
	}
}

func TestSelectionDockToggleAndSubmit(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.MinSelect = 1
	r.MaxSelect = 1
	d := newSelectionDock(r)
	if !d.toggleHighlighted() {
		t.Fatal("toggle")
	}
	d.move(1)
	if d.toggleHighlighted() || d.errorText == "" {
		t.Fatal("max")
	}
	result, ok := d.submit()
	want := []selecttool.SelectedOption{{ID: "logs", Label: "Logs"}}
	if !ok || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", result, ok)
	}
}
func TestSelectionDockSingleSubmitUsesSelectedState(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeSingle)
	r.MinSelect = 1
	r.MaxSelect = 1
	d := newSelectionDock(r)
	d.move(1)
	if got, ok := d.submit(); ok || len(got.SelectedOptions) != 0 || d.errorText != "Select at least 1 option." {
		t.Fatalf("focused unselected answer submitted: result=%#v ok=%v error=%q", got, ok, d.errorText)
	}
	d.selected["logs"] = true
	got, ok := d.submit()
	want := []selecttool.SelectedOption{{ID: "logs", Label: "Logs"}}
	if !ok || !reflect.DeepEqual(got.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", got, ok)
	}
}
func TestSelectionDockMultipleKeysSpaceTogglesAndEnterSubmits(t *testing.T) {
	r := selectionRequest("", selecttool.ModeMultiple)
	r.MinSelect = 1
	r.MaxSelect = 2
	m, done := selectionKeyTestModel(t, r)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := next.(appModel)
	if got.selectionDock == nil || !got.selectionDock.selected["logs"] {
		t.Fatalf("Space did not select focused answer: dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		t.Fatalf("Space completed multiple selection: %#v", result)
	default:
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock != nil {
		t.Fatalf("Enter did not submit current selection: dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		want := []selecttool.SelectedOption{{ID: "logs", Label: "Logs"}}
		if !reflect.DeepEqual(result.SelectedOptions, want) {
			t.Fatalf("result=%#v, want %#v", result, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter submission blocked")
	}
}

func TestSelectionDockMultipleEnterValidatesMinimumWithoutToggling(t *testing.T) {
	r := selectionRequest("", selecttool.ModeMultiple)
	r.MinSelect = 2
	r.MaxSelect = 3
	r.InitialSelectedIDs = []string{"logs"}
	m, done := selectionKeyTestModel(t, r)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(appModel)
	if got.selectionDock == nil {
		t.Fatal("Enter completed selection below minimum")
	}
	if !got.selectionDock.selected["logs"] {
		t.Fatal("Enter toggled the focused answer instead of validating")
	}
	if got.selectionDock.errorText != "Select at least 2 options." {
		t.Fatalf("errorText=%q", got.selectionDock.errorText)
	}
	select {
	case result := <-done:
		t.Fatalf("invalid Enter completed selection: %#v", result)
	default:
	}
}

func TestSelectionDockMultipleSpaceEnforcesMaximumWithoutCompleting(t *testing.T) {
	r := selectionRequest("", selecttool.ModeMultiple)
	r.MaxSelect = 1
	m, done := selectionKeyTestModel(t, r)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := next.(appModel)
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = next.(appModel)
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeySpace})
	got = next.(appModel)
	if got.selectionDock == nil || !got.selectionDock.selected["logs"] || got.selectionDock.selected["metrics"] {
		t.Fatalf("maximum selection state=%#v", got.selectionDock)
	}
	if got.selectionDock.errorText != "You can select at most 1 option." {
		t.Fatalf("errorText=%q", got.selectionDock.errorText)
	}
	select {
	case result := <-done:
		t.Fatalf("Space completed selection at maximum: %#v", result)
	default:
	}
}

func TestSelectionBrokerSingleSpaceSelectsAndEnterSubmits(t *testing.T) {
	b := selecttool.NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan selecttool.Result, 1)
	go func() { r, _ := b.Ask(ctx, selectionRequest("", selecttool.ModeSingle)); done <- r }()
	event, err := b.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(ctx, &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionBroker = b
	next, _ := m.Update(selectionBrokerEventMsg{event: event})
	got := next.(appModel)
	if got.selectionDock == nil {
		t.Fatal("dock not opened")
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = next.(appModel)
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeySpace})
	got = next.(appModel)
	if got.selectionDock == nil || !got.selectionDock.selected["metrics"] {
		t.Fatalf("Space did not select metrics: dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		t.Fatalf("Space submitted single selection: %#v", result)
	default:
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock != nil {
		t.Fatal("Enter did not close dock")
	}
	select {
	case r := <-done:
		want := []selecttool.SelectedOption{{ID: "metrics", Label: "Metrics"}}
		if !reflect.DeepEqual(r.SelectedOptions, want) {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked")
	}
}

func TestSelectionDockSingleInitialPresetEnterSubmits(t *testing.T) {
	r := selectionRequest("", selecttool.ModeSingle)
	r.InitialSelectedIDs = []string{"metrics"}
	m, done := selectionKeyTestModel(t, r)
	if !m.selectionDock.selected["metrics"] {
		t.Fatal("initial preset is not selected")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(appModel)
	if got.selectionDock != nil {
		t.Fatal("Enter did not submit initial preset")
	}
	select {
	case result := <-done:
		want := []selecttool.SelectedOption{{ID: "metrics", Label: "Metrics"}}
		if !reflect.DeepEqual(result.SelectedOptions, want) {
			t.Fatalf("result=%#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("initial preset submission blocked")
	}
}

func TestSelectionDockSingleEnterWithoutSelectionValidates(t *testing.T) {
	m, done := selectionKeyTestModel(t, selectionRequest("", selecttool.ModeSingle))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(appModel)
	if got.selectionDock == nil || got.selectionDock.errorText != "Select at least 1 option." {
		t.Fatalf("dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		t.Fatalf("unselected Enter submitted: %#v", result)
	default:
	}
}
func TestSelectionDockConsumesCtrlCAsCancellation(t *testing.T) {
	b := selecttool.NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan selecttool.Result, 1)
	go func() { r, _ := b.Ask(ctx, selectionRequest("", selecttool.ModeMultiple)); done <- r }()
	e, _ := b.NextEvent(ctx)
	m := newModel(ctx, &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionBroker = b
	m.selectionDock = newSelectionDock(e.Request)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := next.(appModel)
	if !got.lastCtrlCAt.IsZero() {
		t.Fatal("global ctrl-c used")
	}
	r := <-done
	if !r.Cancelled || r.SelectedOptions == nil {
		t.Fatalf("%#v", r)
	}
}
func TestRenderSelectionDock(t *testing.T) {
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	plain := ansi.Strip(m.renderSelectionDock(60, 14))
	for _, want := range []string{
		"QUESTION  MULTIPLE",
		"Choose signals",
		"3 answers",
		"Custom option",
		"Chat about this",
		"space toggle",
		"enter submit",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "more") {
		t.Fatalf("ambiguous scroll text in %q", plain)
	}
}

func TestSelectionDockOptionsOnlyHidesGenericActions(t *testing.T) {
	r := selectionRequest("permission", selecttool.ModeSingle)
	r.Options = r.Options[:2]
	r.OptionsOnly = true
	r.MinSelect, r.MaxSelect = 1, 1
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	m.selectionDock.end()
	plain := ansi.Strip(m.renderSelectionDock(60, 12))
	if strings.Contains(plain, "Custom option") || strings.Contains(plain, "Chat about this") {
		t.Fatalf("options-only dock exposed generic actions: %q", plain)
	}
	if m.selectionDock.focus.kind != selectionFocusAnswer || m.selectionDock.focus.answerIndex != 1 {
		t.Fatalf("end focus = %#v, want final fixed option", m.selectionDock.focus)
	}
}
func TestRenderSelectionDockBatchProgress(t *testing.T) {
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	r := selectionRequest("x", selecttool.ModeSingle)
	r.Questions = []selecttool.Question{
		{Prompt: "First", Mode: selecttool.ModeSingle, Options: r.Options, MinSelect: 0, MaxSelect: 3},
		{Prompt: "Second", Mode: selecttool.ModeSingle, Options: r.Options, MinSelect: 0, MaxSelect: 3},
		{Prompt: "Third", Mode: selecttool.ModeSingle, Options: r.Options, MinSelect: 0, MaxSelect: 3},
	}
	m.selectionDock = newSelectionDock(r)
	m.selectionDock.questionIndex = 1
	m.selectionDock.loadQuestionState(1)
	plain := ansi.Strip(m.renderSelectionDock(60, 14))
	if !strings.Contains(plain, "QUESTION 2/3") {
		t.Fatalf("missing batch progress in %q", plain)
	}
	m.selectionDock.questionIndex = 0
	m.selectionDock.loadQuestionState(0)
	plain = ansi.Strip(m.renderSelectionDock(60, 14))
	if strings.Contains(plain, "QUESTION 0/") {
		t.Fatalf("non-batch request rendered progress: %q", plain)
	}
}

func TestSelectionDockBatchPagesPreserveIndependentSelectionsAndReview(t *testing.T) {
	r := selecttool.Request{ID: "batch", Questions: []selecttool.Question{
		{Prompt: "First", Mode: selecttool.ModeMultiple, Options: []selecttool.Option{{ID: "a", Label: "A"}}, MinSelect: 0, MaxSelect: 1},
		{Prompt: "Second", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "b", Label: "B"}}, MinSelect: 1, MaxSelect: 1},
	}}
	d := newSelectionDock(r)
	if len(d.questions) != 2 || d.questionIndex != 0 || d.review {
		t.Fatalf("initial page state=%#v", d)
	}
	d.activateFocused()
	if !d.selected["a"] {
		t.Fatal("first question selection was not recorded")
	}
	d.moveQuestion(1)
	if d.questionIndex != 1 || d.selected["a"] {
		t.Fatalf("question state leaked across pages: %#v", d)
	}
	d.activateFocused()
	if !d.selected["b"] {
		t.Fatal("second question selection was not recorded")
	}
	d.moveQuestion(-1)
	if !d.selected["a"] || d.selected["b"] {
		t.Fatalf("page selections were not restored: %#v", d)
	}
	d.enterReview()
	if !d.review || d.reviewFocus != 0 {
		t.Fatalf("review state=%#v", d)
	}
	results := d.reviewResult().Results
	if len(results) != 2 || len(results[0].SelectedOptions) != 1 || results[0].SelectedOptions[0].ID != "a" {
		t.Fatalf("review results=%#v", results)
	}
}

func TestSelectionDockReviewNavigationAndAtomicCancel(t *testing.T) {
	r := selecttool.Request{ID: "batch", Questions: []selecttool.Question{
		{Prompt: "First", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "a", Label: "A"}}, MinSelect: 1, MaxSelect: 1},
		{Prompt: "Second", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "b", Label: "B"}}, MinSelect: 1, MaxSelect: 1},
	}}
	d := newSelectionDock(r)
	d.enterReview()
	if d.reviewFocus != 0 {
		t.Fatal("review did not default to submit")
	}
	d.reviewFocus = 1
	cancelled := d.reviewCancel()
	if !cancelled.Cancelled || len(cancelled.Results) != 2 {
		t.Fatalf("cancel result=%#v", cancelled)
	}
	for i, result := range cancelled.Results {
		if !result.Cancelled || result.SelectedOptions == nil {
			t.Fatalf("cancelled result[%d]=%#v", i, result)
		}
	}
	d.leaveReview()
	if d.review || d.questionIndex != 1 {
		t.Fatalf("review back state=%#v", d)
	}
}

func TestCurrentLayoutUsesSelectionDock(t *testing.T) {
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.ready = true
	m.width = 80
	m.height = 24
	m.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	m.relayout()
	l := m.currentLayout()
	if l.inputHeight <= inputMinVisibleLines || l.transcriptHeight < 1 {
		t.Fatalf("layout=%#v", l)
	}
}

func TestSelectionDockTinyTerminalDoesNotOverflow(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {40, 10}, {20, 6}, {8, 3}} {
		m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
		m.ready = true
		m.width, m.height = size.width, size.height
		m.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
		m.relayout()
		plain := ansi.Strip(m.View())
		lines := strings.Split(plain, "\n")
		if len(lines) != size.height {
			t.Fatalf("%dx%d rendered %d lines", size.width, size.height, len(lines))
		}
		for i, line := range lines {
			if got := terminalCellWidth(line); got != size.width {
				t.Fatalf("%dx%d line %d width = %d: %q", size.width, size.height, i, got, line)
			}
		}
	}
}

func TestSelectionDockSingleCustomReplacesInitialPresetAndRequiresSubmit(t *testing.T) {
	r := selectionRequest("", selecttool.ModeSingle)
	r.MinSelect = 1
	r.MaxSelect = 1
	r.InitialSelectedIDs = []string{"logs"}
	m, done := selectionKeyTestModel(t, r)
	m.selectionDock.focus = selectionFocus{kind: selectionFocusCustom}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := next.(appModel)
	if got.selectionDock == nil || !got.selectionDock.editingCustom {
		t.Fatalf("Space did not activate custom editing: dock=%#v", got.selectionDock)
	}
	got.selectionDock.customInput.SetValue("Replacement")
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock == nil || got.selectionDock.editingCustom || got.selectionDock.selected["logs"] || got.selectionDock.customLabel != "Replacement" {
		t.Fatalf("custom confirmation state=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		t.Fatalf("custom confirmation submitted single selection: %#v", result)
	default:
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock != nil {
		t.Fatalf("second Enter did not submit custom: dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		want := []selecttool.SelectedOption{{ID: selecttool.CustomOptionID, Label: "Replacement"}}
		if !reflect.DeepEqual(result.SelectedOptions, want) {
			t.Fatalf("result=%#v, want %#v", result.SelectedOptions, want)
		}
	case <-time.After(time.Second):
		t.Fatal("custom submission blocked")
	}
}

func TestSelectionDockMultipleEditsExistingCustomWhenMaxIsFull(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.MaxSelect = 2
	d := newSelectionDock(r)
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("Existing custom")
	d.confirmCustom()
	if d.selectedCount() != d.request.MaxSelect {
		t.Fatalf("selectedCount=%d, want max %d", d.selectedCount(), d.request.MaxSelect)
	}

	d.activateFocused()
	if !d.editingCustom || d.customInput.Value() != "Existing custom" {
		t.Fatalf("existing custom could not be reopened at max: dock=%#v", d)
	}
	d.customInput.SetValue("Edited custom")
	if result, complete := d.confirmCustom(); complete || len(result.SelectedOptions) != 0 {
		t.Fatalf("unexpected edit completion: result=%#v complete=%v", result, complete)
	}
	result, ok := d.submit()
	if !ok {
		t.Fatalf("submit failed: dock=%#v", d)
	}
	customCount := 0
	for _, option := range result.SelectedOptions {
		if option.ID == selecttool.CustomOptionID {
			customCount++
			if option.Label != "Edited custom" {
				t.Fatalf("custom label=%q", option.Label)
			}
		}
	}
	if customCount != 1 {
		t.Fatalf("custom option count=%d in result %#v", customCount, result.SelectedOptions)
	}
}

func TestSelectionDockSingleCustomConfirmationSelectsWithoutSubmitting(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeSingle))
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	if result, complete := d.activateFocused(); complete || len(result.SelectedOptions) != 0 || !d.editingCustom {
		t.Fatalf("activation result=%#v complete=%v dock=%#v", result, complete, d)
	}
	d.customInput.SetValue("  Custom answer  ")
	result, complete := d.confirmCustom()
	if complete || len(result.SelectedOptions) != 0 || d.editingCustom || d.customLabel != "Custom answer" || len(d.selected) != 0 {
		t.Fatalf("result=%#v complete=%v dock=%#v", result, complete, d)
	}
	result, complete = d.submit()
	want := []selecttool.SelectedOption{{ID: selecttool.CustomOptionID, Label: "Custom answer"}}
	if !complete || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v complete=%v", result, complete)
	}
}

func TestSelectionDockSinglePresetSpaceClearsCustom(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeSingle))
	d.customLabel = "Custom answer"
	d.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: 1}
	if result, complete := d.activateFocused(); complete || len(result.SelectedOptions) != 0 {
		t.Fatalf("activation result=%#v complete=%v", result, complete)
	}
	if d.customLabel != "" || !d.selected["metrics"] || len(d.selected) != 1 {
		t.Fatalf("dock=%#v", d)
	}
}

func TestSelectionDockMultipleCustomOptionAddsAndEditsOneAnswer(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("First custom")
	if result, complete := d.confirmCustom(); complete || len(result.SelectedOptions) != 0 {
		t.Fatalf("unexpected completion: %#v %v", result, complete)
	}
	if d.customLabel != "First custom" || d.selectedCount() != 2 {
		t.Fatalf("dock=%#v", d)
	}
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	if d.customInput.Value() != "First custom" {
		t.Fatalf("prefill=%q", d.customInput.Value())
	}
	d.customInput.SetValue("Edited custom")
	d.confirmCustom()
	result, ok := d.submit()
	want := []selecttool.SelectedOption{
		{ID: "logs", Label: "Logs"},
		{ID: selecttool.CustomOptionID, Label: "Edited custom"},
	}
	if !ok || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", result, ok)
	}
}

func TestSelectionDockCustomOptionValidatesEmptyAndMax(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.request.MaxSelect = 1
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("Extra")
	if _, complete := d.confirmCustom(); complete || d.errorText != "You can select at most 1 option." {
		t.Fatalf("dock=%#v", d)
	}

	d = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("   ")
	if _, complete := d.confirmCustom(); complete || d.errorText != "Custom option cannot be empty." {
		t.Fatalf("dock=%#v", d)
	}
}

func TestSelectionDockCustomSpaceActivatesAndEnterOnlySubmitsSelection(t *testing.T) {
	r := selectionRequest("", selecttool.ModeMultiple)
	r.MinSelect = 1
	m, done := selectionKeyTestModel(t, r)
	m.selectionDock.focus = selectionFocus{kind: selectionFocusCustom}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(appModel)
	if got.selectionDock == nil || got.selectionDock.editingCustom {
		t.Fatalf("Enter activated custom editing or closed dock: dock=%#v", got.selectionDock)
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeySpace})
	got = next.(appModel)
	if got.selectionDock == nil || !got.selectionDock.editingCustom {
		t.Fatalf("Space did not activate custom editing: dock=%#v", got.selectionDock)
	}
	select {
	case result := <-done:
		t.Fatalf("custom activation completed selection: %#v", result)
	default:
	}
}

func TestSelectionDockCustomEditEscPreservesSelectAndCtrlCCancels(t *testing.T) {
	t.Run("esc", func(t *testing.T) {
		m, done := selectionKeyTestModel(t, selectionRequest("", selecttool.ModeMultiple))
		m.selectionDock.focus = selectionFocus{kind: selectionFocusCustom}
		m.selectionDock.activateFocused()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got := next.(appModel)
		if got.selectionDock == nil || got.selectionDock.editingCustom {
			t.Fatalf("Esc did not leave custom editing while preserving Select: dock=%#v", got.selectionDock)
		}
		select {
		case result := <-done:
			t.Fatalf("Esc cancelled Select: %#v", result)
		default:
		}
	})

	t.Run("ctrl+c", func(t *testing.T) {
		m, done := selectionKeyTestModel(t, selectionRequest("", selecttool.ModeMultiple))
		m.selectionDock.focus = selectionFocus{kind: selectionFocusCustom}
		m.selectionDock.activateFocused()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		got := next.(appModel)
		if got.selectionDock != nil {
			t.Fatalf("Ctrl+C preserved Select: dock=%#v", got.selectionDock)
		}
		select {
		case result := <-done:
			if !result.Cancelled {
				t.Fatalf("Ctrl+C result=%#v", result)
			}
		case <-time.After(time.Second):
			t.Fatal("Ctrl+C cancellation blocked")
		}
	})
}

func TestSelectionDockLongListShowsExactRangeAndRemainingCounts(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.Options = nil
	for i := 0; i < 12; i++ {
		r.Options = append(r.Options, selecttool.Option{ID: fmt.Sprintf("id-%d", i), Label: fmt.Sprintf("Option %d", i)})
	}
	r.MaxSelect = len(r.Options)
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	first := ansi.Strip(m.renderSelectionDock(48, 14))
	if !strings.Contains(first, "12 answers") || !strings.Contains(first, "showing 1-") || !strings.Contains(first, "answers below") {
		t.Fatalf("first page=%q", first)
	}
	if !strings.Contains(first, "Custom option") || !strings.Contains(first, "Chat about this") {
		t.Fatalf("fixed actions missing=%q", first)
	}
	m.selectionDock.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: 11}
	last := ansi.Strip(m.renderSelectionDock(48, 14))
	if !strings.Contains(last, "answers above") || !strings.Contains(last, "Option 11") {
		t.Fatalf("last page=%q", last)
	}
	if strings.Contains(last, "more") {
		t.Fatalf("ambiguous more indicator remains=%q", last)
	}
}

func TestSelectionDockSingleSelectedAndFocusRemainDistinct(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeSingle)
	r.InitialSelectedIDs = []string{"logs"}
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	m.selectionDock.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: 1}
	plain := ansi.Strip(m.renderSelectionDock(60, 14))
	if !strings.Contains(plain, "  [x] Logs") || !strings.Contains(plain, "› [ ] Metrics") {
		t.Fatalf("selected and focus are not distinct: %q", plain)
	}
}

func TestSelectionDockCustomSelectedAndEditHints(t *testing.T) {
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	m.selectionDock.customLabel = "Saved answer"
	m.selectionDock.focus = selectionFocus{kind: selectionFocusCustom}
	plain := ansi.Strip(m.renderSelectionDock(60, 14))
	if !strings.Contains(plain, "› [x] Custom option  Saved answer") || !strings.Contains(plain, "Chat about this") {
		t.Fatalf("saved custom state missing: %q", plain)
	}
	m.selectionDock.beginCustomEdit()
	editing := ansi.Strip(m.renderSelectionDock(60, 14))
	if !strings.Contains(editing, "enter save") || strings.Contains(editing, "enter submit") || !strings.Contains(editing, "Chat about this") {
		t.Fatalf("custom edit hints/actions wrong: %q", editing)
	}
}

func TestCurrentLayoutAllowsTallerSelectionDockWithoutChangingInputLimit(t *testing.T) {
	selection := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	selection.ready = true
	selection.width = 100
	selection.height = 32
	selection.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	selectionLayout := selection.currentLayout()
	if selectionLayout.inputHeight <= inputMaxVisibleLines {
		t.Fatalf("selection input height=%d, want above normal max=%d", selectionLayout.inputHeight, inputMaxVisibleLines)
	}

	normal := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	normal.ready = true
	normal.width = 100
	normal.height = 32
	normal.input.SetHeight(inputMaxVisibleLines + 8)
	normalLayout := normal.currentLayout()
	if normalLayout.inputHeight != inputMaxVisibleLines {
		t.Fatalf("normal input height=%d, want %d", normalLayout.inputHeight, inputMaxVisibleLines)
	}
}

func TestSelectionDockChatSpaceDoesNotCompleteButEnterCancels(t *testing.T) {
	m, done := selectionKeyTestModel(t, selectionRequest("", selecttool.ModeMultiple))
	m.selectionDock.focus = selectionFocus{kind: selectionFocusChat}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := next.(appModel)
	if got.selectionDock == nil {
		t.Fatal("Space completed Chat cancellation")
	}
	select {
	case result := <-done:
		t.Fatalf("Space completed result: %#v", result)
	default:
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock != nil {
		t.Fatal("Enter did not activate Chat cancellation")
	}
	select {
	case result := <-done:
		if !result.Cancelled {
			t.Fatalf("result=%#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter cancellation blocked")
	}
}

func TestSelectionDockLongPromptAndDescriptionPreserveStructureAndExactRange(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.Prompt = strings.Repeat("long prompt words ", 30)
	r.Options[0].Description = strings.Repeat("long description words ", 30)
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	plain := ansi.Strip(m.renderSelectionDock(42, 16))
	for _, want := range []string{"QUESTION  MULTIPLE", "…", "3 answers  showing 1-", "› [ ] Logs", "↑ 0 answers above", "answers below", "Custom option", "Chat about this", "enter submit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in %q", want, plain)
		}
	}
	if !strings.Contains(plain, "showing 1-1") {
		t.Fatalf("rendered answer range is not exact: %q", plain)
	}
	if !strings.Contains(plain, "› [ ] Logs") || !strings.Contains(plain, "long description words") {
		t.Fatalf("focused option or wrapped description missing: %q", plain)
	}
}

func TestSelectionDockVisibleRangeRequiresCompleteOption(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	if start, end := d.visibleRange([]int{3, 1}, 0); start != 0 || end != 0 {
		t.Fatalf("zero budget range=%d,%d", start, end)
	}
	if start, end := d.visibleRange([]int{3, 1}, 2); start != 0 || end != 0 {
		t.Fatalf("partial option counted in range=%d,%d", start, end)
	}
	if start, end := d.visibleRange([]int{2, 1}, 2); start != 0 || end != 1 {
		t.Fatalf("complete option range=%d,%d", start, end)
	}
}

func TestSelectionDockMultilinePromptKeepsAnswersAndActionsVisible(t *testing.T) {
	r := selecttool.Request{
		ID:     "review",
		Prompt: "设计第 4–5 节：错误处理与测试\n\n错误处理：模型发现失败不阻断启动。\n\n测试分层：\n1. discovery\n2. catalog\n3. cache\n\n是否批准完整设计？",
		Mode:   selecttool.ModeSingle,
		Options: []selecttool.Option{
			{ID: "approve", Label: "批准设计", Description: "开始编写规格文档"},
			{ID: "revise", Label: "需要修改", Description: "暂停并调整设计"},
		},
		InitialSelectedIDs: []string{"approve"},
		MinSelect:          1,
		MaxSelect:          1,
	}
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.ready = true
	m.width = 84
	m.height = 20
	m.selectionDock = newSelectionDock(r)
	m.relayout()
	plain := ansi.Strip(m.View())
	for _, want := range []string{"批准设计", "需要修改", "Custom option", "Chat about this", "enter submit"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("multiline prompt hid %q in %q", want, plain)
		}
	}
}
