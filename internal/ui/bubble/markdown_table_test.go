package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// assertTableLinesFit 断言表格渲染的每一行都不超过给定宽度，
// 防止右缘被切、边框断裂（┐/┤/┘ 丢失）和超宽折行错位。
func assertTableLinesFit(t *testing.T, rendered string, maxWidth int) {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if w := terminalCellWidth(line); w > maxWidth {
			t.Errorf("line width %d exceeds %d: %q", w, maxWidth, line)
		}
	}
}

func TestMarkdownTableMaxColumnsForWidth(t *testing.T) {
	// n 列的最小总宽度是 4n+1，所以列数上限应为 (width-1)/4。
	cases := []struct {
		width int
		want  int
	}{
		{8, 1}, {9, 2}, {12, 2}, {13, 3}, {19, 4}, {20, 4}, {21, 5},
		{40, 9}, {80, 19}, {100, 24},
	}
	for _, c := range cases {
		if got := markdownTableMaxColumnsForWidth(c.width); got != c.want {
			t.Errorf("markdownTableMaxColumnsForWidth(%d) = %d, want %d", c.width, got, c.want)
		}
	}
	// 上限满足 4n+1 ≤ width，而 n+1 列必然放不下。
	for _, c := range cases {
		if n := markdownTableMaxColumnsForWidth(c.width); 4*n+1 > c.width {
			t.Errorf("limit %d columns does not fit width %d (4*%d+1=%d)", n, c.width, n, 4*n+1)
		}
	}
}

// TestMarkdownTableNarrowWidthKeepsBordersIntact 回归测试：
// 40 格终端渲染 10 列表格时，多余列应被丢弃而不是让表格超宽，
// 保证右缘边框完整、内容不被终端折行。
func TestMarkdownTableNarrowWidthKeepsBordersIntact(t *testing.T) {
	lines := []string{
		"| a | b | c | d | e | f | g | h | i | j |",
		"|---|---|---|---|---|---|---|---|---|---|",
		"| 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 |",
	}
	rendered := renderMarkdownTable(lines, 40)
	assertTableLinesFit(t, rendered, 40)

	plain := ansi.Strip(rendered)
	if !strings.HasPrefix(plain, "┌") || !strings.Contains(plain, "┐") {
		t.Errorf("top border broken: %q", plain)
	}
	if !strings.HasSuffix(plain, "┘") {
		t.Errorf("bottom border broken: %q", plain)
	}
	// 第 10 列（"j" / "10"）超出 (40-1)/4=9 列上限，应被整体丢弃，
	// 而不是把 "10" 拆进 1 格列里显示成两行。
	if strings.Contains(plain, "10") {
		t.Errorf("dropped column content leaked into table: %q", plain)
	}
}

// TestMarkdownTableCJKColumnKeepsTwoCells 验证宽字符列保底 2 格：
// 空间不足时中文列应优先保留 2 格（内容换行显示），而不是退化成省略号。
func TestMarkdownTableColumnWidthsKeepsWideCharFloor(t *testing.T) {
	rows := [][]string{
		{"aaaaaaaaaaaaaaaa", "中文"},
		{"bbbbbbbbbbbbbbbb", "测试"},
	}
	widths := markdownTableColumnWidths(rows, 2, 10)
	// 宽度 10、2 列：总宽 ≤ 10，即 sum(widths) ≤ 3；宽字符列应保底 2 格。
	if widths[1] != 2 {
		t.Fatalf("wide-char column width = %d, want 2 (floor): %v", widths[1], widths)
	}
	if widths[0]+widths[1] > 3 {
		t.Fatalf("total width %d exceeds available 3", widths[0]+widths[1])
	}
}

// TestMarkdownTableCJKColumnRendersWithoutEllipsis 端到端验证：
// 中文内容在需要换行的窄列里显示完整字符，不出现省略号退化。
func TestMarkdownTableCJKColumnRendersWithoutEllipsis(t *testing.T) {
	lines := []string{
		"| aaaaaaaaaaaaaaaa | 中文 |",
		"|------------------|------|",
		"| bbbbbbbbbbbbbbbb | 内容 |",
	}
	rendered := renderMarkdownTable(lines, 20)
	assertTableLinesFit(t, rendered, 20)
	if strings.Contains(ansi.Strip(rendered), "…") {
		t.Errorf("wide-char content degraded to ellipsis:\n%s", ansi.Strip(rendered))
	}
}

