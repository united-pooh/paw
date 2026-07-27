// 本文件实现面向终端聊天区的轻量 Markdown 渲染器。
package bubble

import (
	"strconv"
	"strings"
	"unicode"

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
		if level, text, ok := markdownHeading(trimmed); ok {
			parts = append(parts, renderMarkdownHeading(level, text, width))
			continue
		}
		if text, ok := strings.CutPrefix(trimmed, ">"); ok {
			parts = append(parts, markdownQuoteStyle.Width(maxInt(1, width-2)).Render(renderInlineMarkdown(strings.TrimSpace(text))))
			continue
		}
		if marker, text, ok := markdownListItem(trimmed); ok {
			body := renderInlineMarkdown(text)
			parts = append(parts, markdownBulletStyle.Render(marker)+" "+bodyStyle.Width(maxInt(1, width-lipgloss.Width(marker)-1)).Render(body))
			continue
		}

		parts = append(parts, bodyStyle.Width(width).Render(renderInlineMarkdown(trimmed)))
	}

	return compactBlankLines(strings.TrimRight(strings.Join(parts, "\n"), "\n"))
}

// fencedCodeStart 判断一行是否开启 fenced code block，并返回语言标签。
func fencedCodeStart(line string) (string, bool) {
	lang, ok := strings.CutPrefix(line, "```")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(lang), true
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
				return codeLines, i
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

// renderCodeBlock 渲染代码块，并把语言标签放在代码块外部。
func renderCodeBlock(lang, code string, width int) string {
	width = maxInt(1, width)
	label := "code"
	if lang != "" {
		label = lang
	}
	body := strings.TrimRight(code, "\n")
	if body == "" {
		body = " "
	}
	block := renderCodeBlockPanel(body, width)
	return markdownBulletStyle.Render(truncateDisplayWidth(label, width)) + "\n" + block
}

func renderCodeBlockPanel(code string, width int) string {
	width = maxInt(1, width)
	blockWidth := markdownCodeBlockWidth(code, width)
	if blockWidth < 6 {
		lines := limitRenderedCodeBlockLines(strings.Split(wrapDisplayWidthLines(code, width), "\n"), maxRenderedCodeBlockLines)
		return markdownCodeBlockStyle.Render(strings.Join(lines, "\n"))
	}
	textWidth := maxInt(1, blockWidth-4)
	lines := limitRenderedCodeBlockLines(strings.Split(wrapDisplayWidthLines(code, textWidth), "\n"), maxRenderedCodeBlockLines)
	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, renderCodeBlockBorderLine("┌", "─", "┐", blockWidth))
	for _, line := range lines {
		body := " " + markdownCodeBlockStyle.Render(padDisplayWidth(line, textWidth)) + " "
		rendered = append(rendered,
			markdownCodeBlockBorderStyle.Render("│")+body+markdownCodeBlockBorderStyle.Render("│"),
		)
	}
	rendered = append(rendered, renderCodeBlockBorderLine("└", "─", "┘", blockWidth))
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

func markdownCodeBlockWidth(code string, width int) int {
	maxWidth := maxInt(6, width-4)
	widest := 1
	for _, line := range strings.Split(code, "\n") {
		widest = maxInt(widest, terminalCellWidth(line))
	}
	return minInt(maxWidth, maxInt(6, widest+4))
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
	widths := markdownTableColumnWidths(rows, columnCount, width)
	renderedRows := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		renderedRows = append(renderedRows, renderMarkdownTableRow(row, widths, i == 0))
		if i == 0 {
			renderedRows = append(renderedRows, markdownRuleStyle.Render(renderMarkdownTableRule(widths)))
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

func markdownTableMaxColumnsForWidth(width int) int {
	width = maxInt(1, width)
	return maxInt(1, (width+3)/4)
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
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = maxInt(widths[i], terminalCellWidth(renderInlineMarkdown(cell)))
		}
	}
	available := maxInt(columnCount, maxWidth-(columnCount-1)*3)
	for markdownTableTotalWidth(widths) > available {
		widest := 0
		for i := range widths {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}
	for i := range widths {
		widths[i] = maxInt(1, widths[i])
	}
	return widths
}

// markdownTableTotalWidth 计算所有列宽之和，不包含列间分隔符。
func markdownTableTotalWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

// renderMarkdownTableRow 渲染一行表格，并对表头应用强调样式。
func renderMarkdownTableRow(row []string, widths []int, header bool) string {
	cells := make([]string, 0, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = renderInlineMarkdown(row[i])
		}
		cell = truncateDisplayWidth(cell, width)
		padded := cell + strings.Repeat(" ", maxInt(0, width-terminalCellWidth(cell)))
		if header {
			padded = markdownHeadingStyle.Render(padded)
		}
		cells = append(cells, padded)
	}
	return strings.Join(cells, "  ")
}

// renderMarkdownTableRule 渲染表头和正文之间的横向分隔线。
func renderMarkdownTableRule(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("─", width))
	}
	return strings.Join(parts, "  ")
}

