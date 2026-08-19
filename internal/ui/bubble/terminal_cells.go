package bubble

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// terminalCellWidth returns the number of terminal cells occupied by styled
// text. x/ansi is shared by this package, Lipgloss, and Bubble Tea, so all
// layout and renderer layers use the same width model.
func terminalCellWidth(text string) int {
	return ansi.StringWidth(text)
}

func terminalFirstGraphemeCluster(text string) (string, int) {
	if text == "" {
		return "", 0
	}
	cluster, width := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
	if cluster == "" {
		cluster = text[:1]
		width = ansi.StringWidth(cluster)
	}
	return cluster, width
}

// wrapStyledCellText wraps styled multi-line text without splitting terminal
// control sequences or grapheme clusters. Explicit source newlines are kept as
// line boundaries, including trailing blank lines.
func wrapStyledCellText(text string, width int) []string {
	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapStyledCellLine(line, width)...)
	}
	return wrapped
}

// wrapStyledCellLine wraps one rendered line at legal terminal-cell
// boundaries. ANSI CSI/OSC sequences are zero-width atoms and are replayed as
// complete sequences on every fragment that needs their style state.
func wrapStyledCellLine(text string, width int) []string {
	width = maxInt(1, width)
	parsed := parseStyledCellLine(text)
	safeText := renderStyledCellAtoms(parsed.atoms, 0, maxInt(1, parsed.width))
	if parsed.width <= width {
		return []string{safeText}
	}

	lines := make([]string, 0, (parsed.width+width-1)/width)
	for left := 0; left < parsed.width; {
		limit := minInt(parsed.width, left+width)
		right := floorStyledCellBoundary(parsed.boundaries, limit)
		if right <= left {
			// The next grapheme is wider than the entire row (normally a
			// two-cell glyph in a one-cell row). Preserve geometry without
			// splitting the grapheme and advance to its next legal boundary.
			next := ceilStyledCellBoundary(parsed.boundaries, left+1)
			if next <= left {
				break
			}
			lines = append(lines, "…")
			left = next
			continue
		}
		lines = append(lines, renderStyledCellAtoms(parsed.atoms, left, right))
		left = right
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// truncateStyledCells truncates styled text to width cells and appends tail
// when truncation is required. Both text and tail are sanitized through the
// styled-cell parser, so incomplete terminal controls are never emitted.
func truncateStyledCells(text string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	parsed := parseStyledCellLine(text)
	safeText := renderStyledCellAtoms(parsed.atoms, 0, maxInt(1, parsed.width))
	if parsed.width <= width {
		return safeText
	}

	tailParsed := parseStyledCellLine(tail)
	safeTail := renderStyledCellAtoms(tailParsed.atoms, 0, maxInt(1, tailParsed.width))
	if tailParsed.width > width {
		safeTail = truncateStyledCells(safeTail, width, "")
		tailParsed = parseStyledCellLine(safeTail)
	}
	contentWidth := maxInt(0, width-tailParsed.width)
	right := floorStyledCellBoundary(parsed.boundaries, contentWidth)
	content := renderStyledCellAtoms(parsed.atoms, 0, right)
	return content + safeTail
}

// truncateStyledCellLine is the standard UI truncation policy: keep the line
// within its cell budget and show a one-cell ellipsis when content is omitted.
func truncateStyledCellLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return truncateStyledCells(text, width, "…")
}

// fitStyledCellLine truncates and pads one styled line to an exact terminal
// cell width. It never splits a grapheme cluster.
func fitStyledCellLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	w := terminalCellWidth(line)
	if w > width {
		// cutStyledCellsExact 保证返回恰好 width 个 cell，无需二次测量。
		return cutStyledCellsExact(line, 0, width)
	}
	if w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}

type styledCellAtom struct {
	text       string
	cellStart  int
	cellEnd    int
	ownerStart int
	ownerEnd   int
	control    bool
}

type styledCellLine struct {
	atoms      []styledCellAtom
	boundaries []int
	width      int
}

