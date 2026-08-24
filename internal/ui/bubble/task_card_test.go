// 本文件覆盖 task 任务卡（右侧垂直居中）与 <task> 结构化完成块的
// 解析、渲染和叠加行为。
package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"paw/internal/settings"
	"paw/internal/task"
	"paw/internal/ui"
)

func TestIsTaskCompletionBlock(t *testing.T) {
	for _, body := range []string{
		"<task id=\"abc\" state=\"completed\">\nsummary: done\n</task>",
		"  <task id=\"abc\" state=\"failed\">\n</task>\n",
	} {
		if !isTaskCompletionBlock(body) {
			t.Fatalf("isTaskCompletionBlock(%q) = false, want true", body)
		}
	}
	for _, body := range []string{
		"",
		"<task id=\"abc\" state=\"completed\">",
		"</task>",
		"plain text <task id=\"abc\">\n</task>",
		"<task id=\"abc\">\nno closing tag",
	} {
		if isTaskCompletionBlock(body) {
			t.Fatalf("isTaskCompletionBlock(%q) = true, want false", body)
		}
	}
}

func TestParseTaskCompletionBlock(t *testing.T) {
	body := "<task id=\"a6c81e94\" state=\"failed\" name=\"二叶筑\" duration_ms=\"42420\" output_size=\"1536\">\n" +
		"summary: 模型接口返回异常状态 400: invalid_request_error\n" +
		"error: missing field `output`\n" +
		"transcript: /tmp/a6c81e94/transcript.jsonl\n" +
		"output: /tmp/a6c81e94/output.json\n" +
		"</task>"
	info, ok := parseTaskCompletionBlock(body)
	if !ok {
		t.Fatalf("parseTaskCompletionBlock() = false, want true")
	}
	if info.ID != "a6c81e94" || info.State != "failed" || info.Name != "二叶筑" {
		t.Fatalf("task info = %#v", info)
	}
	if info.DurationMS != 42420 || info.OutputSize != 1536 {
		t.Fatalf("task info = %#v, want duration/size parsed", info)
	}
	if !strings.Contains(info.Summary, "invalid_request_error") || info.Error != "missing field `output`" {
		t.Fatalf("task info = %#v, want summary/error sections", info)
	}
	if info.Transcript != "/tmp/a6c81e94/transcript.jsonl" || info.Output != "/tmp/a6c81e94/output.json" {
		t.Fatalf("task info = %#v, want paths", info)
	}
}

func TestParseTaskCompletionBlockUnescapesAttributes(t *testing.T) {
	body := "<task id=\"a&amp;b&lt;c&gt;d&quot;e\" state=\"completed\" name=\"&quot;quoted&quot;\">\n</task>"
	info, ok := parseTaskCompletionBlock(body)
	if !ok {
		t.Fatalf("parseTaskCompletionBlock() = false, want true")
	}
	if info.ID != "a&b<c>d\"e" || info.Name != `"quoted"` {
		t.Fatalf("task info = %#v, want unescaped attribute values", info)
	}
}

func TestRenderTaskCompletionCardStates(t *testing.T) {
	cases := []struct {
		state  string
		marker string
		text   string
	}{
		{"completed", "✓", "完成"},
		{"failed", "✗", "失败"},
		{"stopped", "■", "已停止"},
		{"interrupted", "!", "已中断"},
		{"unknown-state", "✓", "完成"},
	}
	for _, tc := range cases {
		body := "<task id=\"task-1\" state=\"" + tc.state + "\" name=\"角色A\">\nsummary: 结果摘要\n</task>"
		rendered := ansi.Strip(renderTaskCompletionCard(body, 60))
		if !strings.Contains(rendered, tc.marker) || !strings.Contains(rendered, tc.text) {
			t.Fatalf("state %q rendered = %q, want marker %q + %q", tc.state, rendered, tc.marker, tc.text)
		}
		if !strings.Contains(rendered, "角色A") || !strings.Contains(rendered, "结果摘要") {
			t.Fatalf("state %q rendered = %q, want name and summary", tc.state, rendered)
		}
	}
}

func TestRenderTaskCompletionCardFallsBackToPlainBody(t *testing.T) {
	rendered := renderTaskCompletionCard("<task id=\"x\"", 60)
	if !strings.Contains(rendered, "<task id=\"x\"") {
		t.Fatalf("fallback rendered = %q, want raw body preserved", rendered)
	}
}

