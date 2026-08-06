// 本文件实现面向终端聊天区的轻量 Markdown 渲染器。
package bubble

import (
	"net/url"
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
			parts = append(parts, leading+markdownQuoteStyle.Width(maxInt(1, width-2)).Render(renderInlineMarkdown(strings.TrimSpace(text))))
			continue
		}
		if marker, text, ok := markdownListItem(trimmed); ok {
			body := renderInlineMarkdown(text)
			parts = append(parts, leading+markdownBulletStyle.Render(marker)+" "+bodyStyle.Width(maxInt(1, width-terminalCellWidth(marker)-1)).Render(body))
			continue
		}

		parts = append(parts, bodyStyle.Width(width).Render(wrapCompact(leading+renderInlineMarkdown(strings.TrimRight(line, " \t")), width)))
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
			cell = renderInlineMarkdown(row[i])
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
	text = renderInlineMarkdown(text)
	if level == 1 {
		rule := strings.Repeat("─", maxInt(8, minInt(width, terminalCellWidth(text)+4)))
		return markdownHeadingStyle.Render(text) + "\n" + markdownRuleStyle.Render(rule)
	}
	return markdownHeadingStyle.Render(text)
}

// markdownListItem 解析无序、有序和任务列表项，并统一转换为终端 bullet。
func markdownListItem(line string) (string, string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if text, ok := strings.CutPrefix(line, prefix); ok {
			return markdownTaskMarker(text)
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

// markdownTaskMarker 将 Markdown task-list 的方括号转换为终端可读的状态符号，
// 避免把 [ ]/[x] 当成普通正文渲染成截图中的“• ]”。
func markdownTaskMarker(text string) (string, string, bool) {
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
	return "•", text, true
}

// renderInlineMarkdown 渲染行内 Markdown，反引号代码片段优先于粗体解析。
func renderInlineMarkdown(line string) string {
	var rendered strings.Builder
	for line != "" {
		codeStart := strings.Index(line, "`")
		boldStart := strings.Index(line, "**")
		switch {
		case codeStart == -1 && boldStart == -1:
			rendered.WriteString(renderTerminalLinks(line))
			return rendered.String()
		case codeStart != -1 && (boldStart == -1 || codeStart < boldStart):
			rendered.WriteString(renderTerminalLinks(line[:codeStart]))
			line = line[codeStart+1:]
			end := strings.Index(line, "`")
			if end == -1 {
				rendered.WriteString("`")
				rendered.WriteString(renderTerminalLinks(line))
				return rendered.String()
			}
			rendered.WriteString(markdownCodeStyle.Render(line[:end]))
			line = line[end+1:]
		default:
			rendered.WriteString(renderTerminalLinks(line[:boldStart]))
			line = line[boldStart+2:]
			end := strings.Index(line, "**")
			if end <= 0 {
				rendered.WriteString("**")
				rendered.WriteString(renderTerminalLinks(line))
				return rendered.String()
			}
			rendered.WriteString(renderTerminalLinksWithStyle(line[:end], markdownBoldStyle))
			line = line[end+2:]
		}
	}
	return rendered.String()
}

// renderTerminalLinks 将裸 http(s) URL 和 Markdown 链接转换为 OSC 8 终端超链接。
// URL 的可见文本使用独立颜色、粗体和下划线；代码片段不会调用此函数。
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

		if hasHTTPURLPrefix(text[index:]) {
			target := terminalURLCandidate(text[index:])
			if isClickableTerminalURL(target) {
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
			if !isClickableTerminalURL(target) {
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
