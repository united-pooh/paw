package main

import (
	"testing"

	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
	selecttool "paw/internal/tool/select"
)

func TestRegisterInteractiveToolsAddsSelect(t *testing.T) {
	registry := tool.NewRegistry()
	broker := selecttool.NewBroker()
	defer broker.Close()
	if err := registerInteractiveTools(registry, broker); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("Select"); !ok {
		t.Fatal("interactive registry missing Select")
	}
}

func TestRegisterToolsDoesNotAddSelect(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("Select"); ok {
		t.Fatal("base registry unexpectedly contains Select")
	}
}

func TestRegisterInteractiveToolsRejectsNil(t *testing.T) {
	broker := selecttool.NewBroker()
	defer broker.Close()
	if err := registerInteractiveTools(nil, broker); err == nil || err.Error() != "tool registry is nil" {
		t.Fatalf("nil registry error = %v", err)
	}
	if err := registerInteractiveTools(tool.NewRegistry(), nil); err == nil || err.Error() != "selection broker is nil" {
		t.Fatalf("nil broker error = %v", err)
	}
}

func TestRegisterToolsIncludesEdit(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, false); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	ed, ok := registry.Get("Edit")
	if !ok {
		t.Fatal("Edit tool not registered")
	}
	if ed.Name() != "Edit" {
		t.Fatalf("Edit tool name = %q", ed.Name())
	}
	if _, ok := ed.(*toolfile.EditTool); !ok {
		t.Fatalf("Edit tool concrete type = %T, want *toolfile.EditTool", ed)
	}
}

func TestRegisterToolsEnablesOutsideReadInDangerousMode(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, true); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	registered, ok := registry.Get("Read")
	if !ok {
		t.Fatal("Read tool not registered")
	}
	readTool, ok := registered.(*toolfile.ReadTool)
	if !ok {
		t.Fatalf("Read tool concrete type = %T, want *toolfile.ReadTool", registered)
	}
	if !readTool.AllowOutsideRoot {
		t.Fatal("Read tool outside-root access is disabled")
	}
}
