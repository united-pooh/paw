package bubble

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestIdleMouseMotionFilterDoesNotAllocateWithLongHistory(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.transcript = make([]transcriptEntry, 250)
	for index := range model.transcript {
		model.transcript[index] = transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
			body:  fmt.Sprintf("stable history line %03d", index),
		}
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	var current tea.Model = model
	var message tea.Msg = tea.MouseMsg{
		X:      mainContentPadding,
		Y:      model.transcriptScreenTop(),
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	}
	var filtered tea.Msg
	allocs := testing.AllocsPerRun(100, func() {
		filtered = filterIdleMouseMotion(current, message)
	})

	if filtered != nil {
		t.Fatalf("stable passive motion was not filtered: %#v", filtered)
	}
	if allocs != 0 {
		t.Fatalf("stable passive motion allocated %.2f times, want 0", allocs)
	}
}

func TestToolHoverIsPresentationOnlyAndLeavesCanonicalViewportUnchanged(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "hover.txt",
		toolStatus: "ok", toolGroupPending: true,
	}}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	canonical := model.viewport.View()

	model.setToolHover(0)

	if got := model.viewport.View(); got != canonical {
		t.Fatal("presentation-only hover mutated canonical viewport content")
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: hover.txt") {
		t.Fatalf("hover overlay missing from rendered view:\n%s", view)
	}
}

func TestInvalidToolHoverOverlayFallsBackAtomicallyToCanonicalViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "hover.txt",
		toolStatus: "ok", toolGroupPending: true,
	}}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	model.setToolHover(0)
	model.transcriptInteraction.tools[0].patchHeight++

	model.toolHoverIndex = -1
	canonical := model.renderTranscriptRegion(model.currentLayout())
	model.toolHoverIndex = 0
	if got := model.renderTranscriptRegion(model.currentLayout()); got != canonical {
		t.Fatal("invalid hover overlay partially changed canonical viewport")
	}
}

func TestToolHoverTransitionIsPresentationOnly(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:             entryTool,
		title:            "tool",
		toolName:         "Read",
		toolTarget:       "README.md",
		toolStatus:       "ok",
		toolGroupPending: true,
	}}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	toolRow := -1
	for row, interaction := range model.transcriptInteraction.rows {
		if interaction.toolIndex == 0 && interaction.hoverVisible {
			toolRow = row
			break
		}
	}
	if toolRow < 0 {
		t.Fatal("rendered tool has no hoverable interaction row")
	}

	beforeVersion := model.transcript[0].version
	beforeInvalidation := model.transcriptInvalidation
	beforeRefreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	y := model.transcriptScreenTop() + toolRow - model.viewport.YOffset
	next, _ := model.Update(tea.MouseMsg{
		X:      mainContentPadding + 1,
		Y:      y,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	})
	model = next.(appModel)

	if model.toolHoverIndex != 0 {
		t.Fatalf("tool hover index = %d, want 0", model.toolHoverIndex)
	}
	if got := model.transcript[0].version; got != beforeVersion {
		t.Fatalf("tool hover changed entry version %d -> %d", beforeVersion, got)
	}
	if model.transcriptInvalidation != beforeInvalidation {
		t.Fatalf("tool hover changed transcript invalidation from %#v to %#v", beforeInvalidation, model.transcriptInvalidation)
	}
	if got := model.transcriptRefreshCount - beforeRefreshes; got != 0 {
		t.Fatalf("tool hover refreshed transcript %d times, want 0", got)
	}
	if got := model.transcriptRenderVisits; got != 0 {
		t.Fatalf("tool hover rendered %d transcript entries, want 0", got)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃") {
		t.Fatalf("hover marker missing from view:\n%s", view)
	}

	var current tea.Model = model
	var message tea.Msg = tea.MouseMsg{X: mainContentPadding + 1, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}
	if allocs := testing.AllocsPerRun(100, func() {
		if got := filterIdleMouseMotion(current, message); got != nil {
			panic("stable tool hover was not filtered")
		}
	}); allocs != 0 {
		t.Fatalf("stable tool hover allocated %.2f times, want 0", allocs)
	}
}

