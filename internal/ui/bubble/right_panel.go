// 本文件定义右侧 30% 面板的三个卡片渲染逻辑。
package bubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gocode/internal/subagent"
)

// renderRightPanel 渲染右侧面板：Pipeline/Tasks 卡片 + Subagents 卡片 + Context 卡片。
// 整体高度被钳制为 totalHeight，防止右侧面板撑高终端布局。
func (m appModel) renderRightPanel(width, totalHeight int) string {
	inner := maxInt(4, width-4)

	subagentsContent := m.renderSubagentsCardContent(inner)
	subagentsCard := rightCardStyle.Width(inner).Render(subagentsContent)
	subH := lipgloss.Height(subagentsCard)

	contextContent := m.renderContextCardContent(inner)
	contextCard := rightCardStyle.Width(inner).Render(contextContent)
	ctxH := lipgloss.Height(contextCard)

	pipelineH := maxInt(6, totalHeight-subH-ctxH)
	pipelineContent := m.renderPipelineOrTasksContent(inner, pipelineH-4)
	pipelineCard := rightCardStyle.Width(inner).Height(pipelineH - 4).Render(pipelineContent)

	joined := lipgloss.JoinVertical(lipgloss.Left,
		pipelineCard,
		subagentsCard,
		contextCard,
	)
	// Clamp the right panel to totalHeight so it never exceeds the terminal height.
	return lipgloss.NewStyle().
		Width(width).
		Height(totalHeight).
		MaxHeight(totalHeight).
		Render(joined)
}

// renderContextCard 返回 Context 卡片（用于测试）。
func (m appModel) renderContextCard(width int) string {
	inner := maxInt(4, width-4)
	return rightCardStyle.Width(inner).Render(m.renderContextCardContent(inner))
}

// renderSubagentsCardContent 渲染 Subagents 内容（Task 4 实现）。
func (m appModel) renderSubagentsCardContent(width int) string {
	hdrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("237")).Bold(false)
	hdr := hdrStyle.Render("subagents")

	if m.subagents == nil {
		return hdr
	}
	tasks := m.subagents.ListTasks()
	if len(tasks) == 0 {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Italic(true).Render("none")
		return hdr + "\n" + empty
	}

	dotRun  := lipgloss.NewStyle().Foreground(lipgloss.Color("84")).Render("⟳")
	dotDone := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render("✓")
	dotFail := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("✗")

	lines := []string{hdr}
	for _, t := range tasks {
		var dot, label string
		switch t.Status {
		case subagent.TaskRunning:
			dot = dotRun
			label = lipgloss.NewStyle().Foreground(lipgloss.Color("84")).Render(t.ID)
		case subagent.TaskFailed:
			dot = dotFail
			label = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(t.ID)
		default:
			dot = dotDone
			label = lipgloss.NewStyle().Foreground(lipgloss.Color("236")).Render(t.ID)
		}
		lines = append(lines, dot+" "+label)
	}
	_ = width
	return strings.Join(lines, "\n")
}

// renderPipelineOrTasksContent 渲染 Pipeline/Tasks 内容（Task 5-7 实现）。
func (m appModel) renderPipelineOrTasksContent(_, _ int) string {
	return "pipeline / tasks"
}
