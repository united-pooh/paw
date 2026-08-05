package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func assertTerminalSequencesComplete(t *testing.T, text string) {
	t.Helper()
	state := byte(ansi.NormalState)
	for remaining := text; remaining != ""; {
		_, _, read, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if read <= 0 {
			t.Fatalf("terminal parser made no progress: state=%d raw=%q", state, remaining)
		}
		remaining = remaining[read:]
		state = newState
	}
	if state != ansi.NormalState {
		t.Fatalf("terminal sequence ended in parser state %d: %q", state, text)
	}
}

func TestWrapStyledCellLineKeepsTrueColorANSIAtomic(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Background(lipgloss.Color("#1f2334")).
		Padding(0, 1)
	input := style.Render("responses.go") + " / " +
		style.Render("openai_compatible_adapter.go") + " / " +
		style.Render("deepseek_adapter.go")
	lines := wrapStyledCellLine(input, 18)
	if len(lines) < 2 {
		t.Fatalf("wrapped lines=%d, want multiple lines: %q", len(lines), lines)
	}

	var plain strings.Builder
	for _, line := range lines {
		assertTerminalSequencesComplete(t, line)
		if width := terminalCellWidth(line); width > 18 {
			t.Fatalf("line width=%d, want <=18: %q", width, line)
		}
		plain.WriteString(ansi.Strip(line))
	}
	if got, want := plain.String(), ansi.Strip(input); got != want {
		t.Fatalf("plain wrapped text=%q, want %q", got, want)
	}
	for _, leaked := range []string{";245;", "48;2;31;35;52m", "[38;5"} {
		if strings.Contains(plain.String(), leaked) {
			t.Fatalf("ANSI payload leaked as visible text %q: %q", leaked, plain.String())
		}
	}
}

func TestWrapStyledCellLineKeepsOSC8AndUnicodeAtomic(t *testing.T) {
	input := ansi.SetHyperlink("https://example.com") + "中👩‍💻e\u0301हिन्दीالعربية" + ansi.ResetHyperlink()
	lines := wrapStyledCellLine(input, 4)
	var plain strings.Builder
	for _, line := range lines {
		assertTerminalSequencesComplete(t, line)
		if width := terminalCellWidth(line); width > 4 {
			t.Fatalf("line width=%d, want <=4: %q", width, line)
		}
		plain.WriteString(ansi.Strip(line))
	}
	if got, want := plain.String(), ansi.Strip(input); got != want {
		t.Fatalf("plain wrapped text=%q, want %q", got, want)
	}
}

func TestWrapStyledCellLineUsesEllipsisForUnrenderableWideGrapheme(t *testing.T) {
	lines := wrapStyledCellLine("中A", 1)
	if got, want := lines, []string{"…", "A"}; !slicesEqual(got, want) {
		t.Fatalf("wrapped=%q, want %q", got, want)
	}
}

func TestWrapStyledCellLineDropsIncompleteANSI(t *testing.T) {
	for _, input := range []string{
		"safe\x1b[38;5",
		"safe\x1b]8;;https://example.com",
		"safe\x1b]8;;https://example.com\x1b",
	} {
		lines := wrapStyledCellLine(input, 80)
		if got := strings.Join(lines, ""); got != "safe" {
			t.Fatalf("wrapped malformed ANSI=%q, want safe", got)
		}
		assertTerminalSequencesComplete(t, strings.Join(lines, ""))
	}
}

func TestWrapStyledCellLineClosesOpenStylesAndLinksPerFragment(t *testing.T) {
	input := "\x1b[1;38;5;245;48;2;31;35;52m" + ansi.SetHyperlink("https://example.com") + "abcdef"
	lines := wrapStyledCellLine(input, 2)
	if len(lines) != 3 {
		t.Fatalf("wrapped lines=%d, want 3: %q", len(lines), lines)
	}
	for _, line := range lines {
		assertTerminalSequencesComplete(t, line)
		if !strings.Contains(line, ansi.ResetHyperlink()) || !strings.HasSuffix(line, ansi.ResetStyle) {
			t.Fatalf("fragment did not close OSC 8 and SGR state: %q", line)
		}
	}
}

