// 本文件定义输入光标的两相淡入淡出动画和颜色插值。
package bubble

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// applyCursorAnimation 发布真实终端光标颜色和隐藏状态。
// 光标呼吸已退役（活动指示收敛到底部 ⚒ 状态行），光标使用固定全亮。
func (m *appModel) applyCursorAnimation() {
	if m.cursorFrameAt.IsZero() {
		m.cursorFrameAt = time.Now()
	}
	if m.cursorAnchor != nil {
		brightRole := colorCursorNormalBright
		if m.isTerminalInputActive() {
			brightRole = colorCursorTerminalBright
		}
		m.cursorAnchor.setAnimation(terminalCursorAnimation{
			background: m.styles.Colors.Hex(colorTerminalBackground),
			bright:     m.styles.Colors.Hex(brightRole),
		})
		m.cursorAnchor.setVisual(terminalCursorVisual{
			color:   m.styles.Colors.CursorColor(1, m.isTerminalInputActive()),
			visible: true,
		})
	}
}

// clamp01 将浮点数限制在 0 到 1 之间。
func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// easeOutCubic 提供快速展开、末尾放缓的曲线，用于短时 UI 展开动画。
func easeOutCubic(t float64) float64 {
	t = 1 - clamp01(t)
	return 1 - t*t*t
}

// scheduleUIAnimationFrame ensures at most one Bubble Tea animation tick is in flight.
func (m *appModel) scheduleUIAnimationFrame() tea.Cmd {
	if m == nil || m.uiAnimationFrameScheduled {
		return nil
	}
	m.uiAnimationFrameScheduled = true
	return cursorFrameTick()
}

// scheduleClockTick ensures at most one idle clock tick is in flight.
// 空闲时钟链与动画帧链互斥：同一时刻至多存在一条链。
func (m *appModel) scheduleClockTick() tea.Cmd {
	if m == nil || m.clockTickScheduled {
		return nil
	}
	m.clockTickScheduled = true
	return clockTickCmd()
}

// Bubble Tea redraws. Cursor color itself is animated directly by anchoredOutput.
func (m appModel) needsUIAnimationFrames(now time.Time) bool {
	if m.isWorkRunning() || m.isGenerating || m.transcriptRefreshPending || m.assistantStream.HasPendingCharacters() || m.thinkingStream.HasPendingCharacters() {
		return true
	}
	if m.tokenRippleActive(now) {
		return true
	}
	if m.activity.visible {
		for _, task := range m.activity.tasks {
			if string(task.Status) == "running" {
				return true
			}
		}
	}
	// 运行中 task 任务卡：需要动画帧驱动 spinner 与任务状态刷新。
	if m.hasRunningTasks() {
		return true
	}
	return false
}
