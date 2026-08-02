package bubble

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTerminalBackgroundSequenceUsesTrueColor(t *testing.T) {
	if got, want := terminalBackgroundSequence("#1a1b26"), "\x1b[48;2;26;27;38m"; got != want {
		t.Fatalf("terminalBackgroundSequence() = %q, want %q", got, want)
	}
}

func TestTerminalCursorColorAndResetSequences(t *testing.T) {
	if got, want := terminalCursorColorSequence("#7aa2f7"), "\x1b]12;#7aa2f7\x1b\\"; got != want {
		t.Fatalf("terminalCursorColorSequence() = %q, want %q", got, want)
	}
	if got, want := resetTerminalCursorColorSequence(), "\x1b]112\x1b\\"; got != want {
		t.Fatalf("resetTerminalCursorColorSequence() = %q, want %q", got, want)
	}
}

func TestTerminalVisualOnlySequenceNeverMovesCursor(t *testing.T) {
	sequence := terminalCursorVisualSequence(
		terminalCursorVisual{color: "#111111", visible: false},
		terminalCursorVisual{color: "#abcdef", visible: true},
	)
	for _, forbidden := range []string{"\r", "\x1b[1A", "\x1b[1B", "\x1b[1C", "\x1b[1D"} {
		if strings.Contains(sequence, forbidden) {
			t.Fatalf("visual-only sequence %q contains cursor movement %q", sequence, forbidden)
		}
	}
	if !strings.Contains(sequence, "\x1b]12;#abcdef\x1b\\") {
		t.Fatalf("visual-only sequence = %q, want color update", sequence)
	}
	if strings.Contains(sequence, terminalCursorShow) || strings.Contains(sequence, terminalCursorHide) {
		t.Fatalf("visual-only sequence = %q, must not blink terminal cursor", sequence)
	}
}

func TestTerminalInvalidColorsDoNotBecomeBlack(t *testing.T) {
	for _, color := range []string{"", "black", "#000", "#gg0000", "#12345678"} {
		if got := terminalBackgroundSequence(color); got != "" {
			t.Fatalf("terminalBackgroundSequence(%q) = %q, want empty", color, got)
		}
		if got := terminalCursorColorSequence(color); got != "" {
			t.Fatalf("terminalCursorColorSequence(%q) = %q, want empty", color, got)
		}
	}
}

func TestTerminalCursorActivationOrder(t *testing.T) {
	position := terminalCursorPosition{active: true, upFromBottom: 2, column: 7, background: "#1a1b26"}
	visual := terminalCursorVisual{color: "#7aa2f7", visible: true}
	got := activateTerminalCursor(position, visual)
	want := "\r\x1b[2A\x1b[7C" +
		"\x1b[48;2;26;27;38m" +
		"\x1b]12;#7aa2f7\x1b\\" +
		terminalCursorShow
	if got != want {
		t.Fatalf("activateTerminalCursor() = %q, want %q", got, want)
	}
}

func TestTerminalCursorRestoreIsCompleteAndIdempotent(t *testing.T) {
	got := restoreTerminalCursorState()
	for _, want := range []string{terminalCursorShow, "\x1b]112\x1b\\", terminalSGRReset} {
		if !strings.Contains(got, want) {
			t.Fatalf("restoreTerminalCursorState() = %q, want %q", got, want)
		}
	}
	if got != restoreTerminalCursorState() {
		t.Fatal("restoreTerminalCursorState() is not deterministic")
	}
}

func TestTerminalCursorAnimationUsesThemeEndpoints(t *testing.T) {
	anchor := newTerminalCursorAnchor()
	anchor.setAnimation(terminalCursorAnimation{background: "#1a1b26", bright: "#7aa2f7"})
	animation, ok := anchor.currentAnimation()
	if !ok {
		t.Fatal("cursor animation was not published")
	}
	if animation.background != "#1a1b26" || animation.bright != "#7aa2f7" {
		t.Fatalf("animation = %#v", animation)
	}
	colors := map[string]bool{}
	for _, offset := range []time.Duration{0, cursorCycleDuration / 6, cursorCycleDuration / 4, cursorCycleDuration / 3, cursorCycleDuration / 2} {
		intensity := cursorIntensityAt(offset)
		colors[interpolateHexColor(animation.background, animation.bright, intensity)] = true
	}
	if len(colors) < 2 {
		t.Fatalf("animation colors = %#v, want gradient", colors)
	}
}

func TestAnchoredOutputCloseRestoresTerminalStateOnce(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "cursor-close-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	output := newAnchoredOutput(file, newTerminalCursorAnchor())
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), restoreTerminalCursorState(); got != want {
		t.Fatalf("close output = %q, want one restore %q", got, want)
	}
}

func TestAnchoredOutputHidesCursorWhenInputAnchorIsSuspended(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "cursor-hidden-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	anchor := newTerminalCursorAnchor()
	output := newAnchoredOutput(file, anchor)
	defer output.Close()

	anchor.hide()
	if _, err := output.Write([]byte("modal frame")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "modal frame" + terminalCursorHide + resetTerminalCursorColorSequence() + terminalSGRReset
	if got := string(data); got != want {
		t.Fatalf("hidden cursor output = %q, want %q", got, want)
	}
	if strings.Contains(string(data), terminalCursorShow) {
		t.Fatalf("hidden cursor output unexpectedly shows cursor: %q", data)
	}
}
