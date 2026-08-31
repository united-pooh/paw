package task

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	coremcp "paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/tokentracer"
	"strings"
	"sync"

	"github.com/sourcegraph/conc"
)

type WorkerRequest struct {
	TaskID          string               `json:"task_id"`
	SessionID       string               `json:"session_id"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	ParentTurnID    string               `json:"parent_turn_id,omitempty"`
	ParentTaskID    string               `json:"parent_task_id,omitempty"`
	Prompt          string               `json:"prompt"`
	SystemPrompt    string               `json:"system_prompt,omitempty"`
	Description     string               `json:"description,omitempty"`
	DisableTools    bool                 `json:"disable_tools,omitempty"`
	Streaming       bool                 `json:"streaming,omitempty"`
	BootstrapOnly   bool                 `json:"bootstrap_only,omitempty"`
	ContextMode     settings.ContextMode `json:"context_mode"`
	RunMode         settings.RunMode     `json:"run_mode"`
	Depth           int                  `json:"depth"`
	MaxDepth        int                  `json:"max_depth"`
	MCPSnapshot     coremcp.Snapshot     `json:"mcp_snapshot,omitempty"`
}

type WorkerResult struct {
	TaskID     string             `json:"task_id"`
	SessionID  string             `json:"session_id"`
	Content    string             `json:"content,omitempty"`
	Error      string             `json:"error,omitempty"`
	ExitCode   int                `json:"exit_code"`
	UsedTokens int                `json:"used_tokens,omitempty"`
	Usage      *tokentracer.Usage `json:"usage,omitempty"`
}

type WorkerStreamEvent struct {
	Delta    string       `json:"delta,omitempty"`
	Thinking string       `json:"thinking,omitempty"`
	Done     bool         `json:"done,omitempty"`
	Error    string       `json:"error,omitempty"`
	Usage    *model.Usage `json:"usage,omitempty"`
}

const workerProtocolV2 = 2

type Launcher interface {
	Start(ctx context.Context, req WorkerRequest) (Process, error)
}

type Process interface {
	PID() int
	Wait() (WorkerResult, error)
	Stop() error
}

type ProcessPartialResultSource interface {
	PartialResult() WorkerResult
}

type ProcessCauseStopper interface {
	StopWithCause(error) error
}

// WorkerRoleSource 由能识别执行 worker 角色的进程实现（进程池具名 worker）。
// 返回空名字表示没有具名 worker，调用方回落进程内角色分配。
type WorkerRoleSource interface {
	WorkerRole() (name, color string)
}

type ProcessLauncher struct {
	mu      sync.RWMutex
	Command string
	Args    []string
	Dir     string
	Env     []string
	Broker  coremcp.Broker
}

func NewProcessLauncher(command, dir string) *ProcessLauncher {
	return &ProcessLauncher{
		Command: command,
		Args:    []string{"--task-worker"},
		Dir:     dir,
	}
}

func (l *ProcessLauncher) SetMCPBroker(broker coremcp.Broker) {
	if l != nil {
		l.mu.Lock()
		l.Broker = broker
		l.mu.Unlock()
	}
}

func (l *ProcessLauncher) SetDangerousMode(enabled bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	args := make([]string, 0, len(l.Args)+1)
	for _, arg := range l.Args {
		if arg != "--dangerously" && arg != "--yolo" {
			args = append(args, arg)
		}
	}
	if enabled {
		args = append(args, "--dangerously")
	}
	l.Args = args
}

func (l *ProcessLauncher) commandConfig() (args, env []string, dir string, broker coremcp.Broker) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.Args...), append([]string(nil), l.Env...), l.Dir, l.Broker
}

func (l *ProcessLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	if l == nil {
		return nil, fmt.Errorf("task process launcher is nil")
	}
	l.mu.RLock()
	command := strings.TrimSpace(l.Command)
	l.mu.RUnlock()
	if command == "" {
		return nil, fmt.Errorf("task worker command is empty")
	}
	args, env, dir, broker := l.commandConfig()
	if broker != nil {
		return l.startFramed(ctx, req, command, args, env, dir, broker)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal worker request: %w", err)
	}
	payload = append(payload, '\n')

	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = bytes.NewReader(payload)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start task worker: %w", err)
	}
	return &execProcess{cmd: cmd, cancel: cancel, stdout: &stdout, stderr: &stderr}, nil
}

func (l *ProcessLauncher) startFramed(ctx context.Context, req WorkerRequest, command string, args, env []string, dir string, broker coremcp.Broker) (Process, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create task worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("create task worker stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, fmt.Errorf("create task worker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		cancel()
		return nil, fmt.Errorf("start task worker: %w", err)
	}
	p := &framedProcess{
		cmd:        cmd,
		cancel:     cancel,
		ctx:        childCtx,
		stdin:      stdin,
		stdout:     stdout,
		stderr:     &bytes.Buffer{},
		broker:     broker,
		readDone:   make(chan struct{}),
		resultDone: make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(p.stderr, stderr)
		_ = stderr.Close()
	}()
	go p.readLoop()
	if err := p.send(NewWorkerStartMessage(req, broker.Snapshot())); err != nil {
		_ = p.Stop()
		return nil, err
	}
	return p, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

const (
	workerMessageHello     = "worker.hello"
	workerMessageReady     = "worker.ready"
	workerMessageStart     = "worker.start"
	workerMessageEvent     = "worker.event"
	workerMessageMCPCall   = "mcp.call"
	workerMessageMCPResult = "mcp.result"
	workerMessageSnapshot  = "mcp.snapshot"
	workerMessageResult    = "worker.result"
	workerMessageCancel    = "worker.cancel"
	workerMessageShutdown  = "worker.shutdown"
)

// workerMessage is a newline-delimited envelope shared by the parent worker
// launcher and the child process. WorkerRequest/WorkerResult fields are
// flattened to keep the wire format simple and forward-compatible.
type workerMessage struct {
	Protocol  int                `json:"protocol,omitempty"`
	Seq       uint64             `json:"seq,omitempty"`
	Type      string             `json:"type"`
	RequestID string             `json:"request_id,omitempty"`
	Tool      string             `json:"tool,omitempty"`
	Input     json.RawMessage    `json:"input,omitempty"`
	Content   string             `json:"content,omitempty"`
	Error     string             `json:"error,omitempty"`
	Snapshot  coremcp.Snapshot   `json:"snapshot,omitempty"`
	Event     *WorkerStreamEvent `json:"event,omitempty"`

	TaskID          string               `json:"task_id,omitempty"`
	SessionID       string               `json:"session_id,omitempty"`
	ParentSessionID string               `json:"parent_session_id,omitempty"`
	ParentTurnID    string               `json:"parent_turn_id,omitempty"`
	ParentTaskID    string               `json:"parent_task_id,omitempty"`
	Prompt          string               `json:"prompt,omitempty"`
	SystemPrompt    string               `json:"system_prompt,omitempty"`
	Description     string               `json:"description,omitempty"`
	DisableTools    bool                 `json:"disable_tools,omitempty"`
	Streaming       bool                 `json:"streaming,omitempty"`
	BootstrapOnly   bool                 `json:"bootstrap_only,omitempty"`
	ContextMode     settings.ContextMode `json:"context_mode,omitempty"`
	RunMode         settings.RunMode     `json:"run_mode,omitempty"`
	Depth           int                  `json:"depth,omitempty"`
	MaxDepth        int                  `json:"max_depth,omitempty"`

	ExitCode   int                `json:"exit_code,omitempty"`
	UsedTokens int                `json:"used_tokens,omitempty"`
	Usage      *tokentracer.Usage `json:"usage,omitempty"`
}

// WorkerMessage is the public wire envelope used by the agent entrypoint to
// bootstrap a worker and exchange brokered MCP calls.
type WorkerMessage = workerMessage

const (
	WorkerMessageHello     = workerMessageHello
	WorkerMessageReady     = workerMessageReady
	WorkerMessageStart     = workerMessageStart
	WorkerMessageEvent     = workerMessageEvent
	WorkerMessageMCPCall   = workerMessageMCPCall
	WorkerMessageMCPResult = workerMessageMCPResult
	WorkerMessageSnapshot  = workerMessageSnapshot
	WorkerMessageResult    = workerMessageResult
	WorkerMessageCancel    = workerMessageCancel
	WorkerMessageShutdown  = workerMessageShutdown
)

func NewWorkerStartMessage(req WorkerRequest, snapshot coremcp.Snapshot) WorkerMessage {
	return workerMessage{
		Type:            workerMessageStart,
		Snapshot:        snapshot.Clone(),
		TaskID:          req.TaskID,
		SessionID:       req.SessionID,
		ParentSessionID: req.ParentSessionID,
		ParentTurnID:    req.ParentTurnID,
		ParentTaskID:    req.ParentTaskID,
		Prompt:          req.Prompt,
		Description:     req.Description,
		DisableTools:    req.DisableTools,
		ContextMode:     req.ContextMode,
		RunMode:         req.RunMode,
		Depth:           req.Depth,
		MaxDepth:        req.MaxDepth,
	}
}

func (m workerMessage) Request() WorkerRequest {
	return WorkerRequest{
		TaskID:          m.TaskID,
		SessionID:       m.SessionID,
		ParentSessionID: m.ParentSessionID,
		ParentTurnID:    m.ParentTurnID,
		ParentTaskID:    m.ParentTaskID,
		Prompt:          m.Prompt,
		Description:     m.Description,
		DisableTools:    m.DisableTools,
		ContextMode:     m.ContextMode,
		RunMode:         m.RunMode,
		Depth:           m.Depth,
		MaxDepth:        m.MaxDepth,
		MCPSnapshot:     m.Snapshot.Clone(),
	}
}

func NewWorkerEventMessage(taskID string, event WorkerStreamEvent) WorkerMessage {
	copied := event
	return workerMessage{Type: workerMessageEvent, TaskID: taskID, Event: &copied}
}

func NewWorkerResultMessage(result WorkerResult) WorkerMessage {
	return workerMessage{
		Type:       workerMessageResult,
		TaskID:     result.TaskID,
		SessionID:  result.SessionID,
		Content:    result.Content,
		Error:      result.Error,
		ExitCode:   result.ExitCode,
		UsedTokens: result.UsedTokens,
		Usage:      result.Usage,
	}
}

// NewWorkerReadyMessage 是池 worker 对 parent 的 Hello 的应答，表示该 worker
// 已就绪、可接收 start 任务。
func NewWorkerReadyMessage() WorkerMessage {
	return workerMessage{Protocol: workerProtocolV2, Type: workerMessageReady}
}

func (m workerMessage) Result() WorkerResult {
	return WorkerResult{
		TaskID:     m.TaskID,
		SessionID:  m.SessionID,
		Content:    m.Content,
		Error:      m.Error,
		ExitCode:   m.ExitCode,
		UsedTokens: m.UsedTokens,
		Usage:      m.Usage,
	}
}

type framedProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	ctx    context.Context
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *bytes.Buffer
	broker coremcp.Broker

	writeMu    sync.Mutex
	readDone   chan struct{}
	resultDone chan struct{}
	resultMu   sync.RWMutex
	result     WorkerResult
	resultErr  error
	gotResult  bool
	waitOnce   sync.Once
}

func (p *framedProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *framedProcess) send(message workerMessage) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if p.stdin == nil {
		return fmt.Errorf("task worker stdin is closed")
	}
	if err := json.NewEncoder(p.stdin).Encode(message); err != nil {
		return fmt.Errorf("write task worker message: %w", err)
	}
	return nil
}

func (p *framedProcess) readLoop() {
	defer close(p.readDone)
	defer close(p.resultDone)
	scanner := bufio.NewScanner(p.stdout)
	for scanner.Scan() {
		var message workerMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			p.setResultError(fmt.Errorf("decode task worker message: %w", err))
			return
		}
		switch message.Type {
		case workerMessageMCPCall:
			go p.handleMCPCall(message)
		case workerMessageResult:
			p.resultMu.Lock()
			p.result = message.Result()
			p.gotResult = true
			p.resultMu.Unlock()
		case workerMessageCancel:
			p.cancel()
		}
	}
	if err := scanner.Err(); err != nil {
		p.setResultError(fmt.Errorf("read task worker output: %w", err))
	} else {
		p.resultMu.Lock()
		if !p.gotResult && p.resultErr == nil {
			p.resultErr = errors.New("task worker exited without worker.result")
		}
		p.resultMu.Unlock()
	}
}

func (p *framedProcess) handleMCPCall(message workerMessage) {
	result := workerMessage{Type: workerMessageMCPResult, RequestID: message.RequestID}
	if p.broker == nil {
		result.Error = "MCP broker is unavailable"
	} else {
		content, err := p.broker.Call(p.ctx, message.Tool, message.Input)
		result.Content = content
		if err != nil {
			result.Error = err.Error()
		}
	}
	_ = p.send(result)
}

func (p *framedProcess) setResultError(err error) {
	p.resultMu.Lock()
	if p.resultErr == nil {
		p.resultErr = err
	}
	p.resultMu.Unlock()
}

func (p *framedProcess) Wait() (WorkerResult, error) {
	if p == nil || p.cmd == nil {
		return WorkerResult{ExitCode: 1}, fmt.Errorf("task process is nil")
	}
	p.waitOnce.Do(func() {
		<-p.resultDone
		waitErr := p.cmd.Wait()
		if p.cancel != nil {
			p.cancel()
		}
		p.resultMu.Lock()
		defer p.resultMu.Unlock()
		if waitErr != nil && p.result.ExitCode == 0 {
			p.result.ExitCode = exitCodeFromError(waitErr)
		}
		if p.result.Error == "" && p.resultErr != nil {
			p.result.Error = p.resultErr.Error()
		}
		if waitErr != nil && p.result.Error == "" {
			p.result.Error = strings.TrimSpace(p.stderr.String())
			if p.result.Error == "" {
				p.result.Error = waitErr.Error()
			}
		}
		if p.result.Error != "" && p.result.ExitCode == 0 {
			p.result.ExitCode = 1
		}
	})
	p.resultMu.RLock()
	result, resultErr := p.result, p.resultErr
	p.resultMu.RUnlock()
	if resultErr != nil {
		return result, resultErr
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func (p *framedProcess) Stop() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
			return err
		}
	}
	return nil
}

func (p *framedProcess) UpdateMCPSnapshot(snapshot coremcp.Snapshot) error {
	if p == nil {
		return fmt.Errorf("task process is nil")
	}
	return p.send(workerMessage{Type: workerMessageSnapshot, Snapshot: snapshot.Clone()})
}

func (p *execProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *execProcess) Wait() (WorkerResult, error) {
	if p == nil || p.cmd == nil {
		return WorkerResult{ExitCode: 1}, fmt.Errorf("task process is nil")
	}
	err := p.cmd.Wait()
	if p.cancel != nil {
		p.cancel()
	}

	var result WorkerResult
	output := bytes.TrimSpace(p.stdout.Bytes())
	if len(output) > 0 {
		if decodeErr := json.Unmarshal(output, &result); decodeErr != nil {
			return WorkerResult{ExitCode: exitCodeFromError(err)}, fmt.Errorf("decode task worker output: %w: %s", decodeErr, string(output))
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
		return result, fmt.Errorf("task worker failed: %s", detail)
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
		return nil, fmt.Errorf("task in-process launcher is nil")
	}
	childCtx, cancel := context.WithCancel(ctx)
	p := &inProcessProcess{
		pid:       l.pid,
		cancel:    cancel,
		taskID:    req.TaskID,
		sessionID: req.SessionID,
		done:      make(chan workerDone, 1),
		waitGroup: conc.NewWaitGroup(),
	}
	p.waitGroup.Go(func() {
		result, err := l.run(childCtx, req)
		p.done <- workerDone{result: result, err: err}
	})
	return p, nil
}

type workerDone struct {
	result WorkerResult
	err    error
}

type inProcessProcess struct {
	pid       int
	cancel    context.CancelFunc
	taskID    string
	sessionID string
	done      chan workerDone
	waitGroup *conc.WaitGroup

	waitOnce sync.Once
	mu       sync.RWMutex
	result   WorkerResult
	err      error
}

func (p *inProcessProcess) PID() int {
	if p == nil {
		return 0
	}
	return p.pid
}

func (p *inProcessProcess) Wait() (WorkerResult, error) {
	if p == nil {
		return WorkerResult{ExitCode: 1}, fmt.Errorf("task process is nil")
	}
	p.waitOnce.Do(func() {
		recovered := p.waitGroup.WaitAndRecover()
		if p.cancel != nil {
			p.cancel()
		}

		p.mu.Lock()
		defer p.mu.Unlock()
		if recovered != nil {
			p.result = WorkerResult{
				TaskID:    p.taskID,
				SessionID: p.sessionID,
				Error:     recovered.AsError().Error(),
				ExitCode:  1,
			}
			p.err = recovered.AsError()
			return
		}
		done := <-p.done
		p.result = done.result
		p.err = done.err
	})
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.result, p.err
}

func (p *inProcessProcess) Stop() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}
