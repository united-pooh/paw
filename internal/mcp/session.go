package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// RPCSession is the small transport-independent MCP JSON-RPC surface used by
// the manager and by tests with in-memory pipes.
type RPCSession interface {
	Call(context.Context, string, any, any) error
	Notify(context.Context, string, any) error
	Notifications() <-chan Notification
	Close(context.Context) error
}

// processSession owns one MCP server process and the stdio JSON-RPC transport
// attached to it. MCP initialization and capability discovery are deliberately
// kept above this layer so the same transport can be used by every manager.
type processSession struct {
	config ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer
	rpc    *jsonRPCSession

	waitOnce sync.Once
	waitDone chan struct{}
	waitMu   sync.RWMutex
	waitErr  error

	closeOnce sync.Once
	closeErr  error
}

func startSession(ctx context.Context, config ServerConfig) (*processSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(config.Command) == "" {
		return nil, errors.New("MCP server command is empty")
	}
	if strings.TrimSpace(config.WorkDir) == "" {
		return nil, fmt.Errorf("MCP server %q working directory is empty", config.Name)
	}

	cmd := exec.CommandContext(ctx, config.Command, config.Args...)
	cmd.Dir = config.WorkDir
	cmd.Env = mergedEnvironment(config.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create MCP server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create MCP server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("create MCP server stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start MCP server %q: %w", config.Name, err)
	}

	tail := newTailBuffer(16 * 1024)
	process := &processSession{
		config:   config,
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   tail,
		waitDone: make(chan struct{}),
	}
	process.rpc = newJSONRPCSessionWithClosers(stdout, stdin, stdout.Close, stdin.Close)
	go func() {
		_, _ = io.Copy(tail, stderr)
		_ = stderr.Close()
	}()
	go process.wait()
	return process, nil
}

func (s *processSession) Call(ctx context.Context, method string, params, result any) error {
	if s == nil || s.rpc == nil {
		return errors.New("MCP process session is nil")
	}
	return s.rpc.Call(ctx, method, params, result)
}

func (s *processSession) Notify(ctx context.Context, method string, params any) error {
	if s == nil || s.rpc == nil {
		return errors.New("MCP process session is nil")
	}
	return s.rpc.Notify(ctx, method, params)
}

func (s *processSession) Notifications() <-chan Notification {
	if s == nil || s.rpc == nil {
		return nil
	}
	return s.rpc.Notifications()
}

func (s *processSession) PID() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

func (s *processSession) StderrTail() string {
	if s == nil || s.stderr == nil {
		return ""
	}
	return s.stderr.String()
}

func (s *processSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.rpc.Close(ctx)
		select {
		case <-s.waitDone:
		default:
			select {
			case <-s.waitDone:
			case <-ctx.Done():
				if s.cmd.Process != nil {
					if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && s.closeErr == nil {
						s.closeErr = err
					}
				}
				select {
				case <-s.waitDone:
				case <-context.Background().Done():
				}
			}
		}
	})
	return s.closeErr
}

func (s *processSession) wait() {
	s.waitOnce.Do(func() {
		err := s.cmd.Wait()
		s.waitMu.Lock()
		s.waitErr = err
		s.waitMu.Unlock()
		close(s.waitDone)
	})
}

func (s *processSession) WaitError() error {
	if s == nil {
		return nil
	}
	<-s.waitDone
	s.waitMu.RLock()
	defer s.waitMu.RUnlock()
	return s.waitErr
}

func mergedEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	values := os.Environ()
	indices := make(map[string]int, len(values))
	for i, value := range values {
		if key, _, ok := strings.Cut(value, "="); ok {
			indices[key] = i
		}
	}
	for key, value := range overrides {
		entry := key + "=" + value
		if index, ok := indices[key]; ok {
			values[index] = entry
		} else {
			indices[key] = len(values)
			values = append(values, entry)
		}
	}
	return values
}

type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newTailBuffer(max int) *tailBuffer {
	if max < 1 {
		max = 1
	}
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > b.max {
		b.data = append([]byte(nil), b.data[len(b.data)-b.max:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return redactSensitiveText(string(append([]byte(nil), b.data...)))
}