// parseStyledCellLine converts a rendered line into zero-width ANSI controls
// and printable cell atoms. Zero-width printable marks are attached to the
// preceding cell span so truncation keeps constructs such as "(ﾟ" and "é"
// intact even when the upstream decoder reports them as separate sequences.
func parseStyledCellLine(line string) styledCellLine {
	result := styledCellLine{
		boundaries: []int{0},
	}
	state := byte(ansi.NormalState)
	cell := 0
	lastVisibleStart := -1
	lastVisibleEnd := -1
	pendingZeroWidth := make([]int, 0, 1)
	var pendingControl strings.Builder

	for len(line) > 0 {
		var (
			sequence string
			width    int
			read     int
			newState = state
			control  = state != ansi.NormalState || isTerminalControlByte(line[0])
		)
		if control {
			sequence, width, read, newState = ansi.GraphemeWidth.DecodeSequenceInString(line, state, nil)
		} else {
			sequence, width = ansi.FirstGraphemeCluster(line, ansi.GraphemeWidth)
			read = len(sequence)
			newState = ansi.NormalState
		}
		if read <= 0 {
			// Defensive progress for malformed input. Display paths sanitize
			// invalid UTF-8 before styling, so this should not normally occur.
			sequence = line[:1]
			read = 1
			width = 0
			newState = ansi.NormalState
		}
		line = line[read:]
		if control && newState != ansi.NormalState {
			pendingControl.WriteString(sequence)
			state = newState
			continue
		}
		if control && pendingControl.Len() > 0 {
			pendingControl.WriteString(sequence)
			sequence = pendingControl.String()
			pendingControl.Reset()
		}
		state = newState
		if control && !terminalControlSequenceComplete(sequence) {
			// Some decoder paths report EOF-terminated OSC/CSI input as a
			// syntactically finished zero-width sequence. Keep it pending so it
			// is discarded at end of line instead of exposing its parameters.
			pendingControl.WriteString(sequence)
			continue
		}

		if width > 0 {
			atom := styledCellAtom{
				text:       sequence,
				cellStart:  cell,
				cellEnd:    cell + width,
				ownerStart: cell,
				ownerEnd:   cell + width,
			}
			result.atoms = append(result.atoms, atom)
			for _, index := range pendingZeroWidth {
				result.atoms[index].ownerStart = atom.cellStart
				result.atoms[index].ownerEnd = atom.cellEnd
			}
			pendingZeroWidth = pendingZeroWidth[:0]
			cell += width
			result.boundaries = append(result.boundaries, cell)
			lastVisibleStart = atom.cellStart
			lastVisibleEnd = atom.cellEnd
			continue
		}

		if control {
			result.atoms = append(result.atoms, styledCellAtom{
				text:       sequence,
				cellStart:  cell,
				cellEnd:    cell,
				ownerStart: -1,
				ownerEnd:   -1,
				control:    true,
			})
			continue
		}

		atom := styledCellAtom{
			text:       sequence,
			cellStart:  cell,
			cellEnd:    cell,
			ownerStart: lastVisibleStart,
			ownerEnd:   lastVisibleEnd,
		}
		result.atoms = append(result.atoms, atom)
		if lastVisibleStart < 0 {
			pendingZeroWidth = append(pendingZeroWidth, len(result.atoms)-1)
		}
	}

	result.width = cell
	return result
}

func isTerminalControlByte(first byte) bool {
	return first == 0x1b ||
		first < 0x20 ||
		first == 0x7f ||
		first >= 0x80 && first <= 0x9f
}

func terminalControlSequenceComplete(sequence string) bool {
	if sequence == "" {
		return false
	}
	if strings.HasPrefix(sequence, "\x1b[") || sequence[0] == 0x9b {
		last := sequence[len(sequence)-1]
		return last >= 0x40 && last <= 0x7e
	}
	if strings.HasPrefix(sequence, "\x1b]") || sequence[0] == 0x9d {
		return strings.HasSuffix(sequence, "\a") ||
			strings.HasSuffix(sequence, "\x1b\\") ||
			sequence[len(sequence)-1] == 0x9c
	}
	if strings.HasPrefix(sequence, "\x1bP") ||
		strings.HasPrefix(sequence, "\x1bX") ||
		strings.HasPrefix(sequence, "\x1b^") ||
		strings.HasPrefix(sequence, "\x1b_") ||
		sequence[0] == 0x90 || sequence[0] == 0x98 || sequence[0] == 0x9e || sequence[0] == 0x9f {
		return strings.HasSuffix(sequence, "\x1b\\") || sequence[len(sequence)-1] == 0x9c
	}
	if sequence[0] == 0x1b {
		return len(sequence) >= 2
	}
	return true
}

func renderStyledCellAtoms(atoms []styledCellAtom, left, right int) string {
	var rendered strings.Builder
	sgrActive := false
	hyperlinkActive := false
	for _, atom := range atoms {
		if atom.control {
			// Keeping controls in source order recreates the active SGR state at
			// the start of the slice and restores it after the selected text.
			rendered.WriteString(atom.text)
			sgrActive = styledCellSGRActive(atom.text, sgrActive)
			hyperlinkActive = styledCellHyperlinkActive(atom.text, hyperlinkActive)
			continue
		}
		if atom.cellEnd > atom.cellStart {
			if atom.cellStart >= left && atom.cellEnd <= right {
				rendered.WriteString(atom.text)
			}
			continue
		}
		if atom.ownerEnd > atom.ownerStart {
			if atom.ownerStart >= left && atom.ownerEnd <= right {
				rendered.WriteString(atom.text)
			}
			continue
		}
		if atom.cellStart >= left && atom.cellStart < right {
			rendered.WriteString(atom.text)
		}
	}
	if hyperlinkActive {
		rendered.WriteString(ansi.ResetHyperlink())
	}
	if sgrActive {
		rendered.WriteString(ansi.ResetStyle)
	}
	return rendered.String()
}

