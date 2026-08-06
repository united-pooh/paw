package theme

func palette(bg, surface, fg, muted, primary, secondary, cyan, green, yellow, red, diffAddedBackground, diffDeletedBackground string) Palette {
	return Palette{
		TerminalBackground: bg, HeaderBackground: surface, HeaderForeground: fg,
		LabelUser: "#ffaf00", LabelAssistant: fg, LabelTool: green, LabelResult: green, LabelSystem: muted, LabelError: red,
		Body: fg, ToolDetailBackground: surface,
		MarkdownHeading: primary, MarkdownRule: muted, MarkdownBullet: cyan,
		MarkdownBold: primary, MarkdownHighlight: yellow,
		MarkdownCodeForeground: fg, MarkdownCodeBackground: surface, MarkdownCodeBorder: primary,
		MarkdownLink: cyan, MarkdownQuote: muted, MarkdownQuoteBorder: muted,
		PanelBorder: muted, InputFocusedBorder: cyan, InputWaitingBorder: muted, InputMultilineBorder: yellow,
		InputTerminal: secondary, InputTokenCommand: secondary, InputTokenFile: green,
		SelectedProviderBG: primary, SelectedProviderFG: bg, UnselectedProvider: muted,
		WizardTitle: primary, WizardBorder: primary, SelectionBackground: surface, SelectionForeground: fg,
		ContextCache: yellow, ContextUsed: cyan, ContextFree: muted, Signal: primary,
		WorktreeBackground: surface, WorktreeBorder: muted, WorktreeClean: green, WorktreeDirty: yellow, WorktreeConflict: red,
		CursorNormalBright: green, CursorTerminalBright: secondary,
		DiffAddedForeground: green, DiffAddedBackground: diffAddedBackground, DiffDeletedForeground: red, DiffDeletedBackground: diffDeletedBackground,
	}
}

var builtIns = []Theme{
	{ID: Default, Name: "Default", Mode: ModeDark, Colors: palette("#292c33", "#182830", "#c9c2b7", "#8e98a8", "#76d5e8", "#ff5ac8", "#76d5e8", "#a9c8b5", "#e5b66e", "#ef7d7d", "#0f2e1d", "#2e0f15")},
	{ID: TokyoNight, Name: "Tokyo Night", Mode: ModeDark, Colors: palette("#1a1b26", "#24283b", "#c0caf5", "#565f89", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#3b4261", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightStorm, Name: "Tokyo Night Storm", Mode: ModeDark, Colors: palette("#24283b", "#1f2335", "#c0caf5", "#737aa2", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#3b4261", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightLight, Name: "Tokyo Night Light", Mode: ModeLight, Colors: palette("#d5d6db", "#cbccd1", "#343b58", "#6172b0", "#34548a", "#5a4a78", "#0f4b6e", "#485e30", "#c6cbe3", "#8c4351", "#d8e8d0", "#f0d8d8")},
	{ID: CatppuccinMocha, Name: "Catppuccin Mocha", Mode: ModeDark, Colors: palette("#1e1e2e", "#313244", "#cdd6f4", "#6c7086", "#89b4fa", "#cba6f7", "#89dceb", "#a6e3a1", "#f9e2af", "#f38ba8", "#1e3a2c", "#3a1e28")},
	{ID: Dracula, Name: "Dracula", Mode: ModeDark, Colors: palette("#282a36", "#44475a", "#f8f8f2", "#6272a4", "#8be9fd", "#bd93f9", "#8be9fd", "#50fa7b", "#f1fa8c", "#ff5555", "#12381f", "#38121a")},
	{ID: GruvboxDark, Name: "Gruvbox Dark", Mode: ModeDark, Colors: palette("#282828", "#3c3836", "#ebdbb2", "#928374", "#83a598", "#d3869b", "#8ec07c", "#b8bb26", "#fabd2f", "#fb4934", "#2b3a1e", "#3a1e1e")},
}

func init() {
	p := &builtIns[0].Colors
	p.HeaderBackground = "#242830"
	p.HeaderForeground = "#f0e6d5"
	p.LabelUser = "#ffaf00"
	p.LabelAssistant = "#f0e6d5"
	p.MarkdownHeading = "#ffffaf"
	p.MarkdownRule = "#808080"
	p.MarkdownBullet = "#5fd7d7"
	p.MarkdownBold = "#ffffaf"
	p.MarkdownHighlight = "#5f5fd7"
	p.MarkdownCodeForeground = "#ffffd7"
	p.MarkdownCodeBackground = "#303030"
	p.MarkdownCodeBorder = "#5f5fd7"
	p.MarkdownQuote = "#8a8a8a"
	p.MarkdownQuoteBorder = "#808080"
	p.InputWaitingBorder = "#808080"
	p.InputMultilineBorder = "#ffaf00"
	p.SelectedProviderBG = "#5f5fd7"
	p.SelectedProviderFG = "#ffffff"
	p.UnselectedProvider = "#8a8a8a"
	p.WizardTitle = "#ffffaf"
	p.WizardBorder = "#5f5fd7"
	p.SelectionBackground = "#3a3a3a"
	p.SelectionForeground = "#eeeeee"
	p.ContextCache = "#ffaf00"
}
