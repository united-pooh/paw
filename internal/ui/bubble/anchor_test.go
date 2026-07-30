package bubble

import (
	"strings"
	"testing"
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
	if !strings.Contains(sequence, "\x1b]12;#abcdef\x1b\\") || !strings.Contains(sequence, terminalCursorShow) {
		t.Fatalf("visual-only sequence = %q, want color and visibility", sequence)
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
	want := terminalCursorHide +
		"\r\x1b[2A\x1b[7C" +
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
