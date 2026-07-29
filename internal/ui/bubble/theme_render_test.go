package bubble

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
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
