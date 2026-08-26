package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"paw/internal/theme"
)

// StyleSet contains the theme-derived styles owned by one appModel.
type StyleSet struct {
	Colors                                                                                                                     ColorManager
	Frame, Header, Body, ToolDetail                                                                                            lipgloss.Style
	LabelUser, LabelAssistant, LabelThinking, LabelTool, LabelSystem, LabelError                                               lipgloss.Style
	MarkdownHeading, MarkdownRule, MarkdownBullet, MarkdownBold, MarkdownItalic, MarkdownHighlight, MarkdownCode, MarkdownLink lipgloss.Style
	MarkdownCodeBlock, MarkdownCodeBlockBorder, MarkdownCodeBlockLabel, MarkdownQuote                                          lipgloss.Style
	TranscriptContent, InputDock, InputDockTerminal                                                                            lipgloss.Style
	InputHint, InputPrompt, InputTokenCommand, InputTokenFile, InputTokenImage                                                 lipgloss.Style
	ContextCache, ContextUsed, ContextFree, ContextThinking                                                                    lipgloss.Style
	TerminalInputLabel, TerminalInputText                                                                                      lipgloss.Style
	Modal, ModalTitle, Selected, SelectionFocused, SelectionNormal, SelectionSelected, SelectionFocusedSelected, Unselected    lipgloss.Style
	StatusRunning, StatusSuccess, StatusWarning, StatusError, StatusMuted                                                      lipgloss.Style
	Notice, NoticeHover                                                                                                        lipgloss.Style
}

