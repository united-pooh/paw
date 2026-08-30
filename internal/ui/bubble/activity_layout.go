package bubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	activityDefaultPercent = 36
	activityMinWidth       = 32
	activityMaxWidth       = 52
	activityWorkspaceMin   = 52
	activitySeparatorWidth = 1
	activityResizeStep     = 4
	activityDockMinWidth   = activityWorkspaceMin + activitySeparatorWidth + activityMinWidth
)

type activityGeometry struct {
	mode           activityLayoutMode
	workspaceWidth int
	activityWidth  int
	separatorWidth int
}

func computeActivityGeometry(fullWidth int, visible bool, requestedWidth int) activityGeometry {
	fullWidth = maxInt(1, fullWidth)
	if !visible {
		return activityGeometry{mode: activityLayoutHidden, workspaceWidth: fullWidth}
	}
	if fullWidth < activityDockMinWidth {
		return activityGeometry{mode: activityLayoutFullscreen, activityWidth: fullWidth}
	}
	width := requestedWidth
	if width <= 0 {
		width = (fullWidth*activityDefaultPercent + 50) / 100
	}
	maxForWorkspace := fullWidth - activitySeparatorWidth - activityWorkspaceMin
	width = clampInt(width, activityMinWidth, minInt(activityMaxWidth, maxForWorkspace))
	return activityGeometry{
		mode:           activityLayoutDocked,
		workspaceWidth: fullWidth - activitySeparatorWidth - width,
		activityWidth:  width,
		separatorWidth: activitySeparatorWidth,
	}
}

func resizeActivityWidth(width, direction int) int {
	if width <= 0 {
		width = activityMinWidth
	}
	return clampInt(width+direction*activityResizeStep, activityMinWidth, activityMaxWidth)
}

func applyActivityGeometry(layout tuiLayout, visible bool, requestedWidth int) tuiLayout {
	geometry := computeActivityGeometry(layout.contentWidth, visible, requestedWidth)
	layout.workspaceWidth = geometry.workspaceWidth
	layout.activityWidth = geometry.activityWidth
	layout.activitySeparatorWidth = geometry.separatorWidth
	layout.activityMode = geometry.mode
	return layout
}

func joinActivityColumns(left, right string, leftWidth, rightWidth, height int, separator string) string {
	left = fitStyledRect(left, leftWidth, height)
	right = fitStyledRect(right, rightWidth, height)
	separator = fitStyledCellLine(separator, activitySeparatorWidth)
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		lines[row] = leftLines[row] + separator + rightLines[row]
	}
	return strings.Join(lines, "\n")
}

func renderSplitHairline(leftContent, rightContent string, leftWidth, rightWidth int, joint, lineColor string) string {
	jointStyle := lipgloss.NewStyle()
	if lineColor != "" {
		jointStyle = jointStyle.Foreground(lipgloss.Color(lineColor))
	}
	return embedHairlineContent(leftContent, leftWidth, lineColor) +
		jointStyle.Render(joint) +
		embedHairlineContent(rightContent, rightWidth, lineColor)
}

func renderHairlineWithRightHint(mainContent, hint string, width int, lineColor string) string {
	width = maxInt(1, width)
	mainContent = truncateStyledCellLine(mainContent, maxInt(1, width/2))
	hintBudget := maxInt(0, width-terminalCellWidth(mainContent)-3)
	if hintBudget <= 0 || strings.TrimSpace(hint) == "" {
		return embedHairlineContent(mainContent, width, lineColor)
	}
	hint = truncateStyledCellLine(hint, hintBudget)
	lineStyle := lipgloss.NewStyle()
	if lineColor != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(lineColor))
	}
	dash := func(n int) string {
		if n <= 0 {
			return ""
		}
		return lineStyle.Render(strings.Repeat("─", n))
	}
	left := dash(1) + " " + mainContent + " "
	right := " " + hint + " " + dash(1)
	middle := maxInt(0, width-terminalCellWidth(left)-terminalCellWidth(right))
	return fitStyledCellLine(left+dash(middle)+right, width)
}
