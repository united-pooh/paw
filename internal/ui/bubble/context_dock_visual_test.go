package bubble

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/runtime/loop"
	"paw/internal/ui/theme"
)

func TestContextDockFullView(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, tc := range []struct {
		width   int
		used    int
		theme   theme.ThemeID
		sidebar bool
		want    string
	}{
		{80, 244000, theme.TokyoNightLight, false, "23%"},
		{80, 1047500, theme.TokyoNight, false, "99.9%"},
		{40, 1174405, theme.TokyoNight, false, "112%"},
		{120, 244000, theme.TokyoNightLight, true, "23%"},
	} {
		t.Run(fmt.Sprintf("%d-%s-%d", tc.width, tc.theme, tc.used), func(t *testing.T) {
			m := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: tc.used, CacheTokens: 105000, LimitTokens: 1048576}})
			m.width, m.height, m.ready, m.hasInteracted = tc.width, 16, true, true
			m.cursorFrameAt = time.Unix(0, 0)
			m.worktree = worktreeSnapshot{name: "paw", ref: "dev", isGit: true}
			if err := m.applyTheme(tc.theme); err != nil {
				t.Fatal(err)
			}
			m.activity.visible = tc.sidebar
			m.relayout()
			view := ansi.Strip(m.View())
			assertFixedFrame(t, m.View(), tc.width, 16)
			lines := strings.Split(view, "\n")
			if strings.Count(view, copyContextLabel) != 1 || !strings.Contains(lines[len(lines)-1], tc.want) || !strings.Contains(lines[len(lines)-1], copyModeChat) {
				t.Fatalf("context must occur once on mode row:\n%s", view)
			}
			captureContextLimitView(t, fmt.Sprintf("dock-%d-%s-%d", tc.width, tc.theme, tc.used), m, tc.want)
		})
	}
}
