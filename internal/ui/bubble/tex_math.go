package bubble

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/doug/termtex"
)

type texStatus uint8

const (
	texSuccess texStatus = iota
	texIncomplete
	texUnsupported
	texOverflow
)

type texResult struct {
	text   string
	status texStatus
	reason string
}

const maxTeXBytes = 8192

func tuiTeXEnabled() bool { return os.Getenv("PAW_TUI_TEX") != "0" }

// Keep this allowlist narrower than termtex: its unknown-command fallback drops
// the backslash and can silently change the meaning of an unsupported formula.
var texCommands = func() map[string]bool {
	names := `frac dfrac tfrac binom sqrt begin end left right big Big bigg Bigg bigl bigr Bigl Bigr biggl biggr Biggl Biggr
 alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega Gamma Delta Theta Lambda Xi Pi Sigma Upsilon Phi Psi Omega varepsilon vartheta varphi varpi varrho varsigma
 mathbb mathcal mathscr mathbf boldsymbol bm mathfrak mathsf mathtt mathit mathrm text textrm textbf textit textsf texttt mbox operatorname
 sum prod coprod int iint iiint oint bigcup bigcap bigoplus bigotimes bigvee bigwedge lim limsup liminf max min sup inf argmax argmin limits nolimits
 pm mp times div cdot ast star circ bullet oplus ominus otimes oslash odot setminus bmod pmod
 leq le geq ge neq ne approx equiv sim simeq cong propto doteq asymp ll gg prec succ preceq succeq models vdash dashv perp parallel mid nmid lt gt therefore because coloneqq not
 to gets rightarrow leftarrow leftrightarrow Rightarrow Leftarrow Leftrightarrow iff implies impliedby longrightarrow longleftarrow mapsto longmapsto hookrightarrow twoheadrightarrow uparrow downarrow nearrow searrow
in notin ni subset subseteq subsetneq supset supseteq supsetneq cup cap emptyset varnothing forall exists neg lnot land wedge lor vee top bot
 partial nabla infty hbar hslash aleph beth Re Im wp ell imath jmath angle triangle square degree prime dagger ddagger
 langle rangle lfloor rfloor lceil rceil lbrace rbrace lvert rvert vert lVert rVert Vert colon
 sin cos tan cot sec csc arcsin arccos arctan sinh cosh tanh log ln lg exp det dim ker deg hom gcd lcm arg sgn tr rank Pr
 quad qquad thinspace enspace negthinspace ldots dots cdots vdots ddots iddots dotsc dotsb
 hat bar vec dot ddot tilde breve check acute grave mathring overline underline widehat widetilde overrightarrow overleftarrow
 overset underset stackrel overbrace underbrace substack phantom xrightarrow xleftarrow
 displaystyle textstyle scriptstyle scriptscriptstyle notag label`
	out := make(map[string]bool)
	for _, n := range strings.Fields(names) {
		out[n] = true
	}
	return out
}()

var texEnvironments = map[string]bool{"matrix": true, "pmatrix": true, "bmatrix": true, "Bmatrix": true, "vmatrix": true, "Vmatrix": true, "aligned": true, "align": true, "align*": true, "gather": true, "gathered": true, "split": true, "cases": true, "array": true}

func texLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func texCommandAt(s string, i int) (string, int) {
	j := i + 1
	if j >= len(s) {
		return "", j
	}
	if !texLetter(s[j]) {
		return s[j : j+1], j + 1
	}
	for j < len(s) && texLetter(s[j]) {
		j++
	}
	return s[i+1 : j], j
}

func texGroupAt(s string, i int) (string, int, bool) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\n' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", i, false
	}
	start, depth := i+1, 1
	for i++; i < len(s); i++ {
		if s[i] == '\\' {
			_, i = texCommandAt(s, i)
			i--
			continue
		}
		if s[i] == '{' {
			depth++
		}
		if s[i] == '}' {
			depth--
			if depth == 0 {
				return s[start:i], i + 1, true
			}
		}
	}
	return "", len(s), false
}