func TestToolHoverMoveChangesPresentationWithoutTranscriptWork(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.txt", toolStatus: "ok", toolGroupPending: true},
		{kind: entryAssistant, title: "assistant", body: "between tools"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.txt", toolStatus: "ok", toolGroupPending: true},
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	pointFor := func(toolIndex int) tea.MouseMsg {
		for row, interaction := range model.transcriptInteraction.rows {
			if interaction.toolIndex == toolIndex && interaction.hoverVisible {
				return tea.MouseMsg{
					X:      mainContentPadding + 1,
					Y:      model.transcriptScreenTop() + row - model.viewport.YOffset,
					Button: tea.MouseButtonNone,
					Action: tea.MouseActionMotion,
				}
			}
		}
		t.Fatalf("tool %d has no hoverable row", toolIndex)
		return tea.MouseMsg{}
	}

	next, _ := model.Update(pointFor(0))
	model = next.(appModel)
	beforeRefreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	next, _ = model.Update(pointFor(2))
	model = next.(appModel)

	if model.toolHoverIndex != 2 {
		t.Fatalf("tool hover index = %d, want 2", model.toolHoverIndex)
	}
	if got := model.transcriptRefreshCount - beforeRefreshes; got != 0 {
		t.Fatalf("cross-tool hover refreshed transcript %d times, want 0", got)
	}
	if model.transcriptRenderVisits != 0 {
		t.Fatalf("cross-tool hover render_visits=%d", model.transcriptRenderVisits)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: two.txt") {
		t.Fatalf("new tool hover missing from rendered view:\n%s", view)
	}
}

func TestDirtySuffixAfterHoveredToolKeepsStableOverlay(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.txt", toolStatus: "ok", toolGroupPending: true},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.txt", toolStatus: "ok", toolGroupPending: true},
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	first, ok := model.transcriptInteraction.toolAt(0)
	if !ok {
		t.Fatal("first tool interaction missing")
	}
	second, ok := model.transcriptInteraction.toolAt(1)
	if !ok {
		t.Fatal("second tool interaction missing")
	}
	if got, want := second.patchStart, first.patchStart+first.patchHeight; got != want {
		t.Fatalf("test setup has a gap between tool patches: second start=%d, first end=%d", got, want)
	}

	model.setToolHover(0)
	model.transcript[1].toolTarget = "updated.txt"
	model.touchTranscriptEntryAt(1)
	model.refreshViewportPreservingOffset()

	if !model.transcriptInteraction.valid || len(model.transcriptInteraction.rows) != len(model.transcriptLines) {
		t.Fatalf("interaction index lost suffix alignment: valid=%v rows=%d transcript_lines=%d", model.transcriptInteraction.valid, len(model.transcriptInteraction.rows), len(model.transcriptLines))
	}
	if got, ok := model.transcriptInteraction.toolAt(0); !ok || got != first {
		t.Fatalf("stable prefix interaction changed: got=%#v ok=%v want=%#v", got, ok, first)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: one.txt") || !strings.Contains(view, "updated.txt") {
		t.Fatalf("stable hover overlay or dirty suffix missing:\n%s", view)
	}
}

func TestDirtyHoveredToolRebuildsInteractionRowsBeforeRenderingOverlay(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:             entryTool,
		title:            "tool",
		toolName:         "Read",
		toolTarget:       "one.txt",
		toolStatus:       "ok",
		toolResult:       "first line",
		toolExpanded:     true,
		toolGroupPending: true,
	}}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	model.setToolHover(0)

	before, ok := model.transcriptInteraction.toolAt(0)
	if !ok {
		t.Fatal("initial tool interaction missing")
	}
	model.transcript[0].toolResult = "first line\nsecond line\nthird line"
	model.touchTranscriptEntryAt(0)
	model.refreshViewportPreservingOffset()

	after, ok := model.transcriptInteraction.toolAt(0)
	if !ok {
		t.Fatal("updated tool interaction missing")
	}
	if after.patchHeight <= before.patchHeight {
		t.Fatalf("updated patch height = %d, want greater than %d", after.patchHeight, before.patchHeight)
	}
	if !model.transcriptInteraction.valid || len(model.transcriptInteraction.rows) != len(model.transcriptLines) {
		t.Fatalf("interaction index lost suffix alignment: valid=%v rows=%d transcript_lines=%d", model.transcriptInteraction.valid, len(model.transcriptInteraction.rows), len(model.transcriptLines))
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃") {
		t.Fatalf("hover marker missing after dirty suffix replacement:\n%s", view)
	}
}

