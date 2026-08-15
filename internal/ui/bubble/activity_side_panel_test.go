// 覆盖 ctrl+g 打开的 Activity 面板以右侧边栏形态渲染：
// 不经过居中 modal 合成，面板打开时右侧悬浮任务卡不重复显示。
package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/subagent"
)

// TestActivityPanelRendersAsRightSidebar 验证 subagentPicker 打开时：
// renderActiveModalBox 不再居中渲染 Activity；renderTranscriptRegion 把
// Activity 面板合成在内容区右端（面板文本出现在行末附近而非居中）。
func TestActivityPanelRendersAsRightSidebar(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.subagents = &fakeSubagentController{tasks: []subagent.TaskSnapshot{
		{ID: "agent-1", SessionID: "agent-1", ParentSessionID: "session-1", Name: "worker", Status: subagent.TaskCompleted},
	}}
	model.relayout()
	model.openActivity(activityTabSubagents)

	layout := model.currentLayout()
	if modal := model.renderActiveModalBox(layout); modal != "" {
		t.Fatalf("renderActiveModalBox = %q, want Activity rendered as right sidebar, not centered modal", modal)
	}

	rendered := ansi.Strip(model.renderTranscriptRegion(layout))
	if !strings.Contains(rendered, "Activity") || !strings.Contains(rendered, "Subagents") || !strings.Contains(rendered, "worker") {
		t.Fatalf("transcript region = %q, want Activity sidebar content", rendered)
	}
	// 面板应贴在右端：包含 "Activity" 标题的行，其列位置应超过内容区中线。
	activityColumn := -1
	for _, line := range strings.Split(rendered, "\n") {
		if idx := strings.Index(line, "Activity"); idx >= 0 {
			activityColumn = idx
			break
		}
	}
	if activityColumn < 0 {
		t.Fatalf("transcript region missing Activity title:\n%s", rendered)
	}
	if activityColumn <= layout.contentWidth/2 {
		t.Fatalf("Activity column = %d, want right half of content width %d:\n%s", activityColumn, layout.contentWidth, rendered)
	}
}

// TestActivityPanelHidesFloatingTaskCard 验证 Activity 面板打开时，右侧悬浮
// 任务卡（"运行中" 标题）不再重复渲染；任务本身仍出现在面板列表中。
func TestActivityPanelHidesFloatingTaskCard(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.subagents = &fakeSubagentController{tasks: []subagent.TaskSnapshot{
		{ID: "agent-1", SessionID: "agent-1", ParentSessionID: "session-1", Name: "worker", Status: subagent.TaskRunning, StartedAt: time.Now()},
	}}
	model.relayout()
	model.openActivity(activityTabSubagents)

	rendered := ansi.Strip(model.renderTranscriptRegion(model.currentLayout()))
	if strings.Contains(rendered, "运行中") {
		t.Fatalf("transcript region = %q, want floating task card hidden while Activity panel is open", rendered)
	}
	if !strings.Contains(rendered, "worker") {
		t.Fatalf("transcript region = %q, want task visible in Activity panel list", rendered)
	}
}

// TestActivityPanelLayoutKeepsTranscriptVisible 验证右侧面板不占满整个
// transcript：面板宽度小于内容区宽度，transcript 内容仍在左侧可见。
func TestActivityPanelLayoutKeepsTranscriptVisible(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.relayout()
	model.openActivity(activityTabSubagents)

	panel := model.renderActivityBox()
	panelWidth := lipgloss.Width(panel)
	contentWidth := model.currentLayout().contentWidth
	if panelWidth >= contentWidth {
		t.Fatalf("Activity panel width = %d, want < content width %d", panelWidth, contentWidth)
	}
	if panelWidth > 60 {
		t.Fatalf("Activity panel width = %d, want <= 60", panelWidth)
	}
}