func validateTeX(s string) texResult {
	if len(s) > maxTeXBytes {
		return texResult{status: texUnsupported, reason: "公式超过长度限制"}
	}
	depth, commands, rows, delimiters := 0, 0, 1, 0
	var envs []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '{':
			depth++
			if depth > 32 {
				return texResult{status: texUnsupported, reason: "公式嵌套过深"}
			}
			i++
		case '}':
			depth--
			if depth < 0 {
				return texResult{status: texUnsupported, reason: "公式括号不匹配"}
			}
			i++
		case '\\':
			name, end := texCommandAt(s, i)
			i = end
			commands++
			if name == "" {
				return texResult{status: texIncomplete}
			}
			if name == "\\" {
				rows++
			}
			if !texCommands[name] && !strings.Contains("{}[]|,;:! %_$&#\\", name) {
				return texResult{status: texUnsupported, reason: "不支持命令 \\" + name}
			}
			if name == "left" {
				delimiters++
			}
			if name == "right" {
				delimiters--
				if delimiters < 0 {
					return texResult{status: texUnsupported, reason: "公式定界符不匹配"}
				}
			}
			if name == "text" || name == "textrm" || name == "textbf" || name == "textit" || name == "textsf" || name == "texttt" || name == "mbox" {
				if _, _, ok := texGroupAt(s, i); !ok {
					return texResult{status: texIncomplete, reason: "文字参数未闭合"}
				}
			}
			if name == "begin" || name == "end" {
				env, next, ok := texGroupAt(s, i)
				if !ok {
					return texResult{status: texIncomplete}
				}
				if !texEnvironments[env] {
					return texResult{status: texUnsupported, reason: "不支持环境 " + env}
				}
				i = next
				if name == "begin" {
					envs = append(envs, env)
				} else {
					if len(envs) == 0 || envs[len(envs)-1] != env {
						return texResult{status: texUnsupported, reason: "公式环境不匹配"}
					}
					envs = envs[:len(envs)-1]
				}
				if len(envs) > 16 {
					return texResult{status: texUnsupported, reason: "公式环境嵌套过深"}
				}
			}
		default:
			if s[i] < 32 && s[i] != '\n' && s[i] != '\t' {
				return texResult{status: texUnsupported, reason: "公式含控制字符"}
			}
			_, n := utf8.DecodeRuneInString(s[i:])
			i += n
		}
		if commands > 512 || rows > 128 {
			return texResult{status: texUnsupported, reason: "公式超过排版预算"}
		}
	}
	if depth != 0 || len(envs) != 0 || delimiters != 0 {
		return texResult{status: texIncomplete}
	}
	// Bound the canvas before invoking a library which allocates width × height.
	if utf8.RuneCountInString(s)*2*(2*commands+rows+1) > 200000 {
		return texResult{status: texUnsupported, reason: "公式超过排版预算"}
	}
	return texResult{status: texSuccess}
}

func renderTeXMath(source string, inline bool, width int) texResult {
	check := validateTeX(source)
	if check.status != texSuccess {
		return check
	}
	// Upstream measures runes rather than Paw's grapheme cells.
	if !texWidthsCompatible(source) {
		return texResult{status: texUnsupported, reason: "公式包含不支持的字符宽度"}
	}
	s := texScriptedBars(source)
	if inline {
		s = inlineTeXSource(s)
	}
	out, err := termtex.Render(s, termtex.Style{Inline: inline, Width: width})
	if err != nil {
		return texResult{status: texUnsupported, reason: "公式语法不支持或不完整"}
	}
	fits := true
	for _, line := range strings.Split(out, "\n") {
		if width > 0 && terminalCellWidth(line) > width {
			fits = false
		}
	}
	if inline && strings.Contains(out, "\n") || !inline && !fits {
		flat, ok := linearTeX(source)
		if !ok {
			return texResult{status: texUnsupported, reason: "公式无法无损转换为单行"}
		}
		status := texSuccess
		if !inline {
			status = texOverflow
		}
		return texResult{text: flat, status: status}
	}
	return texResult{text: out, status: texSuccess}
}

