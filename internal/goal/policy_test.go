package goal

import "testing"

func TestDefaultPolicyIsFiniteAndRiskPausing(t *testing.T) {
	p := DefaultPolicy()
	if p.Budget.MaxContinuations <= 0 || p.Budget.MaxNoProgress <= 0 || !p.RiskPause {
		t.Fatalf("policy = %+v", p)
	}
}

func TestPolicyNormalizesNegativeLimits(t *testing.T) {
	p := (Policy{Budget: GoalBudget{MaxContinuations: -1, MaxNoProgress: -2}}).Normalize()
	if p.Budget.MaxContinuations != 12 || p.Budget.MaxNoProgress != 2 {
		t.Fatalf("policy = %+v", p)
	}
}
