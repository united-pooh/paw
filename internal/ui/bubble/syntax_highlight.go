package bubble

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// syntaxLanguageFromText infers a small, dependency-free syntax profile from a
// tool hint, file path, or the content itself. It intentionally stays
// conservative: unknown output is rendered unchanged rather than guessed.
func syntaxLanguageFromText(text, hint string) string {
	hint = strings.ToLower(strings.TrimSpace(hint))
	if hint != "" {
		hint = strings.TrimPrefix(hint, ".")
		switch hint {
		case "go", "golang", "js", "javascript", "jsx", "ts", "typescript", "tsx", "py", "python", "rb", "ruby", "rs", "rust", "java", "c", "cpp", "cxx", "h", "hpp", "json", "yaml", "yml", "toml", "sh", "bash", "zsh", "fish", "sql", "md", "markdown", "html", "css", "xml":
			if hint == "golang" {
				return "go"
			}
			if hint == "javascript" {
				return "js"
			}
			if hint == "typescript" {
				return "ts"
			}
			if hint == "python" {
				return "py"
			}
			if hint == "ruby" {
				return "rb"
			}
			if hint == "rust" {
				return "rs"
			}
			return hint
		}
		base := strings.ToLower(filepath.Base(hint))
		if ext := strings.TrimPrefix(filepath.Ext(base), "."); ext != "" {
			return syntaxLanguageFromText("", ext)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return "go"
		}
		if strings.HasPrefix(trimmed, "#!") && (strings.Contains(trimmed, "sh") || strings.Contains(trimmed, "bash")) {
			return "sh"
		}
		if strings.HasPrefix(trimmed, "{\"") || strings.HasPrefix(trimmed, "[{") {
			return "json"
		}
	}
	return ""
}

func syntaxLanguageFromLines(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "..." || strings.HasPrefix(trimmed, "@@ ") {
			continue
		}
		if fields := strings.Fields(trimmed); len(fields) >= 2 {
			for _, field := range fields {
				if strings.Contains(field, ".") && !strings.Contains(field, "…") {
					if lang := syntaxLanguageFromText("", strings.Trim(field, "`'\"()[]{}:,")); lang != "" {
						return lang
					}
				}
			}
		}
	}
	return syntaxLanguageFromText(strings.Join(lines, "\n"), "")
}

