package bubble

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type wheelFlushTestMsg struct{}

type wheelEventReader struct {
	source *strings.Reader
}

func (r *wheelEventReader) Read(p []byte) (int, error) {
	if len(p) > len("\x1b[<64;11;4M") {
		p = p[:len("\x1b[<64;11;4M")]
	}
	return r.source.Read(p)
}

type pacedWheelEventReader struct {
	source     *strings.Reader
	reads      int
	pauseAfter int
}

func (r *pacedWheelEventReader) Read(p []byte) (int, error) {
	if len(p) > len("\x1b[<64;11;4M") {
		p = p[:len("\x1b[<64;11;4M")]
	}
	n, err := r.source.Read(p)
	if n > 0 {
		r.reads++
		if r.reads == r.pauseAfter {
			time.Sleep(2 * transcriptWheelFlushInterval)
		}
	}
	return n, err
}

type wheelProgramTrace struct {
	views              int
	batchUpdates       int
	rawWheelUpdates    int
	keyUpdates         int
	otherMouseUpdates  int
	otherUpdates       int
	viewsBeforeReverse int
	firstDirection     int
	reverseSeen        bool
	reverseBefore      int
	reverseAfter       int
}

type wheelProgramModel struct {
	app   appModel
	trace *wheelProgramTrace
}

func (m wheelProgramModel) Init() tea.Cmd { return nil }

func (m wheelProgramModel) transcriptWheelAppModel() appModel { return m.app }

func (m wheelProgramModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "q" {
		return m, tea.Quit
	}
	reverse := false
	switch msg := msg.(type) {
	case transcriptWheelBatchMsg:
		direction := 1
		if msg.lines < 0 {
			direction = -1
		}
		if m.trace.batchUpdates == 0 {
			m.trace.firstDirection = direction
		} else if direction != m.trace.firstDirection && !m.trace.reverseSeen {
			m.trace.viewsBeforeReverse = m.trace.views
			m.trace.reverseBefore = m.app.viewport.YOffset
			reverse = true
		}
		m.trace.batchUpdates++
	case tea.MouseMsg:
		if isMouseWheel(msg) {
			m.trace.rawWheelUpdates++
		} else {
			m.trace.otherMouseUpdates++
		}
	case tea.KeyMsg:
		m.trace.keyUpdates++
	default:
		m.trace.otherUpdates++
	}
	next, cmd := m.app.Update(msg)
	m.app = next.(appModel)
	if reverse {
		m.trace.reverseSeen = true
		m.trace.reverseAfter = m.app.viewport.YOffset
	}
	return m, cmd
}

func (m wheelProgramModel) View() string {
	m.trace.views++
	return m.app.View()
}

func TestProgramEventFilterLetsWheelReversalPreemptPendingBurst(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()

	scheduled := 0
	filter := newProgramEventFilter(func(generation uint64) tea.Cmd {
		scheduled++
		return func() tea.Msg { return transcriptWheelFlushMsg{generation: generation} }
	})
	up := tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseWheelUp,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	down := up
	down.Type = tea.MouseWheelDown
	down.Button = tea.MouseButtonWheelDown

	first, ok := filter.Filter(model, up).(transcriptWheelBatchMsg)
	if !ok {
		t.Fatalf("first wheel = %T, want transcriptWheelBatchMsg", first)
	}
	if first.lines != -1 || first.flush == nil {
		t.Fatalf("first wheel batch = lines %d flush %v, want -1/non-nil", first.lines, first.flush != nil)
	}
	for range 3_000 {
		if got := filter.Filter(model, up); got != nil {
			t.Fatalf("same-direction queued wheel = %T, want filtered", got)
		}
	}

	reversed, ok := filter.Filter(model, down).(transcriptWheelBatchMsg)
	if !ok {
		t.Fatalf("reverse wheel = %T, want transcriptWheelBatchMsg", reversed)
	}
	if reversed.lines != 1 || reversed.flush != nil {
		t.Fatalf("reverse wheel batch = lines %d flush %v, want 1/nil", reversed.lines, reversed.flush != nil)
	}
	if got := filter.Filter(model, transcriptWheelFlushMsg{generation: first.generation}); got != nil {
		t.Fatalf("flush after reversal = %T, want old-direction pending discarded", got)
	}
	if scheduled != 1 {
		t.Fatalf("scheduled flushes = %d, want 1", scheduled)
	}
}

