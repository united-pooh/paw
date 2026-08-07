package bubble

import (
	"testing"

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

var _ tea.Cmd = nil
