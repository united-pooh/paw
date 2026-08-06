// 本文件提供输入框相关的轻量解析和按键分类辅助函数。
package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	inputPasteFoldThreshold  = 6
	inputPasteFoldHeadLines  = 3
	inputPasteFoldTailLines  = 2
	inputPasteFoldMarkerLine = "... %d lines folded ..."
)

func logicalInputLineCount(value string) int {
	if value == "" {
		return 1
	}
	return len(strings.Split(value, "\n"))
}

func inputPasteFoldable(value string) bool {
	return logicalInputLineCount(value) > inputPasteFoldThreshold
}

// inputWrappedVisualLineCount 计算 value 在指定宽度下 soft-wrap 后的可视行数
// （含截断到 width 的单行，保证任何输入都不会让布局超过输入框高度）。
func inputWrappedVisualLineCount(value string, width int) int {
	return maxInt(1, len(wrapStyledCellText(value, maxInt(1, width))))
}

// inputPasteFoldableWithWidth 按可视行数（宽度感知）判定是否可折叠：
// 逻辑行数超过阈值，或任何输入在给定宽度下 soft-wrap 后超过输入框上限，
// 都会被折叠，避免长单行软换行把输入框撑爆。
func inputPasteFoldableWithWidth(value string, width int) bool {
	if inputPasteFoldable(value) {
		return true
	}
	return inputWrappedVisualLineCount(value, width) > inputMaxVisibleLines
}

// visualRowLines 把 value 按 width 宽度切分为 soft-wrap 后的可视行（保留尾行
// 原样，不截断），与 projectInput 的换行语义一致（按 grapheme cluster 换行）。
func visualRowLines(value string, width int) []string {
	return wrapStyledCellText(value, maxInt(1, width))
}

// inputPasteFoldProjectionWithWidth 折叠投影：优先按逻辑行折叠（与旧行为一致）；
// 当只有单条长行（逻辑行少但 soft-wrap 后行数超上限）时，按可视行折叠并插入
// 折叠标记。
func inputPasteFoldProjectionWithWidth(value string, width int) ([]string, int, bool) {
	if inputPasteFoldable(value) {
		return inputPasteFoldProjection(value)
	}
	rows := visualRowLines(value, width)
	if len(rows) <= inputMaxVisibleLines {
		return strings.Split(value, "\n"), 0, false
	}
	head := minInt(inputPasteFoldHeadLines, len(rows))
	tail := minInt(inputPasteFoldTailLines, maxInt(0, len(rows)-head))
	hidden := len(rows) - head - tail
	if hidden <= 0 {
		return rows, 0, false
	}
	projected := make([]string, 0, head+1+tail)
	projected = append(projected, rows[:head]...)
	projected = append(projected, fmt.Sprintf(inputPasteFoldMarkerLine, hidden))
	projected = append(projected, rows[len(rows)-tail:]...)
	return projected, hidden, true
}

// inputPasteFoldHiddenRangeWithWidth 返回折叠隐藏的闭区间。逻辑行折叠按逻辑行
// 区间；单条长行折叠按可视行区间。
func inputPasteFoldHiddenRangeWithWidth(value string, width int) (int, int, bool) {
	if inputPasteFoldable(value) {
		return inputPasteFoldHiddenRange(value)
	}
	rows := visualRowLines(value, width)
	if len(rows) <= inputMaxVisibleLines {
		return 0, 0, false
	}
	start := minInt(inputPasteFoldHeadLines, len(rows))
	end := maxInt(start, len(rows)-inputPasteFoldTailLines)
	return start, end, end > start
}

// inputCursorInPasteFoldHiddenRangeWithWidth 判断光标是否位于折叠隐藏区间。
// 逻辑行折叠用 input.Line()；单条长行折叠用当前逻辑行内的可视行偏移 RowOffset。
func inputCursorInPasteFoldHiddenRangeWithWidth(input textarea.Model, width int) bool {
	value := input.Value()
	if inputPasteFoldable(value) {
		return inputCursorInPasteFoldHiddenRange(input)
	}
	start, end, ok := inputPasteFoldHiddenRangeWithWidth(value, width)
	if !ok {
		return false
	}
	row := input.LineInfo().RowOffset
	return row >= start && row < end
}

