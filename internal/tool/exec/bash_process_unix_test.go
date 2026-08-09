//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package exec

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBashStreamTerminatesChildProcessGroup(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	bash := &BashTool{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan struct {
		interrupted bool
		err         error
	}, 1)
	go func() {
		_, interrupted, err := bash.Stream(ctx, json.RawMessage(`{"command":"sleep 30 & child=$!; printf '%s' $child > `+pidFile+`; printf ready; wait $child"}`))
		resultCh <- struct {
			interrupted bool
			err         error
		}{interrupted, err}
	}()
	// The child writes its pid file before entering `wait`, so its presence is
	// the signal that the process group is up; cancel once it appears.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, statErr := os.Stat(pidFile); statErr == nil {
			cancel()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child pid file never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Stream() error = %v", result.err)
		}
		if !result.interrupted {
			t.Fatal("Stream() interrupted = false, want true")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled Stream() did not return")
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("ReadFile(pid) error = %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	killDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(killDeadline) {
		err = syscall.Kill(pid, 0)
		if err != nil {
			if err == syscall.ESRCH {
				return
			}
			t.Fatalf("Kill(%d, 0) error = %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child process %d is still alive after stream interruption", pid)
}

var _ = syscall.Signal(0)
