// 本文件定义输入光标的两相淡入淡出动画和颜色插值。
package bubble

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// applyCursorAnimation 根据当前时间帧发布真实终端光标颜色和隐藏状态。
func (m *appModel) applyCursorAnimation() {
	if m.cursorFrameAt.IsZero() {
		m.cursorFrameAt = time.Now()
	}
	// cursorFrameAt can intentionally stop advancing while the TUI is idle.
	// Keyboard events must not republish that stale frame, otherwise every key
	// jumps the real cursor gradient back to the same point before the output
	// ticker advances it again.
	cursorNow := time.Now()
	intensity := cursorIntensityAt(cursorCycleOffset(cursorNow))
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
			color:   m.styles.Colors.CursorColor(intensity, m.isTerminalInputActive()),
			visible: true,
		})
	}
}

// cursorCycleOffset 计算当前时间落在 3 秒光标周期中的偏移。
func cursorCycleOffset(at time.Time) time.Duration {
	return time.Duration(at.UnixNano()) % cursorCycleDuration
}

// cursorIntensityAt 根据周期偏移计算非线性的光标亮度。
func cursorIntensityAt(offset time.Duration) float64 {
	phase := float64(offset) / float64(cursorCycleDuration)
	halfPhase := phase
	if halfPhase >= 0.5 {
		halfPhase -= 0.5
	}
	switch {
	case halfPhase < 1.0/12.0:
		return 0
	case halfPhase < 1.0/4.0:
		return easeInOutSine((halfPhase - 1.0/12.0) / (1.0 / 6.0))
	case halfPhase < 1.0/3.0:
		return 1
	default:
		return 1 - easeInOutSine((halfPhase-1.0/3.0)/(1.0/6.0))
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

// easeInOutSine 提供平滑的缓入缓出曲线，用于光标淡入淡出。
func easeInOutSine(t float64) float64 {
	t = clamp01(t)
	return t * t * (3 - 2*t)
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
	if !m.waveAmpStartedAt.IsZero() && now.Sub(m.waveAmpStartedAt) < equalizerAmpDuration {
		return true
	}
	if m.taskPicker != nil {
		for _, task := range m.taskPicker.tasks {
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
