package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

// 连续引用行合并为一个引用块：每行一个 │ 边栏，行间无空白填充行。
// 回归：lipgloss .Border() 曾同时启用四个边，top/bottom 无字符导致每个
// 引用行上下各多渲染一行空白（引用块变成双倍行距）。
func TestMarkdownQuoteLinesMergeWithoutStrayBlanks(t *testing.T) {
	out := ansi.Strip(renderMarkdown("> line a\n> line b\n> line c", 60))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("quote render = %d lines, want exactly 3 (one rail row per quote line):\n%s", len(lines), out)
	}
	for i, want := range []string{"line a", "line b", "line c"} {
		if !strings.Contains(lines[i], "│") || !strings.Contains(lines[i], want) {
			t.Fatalf("quote line %d = %q, want rail + %q", i, lines[i], want)
		}
	}
}

// 引用内的嵌套代码块递归渲染为带 │ 边栏的代码面板，``` 标记不再以纯文本漏出。
func TestMarkdownQuoteNestedCodeBlock(t *testing.T) {
	src := "> 示例：\n>\n> ```json\n> {\n> }\n> ```\n> 完"
	out := ansi.Strip(renderMarkdown(src, 60))
	if strings.Contains(out, "```") {
		t.Fatalf("nested fence leaked as literal text:\n%s", out)
	}
	if !strings.Contains(out, "json") || !strings.Contains(out, "│") {
		t.Fatalf("nested code block not rendered inside quote rail:\n%s", out)
	}
}

// 横跨整条消息的 ```text 围栏视为传输包装解开；消息中段的普通 ```text
// 代码块保持面板渲染。
func TestWholeMessageTextFenceUnwraps(t *testing.T) {
	wrapped := "```text\n## 标题\n\n正文\n```"
	out := ansi.Strip(renderMarkdown(wrapped, 60))
	if strings.Contains(out, "```text") {
		t.Fatalf("whole-message text fence not unwrapped:\n%s", out)
	}
	if !strings.Contains(out, "标题") || !strings.Contains(out, "正文") {
		t.Fatalf("unwrapped content missing:\n%s", out)
	}

	inline := "前文\n\n```text\nplain snippet\n```\n\n后文"
	out = ansi.Strip(renderMarkdown(inline, 60))
	if !strings.Contains(out, "plain snippet") || !strings.Contains(out, "text") {
		t.Fatalf("mid-message text block lost panel or label:\n%s", out)
	}
}

// 未闭合的空围栏不渲染空面板。
func TestTrailingEmptyFenceIsSuppressed(t *testing.T) {
	out := ansi.Strip(renderMarkdown("内容\n```", 60))
	if strings.Contains(out, "╭") || strings.Contains(out, "╰") {
		t.Fatalf("empty trailing fence rendered an empty panel:\n%s", out)
	}
}

// tab 缩进的代码块：tab 展开为 4 空格（与 lipgloss 渲染一致），
// 行尾 token 不因测量/渲染宽度不一致而被错误换行。
// 回归：高亮后 \t 先被展开，wrap 按展开后宽度测量，把 "{" 挤到了下一行。
func TestCodeBlockTabIndentationRendersInline(t *testing.T) {
	src := "```go\nfunc main() {\n\tif x > 0 {\n\t\tx++\n\t}\n}\n```"
	out := ansi.Strip(renderMarkdown(src, 60))
	for _, want := range []string{"if x > 0 {", "x++"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tab-indented code block lost/wrapped %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "        x++") {
		t.Fatalf("nested tab indentation not expanded to 8 cells:\n%s", out)
	}
}
