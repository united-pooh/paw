// 本文件定义 TUI 颜色管理器，集中维护主题色和光标颜色。
package bubble

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/theme"
)

// colorRole 表示一个可复用的颜色语义角色。
type colorRole string

const (
	colorTerminalBackground          colorRole = "terminal.background"
	colorHeaderBackground            colorRole = "header.background"
	colorHeaderForeground            colorRole = "header.foreground"
	colorLabelUser                   colorRole = "label.user"
	colorLabelAssistant              colorRole = "label.assistant"
	colorLabelTool                   colorRole = "label.tool"
	colorLabelResult                 colorRole = "label.result"
	colorLabelSystem                 colorRole = "label.system"
	colorLabelError                  colorRole = "label.error"
	colorBody                        colorRole = "body"
	colorToolDetailBackground        colorRole = "tool.detail.background"
	colorMarkdownHeading             colorRole = "markdown.heading"
	colorMarkdownRule                colorRole = "markdown.rule"
	colorMarkdownBullet              colorRole = "markdown.bullet"
	colorMarkdownBold                colorRole = "markdown.bold"
	colorMarkdownItalic              colorRole = "markdown.italic"
	colorMarkdownHighlight           colorRole = "markdown.highlight"
	colorMarkdownHighlightForeground colorRole = "markdown.highlight.foreground"
	colorMarkdownCodeForeground      colorRole = "markdown.code.foreground"
	colorMarkdownCodeBackground      colorRole = "markdown.code.background"
	colorMarkdownCodeBorder          colorRole = "markdown.code.border"
	colorMarkdownLink                colorRole = "markdown.link"
	colorMarkdownQuote               colorRole = "markdown.quote"
	colorMarkdownQuoteBorder         colorRole = "markdown.quote.border"
	colorMarkdownQuoteText           colorRole = "markdown.quote.text"
	colorSyntaxKeyword               colorRole = "syntax.keyword"
	colorSyntaxString                colorRole = "syntax.string"
	colorSyntaxNumber                colorRole = "syntax.number"
	colorSyntaxComment               colorRole = "syntax.comment"
	colorSyntaxBracket1              colorRole = "syntax.bracket.1"
	colorSyntaxBracket2              colorRole = "syntax.bracket.2"
	colorSyntaxBracket3              colorRole = "syntax.bracket.3"
	colorSyntaxBracket4              colorRole = "syntax.bracket.4"
	colorPanelBorder                 colorRole = "panel.border"
	colorInputFocusedBorder          colorRole = "input.focused.border"
	colorInputWaitingBorder          colorRole = "input.waiting.border"
	colorInputMultilineBorder        colorRole = "input.multiline.border"
	colorInputTerminal               colorRole = "input.terminal"
	colorInputTokenCommand           colorRole = "input.token.command"
	colorInputTokenFile              colorRole = "input.token.file"
	colorUnselectedProvider          colorRole = "provider.unselected"
	colorWizardTitle                 colorRole = "wizard.title"
	colorWizardBorder                colorRole = "wizard.border"
	colorSelectionBackground         colorRole = "selection.background"
	colorSelectionForeground         colorRole = "selection.foreground"
	colorContextCache                colorRole = "context.cache"
	colorContextUsed                 colorRole = "context.used"
	colorContextFree                 colorRole = "context.free"
	colorSignal                      colorRole = "signal"
	colorWorktreeBackground          colorRole = "worktree.background"
	colorWorktreeBorder              colorRole = "worktree.border"
	colorWorktreeClean               colorRole = "worktree.clean"
	colorWorktreeDirty               colorRole = "worktree.dirty"
	colorWorktreeConflict            colorRole = "worktree.conflict"
	colorCursorNormalBright          colorRole = "cursor.normal.bright"
	colorCursorTerminalBright        colorRole = "cursor.terminal.bright"
	colorDiffAddedForeground         colorRole = "diff.added.foreground"
	colorDiffAddedBackground         colorRole = "diff.added.background"
	colorDiffDeletedForeground       colorRole = "diff.deleted.foreground"
	colorDiffDeletedBackground       colorRole = "diff.deleted.background"
)

type ColorManager struct{ palette theme.Palette }

func NewColorManager(palette theme.Palette) ColorManager { return ColorManager{palette: palette} }

func defaultColorManager() ColorManager {
	item, _ := theme.ByID(theme.Default)
	return NewColorManager(item.Colors)
}

// Transitional style source. Rendering activates the appModel palette before
// using legacy package-level styles; all persistent theme state remains on appModel.
var colorManager = defaultColorManager()

