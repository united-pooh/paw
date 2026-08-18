// 本文件实现面向终端聊天区的轻量 Markdown 渲染器。
package bubble

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const maxRenderedCodeBlockLines = 32

// renderMarkdown 将 assistant 返回的 Markdown 文本转换为带样式的终端文本。
func renderMarkdown(markdown string, width int) string {
	width = maxInt(1, width)
	lines := strings.Split(strings.TrimRight(markdown, "\n"), "\n")
	parts := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if trimmed == "" {
			parts = append(parts, "")
			continue
		}

		if lang, ok := fencedCodeStart(trimmed); ok {
			codeLines, end := collectFencedCodeLines(lines, i, lang)
			i = end
			parts = append(parts, renderCodeBlock(lang, strings.Join(codeLines, "\n"), width))
			continue
		}
		if isMarkdownTableStart(lines, i) {
			tableLines, end := collectMarkdownTableLines(lines, i)
			i = end
			parts = append(parts, renderMarkdownTable(tableLines, width))
			continue
		}
		if isMarkdownThematicBreak(trimmed) {
			parts = append(parts, markdownRuleStyle.Render(strings.Repeat("─", width)))
			continue
		}
		if level, text, ok := markdownHeading(trimmed); ok {
			parts = append(parts, leading+renderMarkdownHeading(level, text, width))
			continue
		}
		if text, ok := strings.CutPrefix(trimmed, ">"); ok {
			parts = append(parts, leading+markdownQuoteStyle.Width(maxInt(1, width-2)).Render(restoreForegroundAfterANSIReset(renderInlineMarkdown(strings.TrimSpace(text)), colorManager.Hex(colorMarkdownQuote))))
			continue
		}
		if marker, text, ok := markdownListItem(trimmed); ok {
			body := restoreForegroundAfterANSIReset(renderInlineMarkdown(text), colorManager.Hex(colorBody))
			parts = append(parts, leading+markdownBulletStyle.Render(marker)+" "+bodyStyle.Width(maxInt(1, width-terminalCellWidth(marker)-1)).Render(body))
			continue
		}

		body := restoreForegroundAfterANSIReset(renderInlineMarkdown(strings.TrimRight(line, " \t")), colorManager.Hex(colorBody))
		parts = append(parts, bodyStyle.Width(width).Render(wrapCompact(leading+body, width)))
	}

	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

// isMarkdownThematicBreak recognizes CommonMark-style horizontal rules. The
// lightweight renderer intentionally treats a marker-only line as a divider
// even after ordinary text instead of implementing Setext headings.
func isMarkdownThematicBreak(line string) bool {
	marker := rune(0)
	count := 0
	for _, current := range strings.TrimSpace(line) {
		if unicode.IsSpace(current) {
			continue
		}
		if current != '-' && current != '*' && current != '_' {
			return false
		}
		if marker == 0 {
			marker = current
		} else if current != marker {
			return false
		}
		count++
	}
	return count >= 3
}

// fencedCodeStart 判断一行是否开启 fenced code block，并返回语言标签。
func fencedCodeStart(line string) (string, bool) {
	lang, ok := strings.CutPrefix(line, "```")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(lang), true
}

// isFencedCodeClose 只把没有语言标签的 fence 当作结束标记。
// 这样代码示例中的 ```json / ```go 不会意外关闭外层代码块。
func isFencedCodeClose(line string) bool {
	return strings.TrimSpace(line) == "```"
}

// collectFencedCodeLines 收集代码块内容，并对 markdown 代码块中的嵌套 fence 做特殊处理。
func collectFencedCodeLines(lines []string, start int, lang string) ([]string, int) {
	if start >= len(lines) {
		return nil, start
	}
	codeLines := []string{}
	nestedMarkdownFences := 0
	trackNested := isMarkdownFenceLanguage(lang)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			if !trackNested {
				if isFencedCodeClose(trimmed) {
					return codeLines, i
				}
				codeLines = append(codeLines, lines[i])
				continue
			}
			innerLang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if innerLang == "" {
				if nestedMarkdownFences == 0 {
					return codeLines, i
				}
				nestedMarkdownFences--
				codeLines = append(codeLines, lines[i])
				continue
			}
			nestedMarkdownFences++
			codeLines = append(codeLines, lines[i])
			continue
		}
		codeLines = append(codeLines, lines[i])
	}
	return codeLines, len(lines) - 1
}