func TestWheelBatchScrollsViewportOnceAndReturnsFlush(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	lines := make([]string, 240)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %03d", index)
	}
	model.viewport.SetLines(lines)
	model.viewport.GotoBottom()
	model.newMessageNoticeCount = 1
	beforeOffset := model.viewport.YOffset
	refreshes := model.transcriptRefreshCount

	next, cmd := model.Update(transcriptWheelBatchMsg{
		lines: -25,
		x:     10,
		y:     3,
		flush: func() tea.Msg { return wheelFlushTestMsg{} },
	})
	updated := next.(appModel)
	if got := beforeOffset - updated.viewport.YOffset; got != 25 {
		t.Fatalf("batched scroll distance = %d, want 25", got)
	}
	if !updated.transcriptKeyScrollActive {
		t.Fatal("batched wheel did not activate transcript key scrolling")
	}
	if updated.newMessageNoticeCount != 1 {
		t.Fatalf("notice count = %d, want preserved away from bottom", updated.newMessageNoticeCount)
	}
	if got := updated.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("batched scroll refreshed transcript %d times, want 0", got)
	}
	if cmd == nil {
		t.Fatal("batched wheel did not return its flush command")
	}
	if _, ok := cmd().(wheelFlushTestMsg); !ok {
		t.Fatalf("flush command returned unexpected message")
	}
}

func TestCanceledDoneDefersFinalTranscriptRefreshUntilWheelIdle(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	for index := range 120 {
		model.appendTranscriptEntry(transcriptEntry{
			kind: entrySystem,
			body: fmt.Sprintf("history line %03d", index),
		})
	}
	model.refreshViewport()
	model.viewport.SetYOffset(model.viewport.YOffset / 2)

	next, _ := model.Update(assistantDeltaMsg("canceled **tail"))
	model = next.(appModel)
	model.modelCancelRequested = true
	refreshes := model.transcriptRefreshCount

	next, doneCmd := model.Update(doneMsg{})
	model = next.(appModel)
	if got := model.transcriptRefreshCount; got != refreshes {
		t.Fatalf("canceled done refreshed transcript synchronously: %d -> %d", refreshes, got)
	}
	if !model.transcriptRefreshDeferred || doneCmd == nil {
		t.Fatalf("canceled done deferred=%v cmd=%v, want true/non-nil", model.transcriptRefreshDeferred, doneCmd != nil)
	}
	firstGeneration := model.transcriptRefreshDeferredGeneration

	next, wheelCmd := model.Update(transcriptWheelBatchMsg{lines: -3, x: 10, y: 3})
	model = next.(appModel)
	if wheelCmd == nil {
		t.Fatal("wheel did not renew deferred transcript refresh")
	}
	if model.transcriptRefreshDeferredGeneration <= firstGeneration {
		t.Fatalf("wheel generation = %d, want newer than %d", model.transcriptRefreshDeferredGeneration, firstGeneration)
	}
	wheelGeneration := model.transcriptRefreshDeferredGeneration
	wheelOffset := model.viewport.YOffset

	next, _ = model.Update(transcriptDeferredRefreshMsg{generation: firstGeneration})
	model = next.(appModel)
	if got := model.transcriptRefreshCount; got != refreshes {
		t.Fatalf("stale deferred tick refreshed transcript: %d -> %d", refreshes, got)
	}
	if !model.transcriptRefreshDeferred {
		t.Fatal("stale deferred tick cleared pending refresh")
	}

	next, _ = model.Update(transcriptDeferredRefreshMsg{generation: wheelGeneration})
	model = next.(appModel)
	if got := model.transcriptRefreshCount; got != refreshes+1 {
		t.Fatalf("wheel-idle tick refresh count = %d, want %d", got, refreshes+1)
	}
	if model.transcriptRefreshDeferred {
		t.Fatal("wheel-idle tick left refresh deferred")
	}
	if model.viewport.YOffset != wheelOffset {
		t.Fatalf("wheel-idle refresh changed manual offset: %d -> %d", wheelOffset, model.viewport.YOffset)
	}
	if len(model.transcript) == 0 || !strings.Contains(model.transcript[len(model.transcript)-1].body, "canceled **tail") {
		t.Fatalf("canceled tail was not preserved: %#v", model.transcript)
	}
}