func TestTruncateStyledCellsPreservesBoundariesAndTail(t *testing.T) {
	input := "\x1b[31m中ABC\x1b[0m"
	got := truncateStyledCells(input, 4, "…")
	assertTerminalSequencesComplete(t, got)
	if width := terminalCellWidth(got); width != 4 {
		t.Fatalf("width=%d, want 4: %q", width, got)
	}
	if plain := ansi.Strip(got); plain != "中A…" {
		t.Fatalf("plain=%q, want %q", plain, "中A…")
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestCutStyledCellsExactHandlesGraphemeBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		left  int
		right int
		want  string
	}{
		{
			name:  "left half of wide character",
			line:  "中ABCDE",
			left:  0,
			right: 1,
			want:  " ",
		},
		{
			name:  "right half followed by ascii",
			line:  "中ABCDE",
			left:  1,
			right: 3,
			want:  " A",
		},
		{
			name:  "ansi styled wide character",
			line:  "\x1b[31m中A\x1b[0m",
			left:  1,
			right: 3,
			want:  " A",
		},
		{
			name:  "promoted halfwidth grapheme partial",
			line:  "(ﾟABC",
			left:  0,
			right: 1,
			want:  " ",
		},
		{
			name:  "promoted halfwidth grapheme complete",
			line:  "(ﾟABC",
			left:  0,
			right: 2,
			want:  "(ﾟ",
		},
		{
			name:  "combining grapheme",
			line:  "e\u0301X",
			left:  0,
			right: 1,
			want:  "e\u0301",
		},
		{
			name:  "emoji zwj partial",
			line:  "👩‍💻X",
			left:  1,
			right: 3,
			want:  " X",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := cutStyledCellsExact(test.line, test.left, test.right)
			if width := terminalCellWidth(got); width != test.right-test.left {
				t.Fatalf("width=%d, want %d; raw=%q", width, test.right-test.left, got)
			}
			if plain := ansi.Strip(got); plain != test.want {
				t.Fatalf("plain=%q, want %q; raw=%q", plain, test.want, got)
			}
		})
	}
}

func TestCutStyledCellsExactAlwaysFillsRequestedInterval(t *testing.T) {
	line := "\x1b[31m中\x1b[0m(ﾟe\u0301👩‍💻Z"
	width := terminalCellWidth(line)
	for left := 0; left <= width; left++ {
		for right := left; right <= width; right++ {
			got := cutStyledCellsExact(line, left, right)
			if gotWidth := terminalCellWidth(got); gotWidth != right-left {
				t.Fatalf("[%d,%d) width=%d, want %d; raw=%q", left, right, gotWidth, right-left, got)
			}
		}
	}
}

func TestOpaqueOverlayClearsWideGraphemeAtItsCellBoundary(t *testing.T) {
	base := "\x1b[31m中\x1b[0mABCDE"
	got := placeOpaqueOverlay(base, "XXXXX", 7, 1, overlayAlignCenter)
	if width := terminalCellWidth(got); width != 7 {
		t.Fatalf("overlay width=%d, want 7; raw=%q", width, got)
	}
	if plain := ansi.Strip(got); plain != " XXXXXE" {
		t.Fatalf("overlay=%q, want %q; raw=%q", plain, " XXXXXE", got)
	}
	if strings.Contains(got, "\x1b[31mXXXXX") {
		t.Fatalf("overlay inherited base foreground style: %q", got)
	}
}

func TestTerminalGraphemeClustersUseTerminalCellWidths(t *testing.T) {
	var clusters []string
	for remaining := "हिन्दी"; remaining != ""; {
		cluster, _ := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		clusters = append(clusters, cluster)
	}
	if got, want := strings.Join(clusters, "|"), "हि|न्दी"; got != want {
		t.Fatalf("clusters=%q, want %q", got, want)
	}
	if got := terminalCellWidth(strings.Join(clusters, "")); got != 3 {
		t.Fatalf("terminal width=%d, want 3", got)
	}
}

func TestTerminalCellWidthMatchesGhosttyComplexScriptAdvance(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "arabic", text: "العربية", want: 7},
		{name: "devanagari", text: "हिन्दी", want: 3},
		{name: "thai", text: "ภาษาไทย", want: 7},
		{
			name: "mixed language line",
			text: "中文 English 日本語 한국어 Русский العربية हिन्दी ภาษาไทย",
			want: 54,
		},
		{name: "styled devanagari", text: "\x1b[31mहिन्दी\x1b[0m", want: 3},
		{name: "combining latin", text: "e\u0301", want: 1},
		{name: "spacing mark cluster", text: "(ﾟ", want: 2},
		{name: "emoji zwj", text: "👩‍💻", want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := terminalCellWidth(test.text); got != test.want {
				t.Fatalf("terminalCellWidth(%q)=%d, want %d", test.text, got, test.want)
			}
			if got := lipgloss.Width(test.text); got != test.want {
				t.Fatalf("lipgloss.Width(%q)=%d, want %d", test.text, got, test.want)
			}
		})
	}
}