// isMarkdownFenceLanguage 判断当前代码块是否是 markdown/md，需要允许内部嵌套 fence。
func isMarkdownFenceLanguage(lang string) bool {
	lang = strings.ToLower(strings.TrimSpace(lang))
	return lang == "markdown" || lang == "md"
}

// renderCodeBlock 渲染代码块，并将语言标签嵌入代码块顶边。
func renderCodeBlock(lang, code string, width int) string {
	width = maxInt(1, width)
	label := strings.TrimSpace(lang)
	body := strings.TrimRight(code, "\n")
	if body == "" {
		body = " "
	}
	// Models sometimes wrap a complete Markdown answer in ```markdown. Treat
	// that wrapper as a transport/presentation fence and render the contained
	// Markdown normally; otherwise headings and task lists leak their syntax
	// markers into the terminal (for example: "### 根因" and "- [ ] ...").
	if isMarkdownFenceLanguage(label) {
		return renderMarkdown(body, width)
	}
	return renderCodeBlockPanel(body, width, label)
}

func renderCodeBlockPanel(code string, width int, label string) string {
	width = maxInt(1, width)
	blockWidth := markdownCodeBlockWidth(code, label, width)
	if blockWidth < 6 {
		lines := limitRenderedCodeBlockLines(wrapStyledCellText(code, width), maxRenderedCodeBlockLines)
		return markdownCodeBlockStyle.Render(strings.Join(lines, "\n"))
	}
	textWidth := maxInt(1, blockWidth-4)
	lines := limitRenderedCodeBlockLines(wrapStyledCellText(code, textWidth), maxRenderedCodeBlockLines)
	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, renderCodeBlockTopBorder(label, blockWidth))
	for _, line := range lines {
		body := " " + markdownCodeBlockStyle.Render(fitStyledCellLine(line, textWidth)) + " "
		rendered = append(rendered,
			markdownCodeBlockBorderStyle.Render("│")+body+markdownCodeBlockBorderStyle.Render("│"),
		)
	}
	rendered = append(rendered, renderCodeBlockBorderLine("╰", "─", "╯", blockWidth))
	return strings.Join(rendered, "\n")
}

func limitRenderedCodeBlockLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	hidden := len(lines) - maxLines
	out := append([]string(nil), lines[:maxLines]...)
	out = append(out, "... "+strconv.Itoa(hidden)+" more lines hidden")
	return out
}

func markdownCodeBlockWidth(code, label string, width int) int {
	maxWidth := maxInt(6, width-4)
	widest := 1
	sourceLines := strings.Split(code, "\n")
	for _, line := range sourceLines {
		widest = maxInt(widest, terminalCellWidth(line))
	}
	if len(sourceLines) > maxRenderedCodeBlockLines {
		hiddenSummary := "... " + strconv.Itoa(len(sourceLines)-maxRenderedCodeBlockLines) + " more lines hidden"
		widest = maxInt(widest, terminalCellWidth(hiddenSummary))
	}
	required := maxInt(6, widest+4)
	if label != "" {
		required = maxInt(required, terminalCellWidth(label)+6)
	}
	return minInt(maxWidth, required)
}

func renderCodeBlockTopBorder(label string, width int) string {
	label = strings.TrimSpace(label)
	if label == "" || width < 7 {
		return renderCodeBlockBorderLine("╭", "─", "╮", width)
	}

	label = truncateStyledCellLine(label, width-6)
	if label == "" {
		return renderCodeBlockBorderLine("╭", "─", "╮", width)
	}

	chipText := " " + label + " "
	chip := markdownCodeBlockLabelStyle.Render(chipText)
	fillWidth := maxInt(0, width-2-terminalCellWidth(chipText))
	leftWidth := fillWidth / 2
	rightWidth := fillWidth - leftWidth
	return markdownCodeBlockBorderStyle.Render("╭"+strings.Repeat("─", leftWidth)) +
		chip +
		markdownCodeBlockBorderStyle.Render(strings.Repeat("─", rightWidth)+"╮")
}

