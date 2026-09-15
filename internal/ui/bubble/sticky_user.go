package bubble

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func (m *appModel) refreshStickyUserLocations(from int) {
	keep := sort.Search(len(m.stickyUserLocations), func(i int) bool {
		return m.stickyUserLocations[i].transcriptIndex >= from
	})
	m.stickyUserLocations = m.stickyUserLocations[:keep]
	for i := from; i < len(m.transcriptEntrySpans); i++ {
		span := m.transcriptEntrySpans[i]
		if span.startRow >= 0 && span.height > 0 && m.viewEntries[i].kind == entryUser {
			m.stickyUserLocations = append(m.stickyUserLocations, transcriptEntryLocation{transcriptIndex: i, startRow: span.startRow, height: span.height})
		}
	}
}

func (m appModel) stickyUserLocation() (transcriptEntryLocation, bool) {
	if m.viewport.Height < 2 || len(m.stickyUserLocations) == 0 {
		return transcriptEntryLocation{}, false
	}
	i := sort.Search(len(m.stickyUserLocations), func(i int) bool {
		return m.stickyUserLocations[i].startRow > m.viewport.YOffset
	}) - 1
	if i < 0 {
		return transcriptEntryLocation{}, false
	}
	loc := m.stickyUserLocations[i]
	return loc, loc.startRow+loc.height <= m.viewport.YOffset
}

func (m appModel) stickyUserAtMouse(x, y int) (transcriptEntryLocation, bool) {
	if m.currentLayout().activityMode == activityLayoutFullscreen || m.configCenter != nil || m.settingWizard != nil {
		return transcriptEntryLocation{}, false
	}
	if y != m.transcriptScreenTop() {
		return transcriptEntryLocation{}, false
	}
	if _, ok := m.transcriptContentColumn(x); !ok {
		return transcriptEntryLocation{}, false
	}
	return m.stickyUserLocation()
}

// The overlay leaves every subsequent row at its original screen coordinate.
// Its control row is excluded from text/tool hit tests; clicking returns to the source.
func (m appModel) overlayStickyUser(content string) string {
	loc, ok := m.stickyUserLocation()
	if !ok || loc.startRow >= len(m.transcriptLines) {
		return content
	}
	_, tail, hasTail := strings.Cut(content, "\n")
	if !hasTail {
		return content
	}
	width := m.viewport.Width
	hint := " ↥"
	if loc.height > 1 {
		hint = "… ↥"
	}
	line := cutStyledCellsExact(m.transcriptLines[loc.startRow], 0, maxInt(0, width-terminalCellWidth(hint))) + hint
	line = truncateStyledCellLine(line, width)
	return renderUserTranscriptRow(line, width) + "\n" + tail
}

func renderUserTranscriptRow(body string, width int) string {
	if lipgloss.ColorProfile() == termenv.TrueColor {
		if background, ok := userTranscriptRowStyle.GetBackground().(lipgloss.Color); ok {
			row := userTranscriptRowStyle.UnsetBackground().Width(width).Render(body)
			return restoreBackgroundAfterANSIReset(row, string(background), colorManager.Hex(colorLabelUser))
		}
	}
	return userTranscriptRowStyle.Width(width).Render(body)
}
