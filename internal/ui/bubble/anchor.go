// 本文件实现终端真实光标锚点，避免 Bubble Tea 重绘后输入法光标跑到屏幕角落。
package bubble

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	terminalCursorShow = "\x1b[?25h"
	terminalCursorHide = "\x1b[?25l"
	terminalSGRReset   = "\x1b[0m"
	terminalOSCST      = "\x1b\\"
)

// terminalCursorPosition 描述一次重绘后真实终端光标应停留的位置。
type terminalCursorPosition struct {
	active       bool
	upFromBottom int
	column       int
	background   string
}

// terminalCursorVisual 描述不改变坐标的真实终端光标视觉状态。
type terminalCursorVisual struct {
	color   string
	visible bool
}

// terminalCursorAnimation 保存真实光标渐变的主题端点。输出层独立计时，
// 因此空闲输入时无需触发 Bubble Tea 整帧重绘。
type terminalCursorAnimation struct {
	background string
	bright     string
}

// terminalCursorAnchor 在线程安全的容器中分别保存位置和视觉状态。
type terminalCursorAnchor struct {
	mu           sync.Mutex
	pending      terminalCursorPosition
	hasPending   bool
	visual       terminalCursorVisual
	hasVisual    bool
	animation    terminalCursorAnimation
	hasAnimation bool
	visualWake   chan struct{}
}

// newTerminalCursorAnchor 创建一个空的终端光标锚点容器。
func newTerminalCursorAnchor() *terminalCursorAnchor {
	return &terminalCursorAnchor{visualWake: make(chan struct{}, 1)}
}

// set 保存下一次帧输出后需要应用的光标位置。
func (a *terminalCursorAnchor) set(position terminalCursorPosition) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.pending = position
	a.hasPending = true
	a.mu.Unlock()
}

// setVisual 发布只影响颜色和可见性的状态。通知是合并的，消费者总读取最新值。
func (a *terminalCursorAnchor) setVisual(visual terminalCursorVisual) {
	if a == nil {
		return
	}
	a.mu.Lock()
	changed := !a.hasVisual || a.visual != visual
	a.visual = visual
	a.hasVisual = true
	a.mu.Unlock()
	if !changed {
		return
	}
	select {
	case a.visualWake <- struct{}{}:
	default:
	}
}

func (a *terminalCursorAnchor) setAnimation(animation terminalCursorAnimation) {
	if a == nil {
		return
	}
	background, backgroundOK := normalizeTerminalHexColor(animation.background)
	bright, brightOK := normalizeTerminalHexColor(animation.bright)
	if !backgroundOK || !brightOK {
		return
	}
	a.mu.Lock()
	a.animation = terminalCursorAnimation{background: background, bright: bright}
	a.hasAnimation = true
	a.mu.Unlock()
}