func renderCodeBlockBorderLine(left, fill, right string, width int) string {
	fillWidth := maxInt(1, width-terminalCellWidth(left)-terminalCellWidth(right))
	return markdownCodeBlockBorderStyle.Render(left + strings.Repeat(fill, fillWidth) + right)
}

// isMarkdownTableStart 判断指定行是否是 Markdown 表格的表头行。
func isMarkdownTableStart(lines []string, index int) bool {
	if index+1 >= len(lines) {
		return false
	}
	header := parseMarkdownTableRow(lines[index])
	separator := parseMarkdownTableRow(lines[index+1])
	if len(header) < 2 || len(separator) < 2 {
		return false
	}
	for _, cell := range separator {
		if !isMarkdownTableSeparatorCell(cell) {
			return false
		}
	}
	return true
}

// collectMarkdownTableLines 从表头开始收集连续的 Markdown 表格行。
func collectMarkdownTableLines(lines []string, start int) ([]string, int) {
	tableLines := []string{lines[start], lines[start+1]}
	end := start + 1
	for i := start + 2; i < len(lines); i++ {
		if len(parseMarkdownTableRow(lines[i])) < 2 {
			break
		}
		tableLines = append(tableLines, lines[i])
		end = i
	}
	return tableLines, end
}

// renderMarkdownTable 将 Markdown 表格渲染为终端中对齐的文本表格。
func renderMarkdownTable(lines []string, width int) string {
	if len(lines) < 2 {
		return ""
	}
	rows := make([][]string, 0, len(lines)-1)
	rows = append(rows, parseMarkdownTableRow(lines[0]))
	for _, line := range lines[2:] {
		rows = append(rows, parseMarkdownTableRow(line))
	}
	columnCount := markdownTableColumnCount(rows)
	if columnCount == 0 {
		return ""
	}
	columnCount = minInt(columnCount, markdownTableMaxColumnsForWidth(width))
	normalizeMarkdownTableRows(rows, columnCount)
	alignments := markdownTableAlignments(parseMarkdownTableRow(lines[1]), columnCount)
	widths := markdownTableColumnWidths(rows, columnCount, width)
	renderedRows := make([]string, 0, len(rows)+2)
	renderedRows = append(renderedRows, renderMarkdownTableBorder("┌", "┬", "┐", widths))
	for i, row := range rows {
		renderedRows = append(renderedRows, renderMarkdownTableRowLines(row, widths, i == 0, alignments)...)
		if i == len(rows)-1 {
			renderedRows = append(renderedRows, renderMarkdownTableBorder("└", "┴", "┘", widths))
		} else {
			renderedRows = append(renderedRows, renderMarkdownTableBorder("├", "┼", "┤", widths))
		}
	}
	return strings.Join(renderedRows, "\n")
}

// parseMarkdownTableRow 将一行 Markdown 表格拆成单元格。
func parseMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

// isMarkdownTableSeparatorCell 判断单元格是否属于表头分隔线。
func isMarkdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}
	cell = strings.Trim(cell, ":")
	count := 0
	for _, r := range cell {
		if unicode.IsSpace(r) {
			continue
		}
		if !isMarkdownTableSeparatorRune(r) {
			return false
		}
		count++
	}
	return count >= 3
}

func isMarkdownTableSeparatorRune(r rune) bool {
	switch r {
	case '-', '—', '–', '─', '━':
		return true
	default:
		return false
	}
}

// markdownTableMaxColumnsForWidth 返回当前宽度下最多可渲染的表格列数。
// 每列至少需要一个内容格和左右两个内边距（共 3 格），列间及两端还各有一条竖线
// （n 列共 n+1 条），所以 n 列的最小总宽度是 3n+(n+1)=4n+1，列数上限为 (width-1)/4。
// 超过上限的列会被丢弃，保证表格总宽不会超过终端宽度。
func markdownTableMaxColumnsForWidth(width int) int {
	width = maxInt(1, width)
	return maxInt(1, (width-1)/4)
}