func TestCollapsedToolGroupHoverDoesNotRenderOverlay(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{kind: entryTool, title: "tool", toolName: "Read", toolStatus: "ok"}}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	toolRow := -1
	for row, interaction := range model.transcriptInteraction.rows {
		if interaction.toolIndex == 0 {
			toolRow = row
			break
		}
	}
	if toolRow < 0 {
		t.Fatal("collapsed tool group header was not indexed")
	}
	before := model.View()
	beforeRefreshes := model.transcriptRefreshCount
	next, _ := model.Update(tea.MouseMsg{
		X:      mainContentPadding + 1,
		Y:      model.transcriptScreenTop() + toolRow - model.viewport.YOffset,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	})
	model = next.(appModel)

	if model.toolHoverIndex != 0 {
		t.Fatalf("collapsed group hover index = %d, want 0", model.toolHoverIndex)
	}
	if got := model.transcriptRefreshCount - beforeRefreshes; got != 0 {
		t.Fatalf("collapsed group hover refreshed transcript %d times, want 0", got)
	}
	if after := model.View(); after != before {
		t.Fatal("collapsed group hover changed rendered presentation")
	}
}

func TestMouseWheelReconcilesToolHoverAtSameScreenPoint(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("before %02d", index)})
	}
	firstTool := len(model.transcript)
	model.transcript = append(model.transcript,
		transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.txt", toolStatus: "ok", toolGroupPending: true},
		transcriptEntry{kind: entryAssistant, title: "assistant", body: "between"},
	)
	secondTool := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.txt", toolStatus: "ok", toolGroupPending: true})
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("after %02d", index)})
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	toolRow := func(toolIndex int) int {
		for row, interaction := range model.transcriptInteraction.rows {
			if interaction.toolIndex == toolIndex && interaction.hoverVisible {
				return row
			}
		}
		t.Fatalf("tool %d has no hoverable row", toolIndex)
		return -1
	}
	firstRow := toolRow(firstTool)
	secondRow := toolRow(secondTool)
	delta := secondRow - firstRow
	if delta <= 0 {
		t.Fatalf("tool row delta = %d, want positive", delta)
	}
	model.viewport.MouseWheelDelta = delta
	screenRow := 1
	model.viewport.SetYOffset(firstRow - screenRow)
	x := mainContentPadding + 1
	y := model.transcriptScreenTop() + screenRow

	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != firstTool {
		t.Fatalf("initial hover index = %d, want %d", model.toolHoverIndex, firstTool)
	}
	beforeRefreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	next, _ = model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Type:   tea.MouseWheelDown,
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	})
	model = next.(appModel)

	if model.toolHoverIndex != secondTool {
		t.Fatalf("hover index after wheel = %d, want %d", model.toolHoverIndex, secondTool)
	}
	if got := model.transcriptRefreshCount - beforeRefreshes; got != 0 {
		t.Fatalf("wheel hover reconciliation refreshed transcript %d times, want 0", got)
	}
	if model.transcriptRenderVisits != 0 {
		t.Fatalf("wheel hover reconciliation render_visits=%d", model.transcriptRenderVisits)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: two.txt") {
		t.Fatalf("wheel hover overlay missing:\n%s", view)
	}
}

