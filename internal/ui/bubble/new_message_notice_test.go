package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/ui"
)

func TestNewMessageNoticeText(t *testing.T) {
	if got := newMessageNoticeText(3, false, 80); got != "↓ 3 条新消息" {
		t.Fatalf("default text = %q", got)
	}
	if got := newMessageNoticeText(3, true, 80); got != "↓ 3 条新消息 · 回到底部" {
		t.Fatalf("hover text = %q", got)
	}
}

func TestNewMessageNoticeRenderUsesBackgroundOnly(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.newMessageNoticeCount = 3
	rendered := model.renderNewMessageNotice(80)
	if rendered == "" || !strings.Contains(ansi.Strip(rendered), "↓ 3 条新消息") {
		t.Fatalf("rendered notice = %q", rendered)
	}
	if got := model.styles.Notice.GetBackground(); got != model.styles.Colors.LipglossColor(colorWorktreeBackground) {
		t.Fatalf("notice background = %#v", got)
	}
	if strings.Contains(ansi.Strip(rendered), "┌") || strings.Contains(ansi.Strip(rendered), "─") {
		t.Fatalf("rendered notice contains a border: %q", ansi.Strip(rendered))
	}
}

func TestNewMessageNoticeBoundsAreCentered(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 10
	model.relayout()
	model.newMessageNoticeCount = 2
	bounds := model.transcriptNoticeBounds()
	if bounds.width <= 0 || bounds.height != 1 {
		t.Fatalf("notice bounds = %#v", bounds)
	}
	want := (model.currentLayout().contentWidth - bounds.width) / 2
	if bounds.x != want {
		t.Fatalf("notice x = %d, want %d", bounds.x, want)
	}
}

func TestNewMessageNoticeHiddenWhenCountIsZero(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if got := model.renderNewMessageNotice(80); got != "" {
		t.Fatalf("zero-count notice = %q, want hidden", got)
	}
}

func TestAssistantActivityCountsOnceWhileAwayFromBottom(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)

	next, _ := model.Update(assistantDeltaMsg("first line\nsecond line\n"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("third line\n"))
	model = next.(appModel)

	if model.newMessageNoticeCount != 1 {
		t.Fatalf("assistant notice count = %d, want 1", model.newMessageNoticeCount)
	}
}

func TestThinkingToolAndSystemActivitiesCount(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)

	next, _ := model.Update(thinkingDeltaMsg("private thought\n"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("answer\n"))
	model = next.(appModel)
	next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Read"}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: "ok"}))
	model = next.(appModel)
	next, _ = model.Update(systemEventMsg(ui.SystemEvent{Title: "后台任务", Body: "完成"}))
	model = next.(appModel)

	if model.newMessageNoticeCount != 4 {
		t.Fatalf("activity notice count = %d, want 4", model.newMessageNoticeCount)
	}
}

func TestRunningToolDoesNotCountUntilResult(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)

	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Read"}))
	model = next.(appModel)
	if model.newMessageNoticeCount != 0 {
		t.Fatalf("running tool notice count = %d, want 0", model.newMessageNoticeCount)
	}

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: "ok"}))
	model = next.(appModel)
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("tool result notice count = %d, want 1", model.newMessageNoticeCount)
	}
}

func TestBottomActivityDoesNotCount(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	next, _ := model.Update(assistantDeltaMsg("visible at bottom\n"))
	model = next.(appModel)

	if model.newMessageNoticeCount != 0 || !model.viewport.AtBottom() {
		t.Fatalf("bottom state = count %d atBottom %v", model.newMessageNoticeCount, model.viewport.AtBottom())
	}
}

func TestViewRendersNewMessageNoticeAboveStatusLine(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.newMessageNoticeCount = 3
	view := ansi.Strip(model.View())
	lines := strings.Split(view, "\n")
	noticeRow, statusRow := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "↓ 3 条新消息") {
			noticeRow = i
		}
		if strings.Contains(line, "ready") && strings.Contains(line, "chat") {
			statusRow = i
		}
	}
	if noticeRow < 0 || statusRow < 0 || noticeRow >= statusRow {
		t.Fatalf("notice row=%d status row=%d\n%s", noticeRow, statusRow, view)
	}
}

func TestClickingNewMessageNoticeGoesToBottomAndClears(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.newMessageNoticeCount = 2
	bounds := model.transcriptNoticeBounds()
	x, y := bounds.x+bounds.width/2, bounds.y

	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)

	if model.newMessageNoticeCount != 0 || !model.viewport.AtBottom() {
		t.Fatalf("after click: count=%d atBottom=%v", model.newMessageNoticeCount, model.viewport.AtBottom())
	}
}

func TestNoticeHoverChangesCopyWithoutStartingSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.newMessageNoticeCount = 1
	bounds := model.transcriptNoticeBounds()
	next, _ := model.Update(tea.MouseMsg{X: bounds.x, Y: bounds.y, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if !model.newMessageNoticeHovered || model.selecting {
		t.Fatalf("hover state=%v selecting=%v", model.newMessageNoticeHovered, model.selecting)
	}
	if got := ansi.Strip(model.renderNewMessageNotice(model.width)); !strings.Contains(got, "回到底部") {
		t.Fatalf("hover copy = %q", got)
	}
}

func TestScrollingToBottomClearsNewMessageNotice(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.newMessageNoticeCount = 2
	model.transcriptKeyScrollActive = true

	for !model.viewport.AtBottom() {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = next.(appModel)
	}
	if model.newMessageNoticeCount != 0 {
		t.Fatalf("notice count after down-to-bottom = %d, want 0", model.newMessageNoticeCount)
	}
}

func TestSelectionDoesNotPauseNewMessageNotice(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.selectionActive = true
	next, _ := model.Update(systemEventMsg(ui.SystemEvent{Title: "后台任务", Body: "完成"}))
	model = next.(appModel)
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("selection notice count = %d, want 1", model.newMessageNoticeCount)
	}
}

func TestClearCommandRemovesNoticeState(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.newMessageNoticeCount = 3
	model.newMessageNoticeHovered = true
	handled, _ := model.handleCommand("/clear")
	if !handled {
		t.Fatal("/clear was not handled")
	}
	if model.newMessageNoticeCount != 0 || model.newMessageNoticeHovered {
		t.Fatalf("clear state = count %d hovered %v", model.newMessageNoticeCount, model.newMessageNoticeHovered)
	}
}
