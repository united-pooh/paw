// 本文件集中定义 Bubble Tea TUI 的颜色、边框、文本样式和布局常量。
package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"time"
)

// 这些 lipgloss 样式描述 TUI 各区域和 Markdown 元素的视觉呈现。
var (
	headerStyle = lipgloss.NewStyle().
			Background(colorManager.LipglossColor(colorHeaderBackground)).
			Foreground(colorManager.LipglossColor(colorHeaderForeground)).
			Padding(0, 1)
	labelUserStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorLabelUser)).
			Bold(true)
	labelAssistantStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelAssistant)).
				Bold(true)
	labelThinkingStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Italic(true)
	labelToolStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorLabelTool)).
			Bold(true)
	toolHeaderStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorLabelTool)).
			Bold(true)
	toolDetailStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorBody))
	labelSystemStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelSystem)).
				Bold(true)
	labelErrorStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorLabelError)).
			Bold(true)
	bodyStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorBody))
	thinkingBodyStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Italic(true)
	markdownHeadingStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownHeading)).
				Bold(true)
	markdownRuleStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownRule))
	markdownBulletStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownBullet)).
				Bold(true)
	markdownCodeStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownCodeForeground)).
				Background(colorManager.LipglossColor(colorMarkdownCodeBackground)).
				Padding(0, 1)
	markdownCodeBlockStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownCodeForeground)).
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorManager.LipglossColor(colorMarkdownCodeBorder)).
				Padding(0, 1)
	markdownQuoteStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(colorManager.LipglossColor(colorMarkdownQuoteBorder)).
				PaddingLeft(1)
	transcriptPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorManager.LipglossColor(colorPanelBorder)).
				Padding(0, 1)
	inputLabelStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorLabelUser)).
			Bold(true)
	contextCacheStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorContextCache)).
				Bold(true)
	contextUsedStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorContextUsed)).
				Bold(true)
	contextFreeStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorContextFree))
	contextThinkingStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorBody)).
				Bold(true)
	terminalInputLabelStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTerminal)).
				Bold(true)
	inputWaitingStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputWaitingBorder)).
				Italic(true)
	inputPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorManager.LipglossColor(colorPanelBorder)).
			Padding(0, 1)
	inputPanelFocusedStyle = inputPanelStyle.Copy().
				BorderForeground(colorManager.LipglossColor(colorInputFocusedBorder))
	inputPanelWaitingStyle = inputPanelStyle.Copy().
				BorderForeground(colorManager.LipglossColor(colorInputWaitingBorder))
	inputPanelMultilineStyle = inputPanelStyle.Copy().
					BorderForeground(colorManager.LipglossColor(colorInputMultilineBorder))
	inputPanelTerminalStyle = inputPanelStyle.Copy().
				BorderForeground(colorManager.LipglossColor(colorInputTerminal))
	selectedProviderStyle = lipgloss.NewStyle().
				Background(colorManager.LipglossColor(colorSelectedProviderBg)).
				Foreground(colorManager.LipglossColor(colorSelectedProviderFg)).
				Bold(true)
	unselectedProviderStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorUnselectedProvider))
	wizardTitleStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorWizardTitle)).
				Bold(true)
	wizardPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorManager.LipglossColor(colorWizardBorder)).
				Padding(0, 1)
	completionPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorManager.LipglossColor(colorWizardBorder)).
				Padding(0, 1)

	// Blockquote styles for tool calls — coloured left border
	toolCallBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(colorManager.LipglossColor(colorLabelTool)).
				Background(lipgloss.Color("232")).
				PaddingLeft(1)

	toolResultBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(lipgloss.Color("65")).
				Background(lipgloss.Color("232")).
				PaddingLeft(1)

	toolErrorBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(colorManager.LipglossColor(colorLabelError)).
				Background(lipgloss.Color("232")).
				PaddingLeft(1)

	rightCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorManager.LipglossColor(colorPanelBorder)).
			Padding(0, 1)
	selectedTranscriptLineStyle = lipgloss.NewStyle().
					Background(colorManager.LipglossColor(colorSelectionBackground)).
					Foreground(colorManager.LipglossColor(colorSelectionForeground))
)

// 这些常量控制光标动画节奏、输入框高度、边框尺寸和主题色。
const (
	cursorFrameInterval            = time.Second / 30
	cursorCycleDuration            = 3 * time.Second
	cursorHiddenThreshold          = 0.03
	inputMaxVisibleLines           = 5
	contextMeterDefaultWidth       = 80
	contextMeterMinimumBarCells    = 1
	transcriptPanelHorizontalFrame = 4
	transcriptPanelVerticalFrame   = 2
	rightSidebarMinWidth           = 20
)