// markdownTableColumnCount 返回表格中需要渲染的最大列数。
func markdownTableColumnCount(rows [][]string) int {
	count := 0
	for _, row := range rows {
		count = maxInt(count, len(row))
	}
	return count
}

// normalizeMarkdownTableRows 将所有表格行修正到相同列数。
func normalizeMarkdownTableRows(rows [][]string, columnCount int) {
	for i := range rows {
		for len(rows[i]) < columnCount {
			rows[i] = append(rows[i], "")
		}
		if len(rows[i]) > columnCount {
			rows[i] = rows[i][:columnCount]
		}
	}
}

// markdownTableColumnWidths 根据内容和最大宽度计算每列展示宽度。
func markdownTableColumnWidths(rows [][]string, columnCount, maxWidth int) []int {
	widths := make([]int, columnCount)
	minWidths := make([]int, columnCount)
	for i := range minWidths {
		minWidths[i] = 1
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = maxInt(widths[i], terminalCellWidth(renderInlineMarkdown(cell)))
			if minWidths[i] < 2 && markdownTextHasWideChar(cell) {
				minWidths[i] = 2
			}
		}
	}
	available := maxInt(columnCount, maxWidth-columnCount*3-1)
	// 先削减到可读下限：含宽字符（CJK/emoji）的列保底 2 格，
	// 避免中文内容在 1 格列里整体退化成省略号。
	for markdownTableTotalWidth(widths) > available {
		widest := widestReducibleColumn(widths, minWidths)
		if widest < 0 {
			break
		}
		widths[widest]--
	}
	// 宽字符列过多导致仍超宽时，允许继续削减到 1 格，优先保证表格总宽不超过终端。
	for markdownTableTotalWidth(widths) > available {
		widest := widestReducibleColumn(widths, nil)
		if widest < 0 {
			break
		}
		widths[widest]--
	}
	for i := range widths {
		widths[i] = maxInt(1, widths[i])
	}
	return widths
}

// widestReducibleColumn 返回宽度大于下限的最宽列；mins 为 nil 时下限统一为 1。
func widestReducibleColumn(widths, mins []int) int {
	widest := -1
	for i := range widths {
		floor := 1
		if mins != nil {
			floor = mins[i]
		}
		if widths[i] > floor && (widest < 0 || widths[i] > widths[widest]) {
			widest = i
		}
	}
	return widest
}

// markdownTextHasWideChar 报告文本中是否包含显示宽度大于 1 的字符（如 CJK、emoji）。
func markdownTextHasWideChar(text string) bool {
	for _, r := range text {
		if terminalCellWidth(string(r)) > 1 {
			return true
		}
	}
	return false
}

// markdownTableTotalWidth 计算所有列宽之和，不包含列间分隔符。
func markdownTableTotalWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

// markdownTableAlignment 表示 Markdown 表格单元格的水平对齐方式。
type markdownTableAlignment uint8

const (
	markdownTableAlignLeft markdownTableAlignment = iota
	markdownTableAlignCenter
	markdownTableAlignRight
)

// markdownTableAlignments 从分隔行中的冒号读取各列的对齐方式。
func markdownTableAlignments(separator []string, columnCount int) []markdownTableAlignment {
	alignments := make([]markdownTableAlignment, columnCount)
	for i := range alignments {
		if i >= len(separator) {
			continue
		}
		cell := strings.TrimSpace(separator[i])
		left := strings.HasPrefix(cell, ":")
		right := strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			alignments[i] = markdownTableAlignCenter
		case right:
			alignments[i] = markdownTableAlignRight
		default:
			alignments[i] = markdownTableAlignLeft
		}
	}
	return alignments
}

