package bubble

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
