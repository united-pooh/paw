package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gocode/internal/settings"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type WorkerRequest struct {
	TaskID          string               `json:"task_id"`
	SessionID       string               `json:"session_id"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	ParentTaskID    string               `json:"parent_task_id,omitempty"`
	Prompt          string               `json:"prompt"`
	Description     string               `json:"description,omitempty"`
	ContextMode     settings.ContextMode `json:"context_mode"`
	RunMode         settings.RunMode     `json:"run_mode"`
	Depth           int                  `json:"depth"`
	MaxDepth        int                  `json:"max_depth"`
}

type WorkerResult struct {
	TaskID     string `json:"task_id"`
	SessionID  string `json:"session_id"`
	Content    string `json:"content,omitempty"`
	Error      string `json:"error,omitempty"`
	ExitCode   int    `json:"exit_code"`
	UsedTokens int    `json:"used_tokens,omitempty"`
}

type Launcher interface {
	Start(ctx context.Context, req WorkerRequest) (Process, error)
}

type Process interface {
	PID() int
	Wait() (WorkerResult, error)
	Stop() error
}

type ProcessLauncher struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
}

func NewProcessLauncher(command, dir string) *ProcessLauncher {
	return &ProcessLauncher{
		Command: command,
		Args:    []string{"--subagent-worker"},
		Dir:     dir,
	}
}

func (l *ProcessLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	if l == nil {
		return nil, fmt.Errorf("subagent process launcher is nil")
	}
	command := strings.TrimSpace(l.Command)
	if command == "" {
		return nil, fmt.Errorf("subagent worker command is empty")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal worker request: %w", err)
	}
	payload = append(payload, '\n')

	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command, l.Args...)
	cmd.Dir = l.Dir
	cmd.Env = append(os.Environ(), l.Env...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start subagent worker: %w", err)
	}
	return &execProcess{cmd: cmd, cancel: cancel, stdout: &stdout, stderr: &stderr}, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (p *execProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *execProcess) Wait() (WorkerResult, error) {
	if p == nil || p.cmd == nil {
		return WorkerResult{ExitCode: 1}, fmt.Errorf("subagent process is nil")
	}
	err := p.cmd.Wait()
	if p.cancel != nil {
		p.cancel()
	}

	var result WorkerResult
	output := bytes.TrimSpace(p.stdout.Bytes())
	if len(output) > 0 {
		if decodeErr := json.Unmarshal(output, &result); decodeErr != nil {
			return WorkerResult{ExitCode: exitCodeFromError(err)}, fmt.Errorf("decode subagent worker output: %w: %s", decodeErr, string(output))
		}
	}
	if result.ExitCode == 0 && err != nil {
		result.ExitCode = exitCodeFromError(err)
	}
	if result.ExitCode == 0 && result.Error != "" {
		result.ExitCode = 1
	}

	if err != nil {
		detail := strings.TrimSpace(p.stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		if result.Error == "" {
			result.Error = detail
		}
		return result, fmt.Errorf("subagent worker failed: %s", detail)
	}
	if result.Error != "" {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

func (p *execProcess) Stop() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
			return err
		}
	}
	return nil
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

type inProcessLauncher struct {
	pid int
	run func(context.Context, WorkerRequest) (WorkerResult, error)
}

func newInProcessLauncher(run func(context.Context, WorkerRequest) (WorkerResult, error)) Launcher {
	return &inProcessLauncher{pid: os.Getpid(), run: run}
}

func (l *inProcessLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	if l == nil || l.run == nil {
		return nil, fmt.Errorf("subagent in-process launcher is nil")
	}
	childCtx, cancel := context.WithCancel(ctx)
	p := &inProcessProcess{
		pid:    l.pid,
		cancel: cancel,
		done:   make(chan workerDone, 1),
	}
	go func() {
		result, err := l.run(childCtx, req)
		p.done <- workerDone{result: result, err: err}
	}()
	return p, nil
}

type workerDone struct {
	result WorkerResult
	err    error
}

type inProcessProcess struct {
	pid    int
	cancel context.CancelFunc
	once   sync.Once
	done   chan workerDone
}

func (p *inProcessProcess) PID() int {
	if p == nil {
		return 0
	}
	return p.pid
}

func (p *inProcessProcess) Wait() (WorkerResult, error) {
	if p == nil {
		return WorkerResult{ExitCode: 1}, fmt.Errorf("subagent process is nil")
	}
	done := <-p.done
	p.once.Do(p.cancel)
	return done.result, done.err
}

func (p *inProcessProcess) Stop() error {
	if p == nil {
		return nil
	}
	p.once.Do(p.cancel)
	return nil
}
