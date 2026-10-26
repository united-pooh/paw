package theme

func palette(bg, surface, fg, muted, primary, secondary, cyan, green, yellow, red, diffAddedBackground, diffDeletedBackground string) Palette {
	secondaryText := blendHex(bg, fg, 0.65)
	border := blendHex(bg, fg, 0.20)
	return Palette{
		TerminalBackground: bg, HeaderBackground: surface, HeaderForeground: fg,
		LabelUser: fg, LabelAssistant: fg, LabelTool: green, LabelResult: green, LabelSystem: secondaryText, LabelError: red,
		Body: fg, ToolDetailBackground: surface,
		MarkdownHeading: fg, MarkdownRule: border, MarkdownBullet: cyan,
		MarkdownBold: primary, MarkdownItalic: fg, MarkdownHighlight: yellow, MarkdownHighlightForeground: bg,
		MarkdownCodeForeground: fg, MarkdownCodeBackground: blendHex(bg, fg, 0.07), MarkdownCodeBorder: primary,
		MarkdownLink: cyan, MarkdownQuote: secondaryText, MarkdownQuoteBorder: muted, MarkdownQuoteText: blendHex(bg, fg, 0.70),
		// 语法高亮默认映射（keyword 紫 / string 绿 / number 黄 / comment 灰），
		// 与各主题官方语法色系一致；个别主题在 init() 中按官方值覆盖。
		SyntaxKeyword: secondary, SyntaxString: green, SyntaxNumber: yellow, SyntaxComment: muted,
		SyntaxBrackets: [4]string{cyan, secondary, yellow, green},
		PanelBorder:    border, InputFocusedBorder: cyan, InputWaitingBorder: muted, InputMultilineBorder: yellow,
		InputTerminal: secondary, InputTokenCommand: secondary, InputTokenFile: green,
		UnselectedProvider: muted,
		WizardTitle:        primary, WizardBorder: primary,
		// 选区背景 = 正文背景与前景按 30% 混合：深色主题下同时满足
		// “选区文字 ≥4.5:1”与“选区 vs 正文 ≥2:1”双对比度约束，且与
		// markdown 高亮 / diff 背景等语义色不冲突（见 docs/mouse-selection-research.md §4）。
		// Default 使用终端原生反色，不依赖此 RGB 混合色。
		SelectionBackground: blendHex(bg, fg, 0.30), SelectionForeground: fg,
		ContextCache: "#81a1f1", ContextUsed: cyan, ContextFree: secondaryText, Signal: primary,
		WorktreeBackground: blendHex(bg, fg, 0.055), WorktreeBorder: muted, WorktreeClean: green, WorktreeDirty: yellow, WorktreeConflict: red,
		CursorNormalBright: green, CursorTerminalBright: secondary,
		DiffAddedForeground: green, DiffAddedBackground: diffAddedBackground, DiffDeletedForeground: red, DiffDeletedBackground: diffDeletedBackground,
		// hex 主题的新角色全部从既有色相派生，不为每套主题单独策展。
		StatusSuccess: green, StatusRunning: cyan, StatusWarning: yellow, StatusError: red,
		Accent: secondary, Narration: primary,
		ChipRead: primary, ChipEdit: green, ChipShell: secondary, ChipSearch: cyan, ChipTask: yellow, ChipPlugin: muted,
	}
}