func TestTaskBlockMetaLineFormatsDurationAndSize(t *testing.T) {
	meta := taskBlockMetaLine(taskBlockInfo{ID: "a6c81e94", DurationMS: 42420, OutputSize: 1536})
	for _, want := range []string{"42s", "1.5KB"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("meta line = %q, want %q", meta, want)
		}
	}
	if strings.Contains(meta, "a6c81e94") {
		t.Fatalf("meta line = %q, should not show task id / session id", meta)
	}
	if got := taskBlockMetaLine(taskBlockInfo{}); got != "" {
		t.Fatalf("empty meta line = %q, want empty", got)
	}
}

func TestSystemTaskBlockEntryRendersAsCard(t *testing.T) {
	entry := transcriptEntry{
		kind:  entrySystem,
		title: "task",
		body:  "<task id=\"task-9\" state=\"completed\" name=\"深潜者\">\nsummary: 完成工作\n</task>",
	}
	rendered := ansi.Strip(renderEntryAt(entry, 60, time.Time{}, false))
	for _, want := range []string{"✓", "深潜者", "完成", "摘要: 完成工作"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "task") {
		t.Fatalf("rendered = %q, should not show the task system label", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > 60 {
			t.Fatalf("line width = %d, want <= 60: %q", got, line)
		}
	}
}

func TestRenderTaskCardEmpty(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{}
	if card := model.renderTaskCard(time.Time{}); card != "" {
		t.Fatalf("task card = %q, want empty for no running tasks", card)
	}
}

func TestRenderTaskCardListsRunningOnly(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "running-1", Name: "二叶筑", Color: "#ff9900", Status: task.TaskRunning},
		{ID: "done-1", Name: "已完成", Status: task.TaskCompleted},
		{ID: "running-2", Status: task.TaskRunning},
	}}
	card := ansi.Strip(model.renderTaskCard(time.Time{}))
	for _, want := range []string{"taskController · 2 运行中", "二叶筑", "task"} {
		if !strings.Contains(card, want) {
			t.Fatalf("task card = %q, want %q", card, want)
		}
	}
	if strings.Contains(card, "done-1") || strings.Contains(card, "已完成") {
		t.Fatalf("task card = %q, should not list completed tasks", card)
	}
	if strings.Contains(card, "running-1") || strings.Contains(card, "running-2") {
		t.Fatalf("task card = %q, should not show task id / session id", card)
	}
	for _, line := range strings.Split(card, "\n") {
		if got := lipgloss.Width(line); got > taskCardMaxWidth {
			t.Fatalf("task card line width = %d, want <= %d: %q", got, taskCardMaxWidth, line)
		}
	}
}

type activeTaskController struct {
	*fakeTaskController
	active []task.TaskSnapshot
}

func (c *activeTaskController) ActiveTasks() []task.TaskSnapshot {
	return append([]task.TaskSnapshot(nil), c.active...)
}

func TestRenderTaskCardUsesActiveTasksInsteadOfStaleRunningProjection(t *testing.T) {
	stale := task.TaskSnapshot{ID: "stale-running", Name: "stale worker", Status: task.TaskRunning}
	controller := &activeTaskController{
		fakeTaskController: &fakeTaskController{tasks: []task.TaskSnapshot{stale}},
	}
	model := newTestModel(&fakeRunner{})
	model.taskController = controller

	if card := model.renderTaskCard(time.Now()); card != "" {
		t.Fatalf("task card = %q, want stale projected task hidden after its process ended", ansi.Strip(card))
	}
	if model.hasRunningTasks() {
		t.Fatal("hasRunningTasks = true, want process liveness to override stale running projection")
	}

	controller.active = []task.TaskSnapshot{stale}
	if card := ansi.Strip(model.renderTaskCard(time.Now())); !strings.Contains(card, "stale worker") {
		t.Fatalf("task card = %q, want active task visible", card)
	}
}

