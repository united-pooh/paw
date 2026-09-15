package bubble

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func stickyUserFixture() appModel {
	m := newTestModel(&fakeRunner{})
	m.ready = true
	m.width, m.height = 70, 14
	m.transcript = []transcriptEntry{
		{kind: entryUser, body: "第一轮：检查磁盘\n保留下载目录"},
		{kind: entryAssistant, body: strings.Repeat("第一轮回答\n", 24)},
		{kind: entryUser, body: "第二轮：只检查缓存"},
		{kind: entryAssistant, body: strings.Repeat("第二轮回答\n", 24)},
	}
	m.relayout()
	m.refreshViewport()
	return m
}

func TestStickyUserTracksScrolledTurnAndSuppressesVisibleSource(t *testing.T) {
	m := stickyUserFixture()
	locations := m.transcriptEntryLocationsAt()
	first, second := locations[0], locations[2]
	for _, offset := range []int{first.startRow, first.startRow + 1, second.startRow} {
		m.viewport.SetYOffset(offset)
		if _, ok := m.stickyUserLocation(); ok {
			t.Fatalf("offset %d duplicated a visible input", offset)
		}
	}
	for _, loc := range []transcriptEntryLocation{first, second} {
		m.viewport.SetYOffset(loc.startRow + loc.height + 2)
		got, ok := m.stickyUserLocation()
		if !ok || got.transcriptIndex != loc.transcriptIndex {
			t.Fatalf("wrong sticky source: %#v", got)
		}
		view := ansi.Strip(m.renderTranscriptRegion(m.currentLayout()))
		if !strings.Contains(strings.Split(view, "\n")[0], strings.Split(m.viewEntries[loc.transcriptIndex].body, "\n")[0]) {
			t.Fatalf("sticky row missing input: %s", view)
		}
	}
	// Seeing the next input below the viewport top does not replace the current turn.
	m.viewport.SetYOffset(second.startRow - 2)
	if got, ok := m.stickyUserLocation(); !ok || got.transcriptIndex != first.transcriptIndex {
		t.Fatal("switched turn too early")
	}
}

func TestStickyUserClickReturnsToSourceWithoutSelectingHiddenText(t *testing.T) {
	m := stickyUserFixture()
	m.viewport.SetYOffset(7)
	if _, ok := m.transcriptPointForMouse(6, m.transcriptScreenTop()); ok {
		t.Fatal("sticky control maps to hidden transcript text")
	}
	if got, _ := m.toolHoverIndexAtMouse(6, m.transcriptScreenTop()); got != -1 {
		t.Fatal("sticky row hovered a hidden tool")
	}
	point, ok := m.transcriptPointForMouse(6, m.transcriptScreenTop()+1)
	if !ok || point.row != 8 {
		t.Fatalf("body row mapping changed: %#v", point)
	}
	m, handled, _ := m.handleTranscriptMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 6, Y: m.transcriptScreenTop()})
	if !handled || m.viewport.YOffset != 0 || m.selecting {
		t.Fatal("sticky click did not return to original input")
	}
}

func TestStickyUserPreservesBottomAndUpdatesAfterResize(t *testing.T) {
	m := stickyUserFixture()
	for _, width := range []int{40, 70, 120} {
		m.width = width
		m.relayout()
		m.refreshViewport()
		m.viewport.GotoBottom()
		before := strings.Split(ansi.Strip(m.viewport.View()), "\n")
		after := strings.Split(ansi.Strip(m.overlayStickyUser(m.viewport.View())), "\n")
		if len(before) != len(after) || strings.Join(before[1:], "\n") != strings.Join(after[1:], "\n") {
			t.Fatal("sticky overlay moved or lost bottom content")
		}
		assertFixedFrame(t, m.View(), width, m.height)
	}
	m.addEntry(transcriptEntry{kind: entryUser, body: "第三轮"})
	m.addEntry(transcriptEntry{kind: entryAssistant, body: strings.Repeat("结果\n", 25)})
	m.viewport.GotoBottom()
	if got, ok := m.stickyUserLocation(); !ok || m.viewEntries[got.transcriptIndex].body != "第三轮" {
		t.Fatal("incremental user anchor did not update")
	}
}

func TestStickyUserControlIsInactiveBehindFullscreenActivity(t *testing.T) {
	m := stickyUserFixture()
	m.width = 40
	m.activity.visible = true
	m.relayout()
	m.refreshViewport()
	m.viewport.GotoBottom()
	if m.currentLayout().activityMode != activityLayoutFullscreen {
		t.Fatal("fixture did not open fullscreen Activity")
	}
	if _, ok := m.stickyUserAtMouse(6, m.transcriptScreenTop()); ok {
		t.Fatal("hidden sticky input intercepted Activity")
	}
}

func BenchmarkStickyUserLookup(b *testing.B) {
	m := stickyUserFixture()
	m.transcript = nil
	for i := 0; i < 1000; i++ {
		m.transcript = append(m.transcript, transcriptEntry{kind: entryUser, body: fmt.Sprint(i)}, transcriptEntry{kind: entryAssistant, body: "reply"})
	}
	m.invalidateTranscriptRender()
	m.refreshViewport()
	m.viewport.GotoBottom()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.stickyUserLocation()
	}
}

func TestUserBackgroundCoversEveryCellIncludingTokenResetsAndTrailingSpace(t *testing.T) {
	setTrueColorForTest(t)
	want := string(userTranscriptRowStyle.GetBackground().(lipgloss.Color))
	entry := transcriptEntry{kind: entryUser, body: "检查 file.go", inputTokens: []inputToken{{Kind: inputTokenFile, Start: len("检查 "), End: len("检查 file.go"), Label: "file.go"}}}
	for _, width := range []int{40, 80} {
		line := renderEntry(entry, width)
		parsed := parseStyledCellLine(line)
		state := activityVisualANSIStyle{}
		if parsed.width != width {
			t.Fatalf("row width %d, want %d", parsed.width, width)
		}
		for _, atom := range parsed.atoms {
			if atom.control {
				if strings.HasPrefix(atom.text, "\x1b[") && strings.HasSuffix(atom.text, "m") {
					activityVisualApplySGR(&state, atom.text[2:len(atom.text)-1])
				}
				continue
			}
			if state.background != want {
				t.Fatalf("cell %d (%q) background %q, want %q", atom.cellStart, atom.text, state.background, want)
			}
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if rendered := renderEntry(entry, 40); strings.Contains(rendered, "\x1b[") {
		t.Fatal("no-color output contains background escapes")
	}
}