// renderMarkdownTableRowLines 渲染一行表格，并对表头应用强调样式。
// 每个单元格都按自己的列宽换行，避免用省略号丢失模型返回的内容。
func renderMarkdownTableRowLines(row []string, widths []int, header bool, alignments []markdownTableAlignment) []string {
	wrapped := make([][]string, len(widths))
	lineCount := 1
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = restoreForegroundAfterANSIReset(renderInlineMarkdown(row[i]), colorManager.Hex(colorBody))
		}
		wrapped[i] = wrapStyledCellText(cell, width)
		if len(wrapped[i]) > lineCount {
			lineCount = len(wrapped[i])
		}
	}

	lines := make([]string, 0, lineCount)
	for lineIndex := 0; lineIndex < lineCount; lineIndex++ {
		var rendered strings.Builder
		rendered.WriteString(markdownRuleStyle.Render("│"))
		for i, width := range widths {
			cell := ""
			if lineIndex < len(wrapped[i]) {
				cell = wrapped[i][lineIndex]
			}
			alignment := markdownTableAlignLeft
			if i < len(alignments) {
				alignment = alignments[i]
			}
			if header {
				// 表头列名统一居中，不跟随 Markdown 声明的对齐方式。
				alignment = markdownTableAlignCenter
			}
			padded := padMarkdownTableCell(cell, width, alignment)
			if header {
				padded = markdownHeadingStyle.Render(padded)
			} else {
				// 普通单元格必须自带正文前景色：renderInlineMarkdown 的纯文本
				// 段不带 SGR，若没有这层包裹会回退为终端默认前景色，与正文
				// 及其他元素颜色不一致。
				padded = bodyStyle.Render(padded)
			}
			rendered.WriteString(" ")
			rendered.WriteString(padded)
			rendered.WriteString(" ")
			rendered.WriteString(markdownRuleStyle.Render("│"))
		}
		lines = append(lines, rendered.String())
	}
	return lines
}

// renderMarkdownTableRow 保留单行调用方的兼容包装；新表格渲染使用多行版本。
func renderMarkdownTableRow(row []string, widths []int, header bool, alignments []markdownTableAlignment) string {
	return strings.Join(renderMarkdownTableRowLines(row, widths, header, alignments), "\n")
}

// padMarkdownTableCell 将单元格按 Markdown 声明的对齐方式补齐到列宽。
func padMarkdownTableCell(cell string, width int, alignment markdownTableAlignment) string {
	remaining := maxInt(0, width-terminalCellWidth(cell))
	switch alignment {
	case markdownTableAlignCenter:
		left := remaining / 2
		return strings.Repeat(" ", left) + cell + strings.Repeat(" ", remaining-left)
	case markdownTableAlignRight:
		return strings.Repeat(" ", remaining) + cell
	default:
		return cell + strings.Repeat(" ", remaining)
	}
}

// renderMarkdownTableBorder 渲染表格外框或行间的完整横线。
func renderMarkdownTableBorder(left, junction, right string, widths []int) string {
	var rendered strings.Builder
	rendered.WriteString(markdownRuleStyle.Render(left))
	for i, width := range widths {
		rendered.WriteString(markdownRuleStyle.Render(strings.Repeat("─", width+2)))
		if i < len(widths)-1 {
			rendered.WriteString(markdownRuleStyle.Render(junction))
		}
	}
	rendered.WriteString(markdownRuleStyle.Render(right))
	return rendered.String()
}

// markdownHeading 解析 Markdown 标题等级和标题文本。
func markdownHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

// renderMarkdownHeading 根据标题等级渲染标题，标题标记只用于解析，不显示在终端中；一级标题会额外带下划线。
func renderMarkdownHeading(level int, text string, width int) string {
	text = restoreForegroundAfterANSIReset(renderInlineMarkdown(text), colorManager.Hex(colorMarkdownHeading))
	if level == 1 {
		rule := strings.Repeat("─", maxInt(8, minInt(width, terminalCellWidth(text)+4)))
		return markdownHeadingStyle.Render(text) + "\n" + markdownRuleStyle.Render(rule)
	}
	return markdownHeadingStyle.Render(text)
}