// TestMarkdownTableManyWideColumnsStillFit 极端场景：
// 列数接近上限且全部是宽字符时，表格仍不得超过终端宽度。
func TestMarkdownTableManyWideColumnsStillFit(t *testing.T) {
	lines := []string{
		"| 甲 | 乙 | 丙 | 丁 | 戊 |",
		"|---|---|---|---|---|",
		"| 中文 | 中文 | 中文 | 中文 | 中文 |",
	}
	rendered := renderMarkdownTable(lines, 20)
	assertTableLinesFit(t, rendered, 20)
}

// TestMarkdownTableHeaderCentered 验证表头列名始终居中，
// 即使该列在 Markdown 里声明了右对齐（---:）；正文行仍保留声明的对齐。
func TestMarkdownTableHeaderCentered(t *testing.T) {
	header := ansi.Strip(renderMarkdownTableRowLines(
		[]string{"Name", "Value"}, []int{5, 5}, true,
		[]markdownTableAlignment{markdownTableAlignCenter, markdownTableAlignRight},
	)[0])
	// 表头单元格两侧对称填充：左侧无额外空格（居中），右侧声明右对齐也被覆盖。
	if !strings.Contains(header, " Name  ") || !strings.Contains(header, " Value ") {
		t.Fatalf("header row = %q, want centered cells", header)
	}

	body := ansi.Strip(renderMarkdownTableRowLines(
		[]string{"alpha", "1"}, []int{5, 5}, false,
		[]markdownTableAlignment{markdownTableAlignCenter, markdownTableAlignRight},
	)[0])
	if !strings.Contains(body, " alpha ") {
		t.Fatalf("body row = %q, want centered alpha", body)
	}
	if !strings.HasSuffix(body, "1 │") {
		t.Fatalf("body row = %q, want right-aligned value cell preserved", body)
	}
}

// TestMarkdownTableCJKWidthsCorrect 基础回归：普通中文表格渲染宽度正确、内容完整。
func TestMarkdownTableCJKWidthsCorrect(t *testing.T) {
	lines := []string{
		"| 名称 | 值 |",
		"|------|-----|",
		"| 中文内容 | 12345 |",
	}
	rendered := renderMarkdownTable(lines, 30)
	assertTableLinesFit(t, rendered, 30)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "中文内容") {
		t.Errorf("CJK content missing:\n%s", plain)
	}
}

func TestMarkdownTableStyledLongCellsKeepANSIAndBordersIntact(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousCodeStyle := markdownCodeStyle
	lipgloss.SetColorProfile(termenv.TrueColor)
	markdownCodeStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Background(lipgloss.Color("#1f2334")).
		Padding(0, 1)
	t.Cleanup(func() {
		markdownCodeStyle = previousCodeStyle
		lipgloss.SetColorProfile(previousProfile)
	})

	lines := []string{
		"| 文件 | 改动 |",
		"|---|---|",
		"| `deepseek_schema.go` | +635 行，大量新增（strict 模式 schema 扩展） |",
		"| `model_turn.go`（internal/loop） | +430 行 |",
		"| `stream.go` | +147 行，流式处理逻辑扩展 |",
		"| `responses.go` / `adapter.go` / `client.go` / `gpt_adapter.go` / `openai_compatible_adapter.go` / `deepseek_adapter.go` | 均有修改，含 DeepSeek strict 适配 |",
	}
	for _, width := range []int{40, 80, 100} {
		rendered := renderMarkdownTable(lines, width)
		plain := ansi.Strip(rendered)
		rows := [][]string{parseMarkdownTableRow(lines[0])}
		for _, line := range lines[2:] {
			rows = append(rows, parseMarkdownTableRow(line))
		}
		columnWidths := markdownTableColumnWidths(rows, 2, width)
		var firstColumn strings.Builder
		for _, renderedLine := range strings.Split(rendered, "\n") {
			if strings.HasPrefix(ansi.Strip(renderedLine), "│") {
				cell := cutStyledCellsExact(renderedLine, 2, 2+columnWidths[0])
				firstColumn.WriteString(strings.TrimSpace(ansi.Strip(cell)))
			}
		}
		for _, leaked := range []string{";245;", "48;2;31;35;52m", "[38;5"} {
			if strings.Contains(plain, leaked) {
				t.Fatalf("width=%d ANSI payload leaked as %q:\n%s", width, leaked, plain)
			}
		}
		for _, file := range []string{
			"deepseek_schema.go", "model_turn.go", "stream.go", "responses.go", "adapter.go",
			"client.go", "gpt_adapter.go", "openai_compatible_adapter.go", "deepseek_adapter.go",
		} {
			if !strings.Contains(firstColumn.String(), file) {
				t.Fatalf("width=%d lost %q:\n%s", width, file, plain)
			}
		}
		for index, line := range strings.Split(rendered, "\n") {
			assertTerminalSequencesComplete(t, line)
			if got := terminalCellWidth(line); got != width {
				t.Fatalf("width=%d line=%d cell width=%d:\n%s", width, index, got, ansi.Strip(line))
			}
			if got := terminalCellWidth(ansi.Strip(line)); got != width {
				t.Fatalf("width=%d line=%d stripped width=%d:\n%s", width, index, got, ansi.Strip(line))
			}
		}
	}
}

