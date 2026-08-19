package loop

import (
	"testing"

	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
)

func TestRunnerSetYoloModeUpdatesReadAndHandler(t *testing.T) {
	registry := tool.NewRegistry()
	readTool := &toolfile.ReadTool{Root: t.TempDir()}
	registry.Register(readTool)
	runner := &Engine{enginePorts: enginePorts{registry: registry}}

	var propagated bool
	runner.SetYoloModeHandler(func(enabled bool) {
		propagated = enabled
	})

	enabled, err := runner.SetYoloMode(true)
	if err != nil {
		t.Fatalf("SetYoloMode(true): %v", err)
	}
	if !enabled || !runner.YoloMode() || !readTool.OutsideRootAllowed() {
		t.Fatal("yolo mode was not enabled")
	}
	if !propagated {
		t.Fatal("yolo mode was not propagated")
	}

	enabled, err = runner.SetYoloMode(false)
	if err != nil {
		t.Fatalf("SetYoloMode(false): %v", err)
	}
	if enabled || runner.YoloMode() || readTool.OutsideRootAllowed() {
		t.Fatal("yolo mode was not disabled")
	}
	if propagated {
		t.Fatal("disabled yolo mode was not propagated")
	}
}
