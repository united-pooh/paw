package theme

import (
	"fmt"
	"strconv"
	"strings"
)

// blendHex 在 from 与 to 两个 sRGB hex 颜色之间按 amount（0..1）线性插值。
// 用于从主题的正文背景/前景自动推导选区背景：模拟“半透明覆盖层”的浮起效果，
// 保证任何新增主题的选区色都自动满足与正文的区分度，无需人工选色。
func blendHex(from, to string, amount float64) string {
	fr, fg, fb := parseHexColor(from)
	tr, tg, tb := parseHexColor(to)
	return fmt.Sprintf("#%02x%02x%02x", lerpInt(fr, tr, amount), lerpInt(fg, tg, amount), lerpInt(fb, tb, amount))
}

func parseHexColor(color string) (int, int, int) {
	color = strings.TrimPrefix(color, "#")
	if len(color) != 6 {
		return 0, 0, 0
	}
	r, e1 := strconv.ParseInt(color[0:2], 16, 0)
	g, e2 := strconv.ParseInt(color[2:4], 16, 0)
	b, e3 := strconv.ParseInt(color[4:6], 16, 0)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0
	}
	return int(r), int(g), int(b)
}

func lerpInt(from, to int, amount float64) int { return from + int(float64(to-from)*amount+0.5) }

type ThemeID string
type ThemeMode string

const (
	ModeDark  ThemeMode = "dark"
	ModeLight ThemeMode = "light"

	Default         ThemeID = "default"
	TokyoNight      ThemeID = "tokyo-night"
	TokyoNightStorm ThemeID = "tokyo-night-storm"
	TokyoNightLight ThemeID = "tokyo-night-light"
	CatppuccinMocha ThemeID = "catppuccin-mocha"
	Dracula         ThemeID = "dracula"
	GruvboxDark     ThemeID = "gruvbox-dark"
)

type Palette struct {
	TerminalBackground, HeaderBackground, HeaderForeground                                                      string
	LabelUser, LabelAssistant, LabelTool, LabelResult, LabelSystem, LabelError                                  string
	Body, ToolDetailBackground                                                                                  string
	MarkdownHeading, MarkdownRule, MarkdownBullet, MarkdownBold, MarkdownItalic, MarkdownHighlight, MarkdownHighlightForeground string
	MarkdownCodeForeground, MarkdownCodeBackground, MarkdownCodeBorder                                          string
	MarkdownLink, MarkdownQuote, MarkdownQuoteBorder                                                            string
	// MarkdownQuoteText 是 markdown 引用块文字的独立角色：MarkdownQuote 同时被
	// Thoughts 暗色正文/工具引用/todo 说明复用，引用文字单独配色不影响它们。
	MarkdownQuoteText string
	// 语法高亮专用色：独立于 UI 语义角色，避免代码 token 与界面元素撞色。
	SyntaxKeyword, SyntaxString, SyntaxNumber, SyntaxComment string
	SyntaxBrackets                                           [4]string // 彩虹括号，按嵌套深度循环
	PanelBorder, InputFocusedBorder, InputWaitingBorder, InputMultilineBorder                                   string
	InputTerminal, InputTokenCommand, InputTokenFile                                                            string
	UnselectedProvider                                                                                          string
	WizardTitle, WizardBorder, SelectionBackground, SelectionForeground                                         string
	ContextCache, ContextUsed, ContextFree, Signal                                                              string
	WorktreeBackground, WorktreeBorder, WorktreeClean, WorktreeDirty, WorktreeConflict                          string
	CursorNormalBright, CursorTerminalBright                                                                    string
	DiffAddedForeground, DiffAddedBackground, DiffDeletedForeground, DiffDeletedBackground                      string
}

type Theme struct {
	ID     ThemeID
	Name   string
	Mode   ThemeMode
	Colors Palette
}

func (p Palette) Values() map[string]string {
	return map[string]string{
		"terminal.background": p.TerminalBackground, "header.background": p.HeaderBackground, "header.foreground": p.HeaderForeground,
		"label.user": p.LabelUser, "label.assistant": p.LabelAssistant, "label.tool": p.LabelTool, "label.result": p.LabelResult, "label.system": p.LabelSystem, "label.error": p.LabelError,
		"body": p.Body, "tool.detail.background": p.ToolDetailBackground, "markdown.heading": p.MarkdownHeading, "markdown.rule": p.MarkdownRule, "markdown.bullet": p.MarkdownBullet, "markdown.bold": p.MarkdownBold, "markdown.italic": p.MarkdownItalic, "markdown.highlight": p.MarkdownHighlight, "markdown.highlight.foreground": p.MarkdownHighlightForeground,
		"markdown.code.foreground": p.MarkdownCodeForeground, "markdown.code.background": p.MarkdownCodeBackground, "markdown.code.border": p.MarkdownCodeBorder,
		"markdown.link": p.MarkdownLink, "markdown.quote": p.MarkdownQuote, "markdown.quote.border": p.MarkdownQuoteBorder, "markdown.quote.text": p.MarkdownQuoteText,
		"syntax.keyword": p.SyntaxKeyword, "syntax.string": p.SyntaxString, "syntax.number": p.SyntaxNumber, "syntax.comment": p.SyntaxComment,
		"syntax.bracket.1": p.SyntaxBrackets[0], "syntax.bracket.2": p.SyntaxBrackets[1], "syntax.bracket.3": p.SyntaxBrackets[2], "syntax.bracket.4": p.SyntaxBrackets[3],
		"panel.border": p.PanelBorder, "input.focused.border": p.InputFocusedBorder, "input.waiting.border": p.InputWaitingBorder, "input.multiline.border": p.InputMultilineBorder,
		"input.terminal": p.InputTerminal, "input.token.command": p.InputTokenCommand, "input.token.file": p.InputTokenFile,
		"provider.unselected": p.UnselectedProvider,
		"wizard.title": p.WizardTitle, "wizard.border": p.WizardBorder, "selection.background": p.SelectionBackground, "selection.foreground": p.SelectionForeground,
		"context.cache": p.ContextCache, "context.used": p.ContextUsed, "context.free": p.ContextFree, "signal": p.Signal,
		"worktree.background": p.WorktreeBackground, "worktree.border": p.WorktreeBorder, "worktree.clean": p.WorktreeClean, "worktree.dirty": p.WorktreeDirty, "worktree.conflict": p.WorktreeConflict,
		"cursor.normal.bright": p.CursorNormalBright, "cursor.terminal.bright": p.CursorTerminalBright,
		"diff.added.foreground": p.DiffAddedForeground, "diff.added.background": p.DiffAddedBackground, "diff.deleted.foreground": p.DiffDeletedForeground, "diff.deleted.background": p.DiffDeletedBackground,
	}
}

func NormalizeID(value string) ThemeID {
	id := ThemeID(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := ByID(id); ok {
		return id
	}
	return Default
}

func ByID(id ThemeID) (Theme, bool) {
	for _, item := range builtIns {
		if item.ID == id {
			return item, true
		}
	}
	return Theme{}, false
}

func List() []Theme { return append([]Theme(nil), builtIns...) }
