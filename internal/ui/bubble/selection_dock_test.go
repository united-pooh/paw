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
func TestNewSelectionDockAndNavigation(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.InitialSelectedIDs = []string{"metrics"}
	d := newSelectionDock(r)
	if d.highlighted != 0 || !d.selected["metrics"] {
		t.Fatalf("dock=%#v", d)
	}
	d.move(-1)
	if d.highlighted != 0 {
		t.Fatal("above")
	}
	d.end()
	if d.highlighted != 2 {
		t.Fatal("end")
	}
	d.move(1)
	if d.highlighted != 2 {
		t.Fatal("wrapped")
	}
	d.home()
	if d.highlighted != 0 {
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
func TestSelectionDockStableOrder(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.selected["traces"] = true
	d.selected["logs"] = true
	r, ok := d.submit()
	want := []selecttool.SelectedOption{{ID: "logs", Label: "Logs"}, {ID: "traces", Label: "Traces"}}
	if !ok || !reflect.DeepEqual(r.SelectedOptions, want) {
		t.Fatalf("%#v", r)
	}
}
func TestSelectionDockSingleSubmit(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeSingle)
	r.MinSelect = 1
	r.MaxSelect = 1
	d := newSelectionDock(r)
	d.move(1)
	got, ok := d.submit()
	want := []selecttool.SelectedOption{{ID: "metrics", Label: "Metrics"}}
	if !ok || !reflect.DeepEqual(got.SelectedOptions, want) {
		t.Fatalf("%#v", got)
	}
}
func TestSelectionBrokerRequestAndKeys(t *testing.T) {
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
	if got.selectionDock.highlighted != 1 {
		t.Fatal("down")
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(appModel)
	if got.selectionDock != nil {
		t.Fatal("dock remains")
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
	plain := ansi.Strip(m.renderSelectionDock(60, 8))
	for _, want := range []string{"Select · multiple", "Choose signals", "Logs", "space toggle"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in %q", want, plain)
		}
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

func TestSelectionDockLongListShowsScrollIndicators(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.Options = nil
	for i := 0; i < 12; i++ {
		r.Options = append(r.Options, selecttool.Option{ID: fmt.Sprintf("id-%d", i), Label: fmt.Sprintf("Option %d", i)})
	}
	r.MaxSelect = len(r.Options)
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	first := ansi.Strip(m.renderSelectionDock(40, 8))
	if !strings.Contains(first, "↓ more") {
		t.Fatalf("missing lower indicator: %q", first)
	}
	m.selectionDock.end()
	last := ansi.Strip(m.renderSelectionDock(40, 8))
	if !strings.Contains(last, "↑ more") || !strings.Contains(last, "Option 11") {
		t.Fatalf("missing upper indicator or final option: %q", last)
	}
}
