package theme

import (
	"reflect"
	"regexp"
	"testing"
)

func TestListReturnsStableBuiltInOrder(t *testing.T) {
	gotThemes := List()
	got := make([]ThemeID, 0, len(gotThemes))
	for _, item := range gotThemes {
		got = append(got, item.ID)
	}
	want := []ThemeID{Default, TokyoNight, TokyoNightStorm, TokyoNightLight, CatppuccinMocha, Dracula, GruvboxDark}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("theme order = %#v, want %#v", got, want)
	}
}

func TestBuiltInThemesHaveCompleteTrueColorPalettes(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	seen := map[ThemeID]bool{}
	for _, item := range List() {
		if seen[item.ID] {
			t.Fatalf("duplicate theme id %q", item.ID)
		}
		seen[item.ID] = true
		if item.Name == "" {
			t.Fatalf("theme %q has empty name", item.ID)
		}
		for role, color := range item.Colors.Values() {
			if !hex.MatchString(color) {
				t.Fatalf("theme %q role %q color = %q, want #RRGGBB", item.ID, role, color)
			}
		}
	}
	if len(seen) != 7 {
		t.Fatalf("theme count = %d, want 7", len(seen))
	}
}

func TestNormalizeID(t *testing.T) {
	tests := map[string]ThemeID{" TOKYO-NIGHT ": TokyoNight, "DrAcUlA": Dracula, "": Default, "unknown": Default}
	for input, want := range tests {
		if got := NormalizeID(input); got != want {
			t.Fatalf("NormalizeID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTokyoNightLightIsOnlyLightTheme(t *testing.T) {
	for _, item := range List() {
		want := ModeDark
		if item.ID == TokyoNightLight {
			want = ModeLight
		}
		if item.Mode != want {
			t.Fatalf("theme %q mode = %q, want %q", item.ID, item.Mode, want)
		}
	}
}

func TestDefaultPaletteMatchesLegacyBaseline(t *testing.T) {
	item, ok := ByID(Default)
	if !ok {
		t.Fatal("default theme missing")
	}
	want := map[string]string{
		"terminal.background": "#292c33", "header.background": "#242830", "header.foreground": "#f0e6d5",
		"label.user": "#d98568", "label.assistant": "#f0e6d5", "label.tool": "#a9c8b5", "label.error": "#ef7d7d",
		"body": "#c9c2b7", "tool.detail.background": "#182830", "markdown.link": "#76d5e8", "panel.border": "#8e98a8",
		"input.terminal": "#ff5ac8", "context.used": "#76d5e8", "worktree.clean": "#a9c8b5",
	}
	values := item.Colors.Values()
	for role, expected := range want {
		if values[role] != expected {
			t.Fatalf("default %s = %q, want %q", role, values[role], expected)
		}
	}
}
