package bubble

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

type markdownTeXSpan struct {
	source, body string
	closed       bool
}

func markdownMathAt(s string, i int) (markdownTeXSpan, bool) {
	open, close := "", ""
	switch {
	case strings.HasPrefix(s[i:], `\(`):
		open, close = `\(`, `\)`
	case strings.HasPrefix(s[i:], "$") && !strings.HasPrefix(s[i:], "$$") && (i == 0 || s[i-1] != '$'):
		open, close = "$", "$"
		if i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\t' {
			return markdownTeXSpan{}, false
		}
	default:
		return markdownTeXSpan{}, false
	}
	start := i + len(open)
	if open == "$" && s[start] >= '0' && s[start] <= '9' {
		j := start
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == ',') {
			j++
		}
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j == len(s) || !strings.ContainsRune("$+-*/^_=<>\\", rune(s[j])) {
			return markdownTeXSpan{}, false
		}
	}
	for j := start; j < len(s); j++ {
		if strings.HasPrefix(s[j:], close) {
			if close == "$" && (j == start || unicode.IsSpace(rune(s[j-1])) || j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9') {
				continue
			}
			return markdownTeXSpan{source: s[i : j+len(close)], body: s[start:j], closed: true}, true
		}
		if s[j] == '\\' {
			_, j = texCommandAt(s, j)
			j--
		}
	}
	if open == "$" && s[start] >= '0' && s[start] <= '9' {
		return markdownTeXSpan{}, false
	}
	return markdownTeXSpan{source: s[i:], body: s[start:]}, true
}

func markdownCodeSpanLength(s string) (int, int) {
	if !strings.HasPrefix(s, "`") {
		return 0, 0
	}
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	marker := s[:n]
	for j := n; j < len(s); {
		at := strings.Index(s[j:], marker)
		if at < 0 {
			break
		}
		at += j
		end := at + n
		if (at == 0 || s[at-1] != '`') && (end == len(s) || s[end] != '`') {
			return end, n
		}
		j = end
	}
	return len(s), n
}

