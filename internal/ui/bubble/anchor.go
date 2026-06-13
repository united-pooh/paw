// 本文件实现终端真实光标锚点，避免 Bubble Tea 重绘后输入法光标跑到屏幕角落。
package bubble

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// terminalCursorPosition 描述一次重绘后真实终端光标应停留的位置。
type terminalCursorPosition struct {
	active       bool
	upFromBottom int
	column       int
}

// terminalCursorAnchor 在线程安全的容器中保存下一帧要应用的光标位置。
type terminalCursorAnchor struct {
	mu         sync.Mutex
	pending    terminalCursorPosition
	hasPending bool
}

// newTerminalCursorAnchor 创建一个空的终端光标锚点容器。
func newTerminalCursorAnchor() *terminalCursorAnchor {
	return &terminalCursorAnchor{}
}

// set 保存下一次输出写入后需要应用的光标位置。
func (a *terminalCursorAnchor) set(position terminalCursorPosition) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = position
	a.hasPending = true
}

// clear 请求下一次输出后清除光标锚点。
func (a *terminalCursorAnchor) clear() {
	a.set(terminalCursorPosition{})
}

// consume 取走并清空待应用的光标位置。
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

// anchoredOutput 包装 Bubble Tea 输出流，在每次绘制后修正真实终端光标。
type anchoredOutput struct {
	out    *os.File
	anchor *terminalCursorAnchor
	mu     sync.Mutex
	last   terminalCursorPosition
	moved  bool
}

// 确保 anchoredOutput 满足 Bubble Tea 输出所需的读写关闭接口。
var _ io.ReadWriteCloser = (*anchoredOutput)(nil)

// newAnchoredOutput 创建一个会在写入后应用光标锚点的输出包装器。
func newAnchoredOutput(out *os.File, anchor *terminalCursorAnchor) *anchoredOutput {
	return &anchoredOutput{out: out, anchor: anchor}
}

// Fd 暴露底层文件描述符，供 Bubble Tea 判断终端能力。
func (w *anchoredOutput) Fd() uintptr {
	return w.out.Fd()
}

// Read 委托到底层终端输出文件。
func (w *anchoredOutput) Read(p []byte) (int, error) {
	return w.out.Read(p)
}

// Close 不关闭标准输出，只满足接口要求。
func (w *anchoredOutput) Close() error {
	return nil
}

// Write 写入 Bubble Tea 帧内容，并在帧末把真实光标移动到输入框位置。
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

// moveTerminalCursorToAnchor 生成从帧底部移动到输入光标位置的 ANSI 序列。
func moveTerminalCursorToAnchor(position terminalCursorPosition) string {
	var builder strings.Builder
	builder.WriteByte('\r')
	if position.upFromBottom > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dA", position.upFromBottom))
	}
	if position.column > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dC", position.column))
	}
	return builder.String()
}

// restoreTerminalCursorInvariant 生成恢复到帧底部行首的 ANSI 序列，确保下一帧从稳定位置绘制。
func restoreTerminalCursorInvariant(position terminalCursorPosition) string {
	var builder strings.Builder
	if position.upFromBottom > 0 {
		builder.WriteString(fmt.Sprintf("\x1b[%dB", position.upFromBottom))
	}
	builder.WriteByte('\r')
	return builder.String()
}