func TestSpinnerFrameIndexRotates(t *testing.T) {
	base := time.Unix(0, 0)
	if spinnerFrameIndex(base) != 0 {
		t.Fatalf("spinnerFrameIndex(zero) = %d, want 0", spinnerFrameIndex(base))
	}
	seen := make(map[int]bool)
	for i := 0; i < len(taskSpinnerFrames)*4; i++ {
		frame := spinnerFrameIndex(base.Add(time.Duration(i) * 250 * time.Millisecond))
		seen[frame] = true
	}
	if len(seen) != len(taskSpinnerFrames) {
		t.Fatalf("spinner frames seen = %d, want %d", len(seen), len(taskSpinnerFrames))
	}
}

func TestPlaceRightCenteredOverlayPosition(t *testing.T) {
	width, height := 40, 10
	base := strings.Repeat("x\n", height-1) + "x"
	overlay := "AAA\nBBB"
	composed := ansi.Strip(placeRightCenteredOverlay(base, overlay, width, height))
	lines := strings.Split(composed, "\n")
	if len(lines) != height {
		t.Fatalf("composed lines = %d, want %d", len(lines), height)
	}
	// 垂直居中：overlay 高度 2，首行应在 (10-2)/2 = 4。
	if idx := strings.Index(lines[4], "AAA"); idx != 36 {
		t.Fatalf("line 4 = %q, want overlay first row at column 36 (got %d)", lines[4], idx)
	}
	if idx := strings.Index(lines[5], "BBB"); idx != 36 {
		t.Fatalf("line 5 = %q, want overlay second row at column 36 (got %d)", lines[5], idx)
	}
	// 其余行保持 base 内容。
	if !strings.Contains(lines[0], "x") || strings.Contains(lines[0], "AAA") {
		t.Fatalf("line 0 = %q, want untouched base", lines[0])
	}
}

func TestRenderTaskCardRowHidesSessionID(t *testing.T) {
	row := ansi.Strip(renderTaskCardRow(task.TaskSnapshot{
		ID:    "a6c81e94d94a5e40",
		Name:  "八潮瑠唯",
		Color: "#669988",
	}, "◐"))
	for _, want := range []string{"◐", "八潮瑠唯"} {
		if !strings.Contains(row, want) {
			t.Fatalf("task row = %q, want %q", row, want)
		}
	}
	if strings.Contains(row, "a6c81e94") {
		t.Fatalf("task row = %q, should not show task id / session id", row)
	}
	fallback := ansi.Strip(renderTaskCardRow(task.TaskSnapshot{ID: "x123", Status: task.TaskRunning}, "◐"))
	if !strings.Contains(fallback, "task") || strings.Contains(fallback, "x123") {
		t.Fatalf("nameless task row = %q, want generic name without id", fallback)
	}
}

func TestRunningTasksKeepsAnimationFrames(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.taskController = &fakeTaskController{}
	if model.needsUIAnimationFrames(time.Now()) {
		t.Fatal("animation frames needed without running tasks")
	}
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "run-1", Status: task.TaskRunning},
		{ID: "done-1", Status: task.TaskCompleted},
	}}
	if !model.needsUIAnimationFrames(time.Now()) {
		t.Fatal("animation frames not needed with a running task")
	}
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "done-1", Status: task.TaskCompleted},
	}}
	if model.needsUIAnimationFrames(time.Now()) {
		t.Fatal("animation frames needed with only completed tasks")
	}
}

func TestSystemEventTaskBlockAppearsInViewport(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()

	next, _ := model.Update(systemEventMsg(uiSystemEventTaskBlock()))
	model = next.(appModel)

	rendered := ansi.Strip(model.viewport.View())
	for _, want := range []string{"✓", "二叶筑", "完成", "摘要"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("viewport = %q, want task card content %q", rendered, want)
		}
	}
}

func uiSystemEventTaskBlock() systemEventMsg {
	return systemEventMsg(ui.SystemEvent{
		Title: "task",
		Body:  "<task id=\"a6c81e94\" state=\"completed\" name=\"二叶筑\">\nsummary: 后台调查完成\n</task>",
	})
}

func TestSettingsDefaultWaitTimeout(t *testing.T) {
	cfg := settings.DefaultConfig()
	if cfg.Task.WaitTimeoutMs != settings.DefaultTaskWaitTimeoutMs {
		t.Fatalf("default wait_timeout_ms = %d, want %d", cfg.Task.WaitTimeoutMs, settings.DefaultTaskWaitTimeoutMs)
	}
}
