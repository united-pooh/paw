package bubble

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"codex-agent-go/internal/skill"
)

func TestCommandCompletionCreatesAtomicTokenProjection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.input.SetValue("/he")
	model.completion = &completion{
		kind:          completionKindCommand,
		items:         []string{"/help"},
		selectedIndex: 0,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = next.(appModel)

	if got := model.input.Value(); got != "/help " {
		t.Fatalf("raw input = %q, want /help with separator", got)
	}
	if len(model.inputTokens) != 1 {
		t.Fatalf("inputTokens = %#v, want one command token", model.inputTokens)
	}
	token := model.inputTokens[0]
	if token.Kind != inputTokenCommand || token.Start != 0 || token.End != len([]rune("/help")) ||
		token.Label != "help" || !token.AutoSpace {
		t.Fatalf("token = %#v", token)
	}
	visible := ansi.Strip(model.renderInputContent())
	if strings.Contains(visible, "/help") || !strings.Contains(visible, "help") {
		t.Fatalf("visible input = %q, want hidden slash and visible label", visible)
	}
	if !strings.Contains(model.renderInputContent(), inputCommandTokenStyle.Render("help")) {
		t.Fatalf("rendered input does not contain purple bold command segment")
	}
}

func TestSkillAndFileCompletionUseHiddenLabels(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "design", "# Design\n")
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("$de")
	model.completion = &completion{
		kind:          completionKindSkill,
		items:         []string{"design"},
		selectedIndex: 0,
	}
	model = model.applySkillCompletion("design")

	wantSkill := "[$design](" + path + ") "
	if got := model.input.Value(); got != wantSkill {
		t.Fatalf("raw skill input = %q, want %q", got, wantSkill)
	}
	if len(model.inputTokens) != 1 || model.inputTokens[0].Kind != inputTokenSkill {
		t.Fatalf("skill token = %#v, want inputTokenSkill", model.inputTokens)
	}
	if got := ansi.Strip(model.renderInputContent()); strings.Contains(got, "[$design]") ||
		strings.Contains(got, path) || !strings.Contains(got, "design") {
		t.Fatalf("visible skill input = %q", got)
	}

	model.input.SetValue("引用 @资")
	model.inputTokens = nil
	model.completion = &completion{
		kind:      completionKindFile,
		query:     "资",
		searchDir: ".",
	}
	model = model.applyFileCompletion("资料📁.md", true)
	if got := model.input.Value(); got != "引用 @资料📁.md " {
		t.Fatalf("raw file input = %q", got)
	}
	if len(model.inputTokens) != 1 || model.inputTokens[0].Label != "资料📁.md" ||
		model.inputTokens[0].Kind != inputTokenFile {
		t.Fatalf("file token = %#v", model.inputTokens)
	}
	visible := ansi.Strip(model.renderInputContent())
	if strings.Contains(visible, "@资料") || !strings.Contains(visible, "资料📁.md") {
		t.Fatalf("visible file input = %q", visible)
	}
	if !strings.Contains(model.renderInputContent(), inputFileTokenStyle.Render("资")) {
		t.Fatalf("rendered input does not contain green bold file segment")
	}
}

func TestSlashSkillCompletionCreatesSkillToken(t *testing.T) {
	root := t.TempDir()
	path := writeBubbleTestSkill(t, root, "design", "# Design\n")
	model := newTestModel(&fakeRunner{})
	model.skillRegistry = skill.NewRegistry([]string{root})
	model.input.SetValue("/de")
	model.completion = &completion{
		kind:          completionKindCommand,
		items:         []string{"/design"},
		selectedIndex: 0,
	}
	model = model.applyCommandCompletion("/design")

	want := "[$design](" + path + ") "
	if model.input.Value() != want || len(model.inputTokens) != 1 ||
		model.inputTokens[0].Kind != inputTokenSkill || model.inputTokens[0].Label != "design" {
		t.Fatalf("slash skill draft = value %q tokens %#v", model.input.Value(), model.inputTokens)
	}
}

