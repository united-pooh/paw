package bubble

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/plan"
)

type workflowGoalController struct {
	fakeGoalController
	submitted []string
	statuses  int
}

func (c *workflowGoalController) Submit(input string) (string, error) {
	c.submitted = append(c.submitted, input)
	return "goal-current", c.err
}

func (c *workflowGoalController) Status() string {
	c.statuses++
	return c.status
}

type workflowPlanController struct {
	modeTestPlanController
	submitted []string
	pauses    int
	executes  int
	doc       plan.PlanDoc
	err       error
}

func (c *workflowPlanController) Submit(input string) (string, error) {
	c.submitted = append(c.submitted, input)
	return "plan-current", c.err
}

func (c *workflowPlanController) Pause() error {
	c.pauses++
	return c.err
}

func (c *workflowPlanController) Execute() (plan.PlanDoc, error) {
	c.executes++
	return c.doc, c.err
}

func TestModeLifecycleCommandsUseSubmitRoute(t *testing.T) {
	for _, input := range []string{"/goal status", "/goal pause", "/plan stop"} {
		t.Run(input, func(t *testing.T) {
			g, p := &workflowGoalController{}, &workflowPlanController{}
			m := newTestModel(&fakeRunner{})
			m.goalController, m.planController = g, p
			m.goalMode = strings.HasPrefix(input, "/goal")
			m.planMode = !m.goalMode
			m.goalWorking, m.planWorking = m.goalMode, m.planMode
			m.input.SetValue(input)
			next, _ := m.handleSubmit()
			m = next.(appModel)
			if len(g.started)+len(g.submitted)+len(p.submitted) > 0 || p.started != "" {
				t.Fatalf("command reached objective submission: goal=%q plan=%q", g.started, p.started)
			}
			if input == "/goal status" && g.statuses != 1 || input == "/goal pause" && g.pauses != 1 || input == "/plan stop" && p.pauses != 1 {
				t.Fatalf("command did not reach lifecycle control: status=%d goal pause=%d plan pause=%d", g.statuses, g.pauses, p.pauses)
			}
		})
	}
}

func TestModeSupplementContinuesExistingWorkflow(t *testing.T) {
	for _, mode := range []string{"goal-running", "goal-paused", "plan-running", "plan-paused", "chat-with-plan", "chat-with-goal"} {
		t.Run(mode, func(t *testing.T) {
			g, p := &workflowGoalController{}, &workflowPlanController{}
			m := newTestModel(&fakeRunner{})
			m.goalController, m.planController = g, p
			m.goalMode = strings.HasPrefix(mode, "goal")
			m.planMode = strings.HasPrefix(mode, "plan")
			m.goalWorking = mode == "goal-running" || mode == "chat-with-goal"
			m.planWorking = mode == "plan-running" || mode == "chat-with-plan"
			wasWorking := m.goalWorking || m.planWorking
			started := time.Unix(1000, 0)
			if wasWorking {
				m.turnStartedAt = started
			}
			m.input.SetValue("补充：保留已有接口")
			next, _ := m.handleSubmit()
			m = next.(appModel)
			if len(g.started) != 0 || p.started != "" || len(g.submitted)+len(p.submitted) != 1 {
				t.Fatalf("supplement was not submitted to current workflow: goal start=%q submit=%q, plan start=%q submit=%q", g.started, g.submitted, p.started, p.submitted)
			}
			if wasWorking && !m.turnStartedAt.Equal(started) {
				t.Fatal("steering restarted task timing")
			}
			if m.input.Value() != "" || !m.input.Focused() {
				t.Fatal("accepted supplement changed input focus or retained draft")
			}
		})
	}
}

func TestModeRejectedSupplementPreservesDraft(t *testing.T) {
	for _, mode := range []string{"goal", "plan"} {
		t.Run(mode, func(t *testing.T) {
			g, p := &workflowGoalController{}, &workflowPlanController{}
			g.err, p.err = errors.New("temporarily unavailable"), errors.New("temporarily unavailable")
			m := newTestModel(&fakeRunner{})
			m.goalController, m.planController = g, p
			m.goalMode, m.planMode = mode == "goal", mode == "plan"
			m.input.SetValue("补充：阅读 @README.md")
			m.inputTokens = []inputToken{{Kind: inputTokenFile, Start: 6, End: 16, Label: "README.md"}}
			want := m.currentInputDraft()
			next, _ := m.handleSubmit()
			m = next.(appModel)
			if m.input.Value() != want.Text || len(m.inputTokens) != len(want.Tokens) || !m.input.Focused() {
				t.Fatalf("rejection lost draft: got=%#v want=%#v", m.currentInputDraft(), want)
			}
		})
	}
}

