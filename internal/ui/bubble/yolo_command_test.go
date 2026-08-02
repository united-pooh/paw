package bubble

import (
	"context"
	"strings"
	"testing"

	"paw/internal/message"
)

type yoloCommandRunner struct {
	enabled bool
}

func (r *yoloCommandRunner) RunTurn(context.Context, string) (message.Message, error) {
	return message.Message{}, nil
}

func (r *yoloCommandRunner) ResetHistory() {}

func (r *yoloCommandRunner) LoadHistory(context.Context, string) ([]message.Message, error) {
	return nil, nil
}

func (r *yoloCommandRunner) SetYoloMode(enabled bool) (bool, error) {
	r.enabled = enabled
	return r.enabled, nil
}

func (r *yoloCommandRunner) YoloMode() bool {
	return r.enabled
}

func TestYoloCommandTogglesMode(t *testing.T) {
	runner := &yoloCommandRunner{}
	model := newModel(context.Background(), runner, "session", nil, nil, nil, nil, nil)

	handled, cmd := model.commandRegistry.Dispatch(&model, "/yolo")
	if !handled || cmd != nil {
		t.Fatalf("first dispatch = handled %v, cmd %v", handled, cmd)
	}
	if !runner.enabled {
		t.Fatal("/yolo did not enable yolo mode")
	}
	if len(model.transcript) == 0 || !strings.Contains(model.transcript[len(model.transcript)-1].body, "enabled") {
		t.Fatalf("enable feedback = %#v", model.transcript)
	}

	handled, cmd = model.commandRegistry.Dispatch(&model, "/yolo")
	if !handled || cmd != nil {
		t.Fatalf("second dispatch = handled %v, cmd %v", handled, cmd)
	}
	if runner.enabled {
		t.Fatal("second /yolo did not disable yolo mode")
	}
	if len(model.transcript) == 0 || !strings.Contains(model.transcript[len(model.transcript)-1].body, "disabled") {
		t.Fatalf("disable feedback = %#v", model.transcript)
	}
}

func TestYoloCommandAppearsInHelp(t *testing.T) {
	if help := NewCommandRegistry().HelpText(); !strings.Contains(help, "/yolo") {
		t.Fatalf("help text does not contain /yolo:\n%s", help)
	}
}
