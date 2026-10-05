package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestDiffLineBackgroundRestored(t *testing.T) {
	newRGBTestModel(t)
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	out := renderToolDetailLinesWithHint([]string{"1 + │ added line", "2 - │ removed line", "3   │ context"}, 40, "")
	// Tokyo Night added/deleted lines retain their semantic RGB backgrounds.
	if !strings.Contains(out, "48;2;20;48;42") {
		t.Fatalf("added line missing green background: %q", out)
	}
	// Deleted line uses the same explicit theme.
	if !strings.Contains(out, "48;2;46;20;32") {
		t.Fatalf("deleted line missing red background: %q", out)
	}
}

// TestDiffLineBackgroundPersistsAcrossHighlight 保证行内语法高亮不会破坏 diff
// 行的背景：子样式渲染自带的尾部 reset（\x1b[0m）会清掉外层背景，因此每个
// token 段都必须自行恢复背景，背景需从行首延续到行尾。
func TestDiffLineBackgroundPersistsAcrossHighlight(t *testing.T) {
	newRGBTestModel(t)
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	out := renderToolDetailLinesWithHint([]string{
		"12 + │ return strings.TrimRight(line, \" \\t\\r\")",
		"13 - │ if strings.HasPrefix(line, \"//\") {",
		"14   │ context line without marker",
	}, 60, "foo.go")
	for _, line := range strings.Split(out, "\n") {
		if backgroundLostInLine(line) {
			t.Errorf("diff 行背景在行内中途丢失: %q", line)
		}
	}
}

// backgroundLostInLine 用 SGR 状态机模拟终端对转义序列的处理：跟踪当前背景
// 状态，若 reset（\x1b[0m）之后出现可视文本且没有恢复背景，则判定背景丢失。
func backgroundLostInLine(out string) bool {
	hasBg := false
	textSinceReset := false
	lost := false
	i := 0
	for i < len(out) {
		if out[i] == '\x1b' {
			j := i + 1
			for j < len(out) && out[j] != 'm' {
				j++
			}
			if j < len(out) {
				seq := strings.TrimPrefix(out[i+1:j], "[")
				switch {
				case seq == "0":
					hasBg = false
				case strings.Contains(seq, "48;"):
					hasBg = true
					textSinceReset = false
				}
				i = j + 1
				continue
			}
		}
		// 可视字符
		if !hasBg && out[i] != '\n' {
			if textSinceReset {
				lost = true
			}
			textSinceReset = true
		} else {
			textSinceReset = false
		}
		i++
	}
	return lost
}
