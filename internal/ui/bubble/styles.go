// 本文件集中定义 Bubble Tea TUI 的颜色、边框、文本样式和布局常量。
package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"time"
)

// 这些 lipgloss 样式描述 TUI 各区域和 Markdown 元素的视觉呈现。
var (
	headerStyle                  lipgloss.Style
	labelUserStyle               lipgloss.Style
	labelAssistantStyle          lipgloss.Style
	labelThinkingStyle           lipgloss.Style
	labelToolStyle               lipgloss.Style
	toolHeaderStyle              lipgloss.Style
	toolDetailStyle              lipgloss.Style
	toolCitationStyle            lipgloss.Style
	toolCitationKeyStyle         lipgloss.Style
	toolCitationOKStyle          lipgloss.Style
	toolCitationErrorStyle       lipgloss.Style
	toolStatusOKStyle            lipgloss.Style
	toolStatusRunningStyle       lipgloss.Style
	toolStatusErrorStyle         lipgloss.Style
	toolCitationQuoteBorderStyle lipgloss.Style
	toolCitationRailStyle        lipgloss.Style
	labelSystemStyle             lipgloss.Style
	labelErrorStyle              lipgloss.Style
	bodyStyle                    lipgloss.Style
	subagentWaitStyle            lipgloss.Style
	assistantMarkerStyle         lipgloss.Style
	userTranscriptRowStyle       lipgloss.Style
	thinkingBodyStyle            lipgloss.Style
	markdownBoldStyle            lipgloss.Style
	markdownItalicStyle          lipgloss.Style
	markdownHighlightStyle       lipgloss.Style
	markdownHeadingStyle         lipgloss.Style
	markdownRuleStyle            lipgloss.Style
	markdownBulletStyle          lipgloss.Style
	markdownCodeStyle            lipgloss.Style
	markdownLinkStyle            lipgloss.Style
	markdownCodeBlockStyle       lipgloss.Style
	markdownCodeBlockBorderStyle lipgloss.Style
	markdownCodeBlockLabelStyle  lipgloss.Style
	markdownQuoteStyle           lipgloss.Style
	transcriptPanelStyle         lipgloss.Style
	mainFrameStyle               lipgloss.Style
	transcriptContentStyle       lipgloss.Style
	inputDockStyle               lipgloss.Style
	inputDockTerminalStyle       lipgloss.Style
	inputDockGoalStyle           lipgloss.Style
	worktreeNameStyle            lipgloss.Style
	worktreeSeparatorStyle       lipgloss.Style
	worktreeChipStyle            lipgloss.Style
	inputHintStyle               lipgloss.Style
	inputPromptStyle             lipgloss.Style
	emptyTitleStyle              lipgloss.Style
	dockRuleStyle                lipgloss.Style
	modelStatusStyle             lipgloss.Style
	inputLabelStyle              lipgloss.Style
	contextCacheStyle            lipgloss.Style
	contextUsedStyle             lipgloss.Style
	contextFreeStyle             lipgloss.Style
	terminalInputLabelStyle      lipgloss.Style
	terminalInputTextStyle       lipgloss.Style
	generatingStatusStyle        lipgloss.Style
	copyToastStyle               lipgloss.Style
	modeTerminalStyle            lipgloss.Style
	modeShellStyle               lipgloss.Style
	modeChatStyle                lipgloss.Style
	modeGoalStyle                lipgloss.Style
	modePlanStyle                lipgloss.Style
	inputCommandTokenStyle       lipgloss.Style
	inputFileTokenStyle          lipgloss.Style
	inputImageTokenStyle         lipgloss.Style
	inputWaitingStyle            lipgloss.Style
	inputPanelStyle              lipgloss.Style
	inputPanelFocusedStyle       lipgloss.Style
	inputPanelWaitingStyle       lipgloss.Style
	inputPanelTerminalStyle      lipgloss.Style
	selectedProviderStyle        lipgloss.Style
	unselectedProviderStyle      lipgloss.Style
	wizardTitleStyle             lipgloss.Style
	wizardPanelStyle             lipgloss.Style
	completionPanelStyle         lipgloss.Style
	toolCallBorderStyle          lipgloss.Style
	toolResultBorderStyle        lipgloss.Style
	toolErrorBorderStyle         lipgloss.Style
	toolFocusedStyle             lipgloss.Style
	todoTitleStyle               lipgloss.Style
	todoCountStyle               lipgloss.Style
	todoCompletedStyle           lipgloss.Style
	todoInProgressStyle          lipgloss.Style
	todoPendingStyle             lipgloss.Style
	todoExplanationStyle         lipgloss.Style
	todoSummaryStyle             lipgloss.Style
	selectedTranscriptLineStyle  lipgloss.Style
	syntaxKeywordStyle           lipgloss.Style
	syntaxStringStyle            lipgloss.Style
	syntaxNumberStyle            lipgloss.Style
	syntaxCommentStyle           lipgloss.Style
	syntaxBracketStyles          []lipgloss.Style
)