// termtex rejects scripts on operator atoms, including a standalone bar.
// Keep paired absolute-value delimiters and text arguments untouched.
func texScriptedBars(s string) string {
	if !strings.Contains(s, "|") {
		return s
	}
	var b strings.Builder
	open := []bool{false}
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			name, end := texCommandAt(s, i)
			switch name {
			case "text", "textrm", "textbf", "textit", "textsf", "texttt", "mbox":
				if _, next, ok := texGroupAt(s, end); ok {
					end = next
				}
			case "left", "right":
				if _, next, ok := texArgument(s, end); ok {
					end = next
				}
			case "\\":
				open[len(open)-1] = false
			}
			b.WriteString(s[i:end])
			i = end
			continue
		}
		switch s[i] {
		case '{', '(', '[':
			open = append(open, false)
		case '}', ')', ']':
			if len(open) > 1 {
				open = open[:len(open)-1]
			}
		case '&':
			open[len(open)-1] = false
		case '|':
			level := len(open) - 1
			if open[level] {
				open[level] = false
			} else {
				next := i + 1
				for next < len(s) && strings.ContainsRune(" \t\r\n", rune(s[next])) {
					next++
				}
				if next < len(s) && (s[next] == '_' || s[next] == '^') {
					b.WriteString(`\text{|}`)
					i++
					continue
				}
				open[level] = true
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func inlineTeXSource(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		name, end := texCommandAt(s, i)
		switch name {
		case "text", "mathrm", "textrm", "textbf", "textit", "textsf", "texttt", "mbox":
			if _, next, ok := texGroupAt(s, end); ok {
				b.WriteString(s[i:next])
				i = next
				continue
			}
		case "dfrac":
			b.WriteString(`\tfrac`)
			i = end
			continue
		case "displaystyle":
			b.WriteString(`\textstyle`)
			i = end
			continue
		case "limits":
			b.WriteString(`\nolimits`)
			i = end
			continue
		}
		b.WriteString(s[i:end])
		i = end
	}
	return b.String()
}

func texWidthsCompatible(s string) bool {
	for s != "" {
		cluster, w := terminalFirstGraphemeCluster(s)
		expected := 0
		for _, r := range cluster {
			if r == '\n' || r == '\t' {
				continue
			}
			if r >= 0x300 && r <= 0x36f || r >= 0x20d0 && r <= 0x20ff {
				continue
			}
			expected++
			if r >= 0xff01 && r <= 0xff60 || r >= 0x4e00 && r <= 0x9fff {
				expected++
			}
		}
		if expected != w {
			return false
		}
		s = s[len(cluster):]
	}
	return true
}

// texRows splits only at the outer environment level, keeping nested matrices,
// text groups and escaped ampersands intact.
func texRows(s string) [][]string {
	rows := [][]string{{}}
	start, depth, env := 0, 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		}
		if s[i] == '}' {
			depth--
		}
		if s[i] == '\\' {
			name, end := texCommandAt(s, i)
			if depth == 0 && (name == "ar" || name == "arrow") {
				j := end
				for j < len(s) && s[j] == ' ' {
					j++
				}
				if j < len(s) && s[j] == '[' {
					if _, next, ok := tikzOptions(s, j); ok {
						i = next - 1
						continue
					}
				}
			}
			if depth == 0 && name == "begin" {
				env++
			}
			if depth == 0 && name == "end" {
				env--
			}
			if name == "\\" && depth == 0 && env == 0 {
				rows[len(rows)-1] = append(rows[len(rows)-1], s[start:i])
				rows = append(rows, []string{})
				start = end
			}
			i = end - 1
			continue
		}
		if s[i] == '&' && depth == 0 && env == 0 {
			rows[len(rows)-1] = append(rows[len(rows)-1], s[start:i])
			start = i + 1
		}
	}
	rows[len(rows)-1] = append(rows[len(rows)-1], s[start:])
	return rows
}

