package bubble

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

func inputVisibleLineCount(input textarea.Model) int {
	lineCount := maxInt(1, strings.Count(input.Value(), "\n")+1)
	return minInt(inputMaxVisibleLines, lineCount)
}

func splitContinuation(line string) (bool, string) {
	if !strings.HasSuffix(line, `\`) {
		return false, line
	}
	return true, strings.TrimSpace(strings.TrimSuffix(line, `\`))
}

func shellCommandFromBang(line string) (string, bool) {
	if !strings.HasPrefix(line, "!") {
		return "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(line, "!"))
	return command, command != ""
}

func hasBangPrefix(value string) bool {
	return strings.HasPrefix(value, "!")
}

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
