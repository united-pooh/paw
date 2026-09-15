package bubble

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/capability/model"
	"paw/internal/runtime/loop"
	"paw/internal/ui/theme"
)

func TestCaptureVisualHierarchy(t *testing.T) {
	dir := os.Getenv("PAW_HIERARCHY_VISUAL_DIR")
	if dir == "" {
		t.Skip("set PAW_HIERARCHY_VISUAL_DIR to capture")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, width := range []int{60, 100, 140} {
		m := newThemedTestModel(t, theme.Dracula)
		m.modelConfig = &fakeModelConfigController{current: model.Config{Model: "z-ai/glm-5.3-flash", Provider: "openrouter", ContextLimitTokens: 256000}}
		m.runner = &fakeRunner{stats: loop.ContextStats{UsedTokens: 68700, CacheTokens: 65265, LimitTokens: 256000}}
		m.ready, m.hasInteracted = true, true
		m.width, m.height = width, 40
		m.cursorFrameAt = time.Date(2026, 9, 15, 16, 44, 0, 0, time.Local)
		m.worktree = worktreeSnapshot{name: "paw", ref: "main", state: worktreeClean, isGit: true}
		start, end := m.cursorFrameAt.Add(-154*time.Second), m.cursorFrameAt
		m.transcript = []transcriptEntry{
			{kind: entryUser, body: "帮我清理一下硬盘空间"},
			{kind: entrySystem, body: formatModelSwitchBlock(model.Config{Model: "z-ai/glm-5.3-flash", Provider: "openrouter", APIBaseURL: "https://openrouter.ai/api/v1", ContextLimitTokens: 256000, RetryCount: 3}, 0)},
			{kind: entryReasoning, body: "检查磁盘空间与缓存大小。", reasoningStartedAt: &start, reasoningFinishedAt: &end, createdAt: start},
			{kind: entryTool, toolName: "Bash", toolUseID: "disk", toolStatus: "error", isError: true, body: "Bash error permission denied", createdAt: end},
			{kind: entryAssistant, createdAt: end, body: "磁盘扫描完成：**228G 的磁盘剩余 7.8G（96% 满）**。以下是扫描到的缓存目录。\n\n### 可清理项目\n\n| 项目 | 大小 |\n| :--- | ---: |\n| `~/.cache/uv`（Python 包缓存） | 10G |\n| `~/.npm`（npm/npx 缓存） | 3.7G |\n| `~/.cache/codex-runtimes` | 1.7G |\n\n缓存可以重新生成。清理前仍需确认没有运行中的任务依赖这些文件。\n\n```sh\ndu -sh ~/.cache/uv ~/.npm\n```"},
		}
		for _, state := range []string{"top", "sticky"} {
			if state == "sticky" {
				m.height = 18
			}
			m.relayout()
			m.refreshViewport()
			m.viewport.GotoTop()
			if state == "sticky" {
				m.viewport.GotoBottom()
			}
			view := m.View()
			assertFixedFrame(t, view, width, m.height)
			for _, line := range strings.Split(view, "\n") {
				assertTerminalSequencesComplete(t, line)
			}
			stem := filepath.Join(dir, fmt.Sprintf("disk-%d-%s", width, state))
			page := `<!doctype html><html lang="zh"><meta charset="utf-8"><title>Paw TUI</title><style>body{margin:0;padding:24px;background:#161821;color:#f8f8f2}pre{display:inline-block;margin:0;background:#282a36;font:16px/1.45 Menlo,monospace;white-space:pre;padding:16px;border:1px solid #363945;border-radius:8px}h1{font:16px system-ui;color:#b5bac8;margin:0 0 16px}</style><h1>` + html.EscapeString(fmt.Sprintf("Paw · %d × %d terminal cells · %s", width, m.height, state)) + `</h1><pre>` + hierarchyVisualANSIToHTML(view) + `</pre></html>`
			for ext, body := range map[string]string{".html": page, ".ansi": view, ".txt": ansi.Strip(view)} {
				if err := os.WriteFile(stem+ext, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// Browser CJK fallback fonts are narrower than two terminal cells. Reserve the
// cell width explicitly so the exported backgrounds and columns match the TUI.
func hierarchyVisualANSIToHTML(view string) string {
	var out strings.Builder
	inTag := false
	for _, r := range activityVisualANSIToHTML(view) {
		if r == '<' {
			inTag = true
		}
		if !inTag && ansi.StringWidth(string(r)) == 2 {
			out.WriteString(`<span style="display:inline-block;width:2ch">` + string(r) + `</span>`)
		} else {
			out.WriteRune(r)
		}
		if r == '>' {
			inTag = false
		}
	}
	return out.String()
}
