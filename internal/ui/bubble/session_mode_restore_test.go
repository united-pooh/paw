package bubble

import (
	"context"
	"strings"
	"testing"
	"time"

	"paw/internal/loop"
)

type restoredModeRunner struct {
	fakeRunner
	modes *loop.SessionModeSnapshot
	calls int
}

func (r *restoredModeRunner) SessionModes(context.Context, string) (*loop.SessionModeSnapshot, error) {
	r.calls++
	copy := *r.modes
	return &copy, nil
}

type reboundGoalController struct {
	fakeGoalController
	rebound []string
}

func (c *reboundGoalController) Rebind(sessionID string) error {
	c.rebound = append(c.rebound, sessionID)
	return nil
}

type reboundPlanController struct {
	modeTestPlanController
	rebound []string
}

func (c *reboundPlanController) Rebind(sessionID string) error {
	c.rebound = append(c.rebound, sessionID)
	return nil
}

func TestSessionResumeRebindsModesWithoutAutoExecution(t *testing.T) {
	runner := &restoredModeRunner{modes: &loop.SessionModeSnapshot{
		ActiveGoalID: "goal-b", GoalStatus: "paused",
		ActivePlanID: "plan-b", PlanStatus: "paused",
		PendingPermissionID: "permission-b",
	}}
	goalController := &reboundGoalController{}
	planController := &reboundPlanController{}
	model := newTestModel(runner)
	model.goalController = goalController
	model.planController = planController
	model.goalMode, model.planMode = true, true
	model.goalWorking, model.planWorking = true, true
	model.turnID = "old-turn"
	model.turnStartedAt = time.Now()

	next, _ := model.Update(sessionRestoredMsg{
		sessionID: "session-b",
		modes: &loop.SessionModeSnapshot{
			ActiveGoalID: "goal-b", GoalStatus: "running",
			ActivePlanID: "plan-b", PlanStatus: "drafting",
		},
	})
	model = next.(appModel)
	if len(goalController.rebound) != 1 || goalController.rebound[0] != "session-b" {
		t.Fatalf("goal rebinds = %#v", goalController.rebound)
	}
	if len(planController.rebound) != 1 || planController.rebound[0] != "session-b" {
		t.Fatalf("plan rebinds = %#v", planController.rebound)
	}
	if goalController.resumes != 0 || planController.resumes != 0 {
		t.Fatalf("resume auto-executed: goal=%d plan=%d", goalController.resumes, planController.resumes)
	}
	if model.goalMode || model.planMode || model.goalWorking || model.planWorking || model.turnID != "" || !model.turnStartedAt.IsZero() {
		t.Fatalf("restored work state leaked: goalMode=%v planMode=%v goalWorking=%v planWorking=%v turn=%q", model.goalMode, model.planMode, model.goalWorking, model.planWorking, model.turnID)
	}
	if runner.calls != 1 {
		t.Fatalf("mode refresh calls = %d, want 1", runner.calls)
	}
	var guidance string
	for _, entry := range model.transcript {
		if entry.title == "resume" {
			guidance = entry.body
		}
	}
	for _, want := range []string{
		"Goal goal-b 已恢复为 paused；使用 /goal resume 继续。",
		"Plan plan-b 已恢复为 paused；使用 /plan resume 继续。",
		"存在待处理的 Read 权限请求",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("guidance = %q, want %q", guidance, want)
		}
	}
}
