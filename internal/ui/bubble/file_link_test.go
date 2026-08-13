// 本文件覆盖「输出中的文件引用可点击」：markdown 链接指向本地绝对路径或
// file:// URL 时渲染为 OSC 8 终端超链接，鼠标单击用系统默认程序打开文件，
// 与网页链接的交互一致（[$skill](/abs/path/SKILL.md) 形式）。
package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestRenderInlineMarkdownMakesLocalFileLinkClickable 验证输出中的文件引用
// （[$name](/绝对/路径) 形式，路径可含空格）渲染成 OSC 8 超链接，可见文本
// 只保留链接标签，与网页链接一致。
func TestRenderInlineMarkdownMakesLocalFileLinkClickable(t *testing.T) {
	const target = "/Users/united_pooh/Library/Application Support/Paw/skills/investigation-first/SKILL.md"
	rendered := renderInlineMarkdown("[$investigation-first](" + target + ")")
	wantLink := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render("$investigation-first") +
		ansi.ResetHyperlink()

	if !strings.Contains(rendered, wantLink) {
		t.Fatalf("inline markdown = %q, want OSC 8 file link %q", rendered, wantLink)
	}
	if got := ansi.Strip(rendered); got != "$investigation-first" {
		t.Fatalf("stripped inline markdown = %q, want link label only", got)
	}
}

// TestRenderInlineMarkdownKeepsRelativePathLinkLiteral 验证相对路径的
// markdown 链接不渲染成超链接（没有可解析的基准目录），原样保留为文本，
// 避免把正文里无意的 [x](y) 变成死链接。
func TestRenderInlineMarkdownKeepsRelativePathLinkLiteral(t *testing.T) {
	const literal = "see [notes](docs/notes.md)"
	rendered := renderInlineMarkdown(literal)
	if strings.Contains(rendered, ansi.SetHyperlink("docs/notes.md")) {
		t.Fatalf("inline markdown = %q, relative path must not be clickable", rendered)
	}
	if got := ansi.Strip(rendered); got != literal {
		t.Fatalf("stripped inline markdown = %q, want literal %q", got, literal)
	}
}

// TestRenderInlineMarkdownMakesBareFileURLClickable 验证裸 file:// URL 与
// 裸 http(s) URL 一样可点击。
func TestRenderInlineMarkdownMakesBareFileURLClickable(t *testing.T) {
	const target = "file:///Users/me/notes.md"
	rendered := renderInlineMarkdown("打开 " + target)
	wantLink := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render(target) +
		ansi.ResetHyperlink()

	if !strings.Contains(rendered, wantLink) {
		t.Fatalf("inline markdown = %q, want OSC 8 file URL %q", rendered, wantLink)
	}
	if got := ansi.Strip(rendered); got != "打开 "+target {
		t.Fatalf("stripped inline markdown = %q, want visible file URL unchanged", got)
	}
}

// TestRenderInlineMarkdownFileLinkWithUnderscores 回归：路径中的下划线
// （如 united_pooh、a_b/c_d.md）按 CommonMark intraword 规则不构成斜体
// 标记，链接目标保持完整，仍可点击。
func TestRenderInlineMarkdownFileLinkWithUnderscores(t *testing.T) {
	for _, target := range []string{
		"/Users/united_pooh/a_b/c_d.md",
		"/data/中文_测试/file.md",
	} {
		rendered := renderInlineMarkdown("[$skill](" + target + ")")
		wantLink := ansi.SetHyperlink(target) +
			markdownLinkStyle.Render("$skill") +
			ansi.ResetHyperlink()
		if !strings.Contains(rendered, wantLink) {
			t.Fatalf("inline markdown = %q, want whole file link %q", rendered, wantLink)
		}
	}
}

// TestRenderInlineMarkdownKeepsIntrawordUnderscoreLiteral 验证普通文本中的
// intraword 下划线/星号保持字面量，不误判为斜体。
func TestRenderInlineMarkdownKeepsIntrawordUnderscoreLiteral(t *testing.T) {
	for _, input := range []string{
		"use a_b_c here",
		"read united_pooh notes",
		"value v1*v2 kept",
	} {
		if got := ansi.Strip(renderInlineMarkdown(input)); got != input {
			t.Fatalf("renderInlineMarkdown(%q) = %q, want literal input", input, got)
		}
	}
}

// TestRenderInlineMarkdownSkipsFilePathInsideCodeSpan 验证代码片段中的
// 文件路径不会变成可点击链接（与 URL 规则一致）。
func TestRenderInlineMarkdownSkipsFilePathInsideCodeSpan(t *testing.T) {
	const path = "/tmp/secret/keys.txt"
	rendered := renderInlineMarkdown("路径 `" + path + "`")
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("inline markdown = %q, code span path should not be clickable", rendered)
	}
	if !strings.Contains(rendered, markdownCodeStyle.Render(path)) {
		t.Fatalf("inline markdown = %q, want code-styled path", rendered)
	}
}

