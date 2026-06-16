// 本文件定义右侧 30% 面板的三个卡片渲染逻辑。
package bubble

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gocode/internal/subagent"
)

// pipelineArtifacts lists the 18 pipeline phases in order, each with display name and artifact filename.
// Artifact paths are relative to .pipeline-workspace/ (except Cleanup which is in the parent dir).
var pipelineArtifacts = [18][2]string{
	{"Brainstorm", "design.md"},
	{"Spec", "spec.json"},
	{"Plan", "plan.json"},
	{"Arch", "architecture.json"},
	{"Dispatch", "dispatch.json"},
	{"Execution", "execution-report.json"},
	{"Complexity", "execution-report.json"}, // shares artifact; detected via Merge existence
	{"Merge", "merge-report.json"},
	{"Validation", "validation-report.json"},
	{"Tree Class", "tree-classification.json"},
	{"Rubric Gen", "tree-rubrics.json"},
	{"Rubric Vfy", "tree-rubric-verification.json"},
	{"Rubric Rfn", "tree-rubrics-refined.json"},
	{"Grading", "tree-grading-individual.json"},
	{"QA", "qa-report.json"},
	{"Docs", "doc-report.json"},
	{"Assessment", "final-assessment.json"},
	{"Cleanup", ".pipeline-last-run-summary.json"}, // in parent of workspaceDir
}

// loadPipelineState scans workspaceDir and infers the current pipeline phase state.
func loadPipelineState(workspaceDir string) pipelineState {
	var s pipelineState
	s.activeIdx = -1

	// Read global iteration from execution-report.json
	iterFile := filepath.Join(workspaceDir, "execution-report.json")
	if data, err := os.ReadFile(iterFile); err == nil {
		var rep struct {
			Iteration int `json:"iteration"`
		}
		if json.Unmarshal(data, &rep) == nil && rep.Iteration > 0 {
			s.globalIter = rep.Iteration
		}
	}

	// Pipeline is "detected" only if spec.json exists
	if _, err := os.Stat(filepath.Join(workspaceDir, "spec.json")); err == nil {
		s.detected = true
	}

	// Determine each phase's status
	lastDoneIdx := -1
	for i, pa := range pipelineArtifacts {
		artifactPath := filepath.Join(workspaceDir, pa[1])
		if pa[0] == "Cleanup" {
			artifactPath = filepath.Join(filepath.Dir(workspaceDir), pa[1])
		}
		_, err := os.Stat(artifactPath)
		exists := err == nil
		s.phases[i] = pipelinePhaseEntry{
			name:     pa[0],
			artifact: pa[1],
		}
		if exists {
			s.phases[i].status = phaseStatusDone
			s.doneCount++
			lastDoneIdx = i
		}
	}

	// Active phase = first phase after last done
	if s.detected && lastDoneIdx+1 < 18 {
		s.activeIdx = lastDoneIdx + 1
		s.phases[s.activeIdx].status = phaseStatusActive
		s.phases[s.activeIdx].iteration = s.globalIter
	}

	// Mark retry: Execution done + globalIter > 1 + Validation not done
	execDone := s.phases[5].status == phaseStatusDone
	validDone := s.phases[8].status == phaseStatusDone
	if execDone && s.globalIter > 1 && !validDone {
		for i := 5; i <= 8; i++ {
			if s.phases[i].status == phaseStatusPending {
				s.phases[i].status = phaseStatusRetry
			}
		}
	}
	return s
}

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

// renderPipelineOrTasksContent 渲染 Pipeline 或 Tasks 卡片内容。
// 有 pipeline 时显示 pipeline 状态（Task 7 实现），无 pipeline 时显示 tasks 列表。
func (m appModel) renderPipelineOrTasksContent(width, height int) string {
	if m.pipelineState.detected {
		return m.renderPipelineWindowedContent(width, height)
	}
	return m.renderTasksContent(width, height)
}