func TestCancelRequestSuppressesPendingStreamingRefreshBeforeDone(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.transcriptRefreshPending = true
	model.transcriptRefreshPendingAt = time.Now().Add(-2 * transcriptStreamingRefreshInterval)
	refreshes := model.transcriptRefreshCount

	model.cancelModelWork()
	if !model.transcriptRefreshDeferred {
		t.Fatal("cancel request did not defer the pending streaming refresh")
	}
	next, _ := model.Update(cursorFrameMsg(time.Now()))
	model = next.(appModel)
	if got := model.transcriptRefreshCount; got != refreshes {
		t.Fatalf("pending frame refreshed after cancel request: %d -> %d", refreshes, got)
	}
}

func TestSuccessfulDoneStillRefreshesTranscriptImmediately(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(assistantDeltaMsg("successful tail"))
	model = next.(appModel)
	refreshes := model.transcriptRefreshCount

	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	if got := model.transcriptRefreshCount; got != refreshes+1 {
		t.Fatalf("successful done refresh count = %d, want %d", got, refreshes+1)
	}
	if model.transcriptRefreshDeferred {
		t.Fatal("successful done deferred transcript refresh")
	}
}

func TestProgramEventFilterFlushesQuietWheelTail(t *testing.T) {
	model := wheelFilterTestModel()
	filter := newProgramEventFilter(func(generation uint64) tea.Cmd {
		return func() tea.Msg { return transcriptWheelFlushMsg{generation: generation} }
	})
	up := wheelFilterMouse(tea.MouseButtonWheelUp)
	first := filter.Filter(model, up).(transcriptWheelBatchMsg)
	for range 9 {
		if got := filter.Filter(model, up); got != nil {
			t.Fatalf("queued wheel = %T, want filtered", got)
		}
	}

	flushed, ok := filter.Filter(model, first.flush()).(transcriptWheelBatchMsg)
	if !ok {
		t.Fatalf("quiet-tail flush = %T, want transcriptWheelBatchMsg", flushed)
	}
	if flushed.lines != -9 || flushed.flush == nil {
		t.Fatalf("quiet-tail batch = lines %d flush %v, want -9/non-nil", flushed.lines, flushed.flush != nil)
	}
	if got := filter.Filter(model, flushed.flush()); got != nil {
		t.Fatalf("empty follow-up flush = %T, want nil", got)
	}
}

func TestProgramEventFilterEmptyFlushClosesBurstWithoutSkippingGeneration(t *testing.T) {
	model := wheelFilterTestModel()
	filter := newProgramEventFilter(func(generation uint64) tea.Cmd {
		return func() tea.Msg { return transcriptWheelFlushMsg{generation: generation} }
	})
	up := wheelFilterMouse(tea.MouseButtonWheelUp)

	first := filter.Filter(model, up).(transcriptWheelBatchMsg)
	if got := filter.Filter(model, first.flush()); got != nil {
		t.Fatalf("empty flush = %T, want nil", got)
	}
	second := filter.Filter(model, up).(transcriptWheelBatchMsg)
	if second.generation != first.generation+1 {
		t.Fatalf("next generation = %d, want %d", second.generation, first.generation+1)
	}
}