func TestNoticeHoverClearsCoveredToolHover(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("history %02d", index)})
	}
	toolIndex := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "covered.txt", toolStatus: "ok", toolGroupPending: true})
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("tail %02d", index)})
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	toolRow := -1
	for row, interaction := range model.transcriptInteraction.rows {
		if interaction.toolIndex == toolIndex && interaction.hoverVisible {
			toolRow = row
			break
		}
	}
	if toolRow < 0 {
		t.Fatal("tool has no hoverable row")
	}
	model.viewport.SetYOffset(toolRow - (model.viewport.Height - 1))
	toolY := model.transcriptScreenTop() + model.viewport.Height - 1
	next, _ := model.Update(tea.MouseMsg{X: mainContentPadding + 1, Y: toolY, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != toolIndex {
		t.Fatalf("initial tool hover index = %d, want %d", model.toolHoverIndex, toolIndex)
	}

	model.newMessageNoticeCount = 1
	bounds := model.transcriptNoticeBounds()
	beforeRefreshes := model.transcriptRefreshCount
	next, _ = model.Update(tea.MouseMsg{X: bounds.x, Y: bounds.y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion})
	model = next.(appModel)

	if !model.newMessageNoticeHovered {
		t.Fatal("notice did not become hovered")
	}
	if model.toolHoverIndex != -1 {
		t.Fatalf("covered tool hover index = %d, want -1", model.toolHoverIndex)
	}
	if got := model.transcriptRefreshCount - beforeRefreshes; got != 0 {
		t.Fatalf("notice hover refreshed transcript %d times, want 0", got)
	}
}

func TestExpandedToolGroupHoverMovesWithinOneOverlay(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.txt", toolStatus: "ok", toolExpanded: true},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.txt", toolStatus: "ok"},
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()

	pointFor := func(toolIndex int) tea.MouseMsg {
		for row, interaction := range model.transcriptInteraction.rows {
			if interaction.toolIndex == toolIndex && interaction.hoverVisible && !interaction.header {
				return tea.MouseMsg{
					X:      mainContentPadding + 1,
					Y:      model.transcriptScreenTop() + row - model.viewport.YOffset,
					Button: tea.MouseButtonNone,
					Action: tea.MouseActionMotion,
				}
			}
		}
		t.Fatalf("expanded group tool %d has no hoverable detail row", toolIndex)
		return tea.MouseMsg{}
	}

	next, _ := model.Update(pointFor(0))
	model = next.(appModel)
	next, _ = model.Update(pointFor(1))
	model = next.(appModel)

	if model.toolHoverIndex != 1 {
		t.Fatalf("expanded group hover index = %d, want 1", model.toolHoverIndex)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: two.txt") {
		t.Fatalf("second grouped tool hover missing:\n%s", view)
	}
}

func TestStableToolGroupHoverReusesPresentationPatchAcrossViews(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 100
	model.height = 30
	model.transcript = make([]transcriptEntry, 50)
	for index := range model.transcript {
		model.transcript[index] = transcriptEntry{
			kind: entryTool, title: "tool", toolName: "Read", toolStatus: "ok",
			toolTarget: fmt.Sprintf("file-%02d.txt", index), toolExpanded: index == 0,
		}
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	renders := 0
	model.transcriptHoverPatchRenderSpy = &renders
	model.setToolHover(25)

	for range 10 {
		_ = model.View()
	}

	if renders != 1 {
		t.Fatalf("stable hover rendered presentation patch %d times, want 1", renders)
	}
}

func TestPendingToolSegmentDiscoversRangeOncePerRenderPath(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.transcript = make([]transcriptEntry, 200)
	for index := range model.transcript {
		model.transcript[index] = transcriptEntry{
			kind: entryTool, title: "tool", toolName: "Read", toolStatus: "running",
			toolTarget: fmt.Sprintf("file-%03d.txt", index), toolGroupPending: index == 0,
		}
	}
	model.invalidateTranscriptStructure()
	model.ensureTranscriptLinesAt(100, true, model.animationNow())

	if got := model.transcriptSegmentRangeCalls; got > 1 {
		t.Fatalf("pending render performed %d segment range lookups, want at most 1", got)
	}
	if got := model.transcriptInteractionRangeCalls; got > 1 {
		t.Fatalf("pending interaction build performed %d segment range lookups, want at most 1", got)
	}
}

func TestKeyboardTranscriptScrollClearsToolHover(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("before %02d", index)})
	}
	toolIndex := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "hovered.txt",
		toolStatus: "ok", toolGroupPending: true,
	})
	for index := 0; index < 20; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("after %02d", index)})
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	model.setToolHover(toolIndex)
	model.transcriptKeyScrollActive = true
	model.viewport.SetYOffset(model.viewport.YOffset - 1)

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)

	if model.toolHoverIndex != -1 {
		t.Fatalf("keyboard scroll retained stale tool hover %d, want -1", model.toolHoverIndex)
	}
}

