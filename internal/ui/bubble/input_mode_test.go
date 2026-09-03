package bubble

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type modeTestGoalController struct{}

func (c *modeTestGoalController) Start(string) (string, error) { return "goal-1", nil }
func (c *modeTestGoalController) Status() string               { return "status" }
func (c *modeTestGoalController) Pause() error                 { return nil }
func (c *modeTestGoalController) Resume() error                { return nil }
func (c *modeTestGoalController) Cancel() error                { return nil }
func (c *modeTestGoalController) Budget() string               { return "budget" }

type modeTestPlanController struct {
	started string
	stopped bool
	resumes int
}

func (c *modeTestPlanController) Start(requirement string) (string, error) {
	c.started = requirement
	return "plan-1", nil
}
func (c *modeTestPlanController) Status() string        { return "status" }
func (c *modeTestPlanController) List() string          { return "list" }
func (c *modeTestPlanController) Show(id string) string { return "show " + id }
func (c *modeTestPlanController) Resume() error         { c.resumes++; return nil }
func (c *modeTestPlanController) Cancel() error         { c.stopped = true; return nil }

func TestCycleInputModeChatGoalPlan(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	if model.goalMode || model.planMode {
		t.Fatalf("initial mode = goal:%v plan:%v, want chat", model.goalMode, model.planMode)
	}
	model.cycleInputMode()
	if !model.goalMode || model.planMode {
		t.Fatalf("after first cycle = goal:%v plan:%v, want goal", model.goalMode, model.planMode)
	}
	model.cycleInputMode()
	if model.goalMode || !model.planMode {
		t.Fatalf("after second cycle = goal:%v plan:%v, want plan", model.goalMode, model.planMode)
	}
	model.cycleInputMode()
	if model.goalMode || model.planMode {
		t.Fatalf("after third cycle = goal:%v plan:%v, want chat", model.goalMode, model.planMode)
	}
}

func TestTabCyclesChatGoalPlan(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	for _, want := range []string{"goal", "plan", "chat"} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
		model = next.(appModel)
		status := ansi.Strip(model.renderModeIndicator())
		if status != want {
			t.Fatalf("mode indicator = %q, want %q", status, want)
		}
	}
}

func TestMultilineTextKeepsChatModeAndSingleDockStyle(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("first\nsecond")
	model.syncInputMode()

	if got := ansi.Strip(model.renderModeIndicator()); got != "chat" {
		t.Fatalf("multiline mode indicator = %q, want chat", got)
	}
	if got := model.renderInputBox(); strings.Contains(ansi.Strip(got), "multiline") {
		t.Fatalf("input box leaked multiline mode label: %q", got)
	}
}

func TestPlanModeSubmitsStartsPlanSession(t *testing.T) {
	controller := &modeTestPlanController{}
	model := newTestModel(&fakeRunner{})
	model.planController = controller
	model.planMode = true
	model.input.SetValue("fix the login flow")

	next, _ := model.handleSubmit()
	model = next.(appModel)
	if controller.started != "fix the login flow" {
		t.Fatalf("plan controller started with %q, want the requirement", controller.started)
	}
	if !model.planWorking {
		t.Fatalf("planWorking = false, want true after starting a plan session")
	}
	if len(model.transcript) != 1 {
		t.Fatalf("transcript = %#v, want one plan entry", model.transcript)
	}
	entry := model.transcript[0]
	if entry.title != "plan" || !strings.Contains(entry.body, "fix the login flow") {
		t.Fatalf("plan entry = %#v", entry)
	}
}

func TestPlanResumeCommandExplicitlyRestartsAttachedPlan(t *testing.T) {
	controller := &modeTestPlanController{}
	model := newTestModel(&fakeRunner{})
	model.planController = controller

	handled, cmd := model.handleCommand("/plan resume")
	if !handled {
		t.Fatal("/plan resume was not handled")
	}
	if cmd != nil {
		_ = cmd()
	}
	if controller.resumes != 1 || !model.planWorking || !model.planMode || model.turnStartedAt.IsZero() {
		t.Fatalf("resume state: calls=%d working=%v mode=%v started=%v", controller.resumes, model.planWorking, model.planMode, model.turnStartedAt)
	}
}

func TestChatSubmitBlockedWhilePlanWorking(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.planController = &modeTestPlanController{}
	model.planWorking = true
	model.input.SetValue("hello")

	next, _ := model.handleSubmit()
	model = next.(appModel)
	if len(model.transcript) != 1 {
		t.Fatalf("transcript = %#v, want one busy entry", model.transcript)
	}
	if model.transcript[0].title != "busy" {
		t.Fatalf("entry = %#v, want busy", model.transcript[0])
	}
}

