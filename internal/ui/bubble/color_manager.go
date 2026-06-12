// 本文件定义 TUI 颜色管理器，集中维护主题色和光标颜色。
package bubble

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// colorRole 表示一个可复用的颜色语义角色。
type colorRole string

// 颜色语义角色常量。
const (
	colorTerminalBackground     colorRole = "terminal.background"
	colorHeaderBackground       colorRole = "header.background"
	colorHeaderForeground       colorRole = "header.foreground"
	colorLabelUser              colorRole = "label.user"
	colorLabelAssistant         colorRole = "label.assistant"
	colorLabelTool              colorRole = "label.tool"
	colorLabelSystem            colorRole = "label.system"
	colorLabelError             colorRole = "label.error"
	colorBody                   colorRole = "body"
	colorMarkdownHeading        colorRole = "markdown.heading"
	colorMarkdownRule           colorRole = "markdown.rule"
	colorMarkdownBullet         colorRole = "markdown.bullet"
	colorMarkdownCodeForeground colorRole = "markdown.code.foreground"
	colorMarkdownCodeBackground colorRole = "markdown.code.background"
	colorMarkdownCodeBorder     colorRole = "markdown.code.border"
	colorMarkdownQuote          colorRole = "markdown.quote"
	colorMarkdownQuoteBorder    colorRole = "markdown.quote.border"
	colorPanelBorder            colorRole = "panel.border"
	colorInputFocusedBorder     colorRole = "input.focused.border"
	colorInputWaitingBorder     colorRole = "input.waiting.border"
	colorInputMultilineBorder   colorRole = "input.multiline.border"
	colorInputTerminal          colorRole = "input.terminal"
	colorSelectedProviderBg     colorRole = "provider.selected.background"
	colorSelectedProviderFg     colorRole = "provider.selected.foreground"
	colorUnselectedProvider     colorRole = "provider.unselected"
	colorWizardTitle            colorRole = "wizard.title"
	colorWizardBorder           colorRole = "wizard.border"
	colorSelectionBackground    colorRole = "selection.background"
	colorSelectionForeground    colorRole = "selection.foreground"
	colorCursorNormalBright     colorRole = "cursor.normal.bright"
	colorCursorTerminalBright   colorRole = "cursor.terminal.bright"
)

// ColorManager 集中管理 Bubble Tea TUI 的颜色调色板。
type ColorManager struct {
	palette map[colorRole]string
}

// colorManager 是当前 TUI 使用的默认颜色管理器。
var colorManager = NewColorManager()

// NewColorManager 创建默认颜色管理器。
func NewColorManager() ColorManager {
	return ColorManager{
		palette: map[colorRole]string{
			colorTerminalBackground:     "#292C33",
			colorHeaderBackground:       "236",
			colorHeaderForeground:       "231",
			colorLabelUser:              "86",
			colorLabelAssistant:         "111",
			colorLabelTool:              "214",
			colorLabelSystem:            "245",
			colorLabelError:             "203",
			colorBody:                   "252",
			colorMarkdownHeading:        "229",
			colorMarkdownRule:           "240",
			colorMarkdownBullet:         "86",
			colorMarkdownCodeForeground: "230",
			colorMarkdownCodeBackground: "236",
			colorMarkdownCodeBorder:     "62",
			colorMarkdownQuote:          "245",
			colorMarkdownQuoteBorder:    "244",
			colorPanelBorder:            "238",
			colorInputFocusedBorder:     "86",
			colorInputWaitingBorder:     "244",
			colorInputMultilineBorder:   "214",
			colorInputTerminal:          "#ff5ac8",
			colorSelectedProviderBg:     "62",
			colorSelectedProviderFg:     "231",
			colorUnselectedProvider:     "245",
			colorWizardTitle:            "229",
			colorWizardBorder:           "62",
			colorSelectionBackground:    "237",
			colorSelectionForeground:    "255",
			colorCursorNormalBright:     "#9fffd3",
			colorCursorTerminalBright:   "#ff8ddd",
		},
	}
}

// Hex 返回指定颜色角色对应的 lipgloss 颜色字符串。
func (c ColorManager) Hex(role colorRole) string {
	if c.palette == nil {
		c = NewColorManager()
	}
	return strings.ToLower(c.palette[role])
}

// LipglossColor 返回指定颜色角色对应的 lipgloss.Color。
func (c ColorManager) LipglossColor(role colorRole) lipgloss.Color {
	return lipgloss.Color(c.Hex(role))
}

// CursorColor 根据亮度和终端模式生成光标颜色。
func (c ColorManager) CursorColor(intensity float64, terminal bool) string {
	bright := colorCursorNormalBright
	if terminal {
		bright = colorCursorTerminalBright
	}
	return interpolateHexColor(c.Hex(colorTerminalBackground), c.Hex(bright), clamp01(intensity))
}

// cursorColor 使用当前默认颜色管理器生成光标颜色。
func cursorColor(intensity float64, terminal bool) string {
	return colorManager.CursorColor(intensity, terminal)
}

// interpolateHexColor 在两个十六进制颜色之间按比例插值。
func interpolateHexColor(from, to string, amount float64) string {
	fr, fg, fb := parseHexColor(from)
	tr, tg, tb := parseHexColor(to)
	return fmt.Sprintf(
		"#%02x%02x%02x",
		lerpInt(fr, tr, amount),
		lerpInt(fg, tg, amount),
		lerpInt(fb, tb, amount),
	)
}

// parseHexColor 将 #rrggbb 字符串解析为 RGB 分量。
func parseHexColor(color string) (int, int, int) {
	color = strings.TrimPrefix(color, "#")
	if len(color) != 6 {
		return 0, 0, 0
	}
	r, err := strconv.ParseInt(color[0:2], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	g, err := strconv.ParseInt(color[2:4], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	b, err := strconv.ParseInt(color[4:6], 16, 0)
	if err != nil {
		return 0, 0, 0
	}
	return int(r), int(g), int(b)
}

// lerpInt 对整数颜色分量做线性插值。
func lerpInt(from, to int, amount float64) int {
	return from + int(float64(to-from)*amount+0.5)
}
