// 本文件定义 transcript 右侧垂直居中的运行中 task 任务卡。
// 卡片只列出 running 任务；任务进入终态后即从卡片移除（持久记录由
// <task> 完成块保留在 transcript 流中）。样式与颜色跟随当前主题。
package bubble

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	taskpkg "paw/internal/task"
)

// taskSpinnerFrames 是任务卡 spinner 的旋转帧，由动画帧驱动换帧。
var taskSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

// taskCardMaxWidth 是任务卡的显示宽度上限（含边框）。
const taskCardMaxWidth = 32

// runningTasks 返回当前所有仍有本地执行进程的 running 任务。Manager 的
// durable projection 可能在进程退出后短暂滞后；悬浮卡使用进程存活视图避免残留。
func (m appModel) runningTasks() []taskpkg.TaskSnapshot {
	if m.taskController == nil {
		return nil
	}
	if active, ok := m.taskController.(ActiveTaskController); ok {
		return active.ActiveTasks()
	}
	tasks := m.taskController.ListTasks()
	running := make([]taskpkg.TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == taskpkg.TaskRunning {
			running = append(running, task)
		}
	}
	return running
}

// hasRunningTasks 报告是否存在运行中的 task 任务。
// 供动画帧驱动判断是否需要持续重绘（spinner 与任务状态变化）。
func (m appModel) hasRunningTasks() bool {
	return len(m.runningTasks()) > 0
}

// renderTaskCard 渲染运行中任务卡；没有 running 任务时返回空串。
//
//	┌ taskController · 2 运行中 ──┐
//	│ ◐ 二叶筑               │
//	│ ◐ 深潜者               │
//	└───────────────────────┘
func (m appModel) renderTaskCard(now time.Time) string {
	running := m.runningTasks()
	if len(running) == 0 {
		return ""
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorManager.LipglossColor(colorMarkdownQuoteBorder)).
		Padding(0, 1)

	title := lipgloss.NewStyle().
		Foreground(colorManager.LipglossColor(colorWorktreeClean)).
		Bold(true).
		Render("taskController · " + itoa(len(running)) + " 运行中")

	lines := make([]string, 0, len(running)+1)
	lines = append(lines, title)
	spinner := taskSpinnerFrames[spinnerFrameIndex(now)]
	for _, task := range running {
		lines = append(lines, renderTaskCardRow(task, spinner))
	}

	// lipgloss 的 Width 含 padding 但不含 border：styleWidth 只减边框，
	// body 再 fit 到减去 padding 后的宽度，最终总宽精确等于卡片上限。
	horizontalBorder := cardStyle.GetHorizontalBorderSize()
	styleWidth := maxInt(1, taskCardMaxWidth-horizontalBorder)
	bodyWidth := maxInt(1, styleWidth-cardStyle.GetHorizontalPadding())
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, fitStyledCellLine(truncateStyledCellLine(line, bodyWidth), bodyWidth))
	}
	return cardStyle.Width(styleWidth).Render(strings.Join(content, "\n"))
}

// renderTaskCardRow 渲染单行任务：spinner + 名称（persona 色）。
// 不显示任务 id / session id。
func renderTaskCardRow(task taskpkg.TaskSnapshot, spinner string) string {
	nameStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorBody))
	if color := strings.TrimSpace(task.Color); color != "" {
		nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = "task"
	}

	return spinnerStyle.Render(spinner) +
		" " +
		nameStyle.Render(name)
}

// spinnerStyle 是任务卡 spinner 的颜色（与 mockup 的暖色一致）。
var spinnerStyle = lipgloss.NewStyle().
	Foreground(colorManager.LipglossColor(colorWorktreeDirty))

// spinnerFrameIndex 按时间取 spinner 帧索引；now 为零值时返回 0（静态帧）。
func spinnerFrameIndex(now time.Time) int {
	if now.IsZero() {
		return 0
	}
	return int(now.UnixMilli()/250) % len(taskSpinnerFrames)
}

// itoa 是 strconv.Itoa 的薄封装，避免任务卡文件引入额外导入。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buf[index] = '-'
	}
	return string(buf[index:])
}

// placeRightCenteredOverlay 把 overlay 贴在 base 右边界内侧并垂直居中，
// 行内替换使用与 placeOpaqueOverlay 相同的 cell 级合成，保留 base 的样式。
func placeRightCenteredOverlay(base, overlay string, width, height int) string {
	base = fitStyledRect(base, width, height)
	overlayWidth := minInt(width, maxInt(1, widestStyledLine(overlay)))
	overlayHeight := minInt(height, maxInt(1, lipgloss.Height(overlay)))
	overlay = fitStyledRect(overlay, overlayWidth, overlayHeight)

	// 右边界内侧留 1 列呼吸空间，避免卡片边框贴着外框。
	left := maxInt(0, width-overlayWidth-1)
	top := maxInt(0, (height-overlayHeight)/2)

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	for row, overlayLine := range overlayLines {
		baseRow := top + row
		if baseRow < 0 || baseRow >= len(baseLines) {
			continue
		}
		baseLines[baseRow] = composeStyledCellOverlay(baseLines[baseRow], overlayLine, left, width)
	}
	return strings.Join(baseLines, "\n")
}
