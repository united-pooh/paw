// 本文件提供输入框相关的轻量解析和按键分类辅助函数。
package bubble

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

// inputVisibleLineCount 计算 textarea 当前需要展示的可视行数。
func inputVisibleLineCount(input textarea.Model) int {
	lineCount := maxInt(1, strings.Count(input.Value(), "\n")+1)
	return minInt(inputMaxVisibleLines, lineCount)
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
