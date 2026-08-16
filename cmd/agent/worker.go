package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	coremcp "paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/subagent"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	toolmcp "paw/internal/tool/mcp"
	uiiface "paw/internal/ui"
	"paw/internal/ui/headless"
	"strings"
	"sync"
	"sync/atomic"
)

func runSubagentWorkerMode(ctx context.Context, input io.Reader, output io.Writer, allowOutsideRead bool, sandboxLimits subagent.SandboxLimits) error {
	if err := subagent.ApplyWorkerResourceLimits(sandboxLimits); err != nil {
		return fmt.Errorf("apply worker resource limits: %w", err)
	}
	decoder := json.NewDecoder(input)
	start, req, _, err := readSubagentWorkerStart(decoder)
	if err != nil {
		return err
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	broker := newWorkerMCPBroker(workerCtx, start.Snapshot, output)
	workerDone := make(chan subagent.WorkerResult, 1)
	go func() {
		workerDone <- runWorkerTurn(workerCtx, req, broker, allowOutsideRead)
	}()

	messages := make(chan subagent.WorkerMessage)
	decodeErr := make(chan error, 1)
	go func() {
		for {
			var message subagent.WorkerMessage
			if err := decoder.Decode(&message); err != nil {
				decodeErr <- err
				close(messages)
				return
			}
			messages <- message
		}
	}()
	for {
		select {
		case result := <-workerDone:
			return broker.send(subagent.NewWorkerResultMessage(result))
		case message, ok := <-messages:
			if !ok {
				cancel()
				return <-decodeErr
			}
			switch message.Type {
			case subagent.WorkerMessageMCPResult:
				broker.resolve(message.RequestID, message.Content, message.Error)
			case subagent.WorkerMessageSnapshot:
				broker.Update(message.Snapshot)
			case subagent.WorkerMessageCancel:
				cancel()
			}
		case err := <-decodeErr:
			cancel()
			return fmt.Errorf("read subagent worker input: %w", err)
		}
	}
}

func readSubagentWorkerStart(decoder *json.Decoder) (subagent.WorkerMessage, subagent.WorkerRequest, subagentRuntimeContext, error) {
	var start subagent.WorkerMessage
	if err := decoder.Decode(&start); err != nil {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, fmt.Errorf("decode subagent worker.start: %w", err)
	}
	if start.Type != subagent.WorkerMessageStart {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, fmt.Errorf("subagent worker.start is required")
	}
	req := start.Request()
	if err := validateWorkerRequest(req); err != nil {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, err
	}
	return start, req, subagentRuntimeContext{
		workerMode:      true,
		depth:           req.Depth,
		maxDepth:        req.MaxDepth,
		parentTaskID:    req.TaskID,
		disableMainTodo: true,
	}, nil
}

func validateWorkerRequest(req subagent.WorkerRequest) error {
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("subagent worker session_id is required")
	}
	if req.MaxDepth < 1 {
		return fmt.Errorf("subagent worker max_depth must be at least 1: %d", req.MaxDepth)
	}
	if req.Depth < 1 || req.Depth > req.MaxDepth {
		return fmt.Errorf("subagent worker depth must satisfy 1 <= depth <= max_depth: depth=%d max_depth=%d", req.Depth, req.MaxDepth)
	}
	return nil
}

// runWorkerTurn 执行单个 worker 任务：为任务构建独立的 runner/会话/工具注册表/
// 上下文/broker，运行一轮后返回结果。池 worker 与单 worker 共用此逻辑，确保
// 每次任务之间的运行时状态完全隔离（仅 model.Client 在池内按需复用）。
func runWorkerTurn(ctx context.Context, req subagent.WorkerRequest, broker *workerMCPBroker, allowOutsideRead bool) subagent.WorkerResult {
	workerUI := &workerUsageUI{UI: headless.New(io.Discard)}
	runner, sessionID, _, configController, _, _, _, _, err := buildRunnerWithSubagentContext(ctx, req.SessionID, workerUI, allowOutsideRead, false, subagentRuntimeContext{
		workerMode:      true,
		depth:           req.Depth,
		maxDepth:        req.MaxDepth,
		parentTaskID:    req.TaskID,
		disableMainTodo: true,
		mcpBroker:       broker,
	})
	result := subagent.WorkerResult{TaskID: req.TaskID, SessionID: sessionID, ExitCode: 0}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		return result
	}
	defer func() { _ = configController.Close() }()
	broker.SetUpdateHandler(func(snapshot coremcp.Snapshot) {
		adapted := toolmcp.NewSnapshotTools(snapshot, broker)
		tools := make([]tool.Tool, 0, len(adapted))
		for _, item := range adapted {
			tools = append(tools, item)
		}
		_ = runner.ReplaceToolNamespace("mcp", tools)
	})
	broker.Update(broker.Snapshot())
	msg, runErr := runner.RunTurn(ctx, req.Prompt)
	result.UsedTokens = runner.ContextStats(1<<30, "").UsedTokens
	result.Usage = workerUI.Usage()
	if runErr != nil {
		result.Error = runErr.Error()
		result.ExitCode = 1
	} else {
		result.Content = strings.TrimSpace(msg.Content)
	}
	return result
}