func renderInlineTeX(line string, codeStyle, highlightStyle lipgloss.Style) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		rest := line[i:]
		if n, marker := markdownCodeSpanLength(rest); n > 0 {
			body := rest[marker:n]
			if len(body) >= marker && strings.HasSuffix(body, strings.Repeat("`", marker)) {
				body = body[:len(body)-marker]
				out.WriteString(codeStyle.Render(body))
			} else {
				out.WriteString(rest[:n])
			}
			i += n
			continue
		}
		if rest[0] == '[' {
			if _, _, n, ok := parseMarkdownLinkSyntax(rest); ok {
				out.WriteString(renderTerminalLinks(rest[:n]))
				i += n
				continue
			}
		}
		if hasTerminalTargetPrefix(rest) {
			target := terminalURLCandidate(rest)
			if target != "" {
				out.WriteString(renderTerminalLinks(target))
				i += len(target)
				continue
			}
		}
		if span, ok := markdownMathAt(line, i); ok {
			text := span.source
			if span.closed {
				result := renderTeXMath(span.body, true, 0)
				if result.status == texSuccess {
					text = result.text
				}
			}
			out.WriteString(text)
			i += len(span.source)
			continue
		}
		if strings.HasPrefix(rest, `\`) && len(rest) > 1 {
			out.WriteString(rest[:2])
			i += 2
			continue
		}
		if strings.ContainsRune("*_ =", rune(rest[0])) && rest[0] != ' ' {
			if at, marker, _, ok := nextInlineMarkdownSpan(rest); ok && at == 0 {
				canOpen := marker != "_" && marker != "*" || emphasisMarkerCanOpen(line, i, marker)
				if end := strings.Index(rest[len(marker):], marker); end > 0 && canOpen {
					content := rest[len(marker) : len(marker)+end]
					style := markdownItalicStyle
					if marker == "==" {
						style = highlightStyle
					}
					if marker == "**" || marker == "__" {
						style = markdownBoldStyle
					}
					out.WriteString(style.Render(renderInlineTeX(content, codeStyle, highlightStyle)))
					i += end + 2*len(marker)
					continue
				}
			}
		}
		_, n := utf8.DecodeRuneInString(rest)
		out.WriteString(rest[:n])
		i += n
	}
	return out.String()
}

func splitMarkdownTeXTable(line string) []string {
	parts := []string{}
	start := 0
	for i := 0; i < len(line); {
		if n, _ := markdownCodeSpanLength(line[i:]); n > 0 {
			i += n
			continue
		}
		if line[i] == '[' {
			if _, _, n, ok := parseMarkdownLinkSyntax(line[i:]); ok {
				i += n
				continue
			}
		}
		if span, ok := markdownMathAt(line, i); ok && span.closed {
			i += len(span.source)
			continue
		}
		if line[i] == '\\' && i+1 < len(line) {
			i += 2
			continue
		}
		if line[i] == '|' {
			parts = append(parts, line[start:i])
			start = i + 1
		}
		i++
	}
	return append(parts, line[start:])
}

func renderTeXLiteral(source string, width int) string {
	return strings.Join(wrapStyledCellText(source, maxInt(1, width)), "\n")
}

func styleTeXBlock(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = bodyStyle.Render(restoreForegroundAfterANSIReset(line, colorManager.Hex(colorBody)))
	}
	return strings.Join(lines, "\n")
}

func renderTeXBlockBody(source, original string, width int) string {
	if strings.HasPrefix(strings.TrimSpace(source), `\begin{tikzcd}`) || strings.HasPrefix(strings.TrimSpace(source), `\begin{tikzpicture}`) {
		return renderTikZBlock(source, original, width)
	}
	result := renderTeXMath(source, false, width)
	if result.status == texSuccess {
		return result.text
	}
	if result.status == texOverflow {
		return renderTeXLiteral(result.text, width)
	}
	return renderTeXLiteral(original, width)
}

func renderMarkdownTeXBlock(lines []string, start, width int) (string, int, bool) {
	trimmed := strings.TrimSpace(lines[start])
	open, close := "", ""
	switch {
	case strings.HasPrefix(trimmed, "$$"):
		open, close = "$$", "$$"
	case strings.HasPrefix(trimmed, `\[`):
		open, close = `\[`, `\]`
	case strings.HasPrefix(trimmed, `\begin{tikzcd}`):
		open, close = `\begin{tikzcd}`, `\end{tikzcd}`
	case strings.HasPrefix(trimmed, `\begin{tikzpicture}`):
		open, close = `\begin{tikzpicture}`, `\end{tikzpicture}`
	default:
		return "", start, false
	}
	var text strings.Builder
	for end := start; end < len(lines); end++ {
		segment := strings.TrimPrefix(trimmed, open)
		if end > start {
			text.WriteByte('\n')
			segment = lines[end]
		}
		if at := strings.Index(segment, close); at >= 0 {
			text.WriteString(segment[:at])
			body := text.String()
			content := body
			if strings.HasPrefix(open, `\begin{`) {
				content = open + content + close
			}
			rendered := renderTeXBlockBody(content, open+body+close, width)
			if tail := strings.TrimSpace(segment[at+len(close):]); tail != "" {
				rendered += "\n" + renderMarkdown(tail, width)
			}
			return rendered, end, true
		}
		text.WriteString(segment)
	}
	return renderTeXLiteral(strings.Join(lines[start:], "\n"), width), len(lines) - 1, true
}

func renderExtendedTeXFence(lines []string, start, width int) (string, int, bool) {
	first := strings.TrimSpace(lines[start])
	if len(first) < 3 || (first[0] != '`' && first[0] != '~') {
		return "", start, false
	}
	n := 0
	for n < len(first) && first[n] == first[0] {
		n++
	}
	if n < 3 || first[0] == '`' && n == 3 {
		return "", start, false
	}
	marker, lang := first[:n], strings.TrimSpace(first[n:])
	end := start + 1
	for ; end < len(lines); end++ {
		trimmed := strings.TrimSpace(lines[end])
		if strings.HasPrefix(trimmed, marker) && strings.Trim(trimmed, string(first[0])) == "" {
			break
		}
	}
	body := strings.Join(lines[start+1:end], "\n")
	last := minInt(end, len(lines)-1)
	if lang == "tikzcd" {
		if end < len(lines) {
			return styleTeXBlock(renderTikZBlock(body, body, width)), last, true
		}
		return styleTeXBlock(renderTeXLiteral(body, width)), last, true
	}
	return renderCodeBlock(lang, body, width), last, true
}
