package goal

import "time"

type Policy struct {
	Budget    GoalBudget
	RiskPause bool
}

func DefaultPolicy() Policy {
	return Policy{Budget: GoalBudget{MaxTurns: 24, MaxContinuations: 12, MaxNoProgress: 2}, RiskPause: true}
}

func (p Policy) Normalize() Policy {
	p.Budget = p.Budget.Normalize()
	if p.Budget.MaxContinuations == 0 {
		p.Budget.MaxContinuations = 12
	}
	if p.Budget.MaxNoProgress == 0 {
		p.Budget.MaxNoProgress = 2
	}
	return p
}

func (p Policy) DeadlineExceeded(now time.Time) bool {
	return !p.Budget.Deadline.IsZero() && !now.Before(p.Budget.Deadline)
}
