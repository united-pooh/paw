// 本文件定义输入光标的两相淡入淡出动画和颜色插值。
package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"time"
)

// applyCursorAnimation 根据当前时间帧更新 textarea 光标颜色和隐藏状态。
func (m *appModel) applyCursorAnimation() {
	if m.cursorFrameAt.IsZero() {
		m.cursorFrameAt = time.Now()
	}
	intensity := cursorIntensityAt(cursorCycleOffset(m.cursorFrameAt))
	m.input.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(cursorColor(intensity, m.isTerminalInputActive())))
	m.input.Cursor.Blink = intensity <= cursorHiddenThreshold
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