// lockedWriter 串行化对共享输出（worker stdout）的写入。MCP 调用应答与任务结果
// 都写同一管道，跨任务的写入不能交错形成非法 JSONL。
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	if lw == nil || lw.w == nil {
		return 0, io.ErrClosedPipe
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

type poolWorkerInput struct {
	msg subagent.WorkerMessage
	err error
	ok  bool
}

// runSubagentPoolWorkerMode 是 ProcessPoolLauncher 生成的长驻 worker 入口。
// 协议：parent 发送 hello → 本 worker 应答 ready；随后循环接收 start 任务，
// 每任务独立执行并回报 result；收到 cancel/shutdown 或 stdin EOF 时退出。
// 单个 worker 同时只处理一个任务（parent 侧串行调度，保证每任务隔离）。
func runSubagentPoolWorkerMode(ctx context.Context, input io.Reader, output io.Writer, allowOutsideRead bool, sandboxLimits subagent.SandboxLimits) error {
	if err := subagent.ApplyWorkerResourceLimits(sandboxLimits); err != nil {
		return fmt.Errorf("apply worker resource limits: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	decoder := json.NewDecoder(input)

	var hello subagent.WorkerMessage
	if err := decoder.Decode(&hello); err != nil {
		return fmt.Errorf("decode subagent pool worker hello: %w", err)
	}
	if hello.Type != subagent.WorkerMessageHello {
		return fmt.Errorf("subagent pool worker hello is required")
	}
	shared := &lockedWriter{w: output}
	if err := json.NewEncoder(shared).Encode(subagent.NewWorkerReadyMessage()); err != nil {
		return fmt.Errorf("send subagent pool worker ready: %w", err)
	}

	jobCtx := ctx
	jobCancel := func() {}
	var current *workerMCPBroker

	msgCh := make(chan poolWorkerInput, 4)
	go func() {
		for {
			var message subagent.WorkerMessage
			if err := decoder.Decode(&message); err != nil {
				msgCh <- poolWorkerInput{err: err, ok: false}
				return
			}
			msgCh <- poolWorkerInput{msg: message, ok: true}
		}
	}()

	for {
		select {
		case in := <-msgCh:
			if !in.ok {
				jobCancel()
				if errors.Is(in.err, io.EOF) {
					return nil
				}
				return fmt.Errorf("read subagent pool worker input: %w", in.err)
			}
			switch in.msg.Type {
			case subagent.WorkerMessageStart:
				req := in.msg.Request()
				if err := validateWorkerRequest(req); err != nil {
					_ = json.NewEncoder(shared).Encode(subagent.NewWorkerResultMessage(subagent.WorkerResult{
						TaskID: req.TaskID, SessionID: req.SessionID, Error: err.Error(), ExitCode: 1,
					}))
					continue
				}
				jobCancel()
				jobCtx, jobCancel = context.WithCancel(ctx)
				broker := newWorkerMCPBroker(jobCtx, req.MCPSnapshot, shared)
				current = broker
				go poolWorkerRunJob(jobCtx, req, shared, broker, allowOutsideRead)
			case subagent.WorkerMessageMCPResult:
				if current != nil {
					current.resolve(in.msg.RequestID, in.msg.Content, in.msg.Error)
				}
			case subagent.WorkerMessageSnapshot:
				if current != nil {
					current.Update(in.msg.Snapshot)
				}
			case subagent.WorkerMessageCancel:
				jobCancel()
			case subagent.WorkerMessageShutdown:
				jobCancel()
				return nil
			}
		}
	}
}

func poolWorkerRunJob(ctx context.Context, req subagent.WorkerRequest, output io.Writer, broker *workerMCPBroker, allowOutsideRead bool) {
	result := runWorkerTurn(ctx, req, broker, allowOutsideRead)
	_ = json.NewEncoder(output).Encode(subagent.NewWorkerResultMessage(result))
}

type workerMCPBroker struct {
	ctx      context.Context
	mu       sync.RWMutex
	snapshot coremcp.Snapshot
	output   io.Writer
	writeMu  sync.Mutex
	pending  map[string]chan workerMCPResult
	sequence uint64
	onUpdate func(coremcp.Snapshot)
}

type workerMCPResult struct {
	content string
	err     string
}

func newWorkerMCPBroker(ctx context.Context, snapshot coremcp.Snapshot, output io.Writer) *workerMCPBroker {
	return &workerMCPBroker{
		ctx:      ctx,
		snapshot: snapshot.Clone(),
		output:   output,
		pending:  make(map[string]chan workerMCPResult),
	}
}

func (b *workerMCPBroker) Snapshot() coremcp.Snapshot {
	if b == nil {
		return coremcp.Snapshot{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshot.Clone()
}

func (b *workerMCPBroker) Update(snapshot coremcp.Snapshot) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.snapshot = snapshot.Clone()
	onUpdate := b.onUpdate
	b.mu.Unlock()
	if onUpdate != nil {
		onUpdate(snapshot)
	}
}

func (b *workerMCPBroker) SetUpdateHandler(handler func(coremcp.Snapshot)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.onUpdate = handler
	b.mu.Unlock()
}

func (b *workerMCPBroker) Call(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if b == nil {
		return "", fmt.Errorf("MCP worker broker is nil")
	}
	if ctx == nil {
		ctx = b.ctx
	}
	b.mu.RLock()
	found := false
	for _, item := range b.snapshot.Tools {
		if item.Name == name {
			found = true
			break
		}
	}
	b.mu.RUnlock()
	if !found {
		return "", fmt.Errorf("MCP tool %q is not in the parent snapshot", name)
	}
	sequence := atomic.AddUint64(&b.sequence, 1)
	requestID := fmt.Sprintf("mcp-%d", sequence)
	result := make(chan workerMCPResult, 1)
	b.mu.Lock()
	b.pending[requestID] = result
	b.mu.Unlock()
	if err := b.send(subagent.WorkerMessage{
		Type:      subagent.WorkerMessageMCPCall,
		RequestID: requestID,
		Tool:      name,
		Input:     append(json.RawMessage(nil), input...),
	}); err != nil {
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return "", err
	}
	select {
	case response := <-result:
		if response.err != "" {
			return response.content, fmt.Errorf("MCP parent call failed: %s", response.err)
		}
		return response.content, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return "", ctx.Err()
	case <-b.ctx.Done():
		return "", b.ctx.Err()
	}
}

func (b *workerMCPBroker) resolve(requestID, content, errText string) {
	b.mu.Lock()
	response, ok := b.pending[requestID]
	if ok {
		delete(b.pending, requestID)
	}
	b.mu.Unlock()
	if ok {
		response <- workerMCPResult{content: content, err: errText}
	}
}

func (b *workerMCPBroker) send(message subagent.WorkerMessage) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.output == nil {
		return fmt.Errorf("subagent worker output is nil")
	}
	if err := json.NewEncoder(b.output).Encode(message); err != nil {
		return fmt.Errorf("write subagent worker message: %w", err)
	}
	return nil
}

type workerUsageUI struct {
	uiiface.UI

	mu    sync.RWMutex
	usage *tokentracer.Usage
}

func (u *workerUsageUI) OnModelUsage(usage model.Usage) {
	structured := tokentracer.UsageFromModelUsage(usage)
	if structured.Empty() {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.usage = &structured
}

func (u *workerUsageUI) Usage() *tokentracer.Usage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.usage == nil {
		return nil
	}
	copied := *u.usage
	return &copied
}
