package bubble

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/ui/theme"
)

func TestMarkdownTableViewHasUniformCanvas(t *testing.T) {
	setTrueColorForTest(t)
	for _, item := range theme.List() {
		for _, width := range []int{60, 100, 140} {
			t.Run(fmt.Sprintf("%s-%d", item.ID, width), func(t *testing.T) {
				m := newThemedTestModel(t, item.ID)
				m.ready, m.hasInteracted = true, true
				m.width, m.height = width, 60
				m.transcript = []transcriptEntry{{kind: entryAssistant, body: markdownTableProjectFixture}}
				m.relayout()
				m.refreshViewport()
				m.viewport.GotoTop()
				view := m.View()
				assertFixedFrame(t, view, width, m.height)
				var rows []string
				complete := false
				for _, line := range strings.Split(view, "\n") {
					plain := ansi.Strip(line)
					if len(rows) == 0 && !strings.Contains(plain, "╭") {
						continue
					}
					rows = append(rows, line)
					if strings.Contains(plain, "╰") {
						complete = true
						break
					}
				}
				if !complete {
					t.Fatal("complete table not visible in fixture")
				}
				table := strings.Join(rows, "\n")
				background := string(m.styles.Frame.GetBackground().(lipgloss.Color))
				assertTableCellBackground(t, table, background)
				for _, line := range rows {
					assertTerminalSequencesComplete(t, line)
				}
				if dir := os.Getenv("PAW_TABLE_VISUAL_DIR"); dir != "" {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					stem := filepath.Join(dir, fmt.Sprintf("table-%s-%d", item.ID, width))
					page := fmt.Sprintf(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>Paw table</title><style>body{margin:0;padding:20px;background:%s;color:%s}pre{margin:0;font:18px/1.5 Menlo,monospace;white-space:pre}</style><pre>%s</pre></html>`, background, m.theme.Colors.Body, hierarchyVisualANSIToHTML(table))
					for ext, content := range map[string]string{".html": page, ".ansi": table, ".txt": ansi.Strip(table)} {
						if err := os.WriteFile(stem+ext, []byte(content), 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}