// markdownListItem 解析无序、有序和任务列表项：普通列表使用醒目的实心圆，
// 未完成与已完成任务保留各自的状态符号。
func markdownListItem(line string) (string, string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if text, ok := strings.CutPrefix(line, prefix); ok {
			return markdownTaskMarker(text)
		}
	}
	// 容忍模型省略列表符号和任务标记之间的空格，例如 -[] 和 -[x]。
	for _, prefix := range []string{"-", "*"} {
		if text, ok := strings.CutPrefix(line, prefix); ok {
			if marker, taskText, task := markdownTaskText(text); task {
				return marker, taskText, true
			}
		}
	}
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return "", "", false
	}
	for _, r := range line[:dot] {
		if !unicode.IsDigit(r) {
			return "", "", false
		}
	}
	return markdownTaskMarker(line[dot+2:])
}

// markdownTaskMarker 将 Markdown task-list 的方括号转换为终端可读的状态符号；
// 普通列表使用比 bullet 更大、更醒目的实心圆。
func markdownTaskMarker(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if marker, taskText, ok := markdownTaskText(text); ok {
		return marker, taskText, true
	}
	return "●", text, true
}

func markdownTaskText(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[]") {
		return "○", strings.TrimSpace(text[2:]), true
	}
	if len(text) >= 3 && text[0] == '[' && text[2] == ']' {
		switch text[1] {
		case ' ', '-':
			return "○", strings.TrimSpace(text[3:]), true
		case 'x', 'X':
			return "✓", strings.TrimSpace(text[3:]), true
		}
	}
	return "", "", false
}

// restoreForegroundAfterANSIReset 在行内样式自带的全量 SGR reset（\x1b[0m，
// lipgloss Style.Render 结束时输出）之后立即重设段落前景色。行内加粗/代码/
// 斜体/高亮/链接的 reset 会把外层 bodyStyle/quoteStyle/headingStyle 设置的前
// 景色一并清掉，使标记之后的同一行正文回退为终端默认色（如用户主题正文色与
// 终端默认前景不一致时肉眼可见）；本函数在 reset 后补回 foreground 指定的前
// 景 SGR，保证整行延续段落前景色。
//
// 若 reset 之后紧跟另一条 CSI SGR（\x1b[，如代码 span 的 padding 空格背景或
// 相邻行的样式），说明马上有新样式接管，不需要插入恢复；OSC 超链接序列
// （\x1b]8;;）与正文文本不属于 SGR 前瞻，仍会插入。与
// layout.restoreBackgroundAfterANSIReset 同源，这里只恢复前景（正文段落无背景）。
func restoreForegroundAfterANSIReset(text, foreground string) string {
	restore := foregroundSGR(foreground)
	if restore == "" || !strings.Contains(text, "\x1b[") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text) + 8)
	for i := 0; i < len(text); {
		if text[i] != '\x1b' {
			out.WriteByte(text[i])
			i++
			continue
		}
		end := i + 1
		for end < len(text) && text[end] != 'm' {
			end++
		}
		if end >= len(text) {
			out.WriteString(text[i:])
			break
		}
		seq := text[i : end+1]
		out.WriteString(seq)
		if (seq == "\x1b[0m" || seq == "\x1b[m") &&
			!(end+1 < len(text) && text[end+1] == '\x1b' && text[end+2] == '[') {
			out.WriteString(restore)
		}
		i = end + 1
	}
	return out.String()
}

// renderInlineMarkdown 渲染行内 Markdown。代码、粗体、斜体和高亮标记
// 在本地被消费后再输出 ANSI 样式，未闭合标记则原样保留，避免模型输出
// 不完整时吞掉后续文本。
func renderInlineMarkdown(line string) string {
	var rendered strings.Builder
	for line != "" {
		start, marker, _, ok := nextInlineMarkdownSpan(line)
		if !ok {
			rendered.WriteString(renderTerminalLinks(line))
			break
		}
		rest := line[start+len(marker):]
		end := strings.Index(rest, marker)
		if end < 0 || end == 0 {
			// 未闭合或空标记不能消费后续内容：整段交回链接渲染器。
			// 这也避免 URL 中的下划线（如 Function_(mathematics)）被
			// 误判为 Markdown 斜体起始标记。
			rendered.WriteString(renderTerminalLinks(line))
			break
		}
		rendered.WriteString(renderTerminalLinks(line[:start]))
		content := rest[:end]
		switch marker {
		case "`":
			rendered.WriteString(markdownCodeStyle.Render(content))
		case "**", "__":
			rendered.WriteString(renderTerminalLinksWithStyle(content, markdownBoldStyle))
		case "==":
			rendered.WriteString(renderTerminalLinksWithStyle(content, markdownHighlightStyle))
		case "*", "_":
			rendered.WriteString(renderTerminalLinksWithStyle(content, markdownItalicStyle))
		}
		line = rest[end+len(marker):]
	}
	return rendered.String()
}