func (a *terminalCursorAnchor) currentAnimation() (terminalCursorAnimation, bool) {
	if a == nil {
		return terminalCursorAnimation{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.animation, a.hasAnimation
}

// clear 请求下一次输出后恢复终端默认光标状态。
func (a *terminalCursorAnchor) clear() {
	a.set(terminalCursorPosition{})
}

// consume 取走并清空待应用的位置状态。
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

func (a *terminalCursorAnchor) currentVisual() (terminalCursorVisual, bool) {
	if a == nil {
		return terminalCursorVisual{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.visual, a.hasVisual
}

// anchoredOutput 包装 Bubble Tea 输出流，在每次绘制后修正真实终端光标。
type anchoredOutput struct {
	out    *os.File
	anchor *terminalCursorAnchor
	mu     sync.Mutex
	last   terminalCursorPosition
	visual terminalCursorVisual
	moved  bool
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

// 确保 anchoredOutput 满足 Bubble Tea 输出所需的读写关闭接口。
var _ io.ReadWriteCloser = (*anchoredOutput)(nil)

// newAnchoredOutput 创建一个会在写入后应用光标锚点的输出包装器。
func newAnchoredOutput(out *os.File, anchor *terminalCursorAnchor) *anchoredOutput {
	w := &anchoredOutput{
		out:    out,
		anchor: anchor,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.forwardVisualUpdates()
	return w
}

func (w *anchoredOutput) forwardVisualUpdates() {
	defer close(w.done)
	if w == nil || w.anchor == nil {
		return
	}
	ticker := time.NewTicker(cursorFrameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.anchor.visualWake:
			visual, ok := w.anchor.currentVisual()
			if ok {
				_ = w.applyVisualOnly(visual)
			}
		case now := <-ticker.C:
			animation, ok := w.anchor.currentAnimation()
			if !ok {
				continue
			}
			intensity := cursorIntensityAt(cursorCycleOffset(now))
			_ = w.applyVisualOnly(terminalCursorVisual{
				color:   interpolateHexColor(animation.background, animation.bright, intensity),
				visible: intensity > cursorHiddenThreshold,
			})
		case <-w.stop:
			return
		}
	}
}

// Fd 暴露底层文件描述符，供 Bubble Tea 判断终端能力。
func (w *anchoredOutput) Fd() uintptr {
	return w.out.Fd()
}

// Read 委托到底层终端输出文件。
func (w *anchoredOutput) Read(p []byte) (int, error) {
	return w.out.Read(p)
}

// Close 恢复终端光标状态，但不关闭标准输出。该操作可重复调用。
func (w *anchoredOutput) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.stop)
	var err error
	if w.out != nil {
		_, err = w.out.Write([]byte(restoreTerminalCursorState()))
	}
	w.moved = false
	w.mu.Unlock()
	<-w.done
	return err
}

// Write 写入 Bubble Tea 帧内容，并在帧末把真实光标移动到输入框位置。
func (w *anchoredOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	if w.moved {
		if _, err := w.out.Write([]byte(terminalCursorHide + restoreTerminalCursorInvariant(w.last) + terminalSGRReset)); err != nil {
			return 0, err
		}
		w.moved = false
	}

	n, err := w.out.Write(p)
	if err != nil {
		return n, err
	}

	if position, ok := w.anchor.consume(); ok {
		if !position.active {
			if _, err := w.out.Write([]byte(restoreTerminalCursorState())); err != nil {
				return n, err
			}
			w.last = terminalCursorPosition{}
			w.visual = terminalCursorVisual{}
			return n, nil
		}
		visual, _ := w.anchor.currentVisual()
		if _, err := w.out.Write([]byte(activateTerminalCursor(position, visual))); err != nil {
			return n, err
		}
		w.last = position
		w.visual = visual
		w.moved = true
	}
	return n, nil
}

func (w *anchoredOutput) applyVisualOnly(visual terminalCursorVisual) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || !w.moved || w.out == nil || visual == w.visual {
		return nil
	}
	sequence := terminalCursorVisualSequence(w.visual, visual)
	if sequence == "" {
		w.visual = visual
		return nil
	}
	_, err := w.out.Write([]byte(sequence))
	if err == nil {
		w.visual = visual
	}
	return err
}

func normalizeTerminalHexColor(color string) (string, bool) {
	color = strings.ToLower(strings.TrimSpace(color))
	if len(color) != 7 || color[0] != '#' {
		return "", false
	}
	for _, ch := range color[1:] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return "", false
		}
	}
	return color, true
}

func terminalBackgroundSequence(color string) string {
	color, ok := normalizeTerminalHexColor(color)
	if !ok {
		return ""
	}
	r, g, b := parseHexColor(color)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func terminalCursorColorSequence(color string) string {
	color, ok := normalizeTerminalHexColor(color)
	if !ok {
		return ""
	}
	return "\x1b]12;" + color + terminalOSCST
}

func resetTerminalCursorColorSequence() string { return "\x1b]112" + terminalOSCST }

func terminalCursorVisibilitySequence(visible bool) string {
	if visible {
		return terminalCursorShow
	}
	return terminalCursorHide
}

func terminalCursorVisualSequence(previous, next terminalCursorVisual) string {
	var out strings.Builder
	if previous.color != next.color {
		out.WriteString(terminalCursorColorSequence(next.color))
	}
	if previous.visible != next.visible {
		out.WriteString(terminalCursorVisibilitySequence(next.visible))
	}
	return out.String()
}

func activateTerminalCursor(position terminalCursorPosition, visual terminalCursorVisual) string {
	return terminalCursorHide +
		moveTerminalCursorToAnchor(position) +
		terminalBackgroundSequence(position.background) +
		terminalCursorColorSequence(visual.color) +
		terminalCursorVisibilitySequence(visual.visible)
}

func restoreTerminalCursorState() string {
	return terminalCursorShow + resetTerminalCursorColorSequence() + terminalSGRReset
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