func TestDirectoryCompletionAndTypedSyntaxStayPlain(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("@sr")
	model.completion = &completion{
		kind:      completionKindFile,
		query:     "sr",
		searchDir: ".",
	}
	model = model.applyFileCompletion("src/", false)
	if got := model.input.Value(); got != "@src/" {
		t.Fatalf("directory raw input = %q", got)
	}
	if len(model.inputTokens) != 0 {
		t.Fatalf("directory traversal created tokens: %#v", model.inputTokens)
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("manual")})
	model = next.(appModel)
	if len(model.inputTokens) != 0 {
		t.Fatalf("typed syntax created tokens: %#v", model.inputTokens)
	}
}

func TestAtomicBackspaceDeletesTokenAndAutomaticSeparator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = &completion{kind: completionKindCommand}
	model = model.applyCommandCompletion("/help")
	model.completion = nil

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(appModel)
	if got := model.input.Value(); got != "" {
		t.Fatalf("input after atomic backspace = %q, want empty", got)
	}
	if len(model.inputTokens) != 0 {
		t.Fatalf("tokens after atomic backspace = %#v", model.inputTokens)
	}
}

func TestAtomicDeletionKeysConsumeWholeToken(t *testing.T) {
	tests := []struct {
		name   string
		cursor int
		key    tea.KeyMsg
	}{
		{name: "delete", cursor: 0, key: tea.KeyMsg{Type: tea.KeyDelete}},
		{name: "ctrl-w", cursor: len([]rune("/help ")), key: tea.KeyMsg{Type: tea.KeyCtrlW}},
		{name: "ctrl-u", cursor: len([]rune("/help ")), key: tea.KeyMsg{Type: tea.KeyCtrlU}},
		{name: "ctrl-k", cursor: 0, key: tea.KeyMsg{Type: tea.KeyCtrlK}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.completion = &completion{kind: completionKindCommand}
			model = model.applyCommandCompletion("/help")
			model.completion = nil
			setTextareaAbsoluteCursor(&model.input, test.cursor)

			next, _ := model.Update(test.key)
			model = next.(appModel)
			if got := model.input.Value(); got != "" {
				t.Fatalf("input after %s = %q, want empty", test.name, got)
			}
			if len(model.inputTokens) != 0 {
				t.Fatalf("tokens after %s = %#v", test.name, model.inputTokens)
			}
		})
	}
}

func TestAtomicNavigationAndExternalEditRebase(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = &completion{kind: completionKindCommand}
	model = model.applyCommandCompletion("/help")
	model.completion = nil

	// Move onto the token boundary, then a second left movement must skip the
	// complete hidden raw syntax.
	setTextareaAbsoluteCursor(&model.input, model.inputTokens[0].End)
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = next.(appModel)
	if got := textareaAbsoluteCursor(model.input); got != 0 {
		t.Fatalf("cursor after moving left into token = %d, want token start", got)
	}

	model.input.CursorEnd()
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("tail")})
	model = next.(appModel)
	if got := model.input.Value(); got != "/help tail" {
		t.Fatalf("input after ordinary suffix edit = %q", got)
	}
	if token := model.inputTokens[0]; token.Start != 0 || token.End != len([]rune("/help")) {
		t.Fatalf("token moved after suffix edit: %#v", token)
	}

	fileModel := newTestModel(&fakeRunner{})
	fileModel.input.SetValue("use @re")
	fileModel.completion = &completion{kind: completionKindFile, query: "re", searchDir: "."}
	fileModel = fileModel.applyFileCompletion("README.md", true)
	fileModel.completion = nil
	setTextareaAbsoluteCursor(&fileModel.input, 0)
	next, _ = fileModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	fileModel = next.(appModel)
	if token := fileModel.inputTokens[0]; token.Start != len([]rune("Xuse ")) {
		t.Fatalf("token start after prefix edit = %d, want %d", token.Start, len([]rune("Xuse ")))
	}

	samePrefixModel := newTestModel(&fakeRunner{})
	samePrefixModel.completion = &completion{kind: completionKindCommand}
	samePrefixModel = samePrefixModel.applyCommandCompletion("/help")
	samePrefixModel.completion = nil
	setTextareaAbsoluteCursor(&samePrefixModel.input, 0)
	next, _ = samePrefixModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	samePrefixModel = next.(appModel)
	if got := samePrefixModel.input.Value(); got != "//help " {
		t.Fatalf("same-prefix insertion raw input = %q", got)
	}
	if token := samePrefixModel.inputTokens[0]; token.Start != 1 || token.End != len([]rune("//help")) {
		t.Fatalf("same-prefix insertion rebased token = %#v", token)
	}
}