func texEnvironmentBody(s string, start int, env string) (string, int, bool) {
	depth := 1
	for i := start; i < len(s); {
		if s[i] != '\\' {
			i++
			continue
		}
		name, end := texCommandAt(s, i)
		if name == "begin" || name == "end" {
			n, next, ok := texGroupAt(s, end)
			if !ok {
				return "", 0, false
			}
			if n == env {
				if name == "begin" {
					depth++
				} else {
					depth--
					if depth == 0 {
						return s[start:i], next, true
					}
				}
			}
			end = next
		}
		i = end
	}
	return "", 0, false
}

func texArgument(s string, i int) (string, int, bool) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\n') {
		i++
	}
	if i >= len(s) {
		return "", i, false
	}
	if s[i] == '{' {
		return texGroupAt(s, i)
	}
	if s[i] == '\\' {
		_, end := texCommandAt(s, i)
		return s[i:end], end, true
	}
	_, n := utf8.DecodeRuneInString(s[i:])
	return s[i : i+n], i + n, true
}

func linearTeX(s string) (string, bool) {
	s = texScriptedBars(s)
	rows := texRows(s)
	if len(rows) > 1 || len(rows[0]) > 1 {
		var flatRows []string
		for _, row := range rows {
			var cells []string
			for _, cell := range row {
				flat, ok := linearTeX(cell)
				if !ok {
					return "", false
				}
				cells = append(cells, flat)
			}
			flatRows = append(flatRows, strings.Join(cells, " "))
		}
		return strings.Join(flatRows, "; "), true
	}
	rewritten, ok := linearTeXSource(s)
	if !ok {
		return "", false
	}
	out, err := termtex.Render(rewritten, termtex.Style{Inline: true})
	return out, err == nil && !strings.Contains(out, "\n")
}

