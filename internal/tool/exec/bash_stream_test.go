package exec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"paw/internal/tool"
)

func TestBashStreamEmitsStdoutAndStderr(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	var mu sync.Mutex
	var events []tool.ToolOutputEvent
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf out; printf err >&2"}`), func(event tool.ToolOutputEvent) error {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		return nil
	})
	if err != nil || interrupted {
		t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err)
	}
	if !containsStreamText(events, tool.ToolOutputStdout, "out") || !containsStreamText(events, tool.ToolOutputStderr, "err") {
		t.Fatalf("events = %#v", events)
	}
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Fatalf("output = %q, want both streams", output)
	}
}

func TestBashStreamCancellationPreservesPartialOutput(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	var once sync.Once
	output, interrupted, err := bash.Stream(ctx, json.RawMessage(`{"command":"printf partial; printf 'stderr-partial' >&2; sleep 30"}`), func(event tool.ToolOutputEvent) error {
		if strings.Contains(event.Chunk, "partial") {
			once.Do(func() {
				close(started)
				cancel()
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if !interrupted {
		t.Fatal("Stream() interrupted = false, want true")
	}
	if !strings.Contains(output, "partial") {
		t.Fatalf("output = %q, want partial output", output)
	}
	select {
	case <-started:
	default:
		t.Fatal("command did not emit partial output")
	}
}

func TestBashStreamTimeoutPreservesOutput(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf before-timeout; sleep 5","timeout_seconds":1}`), nil)
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
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf failed; printf diagnostic >&2; exit 7"}`), nil)
	if err != nil || interrupted {
		t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err)
	}
	if !strings.Contains(output, "failed") || !strings.Contains(output, "diagnostic") || !strings.Contains(output, "exit status") {
		t.Fatalf("output = %q, want output and exit status", output)
	}
}

func TestBashStreamEmitErrorStopsCommand(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	emitErr := errors.New("output consumer stopped")
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf hello; sleep 30"}`), func(tool.ToolOutputEvent) error {
		return emitErr
	})
	if !errors.Is(err, emitErr) {
		t.Fatalf("Stream() error = %v, want %v", err, emitErr)
	}
	if interrupted {
		t.Fatal("emit failure reported as caller interruption")
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("output = %q, want emitted output preserved", output)
	}
}

func TestBashStreamOutputLimitRemainsBounded(t *testing.T) {
	bash := &BashTool{Root: t.TempDir()}
	output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"head -c 40000 /dev/zero"}`), nil)
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

func containsStreamText(events []tool.ToolOutputEvent, stream tool.ToolOutputStream, text string) bool {
	for _, event := range events {
		if event.Stream == stream && strings.Contains(event.Chunk, text) {
			return true
		}
	}
	return false
}