func TestKeyboardPageScrollClearsToolHover(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	for index := 0; index < 40; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("before %02d", index)})
	}
	toolIndex := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "hovered.txt",
		toolStatus: "ok", toolGroupPending: true,
	})
	for index := 0; index < 40; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("after %02d", index)})
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	model.setToolHover(toolIndex)
	model.transcriptKeyScrollActive = true
	beforeOffset := model.viewport.YOffset

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = next.(appModel)

	if model.viewport.YOffset >= beforeOffset {
		t.Fatalf("page up did not scroll viewport: %d -> %d", beforeOffset, model.viewport.YOffset)
	}
	if model.toolHoverIndex != -1 {
		t.Fatalf("page scroll retained stale tool hover %d, want -1", model.toolHoverIndex)
	}
}

func TestDirtySuffixAfterHoveredPrefixReusesPresentationPatch(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "stable.txt", toolStatus: "ok", toolGroupPending: true},
		{kind: entryAssistant, title: "assistant", body: "stable middle"},
		{kind: entryAssistant, title: "assistant", body: "tail"},
	}
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	renders := 0
	model.transcriptHoverPatchRenderSpy = &renders
	model.setToolHover(0)
	if renders != 1 {
		t.Fatalf("initial hover rendered presentation patch %d times, want 1", renders)
	}

	model.transcript[2].body = "updated tail"
	model.touchTranscriptEntryAt(2)
	model.refreshViewportPreservingOffset()

	if renders != 1 {
		t.Fatalf("stable prefix hover patch rerendered after dirty suffix: renders=%d", renders)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "┃ ✓ Read: stable.txt") || !strings.Contains(view, "updated tail") {
		t.Fatalf("stable hover or updated suffix missing:\n%s", view)
	}
}

func TestToolInspectKeyboardNavigationClearsHoverWhenViewportScrolls(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	firstTool := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "first.txt",
		toolStatus: "ok", toolGroupPending: true,
	})
	for index := 0; index < 40; index++ {
		model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: fmt.Sprintf("middle %02d", index)})
	}
	secondTool := len(model.transcript)
	model.transcript = append(model.transcript, transcriptEntry{
		kind: entryTool, title: "tool", toolName: "Read", toolTarget: "second.txt",
		toolStatus: "ok", toolGroupPending: true,
	})
	model.invalidateTranscriptStructure()
	model.relayout()
	model.refreshViewport()
	model.toolInspectActive = true
	model.toolInspectIndex = secondTool
	model.transcript[secondTool].toolFocused = true
	model.touchTranscriptEntryAt(secondTool)
	model.refreshViewportPreservingOffset()
	model.ensureInspectedToolVisible()
	model.setToolHover(secondTool)
	beforeOffset := model.viewport.YOffset

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)

	if model.toolInspectIndex != firstTool {
		t.Fatalf("inspect index = %d, want first tool %d", model.toolInspectIndex, firstTool)
	}
	if model.viewport.YOffset >= beforeOffset {
		t.Fatalf("inspect navigation did not scroll viewport: %d -> %d", beforeOffset, model.viewport.YOffset)
	}
	if model.toolHoverIndex != -1 {
		t.Fatalf("inspect navigation retained stale tool hover %d, want -1", model.toolHoverIndex)
	}
}

func TestIdleMouseMotionFilterDoesNotAllocateForStableNoticeState(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.newMessageNoticeCount = 12

	var current tea.Model = model
	var message tea.Msg = tea.MouseMsg{
		X:      model.width - 1,
		Y:      model.height - 1,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	}
	var filtered tea.Msg
	allocs := testing.AllocsPerRun(100, func() {
		filtered = filterIdleMouseMotion(current, message)
	})

	if filtered != nil {
		t.Fatalf("stable notice motion was not filtered: %#v", filtered)
	}
	if allocs != 0 {
		t.Fatalf("stable notice motion allocated %.2f times, want 0", allocs)
	}
}