func TestProgramEventFilterIgnoresStaleFlushWithoutDroppingCurrentTail(t *testing.T) {
	model := wheelFilterTestModel()
	filter := newProgramEventFilter(func(generation uint64) tea.Cmd {
		return func() tea.Msg { return transcriptWheelFlushMsg{generation: generation} }
	})
	up := wheelFilterMouse(tea.MouseButtonWheelUp)
	down := wheelFilterMouse(tea.MouseButtonWheelDown)

	first := filter.Filter(model, up).(transcriptWheelBatchMsg)
	filter.Filter(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	current := filter.Filter(model, down).(transcriptWheelBatchMsg)
	filter.Filter(model, down)

	if got := filter.Filter(model, first.flush()); got != nil {
		t.Fatalf("stale flush = %T, want nil", got)
	}
	flushed, ok := filter.Filter(model, current.flush()).(transcriptWheelBatchMsg)
	if !ok {
		t.Fatalf("current flush = %T, want transcriptWheelBatchMsg", flushed)
	}
	if flushed.lines != 1 {
		t.Fatalf("current pending tail = %d, want 1", flushed.lines)
	}
}

func TestProgramEventFilterPassesThroughUnsupportedWheelEvents(t *testing.T) {
	model := wheelFilterTestModel()
	filter := newProgramEventFilter(nil)
	shifted := wheelFilterMouse(tea.MouseButtonWheelUp)
	shifted.Shift = true
	horizontal := wheelFilterMouse(tea.MouseButtonWheelLeft)
	outside := wheelFilterMouse(tea.MouseButtonWheelDown)
	outside.Y = model.height - 1
	if model.isTranscriptViewportMouse(outside) {
		t.Fatalf("outside test point (%d,%d) unexpectedly lies in transcript", outside.X, outside.Y)
	}

	for name, msg := range map[string]tea.MouseMsg{
		"shifted":    shifted,
		"horizontal": horizontal,
		"outside":    outside,
	} {
		t.Run(name, func(t *testing.T) {
			filtered := filter.Filter(model, msg)
			got, ok := filtered.(tea.MouseMsg)
			if !ok {
				t.Fatalf("filtered message = %T, want tea.MouseMsg", filtered)
			}
			if got != msg {
				t.Fatalf("filtered message = %#v, want %#v", got, msg)
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		disabled := model
		disabled.viewport.MouseWheelEnabled = false
		msg := wheelFilterMouse(tea.MouseButtonWheelUp)
		filtered := filter.Filter(disabled, msg)
		got, ok := filtered.(tea.MouseMsg)
		if !ok {
			t.Fatalf("filtered message = %T, want tea.MouseMsg", filtered)
		}
		if got != msg {
			t.Fatalf("filtered message = %#v, want %#v", got, msg)
		}
	})
}

func TestProgramEventFilterCancelsPendingWheelForNewUserInputOnly(t *testing.T) {
	model := wheelFilterTestModel()
	newFilter := func() *programEventFilter {
		return newProgramEventFilter(func(generation uint64) tea.Cmd {
			return func() tea.Msg { return transcriptWheelFlushMsg{generation: generation} }
		})
	}
	up := wheelFilterMouse(tea.MouseButtonWheelUp)

	filter := newFilter()
	first := filter.Filter(model, up).(transcriptWheelBatchMsg)
	filter.Filter(model, up)
	internal := assistantDeltaMsg("delta")
	if got := filter.Filter(model, internal); got != internal {
		t.Fatalf("internal message = %#v, want preserved", got)
	}
	if flushed := filter.Filter(model, first.flush()).(transcriptWheelBatchMsg); flushed.lines != -1 {
		t.Fatalf("pending after internal message = %d, want -1", flushed.lines)
	}

	filter = newFilter()
	first = filter.Filter(model, up).(transcriptWheelBatchMsg)
	filter.Filter(model, up)
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	if got, ok := filter.Filter(model, key).(tea.KeyMsg); !ok || got.String() != key.String() {
		t.Fatalf("key message = %#v, want preserved", got)
	}
	if got := filter.Filter(model, first.flush()); got != nil {
		t.Fatalf("flush after key = %T, want canceled", got)
	}

	filter = newFilter()
	first = filter.Filter(model, up).(transcriptWheelBatchMsg)
	filter.Filter(model, up)
	click := tea.MouseMsg{X: 10, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	if got, ok := filter.Filter(model, click).(tea.MouseMsg); !ok || got != click {
		t.Fatalf("click message = %#v, want preserved", got)
	}
	if got := filter.Filter(model, first.flush()); got != nil {
		t.Fatalf("flush after click = %T, want canceled", got)
	}
}

func TestBubbleTeaRawWheelBurstReachesReverseWithoutIntermediateViews(t *testing.T) {
	for _, burst := range []int{3_000, 10_000} {
		for _, forwardButton := range []tea.MouseButton{tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
			name := fmt.Sprintf("%d/button-%d", burst, forwardButton)
			t.Run(name, func(t *testing.T) {
				app := wheelFilterTestModel()
				lines := make([]string, 240)
				for index := range lines {
					lines[index] = fmt.Sprintf("line %03d", index)
				}
				app.viewport.SetLines(lines)
				app.viewport.SetYOffset(100)
				startOffset := app.viewport.YOffset
				trace := &wheelProgramTrace{}
				filter := newProgramEventFilter(nil)
				forward := wheelSGR(forwardButton)
				reverse := wheelSGR(oppositeWheelButton(forwardButton))
				input := strings.Repeat(forward, burst+1) + reverse + "q"
				program := tea.NewProgram(
					wheelProgramModel{app: app, trace: trace},
					tea.WithInput(&wheelEventReader{source: strings.NewReader(input)}),
					tea.WithoutRenderer(),
					tea.WithoutSignalHandler(),
					tea.WithFilter(filter.Filter),
				)
				final, err := program.Run()
				if err != nil {
					t.Fatalf("run raw wheel program: %v", err)
				}
				got := final.(wheelProgramModel)
				if trace.rawWheelUpdates != 0 {
					t.Fatalf("raw wheel updates = %d, want 0", trace.rawWheelUpdates)
				}
				if trace.batchUpdates != 2 {
					t.Fatalf("wheel batch updates = %d, want first+reverse only (raw=%d key=%d mouse=%d other=%d)", trace.batchUpdates, trace.rawWheelUpdates, trace.keyUpdates, trace.otherMouseUpdates, trace.otherUpdates)
				}
				if trace.viewsBeforeReverse != 2 {
					t.Fatalf("views before reverse = %d, want initial+first batch", trace.viewsBeforeReverse)
				}
				if got.app.viewport.YOffset != startOffset {
					t.Fatalf("final YOffset = %d, want restored %d after forward then reverse", got.app.viewport.YOffset, startOffset)
				}
			})
		}
	}
}

func TestBubbleTeaRawWheelBurstWithProductionSchedulerStaysBatched(t *testing.T) {
	if os.Getenv("PAW_WHEEL_TIMING_TEST") != "1" {
		t.Skip("set PAW_WHEEL_TIMING_TEST=1 to exercise the real 60fps scheduler")
	}
	const burst = 256
	for _, forwardButton := range []tea.MouseButton{tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		name := fmt.Sprintf("button-%d", forwardButton)
		t.Run(name, func(t *testing.T) {
			app := wheelFilterTestModel()
			lines := make([]string, 240)
			for index := range lines {
				lines[index] = fmt.Sprintf("line %03d", index)
			}
			app.viewport.SetLines(lines)
			app.viewport.SetYOffset(100)
			trace := &wheelProgramTrace{}
			filter := newProgramEventFilter(scheduleTranscriptWheelFlush)
			input := strings.Repeat(wheelSGR(forwardButton), burst+1) + wheelSGR(oppositeWheelButton(forwardButton)) + "q"
			program := tea.NewProgram(
				wheelProgramModel{app: app, trace: trace},
				tea.WithInput(&pacedWheelEventReader{source: strings.NewReader(input), pauseAfter: 32}),
				tea.WithoutRenderer(),
				tea.WithoutSignalHandler(),
				tea.WithFilter(filter.Filter),
			)
			if _, err := program.Run(); err != nil {
				t.Fatalf("run scheduled raw wheel program: %v", err)
			}
			if trace.rawWheelUpdates != 0 {
				t.Fatalf("raw wheel updates = %d, want 0", trace.rawWheelUpdates)
			}
			maxBatches := 16
			if trace.batchUpdates < 3 || trace.batchUpdates > maxBatches {
				t.Fatalf("scheduled batch updates = %d, want 3..%d for %d raw events", trace.batchUpdates, maxBatches, burst+2)
			}
			if !trace.reverseSeen {
				t.Fatal("reverse batch never reached Update")
			}
			if trace.firstDirection < 0 && trace.reverseAfter <= trace.reverseBefore {
				t.Fatalf("reverse down did not move viewport: %d -> %d", trace.reverseBefore, trace.reverseAfter)
			}
			if trace.firstDirection > 0 && trace.reverseAfter >= trace.reverseBefore {
				t.Fatalf("reverse up did not move viewport: %d -> %d", trace.reverseBefore, trace.reverseAfter)
			}
		})
	}
}

func TestWheelBatchReconcilesHoverOnlyAtFinalOffset(t *testing.T) {
	model := wheelFilterTestModel()
	entries := make([]transcriptEntry, 0, 32)
	for index := range 10 {
		entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("before %d", index)})
	}
	firstTool := len(entries)
	entries = append(entries, transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "first.txt", toolStatus: "ok", toolGroupPending: true})
	for index := range 10 {
		entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("between %d", index)})
	}
	secondTool := len(entries)
	entries = append(entries, transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "second.txt", toolStatus: "ok", toolGroupPending: true})
	for index := range 40 {
		entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("after %d", index)})
	}
	model.replaceTranscript(entries)
	model.refreshViewport()
	first, ok := model.transcriptInteraction.toolAt(firstTool)
	if !ok {
		t.Fatal("first tool interaction missing")
	}
	second, ok := model.transcriptInteraction.toolAt(secondTool)
	if !ok {
		t.Fatal("second tool interaction missing")
	}
	visibleRow := 2
	model.viewport.SetYOffset(first.patchStart - visibleRow)
	model.setToolHover(firstTool)
	renders := 0
	model.transcriptHoverPatchRenderSpy = &renders
	refreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0

	next, _ := model.Update(transcriptWheelBatchMsg{
		lines: second.patchStart - first.patchStart,
		x:     mainContentPadding + 1,
		y:     model.transcriptScreenTop() + visibleRow,
	})
	updated := next.(appModel)
	if updated.toolHoverIndex != secondTool {
		row, _ := updated.transcriptInteraction.rowAt(updated.viewport.YOffset + visibleRow)
		t.Fatalf("final hover index = %d, want %d (offset=%d first_start=%d second_start=%d row_tool=%d)", updated.toolHoverIndex, secondTool, updated.viewport.YOffset, first.patchStart, second.patchStart, row.toolIndex)
	}
	if renders != 1 {
		t.Fatalf("hover patch renders = %d, want one final-offset render", renders)
	}
	if got := updated.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("batched hover reconciliation refreshed transcript %d times, want 0", got)
	}
	if updated.transcriptRenderVisits != 0 {
		t.Fatalf("batched hover reconciliation rendered %d transcript entries, want 0", updated.transcriptRenderVisits)
	}
}

