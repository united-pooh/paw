package main

import (
	"context"
	"encoding/json"
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

func runSubagentWorkerMode(ctx context.Context, input io.Reader, output io.Writer, allowOutsideRead bool) error {
	decoder := json.NewDecoder(input)
	start, req, subCtx, err := readSubagentWorkerStart(decoder)
	if err != nil {
		return err
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	broker := newWorkerMCPBroker(workerCtx, start.Snapshot, output)
	subCtx.mcpBroker = broker
	workerDone := make(chan subagent.WorkerResult, 1)
	go func() {
		workerUI := &workerUsageUI{UI: headless.New(io.Discard)}
		runner, sessionID, _, configController, _, _, _, _, err := buildRunnerWithSubagentContext(workerCtx, req.SessionID, workerUI, allowOutsideRead, false, subCtx)
		result := subagent.WorkerResult{TaskID: req.TaskID, SessionID: sessionID, ExitCode: 0}
		if err != nil {
			result.Error = err.Error()
			result.ExitCode = 1
			workerDone <- result
			return
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
		msg, err := runner.RunTurn(workerCtx, req.Prompt)
		result.UsedTokens = runner.ContextStats(1<<30, "").UsedTokens
		result.Usage = workerUI.Usage()
		if err != nil {
			result.Error = err.Error()
			result.ExitCode = 1
		} else {
			result.Content = strings.TrimSpace(msg.Content)
		}
		workerDone <- result
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
	if strings.TrimSpace(req.SessionID) == "" {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, fmt.Errorf("subagent worker session_id is required")
	}
	if req.MaxDepth < 1 {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, fmt.Errorf("subagent worker max_depth must be at least 1: %d", req.MaxDepth)
	}
	if req.Depth < 1 || req.Depth > req.MaxDepth {
		return subagent.WorkerMessage{}, subagent.WorkerRequest{}, subagentRuntimeContext{}, fmt.Errorf("subagent worker depth must satisfy 1 <= depth <= max_depth: depth=%d max_depth=%d", req.Depth, req.MaxDepth)
	}
	return start, req, subagentRuntimeContext{
		workerMode:      true,
		depth:           req.Depth,
		maxDepth:        req.MaxDepth,
		parentTaskID:    req.TaskID,
		disableMainTodo: true,
	}, nil
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