// TestTerminalHyperlinkAtPointReturnsLocalFilePath 验证渲染文本命中检测能
// 从 OSC 8 序列里取回本地文件路径。
func TestTerminalHyperlinkAtPointReturnsLocalFilePath(t *testing.T) {
	const target = "/tmp/skill/SKILL.md"
	content := ansi.SetHyperlink(target) +
		markdownLinkStyle.Render("$skill") +
		ansi.ResetHyperlink()

	for _, col := range []int{1, 2, 5} {
		if got := terminalHyperlinkAtPoint(content, selectionPoint{row: 0, col: col}); got != target {
			t.Fatalf("hyperlink at col %d = %q, want %q", col, got, target)
		}
	}
	if got := terminalHyperlinkAtPoint("plain text", selectionPoint{row: 0, col: 2}); got != "" {
		t.Fatalf("plain-text hyperlink = %q, want empty", got)
	}
}

// TestOpenTerminalURLCmdAcceptsLocalFileTargets 验证打开命令只接受可点击
// 目标：绝对路径与 file:// URL 返回命令，相对路径与未知 scheme 返回 nil。
func TestOpenTerminalURLCmdAcceptsLocalFileTargets(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantCmd bool
	}{
		{"absolute path", "/tmp/notes.md", true},
		{"file URL", "file:///tmp/notes.md", true},
		{"http URL", "https://example.com", true},
		{"relative path", "docs/notes.md", false},
		{"unknown scheme", "mailto:hi@example.com", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if cmd := openTerminalURLCmd(tc.target); (cmd != nil) != tc.wantCmd {
				t.Fatalf("openTerminalURLCmd(%q) cmd = %v, want %v", tc.target, cmd != nil, tc.wantCmd)
			}
		})
	}
}

// TestTerminalOpenPathConvertsFileURL 验证 file:// URL 打开前转成本地路径
// （含 %xx 转义还原），其它目标原样传递。
func TestTerminalOpenPathConvertsFileURL(t *testing.T) {
	cases := map[string]string{
		"file:///tmp/a%20b.md":      "/tmp/a b.md",
		"file:///Users/me/notes.md": "/Users/me/notes.md",
		"/tmp/plain.md":             "/tmp/plain.md",
		"https://example.com/x?q=1": "https://example.com/x?q=1",
		"file:///tmp/中文/notes.md":   "/tmp/中文/notes.md",
	}
	for input, want := range cases {
		if got := terminalOpenPath(input); got != want {
			t.Fatalf("terminalOpenPath(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestOpenTerminalURLCmdOpensFileThroughHook 验证 file:// 目标执行命令时
// 以本地路径调用系统打开器。
func TestOpenTerminalURLCmdOpensFileThroughHook(t *testing.T) {
	oldOpenTerminalURL := openTerminalURL
	var opened string
	openTerminalURL = func(target string) error {
		opened = target
		return nil
	}
	defer func() {
		openTerminalURL = oldOpenTerminalURL
	}()

	cmd := openTerminalURLCmd("file:///tmp/a%20b.md")
	if cmd == nil {
		t.Fatal("file URL open command is nil")
	}
	cmd()
	if opened != "/tmp/a b.md" {
		t.Fatalf("opened path = %q, want /tmp/a b.md", opened)
	}
}

// TestTranscriptMouseClickOpensLocalFile 端到端：输出中的文件引用渲染为
// OSC 8 超链接后，鼠标单击标签会通过系统打开器打开该文件。
func TestTranscriptMouseClickOpensLocalFile(t *testing.T) {
	oldOpenTerminalURL := openTerminalURL
	var opened string
	openTerminalURL = func(target string) error {
		opened = target
		return nil
	}
	defer func() {
		openTerminalURL = oldOpenTerminalURL
	}()

	const target = "/tmp/skill/SKILL.md"
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "Read [$skill](" + target + ")",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	row := -1
	col := -1
	for index, line := range lines {
		if offset := strings.Index(line.plain, "$skill"); offset >= 0 {
			row = index
			col = terminalCellWidth(line.plain[:offset])
			break
		}
	}
	if row < 0 || col < 0 {
		t.Fatalf("rendered transcript does not contain file link label:\n%s", model.viewport.View())
	}

	x := mainContentPadding + col + 1
	y := model.transcriptScreenTop() + row
	next, _ := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)
	next, cmd := model.Update(tea.MouseMsg{
		X:      x,
		Y:      y,
		Action: tea.MouseActionRelease,
		Button: tea.MouseButtonLeft,
	})
	model = next.(appModel)

	if cmd == nil {
		t.Fatal("file link click returned no deferred command")
	}
	// 单击动作延迟到双击窗口之后（测试里立即派发），处理后才产生打开命令。
	next, cmd = model.Update(cmd())
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("deferred file link click returned no open command")
	}
	cmd()
	if opened != target {
		t.Fatalf("opened file = %q, want %q", opened, target)
	}
	if model.selectionActive {
		t.Fatal("file link click should not create a transcript selection")
	}
}
