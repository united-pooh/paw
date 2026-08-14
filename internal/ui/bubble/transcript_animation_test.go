package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestAnimateStyledTranscriptTextNormalAndCompletionAreExact(t *testing.T) {
	input := "\x1b[31m中e\u0301👩‍💻\x1b[0m"
	line := transcriptAnimationLine{ID: 7, StartedAt: time.Unix(100, 0)}
	if got := animateStyledTranscriptText(input, transcriptAnimationModeChar, transcriptRenderEffectNormal, line, line.StartedAt, time.Second); got != input {
		t.Fatalf("normal changed input: %q", got)
	}
	for _, effect := range []transcriptRenderEffect{transcriptRenderEffectNoise, transcriptRenderEffectReveal} {
		got := animateStyledTranscriptText(input, transcriptAnimationModeChar, effect, line, line.StartedAt.Add(time.Second), time.Second)
		if got != input {
			t.Fatalf("%s completion=%q, want exact input %q", effect, got, input)
		}
	}
}

func TestAnimateStyledTranscriptTextRevealPreservesWidthAndNewlines(t *testing.T) {
	input := "\x1b[38;5;245m中A\x1b[0m\n👩‍💻e\u0301"
	line := transcriptAnimationLine{ID: 11, StartedAt: time.Unix(100, 0)}
	got := animateStyledTranscriptText(input, transcriptAnimationModeChar, transcriptRenderEffectReveal, line, line.StartedAt.Add(500*time.Millisecond), time.Second)
	if terminalCellWidth(got) != terminalCellWidth(input) {
		t.Fatalf("width=%d, want %d: %q", terminalCellWidth(got), terminalCellWidth(input), got)
	}
	if strings.Count(got, "\n") != strings.Count(input, "\n") {
		t.Fatalf("newlines=%d, want %d: %q", strings.Count(got, "\n"), strings.Count(input, "\n"), got)
	}
	if ansi.Strip(got) == ansi.Strip(input) {
		t.Fatal("mid-animation reveal unexpectedly equals final text")
	}
	assertTerminalSequencesComplete(t, got)
}

func TestAnimateStyledTranscriptTextNoiseIsDeterministicAndAnimated(t *testing.T) {
	input := "abcdef"
	line := transcriptAnimationLine{ID: 19, StartedAt: time.Unix(100, 0)}
	a := animateStyledTranscriptText(input, transcriptAnimationModeChar, transcriptRenderEffectNoise, line, line.StartedAt.Add(500*time.Millisecond), time.Second)
	b := animateStyledTranscriptText(input, transcriptAnimationModeChar, transcriptRenderEffectNoise, line, line.StartedAt.Add(500*time.Millisecond), time.Second)
	if a != b {
		t.Fatalf("noise is not deterministic: %q != %q", a, b)
	}
	if a == input {
		t.Fatal("mid-animation noise unexpectedly equals final text")
	}
	if terminalCellWidth(a) != terminalCellWidth(input) {
		t.Fatalf("noise width=%d, want %d", terminalCellWidth(a), terminalCellWidth(input))
	}
}

func TestAnimateStyledTranscriptTextLineModeKeepsGeometry(t *testing.T) {
	input := "\x1b[32mhello 中\x1b[0m"
	line := transcriptAnimationLine{ID: 23, StartedAt: time.Unix(100, 0)}
	got := animateStyledTranscriptText(input, transcriptAnimationModeLine, transcriptRenderEffectReveal, line, line.StartedAt.Add(500*time.Millisecond), time.Second)
	if terminalCellWidth(got) != terminalCellWidth(input) {
		t.Fatalf("width=%d, want %d", terminalCellWidth(got), terminalCellWidth(input))
	}
	if ansi.Strip(got) == ansi.Strip(input) {
		t.Fatal("line animation unexpectedly completed early")
	}
}
