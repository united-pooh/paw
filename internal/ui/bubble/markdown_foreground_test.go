package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// foregroundLostInLine 用 SGR 状态机模拟终端：若 reset（\x1b[0m）之后出现
// 非空白的可视字符且其间没有恢复任何前景色（\x1b[38;...m），则判定前景色丢失。
// 行尾 lipgloss 的 padding 空格无色透明，不视为丢失。
func foregroundLostInLine(out string) bool {
	hasFG := false
	i := 0
	for i < len(out) {
		if out[i] == '\x1b' {
			j := i + 1
			for j < len(out) && out[j] != 'm' {
				j++
			}
			if j < len(out) {
				seq := strings.TrimPrefix(out[i+1:j], "[")
				if seq == "0" || seq == "" {
					hasFG = false
				} else if strings.Contains(seq, "38") {
					hasFG = true
				}
				i = j + 1
				continue
			}
		}
		if out[i] != '\n' && out[i] != ' ' && !hasFG {
			return true
		}
		i++
	}
	return false
}

func TestMarkdownInlineStyleRestoresForeground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previousProfile)
	lipgloss.SetColorProfile(termenv.TrueColor)

	for _, input := range []string{
		"前文 **加粗** 后文",
		"前文 `代码` tail",
		"前文 *italic* tail",
		"前文 ==highlight== tail",
		"**加粗** 以及 **又来** 结尾",
		"a `code` b **bold** c",
	} {
		out := renderMarkdown(input, 60)
		if foregroundLostInLine(out) {
			t.Errorf("行内样式之后前景色丢失 input=%q: %q", input, out)
		}
	}
}

func TestMarkdownInlineStyleRestoresForegroundQuoteAndHeading(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previousProfile)
	lipgloss.SetColorProfile(termenv.TrueColor)

	for _, input := range []string{
		"> **加粗** 后文",
		"# **标题** 后文",
		"- **加粗** 后文",
	} {
		out := renderMarkdown(input, 60)
		if foregroundLostInLine(out) {
			t.Errorf("行内样式之后前景色丢失 input=%q: %q", input, out)
		}
	}
}