func TestMultipleTokensRebaseAndDeleteIndependently(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("use @a")
	model.completion = &completion{kind: completionKindFile, query: "a", searchDir: "."}
	model = model.applyFileCompletion("alpha.md", true)

	model.input.InsertString("and @b")
	model.completion = &completion{kind: completionKindFile, query: "b", searchDir: "."}
	model = model.applyFileCompletion("beta.md", true)
	model.completion = nil
	if len(model.inputTokens) != 2 {
		t.Fatalf("tokens = %#v, want two", model.inputTokens)
	}

	setTextareaAbsoluteCursor(&model.input, len([]rune(model.input.Value())))
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(appModel)
	if got := model.input.Value(); got != "use @alpha.md and " {
		t.Fatalf("raw input after deleting second token = %q", got)
	}
	if len(model.inputTokens) != 1 || model.inputTokens[0].Label != "alpha.md" {
		t.Fatalf("remaining tokens = %#v", model.inputTokens)
	}
}

func TestInputHistoryRestoresTokenMetadataAndRunnerReceivesRawSyntax(t *testing.T) {
	runner := &fakeRunner{}
	model := newTestModel(runner)
	model.ready = true
	model.width = 90
	model.height = 24
	model.input.SetValue("read @re")
	model.completion = &completion{
		kind:      completionKindFile,
		query:     "re",
		searchDir: ".",
	}
	model = model.applyFileCompletion("README.md", true)
	model.completion = nil
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if cmd == nil {
		t.Fatal("submit command = nil")
	}
	_ = cmd()
	if len(runner.inputs) != 1 || runner.inputs[0] != "read @README.md" {
		t.Fatalf("Runner inputs = %#v, want exact raw file syntax", runner.inputs)
	}
	if len(model.inputHistory) != 1 || len(model.inputHistory[0].Tokens) != 1 {
		t.Fatalf("input history = %#v, want token metadata", model.inputHistory)
	}

	model.running = false
	next, _ = model.handleHistoryNavigation(-1)
	model = next.(appModel)
	if got := model.input.Value(); got != "read @README.md" {
		t.Fatalf("restored raw input = %q", got)
	}
	if len(model.inputTokens) != 1 || model.inputTokens[0].Label != "README.md" {
		t.Fatalf("restored tokens = %#v", model.inputTokens)
	}
}

func TestTokenProjectionWrapsUnicodeWithoutExposingRawSyntax(t *testing.T) {
	raw := "前缀 @组合e\u0301-👩‍💻-文件.md 后缀"
	start := len([]rune("前缀 "))
	tokenRaw := "@组合e\u0301-👩‍💻-文件.md"
	projection := projectInput(raw, []inputToken{{
		Kind:  inputTokenFile,
		Start: start,
		End:   start + len([]rune(tokenRaw)),
		Label: strings.TrimPrefix(tokenRaw, "@"),
	}}, len([]rune(raw)), 8, false)

	if len(projection.lines) < 2 {
		t.Fatalf("projection lines = %d, want wrapped Unicode token", len(projection.lines))
	}
	var visible strings.Builder
	for _, line := range projection.lines {
		for _, atom := range line.atoms {
			visible.WriteString(atom.text)
			if atom.width > 8 {
				t.Fatalf("atom %q width = %d, exceeds projection width", atom.text, atom.width)
			}
		}
	}
	if strings.Contains(visible.String(), "@") || !strings.Contains(visible.String(), "👩‍💻") ||
		!strings.Contains(visible.String(), "e\u0301") {
		t.Fatalf("projected text = %q", visible.String())
	}
}

