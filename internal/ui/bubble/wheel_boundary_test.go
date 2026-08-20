package bubble

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newBoundaryScrollModel 构造 240 行内容、可双向滚动的测试模型。
func newBoundaryScrollModel() appModel {
	model := wheelFilterTestModel()
	lines := make([]string, 240)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %03d", index)
	}
	model.viewport.SetLines(lines)
	return model
}

// TestWheelFilterDropsBoundaryNoOpWheel 命题：撞边界的滚轮事件（底部继续下滑
// 或顶部继续上滑）不可能移动视口，filter 必须直接丢弃（返回 nil）而不是转
// 成 batch 占用队列槽位；任何能有效移动视口的事件都必须放行。
//
// 这是「边界积压」修复的目标行为。当前实现把边界事件也全部转成 batch（见
// TestLocalSessionQueuedScrollReversal 的 3002 全接受契约），本测试在该实现
// 下失败——用于先浮现问题。
func TestWheelFilterDropsBoundaryNoOpWheel(t *testing.T) {
	model := newBoundaryScrollModel()
	filter := newProgramEventFilter()
	down := wheelFilterMouse(tea.MouseButtonWheelDown)
	up := wheelFilterMouse(tea.MouseButtonWheelUp)

	// 中部：双向都是有效滚动，必须放行（防误伤）。
	model.viewport.SetYOffset(100)
	if got := filter.Filter(model, down); got == nil {
		t.Fatal("middle wheel-down was dropped, want pass-through as batch")
	}
	if got := filter.Filter(model, up); got == nil {
		t.Fatal("middle wheel-up was dropped, want pass-through as batch")
	}

	// 底部：继续下滑是边界无效事件，必须丢弃；离开底部（上滑）放行。
	model.viewport.GotoBottom()
	if !model.viewport.AtBottom() {
		t.Fatal("setup: model is not at bottom")
	}
	if got := filter.Filter(model, down); got != nil {
		t.Fatalf("boundary wheel-down at bottom queued %T, want dropped (nil)", got)
	}
	if got := filter.Filter(model, up); got == nil {
		t.Fatal("wheel-up away from bottom was dropped, want pass-through as batch")
	}

	// 顶部：继续上滑是边界无效事件，必须丢弃；离开顶部（下滑）放行。
	model.viewport.GotoTop()
	if !model.viewport.AtTop() {
		t.Fatal("setup: model is not at top")
	}
	if got := filter.Filter(model, up); got != nil {
		t.Fatalf("boundary wheel-up at top queued %T, want dropped (nil)", got)
	}
	if got := filter.Filter(model, down); got == nil {
		t.Fatal("wheel-down away from top was dropped, want pass-through as batch")
	}
}

// TestWheelBoundaryBurstDoesNotQueueBeforeReverse 命题（用户场景）：贴底后惯
// 性继续下滑产生的连发无效事件不得占用队列——紧随其后的反向事件必须是唯一
// 被处理的滚轮消息（反向零延迟）。
//
// 当前实现把 100 个边界无效事件全部转成 batch 排队（反向是第 101 个
// Update），本测试在该实现下失败——用于先浮现问题。
func TestWheelBoundaryBurstDoesNotQueueBeforeReverse(t *testing.T) {
	app := newBoundaryScrollModel()
	app.viewport.GotoBottom()
	bottomOffset := app.viewport.YOffset
	trace := &wheelProgramTrace{}
	input := strings.Repeat(wheelSGR(tea.MouseButtonWheelDown), 100) + wheelSGR(tea.MouseButtonWheelUp) + "q"
	program := tea.NewProgram(
		wheelProgramModel{app: app, trace: trace},
		tea.WithInput(&wheelEventReader{source: strings.NewReader(input)}),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithFilter(newProgramEventFilter().Filter),
	)
	final, err := program.Run()
	if err != nil {
		t.Fatalf("run boundary burst program: %v", err)
	}
	finalApp := final.(wheelProgramModel).app

	if trace.rawWheelUpdates != 0 {
		t.Fatalf("raw wheel updates = %d, want 0 (wheel must go through batch path)", trace.rawWheelUpdates)
	}
	if trace.batchUpdates != 1 {
		t.Fatalf("batch updates = %d, want 1 (only the reverse; 100 boundary no-ops must not queue)", trace.batchUpdates)
	}
	if finalApp.viewport.YOffset >= bottomOffset {
		t.Fatalf("reverse wheel-up did not move viewport: %d -> %d", bottomOffset, finalApp.viewport.YOffset)
	}
}