func (c ColorManager) Hex(role colorRole) string {
	p := c.palette
	var value string
	switch role {
	case colorTerminalBackground:
		value = p.TerminalBackground
	case colorHeaderBackground:
		value = p.HeaderBackground
	case colorHeaderForeground:
		value = p.HeaderForeground
	case colorLabelUser:
		value = p.LabelUser
	case colorLabelAssistant:
		value = p.LabelAssistant
	case colorLabelTool:
		value = p.LabelTool
	case colorLabelResult:
		value = p.LabelResult
	case colorLabelSystem:
		value = p.LabelSystem
	case colorLabelError:
		value = p.LabelError
	case colorBody:
		value = p.Body
	case colorToolDetailBackground:
		value = p.ToolDetailBackground
	case colorMarkdownHeading:
		value = p.MarkdownHeading
	case colorMarkdownRule:
		value = p.MarkdownRule
	case colorMarkdownBullet:
		value = p.MarkdownBullet
	case colorMarkdownBold:
		value = p.MarkdownBold
	case colorMarkdownItalic:
		value = p.MarkdownItalic
	case colorMarkdownHighlight:
		value = p.MarkdownHighlight
	case colorMarkdownHighlightForeground:
		value = p.MarkdownHighlightForeground
	case colorMarkdownCodeForeground:
		value = p.MarkdownCodeForeground
	case colorMarkdownCodeBackground:
		value = p.MarkdownCodeBackground
	case colorMarkdownCodeBorder:
		value = p.MarkdownCodeBorder
	case colorMarkdownLink:
		value = p.MarkdownLink
	case colorMarkdownQuote:
		value = p.MarkdownQuote
	case colorMarkdownQuoteBorder:
		value = p.MarkdownQuoteBorder
	case colorMarkdownQuoteText:
		value = p.MarkdownQuoteText
	case colorSyntaxKeyword:
		value = p.SyntaxKeyword
	case colorSyntaxString:
		value = p.SyntaxString
	case colorSyntaxNumber:
		value = p.SyntaxNumber
	case colorSyntaxComment:
		value = p.SyntaxComment
	case colorSyntaxBracket1:
		value = p.SyntaxBrackets[0]
	case colorSyntaxBracket2:
		value = p.SyntaxBrackets[1]
	case colorSyntaxBracket3:
		value = p.SyntaxBrackets[2]
	case colorSyntaxBracket4:
		value = p.SyntaxBrackets[3]
	case colorPanelBorder:
		value = p.PanelBorder
	case colorInputFocusedBorder:
		value = p.InputFocusedBorder
	case colorInputWaitingBorder:
		value = p.InputWaitingBorder
	case colorInputMultilineBorder:
		value = p.InputMultilineBorder
	case colorInputTerminal:
		value = p.InputTerminal
	case colorInputTokenCommand:
		value = p.InputTokenCommand
	case colorInputTokenFile:
		value = p.InputTokenFile
	case colorUnselectedProvider:
		value = p.UnselectedProvider
	case colorWizardTitle:
		value = p.WizardTitle
	case colorWizardBorder:
		value = p.WizardBorder
	case colorSelectionBackground:
		value = p.SelectionBackground
	case colorSelectionForeground:
		value = p.SelectionForeground
	case colorContextCache:
		value = p.ContextCache
	case colorContextUsed:
		value = p.ContextUsed
	case colorContextFree:
		value = p.ContextFree
	case colorSignal:
		value = p.Signal
	case colorWorktreeBackground:
		value = p.WorktreeBackground
	case colorWorktreeBorder:
		value = p.WorktreeBorder
	case colorWorktreeClean:
		value = p.WorktreeClean
	case colorWorktreeDirty:
		value = p.WorktreeDirty
	case colorWorktreeConflict:
		value = p.WorktreeConflict
	case colorCursorNormalBright:
		value = p.CursorNormalBright
	case colorCursorTerminalBright:
		value = p.CursorTerminalBright
	case colorDiffAddedForeground:
		value = p.DiffAddedForeground
	case colorDiffAddedBackground:
		value = p.DiffAddedBackground
	case colorDiffDeletedForeground:
		value = p.DiffDeletedForeground
	case colorDiffDeletedBackground:
		value = p.DiffDeletedBackground
	}
	if value == "" {
		value = p.Body
	}
	return strings.ToLower(value)
}

func (c ColorManager) LipglossColor(role colorRole) lipgloss.Color {
	return lipgloss.Color(c.Hex(role))
}

func (c ColorManager) CursorColor(intensity float64, terminal bool) string {
	bright := colorCursorNormalBright
	if terminal {
		bright = colorCursorTerminalBright
	}
	return interpolateHexColor(c.Hex(colorTerminalBackground), c.Hex(bright), clamp01(intensity))
}

func cursorColor(intensity float64, terminal bool) string {
	return colorManager.CursorColor(intensity, terminal)
}

func interpolateHexColor(from, to string, amount float64) string {
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

func lerpInt(from, to int, amount float64) int {
	// math.Round 而非 int()+0.5：int() 向零截断，负向插值（亮→暗）时
	// 端点会偏 1（如 -136.5 → -136），导致 amount=1 时取不到目标色。
	return from + int(math.Round(float64(to-from)*amount))
}
