package bubble

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestSessionRestoreDismissesPickerAndRebuildsFrame(t *testing.T) {
	setTrueColorForTest(t)
	for _, width := range []int{80, 120, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := newThemedTestModel(t, "dracula")
			m.ready, m.hasInteracted = true, true
			m.width, m.height = width, 32
			m.cursorFrameAt = time.Date(2026, 9, 20, 11, 10, 0, 0, time.UTC)
			m.transcript = []transcriptEntry{{kind: entryAssistant, body: strings.Repeat("OLD-SESSION-CONTENT\n\n", 40)}}
			m.relayout()
			m.refreshViewport()
			m.viewport.GotoTop()
			m.sessionPicker = &sessionPicker{sessions: []sessionSummaryItem{{sessionID: "restored", firstMessage: "查看项目表格"}}}
			pickerView := m.View()
			if !strings.Contains(ansi.Strip(pickerView), "Resume Session") {
				t.Fatal("fixture did not open session picker")
			}
			if width >= 120 {
				m.openActivity(activityTabTasks)
			}
			entries := []transcriptEntry{
				{kind: entryUser, body: "查看项目表格"},
				{kind: entryAssistant, body: markdownTableProjectFixture},
				{kind: entryError, title: "recovery", body: "previous turn failed: context canceled"},
			}
			for _, restored := range [][]transcriptEntry{entries, nil, entries} {
				next, _ := m.Update(sessionRestoredMsg{sessionID: "restored", entries: restored})
				m = next.(appModel)
				view := m.View()
				plain := ansi.Strip(view)
				assertFixedFrame(t, view, width, m.height)
				if m.sessionPicker != nil || m.activity.visible || len(m.transcript) != len(restored) {
					t.Fatal("restore retained modal, activity, or added transcript content")
				}
				wantWidth := m.currentLayout().workspaceWidth - transcriptContentStyle.GetHorizontalPadding()
				if m.viewport.Width != wantWidth {
					t.Fatalf("restored viewport width = %d, want %d", m.viewport.Width, wantWidth)
				}
				for _, unwanted := range []string{"OLD-SESSION-CONTENT", "Resume Session", "已切换到会话"} {
					if strings.Contains(plain, unwanted) {
						t.Fatalf("restored frame retained %q", unwanted)
					}
				}
				if len(restored) > 0 {
					for _, want := range []string{"查看项目表格", "internal/runtime", "previous turn failed"} {
						if !strings.Contains(plain, want) {
							t.Fatalf("restored frame missing %q", want)
						}
					}
				} else if strings.Contains(plain, "internal/runtime") || strings.Contains(plain, "recovery") {
					t.Fatal("empty session retained restored transcript")
				}
			}
			if dir := os.Getenv("PAW_SESSION_VISUAL_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				for name, view := range map[string]string{"picker": pickerView, "restored": m.View()} {
					page := `<!doctype html><html lang="zh"><meta charset="utf-8"><title>Paw session restore</title><style>body{margin:0;padding:20px;background:#282a36;color:#f8f8f2}pre{margin:0;font:16px/1.45 Menlo,monospace;white-space:pre}</style><pre>` + hierarchyVisualANSIToHTML(view) + `</pre></html>`
					stem := filepath.Join(dir, fmt.Sprintf("%s-%d", name, width))
					for ext, content := range map[string]string{".html": page, ".ansi": view} {
						if err := os.WriteFile(stem+ext, []byte(content), 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		})
	}
}
