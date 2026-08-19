package loop

import "testing"

func TestGoalTurnExecutorNilRunnerIsSafe(t *testing.T) {
	var runner *Engine
	if got := runner.GoalTurnExecutor(); got != nil {
		t.Fatal("nil runner should not expose a goal executor")
	}
}