// styleForegroundSGR 返回样式渲染单字符时发出的 24 位前景 SGR 序列，
// 用于断言某段文本确实携带指定前景色而不是回退终端默认。测试环境默认
// 无颜色 profile，先强制 TrueColor 再渲染。
func styleForegroundSGR(t *testing.T, style lipgloss.Style) string {
	t.Helper()
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	rendered := style.Render("x")
	// SGR 可能带 Bold 等前缀（如 \x1b[1;38;2;...m），从参数段向前找转义起点。
	// 返回值不含结尾 'm'：lipgloss 会把前景与背景合并成一条 SGR（如
	// \x1b[38;2;201;194;183;48;2;...m），去掉 'm' 后参数段前缀仍可匹配。
	index := strings.Index(rendered, "38;2;")
	if index < 0 {
		t.Fatalf("style %#v renders without 24-bit foreground: %q", style, rendered)
	}
	escStart := strings.LastIndex(rendered[:index], "\x1b[")
	if escStart < 0 {
		t.Fatalf("style %#v foreground SGR has no escape start: %q", style, rendered)
	}
	end := strings.IndexByte(rendered[index+len("38;2;"):], 'm')
	if end < 0 {
		t.Fatalf("style %#v foreground SGR incomplete: %q", style, rendered)
	}
	return rendered[escStart : index+len("38;2;")+end]
}

// TestMarkdownTableCellsCarryBodyForeground 回归测试：普通单元格此前只拼接
// renderInlineMarkdown 的裸文本（纯文本段无 SGR），回退为终端默认前景色，
// 与正文颜色不一致。现在每个非表头单元格必须自带正文前景色，同时行内代码
// 片段仍保留自己的前景/背景。
func TestMarkdownTableCellsCarryBodyForeground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	previousCodeStyle := markdownCodeStyle
	lipgloss.SetColorProfile(termenv.TrueColor)
	markdownCodeStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Background(lipgloss.Color("#1f2334")).
		Padding(0, 1)
	t.Cleanup(func() {
		markdownCodeStyle = previousCodeStyle
		lipgloss.SetColorProfile(previousProfile)
	})

	rendered := renderMarkdown("| a | b |\n|---|---|\n| x | `y` |\n", 40)
	lines := strings.Split(rendered, "\n")
	// 渲染顺序：┌ 顶边、表头行、├ 分隔、数据行、└ 底边。
	if len(lines) < 5 {
		t.Fatalf("table rows = %d, want at least 5:\n%q", len(lines), rendered)
	}
	bodyFG := styleForegroundSGR(t, bodyStyle)
	headingFG := styleForegroundSGR(t, markdownHeadingStyle)
	codeSegment := markdownCodeStyle.Render("y")

	dataLine := lines[3]
	if !strings.Contains(dataLine, bodyFG) {
		t.Fatalf("data row missing body foreground %q:\n%q", bodyFG, dataLine)
	}
	if !strings.Contains(dataLine, codeSegment) {
		t.Fatalf("data row inline code lost its own style:\n%q", dataLine)
	}
	if strings.Contains(dataLine, headingFG) {
		t.Fatalf("data row must not use heading foreground %q:\n%q", headingFG, dataLine)
	}
	headerLine := lines[1]
	if !strings.Contains(headerLine, headingFG) {
		t.Fatalf("header row missing heading foreground %q:\n%q", headingFG, headerLine)
	}
}