// truncateDisplayWidth 按终端显示宽度截断文本，并在末尾添加省略号。
func truncateDisplayWidth(text string, width int) string {
	width = maxInt(1, width)
	if terminalCellWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "…")
}

func wrapDisplayWidthLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapDisplayWidthLine(line, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func wrapDisplayWidthLine(text string, width int) []string {
	width = maxInt(1, width)
	if text == "" {
		return []string{""}
	}
	if terminalCellWidth(text) <= width {
		return []string{text}
	}

	var lines []string
	var chunk strings.Builder
	chunkWidth := 0
	for remaining := text; remaining != ""; {
		cluster, clusterWidth := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		if clusterWidth <= 0 {
			chunk.WriteString(cluster)
			continue
		}
		if chunkWidth > 0 && chunkWidth+clusterWidth > width {
			lines = append(lines, chunk.String())
			chunk.Reset()
			chunkWidth = 0
		}
		if clusterWidth > width {
			lines = append(lines, truncateDisplayWidth(cluster, width))
			continue
		}
		chunk.WriteString(cluster)
		chunkWidth += clusterWidth
	}
	if chunk.Len() > 0 || len(lines) == 0 {
		lines = append(lines, chunk.String())
	}
	return lines
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

// renderMarkdownHeading 根据标题等级渲染标题，一级标题会额外带下划线。
func renderMarkdownHeading(level int, text string, width int) string {
	text = renderInlineMarkdown(text)
	if level == 1 {
		rule := strings.Repeat("─", maxInt(8, minInt(width, terminalCellWidth(text)+4)))
		return markdownHeadingStyle.Render(text) + "\n" + markdownRuleStyle.Render(rule)
	}
	prefix := strings.Repeat("#", minInt(level, 3))
	return markdownHeadingStyle.Render(prefix + " " + text)
}

// markdownListItem 解析无序和有序列表项，并统一转换为终端 bullet。
func markdownListItem(line string) (string, string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if text, ok := strings.CutPrefix(line, prefix); ok {
			return "•", strings.TrimSpace(text), true
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
	return "•", strings.TrimSpace(line[dot+2:]), true
}

// renderInlineMarkdown 渲染行内 Markdown，反引号代码片段优先于粗体解析。
func renderInlineMarkdown(line string) string {
	var rendered strings.Builder
	for line != "" {
		codeStart := strings.Index(line, "`")
		boldStart := strings.Index(line, "**")
		switch {
		case codeStart == -1 && boldStart == -1:
			rendered.WriteString(line)
			return rendered.String()
		case codeStart != -1 && (boldStart == -1 || codeStart < boldStart):
			rendered.WriteString(line[:codeStart])
			line = line[codeStart+1:]
			end := strings.Index(line, "`")
			if end == -1 {
				rendered.WriteString("`")
				rendered.WriteString(line)
				return rendered.String()
			}
			rendered.WriteString(markdownCodeStyle.Render(line[:end]))
			line = line[end+1:]
		default:
			rendered.WriteString(line[:boldStart])
			line = line[boldStart+2:]
			end := strings.Index(line, "**")
			if end <= 0 {
				rendered.WriteString("**")
				rendered.WriteString(line)
				return rendered.String()
			}
			rendered.WriteString(markdownBoldStyle.Render(line[:end]))
			line = line[end+2:]
		}
	}
	return rendered.String()
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