var syntaxKeywords = map[string]map[string]struct{}{
	"go":   keywordSet("break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var", "nil", "true", "false", "error"),
	"js":   keywordSet("break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "else", "export", "extends", "finally", "for", "function", "if", "import", "in", "instanceof", "let", "new", "return", "switch", "this", "throw", "try", "typeof", "var", "void", "while", "with", "yield", "true", "false", "null", "undefined"),
	"ts":   keywordSet("break", "case", "catch", "class", "const", "continue", "default", "else", "export", "extends", "for", "function", "if", "import", "interface", "let", "namespace", "new", "return", "switch", "this", "throw", "type", "typeof", "var", "while", "async", "await", "true", "false", "null", "undefined"),
	"py":   keywordSet("and", "as", "assert", "async", "await", "break", "case", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "match", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield", "True", "False", "None"),
	"rs":   keywordSet("as", "async", "await", "break", "const", "continue", "crate", "else", "enum", "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "trait", "true", "type", "unsafe", "use", "where", "while"),
	"java": keywordSet("abstract", "assert", "boolean", "break", "case", "catch", "class", "const", "continue", "default", "do", "else", "enum", "extends", "final", "finally", "for", "if", "implements", "import", "instanceof", "interface", "native", "new", "package", "private", "protected", "public", "return", "static", "super", "switch", "this", "throw", "throws", "try", "void", "volatile", "while", "true", "false", "null"),
	"c":    keywordSet("auto", "break", "case", "char", "const", "continue", "default", "do", "else", "enum", "extern", "for", "goto", "if", "inline", "int", "long", "register", "return", "short", "signed", "sizeof", "static", "struct", "switch", "typedef", "union", "unsigned", "void", "volatile", "while", "true", "false", "NULL"),
	"cpp":  keywordSet("alignas", "auto", "bool", "break", "case", "catch", "class", "const", "constexpr", "continue", "default", "delete", "do", "else", "enum", "explicit", "extern", "false", "for", "if", "inline", "namespace", "new", "nullptr", "operator", "private", "protected", "public", "return", "sizeof", "static", "struct", "switch", "template", "this", "throw", "true", "try", "typedef", "typename", "union", "using", "virtual", "void", "while"),
	"json": keywordSet("true", "false", "null"),
	"sh":   keywordSet("case", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in", "select", "then", "until", "while", "export", "local", "return", "true", "false"),
	"sql":  keywordSet("select", "from", "where", "and", "or", "insert", "into", "update", "delete", "create", "alter", "drop", "join", "left", "right", "inner", "outer", "on", "as", "group", "by", "order", "having", "limit", "offset", "values", "set", "null", "is", "not", "distinct"),
	"yaml": keywordSet("true", "false", "null", "yes", "no"),
	"yml":  keywordSet("true", "false", "null", "yes", "no"),
}

func keywordSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func highlightToolDetailLine(line, language string) string {
	return highlightToolDetailLineWithBase(line, language, lipgloss.Style{})
}

// highlightToolDetailLineWithBase 与 highlightToolDetailLine 相同，但每个行内
// token 段都会叠加 base 样式（背景、粗体等）。行级样式（如 diff 行的背景色）
// 由外层 Render 设置，而子样式渲染自带尾部 reset（\x1b[0m），会清掉背景，
// 导致 reset 之后的文本失去背景。让每个 token 段自带 base 属性后，reset 只
// 影响本段，下一段自行恢复背景和粗体。
func highlightToolDetailLineWithBase(line, language string, base lipgloss.Style) string {
	keywords := syntaxKeywords[language]
	if len(keywords) == 0 && base.GetBackground() == nil && !base.GetBold() {
		return line
	}
	var out strings.Builder
	bracketDepth := 0
	for i := 0; i < len(line); {
		if (language == "go" || language == "js" || language == "ts" || language == "java" || language == "c" || language == "cpp" || language == "rs") && i+1 < len(line) && line[i:i+2] == "//" {
			out.WriteString(withBaseStyle(syntaxCommentStyle, base).Render(line[i:]))
			break
		}
		if (language == "py" || language == "sh" || language == "yaml" || language == "yml" || language == "toml") && line[i] == '#' {
			out.WriteString(withBaseStyle(syntaxCommentStyle, base).Render(line[i:]))
			break
		}
		if language == "sql" && i+1 < len(line) && line[i:i+2] == "--" {
			out.WriteString(withBaseStyle(syntaxCommentStyle, base).Render(line[i:]))
			break
		}
		if line[i] == '"' || line[i] == '\'' || line[i] == '`' {
			quote := line[i]
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == quote {
					j++
					break
				}
				j++
			}
			out.WriteString(withBaseStyle(syntaxStringStyle, base).Render(line[i:j]))
			i = j
			continue
		}
		if isSyntaxOpeningBracket(line[i]) {
			out.WriteString(withBaseStyle(syntaxBracketStyle(bracketDepth), base).Render(string(line[i])))
			bracketDepth++
			i++
			continue
		}
		if isSyntaxClosingBracket(line[i]) {
			bracketDepth = maxInt(0, bracketDepth-1)
			out.WriteString(withBaseStyle(syntaxBracketStyle(bracketDepth), base).Render(string(line[i])))
			i++
			continue
		}
		cluster, _ := terminalFirstGraphemeCluster(line[i:])
		firstRune, _ := utf8.DecodeRuneInString(cluster)
		if unicode.IsDigit(firstRune) {
			j := i
			for j < len(line) {
				candidate, _ := terminalFirstGraphemeCluster(line[j:])
				candidateRune, _ := utf8.DecodeRuneInString(candidate)
				if !unicode.IsDigit(candidateRune) && !strings.ContainsRune("._", candidateRune) {
					break
				}
				j += len(candidate)
			}
			out.WriteString(withBaseStyle(syntaxNumberStyle, base).Render(line[i:j]))
			i = j
			continue
		}
		if unicode.IsLetter(firstRune) || firstRune == '_' {
			j := i
			for j < len(line) {
				candidate, _ := terminalFirstGraphemeCluster(line[j:])
				candidateRune, _ := utf8.DecodeRuneInString(candidate)
				if !unicode.IsLetter(candidateRune) && !unicode.IsDigit(candidateRune) && candidateRune != '_' {
					break
				}
				j += len(candidate)
			}
			word := line[i:j]
			if _, ok := keywords[word]; ok {
				out.WriteString(withBaseStyle(syntaxKeywordStyle, base).Render(word))
			} else {
				out.WriteString(renderWithBase(word, base))
			}
			i = j
			continue
		}
		if cluster == "" {
			cluster = line[i : i+1]
		}
		out.WriteString(renderWithBase(cluster, base))
		i += len(cluster)
	}
	return out.String()
}

// withBaseStyle 将 base 的背景和粗体叠加到 sub 上，返回新样式（不修改原样式）。
func withBaseStyle(sub, base lipgloss.Style) lipgloss.Style {
	if bg := base.GetBackground(); bg != nil {
		sub = sub.Background(bg)
	}
	if base.GetBold() {
		sub = sub.Bold(true)
	}
	return sub
}

// renderWithBase 用 base 渲染普通文本段；base 未设置任何属性时原样返回，
// 保持与直接写入文本相同的行为和开销。
func renderWithBase(text string, base lipgloss.Style) string {
	if base.GetBackground() == nil && !base.GetBold() && base.GetForeground() == nil {
		return text
	}
	return base.Render(text)
}

func syntaxBracketStyle(depth int) lipgloss.Style {
	if len(syntaxBracketStyles) == 0 {
		return lipgloss.NewStyle()
	}
	return syntaxBracketStyles[depth%len(syntaxBracketStyles)]
}

func isSyntaxOpeningBracket(ch byte) bool {
	return ch == '(' || ch == '[' || ch == '{'
}

func isSyntaxClosingBracket(ch byte) bool {
	return ch == ')' || ch == ']' || ch == '}'
}

// utf8DecodeRune keeps this file independent of a lexer package while still
// advancing by complete UTF-8 runes for non-ASCII source text.
func utf8DecodeRune(text string) (rune, int) {
	for size := 1; size <= len(text); size++ {
		if size == 1 || text[size-1]&0xc0 != 0x80 {
			return rune(text[0]), size
		}
	}
	return rune(text[0]), 1
}