// nextInlineMarkdownSpan 返回最靠前且可识别的行内标记。双字符标记优先，
// 避免 ** 被误识别为两个斜体标记；代码优先于其它格式。单字符强调标记
// （* / _）还要满足 CommonMark 的 intraword 规则：两侧都是词字符时不开启
// 强调，避免文件路径/URL 中的下划线（如 /Users/a_b/c_d.md）被拆成斜体。
func nextInlineMarkdownSpan(line string) (int, string, lipgloss.Style, bool) {
	markers := []string{"`", "**", "__", "==", "*", "_"}
	best := -1
	bestMarker := ""
	for _, marker := range markers {
		index := strings.Index(line, marker)
		if index < 0 || (marker == "*" && index+1 < len(line) && line[index+1] == '*') ||
			(marker == "_" && index+1 < len(line) && line[index+1] == '_') ||
			(marker == "*" || marker == "_") && !emphasisMarkerCanOpen(line, index, marker) {
			continue
		}
		if best < 0 || index < best || (index == best && len(marker) > len(bestMarker)) {
			best = index
			bestMarker = marker
		}
	}
	if best < 0 {
		return 0, "", lipgloss.Style{}, false
	}
	return best, bestMarker, lipgloss.Style{}, true
}

// emphasisMarkerCanOpen 报告单字符强调标记（* / _）在该位置是否可开启强调。
// CommonMark 规定两侧都是词字符的 intraword 标记不构成强调，例如
// united_pooh 里的下划线；路径中常见这类写法，误判会把链接目标拆开。
func emphasisMarkerCanOpen(line string, index int, marker string) bool {
	if marker != "*" && marker != "_" {
		return true
	}
	prev, _ := utf8.DecodeLastRuneInString(line[:index])
	next, _ := utf8.DecodeRuneInString(line[index+len(marker):])
	return !(isEmphasisWordRune(prev) && isEmphasisWordRune(next))
}

func isEmphasisWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// renderTerminalLinks 将裸 http(s)/file URL 和 Markdown 链接转换为 OSC 8
// 终端超链接。目标可以是网页 URL、file:// URL 或本地绝对路径
// （[label](/abs/path)），点击后打开浏览器或系统默认程序打开该文件。
// 可见文本使用独立颜色、粗体和下划线；代码片段不会调用此函数。
func renderTerminalLinks(text string) string {
	return renderTerminalLinksWithStyle(text, lipgloss.NewStyle())
}

func renderTerminalLinksWithStyle(text string, textStyle lipgloss.Style) string {
	if text == "" {
		return ""
	}

	var rendered strings.Builder
	plainStart := 0
	flushPlain := func(end int) {
		if end <= plainStart {
			return
		}
		rendered.WriteString(textStyle.Render(text[plainStart:end]))
	}

	for index := 0; index < len(text); {
		if text[index] == '[' {
			if label, target, consumed, ok := parseMarkdownTerminalLink(text[index:]); ok {
				flushPlain(index)
				rendered.WriteString(renderTerminalHyperlink(label, target))
				index += consumed
				plainStart = index
				continue
			}
		}

		if hasTerminalTargetPrefix(text[index:]) {
			target := terminalURLCandidate(text[index:])
			if isClickableTerminalTarget(target) {
				flushPlain(index)
				rendered.WriteString(renderTerminalHyperlink(target, target))
				index += len(target)
				plainStart = index
				continue
			}
		}

		_, size := utf8.DecodeRuneInString(text[index:])
		index += size
	}
	flushPlain(len(text))
	return rendered.String()
}

