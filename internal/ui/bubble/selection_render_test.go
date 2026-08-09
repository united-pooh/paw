// 鼠标拖选高亮渲染的回归测试：拖拽中途视口必须跟随选区更新。
package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestTranscriptDragHighlightFollowsMotion 回归：拖拽过程中（多次 motion）
// viewport 渲染的选区高亮必须跟随 selectionEnd 更新。此前 applyTranscriptToViewport
// 只在「transcript 内容变化或首次进入选择」时重绘，而拖拽只改选区状态字段，
// 导致高亮停留在第一次超出单击容差时的选区，释放后屏幕高亮与复制内容不符。
func TestTranscriptDragHighlightFollowsMotion(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()

	selSGR := sgrPrefixOf(selectedTranscriptLineStyle.Render(" "))
	if selSGR == "" {
		t.Fatal("selection SGR empty under truecolor profile")
	}

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	lines := model.transcriptLineSnapshots()
	if !strings.Contains(lines[0].plain, "hello world") {
		t.Fatalf("snapshot plain = %q, want hello world", lines[0].plain)
	}
	y := model.transcriptScreenTop()

	next, _ := model.Update(tea.MouseMsg{X: mainContentPadding + 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)

	// motion1：col 2 -> 4，选区 2..5（"hel"）
	next, _ = model.Update(tea.MouseMsg{X: mainContentPadding + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	afterMotion1 := model.viewport.View()
	if !strings.Contains(afterMotion1, selSGR) {
		t.Fatalf("viewport after first motion has no selection highlight: %q", ansi.Strip(afterMotion1))
	}

	// motion2：col 4 -> 8，选区应扩展为 2..9（"hello w"）。
	// 回归点：视口必须重绘，不能停留在 motion1 的旧选区。
	next, _ = model.Update(tea.MouseMsg{X: mainContentPadding + 8, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.selectionEnd != (selectionPoint{row: 0, col: 9}) {
		t.Fatalf("selectionEnd = %+v, want {row:0 col:9}", model.selectionEnd)
	}
	afterMotion2 := model.viewport.View()
	if afterMotion2 == afterMotion1 {
		t.Fatalf("viewport content unchanged while dragging: %q", ansi.Strip(afterMotion2))
	}
	len1 := selectedRunLength(afterMotion1, selSGR)
	len2 := selectedRunLength(afterMotion2, selSGR)
	if len2 <= len1 {
		t.Fatalf("selection highlight did not grow with drag: motion1 run=%d motion2 run=%d (viewport %q)", len1, len2, ansi.Strip(afterMotion2))
	}

	// release：选区固定，视口高亮与复制文本一致（"hello w" = 7 个可见字符）。
	next, _ = model.Update(tea.MouseMsg{X: mainContentPadding + 8, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if got := model.selectedTranscriptText(); got != "hello w" {
		t.Fatalf("copied selection = %q, want hello w", got)
	}
	afterRelease := model.viewport.View()
	if got := selectedRunLength(afterRelease, selSGR); got != 7 {
		t.Fatalf("highlight run after release = %d, want 7 (viewport %q)", got, ansi.Strip(afterRelease))
	}
	// 选区未变时视口不应重复全量重绘（签名守卫生效）。
	if afterRelease != afterMotion2 {
		t.Fatalf("viewport changed between motion2 and release without selection change")
	}
}

// TestTranscriptDragHighlightStaysDuringWithinSlopMotion 容差内的 motion
// 不改选区，也不应触发高亮重绘或提前建立选区。
func TestTranscriptDragHighlightStaysDuringWithinSlopMotion(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()

	selSGR := sgrPrefixOf(selectedTranscriptLineStyle.Render(" "))
	if selSGR == "" {
		t.Fatal("selection SGR empty under truecolor profile")
	}

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "hello world",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()
	y := model.transcriptScreenTop()

	next, _ := model.Update(tea.MouseMsg{X: mainContentPadding + 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	// 容差内（+1 格）motion：不建立选区，视口不出现高亮。
	next, _ = model.Update(tea.MouseMsg{X: mainContentPadding + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.selectionActive {
		t.Fatal("within-slop motion created a selection")
	}
	if strings.Contains(model.viewport.View(), selSGR) {
		t.Fatalf("within-slop motion rendered selection highlight: %q", ansi.Strip(model.viewport.View()))
	}
}

// selectedRunLength 返回视口内容中首次出现 selSGR 之后的高亮可见字符数。
// 选中片段结构（applySelectionStyle）：fullSGR + [markdown SGR/reset 交错] +
// 文本 + ResetStyle。片段内部每个 reset 后都会重新断言 fullSGR，因此扫描
// 规则为：跳过所有 ESC 序列累计可见字符；遇到 reset 时，若其后紧跟 selSGR
// 则说明仍在选中 run 内（继续），否则 run 结束。
func selectedRunLength(view, selSGR string) int {
	idx := strings.Index(view, selSGR)
	if idx < 0 {
		return 0
	}
	rest := view[idx+len(selSGR):]
	count := 0
	for rest != "" {
		if rest[0] != '\x1b' {
			cluster, _ := terminalFirstGraphemeCluster(rest)
			if cluster == "" {
				break
			}
			rest = rest[len(cluster):]
			count++
			continue
		}
		if strings.HasPrefix(rest, "\x1b]") { // OSC 序列（如超链接）
			if end := strings.Index(rest, "\a"); end >= 0 {
				rest = rest[end+1:]
				continue
			}
			if end := strings.Index(rest, "\x1b\\"); end >= 0 {
				rest = rest[end+2:]
				continue
			}
			break
		}
		seq, _, consumed, _ := ansi.DecodeSequence(rest, ansi.NormalState, nil)
		if consumed <= 0 {
			break
		}
		rest = rest[consumed:]
		if sgrResetsState(seq) {
			if strings.HasPrefix(rest, selSGR) {
				rest = rest[len(selSGR):] // 选中 run 内部的重断言，继续
				continue
			}
			break
		}
	}
	return count
}
