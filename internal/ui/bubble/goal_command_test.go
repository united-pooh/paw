package bubble

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"paw/internal/subagent"
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

func TestGoalModeSubmitPreservesTokenizedTranscript(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		kind  inputTokenKind
		start int
		label string
	}{
		{name: "command", raw: "/help", kind: inputTokenCommand, start: 0, label: "help"},
		{name: "file", raw: "read @README.md", kind: inputTokenFile, start: len([]rune("read ")), label: "README.md"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.ready = true
			model.width = 80
			model.height = 20
			model.goalMode = true
			controller := &fakeGoalController{}
			model.goalController = controller
			model.input.SetValue(test.raw)
			model.inputTokens = []inputToken{{
				Kind:  test.kind,
				Start: test.start,
				End:   len([]rune(test.raw)),
				Label: test.label,
			}}

			next, _ := model.handleSubmit()
			model = next.(appModel)
			if len(controller.started) != 1 || controller.started[0] != test.raw {
				t.Fatalf("controller.started = %#v, want raw objective %q", controller.started, test.raw)
			}
			if model.input.Value() != "" || len(model.inputTokens) != 0 {
				t.Fatalf("submitted input retained state: value=%q tokens=%#v", model.input.Value(), model.inputTokens)
			}
			if len(model.transcript) < 2 {
				t.Fatalf("transcript = %#v, want objective and start confirmation", model.transcript)
			}
			objective := model.transcript[len(model.transcript)-2]
			confirmation := model.transcript[len(model.transcript)-1]
			if objective.kind != entryUser || objective.title != "you (goal)" || objective.body != test.raw {
				t.Fatalf("goal objective entry = %#v", objective)
			}
			if len(objective.inputTokens) != 1 {
				t.Fatalf("goal objective tokens = %#v, want one token", objective.inputTokens)
			}
			token := objective.inputTokens[0]
			if token.Kind != test.kind || token.Start != test.start || token.End != len([]rune(test.raw)) || token.Label != test.label {
				t.Fatalf("goal objective token = %#v", token)
			}
			rendered := renderEntry(objective, 80)
			plain := ansi.Strip(rendered)
			if !strings.Contains(plain, test.label) || strings.Contains(plain, test.raw) {
				t.Fatalf("rendered goal objective = %q, want label without raw syntax %q", plain, test.raw)
			}
			if !strings.Contains(rendered, inputTokenStyleFor(test.kind).Render(test.label)) {
				t.Fatalf("rendered goal objective missed token style: %q", rendered)
			}
			if confirmation.kind != entrySystem || confirmation.title != "goal" || confirmation.body != "started goal-1" {
				t.Fatalf("goal confirmation entry = %#v", confirmation)
			}
			if strings.Contains(confirmation.body, "objective:") || strings.Contains(confirmation.body, test.raw) {
				t.Fatalf("goal confirmation leaked objective: %q", confirmation.body)
			}
			if !model.goalWorking || model.turnStartedAt.IsZero() || model.turnID != "goal-1" {
				t.Fatalf("goal state = working:%v started:%v turnID:%q", model.goalWorking, model.turnStartedAt, model.turnID)
			}
		})
	}
}

func TestGoalModeSubmitFailureDoesNotRecordSuccessfulObjective(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.goalMode = true
	controller := &fakeGoalController{err: errors.New("start failed")}
	model.goalController = controller
	model.input.SetValue("read @README.md")
	model.inputTokens = []inputToken{{Kind: inputTokenFile, Start: len([]rune("read ")), End: len([]rune("read @README.md")), Label: "README.md"}}

	next, _ := model.handleSubmit()
	model = next.(appModel)
	if len(controller.started) != 1 || controller.started[0] != "read @README.md" {
		t.Fatalf("controller.started = %#v", controller.started)
	}
	for _, entry := range model.transcript {
		if entry.kind == entryUser && entry.title == "you (goal)" {
			t.Fatalf("failure recorded successful goal objective: %#v", entry)
		}
		if entry.kind == entrySystem && strings.HasPrefix(entry.body, "started ") {
			t.Fatalf("failure recorded start confirmation: %#v", entry)
		}
	}
	if model.goalWorking || !model.turnStartedAt.IsZero() || model.turnID != "" {
		t.Fatalf("failure entered working state: working=%v started=%v turnID=%q", model.goalWorking, model.turnStartedAt, model.turnID)
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
