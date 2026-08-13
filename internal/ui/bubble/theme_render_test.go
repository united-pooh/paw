package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"paw/internal/theme"
)

func TestViewPaintsWholeTokyoNightLightBackground(t *testing.T) {
	model := newThemedTestModel(t, theme.TokyoNightLight)
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	view := model.View()
	if got := lipgloss.Width(view); got != 80 {
		t.Fatalf("width = %d, want 80", got)
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Fatalf("height = %d, want 24", got)
	}
	if got := model.styles.Frame.GetBackground(); got != lipgloss.Color(model.theme.Colors.TerminalBackground) {
		t.Fatalf("frame background = %#v, want %q", got, model.theme.Colors.TerminalBackground)
	}
	if got := model.input.FocusedStyle.Base.GetBackground(); got != lipgloss.Color(model.theme.Colors.TerminalBackground) {
		t.Fatalf("input background = %#v, want %q", got, model.theme.Colors.TerminalBackground)
	}
}

func TestThemeSwitchChangesWholeFrameBackground(t *testing.T) {
	model := newThemedTestModel(t, theme.TokyoNight)
	oldBackground := model.styles.Frame.GetBackground()
	if err := model.applyTheme(theme.TokyoNightLight); err != nil {
		t.Fatal(err)
	}
	newBackground := model.styles.Frame.GetBackground()
	if oldBackground == newBackground {
		t.Fatalf("frame background did not change: %#v", newBackground)
	}
	if newBackground != lipgloss.Color(model.theme.Colors.TerminalBackground) {
		t.Fatalf("new background = %#v, want %q", newBackground, model.theme.Colors.TerminalBackground)
	}
}

func TestRestoreBackgroundAfterANSIResetReappliesThemeCanvas(t *testing.T) {
	const background = "#1a1b26"
	const foreground = "#c0caf5"
	input := "before\x1b[0mafter\x1b[49mtail\x1b[mend"
	got := restoreBackgroundAfterANSIReset(input, background, foreground)
	wantRestore := "\x1b[48;2;26;27;38m\x1b[38;2;192;202;245m"
	for _, reset := range []string{"\x1b[0m", "\x1b[49m", "\x1b[m"} {
		if !strings.Contains(got, reset+wantRestore) {
			t.Fatalf("restored output = %q, want %q followed by theme canvas colors", got, reset)
		}
	}
	if !strings.HasPrefix(got, "\x1b[48;2;26;27;38m") {
		t.Fatalf("restored output = %q, want theme background prefix", got)
	}
}

func TestRestoreAfterANSIResetSkipsNonHexForeground(t *testing.T) {
	const background = "#1a1b26"
	input := "x\x1b[0my"
	got := restoreBackgroundAfterANSIReset(input, background, "116")
	if strings.Contains(got, "\x1b[38") {
		t.Fatalf("restored output = %q, want no foreground SGR for non-hex color", got)
	}
	if !strings.Contains(got, "\x1b[0m\x1b[48;2;26;27;38m") {
		t.Fatalf("restored output = %q, want background restored after reset", got)
	}
}

func TestViewReappliesThemeBackgroundAfterNestedStyleResets(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	model := newThemedTestModel(t, theme.TokyoNight)
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind: entryAssistant,
		body: "# Heading\n\nplain **bold** and `code`\n\n```go\nfmt.Println(\"hello\")\n```",
	}}
	model.relayout()
	model.refreshViewport()
	view := model.View()
	background := "\x1b[48;2;26;27;38m"
	if !strings.Contains(view, "\x1b[0m"+background) {
		t.Fatalf("view does not restore theme background after nested resets")
	}
}
