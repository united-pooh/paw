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

	"paw/internal/tool"
)

func TestBashStreamTerminatesChildProcessGroup(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	bash := &BashTool{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, interrupted, err := bash.Stream(ctx, json.RawMessage(`{"command":"sleep 30 & child=$!; printf '%s' $child > `+pidFile+`; printf ready; wait $child"}`), func(event tool.ToolOutputEvent) error {
		if strings.Contains(event.Chunk, "ready") {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if !interrupted {
		t.Fatal("Stream() interrupted = false, want true")
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("ReadFile(pid) error = %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
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
