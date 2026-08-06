package bubble

import (
	"bytes"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// nbsp is a non-breaking space.
const nbsp = 0xA0

// wrapCompact 是 word wrap 的紧凑版本：优先在空白处断行（保持单词完整），
// 但当行尾剩余空间不足以容纳下一个词时，若该词为纯文本且以宽字符（中文等）
// 开头，则拆词填满当前行——避免 lipgloss 内部 cellbuf.Wrap 那种"词放不下
// 就整体换行、行尾留下可显示空白"的浪费。
//
// 其余行为（ANSI 样式、宽字符、超长词硬断、连字符断词点）与 x/ansi 的 Wrap
// 一致。
func wrapCompact(s string, limit int) string {
	if limit < 1 {
		return s
	}

	var (
		cluster    string
		buf        bytes.Buffer
		word       bytes.Buffer
		space      bytes.Buffer
		spaceWidth int                  // width of the space buffer
		curWidth   int                  // written width of the line
		wordLen    int                  // word buffer len without ANSI escape codes
		pstate     = parser.GroundState // initial state
	)

	addSpace := func() {
		if spaceWidth == 0 && space.Len() == 0 {
			return
		}
		curWidth += spaceWidth
		buf.Write(space.Bytes())
		space.Reset()
		spaceWidth = 0
	}

	addWord := func() {
		if word.Len() == 0 {
			return
		}

		addSpace()
		curWidth += wordLen
		buf.Write(word.Bytes())
		word.Reset()
		wordLen = 0
	}

	addNewline := func() {
		buf.WriteByte('\n')
		curWidth = 0
		space.Reset()
		spaceWidth = 0
	}

	// splitWord 在行尾空间不足时拆词：把词中能放进剩余空间的前缀填满当前行，
	// 其余部分留到下一行。仅对无 ANSI 序列、以宽字符开头的词生效，避免英文
	// 单词被硬拆（"viewpor"|"t"）以及样式序列被切断。
	// 词前空格不占用拆词预算：空间足够时随词保留（"…viewport 宽"），
	// 空间紧张时丢弃（"…viewport宽"），始终把词尽可能填满行尾。
	splitWord := func() {
		fill := limit - curWidth
		if fill < 2 || bytes.IndexByte(word.Bytes(), 0x1b) >= 0 || !startsWithWideRune(word.Bytes()) {
			return
		}
		if spaceWidth > 0 && spaceWidth+2 > fill {
			space.Reset()
			spaceWidth = 0
		}
		head := truncateStyledCells(word.String(), fill-spaceWidth, "")
		if head == "" {
			return
		}
		addSpace()
		buf.WriteString(head)
		headWidth := terminalCellWidth(head)
		curWidth += headWidth
		rest := word.String()[len(head):]
		word.Reset()
		word.WriteString(rest)
		wordLen -= headWidth
	}

	i := 0
	for i < len(s) {
		state, action := parser.Table.Transition(pstate, s[i])
		if state == parser.Utf8State { //nolint:nestif
			var width int
			cluster, width = ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
			i += len(cluster)

			r, _ := utf8.DecodeRuneInString(cluster)
			switch {
			case r != utf8.RuneError && unicode.IsSpace(r) && r != nbsp: // nbsp is a non-breaking space
				addWord()
				space.WriteRune(r)
				spaceWidth += width
			default:
				if wordLen+width > limit {
					// Hardwrap the word if it's too long
					addWord()
				}

				word.WriteString(cluster)
				wordLen += width

				if curWidth+wordLen+spaceWidth > limit {
					splitWord()
					addNewline()
				}

				if wordLen == limit {
					// Hardwrap the word if it's too long
					addWord()
				}
			}

			pstate = parser.GroundState
			continue
		}

		switch action {
		case parser.PrintAction, parser.ExecuteAction:
			switch r := rune(s[i]); {
			case r == '\n':
				if wordLen == 0 {
					if curWidth+spaceWidth > limit {
						curWidth = 0
					} else {
						// preserve whitespaces
						buf.Write(space.Bytes())
					}
					space.Reset()
					spaceWidth = 0
				}

				addWord()
				addNewline()
			case unicode.IsSpace(r):
				addWord()
				space.WriteRune(r)
				spaceWidth++
			case r == '-':
				addSpace()
				if curWidth+wordLen >= limit {
					// We can't fit the breakpoint in the current line, treat
					// it as part of the word.
					word.WriteRune(r)
					wordLen++
				} else {
					addWord()
					buf.WriteRune(r)
					curWidth++
				}
			default:
				if curWidth == limit {
					addNewline()
				}

				word.WriteRune(r)
				wordLen++

				if wordLen == limit {
					// Hardwrap the word if it's too long
					addWord()
				}

				if curWidth+wordLen+spaceWidth > limit {
					addNewline()
				}
			}

		default:
			word.WriteByte(s[i])
		}

		// We manage the UTF8 state separately manually above.
		if pstate != parser.Utf8State {
			pstate = state
		}
		i++
	}

	if wordLen == 0 {
		if curWidth+spaceWidth > limit {
			curWidth = 0
		} else {
			// preserve whitespaces
			buf.Write(space.Bytes())
		}
		space.Reset()
		spaceWidth = 0
	}

	addWord()

	return buf.String()
}

// startsWithWideRune 报告 b 的第一个可见字符是否为宽字符（中文/全角/emoji，
// 显示宽度 ≥ 2）。b 不应包含 ANSI 序列。
func startsWithWideRune(b []byte) bool {
	r, _ := utf8.DecodeRune(b)
	return r != utf8.RuneError && terminalCellWidth(string(r)) >= 2
}