func texLiteral(s string) string {
	return `\text{` + strings.NewReplacer(`\`, `\backslash `, "{", `\{`, "}", `\}`).Replace(s) + `}`
}

func texBracketAt(s string, i int) (string, int, bool) {
	start, depth := i+1, 1
	for i++; i < len(s); {
		switch s[i] {
		case '\\':
			_, i = texCommandAt(s, i)
			continue
		case '{':
			_, end, ok := texGroupAt(s, i)
			if !ok {
				return "", len(s), false
			}
			i = end
			continue
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start:i], i + 1, true
			}
		}
		i++
	}
	return "", len(s), false
}

func linearTeXSource(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '^' || s[i] == '_' {
			marker := s[i : i+1]
			arg, end, ok := texArgument(s, i+1)
			if !ok {
				return "", false
			}
			flat, ok := linearTeX(arg)
			if !ok {
				return "", false
			}
			out, err := termtex.Render("x"+marker+"{"+arg+"}", termtex.Style{Inline: true})
			if err == nil && !strings.Contains(out, "\n") {
				b.WriteString(marker + "{" + arg + "}")
			} else {
				b.WriteString(texLiteral(marker + "(" + flat + ")"))
			}
			i = end
			continue
		}
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		name, end := texCommandAt(s, i)
		switch name {
		case "xrightarrow", "xleftarrow":
			for end < len(s) && strings.ContainsRune(" \t\r\n", rune(s[end])) {
				end++
			}
			below, hasBelow := "", end < len(s) && s[end] == '['
			if hasBelow {
				arg, next, ok := texBracketAt(s, end)
				if !ok {
					return "", false
				}
				below, ok = linearTeX(arg)
				if !ok {
					return "", false
				}
				end = next
			}
			arg, next, ok := texArgument(s, end)
			if !ok {
				return "", false
			}
			above, ok := linearTeX(arg)
			if !ok {
				return "", false
			}
			label := above
			if hasBelow {
				label = "above: " + above + "; below: " + below
			}
			arrow := "→"
			if name == "xleftarrow" {
				arrow = "←"
			}
			b.WriteString(texLiteral(" " + arrow + "[" + label + "] "))
			i = next
			continue
		case "overline":
			arg, next, ok := texArgument(s, end)
			if !ok {
				return "", false
			}
			flat, ok := linearTeX(arg)
			if !ok {
				return "", false
			}
			text := "overline(" + flat + ")"
			if utf8.RuneCountInString(flat) == 1 && terminalCellWidth(flat) == 1 {
				text = flat + "\u0305"
			}
			b.WriteString(texLiteral(text))
			i = next
			continue
		case "displaystyle", "textstyle", "scriptstyle", "scriptscriptstyle", "limits", "nolimits":
			b.WriteString(`\textstyle `)
			i = end
			continue
		case "left", "right":
			i = end
			for i < len(s) && (s[i] == ' ' || s[i] == '\n') {
				i++
			}
			if i >= len(s) {
				return "", false
			}
			if s[i] == '.' {
				i++
				continue
			}
			delim := s[i : i+1]
			i++
			if delim == `\` {
				name, j := texCommandAt(s, i-1)
				i = j
				delim = map[string]string{"{": "{", "}": "}", "lbrace": "{", "rbrace": "}", "langle": "⟨", "rangle": "⟩", "lfloor": "⌊", "rfloor": "⌋", "lceil": "⌈", "rceil": "⌉", "vert": "|", "lvert": "|", "rvert": "|", "Vert": "‖", "lVert": "‖", "rVert": "‖", "|": "‖"}[name]
				if delim == "" {
					return "", false
				}
			}
			b.WriteString(texLiteral(delim))
			continue
		case "frac", "dfrac", "tfrac", "binom":
			a, j, ok := texArgument(s, end)
			if !ok {
				return "", false
			}
			c, k, ok := texArgument(s, j)
			if !ok {
				return "", false
			}
			fa, ok := linearTeX(a)
			if !ok {
				return "", false
			}
			fc, ok := linearTeX(c)
			if !ok {
				return "", false
			}
			text := "(" + fa + ")/(" + fc + ")"
			if name == "binom" {
				text = "binom(" + fa + ", " + fc + ")"
			}
			b.WriteString(texLiteral(text))
			i = k
			continue
		case "begin":
			env, j, ok := texGroupAt(s, end)
			if !ok {
				return "", false
			}
			body, k, ok := texEnvironmentBody(s, j, env)
			if !ok {
				return "", false
			}
			if env == "array" {
				_, next, valid := texGroupAt(body, 0)
				if !valid {
					return "", false
				}
				body = body[next:]
			}
			var rows []string
			separator := ", "
			if env == "aligned" || env == "align" || env == "align*" || env == "split" || env == "gather" || env == "gathered" {
				separator = " "
			}
			for _, row := range texRows(body) {
				var cells []string
				for _, cell := range row {
					flat, ok := linearTeX(cell)
					if !ok {
						return "", false
					}
					cells = append(cells, flat)
				}
				rows = append(rows, strings.Join(cells, separator))
			}
			label := "[" + strings.Join(rows, "; ") + "]"
			switch env {
			case "cases":
				label = "cases{" + strings.Join(rows, "; ") + "}"
			case "vmatrix":
				label = "det(" + label + ")"
			case "Vmatrix":
				label = "‖" + label + "‖"
			case "aligned", "align", "align*", "split", "gather", "gathered":
				label = strings.Join(rows, "; ")
			}
			b.WriteString(texLiteral(label))
			i = k
			continue
		case "\\":
			b.WriteString(texLiteral("; "))
			i = end
			continue
		}
		if name == "text" || name == "mathrm" || name == "textrm" || name == "textbf" || name == "textit" || name == "textsf" || name == "texttt" || name == "mbox" {
			_, j, ok := texGroupAt(s, end)
			if !ok {
				return "", false
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		b.WriteString(s[i:end])
		i = end
	}
	return b.String(), true
}
