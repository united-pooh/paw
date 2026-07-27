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

// fitStyledCellLine truncates and pads one styled line to an exact terminal
// cell width. It never splits a grapheme cluster.
func fitStyledCellLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if terminalCellWidth(line) > width {
		line = cutStyledCellsExact(line, 0, width)
	}
	if visible := terminalCellWidth(line); visible < width {
		line += strings.Repeat(" ", width-visible)
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

	for len(line) > 0 {
		var (
			sequence string
			width    int
			read     int
			newState = state
			control  = isTerminalControlByte(line[0])
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
		state = newState

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

func renderStyledCellAtoms(atoms []styledCellAtom, left, right int) string {
	var rendered strings.Builder
	for _, atom := range atoms {
		if atom.control {
			// Keeping controls in source order recreates the active SGR state at
			// the start of the slice and restores it after the selected text.
			rendered.WriteString(atom.text)
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
	return rendered.String()
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
