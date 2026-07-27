package displaywidth

import (
	"slices"
	"strings"
	"testing"
)

func TestGhosttyCompatibleComplexScriptWidths(t *testing.T) {
	tests := map[string]int{
		"العربية": 7,
		"हिन्दी":  3,
		"ภาษาไทย": 7,
		"(ﾟ":      2,
		"👩‍💻":     2,
	}
	for text, want := range tests {
		if got := String(text); got != want {
			t.Errorf("String(%q)=%d, want %d", text, got, want)
		}
	}
}

func TestOptionsPreserveControlAndEastAsianSemantics(t *testing.T) {
	ansiOptions := Options{ControlSequences: true}
	if got := ansiOptions.String("\x1b[31mred\x1b[0m"); got != 3 {
		t.Fatalf("ANSI width=%d, want 3", got)
	}

	eastAsianOptions := Options{EastAsianWidth: true}
	if got := eastAsianOptions.String("·"); got != 2 {
		t.Fatalf("ambiguous East Asian width=%d, want 2", got)
	}
}

func TestGhosttyCompatibleIndicGraphemeWidths(t *testing.T) {
	// Ghostty's mode-2027 width rule promotes the Indic conjunct to two cells
	// because a later non-zero-width codepoint joins the same grapheme.
	clusters := StringGraphemes("हिन्दी")
	var values []string
	var widths []int
	for clusters.Next() {
		values = append(values, clusters.Value())
		widths = append(widths, clusters.Width())
	}
	if got, want := strings.Join(values, "|"), "हि|न्दी"; got != want {
		t.Fatalf("graphemes=%q, want %q", got, want)
	}
	if got, want := widths, []int{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("grapheme widths=%v, want %v", got, want)
	}
}

func TestPublicCompatibilityHelpers(t *testing.T) {
	if got := Rune(0xd800); got != 0 {
		t.Fatalf("surrogate rune width=%d, want 0", got)
	}
	if got := string(TruncateBytes([]byte("हिन्दी"), 2, []byte("…"))); got != "हि…" {
		t.Fatalf("TruncateBytes()=%q, want %q", got, "हि…")
	}

	options := Options{ControlSequences: true}
	got := options.TruncateString("\x1b[31mabcdef\x1b[0m", 4, "…")
	if options.String(got) != 4 || !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("ANSI truncation=%q width=%d, want width 4 with reset", got, options.String(got))
	}
}
