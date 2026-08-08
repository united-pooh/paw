package bubble

import (
	"context"
	"paw/internal/subagent"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeGoalController struct {
	started []string
	status  string
	pauses  int
	resumes int
	cancels int
	err     error
}

func (f *fakeGoalController) Start(objective string) (string, error) {
	f.started = append(f.started, objective)
	return "goal-1", f.err
}
func (f *fakeGoalController) Status() string { return f.status }
func (f *fakeGoalController) Pause() error   { f.pauses++; return f.err }
func (f *fakeGoalController) Resume() error  { f.resumes++; return f.err }
func (f *fakeGoalController) Cancel() error  { f.cancels++; return f.err }
func (f *fakeGoalController) Budget() string { return "budget" }

func TestGoalCommandRegisteredAndAllowedWhileRunning(t *testing.T) {
	registry := NewCommandRegistry()
	command, ok := registry.Lookup("/goal")
	if !ok {
		t.Fatal("/goal is not registered")
	}
	if !command.AllowWhileRunning {
		t.Fatal("/goal must remain available for lifecycle controls")
	}
}

func TestGoalCommandLifecycle(t *testing.T) {
	model := newModel(nil, &fakeRunner{}, "session-1", nil, nil, nil, nil, nil)
	controller := &fakeGoalController{status: "status: running"}
	model.goalController = controller

	cases := []struct {
		name  string
		line  string
		check func(*testing.T)
	}{
		{"start", "/goal start ship feature", func(t *testing.T) {
			if len(controller.started) != 1 || controller.started[0] != "ship feature" {
				t.Fatalf("started = %#v", controller.started)
			}
		}},
		{"status", "/goal status", func(t *testing.T) {}},
		{"pause", "/goal pause", func(t *testing.T) {
			if controller.pauses != 1 {
				t.Fatalf("pauses = %d", controller.pauses)
			}
		}},
		{"resume", "/goal resume", func(t *testing.T) {
			if controller.resumes != 1 {
				t.Fatalf("resumes = %d", controller.resumes)
			}
		}},
		{"stop", "/goal stop", func(t *testing.T) {
			if controller.cancels != 1 {
				t.Fatalf("cancels = %d", controller.cancels)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handled, cmd := model.handleCommand(tc.line)
			if !handled {
				t.Fatal("command was not handled")
			}
			if cmd != nil {
				_ = cmd()
			}
			tc.check(t)
		})
	}
}

func TestGoalCommandRejectsEmptyStart(t *testing.T) {
	model := newModel(nil, &fakeRunner{}, "session-1", nil, nil, nil, nil, nil)
	controller := &fakeGoalController{}
	model.goalController = controller
	before := len(model.transcript)
	handled, _ := model.handleCommand("/goal start")
	if !handled {
		t.Fatal("command was not handled")
	}
	if len(controller.started) != 0 {
		t.Fatalf("started = %#v", controller.started)
	}
	if len(model.transcript) <= before {
		t.Fatal("expected usage feedback")
	}
}

// TestGoalCommandSyncsWorkingState 验证 /goal 生命周期命令与 goalWorking
// 标志同步：start 进入工作态，pause/stop 释放，resume 恢复。goalWorking
// 一旦残留，header 会在会话结束后永久显示 working 0s。
func TestGoalCommandSyncsWorkingState(t *testing.T) {
	model := newModel(nil, &fakeRunner{}, "session-1", nil, nil, nil, nil, nil)
	controller := &fakeGoalController{status: "status: running"}
	model.goalController = controller

	handled, cmd := model.handleCommand("/goal start ship feature")
	if !handled {
		t.Fatal("start command was not handled")
	}
	if cmd != nil {
		_ = cmd()
	}
	if !model.goalWorking {
		t.Fatal("goalWorking = false, want true after /goal start")
	}
	if model.turnStartedAt.IsZero() {
		t.Fatal("turnStartedAt not set after /goal start")
	}

	handled, _ = model.handleCommand("/goal pause")
	if !handled {
		t.Fatal("pause command was not handled")
	}
	if model.goalWorking {
		t.Fatal("goalWorking = true, want false after /goal pause")
	}
	if !model.turnStartedAt.IsZero() {
		t.Fatal("turnStartedAt not cleared after /goal pause")
	}

	handled, _ = model.handleCommand("/goal resume")
	if !handled {
		t.Fatal("resume command was not handled")
	}
	if !model.goalWorking {
		t.Fatal("goalWorking = false, want true after /goal resume")
	}

	model.goalMode = true
	handled, _ = model.handleCommand("/goal stop")
	if !handled {
		t.Fatal("stop command was not handled")
	}
	if model.goalWorking {
		t.Fatal("goalWorking = true, want false after /goal stop")
	}
	if model.goalMode {
		t.Fatal("goalMode = true, want false after /goal stop")
	}
	if !model.turnStartedAt.IsZero() {
		t.Fatal("turnStartedAt not cleared after /goal stop")
	}
	if model.isAgentWorking() {
		t.Fatal("isAgentWorking = true, want false after /goal stop")
	}
}

// TestGoalStoppedMsgReleasesWorkingState 验证 goal 会话在后台结束
// （完成/失败/取消/暂停）时，NotifyGoalStopped 投递的 goalStoppedMsg
// 能释放 goalWorking，避免 header 永久显示 working 0s。
func TestGoalStoppedMsgReleasesWorkingState(t *testing.T) {
	model := newModel(nil, &fakeRunner{}, "session-1", nil, nil, nil, nil, nil)
	model.goalWorking = true
	model.goalMode = true
	model.turnStartedAt = time.Now()
	model.turnID = "goal-1"

	updated, _ := model.Update(goalStoppedMsg{reason: "goal.completed"})
	next, ok := updated.(appModel)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	if next.goalWorking {
		t.Fatal("goalWorking = true, want false after goalStoppedMsg")
	}
	if next.goalMode {
		t.Fatal("goalMode = true, want false after goalStoppedMsg")
	}
	if !next.turnStartedAt.IsZero() {
		t.Fatal("turnStartedAt not cleared after goalStoppedMsg")
	}
	if next.turnID != "" {
		t.Fatalf("turnID = %q, want empty after goalStoppedMsg", next.turnID)
	}
	if next.isAgentWorking() {
		t.Fatal("isAgentWorking = true, want false after goalStoppedMsg")
	}
}

// TestSubagentFinishedMsgClearsGenerating 验证 subagent 轮结束也清除
// isGenerating（与 turnFinishedMsg 对称），防止流式标记残留导致 header
// 永久 working。
func TestSubagentFinishedMsgClearsGenerating(t *testing.T) {
	model := newModel(nil, &fakeRunner{}, "session-1", nil, nil, nil, nil, nil)
	model.ctx = context.Background()
	model.isGenerating = true

	updated, _ := model.Update(subagentFinishedMsg{result: subagent.Result{}, err: nil})
	next, ok := updated.(appModel)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	if next.isGenerating {
		t.Fatal("isGenerating = true, want false after subagentFinishedMsg")
	}
	if next.isAgentWorking() {
		t.Fatal("isAgentWorking = true, want false after subagentFinishedMsg")
	}
}

var _ tea.Cmd = nil
