package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"time"
)

var (
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("231")).
			Padding(0, 1)
	labelUserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true)
	labelAssistantStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("111")).
				Bold(true)
	labelToolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)
	labelSystemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Bold(true)
	labelErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)
	bodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))
	markdownHeadingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("229")).
				Bold(true)
	markdownRuleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))
	markdownBulletStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("86")).
				Bold(true)
	markdownCodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("236")).
				Padding(0, 1)
	markdownCodeBlockStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)
	markdownQuoteStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Border(lipgloss.Border{Left: "│"}).
				BorderForeground(lipgloss.Color("244")).
				PaddingLeft(1)
	transcriptPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("238")).
				Padding(0, 1)
	inputLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true)
	terminalInputLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(terminalPanelColor)).
				Bold(true)
	inputWaitingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Italic(true)
	inputPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)
	inputPanelFocusedStyle = inputPanelStyle.Copy().
				BorderForeground(lipgloss.Color("86"))
	inputPanelWaitingStyle = inputPanelStyle.Copy().
				BorderForeground(lipgloss.Color("244"))
	inputPanelMultilineStyle = inputPanelStyle.Copy().
					BorderForeground(lipgloss.Color("214"))
	inputPanelTerminalStyle = inputPanelStyle.Copy().
				BorderForeground(lipgloss.Color(terminalPanelColor))
	selectedProviderStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("231")).
				Bold(true)
	unselectedProviderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	wizardTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("229")).
				Bold(true)
	wizardPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)
)

const (
	cursorFrameInterval            = time.Second / 30
	cursorCycleDuration            = 3 * time.Second
	cursorHiddenThreshold          = 0.03
	inputMaxVisibleLines           = 5
	transcriptPanelHorizontalFrame = 4
	transcriptPanelVerticalFrame   = 2
	terminalPanelColor             = "#ff5ac8"
	normalCursorDim                = "#06130d"
	normalCursorBright             = "#9fffd3"
	terminalCursorDim              = "#29061b"
	terminalCursorBright           = "#ff8ddd"
)