func inputPasteFoldProjection(value string) ([]string, int, bool) {
	lines := strings.Split(value, "\n")
	if len(lines) <= inputPasteFoldThreshold {
		return lines, 0, false
	}
	head := minInt(inputPasteFoldHeadLines, len(lines))
	tail := minInt(inputPasteFoldTailLines, maxInt(0, len(lines)-head))
	hidden := len(lines) - head - tail
	if hidden <= 0 {
		return lines, 0, false
	}
	projected := make([]string, 0, head+1+tail)
	projected = append(projected, lines[:head]...)
	projected = append(projected, fmt.Sprintf(inputPasteFoldMarkerLine, hidden))
	projected = append(projected, lines[len(lines)-tail:]...)
	return projected, hidden, true
}

func inputPasteFoldHiddenRange(value string) (int, int, bool) {
	lines := logicalInputLineCount(value)
	if lines <= inputPasteFoldThreshold {
		return 0, 0, false
	}
	start := minInt(inputPasteFoldHeadLines, lines)
	end := maxInt(start, lines-inputPasteFoldTailLines)
	return start, end, end > start
}

func inputCursorInPasteFoldHiddenRange(input textarea.Model) bool {
	start, end, ok := inputPasteFoldHiddenRange(input.Value())
	return ok && input.Line() >= start && input.Line() < end
}