func TestTokenProjectionDrivesInputHeightFoldAndCursorAnchor(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 42
	model.height = 20
	rawToken := "[$design](/a/very/long/private/path/that/must/not/affect/layout/SKILL.md)"
	model.input.SetValue(rawToken + " ")
	model.inputTokens = []inputToken{{
		Kind:      inputTokenCommand,
		Start:     0,
		End:       len([]rune(rawToken)),
		Label:     "design",
		AutoSpace: true,
	}}
	model.relayout()
	if got := model.input.Height(); got != 1 {
		t.Fatalf("input height = %d, want projected one-line label", got)
	}
	if got := model.visibleInputCursorColumn(); got != len("design ")+0 {
		t.Fatalf("visible cursor column = %d, want %d", got, len("design "))
	}

	lines := []string{rawToken + " ", "one", "two", "three", "four", "five", "six", "seven", "eight"}
	model.input.SetValue(strings.Join(lines, "\n"))
	model.inputTokens[0].AutoSpace = false
	model.inputPasteFoldActive = true
	model.relayout()
	visible := ansi.Strip(model.renderInputContent())
	if !strings.Contains(visible, "design") || !strings.Contains(visible, "lines folded") {
		t.Fatalf("folded token input = %q", visible)
	}
	if strings.Contains(visible, "/a/very/long") {
		t.Fatalf("folded projection leaked raw skill path: %q", visible)
	}
}

func TestTerminalModeKeepsTokenSemanticColorPriority(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("@go.mod ")
	model.inputTokens = []inputToken{{
		Kind:      inputTokenFile,
		Start:     0,
		End:       len([]rune("@go.mod")),
		Label:     "go.mod",
		AutoSpace: true,
	}}
	model.terminalMode = true
	rendered := model.renderInputContent()
	if !strings.Contains(rendered, inputFileTokenStyle.Render("g")) {
		t.Fatalf("terminal input did not preserve file token style: %q", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "@go.mod") {
		t.Fatalf("terminal input leaked file prefix: %q", ansi.Strip(rendered))
	}
}

func TestProjectionWrapsCursorAfterExactWidth(t *testing.T) {
	raw := "/abcd"
	projection := projectInput(raw, []inputToken{{
		Kind:  inputTokenCommand,
		Start: 0,
		End:   len([]rune(raw)),
		Label: "abcd",
	}}, len([]rune(raw)), 4, false)
	if projection.cursorRow != 1 || projection.cursorColumn != 0 {
		t.Fatalf("cursor = (%d,%d), want wrapped (1,0)", projection.cursorRow, projection.cursorColumn)
	}
}

func TestSessionRestoreClearsCurrentInputTokens(t *testing.T) {
	model := newModel(
		context.Background(),
		&fakeRunner{},
		"session-1",
		&fakeModelConfigController{},
		nil,
		nil,
		nil,
		newTerminalCursorAnchor(),
	)
	model.completion = &completion{kind: completionKindCommand}
	model = model.applyCommandCompletion("/help")

	next, _ := model.Update(sessionRestoredMsg{sessionID: "session-2"})
	model = next.(appModel)
	if len(model.inputTokens) != 0 {
		t.Fatalf("session restore retained tokens: %#v", model.inputTokens)
	}
	if got := model.input.Value(); got != "/help " {
		t.Fatalf("session restore changed underlying draft = %q", got)
	}
}

func TestCtrlCAndSubmitClearCurrentTokenState(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.completion = &completion{kind: completionKindCommand}
	model = model.applyCommandCompletion("/help")
	model.completion = nil
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = next.(appModel)
	if model.input.Value() != "" || len(model.inputTokens) != 0 {
		t.Fatalf("Ctrl+C retained input state: value=%q tokens=%#v", model.input.Value(), model.inputTokens)
	}

	model.input.SetValue("read @go")
	model.completion = &completion{kind: completionKindFile, query: "go", searchDir: "."}
	model = model.applyFileCompletion("go.mod", true)
	model.completion = nil
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.input.Value() != "" || len(model.inputTokens) != 0 {
		t.Fatalf("submit retained current input state: value=%q tokens=%#v", model.input.Value(), model.inputTokens)
	}
}

func TestTokenStylesRemainBoldWithoutBackground(t *testing.T) {
	if !inputCommandTokenStyle.GetBold() || !inputFileTokenStyle.GetBold() {
		t.Fatal("token styles must remain bold")
	}
	_, commandHasNoBackground := inputCommandTokenStyle.GetBackground().(lipgloss.NoColor)
	_, fileHasNoBackground := inputFileTokenStyle.GetBackground().(lipgloss.NoColor)
	if !commandHasNoBackground || !fileHasNoBackground {
		t.Fatal("token styles must not use background blocks")
	}
}