func rebuildLegacyStyles() {

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
	toolStatusOKStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorTerminalBackground)).
		Background(colorManager.LipglossColor(colorWorktreeClean)).
		Bold(true)
	toolStatusRunningStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorTerminalBackground)).
		Background(colorManager.LipglossColor(colorWorktreeDirty)).
		Bold(true)
	toolStatusErrorStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorTerminalBackground)).
		Background(colorManager.LipglossColor(colorWorktreeConflict)).
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
	// TaskWait 状态行：与工具标签同色但无边框、无背景，作为
	// "子智能体 高松灯 正在运行 13s" 的单行状态文字。
	subagentWaitStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorLabelTool))
	assistantMarkerStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorLabelAssistant)).
		Bold(true)
	userTranscriptRowStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorLabelUser)).
		Background(colorManager.LipglossColor(colorWorktreeBackground)).
		Bold(true)
	thinkingBodyStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
		Italic(true)
	markdownBoldStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownBold)).
		Bold(true)
	markdownItalicStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorBody)).
		Italic(true)
	markdownHighlightStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownHighlightForeground)).
		Background(colorManager.LipglossColor(colorMarkdownHighlight))
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
	inputDockTerminalStyle = inputDockStyle.Copy().
		Foreground(colorManager.LipglossColor(colorInputTerminal))
	inputDockGoalStyle = inputDockStyle.Copy()
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
	terminalInputLabelStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorInputTerminal)).
		Bold(true)
	terminalInputTextStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorInputTerminal))
	// generating 状态样式（translate 面板「翻译中…」）与复制反馈 toast。
	generatingStatusStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorContextUsed)).
		Bold(true)
	// 复制反馈 toast：与 generating 同色系但更高饱和，短暂提示不喧宾夺主。
	copyToastStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorWorktreeClean)).
		Bold(true)
	modeTerminalStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorInputTerminal)).
		Bold(true)
	modeShellStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorInputTerminal))
	modeChatStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownRule))
	// agentmode（plan/goal）用背景高亮块 + 终端背景色字体；chat/shell/
	// terminal 保持前景色文字，不与 agentmode 抢视觉权重。
	modeGoalStyle = lipgloss.NewStyle().
		Background(colorManager.LipglossColor(colorInputTokenCommand)).
		Foreground(colorManager.LipglossColor(colorTerminalBackground)).
		Bold(true)
	modePlanStyle = lipgloss.NewStyle().
		Background(colorManager.LipglossColor(colorSignal)).
		Foreground(colorManager.LipglossColor(colorTerminalBackground)).
		Bold(true)
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
	// 16 色及以下终端没有可靠的 RGB 背景近似：选区降级为反色渲染，
	// 与终端原生选区观感一致，避免 lipgloss 把真彩色近似成奇怪颜色。
	lowColorSelection := lipgloss.ColorProfile() == termenv.ANSI || lipgloss.ColorProfile() == termenv.Ascii
	if lowColorSelection {
		toolFocusedStyle = lipgloss.NewStyle().
			Bold(true).
			Reverse(true)
		selectedTranscriptLineStyle = lipgloss.NewStyle().
			Reverse(true)
	} else {
		toolFocusedStyle = lipgloss.NewStyle().
			Background(colorManager.LipglossColor(colorSelectionBackground)).
			Foreground(colorManager.LipglossColor(colorSelectionForeground)).
			Bold(true)
		selectedTranscriptLineStyle = lipgloss.NewStyle().
			Background(colorManager.LipglossColor(colorSelectionBackground)).
			Foreground(colorManager.LipglossColor(colorSelectionForeground))
	}
	todoTitleStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorLabelAssistant)).
		Bold(true)
	todoCountStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorContextFree))
	todoCompletedStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorWorktreeClean)).
		Bold(true)
	todoInProgressStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorContextUsed)).
		Bold(true)
	todoPendingStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorContextFree))
	todoExplanationStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
		Italic(true)
	todoSummaryStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownQuote))

	if lowColorSelection {
		selectedTranscriptLineStyle = lipgloss.NewStyle().
			Reverse(true)
	} else {
		selectedTranscriptLineStyle = lipgloss.NewStyle().
			Background(colorManager.LipglossColor(colorSelectionBackground)).
			Foreground(colorManager.LipglossColor(colorSelectionForeground))
	}
	syntaxKeywordStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorSignal)).
		Bold(true)
	syntaxStringStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownCodeForeground))
	syntaxNumberStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorContextUsed))
	syntaxCommentStyle = lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorMarkdownQuote)).
		Italic(true)
	syntaxBracketStyles = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelAssistant)),
		lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextUsed)),
		lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelUser)),
		lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorSignal)),
	}
}

// 这些常量控制光标动画节奏、输入框高度、边框尺寸和主题色。
const (
	cursorFrameInterval            = time.Second / 30
	idleClockInterval              = 15 * time.Second // 空闲态时钟刷新间隔
	idleClockInputGuard            = 3 * time.Second  // 距最后按键的静默窗口，窗口内跳过时钟重绘
	cursorCycleDuration            = 3 * time.Second
	cursorHiddenThreshold          = 0.03
	inputMinVisibleLines           = 1
	inputMaxVisibleLines           = 10
	transcriptPanelHorizontalFrame = 4
	transcriptPanelVerticalFrame   = 2
	modalPanelVerticalFrame        = 2
	completionPanelVerticalFrame   = 2
)

func init() { rebuildLegacyStyles() }
