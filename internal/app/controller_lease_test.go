package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type leaseHelperResult struct {
	Code            string         `json:"code"`
	PID             int            `json:"pid,omitempty"`
	Mode            ControllerMode `json:"mode,omitempty"`
	InstanceID      string         `json:"instance_id,omitempty"`
	OwnerPID        int            `json:"owner_pid,omitempty"`
	OwnerMode       ControllerMode `json:"owner_mode,omitempty"`
	OwnerInstanceID string         `json:"owner_instance_id,omitempty"`
	Message         string         `json:"message,omitempty"`
}

type runningLeaseHelper struct {
	cmd   *exec.Cmd
	ready leaseHelperResult
}

func TestControllerLeaseExclusiveAcrossProcesses(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("controller locks are unsupported on this platform")
	}

	helper := buildLeaseHelper(t)
	dir := t.TempDir()
	first := startLeaseHelper(t, helper, dir, ControllerModeTUI)
	t.Cleanup(first.Stop)

	second := runLeaseHelper(t, helper, dir, ControllerModeWeb)
	if second.Code != "workspace_locked" || second.OwnerPID != first.ready.PID || second.OwnerMode != ControllerModeTUI {
		t.Fatalf("second = %+v, first = %+v", second, first.ready)
	}
	if second.OwnerInstanceID == "" || second.OwnerInstanceID != first.ready.InstanceID {
		t.Fatalf("second owner instance = %q, first instance = %q", second.OwnerInstanceID, first.ready.InstanceID)
	}

	first.Stop()
	third := runLeaseHelper(t, helper, dir, ControllerModeWeb)
	if third.Code != "acquired" || third.Mode != ControllerModeWeb {
		t.Fatalf("third = %+v", third)
	}
}

func TestControllerLeaseWritesDiagnosticsAndClosesIdempotently(t *testing.T) {
	dir := t.TempDir()
	lease, err := AcquireControllerLease(dir, ControllerModeTUI)
	if err != nil {
		if errors.Is(err, ErrControllerLockUnsupported) {
			t.Skip(err)
		}
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, controllerLockFileName))
	if err != nil {
		t.Fatal(err)
	}
	var owner controllerLeaseOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		t.Fatalf("decode diagnostics: %v; data=%q", err, data)
	}
	if owner.PID != os.Getpid() || owner.Mode != ControllerModeTUI || owner.InstanceID != lease.InstanceID() || owner.StartedAt.IsZero() {
		t.Fatalf("owner = %+v, instance = %q", owner, lease.InstanceID())
	}

	const callers = 32
	var wait sync.WaitGroup
	errors := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- lease.Close()
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	next, err := AcquireControllerLease(dir, ControllerModeWeb)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildLeaseHelper(t *testing.T) string {
	t.Helper()
	root := moduleRoot(t)
	name := "leasehelper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", binary, "./internal/app/testdata/leasehelper")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build lease helper: %v\n%s", err, output)
	}
	return binary
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func startLeaseHelper(t *testing.T, helper, dir string, mode ControllerMode) *runningLeaseHelper {
	t.Helper()
	cmd := exec.Command(helper, dir, string(mode), "hold")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	resultChannel := make(chan leaseHelperResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		var result leaseHelperResult
		err := json.NewDecoder(stdout).Decode(&result)
		if err != nil {
			errorChannel <- err
			return
		}
		resultChannel <- result
	}()

	var result leaseHelperResult
	select {
	case result = <-resultChannel:
	case err := <-errorChannel:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("decode lease helper: %v", err)
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("lease helper did not become ready")
	}
	if result.Code != "acquired" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("lease helper result = %+v", result)
	}
	return &runningLeaseHelper{cmd: cmd, ready: result}
}

func (h *runningLeaseHelper) Stop() {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Kill()
	_ = h.cmd.Wait()
	h.cmd = nil
}

func runLeaseHelper(t *testing.T, helper, dir string, mode ControllerMode) leaseHelperResult {
	t.Helper()
	output, err := exec.Command(helper, dir, string(mode)).CombinedOutput()
	if err != nil {
		t.Fatalf("run lease helper: %v\n%s", err, output)
	}
	var result leaseHelperResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode lease helper result: %v; output=%q", err, output)
	}
	return result
}