func NewStyleSet(p theme.Palette) StyleSet {
	c := NewColorManager(p)
	bg := c.LipglossColor(colorTerminalBackground)
	surface := c.LipglossColor(colorToolDetailBackground)
	return StyleSet{
		Colors:                   c,
		Frame:                    lipgloss.NewStyle().Foreground(c.LipglossColor(colorPanelBorder)).Background(bg),
		Header:                   lipgloss.NewStyle().Background(c.LipglossColor(colorHeaderBackground)).Foreground(c.LipglossColor(colorHeaderForeground)).Padding(0, 1),
		Body:                     lipgloss.NewStyle().Foreground(c.LipglossColor(colorBody)).Background(bg),
		ToolDetail:               lipgloss.NewStyle().Foreground(c.LipglossColor(colorBody)).Background(surface),
		LabelUser:                lipgloss.NewStyle().Foreground(c.LipglossColor(colorLabelUser)).Bold(true),
		LabelAssistant:           lipgloss.NewStyle().Foreground(c.LipglossColor(colorLabelAssistant)).Bold(true),
		LabelThinking:            lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownQuote)).Italic(true),
		LabelTool:                lipgloss.NewStyle().Foreground(c.LipglossColor(colorLabelTool)).Bold(true),
		LabelSystem:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorLabelSystem)).Bold(true),
		LabelError:               lipgloss.NewStyle().Foreground(c.LipglossColor(colorLabelError)).Bold(true),
		MarkdownHeading:          lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownHeading)).Bold(true),
		MarkdownBold:             lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownBold)).Bold(true),
		MarkdownItalic:           lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownItalic)).Italic(true),
		MarkdownHighlight:        lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownHighlightForeground)).Background(c.LipglossColor(colorMarkdownHighlight)),
		MarkdownRule:             lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownRule)),
		MarkdownBullet:           lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownBullet)).Bold(true),
		MarkdownCode:             lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownCodeForeground)).Background(c.LipglossColor(colorMarkdownCodeBackground)).Padding(0, 1),
		MarkdownLink:             lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownLink)).Bold(true).Underline(true),
		MarkdownCodeBlock:        lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownCodeForeground)).Background(c.LipglossColor(colorMarkdownCodeBackground)),
		MarkdownCodeBlockBorder:  lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownCodeBorder)),
		MarkdownCodeBlockLabel:   lipgloss.NewStyle().Foreground(bg).Background(c.LipglossColor(colorSignal)).Bold(true),
		MarkdownQuote:            lipgloss.NewStyle().Foreground(c.LipglossColor(colorMarkdownQuoteText)).BorderStyle(lipgloss.Border{Left: "│"}).BorderLeft(true).BorderLeftForeground(c.LipglossColor(colorMarkdownQuoteBorder)).PaddingLeft(1),
		TranscriptContent:        lipgloss.NewStyle().Background(bg).Padding(0, mainContentPadding),
		InputDock:                lipgloss.NewStyle().Background(bg).Padding(0, mainContentPadding),
		InputDockTerminal:        lipgloss.NewStyle().Background(bg).Foreground(c.LipglossColor(colorInputTerminal)).Padding(0, mainContentPadding),
		InputHint:                lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextFree)).Background(bg),
		InputPrompt:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorSignal)).Background(bg).Bold(true),
		InputTokenCommand:        lipgloss.NewStyle().Foreground(c.LipglossColor(colorInputTokenCommand)).Background(bg).Bold(true),
		InputTokenFile:           lipgloss.NewStyle().Foreground(c.LipglossColor(colorInputTokenFile)).Background(bg).Bold(true),
		InputTokenImage:          lipgloss.NewStyle().Foreground(c.LipglossColor(colorInputTokenCommand)).Background(bg).Underline(true),
		ContextCache:             lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextCache)).Background(bg).Bold(true),
		ContextUsed:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextUsed)).Background(bg).Bold(true),
		ContextFree:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextFree)).Background(bg),
		ContextThinking:          lipgloss.NewStyle().Foreground(c.LipglossColor(colorBody)).Background(bg).Bold(true),
		TerminalInputLabel:       lipgloss.NewStyle().Foreground(c.LipglossColor(colorInputTerminal)).Background(bg).Bold(true),
		TerminalInputText:        lipgloss.NewStyle().Foreground(c.LipglossColor(colorInputTerminal)).Background(bg),
		Modal:                    lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(c.LipglossColor(colorWizardBorder)).Background(surface).Padding(0, 1),
		ModalTitle:               lipgloss.NewStyle().Foreground(c.LipglossColor(colorWizardTitle)).Background(surface).Bold(true),
		Selected:                 lipgloss.NewStyle().Background(c.LipglossColor(colorSelectedProviderBg)).Foreground(c.LipglossColor(colorSelectedProviderFg)).Bold(true),
		SelectionNormal:          lipgloss.NewStyle().Foreground(c.LipglossColor(colorBody)).Background(surface),
		SelectionFocused:         lipgloss.NewStyle().Foreground(c.LipglossColor(colorSelectionForeground)).Bold(true),
		SelectionSelected:        lipgloss.NewStyle().Background(c.LipglossColor(colorSelectionBackground)).Foreground(c.LipglossColor(colorSelectionForeground)).Bold(true),
		SelectionFocusedSelected: lipgloss.NewStyle().Background(c.LipglossColor(colorSelectionBackground)).Foreground(c.LipglossColor(colorSelectionForeground)).Bold(true),
		Unselected:               lipgloss.NewStyle().Foreground(c.LipglossColor(colorUnselectedProvider)).Background(surface),
		StatusRunning:            lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextUsed)).Background(bg).Bold(true),
		StatusSuccess:            lipgloss.NewStyle().Foreground(c.LipglossColor(colorWorktreeClean)).Background(bg),
		StatusWarning:            lipgloss.NewStyle().Foreground(c.LipglossColor(colorWorktreeDirty)).Background(bg),
		StatusError:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorWorktreeConflict)).Background(bg),
		StatusMuted:              lipgloss.NewStyle().Foreground(c.LipglossColor(colorContextFree)).Background(bg),
		Notice:                   lipgloss.NewStyle().Foreground(c.LipglossColor(colorSignal)).Background(c.LipglossColor(colorWorktreeBackground)).Bold(true).Padding(0, 1),
		NoticeHover:              lipgloss.NewStyle().Foreground(bg).Background(c.LipglossColor(colorSignal)).Bold(true).Padding(0, 1),
	}
}
