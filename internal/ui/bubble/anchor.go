package bubble

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type terminalCursorPosition struct {
	active       bool
	upFromBottom int
	column       int
}

type terminalCursorAnchor struct {
	mu         sync.Mutex
	pending    terminalCursorPosition
	hasPending bool
}

func newTerminalCursorAnchor() *terminalCursorAnchor {
	return &terminalCursorAnchor{}
}

func (a *terminalCursorAnchor) set(position terminalCursorPosition) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = position
	a.hasPending = true
}

func (a *terminalCursorAnchor) clear() {
	a.set(terminalCursorPosition{})
}

func (a *terminalCursorAnchor) consume() (terminalCursorPosition, bool) {
	if a == nil {
		return terminalCursorPosition{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.hasPending {
		return terminalCursorPosition{}, false
	}
	position := a.pending
	a.pending = terminalCursorPosition{}
	a.hasPending = false
	return position, true
}

type anchoredOutput struct {
	out    *os.File
	anchor *terminalCursorAnchor
	mu     sync.Mutex
	last   terminalCursorPosition
	moved  bool
}

var _ io.ReadWriteCloser = (*anchoredOutput)(nil)

func newAnchoredOutput(out *os.File, anchor *terminalCursorAnchor) *anchoredOutput {
	return &anchoredOutput{out: out, anchor: anchor}
}

func (w *anchoredOutput) Fd() uintptr {
	return w.out.Fd()
}

func (w *anchoredOutput) Read(p []byte) (int, error) {
	return w.out.Read(p)
}

func (w *anchoredOutput) Close() error {
	return nil
}

func (w *anchoredOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.moved {
		if _, err := w.out.Write([]byte(restoreTerminalCursorInvariant(w.last))); err != nil {
			return 0, err
		}
		w.moved = false
	}

	n, err := w.out.Write(p)
	if err != nil {
		return n, err
	}

	if position, ok := w.anchor.consume(); ok && position.active {
		if _, err := w.out.Write([]byte(moveTerminalCursorToAnchor(position))); err != nil {
			return n, err
		}
		w.last = position
		w.moved = true
	}
	return n, nil
}

func moveTerminalCursorToAnchor(position terminalCursorPosition) string {
	var builder strings.Builder
	if position.upFromBottom > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dA", position.upFromBottom))
	}
	if position.column > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dC", position.column))
	}
	return builder.String()
}

func restoreTerminalCursorInvariant(position terminalCursorPosition) string {
	var builder strings.Builder
	if position.upFromBottom > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dB", position.upFromBottom))
	}
	builder.WriteByte('\r')
	return builder.String()
}