// renderTasksContent 渲染无 pipeline 时的 Tasks 列表。
func (m appModel) renderTasksContent(width, height int) string {
	badge := lipgloss.NewStyle().
		Foreground(lipgloss.Color("136")).
		Background(lipgloss.Color("234")).
		Padding(0, 1).
		Render("✦ tasks")

	// 当前 Runner 接口未暴露 TaskList，显示空状态
	empty := lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Italic(true).Render("no tasks")
	return badge + "\n" + empty
}

// renderPipelineWindowedContent 渲染 Pipeline 滚动窗口：18 点总览 + 当前阶段 ±3 邻居。
func (m appModel) renderPipelineWindowedContent(width, height int) string {
	ps := m.pipelineState

	// Badge line
	iterStr := ""
	if ps.globalIter > 0 {
		iterStr = fmt.Sprintf(" · iter %d", ps.globalIter)
	}
	badge := lipgloss.NewStyle().
		Foreground(lipgloss.Color("68")).
		Background(lipgloss.Color("234")).
		Padding(0, 1).
		Render("● pipeline" + iterStr)
	countStr := lipgloss.NewStyle().Foreground(lipgloss.Color("237")).
		Render(fmt.Sprintf("%d/18", ps.doneCount))
	gapWidth := maxInt(1, width-lipgloss.Width(badge)-lipgloss.Width(countStr))
	topLine := badge + strings.Repeat(" ", gapWidth) + countStr

	// 18-dot overview
	dots := make([]string, 18)
	for i, ph := range ps.phases {
		switch ph.status {
		case phaseStatusDone:
			dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("28")).Render("●")
		case phaseStatusActive:
			dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true).Render("▶")
		case phaseStatusRetry:
			dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("○")
		default:
			dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Render("·")
		}
	}
	dotsLine := strings.Join(dots, "")

	// Windowed stages: current ±3
	cur := ps.activeIdx
	if cur < 0 {
		cur = maxInt(0, ps.doneCount-1)
	}

	type windowRow struct {
		offset  int     // distance from current (-3 to +3)
		opacity float64 // 1.0 = current, lower = faded
	}
	rows := []windowRow{
		{-3, 0.18}, {-2, 0.35}, {-1, 0.55},
		{0, 1.0},
		{1, 0.55}, {2, 0.35}, {3, 0.18},
	}

	stageLines := make([]string, 0, 7)
	for _, row := range rows {
		idx := cur + row.offset
		if idx < 0 || idx >= 18 {
			stageLines = append(stageLines, "")
			continue
		}
		ph := ps.phases[idx]

		var dotRune string
		switch ph.status {
		case phaseStatusDone:
			dotRune = "●"
		case phaseStatusActive:
			dotRune = "▶"
		case phaseStatusRetry:
			dotRune = "○"
		default:
			dotRune = "·"
		}

		iterSuffix := ""
		if ph.iteration > 0 {
			if ph.status == phaseStatusRetry {
				iterSuffix = fmt.Sprintf(" ×%d ↻", ph.iteration)
			} else if ph.status == phaseStatusActive {
				iterSuffix = fmt.Sprintf(" ×%d", ph.iteration)
			}
		}

		var fg lipgloss.Color
		switch {
		case row.opacity >= 0.9:
			fg = lipgloss.Color("252")
		case row.opacity >= 0.50:
			fg = lipgloss.Color("242")
		case row.opacity >= 0.30:
			fg = lipgloss.Color("238")
		default:
			fg = lipgloss.Color("235")
		}

		lineStyle := lipgloss.NewStyle().Foreground(fg)
		label := dotRune + " " + ph.name + iterSuffix
		if row.offset == 0 {
			lineStyle = lineStyle.
				Background(lipgloss.Color("234")).
				Border(lipgloss.Border{Left: "▐"}).
				BorderForeground(lipgloss.Color("68")).
				PaddingLeft(1)
		}
		stageLines = append(stageLines, lineStyle.Render(label))
	}

	allLines := []string{topLine, dotsLine}
	allLines = append(allLines, stageLines...)
	return strings.Join(allLines, "\n")
}
