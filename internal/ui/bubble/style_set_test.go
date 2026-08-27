package bubble

import (
	"fmt"
	"strings"
	"testing"

	"paw/internal/theme"
)

func TestNewStyleSetUsesThemePalette(t *testing.T) {
	item, ok := theme.ByID(theme.TokyoNight)
	if !ok {
		t.Fatal("Tokyo Night missing")
	}
	styles := NewStyleSet(item.Colors)
	if got := fmt.Sprint(styles.Body.GetForeground()); got != strings.ToLower(item.Colors.Body) {
		t.Fatalf("body foreground = %q, want %q", got, item.Colors.Body)
	}
	if got := fmt.Sprint(styles.Frame.GetBackground()); got != strings.ToLower(item.Colors.TerminalBackground) {
		t.Fatalf("frame background = %q, want %q", got, item.Colors.TerminalBackground)
	}
	if got := fmt.Sprint(styles.ToolDetail.GetBackground()); got != strings.ToLower(item.Colors.ToolDetailBackground) {
		t.Fatalf("tool background = %q, want %q", got, item.Colors.ToolDetailBackground)
	}
}

func TestSelectionSelectedSwapsNormalForegroundAndBackground(t *testing.T) {
	for _, item := range theme.List() {
		styles := NewStyleSet(item.Colors)
		if styles.SelectionSelected.GetBackground() != styles.SelectionNormal.GetForeground() {
			t.Fatalf("theme %s selected background = %v, want normal foreground %v", item.ID, styles.SelectionSelected.GetBackground(), styles.SelectionNormal.GetForeground())
		}
		if styles.SelectionSelected.GetForeground() != styles.SelectionNormal.GetBackground() {
			t.Fatalf("theme %s selected foreground = %v, want normal background %v", item.ID, styles.SelectionSelected.GetForeground(), styles.SelectionNormal.GetBackground())
		}
		if styles.SelectionFocusedSelected.GetBackground() != styles.SelectionSelected.GetBackground() ||
			styles.SelectionFocusedSelected.GetForeground() != styles.SelectionSelected.GetForeground() {
			t.Fatalf("theme %s focused-selected is not the same inverse style", item.ID)
		}
	}
}

func TestStyleSetsDoNotShareMutableState(t *testing.T) {
	tokyo, _ := theme.ByID(theme.TokyoNight)
	light, _ := theme.ByID(theme.TokyoNightLight)
	a := NewStyleSet(tokyo.Colors)
	b := NewStyleSet(light.Colors)
	if a.Body.GetForeground() == b.Body.GetForeground() {
		t.Fatal("independent themes unexpectedly share body color")
	}
}

func TestColorManagerCursorColorUsesProvidedPalette(t *testing.T) {
	dark, _ := theme.ByID(theme.TokyoNight)
	light, _ := theme.ByID(theme.TokyoNightLight)
	if got := NewColorManager(dark.Colors).CursorColor(0, false); got != dark.Colors.TerminalBackground {
		t.Fatalf("dark cursor at zero = %q", got)
	}
	if got := NewColorManager(light.Colors).CursorColor(0, false); got != light.Colors.TerminalBackground {
		t.Fatalf("light cursor at zero = %q", got)
	}
}