func TestModePausedEventsKeepConversation(t *testing.T) {
	for _, fixture := range []struct {
		name string
		msg  tea.Msg
		plan bool
	}{
		{"goal-paused", goalStoppedMsg{reason: "goal.paused"}, false},
		{"goal-blocked", goalStoppedMsg{reason: "goal.blocked"}, false},
		{"plan-paused", planStoppedMsg{reason: "paused"}, true},
		{"plan-waiting", planStoppedMsg{reason: "awaiting_input"}, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.goalMode, m.planMode = !fixture.plan, fixture.plan
			m.goalWorking, m.planWorking = !fixture.plan, fixture.plan
			m.input.SetValue("保留补充内容")
			next, _ := m.Update(fixture.msg)
			m = next.(appModel)
			if m.planMode != fixture.plan || m.goalMode == fixture.plan || m.goalWorking || m.planWorking {
				t.Fatalf("paused mode was lost: goal=%v plan=%v working=%v/%v", m.goalMode, m.planMode, m.goalWorking, m.planWorking)
			}
			if m.input.Value() != "保留补充内容" || strings.Contains(m.transcript[len(m.transcript)-1].body, "已结束") {
				t.Fatal("paused conversation was presented as ended or lost its draft")
			}
		})
	}
}

func TestPlanReadyRequiresExplicitExecuteCommand(t *testing.T) {
	runner := &fakeRunner{}
	m := newTestModel(runner)
	p := &workflowPlanController{doc: plan.PlanDoc{Path: "/tmp/approved-plan.md"}}
	m.planController = p
	m.planMode, m.planWorking = true, true
	next, cmd := m.Update(planFinalizedMsg{path: p.doc.Path})
	m = next.(appModel)
	if cmd != nil || m.isModelWorkRunning() || !m.planMode || m.planWorking || p.executes != 0 {
		t.Fatal("ready plan automatically started execution or left planning")
	}
	m.input.SetValue("/plan execute")
	next, cmd = m.handleSubmit()
	m = next.(appModel)
	if p.executes != 1 || m.planMode || !m.isModelWorkRunning() || cmd == nil {
		t.Fatalf("explicit execution did not start Chat: executes=%d mode=%v working=%v", p.executes, m.planMode, m.isModelWorkRunning())
	}
}

func TestWaitingModeNotificationRestoresExclusiveInputMode(t *testing.T) {
	for _, isPlan := range []bool{false, true} {
		m := newTestModel(&fakeRunner{})
		m.goalWorking, m.planWorking = !isPlan, isPlan
		m.goalMode, m.planMode = isPlan, !isPlan
		var msg tea.Msg = goalStoppedMsg{reason: "goal.paused"}
		if isPlan {
			msg = planStoppedMsg{reason: "awaiting_input"}
		}
		next, _ := m.Update(msg)
		m = next.(appModel)
		if m.goalMode != !isPlan || m.planMode != isPlan {
			t.Fatalf("waiting mode leaked prior Tab selection: goal=%v plan=%v", m.goalMode, m.planMode)
		}
	}
}

func TestPlanExecuteFailurePreservesCommandDraft(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.planController = &workflowPlanController{err: errors.New("plan is not ready")}
	m.planMode = true
	m.input.SetValue("/plan execute")
	next, _ := m.handleSubmit()
	m = next.(appModel)
	if m.input.Value() != "/plan execute" || !m.planMode || m.isModelWorkRunning() {
		t.Fatal("rejected execution lost the draft or changed mode")
	}
}

func TestPlanReadyRestoreGuidanceOffersExplicitExecute(t *testing.T) {
	guidance := restoredModeGuidance(&loop.SessionModeSnapshot{ActivePlanID: "plan-ready", PlanStatus: "ready"})
	for _, want := range []string{"/plan execute", "/plan resume", "补充"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("ready restore guidance = %q, missing %q", guidance, want)
		}
	}
}

