package theme

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
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
			if item.ID == Default {
				if color == "" {
					continue
				}
				n, err := strconv.Atoi(color)
				if err != nil || n < 0 || n > 15 {
					t.Fatalf("default %s = %q, want terminal default or ANSI 0–15", role, color)
				}
				continue
			}
			if !hex.MatchString(color) {
				t.Fatalf("theme %q role %q color = %q, want #RRGGBB", item.ID, role, color)
			}
		}
	}
	if len(seen) != 7 {
		t.Fatalf("theme count = %d, want 7", len(seen))
	}
}

// 语法高亮四类 token 色必须在每个主题内互不相同（以及不同于正文色），
// 防止配色退化成"整块代码一个颜色"。
func TestSyntaxRolesAreDistinctWithinTheme(t *testing.T) {
	for _, item := range List() {
		p := item.Colors
		seen := map[string]string{}
		roles := map[string]string{
			"keyword": p.SyntaxKeyword, "string": p.SyntaxString,
			"number": p.SyntaxNumber, "comment": p.SyntaxComment,
		}
		for role, color := range roles {
			if color == p.Body {
				t.Fatalf("theme %q syntax %s = %s equals body color (invisible highlight)", item.ID, role, color)
			}
			if prev, dup := seen[color]; dup {
				t.Fatalf("theme %q syntax %s = %s duplicates %s", item.ID, role, color, prev)
			}
			seen[color] = role
		}
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
		if item.ID == Default {
			want = ModeTerminal
		}
		if item.ID == TokyoNightLight {
			want = ModeLight
		}
		if item.Mode != want {
			t.Fatalf("theme %q mode = %q, want %q", item.ID, item.Mode, want)
		}
	}
}

func TestDefaultPaletteFollowsTerminal(t *testing.T) {
	item, _ := ByID(Default)
	for role, color := range item.Colors.Values() {
		if strings.HasSuffix(role, ".background") && color != "" {
			t.Errorf("%s = %q, want terminal default", role, color)
		}
	}
	if item.Colors.Body != "" || item.Colors.HeaderForeground != "" || item.Colors.MarkdownCodeForeground != "" {
		t.Fatal("prose must use terminal default foreground")
	}
	if item.Colors.LabelError != "1" || item.Colors.WorktreeClean != "2" || item.Colors.MarkdownLink != "6" {
		t.Fatal("semantic colors must use terminal ANSI palette")
	}
}

func TestDefaultChromeRemainsReadableOverBrightTransparentDesktop(t *testing.T) {
	p, _ := ByID(Default)
	// Ghostty at 70% opacity over a white desktop is the brightest backdrop.
	backdrop := blendHex("#282c34", "#ffffff", 0.30)
	resolve := func(color string) string {
		switch color {
		case "":
			return "#ffffff"
		case "7":
			return "#c5c8c6"
		case "8":
			return "#666666"
		case "15":
			return "#eaeaea"
		default:
			t.Fatalf("unexpected neutral color %q", color)
			return ""
		}
	}
	for role, color := range map[string]string{
		"status/time": p.Colors.ContextFree, "system": p.Colors.LabelSystem,
		"placeholder": p.Colors.MarkdownRule, "reasoning": p.Colors.MarkdownQuote,
		"quote": p.Colors.MarkdownQuoteText, "unselected": p.Colors.UnselectedProvider,
		"comment": p.Colors.SyntaxComment,
	} {
		if ratio := contrastRatio(resolve(color), backdrop); ratio < 4.2 {
			t.Errorf("%s contrast %.2f:1 over %s, want >= 4.2:1", role, ratio, backdrop)
		}
	}
	for role, color := range map[string]string{"table": p.Colors.PanelBorder, "quote rail": p.Colors.MarkdownQuoteBorder, "worktree": p.Colors.WorktreeBorder, "waiting": p.Colors.InputWaitingBorder} {
		if ratio := contrastRatio(resolve(color), backdrop); ratio < 3 {
			t.Errorf("%s contrast %.2f:1, want >= 3:1", role, ratio)
		}
	}
}
