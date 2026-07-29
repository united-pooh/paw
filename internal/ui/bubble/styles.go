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
			Foreground(colorManager.LipglossColor(colorBody)).
			Background(colorManager.LipglossColor(colorToolDetailBackground))
	toolCitationStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote))
	toolCitationKeyStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelAssistant)).
				Bold(true)
	toolCitationOKStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelResult)).
				Bold(true)
	toolCitationErrorStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelError)).
				Bold(true)
	toolCitationQuoteBorderStyle = lipgloss.NewStyle().
					Foreground(colorManager.LipglossColor(colorMarkdownQuoteBorder))
	toolCitationRailStyle = lipgloss.NewStyle().
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(colorManager.LipglossColor(colorMarkdownQuoteBorder)).
				PaddingLeft(1)
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
	markdownBoldStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorBody)).
				Bold(true)
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
	markdownLinkStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownLink)).
				Bold(true).
				Underline(true)
	markdownCodeBlockStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownCodeForeground)).
				Background(colorManager.LipglossColor(colorMarkdownCodeBackground))
	markdownCodeBlockBorderStyle = lipgloss.NewStyle().
					Foreground(colorManager.LipglossColor(colorMarkdownCodeBorder))
	markdownCodeBlockLabelStyle = lipgloss.NewStyle().
					Foreground(colorManager.LipglossColor(colorTerminalBackground)).
					Background(colorManager.LipglossColor(colorSignal)).
					Bold(true)
	markdownQuoteStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(colorManager.LipglossColor(colorMarkdownQuoteBorder)).
				PaddingLeft(1)
	transcriptPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorManager.LipglossColor(colorPanelBorder)).
				Padding(0, 1)
	mainFrameStyle = lipgloss.NewStyle().
			Border(lipgloss.Border{
			Top:         "─",
			TopLeft:     "─",
			TopRight:    "─",
			Bottom:      "─",
			BottomLeft:  "─",
			BottomRight: "─",
		}).
		BorderForeground(colorManager.LipglossColor(colorPanelBorder))
	transcriptContentStyle = lipgloss.NewStyle().
				Padding(0, mainContentPadding)
	inputDockStyle = lipgloss.NewStyle().
			Padding(0, mainContentPadding)
	inputDockMultilineStyle = inputDockStyle.Copy().
				Foreground(colorManager.LipglossColor(colorInputMultilineBorder))
	inputDockTerminalStyle = inputDockStyle.Copy().
				Foreground(colorManager.LipglossColor(colorInputTerminal))
	worktreeNameStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Background(colorManager.LipglossColor(colorWorktreeBackground))
	worktreeSeparatorStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
				Background(colorManager.LipglossColor(colorWorktreeBackground))
	worktreeChipStyle = lipgloss.NewStyle().
				Background(colorManager.LipglossColor(colorWorktreeBackground)).
				Padding(0, 1)
	inputHintStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorContextFree))
	inputPromptStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorSignal)).
				Bold(true)
	emptyTitleStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorHeaderForeground)).
			Bold(true)
	dockRuleStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorPanelBorder))
	modelStatusStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorLabelAssistant)).
				Bold(true)
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
	terminalInputTextStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTerminal))
	// 底部 status 三段样式：生成态、模式标记。复用既有颜色角色，不新增调色板。
	generatingStatusStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorContextUsed)).
				Bold(true)
	idleStatusStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorMarkdownRule))
	modeTerminalStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTerminal)).
				Bold(true)
	modeShellStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorInputTerminal))
	modeMultilineStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputMultilineBorder))
	modeChatStyle = lipgloss.NewStyle().
			Foreground(colorManager.LipglossColor(colorMarkdownRule))
	inputCommandTokenStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTokenCommand)).
				Bold(true)
	inputFileTokenStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTokenFile)).
				Bold(true)
	inputImageTokenStyle = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorInputTokenCommand)).
				Underline(true)
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
				BorderStyle(lipgloss.Border{Left: "│"}).
				BorderLeft(true).
				BorderLeftForeground(colorManager.LipglossColor(colorLabelTool)).
				PaddingLeft(1)

	toolResultBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.Border{Left: "│"}).
				BorderLeft(true).
				BorderLeftForeground(colorManager.LipglossColor(colorLabelResult)).
				PaddingLeft(1)

	toolErrorBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.Border{Left: "│"}).
				BorderLeft(true).
				BorderLeftForeground(colorManager.LipglossColor(colorLabelError)).
				PaddingLeft(1)
	toolFocusedStyle = lipgloss.NewStyle().
				Background(colorManager.LipglossColor(colorSelectionBackground)).
				Foreground(colorManager.LipglossColor(colorSelectionForeground)).
				Bold(true)

	selectedTranscriptLineStyle = lipgloss.NewStyle().
					Background(colorManager.LipglossColor(colorSelectionBackground)).
					Foreground(colorManager.LipglossColor(colorSelectionForeground))
)

// 这些常量控制光标动画节奏、输入框高度、边框尺寸和主题色。
const (
	cursorFrameInterval            = time.Second / 30
	cursorCycleDuration            = 3 * time.Second
	cursorHiddenThreshold          = 0.03
	inputMinVisibleLines           = 1
	inputMaxVisibleLines           = 10
	contextMeterDefaultWidth       = 80
	contextMeterMinimumBarCells    = 1
	transcriptPanelHorizontalFrame = 4
	transcriptPanelVerticalFrame   = 2
	modalPanelVerticalFrame        = 2
	completionPanelVerticalFrame   = 2
)