var builtIns = []Theme{
	{ID: Default, Name: "Default", Mode: ModeTerminal, Colors: terminalPalette()},
	{ID: TokyoNight, Name: "Tokyo Night", Mode: ModeDark, Colors: palette("#1a1b26", "#24283b", "#c0caf5", "#565f89", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#e0af68", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightStorm, Name: "Tokyo Night Storm", Mode: ModeDark, Colors: palette("#24283b", "#1f2335", "#c0caf5", "#737aa2", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#e0af68", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightLight, Name: "Tokyo Night Light", Mode: ModeLight, Colors: palette("#d5d6db", "#cbccd1", "#343b58", "#6172b0", "#34548a", "#5a4a78", "#0f4b6e", "#485e30", "#8f5e15", "#8c4351", "#d8e8d0", "#f0d8d8")},
	{ID: CatppuccinMocha, Name: "Catppuccin Mocha", Mode: ModeDark, Colors: palette("#1e1e2e", "#313244", "#cdd6f4", "#6c7086", "#89b4fa", "#cba6f7", "#89dceb", "#a6e3a1", "#f9e2af", "#f38ba8", "#1e3a2c", "#3a1e28")},
	{ID: Dracula, Name: "Dracula", Mode: ModeDark, Colors: palette("#282a36", "#44475a", "#f8f8f2", "#6272a4", "#8be9fd", "#bd93f9", "#8be9fd", "#50fa7b", "#f1fa8c", "#ff5555", "#12381f", "#38121a")},
	{ID: GruvboxDark, Name: "Gruvbox Dark", Mode: ModeDark, Colors: palette("#282828", "#3c3836", "#ebdbb2", "#928374", "#83a598", "#d3869b", "#8ec07c", "#b8bb26", "#fabd2f", "#fb4934", "#2b3a1e", "#3a1e1e")},
}

func init() {
	// 各主题官方语法色微调：generic 推导（keyword 紫/string 绿/number 黄）
	// 未覆盖的按官方值覆盖。
	override := func(id ThemeID, fn func(*Palette)) {
		for i := range builtIns {
			if builtIns[i].ID == id {
				fn(&builtIns[i].Colors)
			}
		}
	}
	override(TokyoNight, func(p *Palette) { p.SyntaxNumber = "#ff9e64" })
	override(TokyoNightStorm, func(p *Palette) {
		p.SyntaxNumber = "#ff9e64"
		// 仅引用块文字用 Storm 标志性的紫色（独立角色，不影响 Thoughts 等
		// 复用 markdown.quote 的暗色文字）。
		p.MarkdownQuoteText = "#bb9af7"
	})
	override(TokyoNightLight, func(p *Palette) {
		p.SyntaxNumber = "#965027"
		p.MarkdownQuoteText = blendHex(p.TerminalBackground, p.Body, 0.85)
	})
	override(CatppuccinMocha, func(p *Palette) { p.SyntaxNumber = "#fab387" })
	override(Dracula, func(p *Palette) {
		p.SyntaxKeyword = "#ff79c6"
		p.SyntaxString = "#f1fa8c"
		p.SyntaxNumber = "#bd93f9"
	})
	override(GruvboxDark, func(p *Palette) {
		p.SyntaxKeyword = "#fb4934"
		p.SyntaxNumber = "#d3869b"
	})
}

// Empty roles inherit the terminal defaults; indexed colors use its ANSI palette.
func terminalPalette() Palette {
	return Palette{
		LabelTool: "2", LabelResult: "2", LabelSystem: "", LabelError: "1",
		MarkdownRule: "", MarkdownBullet: "6", MarkdownBold: "4", MarkdownHighlight: "3",
		MarkdownCodeBorder: "6", MarkdownLink: "6", MarkdownQuote: "", MarkdownQuoteBorder: "7", MarkdownQuoteText: "",
		SyntaxKeyword: "5", SyntaxString: "2", SyntaxNumber: "3", SyntaxComment: "15",
		SyntaxBrackets: [4]string{"6", "5", "3", "2"},
		PanelBorder:    "7", InputFocusedBorder: "6", InputWaitingBorder: "7", InputMultilineBorder: "3",
		InputTerminal: "5", InputTokenCommand: "5", InputTokenFile: "2", UnselectedProvider: "",
		WizardTitle: "6", WizardBorder: "6",
		ContextCache: "4", ContextUsed: "6", ContextFree: "", Signal: "6",
		WorktreeBorder: "7", WorktreeClean: "2", WorktreeDirty: "3", WorktreeConflict: "1",
		DiffAddedForeground: "2", DiffDeletedForeground: "1",
		StatusSuccess: "2", StatusRunning: "6", StatusWarning: "3", StatusError: "1",
		Accent: "5", Narration: "4",
		ChipRead: "4", ChipEdit: "2", ChipShell: "5", ChipSearch: "6", ChipTask: "3", ChipPlugin: "7",
	}
}
