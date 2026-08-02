package bubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m appModel) renderActivityBox() string {
	if m.subagentPicker == nil {
		return ""
	}
	layout := m.currentLayout()
	contentWidth := maxInt(1, minInt(72, layout.contentWidth-8))
	contentHeight := maxInt(1, layout.transcriptHeight-6)

	subagentsTab := " Subagents "
	pipelineTab := " Pipeline "
	if m.subagentPicker.tab == activityTabSubagents {
		subagentsTab = selectedProviderStyle.Render(subagentsTab)
		pipelineTab = unselectedProviderStyle.Render(pipelineTab)
	} else {
		subagentsTab = unselectedProviderStyle.Render(subagentsTab)
		pipelineTab = selectedProviderStyle.Render(pipelineTab)
	}

	lines := []string{
		wizardTitleStyle.Render("Activity"),
		subagentsTab + " " + pipelineTab,
	}
	switch m.subagentPicker.tab {
	case activityTabPipeline:
		if !m.pipelineState.detected {
			lines = append(lines, "", unselectedProviderStyle.Render("No pipeline detected."))
		} else {
			lines = append(lines, "", m.renderPipelineContent(contentWidth, maxInt(1, contentHeight-len(lines))))
		}
	default:
		lines = append(lines, "", m.renderActivitySubagents(contentWidth, maxInt(1, contentHeight-len(lines))))
	}
	lines = append(lines, "", unselectedProviderStyle.Render("Tab/←/→ switch  ↑/↓ select  Enter open  Esc close"))
	return m.renderModalPanel(strings.Join(lines, "\n"))
}

func (m appModel) renderActivitySubagents(width, height int) string {
	if m.subagents == nil {
		return labelErrorStyle.Render("Subagent controller is unavailable.")
	}
	if len(m.subagentTasks()) == 0 {
		return unselectedProviderStyle.Render("No subagent tasks.")
	}
	return lipgloss.NewStyle().
		Width(maxInt(1, width)).
		MaxWidth(maxInt(1, width)).
		MaxHeight(maxInt(1, height)).
		Render(m.renderSubagentsCardContentHeight(width, height))
}