func TestPlanExecuteWhileTerminalRunningPreservesReadyPlan(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	p := &workflowPlanController{doc: plan.PlanDoc{Path: "/tmp/ready-plan.md"}}
	m.planController = p
	m.queryGuard.StartTerminal()
	m.syncRunningFlags()
	m.input.SetValue("/plan execute")
	next, _ := m.handleSubmit()
	m = next.(appModel)
	if p.executes != 0 || m.input.Value() != "/plan execute" || !m.isTerminalWorkRunning() {
		t.Fatalf("busy execution consumed ready plan: executes=%d draft=%q terminal=%v", p.executes, m.input.Value(), m.isTerminalWorkRunning())
	}
}

func TestRunningModeRejectsSessionSwitchWithoutLosingDraft(t *testing.T) {
	for _, mode := range []string{"goal", "plan", "chat-with-goal", "chat-with-plan"} {
		for _, command := range []string{"/resume", "/new", "/clear", "/fork", "/compact", "/model", "/config", "/setting"} {
			t.Run(mode+command, func(t *testing.T) {
				m := newTestModel(&fakeRunner{})
				m.goalMode, m.goalWorking = mode == "goal", strings.HasSuffix(mode, "goal")
				m.planMode, m.planWorking = mode == "plan", strings.HasSuffix(mode, "plan")
				m.input.SetValue(command)
				next, cmd := m.handleSubmit()
				m = next.(appModel)
				if cmd != nil || m.sessionPicker != nil || m.input.Value() != command {
					t.Fatalf("running mode allowed session mutation: cmd=%v picker=%v draft=%q", cmd != nil, m.sessionPicker != nil, m.input.Value())
				}
				if len(m.transcript) == 0 || m.transcript[len(m.transcript)-1].title != "busy" {
					t.Fatal("running workflow did not reject incompatible command as busy")
				}
			})
		}
	}
}

func TestModeStopRequestKeepsSessionGuardUntilStopped(t *testing.T) {
	for _, fixture := range []struct {
		command string
		msg     tea.Msg
	}{
		{"/goal pause", goalStoppedMsg{reason: "goal.paused"}},
		{"/goal stop", goalStoppedMsg{reason: "goal.cancelled"}},
		{"/goal cancel", goalStoppedMsg{reason: "goal.cancelled"}},
		{"/plan stop", planStoppedMsg{reason: "paused"}},
		{"/plan cancel", planStoppedMsg{reason: "cancelled"}},
	} {
		t.Run(fixture.command, func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.goalController, m.planController = &workflowGoalController{}, &workflowPlanController{}
			isGoal := strings.HasPrefix(fixture.command, "/goal")
			m.goalMode, m.goalWorking = isGoal, isGoal
			m.planMode, m.planWorking = !isGoal, !isGoal
			started := time.Unix(1000, 0)
			m.turnStartedAt, m.turnID = started, "closing-turn"
			m.input.SetValue(fixture.command)
			next, _ := m.handleSubmit()
			m = next.(appModel)
			if m.goalWorking != isGoal || m.planWorking == isGoal || !m.turnStartedAt.Equal(started) || m.turnID != "closing-turn" {
				t.Fatal("stop request released working state before execution exited")
			}
			body := m.transcript[len(m.transcript)-1].body
			if !strings.Contains(body, "正在暂停") && !strings.Contains(body, "正在取消") {
				t.Fatalf("stop request claimed completion: %q", body)
			}
			for _, command := range []string{"/new", "/resume"} {
				m.input.SetValue(command)
				next, cmd := m.handleSubmit()
				m = next.(appModel)
				if cmd != nil || m.sessionPicker != nil || m.input.Value() != command || m.transcript[len(m.transcript)-1].title != "busy" {
					t.Fatalf("%s bypassed closing workflow guard", command)
				}
			}
			next, _ = m.Update(fixture.msg)
			m = next.(appModel)
			if m.goalWorking || m.planWorking || !m.turnStartedAt.IsZero() || m.turnID != "" {
				t.Fatal("stopped event did not release execution state")
			}
			m.input.SetValue("/new")
			next, cmd := m.handleSubmit()
			m = next.(appModel)
			if cmd == nil || m.input.Value() != "" {
				t.Fatal("stopped event did not permit session switch")
			}
		})
	}
}
