package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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
			name:  "half width grapheme",
			line:  "(ﾟABC",
			left:  0,
			right: 1,
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