func styledCellSGRActive(sequence string, active bool) bool {
	if !strings.HasSuffix(sequence, "m") {
		return active
	}
	start := strings.Index(sequence, "[")
	if start < 0 && len(sequence) > 0 && sequence[0] == 0x9b {
		start = 0
	}
	if start < 0 || start+1 >= len(sequence) {
		return active
	}
	parameters := sequence[start+1 : len(sequence)-1]
	if parameters == "" {
		return false
	}
	for _, parameter := range strings.Split(parameters, ";") {
		if parameter == "" || parameter == "0" {
			active = false
			continue
		}
		active = true
	}
	return active
}

func styledCellHyperlinkActive(sequence string, active bool) bool {
	payload := ""
	switch {
	case strings.HasPrefix(sequence, "\x1b]8;"):
		payload = sequence[len("\x1b]8;"):]
	case len(sequence) > 0 && sequence[0] == 0x9d && strings.HasPrefix(sequence[1:], "8;"):
		payload = sequence[3:]
	default:
		return active
	}
	if end := strings.IndexByte(payload, '\a'); end >= 0 {
		payload = payload[:end]
	}
	if end := strings.Index(payload, "\x1b\\"); end >= 0 {
		payload = payload[:end]
	}
	separator := strings.IndexByte(payload, ';')
	if separator < 0 {
		return active
	}
	return payload[separator+1:] != ""
}

func ceilStyledCellBoundary(boundaries []int, cell int) int {
	index := sort.SearchInts(boundaries, cell)
	if index >= len(boundaries) {
		return boundaries[len(boundaries)-1]
	}
	return boundaries[index]
}

func floorStyledCellBoundary(boundaries []int, cell int) int {
	index := sort.Search(len(boundaries), func(i int) bool {
		return boundaries[i] > cell
	})
	if index == 0 {
		return boundaries[0]
	}
	return boundaries[index-1]
}

// cutStyledCellsExact returns the visual interval [left, right) and guarantees
// that the result occupies exactly right-left terminal cells.
//
// If a boundary crosses a wide grapheme, the unrenderable partial grapheme is
// replaced with spaces. This is required when an opaque overlay begins or ends
// in the second cell of CJK text or emoji: preserving the whole grapheme would
// shift the overlay, while dropping it without padding would shrink the row.
func cutStyledCellsExact(line string, left, right int) string {
	if left < 0 {
		left = 0
	}
	if right <= left {
		return ""
	}
	targetWidth := right - left
	parsed := parseStyledCellLine(line)
	lineWidth := parsed.width
	if left >= lineWidth {
		return strings.Repeat(" ", targetWidth)
	}

	contentRight := minInt(right, lineWidth)
	safeLeft := ceilStyledCellBoundary(parsed.boundaries, left)
	safeRight := floorStyledCellBoundary(parsed.boundaries, contentRight)
	if safeLeft > contentRight || safeRight < safeLeft {
		return strings.Repeat(" ", targetWidth)
	}

	leadingWidth := safeLeft - left
	contentWidth := safeRight - safeLeft
	trailingWidth := targetWidth - leadingWidth - contentWidth
	if trailingWidth < 0 {
		return strings.Repeat(" ", targetWidth)
	}

	var fragment string
	if contentWidth > 0 {
		fragment = renderStyledCellAtoms(parsed.atoms, safeLeft, safeRight)
	}
	return strings.Repeat(" ", leadingWidth) +
		fragment +
		strings.Repeat(" ", trailingWidth)
}

// composeStyledCellOverlay replaces one exact cell interval in base with an
// opaque overlay line while preserving the total row width.
func composeStyledCellOverlay(base, overlay string, left, width int) string {
	if width <= 0 {
		return ""
	}
	overlay = fitStyledCellLine(overlay, minInt(width, terminalCellWidth(overlay)))
	overlayWidth := terminalCellWidth(overlay)
	left = clampInt(left, 0, maxInt(0, width-overlayWidth))

	prefix := cutStyledCellsExact(base, 0, left)
	suffix := cutStyledCellsExact(base, left+overlayWidth, width)
	return fitStyledCellLine(prefix+overlay+suffix, width)
}
