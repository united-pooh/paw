package config

import (
	"strings"
	"testing"
)

func intPtr(value int) *int { return &value }

func TestResolveEffectiveSandboxDefaults(t *testing.T) {
	eff := ResolveEffectiveSandbox(Document{}, WorkspaceDocument{})
	if eff.JobWallSeconds != 90*60 {
		t.Fatalf("default job wall seconds = %d, want 90 minutes", eff.JobWallSeconds)
	}
	want := EffectiveSandbox{
		MaxWorkers:     SandboxDefaultMaxWorkers,
		QueueCapacity:  SandboxDefaultQueueCapacity,
		CPUSeconds:     SandboxDefaultCPUSeconds,
		FileSizeMiB:    SandboxDefaultFileSizeMiB,
		MaxProcesses:   SandboxDefaultMaxProcesses,
		OpenFiles:      SandboxDefaultOpenFiles,
		JobWallSeconds: SandboxDefaultJobWallSecs,
	}
	if eff != want {
		t.Fatalf("defaults mismatch:\n got %#v\nwant %#v", eff, want)
	}
}

func TestResolveEffectiveSandboxGlobalBaselineApplies(t *testing.T) {
	document := Document{Sandbox: &Sandbox{Limits: &SandboxLimits{
		CPUSeconds: intPtr(30), FileSizeMiB: intPtr(64), MaxProcesses: intPtr(4),
		OpenFiles: intPtr(128), JobWallSeconds: intPtr(120),
	}}}
	eff := ResolveEffectiveSandbox(document, WorkspaceDocument{})
	if eff.CPUSeconds != 30 || eff.FileSizeMiB != 64 || eff.MaxProcesses != 4 ||
		eff.OpenFiles != 128 || eff.JobWallSeconds != 120 {
		t.Fatalf("global baseline not applied: %#v", eff)
	}
}

func TestResolveEffectiveSandboxWorkspacePoolClampedByGlobalCap(t *testing.T) {
	globalWorkers, globalQueue := 8, 16
	wsWorkers, wsQueue := 10, 999
	document := Document{Sandbox: &Sandbox{Pool: &SandboxPool{
		MaxWorkers: intPtr(globalWorkers), QueueCapacity: intPtr(globalQueue),
	}}}
	workspace := WorkspaceDocument{Sandbox: &SandboxWorkspace{Pool: &SandboxPool{
		MaxWorkers: intPtr(wsWorkers), QueueCapacity: intPtr(wsQueue),
	}}}
	eff := ResolveEffectiveSandbox(document, workspace)
	if eff.MaxWorkers != globalWorkers || eff.QueueCapacity != globalQueue {
		t.Fatalf("workspace pool not clamped to global cap: %#v", eff)
	}

	// 未设全局 cap 时 workspace 生效，但受 sanity 上限约束。
	effSolo := ResolveEffectiveSandbox(Document{}, workspace)
	if effSolo.MaxWorkers != wsWorkers {
		t.Fatalf("workspace pool without global cap = %d, want %d", effSolo.MaxWorkers, wsWorkers)
	}
}

func TestParseAndValidateWorkspaceRejectsSandboxLimits(t *testing.T) {
	_, err := parseAndValidateWorkspace([]byte(`{"sandbox":{"limits":{"cpuSeconds":10}}}`), "test.jsonc", Document{})
	if err == nil || !strings.Contains(err.Error(), "sandbox.limits") {
		t.Fatalf("workspace sandbox.limits error = %v, want rejection", err)
	}
	_, err = parseAndValidateWorkspace([]byte(`{"sandbox":{"pool":{"maxWorkers":4}}}`), "test.jsonc", Document{})
	if err != nil {
		t.Fatalf("workspace sandbox.pool.maxWorkers should be allowed, got %v", err)
	}
}

func TestValidateSandboxRejectsNonPositiveValues(t *testing.T) {
	_, _, err := parseAndValidateGlobal([]byte(`{"schemaVersion":2,"providers":{},"models":{},"sandbox":{"limits":{"cpuSeconds":0}}}`), "test.jsonc")
	if err == nil || !strings.Contains(err.Error(), "sandbox.limits.cpuSeconds") {
		t.Fatalf("global sandbox zero value error = %v, want rejection", err)
	}
}