func TestWheelBatchToBottomClearsNoticeAndPreservesSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.newMessageNoticeCount = 2
	model.newMessageNoticeHovered = true
	model.selectionActive = true

	next, _ := model.Update(transcriptWheelBatchMsg{lines: 10_000, x: 10, y: 3})
	updated := next.(appModel)
	if !updated.viewport.AtBottom() {
		t.Fatalf("batched scroll YOffset = %d, want bottom", updated.viewport.YOffset)
	}
	if updated.newMessageNoticeCount != 0 || updated.newMessageNoticeHovered {
		t.Fatalf("notice after bottom batch = count %d hovered %v, want cleared", updated.newMessageNoticeCount, updated.newMessageNoticeHovered)
	}
	if !updated.selectionActive {
		t.Fatal("batched wheel cleared the existing selection")
	}
}

func wheelFilterTestModel() appModel {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	return model
}

func wheelFilterMouse(button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseEventType(button),
		Button: button,
		Action: tea.MouseActionPress,
	}
}

func wheelSGR(button tea.MouseButton) string {
	if button == tea.MouseButtonWheelUp {
		return "\x1b[<64;11;4M"
	}
	return "\x1b[<65;11;4M"
}

func oppositeWheelButton(button tea.MouseButton) tea.MouseButton {
	if button == tea.MouseButtonWheelUp {
		return tea.MouseButtonWheelDown
	}
	return tea.MouseButtonWheelUp
}