func renderTerminalHyperlink(label, target string) string {
	return ansi.SetHyperlink(target) +
		markdownLinkStyle.Render(label) +
		ansi.ResetHyperlink()
}

func parseMarkdownTerminalLink(text string) (label, target string, consumed int, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", 0, false
	}
	labelEndOffset := strings.Index(text[1:], "](")
	if labelEndOffset < 0 {
		return "", "", 0, false
	}
	labelEnd := labelEndOffset + 1
	label = text[1:labelEnd]
	if label == "" {
		return "", "", 0, false
	}

	targetStart := labelEnd + 2
	depth := 0
	for offset, r := range text[targetStart:] {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
				continue
			}
			target = strings.TrimSpace(text[targetStart : targetStart+offset])
			if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
				target = strings.TrimSpace(target[1 : len(target)-1])
			}
			if !isClickableTerminalTarget(target) {
				return "", "", 0, false
			}
			return label, target, targetStart + offset + 1, true
		}
	}
	return "", "", 0, false
}

func hasHTTPURLPrefix(text string) bool {
	return strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://")
}

// hasTerminalTargetPrefix 报告文本是否以可点击目标的裸前缀开头：
// http(s) URL 或 file:// URL。
func hasTerminalTargetPrefix(text string) bool {
	return hasHTTPURLPrefix(text) || strings.HasPrefix(text, "file://")
}

func terminalURLCandidate(text string) string {
	end := len(text)
	for offset, r := range text {
		if offset > 0 && terminalURLDelimiter(r) {
			end = offset
			break
		}
	}
	return trimTerminalURLPunctuation(text[:end])
}

func terminalURLDelimiter(r rune) bool {
	return unicode.IsSpace(r) ||
		unicode.IsControl(r) ||
		strings.ContainsRune("<>\"'`，。；：！？、", r)
}

func trimTerminalURLPunctuation(candidate string) string {
	const trailingPunctuation = ".,;:!?。，；：！？、"
	type bracketPair struct {
		open  string
		close string
	}
	pairs := []bracketPair{
		{open: "(", close: ")"},
		{open: "[", close: "]"},
		{open: "{", close: "}"},
	}

	for {
		trimmed := strings.TrimRightFunc(candidate, func(r rune) bool {
			return strings.ContainsRune(trailingPunctuation, r)
		})
		for _, pair := range pairs {
			if strings.HasSuffix(trimmed, pair.close) &&
				strings.Count(trimmed, pair.close) > strings.Count(trimmed, pair.open) {
				trimmed = strings.TrimSuffix(trimmed, pair.close)
				break
			}
		}
		if trimmed == candidate {
			return candidate
		}
		candidate = trimmed
	}
}

func isClickableTerminalURL(target string) bool {
	if target == "" || strings.IndexFunc(target, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}

// isClickableTerminalTarget 报告该目标是否可以渲染为可点击的终端超链接：
// 网页 URL（http/https）、file:// URL 或本地绝对路径。相对路径（如
// docs/notes.md）与裸文本不做链接，避免把正文里无意的 [x](y) 变成链接。
func isClickableTerminalTarget(target string) bool {
	if target == "" || strings.IndexFunc(target, unicode.IsControl) >= 0 {
		return false
	}
	if isClickableTerminalURL(target) {
		return true
	}
	if parsed, err := url.Parse(target); err == nil && parsed.Scheme == "file" {
		return parsed.Path != "" || parsed.Opaque != ""
	}
	return filepath.IsAbs(target)
}

// compactBlankLines 合并连续空行，避免终端历史区出现过大的空洞。
func compactBlankLines(text string) string {
	lines := strings.Split(text, "\n")
	compacted := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			compacted = append(compacted, "")
			continue
		}
		blank = false
		compacted = append(compacted, line)
	}
	return strings.TrimRight(strings.Join(compacted, "\n"), "\n")
}
