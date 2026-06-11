package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
	"unicode"
)

func renderMarkdown(markdown string, width int) string {
	width = maxInt(20, width)
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
			parts = append(parts, markdownQuoteStyle.Width(width-2).Render(renderInlineMarkdown(strings.TrimSpace(text))))
			continue
		}
		if marker, text, ok := markdownListItem(trimmed); ok {
			body := renderInlineMarkdown(text)
			parts = append(parts, markdownBulletStyle.Render(marker)+" "+bodyStyle.Width(width-lipgloss.Width(marker)-1).Render(body))
			continue
		}

		parts = append(parts, bodyStyle.Width(width).Render(renderInlineMarkdown(trimmed)))
	}

	return compactBlankLines(strings.TrimRight(strings.Join(parts, "\n"), "\n"))
}

func fencedCodeStart(line string) (string, bool) {
	lang, ok := strings.CutPrefix(line, "```")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(lang), true
}

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

func isMarkdownFenceLanguage(lang string) bool {
	lang = strings.ToLower(strings.TrimSpace(lang))
	return lang == "markdown" || lang == "md"
}

func renderCodeBlock(lang, code string, width int) string {
	label := "code"
	if lang != "" {
		label = "code " + lang
	}
	body := strings.TrimRight(code, "\n")
	if body == "" {
		body = " "
	}
	block := markdownCodeBlockStyle.Width(maxInt(20, width-2)).Render(body)
	return markdownBulletStyle.Render(label) + "\n" + block
}

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

func isMarkdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}
	cell = strings.Trim(cell, ":")
	if len(cell) < 3 {
		return false
	}
	for _, r := range cell {
		if r != '-' {
			return false
		}
	}
	return true
}

func markdownTableColumnCount(rows [][]string) int {
	count := 0
	for _, row := range rows {
		count = maxInt(count, len(row))
	}
	return count
}

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

func markdownTableColumnWidths(rows [][]string, columnCount, maxWidth int) []int {
	widths := make([]int, columnCount)
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = maxInt(widths[i], lipgloss.Width(renderInlineMarkdown(cell)))
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
		if widths[widest] <= 4 {
			break
		}
		widths[widest]--
	}
	for i := range widths {
		widths[i] = maxInt(1, widths[i])
	}
	return widths
}

func markdownTableTotalWidth(widths []int) int {
	total := 0
	for _, width := range widths {
		total += width
	}
	return total
}

func renderMarkdownTableRow(row []string, widths []int, header bool) string {
	cells := make([]string, 0, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = renderInlineMarkdown(row[i])
		}
		cell = truncateDisplayWidth(cell, width)
		padded := cell + strings.Repeat(" ", maxInt(0, width-lipgloss.Width(cell)))
		if header {
			padded = markdownHeadingStyle.Render(padded)
		}
		cells = append(cells, padded)
	}
	return strings.Join(cells, markdownRuleStyle.Render(" │ "))
}

func renderMarkdownTableRule(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("─", width))
	}
	return strings.Join(parts, "─┼─")
}

func truncateDisplayWidth(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	if len(runes) == 0 {
		return "…"
	}
	return string(runes) + "…"
}

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

func renderMarkdownHeading(level int, text string, width int) string {
	text = renderInlineMarkdown(text)
	if level == 1 {
		rule := strings.Repeat("─", maxInt(8, minInt(width, lipgloss.Width(text)+4)))
		return markdownHeadingStyle.Render(text) + "\n" + markdownRuleStyle.Render(rule)
	}
	prefix := strings.Repeat("#", minInt(level, 3))
	return markdownHeadingStyle.Render(prefix + " " + text)
}

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

func renderInlineMarkdown(line string) string {
	var rendered strings.Builder
	for {
		start := strings.Index(line, "`")
		if start == -1 {
			rendered.WriteString(line)
			return rendered.String()
		}
		rendered.WriteString(line[:start])
		line = line[start+1:]
		end := strings.Index(line, "`")
		if end == -1 {
			rendered.WriteString("`")
			rendered.WriteString(line)
			return rendered.String()
		}
		rendered.WriteString(markdownCodeStyle.Render(line[:end]))
		line = line[end+1:]
	}
}

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
