package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	osexec "os/exec"
	"sync"

	"paw/internal/tool"
)

// Stream runs bash while forwarding stdout and stderr as they arrive. The
// returned output uses the same bounded collector and non-zero-exit rendering
// as Run; interrupted distinguishes caller cancellation from command failure.
func (t *BashTool) Stream(ctx context.Context, input json.RawMessage, emit func(tool.ToolOutputEvent) error) (output string, interrupted bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", true, nil
	}

	in, err := decodeBashInput(input)
	if err != nil {
		return "", false, err
	}
	if blocked, pattern := checkCommandSafety(in.Command); blocked {
		return "", false, fmt.Errorf("命令包含不允许的操作（匹配到危险模式: %s），执行已拦截", pattern)
	}
	workdir, err := resolveWorkingDir(t.Root, in.CWD)
	if err != nil {
		return "", false, err
	}

	timeout := resolveTimeout(in.TimeoutSeconds)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := osexec.Command("bash", "-c", in.Command)
	cmd.Dir = workdir
	cmd.Env = shellEnv(t.Root)
	configureProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}

	var collected streamCollector
	var emitMu sync.Mutex
	var emitErr error
	var stopOnce sync.Once
	stopProcess := func() {
		stopOnce.Do(func() { terminateProcessGroup(cmd) })
	}
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-execCtx.Done():
			stopProcess()
		case <-watchDone:
		}
	}()

	read := func(stream tool.ToolOutputStream, r io.Reader, done chan<- struct{}) {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				collected.Write(chunk)
				if emit != nil {
					emitMu.Lock()
					if emitErr == nil {
						emitErr = emit(tool.ToolOutputEvent{Stream: stream, Chunk: string(chunk)})
						if emitErr != nil {
							cancel()
						}
					}
					emitMu.Unlock()
				}
			}
			if readErr != nil {
				return
			}
		}
	}
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go read(tool.ToolOutputStdout, stdout, stdoutDone)
	go read(tool.ToolOutputStderr, stderr, stderrDone)

	waitErr := cmd.Wait()
	// Closing the pipe readers after the process exits prevents a descendant
	// that inherited a pipe from keeping the collector blocked indefinitely.
	_ = stdout.Close()
	_ = stderr.Close()
	<-stdoutDone
	<-stderrDone
	close(watchDone)

	rendered := collected.String()
	if emitErr != nil {
		return rendered, false, emitErr
	}
	if ctx.Err() != nil {
		return rendered, true, nil
	}
	if execCtx.Err() == context.DeadlineExceeded {
		if rendered == "" {
			return "", false, fmt.Errorf("command timed out after %s", timeout)
		}
		return rendered, false, fmt.Errorf("command timed out after %s\noutput:\n%s", timeout, rendered)
	}
	if waitErr != nil {
		return appendExitStatus(rendered, waitErr.Error()), false, nil
	}
	return rendered, false, nil
}

type streamCollector struct {
	mu  sync.Mutex
	buf limitedBuffer
}

func (c *streamCollector) Write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.buf.Write(p)
}

func (c *streamCollector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func appendExitStatus(output, status string) string {
	exitInfo := fmt.Sprintf("\n[exit status: %s]", status)
	if output == "" {
		return exitInfo
	}
	return output + exitInfo
}