func TestPlanModeDoesNotAddInputFrame(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.planMode = true
	model.relayout()

	input := ansi.Strip(model.renderInputBox())
	for _, corner := range []string{"┌", "┐", "└", "┘"} {
		if strings.Contains(input, corner) {
			t.Fatalf("plan input = %q, contains nested frame corner %q", input, corner)
		}
	}
	bottom := ansi.Strip(model.renderBottomDockLine(80))
	if !strings.Contains(bottom, "plan") {
		t.Fatalf("bottom border = %q, want plan indicator", bottom)
	}
}

// ---------- 提交即回底：上翻脱离状态中发送消息，发送后应回到底部 ----------

// newScrollOffBottomModel 构造一段过满 transcript 并上翻到顶部（脱离贴底跟随）的模型
func newScrollOffBottomModel(t *testing.T, runner Runner) appModel {
	t.Helper()
	model := newTestModel(runner)
	model.ready = true
	model.width = 100
	model.height = 30
	model.relayout()
	for i := 0; i < 30; i++ {
		model.addEntry(transcriptEntry{kind: entryAssistant, body: fmt.Sprintf("history %d\nline b\nline c\nline d", i)})
	}
	model.viewport.GotoBottom()
	if !model.viewport.AtBottom() {
		t.Fatal("precondition: transcript should start pinned at bottom")
	}
	model.viewport.GotoTop()
	if model.viewport.AtBottom() {
		t.Fatal("precondition: transcript should be scrollable off the bottom")
	}
	return model
}

func requireSubmitResultAtBottom(t *testing.T, model appModel) appModel {
	t.Helper()
	next, _ := model.handleSubmit()
	updated, ok := next.(appModel)
	if !ok {
		t.Fatalf("handleSubmit returned %T, want appModel", next)
	}
	if !updated.viewport.AtBottom() {
		t.Fatalf("submit should return viewport to bottom, yoffset=%d", updated.viewport.YOffset)
	}
	return updated
}

func TestSubmitChatTurnReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	model.input.SetValue("新的问题")
	updated := requireSubmitResultAtBottom(t, model)
	last := updated.transcript[len(updated.transcript)-1]
	if last.kind != entryUser || !strings.Contains(last.body, "新的问题") {
		t.Fatalf("last entry = %#v, want user entry for submitted text", last)
	}
}

func TestSubmitSteerReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	// 模拟进行中回合：guard 与 legacy running 双重置位（handleSubmit 会先 reconcile）
	model.queryGuard.StartModel()
	model.running = true
	model.input.SetValue("补充说明")
	updated := requireSubmitResultAtBottom(t, model)
	last := updated.transcript[len(updated.transcript)-1]
	if last.kind != entryUser || last.title != "you (steer)" {
		t.Fatalf("last entry = %#v, want steer user entry", last)
	}
}

func TestSubmitQueueReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &noSteerRunner{})
	model.queryGuard.StartModel()
	model.running = true
	model.input.SetValue("排队消息")
	updated := requireSubmitResultAtBottom(t, model)
	if got := len(updated.chatQueue.Items()); got != 1 {
		t.Fatalf("queue length = %d, want 1 queued message", got)
	}
}

func TestSubmitGoalReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	model.goalController = &modeTestGoalController{}
	model.goalMode = true
	model.input.SetValue("达成大目标")
	requireSubmitResultAtBottom(t, model)
}

func TestSubmitPlanReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	model.planController = &modeTestPlanController{}
	model.planMode = true
	model.input.SetValue("修一下登录流程")
	requireSubmitResultAtBottom(t, model)
}

func TestSubmitShellCommandReturnsViewportToBottom(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	model.terminalMode = true
	model.input.SetValue("echo hi")
	requireSubmitResultAtBottom(t, model)
}

func TestBusyChatSubmitKeepsViewportOffset(t *testing.T) {
	model := newScrollOffBottomModel(t, &fakeRunner{})
	model.planController = &modeTestPlanController{}
	model.planWorking = true
	model.input.SetValue("hello")
	before := model.viewport.YOffset
	next, _ := model.handleSubmit()
	updated := next.(appModel)
	if updated.viewport.AtBottom() {
		t.Fatal("rejected (busy) submit should not jump to bottom")
	}
	if updated.viewport.YOffset != before {
		t.Fatalf("rejected submit moved viewport: yoffset %d -> %d", before, updated.viewport.YOffset)
	}
}
