// 本文件提供输入框相关的轻量解析和按键分类辅助函数。
package bubble

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	inputPasteFoldThreshold    = 6
	inputPasteFoldHeadLines    = 3
	inputPasteFoldTailLines    = 2
	inputPasteFoldMarkerLine   = "... %d lines folded ..."
	rawMouseBracketBurstWindow = 75 * time.Millisecond
)

// inputVisibleLineCount 计算 textarea 当前需要展示的可视行数。
func inputVisibleLineCount(input textarea.Model) int {
	lineCount := wrappedInputLineCount(input.Value(), input.Width())
	return minInt(inputMaxVisibleLines, maxInt(inputMinVisibleLines, lineCount))
}

func wrappedInputLineCount(value string, width int) int {
	width = maxInt(1, width)
	if value == "" {
		return 1
	}
	total := 0
	for _, line := range strings.Split(value, "\n") {
		lineWidth := terminalCellWidth(line)
		total += maxInt(1, (lineWidth+width-1)/width)
	}
	return maxInt(1, total)
}

func logicalInputLineCount(value string) int {
	if value == "" {
		return 1
	}
	return len(strings.Split(value, "\n"))
}

func inputPasteFoldable(value string) bool {
	return logicalInputLineCount(value) > inputPasteFoldThreshold
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
		if text[i] == '[' {
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
		if i < len(text) && text[i] == '[' {
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
		if i == len(text) {
			return false
		}
		if text[i] != '[' {
			return false
		}
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
	m.rawMouseEscapePendingAt = time.Time{}
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

// filterRawMouseEscapeKey hides complete or fragmented SGR mouse reports
// before they reach the textarea. When a held prefix turns out to be normal
// text, it is replayed before the current KeyMsg unchanged.
func (m *appModel) filterRawMouseEscapeKey(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if m == nil {
		return msg, false
	}
	if msg.Type != tea.KeyRunes || msg.Paste || msg.Alt || len(msg.Runes) == 0 {
		m.flushRawMouseEscapePending()
		m.rawMouseEscapeBracketBurst = false
		return msg, false
	}

	text := string(msg.Runes)
	if m.rawMouseEscapeBracketBurst {
		if text == "[" {
			return msg, true
		}
		m.rawMouseEscapeBracketBurst = false
	}
	pending := m.rawMouseEscapePending
	if pending != "" {
		if pending == "[" && text == "[" &&
			!m.rawMouseEscapePendingAt.IsZero() && time.Since(m.rawMouseEscapePendingAt) <= rawMouseBracketBurstWindow {
			m.rawMouseEscapePending = ""
			m.rawMouseEscapePendingAt = time.Time{}
			m.rawMouseEscapeBracketBurst = true
			return msg, true
		}
		candidateText := pending + text
		candidate := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(candidateText)}
		if isRawMouseEscapeKey(candidate) || isRawMouseEscapePrefix(candidateText) {
			m.rawMouseEscapePending = candidateText
			if isRawMouseEscapeKey(candidate) {
				m.rawMouseEscapePending = ""
				m.rawMouseEscapePendingAt = time.Time{}
			} else {
				m.rawMouseEscapePendingAt = time.Now()
			}
			return msg, true
		}
		m.flushRawMouseEscapePending()
	}

	candidate := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}
	if isRawMouseEscapeKey(candidate) {
		return msg, true
	}
	if isRawMouseEscapePrefix(text) {
		m.rawMouseEscapePending = text
		m.rawMouseEscapePendingAt = time.Now()
		return msg, true
	}
	return msg, false
}
