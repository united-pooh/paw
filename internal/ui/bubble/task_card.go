// 本文件定义 transcript 右侧垂直居中的运行中 subagent 任务卡。
// 卡片只列出 running 任务；任务进入终态后即从卡片移除（持久记录由
// <task> 完成块保留在 transcript 流中）。样式与颜色跟随当前主题。
package bubble

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/subagent"
)

// subagentSpinnerFrames 是任务卡 spinner 的旋转帧，由动画帧驱动换帧。
var subagentSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

// subagentTaskCardMaxWidth 是任务卡的显示宽度上限（含边框）。
const subagentTaskCardMaxWidth = 32

// runningSubagentTasks 返回当前所有 running 任务；nil controller 或空列表返回 nil。
func (m appModel) runningSubagentTasks() []subagent.TaskSnapshot {
	if m.subagents == nil {
		return nil
	}
	tasks := m.subagents.ListTasks()
	running := make([]subagent.TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == subagent.TaskRunning {
			running = append(running, task)
		}
	}
	return running
}

// hasRunningSubagentTasks 报告是否存在运行中的 subagent 任务。
// 供动画帧驱动判断是否需要持续重绘（spinner 与任务状态变化）。
func (m appModel) hasRunningSubagentTasks() bool {
	if m.subagents == nil {
		return false
	}
	for _, task := range m.subagents.ListTasks() {
		if task.Status == subagent.TaskRunning {
			return true
		}
	}
	return false
}

// renderSubagentTaskCard 渲染运行中任务卡；没有 running 任务时返回空串。
//
//	┌ subagents · 2 运行中 ──┐
//	│ ◐ 二叶筑   a6c81e94    │
//	│ ◐ 深潜者   b2d9f0aa    │
//	└───────────────────────┘
func (m appModel) renderSubagentTaskCard(now time.Time) string {
	running := m.runningSubagentTasks()
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
		Render("subagents · " + itoa(len(running)) + " 运行中")

	lines := make([]string, 0, len(running)+1)
	lines = append(lines, title)
	spinner := subagentSpinnerFrames[spinnerFrameIndex(now)]
	for _, task := range running {
		lines = append(lines, renderSubagentTaskCardRow(task, spinner))
	}

	// lipgloss 的 Width 含 padding 但不含 border：styleWidth 只减边框，
	// body 再 fit 到减去 padding 后的宽度，最终总宽精确等于卡片上限。
	horizontalBorder := cardStyle.GetHorizontalBorderSize()
	styleWidth := maxInt(1, subagentTaskCardMaxWidth-horizontalBorder)
	bodyWidth := maxInt(1, styleWidth-cardStyle.GetHorizontalPadding())
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, fitStyledCellLine(truncateStyledCellLine(line, bodyWidth), bodyWidth))
	}
	return cardStyle.Width(styleWidth).Render(strings.Join(content, "\n"))
}

// renderSubagentTaskCardRow 渲染单行任务：spinner + 名称（persona 色）+ 短 id。
func renderSubagentTaskCardRow(task subagent.TaskSnapshot, spinner string) string {
	nameStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorBody))
	if color := strings.TrimSpace(task.Color); color != "" {
		nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = shortTaskID(task.ID)
	}
	id := shortTaskID(task.ID)
	idStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))

	return spinnerStyle.Render(spinner) +
		" " +
		nameStyle.Render(name) +
		"  " +
		idStyle.Render(id)
}

// spinnerStyle 是任务卡 spinner 的颜色（与 mockup 的暖色一致）。
var spinnerStyle = lipgloss.NewStyle().
	Foreground(colorManager.LipglossColor(colorWorktreeDirty))

// spinnerFrameIndex 按时间取 spinner 帧索引；now 为零值时返回 0（静态帧）。
func spinnerFrameIndex(now time.Time) int {
	if now.IsZero() {
		return 0
	}
	return int(now.UnixMilli()/250) % len(subagentSpinnerFrames)
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
