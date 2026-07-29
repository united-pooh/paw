// 本文件定义 Activity modal 使用的 Subagents 与 Pipeline 内容渲染逻辑。
package bubble

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/subagent"
)

const (
	pipelineMaxStageRows = 6
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
func loadPipelineState(workspaceDir string, activeAfter time.Time) pipelineState {
	var s pipelineState
	s.activeIdx = -1
	latestArtifactAt := latestPipelineWorkspaceModTime(workspaceDir)
	if info, err := os.Stat(filepath.Join(filepath.Dir(workspaceDir), ".pipeline-last-run-summary.json")); err == nil {
		latestArtifactAt = maxTime(latestArtifactAt, info.ModTime())
	}

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

	specExists := false
	if _, err := os.Stat(filepath.Join(workspaceDir, "spec.json")); err == nil {
		specExists = true
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
	s.detected = specExists && pipelineWorkspaceIsActive(workspaceDir, latestArtifactAt, activeAfter)

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

func latestPipelineWorkspaceModTime(workspaceDir string) time.Time {
	var latest time.Time
	_ = filepath.Walk(workspaceDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		latest = maxTime(latest, info.ModTime())
		return nil
	})
	return latest
}

func pipelineWorkspaceIsActive(workspaceDir string, latestArtifactAt, activeAfter time.Time) bool {
	if activeAfter.IsZero() {
		return true
	}
	if pipelineActiveMarkerExists(workspaceDir, activeAfter) {
		return true
	}
	return !latestArtifactAt.IsZero() && !latestArtifactAt.Before(activeAfter)
}

func pipelineActiveMarkerExists(workspaceDir string, activeAfter time.Time) bool {
	for _, name := range []string{".active", ".pipeline-active", "active", "running", "pipeline.lock"} {
		info, err := os.Stat(filepath.Join(workspaceDir, name))
		if err == nil && !info.ModTime().Before(activeAfter) {
			return true
		}
	}

	statusPath := filepath.Join(workspaceDir, "status.json")
	info, err := os.Stat(statusPath)
	if err != nil || info.ModTime().Before(activeAfter) {
		return false
	}
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return false
	}
	var status struct {
		Active  bool   `json:"active"`
		Running bool   `json:"running"`
		State   string `json:"state"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(data, &status) != nil {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(status.State))
	statusValue := strings.ToLower(strings.TrimSpace(status.Status))
	return status.Active || status.Running || state == "active" || state == "running" || statusValue == "active" || statusValue == "running"
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// renderSubagentsCardContent 渲染 Subagents 内容（Task 4 实现）。
func (m appModel) renderSubagentsCardContent(width int) string {
	return m.renderSubagentsCardContentHeight(width, 0)
}

func (m appModel) renderSubagentsCardContentHeight(width, height int) string {
	hdrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	hdr := renderSidebarRow(width, "subagents", "", hdrStyle, mutedStyle)
	maxLines := height

	if m.subagents == nil {
		if maxLines == 1 {
			return hdr
		}
		return hdr + "\n" + mutedStyle.Italic(true).Render(padDisplayWidth("none", width))
	}
	tasks := m.subagentTasks()
	if len(tasks) == 0 {
		if maxLines == 1 {
			return hdr
		}
		empty := mutedStyle.Italic(true).Render(padDisplayWidth("none", width))
		return hdr + "\n" + empty
	}

	dotRunStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
	dotDoneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dotFailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	dotStopStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	availableTaskRows := len(tasks)
	windowStart := 0
	moreCount := 0
	if maxLines > 0 {
		availableTaskRows = maxInt(0, maxLines-1)
		if len(tasks) > availableTaskRows {
			if availableTaskRows <= 1 {
				moreCount = len(tasks)
				availableTaskRows = 0
			} else {
				availableTaskRows--
				moreCount = len(tasks) - availableTaskRows
			}
		}
	}
	if m.subagentPicker != nil && availableTaskRows > 0 {
		selected := clampInt(m.subagentPicker.selectedIndex, 0, len(tasks)-1)
		if selected < windowStart {
			windowStart = selected
		}
		if selected >= windowStart+availableTaskRows {
			windowStart = selected - availableTaskRows + 1
		}
		maxStart := maxInt(0, len(tasks)-availableTaskRows)
		if windowStart > maxStart {
			windowStart = maxStart
		}
		if windowStart > 0 || windowStart+availableTaskRows < len(tasks) {
			moreCount = windowStart + maxInt(0, len(tasks)-(windowStart+availableTaskRows))
		}
	}

	lines := []string{hdr}
	windowEnd := minInt(len(tasks), windowStart+availableTaskRows)
	for idx := windowStart; idx < windowEnd; idx++ {
		t := tasks[idx]
		var dot, status string
		dotStyle := dotDoneStyle
		lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
		switch t.Status {
		case subagent.TaskRunning:
			dot = spinnerFrames[m.spinnerFrameIdx%len(spinnerFrames)]
			status = formatElapsedTime(time.Since(t.StartedAt))
			dotStyle = dotRunStyle
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
		case subagent.TaskFailed:
			dot = "✗"
			status = "failed"
			dotStyle = dotFailStyle
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
		case subagent.TaskStopped:
			dot = "■"
			status = "stopped"
			dotStyle = dotStopStyle
			lineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		default:
			dot = "✓"
			status = "done"
		}
		nameStyle := lineStyle
		if color := strings.TrimSpace(t.Color); color != "" {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		}
		if m.subagentPicker != nil && idx == clampInt(m.subagentPicker.selectedIndex, 0, len(tasks)-1) {
			dot = ">"
			dotStyle = selectedProviderStyle
			nameStyle = selectedProviderStyle
			lineStyle = selectedProviderStyle
		}
		lines = append(lines, renderSubagentSidebarRow(width, dot, taskDisplayName(t), status, dotStyle, nameStyle, lineStyle))
	}
	if moreCount > 0 {
		lines = append(lines, renderSidebarRow(width, fmt.Sprintf("+%d more", moreCount), "", mutedStyle, mutedStyle))
	}
	return strings.Join(lines, "\n")
}

// renderPipelineOrTasksContent 保留给测试和旧调用点：无 pipeline 时返回空内容。
func (m appModel) renderPipelineOrTasksContent(width, height int) string {
	if m.pipelineState.detected {
		return m.renderPipelineContent(width, height)
	}
	return ""
}

func (m appModel) renderPipelineContent(width, height int) string {
	return m.renderPipelineWindowedContent(width, height)
}

// renderPipelineWindowedContent 渲染满宽 Pipeline 仪表盘：摘要、阶段进度条和 timeline。
func (m appModel) renderPipelineWindowedContent(width, height int) string {
	ps := m.pipelineState

	currentIdx := ps.activeIdx
	if currentIdx < 0 {
		currentIdx = maxInt(0, ps.doneCount-1)
	}
	currentName := ""
	if currentIdx >= 0 && currentIdx < len(ps.phases) {
		currentName = ps.phases[currentIdx].name
	}
	if currentName == "" {
		currentName = "idle"
	}

	titleRight := currentName
	if ps.globalIter > 0 {
		titleRight = fmt.Sprintf("%s ×%d", titleRight, ps.globalIter)
	}

	pendingCount := 0
	retryCount := 0
	activeCount := 0
	for _, ph := range ps.phases {
		switch ph.status {
		case phaseStatusActive:
			activeCount++
		case phaseStatusRetry:
			retryCount++
		case phaseStatusPending:
			pendingCount++
		}
	}

	total := len(ps.phases)
	percent := 0
	if total > 0 {
		percent = int(float64(ps.doneCount) / float64(total) * 100)
	}

	lines := []string{
		renderSidebarRow(width, "pipeline", titleRight, lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true), lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)),
		renderSidebarRow(width, fmt.Sprintf("%d/%d complete", ps.doneCount, total), fmt.Sprintf("%d%%", percent), lipgloss.NewStyle().Foreground(lipgloss.Color("246")), lipgloss.NewStyle().Foreground(lipgloss.Color("246"))),
		renderPipelineProgressBar(ps, width),
		renderSidebarRow(width, fmt.Sprintf("ok %d", ps.doneCount), fmt.Sprintf("now %d", activeCount), lipgloss.NewStyle().Foreground(lipgloss.Color("28")), lipgloss.NewStyle().Foreground(lipgloss.Color("75"))),
		renderSidebarRow(width, fmt.Sprintf("todo %d", pendingCount), fmt.Sprintf("retry %d", retryCount), lipgloss.NewStyle().Foreground(lipgloss.Color("246")), lipgloss.NewStyle().Foreground(lipgloss.Color("214"))),
		renderSidebarRow(width, "timeline", "near", lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true), lipgloss.NewStyle().Foreground(lipgloss.Color("246"))),
	}

	stageRows := minInt(pipelineMaxStageRows, maxInt(0, height-len(lines)))
	for _, idx := range visiblePipelineStageIndices(ps, stageRows) {
		lines = append(lines, renderPipelineStageLine(ps.phases[idx], idx, currentIdx, width))
	}
	return strings.Join(lines, "\n")
}

func renderPipelineProgressBar(ps pipelineState, width int) string {
	width = maxInt(1, width)
	total := len(ps.phases)
	if total == 0 {
		return strings.Repeat("▱", width)
	}
	parts := make([]string, 0, width)
	for cell := 0; cell < width; cell++ {
		idx := minInt(total-1, cell*total/width)
		status := ps.phases[idx].status
		if status == phaseStatusActive && (cell == 0 || minInt(total-1, (cell-1)*total/width) != idx) {
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true).Render("▶"))
			continue
		}
		switch status {
		case phaseStatusDone:
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("28")).Render("▰"))
		case phaseStatusActive:
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Render("▰"))
		case phaseStatusRetry:
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("▱"))
		default:
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("▱"))
		}
	}
	return strings.Join(parts, "")
}

func visiblePipelineStageIndices(ps pipelineState, available int) []int {
	total := len(ps.phases)
	if available <= 0 {
		return nil
	}
	if available >= total {
		indices := make([]int, total)
		for i := range indices {
			indices[i] = i
		}
		return indices
	}
	center := ps.activeIdx
	if center < 0 {
		center = maxInt(0, ps.doneCount-1)
	}
	start := center - available/2
	start = maxInt(0, minInt(start, total-available))
	indices := make([]int, available)
	for i := range indices {
		indices[i] = start + i
	}
	return indices
}

func renderPipelineStageLine(ph pipelinePhaseEntry, idx, currentIdx, width int) string {
	symbol, status := pipelineStageGlyphAndStatus(ph.status)
	name := ph.name
	if ph.iteration > 0 {
		name = fmt.Sprintf("%s ×%d", name, ph.iteration)
	}
	left := symbol + " " + name
	if idx == currentIdx {
		contentWidth := maxInt(1, width-1)
		content := renderSidebarRow(contentWidth, left, status, lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true), lipgloss.NewStyle().Foreground(lipgloss.Color("252")))
		return lipgloss.NewStyle().Foreground(lipgloss.Color("68")).Render("▐") +
			lipgloss.NewStyle().Width(contentWidth).Background(lipgloss.Color("234")).Render(content)
	}
	var fg lipgloss.Color
	switch ph.status {
	case phaseStatusDone:
		fg = lipgloss.Color("248")
	case phaseStatusRetry:
		fg = lipgloss.Color("214")
	case phaseStatusActive:
		fg = lipgloss.Color("252")
	default:
		fg = lipgloss.Color("245")
	}
	style := lipgloss.NewStyle().Foreground(fg)
	return renderSidebarRow(width, left, status, style, style)
}

func pipelineStageGlyphAndStatus(status pipelinePhaseStatus) (string, string) {
	switch status {
	case phaseStatusDone:
		return "✓", "done"
	case phaseStatusActive:
		return "▶", "now"
	case phaseStatusRetry:
		return "↻", "retry"
	default:
		return "·", "pending"
	}
}

func renderSidebarRow(width int, left, right string, leftStyle, rightStyle lipgloss.Style) string {
	width = maxInt(1, width)
	if width <= 2 || strings.TrimSpace(right) == "" {
		return leftStyle.Render(padDisplayWidth(truncateDisplayWidth(left, width), width))
	}
	right = truncateDisplayWidth(right, maxInt(1, width/2))
	rightWidth := lipgloss.Width(right)
	leftMax := maxInt(1, width-rightWidth-1)
	left = truncateDisplayWidth(left, leftMax)
	gap := maxInt(0, width-lipgloss.Width(left)-rightWidth)
	return leftStyle.Render(left) + strings.Repeat(" ", gap) + rightStyle.Render(right)
}

func renderSubagentSidebarRow(width int, dot, name, right string, dotStyle, nameStyle, rightStyle lipgloss.Style) string {
	width = maxInt(1, width)
	right = strings.TrimSpace(right)
	if width <= 2 || right == "" {
		return renderSubagentSidebarLeft(width, dot, name, dotStyle, nameStyle)
	}
	right = truncateDisplayWidth(right, maxInt(1, width/2))
	rightWidth := lipgloss.Width(right)
	leftMax := maxInt(1, width-rightWidth-1)
	left := renderSubagentSidebarLeft(leftMax, dot, name, dotStyle, nameStyle)
	gap := maxInt(0, width-lipgloss.Width(left)-rightWidth)
	return left + strings.Repeat(" ", gap) + rightStyle.Render(right)
}

func renderSubagentSidebarLeft(width int, dot, name string, dotStyle, nameStyle lipgloss.Style) string {
	width = maxInt(1, width)
	dot = truncateDisplayWidth(dot, width)
	dotWidth := lipgloss.Width(dot)
	if width <= dotWidth+1 {
		return dotStyle.Render(padDisplayWidth(dot, width))
	}
	nameWidth := maxInt(1, width-dotWidth-1)
	name = truncateDisplayWidth(name, nameWidth)
	left := dotStyle.Render(dot) + " " + nameStyle.Render(name)
	if lipgloss.Width(left) >= width {
		return left
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left))
}

func padDisplayWidth(text string, width int) string {
	if lipgloss.Width(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-lipgloss.Width(text))
}

func formatElapsedTime(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}