func inputTextMutationLooksLikeMultilinePaste(msg tea.Msg, beforeValue, afterValue string) bool {
	if beforeValue == afterValue {
		return false
	}
	beforeLines := logicalInputLineCount(beforeValue)
	afterLines := logicalInputLineCount(afterValue)
	if afterLines-beforeLines > 1 {
		return true
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	return ok && keyMsg.Type == tea.KeyRunes && strings.Contains(string(keyMsg.Runes), "\n")
}

// splitContinuation 解析以反斜杠结尾的续行输入。
func splitContinuation(line string) (bool, string) {
	if !strings.HasSuffix(line, `\`) {
		return false, line
	}
	return true, strings.TrimSpace(strings.TrimSuffix(line, `\`))
}

// shellCommandFromBang 解析一次性 !<command> 终端命令。
func shellCommandFromBang(line string) (string, bool) {
	if !strings.HasPrefix(line, "!") {
		return "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(line, "!"))
	return command, command != ""
}

// hasBangPrefix 判断当前输入是否以感叹号开头，用于终端模式预览。
func hasBangPrefix(value string) bool {
	return strings.HasPrefix(value, "!")
}

// isTextEditingKey 判断消息是否属于会改变 textarea 内容的编辑按键。
func isTextEditingKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	switch keyMsg.Type {
	case tea.KeyRunes, tea.KeyBackspace, tea.KeyDelete, tea.KeyEnter, tea.KeyCtrlJ, tea.KeyCtrlU, tea.KeyCtrlK, tea.KeyCtrlW:
		return true
	default:
		return keyMsg.String() == "alt+enter" || keyMsg.String() == "shift+enter"
	}
}

// isRawMouseEscapeKey detects SGR mouse reports that occasionally arrive as
// printable text instead of tea.MouseMsg, e.g. "<64;59;25M[<64;59;25M".
func isRawMouseEscapeKey(msg tea.KeyMsg) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return false
	}
	text := string(msg.Runes)
	i := 0
	seen := false
	for i < len(text) {
		for i < len(text) && text[i] == '[' {
			i++
		}
		if i >= len(text) || text[i] != '<' {
			return false
		}
		i++
		for field := 0; field < 3; field++ {
			start := i
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
			}
			if start == i {
				return false
			}
			if field < 2 {
				if i >= len(text) || text[i] != ';' {
					return false
				}
				i++
			}
		}
		if i >= len(text) || (text[i] != 'm' && text[i] != 'M') {
			return false
		}
		i++
		seen = true
	}
	return seen
}

// isRawMouseEscapePrefix reports whether text can still become one or more
// SGR mouse reports. Bubble Tea may expose a short read as KeyRunes before the
// complete ESC[<...M sequence is available, so the prefix must be held until
// the next key message arrives.
func isRawMouseEscapePrefix(text string) bool {
	if text == "" {
		return false
	}

	i := 0
	for {
		for i < len(text) && text[i] == '[' {
			i++
		}
		if i == len(text) {
			return true
		}
		if text[i] != '<' {
			return false
		}
		i++

		for field := 0; field < 3; field++ {
			start := i
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
			}
			if start == i {
				return i == len(text)
			}
			if field < 2 {
				if i == len(text) {
					return true
				}
				if text[i] != ';' {
					return false
				}
				i++
				if i == len(text) {
					return true
				}
			}
		}
		if i == len(text) {
			return true
		}
		if text[i] != 'm' && text[i] != 'M' {
			return false
		}
		i++
		// A trailing run of '[' is a valid prefix for the next report;
		// it is consumed at the top of the next iteration.
	}
}

// flushRawMouseEscapePending replays a held candidate that turned out to be
// ordinary user text. It is inserted before the current key is processed.
func (m *appModel) flushRawMouseEscapePending() {
	if m == nil || m.rawMouseEscapePending == "" {
		return
	}
	text := m.rawMouseEscapePending
	m.rawMouseEscapePending = ""
	beforeText := m.input.Value()
	beforeCursor := textareaAbsoluteCursor(m.input)
	beforeTokens := cloneInputTokens(m.inputTokens)
	m.input.InsertString(text)
	afterText := m.input.Value()
	afterCursor := textareaAbsoluteCursor(m.input)
	if beforeText != afterText {
		m.reconcileInputTokenEdit(beforeText, afterText, beforeCursor, afterCursor, beforeTokens)
	}
}

// clearRawMouseEscapePending drops a held prefix without replaying it as text.
// Mouse fragments are always followed by more mouse input, so a held prefix
// that gets interrupted by an unrelated event is almost certainly a leaked
// fragment rather than real typing.
func (m *appModel) clearRawMouseEscapePending() {
	if m == nil {
		return
	}
	m.rawMouseEscapePending = ""
}

// filterRawMouseEscapeKey hides complete or fragmented SGR mouse reports
// before they reach the textarea.
//
// Defense-in-depth: escCoalescingReader (bubble.go 的 WithInput) 已在字节进入
// BubbleTea 之前把被读边界切断的 \x1b[<...M 拼合成完整序列，正常情况下泄漏
// 片段不再到达此处。本过滤器作为第二道防线，兜底处理 reader 在极端情况下
// （如 SSH/tmux 转发延迟、peek 漏网）仍可能漏过的 [ 或 [​<...M 片段。
//
// A leaked '[' (the second byte of an ESC[<...M SGR mouse report, with the
// ESC stripped by a short read) is held as a prefix. Repeated '[' fragments
// accumulate into a held prefix run, so a trackpad-scroll burst of '[' bytes
// never reaches the textarea even when the fragments arrive with irregular
// gaps. The run is resolved when the next byte arrives: '<' or another '['
// extends the held prefix, a complete '<...M' report is discarded, and
// ordinary typing replays the held prefix as text.
func (m *appModel) filterRawMouseEscapeKey(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if m == nil {
		return msg, false
	}
	if msg.Type != tea.KeyRunes || msg.Paste || len(msg.Runes) == 0 {
		m.clearRawMouseEscapePending()
		return msg, false
	}
	// Alt+key 正常是合法按键（ESC 前缀修饰），跳过过滤。但鼠标短读会把
	// "\x1b["（ESC+bracket，载荷未到）解析成 Alt+`[` KeyRunes——这正是 [[[[ 泄漏
	// 的来源。Alt 且 runes 以 '[' 开头时，仍走鼠标片段过滤。
	if msg.Alt && msg.Runes[0] != '[' {
		m.clearRawMouseEscapePending()
		return msg, false
	}

	text := string(msg.Runes)
	if pending := m.rawMouseEscapePending; pending != "" {
		candidateText := pending + text
		candidate := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(candidateText)}
		if isRawMouseEscapeKey(candidate) {
			m.clearRawMouseEscapePending()
			return msg, true
		}
		if isRawMouseEscapePrefix(candidateText) {
			m.rawMouseEscapePending = candidateText
			return msg, true
		}
		// candidate rejected. If the new text could itself open a fresh
		// mouse report, drop the held prefix without flushing and
		// re-evaluate the text standalone; otherwise the text is ordinary
		// typing and the held prefix is replayed.
		if isRawMouseEscapePrefix(text) || isRawMouseEscapeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}) {
			m.clearRawMouseEscapePending()
		} else {
			m.flushRawMouseEscapePending()
		}
	}

	candidate := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}
	if isRawMouseEscapeKey(candidate) {
		return msg, true
	}
	if isRawMouseEscapePrefix(text) {
		m.rawMouseEscapePending = text
		return msg, true
	}
	return msg, false
}
