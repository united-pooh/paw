package theme

func palette(bg, surface, fg, muted, primary, secondary, cyan, green, yellow, red, diffAddedBackground, diffDeletedBackground string) Palette {
	return Palette{
		TerminalBackground: bg, HeaderBackground: surface, HeaderForeground: fg,
		LabelUser: "#ffaf00", LabelAssistant: fg, LabelTool: green, LabelResult: green, LabelSystem: muted, LabelError: red,
		Body: fg, ToolDetailBackground: surface,
		MarkdownHeading: primary, MarkdownRule: muted, MarkdownBullet: cyan,
		// bold 用主题黄（与标题的 primary 拉开层次）；italic 用 secondary 紫：
		// 很多终端不渲染 italic 属性，颜色偏移是斜体唯一的可见信号。
		MarkdownBold: yellow, MarkdownItalic: secondary, MarkdownHighlight: yellow, MarkdownHighlightForeground: bg,
		MarkdownCodeForeground: fg, MarkdownCodeBackground: surface, MarkdownCodeBorder: primary,
		MarkdownLink: cyan, MarkdownQuote: muted, MarkdownQuoteBorder: muted, MarkdownQuoteText: muted,
		// 语法高亮默认映射（keyword 紫 / string 绿 / number 黄 / comment 灰），
		// 与各主题官方语法色系一致；个别主题在 init() 中按官方值覆盖。
		SyntaxKeyword: secondary, SyntaxString: green, SyntaxNumber: yellow, SyntaxComment: muted,
		SyntaxBrackets: [4]string{cyan, secondary, yellow, green},
		PanelBorder: muted, InputFocusedBorder: cyan, InputWaitingBorder: muted, InputMultilineBorder: yellow,
		InputTerminal: secondary, InputTokenCommand: secondary, InputTokenFile: green,
		UnselectedProvider: muted,
		WizardTitle: primary, WizardBorder: primary,
		// 选区背景 = 正文背景与前景按 30% 混合：深色主题下同时满足
		// “选区文字 ≥4.5:1”与“选区 vs 正文 ≥2:1”双对比度约束，且与
		// markdown 高亮 / diff 背景等语义色不冲突（见 docs/mouse-selection-research.md §4）。
		// 浅色主题（TokyoNightLight）与 Default 在 init() 中用手工特例覆盖。
		SelectionBackground: blendHex(bg, fg, 0.30), SelectionForeground: fg,
		ContextCache: "#81a1f1", ContextUsed: cyan, ContextFree: muted, Signal: primary,
		WorktreeBackground: surface, WorktreeBorder: muted, WorktreeClean: green, WorktreeDirty: yellow, WorktreeConflict: red,
		CursorNormalBright: green, CursorTerminalBright: secondary,
		DiffAddedForeground: green, DiffAddedBackground: diffAddedBackground, DiffDeletedForeground: red, DiffDeletedBackground: diffDeletedBackground,
	}
}

var builtIns = []Theme{
	{ID: Default, Name: "Default", Mode: ModeDark, Colors: palette("#292c33", "#182830", "#c9c2b7", "#8e98a8", "#76d5e8", "#ff5ac8", "#76d5e8", "#a9c8b5", "#e5b66e", "#ef7d7d", "#0f2e1d", "#2e0f15")},
	{ID: TokyoNight, Name: "Tokyo Night", Mode: ModeDark, Colors: palette("#1a1b26", "#24283b", "#c0caf5", "#565f89", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#e0af68", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightStorm, Name: "Tokyo Night Storm", Mode: ModeDark, Colors: palette("#24283b", "#1f2335", "#c0caf5", "#737aa2", "#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a", "#e0af68", "#f7768e", "#14302a", "#2e1420")},
	{ID: TokyoNightLight, Name: "Tokyo Night Light", Mode: ModeLight, Colors: palette("#d5d6db", "#cbccd1", "#343b58", "#6172b0", "#34548a", "#5a4a78", "#0f4b6e", "#485e30", "#8f5e15", "#8c4351", "#d8e8d0", "#f0d8d8")},
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
	// bold 继承主题黄 #e5b66e（与标题的浅黄 #ffffaf 拉开层次）；italic 用
	// 柔和蓝：Default 的 secondary 是 hot pink，做斜体太吵。
	p.MarkdownItalic = "#81a1f1"
	p.MarkdownHighlight = "#5f5fd7"
	p.MarkdownHighlightForeground = "#ffffff"
	p.MarkdownCodeForeground = "#ffffd7"
	p.MarkdownCodeBackground = "#303030"
	// 代码块边框改青色：#5f5fd7 此前被 highlight/provider/wizard 多处复用。
	p.MarkdownCodeBorder = "#5f9ea8"
	p.MarkdownQuote = "#8a8a8a"
	p.MarkdownQuoteBorder = "#808080"
	p.InputWaitingBorder = "#808080"
	p.InputMultilineBorder = "#ffaf00"
	p.UnselectedProvider = "#8a8a8a"
	p.WizardTitle = "#ffffaf"
	p.WizardBorder = "#5f5fd7"
	// Default 主题文字对比本身较弱，30% 混合时选区文字只有 3.96:1（低于 AA），
	// 因此用 25% 混合：#515254，文字 4.43:1、与正文 1.79:1。
	p.SelectionBackground = blendHex("#292c33", "#c9c2b7", 0.25)
	p.SelectionForeground = "#eeeeee"

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
	override(TokyoNightLight, func(p *Palette) { p.SyntaxNumber = "#965027" })
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
