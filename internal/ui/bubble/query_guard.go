package bubble

// QueryGuard centralizes submit lifecycle state for model and shell work.
type QueryGuard struct {
	state queryGuardState
}

type queryGuardState int

const (
	queryGuardIdle queryGuardState = iota
	queryGuardModelRunning
	queryGuardTerminalRunning
	queryGuardCanceled
)

// StartModel marks a model turn as running when the guard is idle.
func (g *QueryGuard) StartModel() bool {
	if g == nil || g.state != queryGuardIdle {
		return false
	}
	g.state = queryGuardModelRunning
	return true
}

// StartTerminal marks a shell command as running when the guard is idle.
func (g *QueryGuard) StartTerminal() bool {
	if g == nil || g.state != queryGuardIdle {
		return false
	}
	g.state = queryGuardTerminalRunning
	return true
}

// FinishModel releases a model turn.
func (g *QueryGuard) FinishModel() {
	if g == nil || g.state == queryGuardCanceled {
		return
	}
	if g.state == queryGuardModelRunning {
		g.state = queryGuardIdle
	}
}

// FinishTerminal releases a shell command.
func (g *QueryGuard) FinishTerminal() {
	if g == nil || g.state == queryGuardCanceled {
		return
	}
	if g.state == queryGuardTerminalRunning {
		g.state = queryGuardIdle
	}
}

// Cancel prevents future queued work from starting.
func (g *QueryGuard) Cancel() {
	if g == nil {
		return
	}
	g.state = queryGuardCanceled
}

// CanStartQueued reports whether queued model work may start now.
func (g *QueryGuard) CanStartQueued() bool {
	return g != nil && g.state == queryGuardIdle
}

// IsModelRunning reports whether a model turn is active.
func (g *QueryGuard) IsModelRunning() bool {
	return g != nil && g.state == queryGuardModelRunning
}

// IsTerminalRunning reports whether a shell command is active.
func (g *QueryGuard) IsTerminalRunning() bool {
	return g != nil && g.state == queryGuardTerminalRunning
}

// IsRunning reports whether any foreground work is active.
func (g *QueryGuard) IsRunning() bool {
	return g.IsModelRunning() || g.IsTerminalRunning()
}

// IsCanceled reports whether the program is exiting or canceled.
func (g *QueryGuard) IsCanceled() bool {
	return g != nil && g.state == queryGuardCanceled
}
