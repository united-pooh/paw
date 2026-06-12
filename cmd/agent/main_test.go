package main

import (
	"bytes"
	"testing"
)

func TestClearTerminalWindowWritesClearAndScrollbackSequence(t *testing.T) {
	var output bytes.Buffer

	clearTerminalWindow(&output)

	if got, want := output.String(), "\x1b[H\x1b[2J\x1b[3J"; got != want {
		t.Fatalf("clearTerminalWindow() wrote %q, want %q", got, want)
	}
}
