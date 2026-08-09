package exec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashStreamCollectsStdoutAndStderr(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf out; printf err >&2"}`))
	if err != nil || interrupted {
		t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err)
	}
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Fatalf("output = %q, want both streams", output)
	}
}

func TestBashStreamCancellationPreservesPartialOutput(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputCh := make(chan struct {
		output      string
		interrupted bool
		err         error
	}, 1)
	go func() {
		output, interrupted, err := bash.Stream(ctx, json.RawMessage(`{"command":"printf partial; printf 'stderr-partial' >&2; sleep 30"}`))
		outputCh <- struct {
			output      string
			interrupted bool
			err         error
		}{output, interrupted, err}
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case result := <-outputCh:
		if result.err != nil {
			t.Fatalf("Stream() error = %v", result.err)
		}
		if !result.interrupted {
			t.Fatal("Stream() interrupted = false, want true")
		}
		if !strings.Contains(result.output, "partial") {
			t.Fatalf("output = %q, want partial output", result.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled Stream() did not return")
	}
}

func TestBashStreamTimeoutPreservesOutput(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf before-timeout; sleep 5","timeout_seconds":1}`))
	if interrupted {
		t.Fatal("timeout reported as caller interruption")
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Stream() error = %v, want timeout error", err)
	}
	if !strings.Contains(output, "before-timeout") && !strings.Contains(err.Error(), "before-timeout") {
		t.Fatalf("output=%q err=%v, want partial output", output, err)
	}
}

func TestBashStreamNonZeroExitIsReturnedInOutput(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf failed; printf diagnostic >&2; exit 7"}`))
	if err != nil || interrupted {
		t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err)
	}
	if !strings.Contains(output, "failed") || !strings.Contains(output, "diagnostic") || !strings.Contains(output, "exit status") {
		t.Fatalf("output = %q, want output and exit status", output)
	}
}

func TestBashStreamOutputLimitRemainsBounded(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"head -c 40000 /dev/zero"}`))
	if err != nil || interrupted {
		t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err)
	}
	if len(output) > maxOutputBytes+len("\n[output truncated]") {
		t.Fatalf("output length = %d, exceeds bounded result", len(output))
	}
	if !strings.Contains(output, "[output truncated]") {
		t.Fatal("output missing truncation marker")
	}
}
