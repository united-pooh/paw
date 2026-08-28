package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"paw/internal/message"
	"paw/internal/tool"
	"paw/internal/ui"
	"strings"
	"sync"
	"time"
)

type toolResultCheckpoint func(callIndex int, result message.ToolResult) error

type resolvedToolCall struct {
	call             message.ToolCall
	selectedTool     tool.Tool
	resolveError     string
	approvedReadPath string
	permissionDenied string
	// argsGenStartedAt 是工具参数开始流式生成的时刻（无流式窗口时为零值）；
	// execStartedAt/execFinishedAt 是纯执行段起止。三者共同支撑「参数生成
	// →执行完成」的合并耗时展示与 tracer 阶段统计。
	argsGenStartedAt time.Time
	execStartedAt    time.Time
	execFinishedAt   time.Time
}

func (runner *Engine) resolveToolCall(call message.ToolCall) resolvedToolCall {
	resolved := resolvedToolCall{call: call}
	if runner.registry == nil {
		resolved.resolveError = "tool registry is nil"
		return resolved
	}
	selected, ok := runner.registry.Get(call.Name)
	if !ok {
		resolved.resolveError = fmt.Sprintf("unknown tool: %s", call.Name)
		return resolved
	}
	resolved.selectedTool = selected
	if filter := runner.currentToolFilter(); filter != nil {
		if err := filter(selected.Name(), call.Input); err != nil {
			resolved.resolveError = err.Error()
			return resolved
		}
	}
	if strings.TrimSpace(call.InputError) != "" {
		resolved.resolveError = call.InputError
	}
	return resolved
}

func (runner *Engine) runToolCalls(ctx context.Context, calls []message.ToolCall) (message.Message, error) {
	return runner.runToolCallsWithCheckpoint(ctx, calls, nil)
}

func (runner *Engine) runToolCallsWithCheckpoint(ctx context.Context, calls []message.ToolCall, checkpoint toolResultCheckpoint) (message.Message, error) {
	return runner.runToolCallsWithPhases(ctx, calls, checkpoint, nil, nil)
}

// runToolCallsWithPhases 在执行之外附带耗时阶段 tracking：argsGen 提供每个
// 调用的参数生成起点（可无），totals 累加本批调用的阶段耗时（可无）。
func (runner *Engine) runToolCallsWithPhases(ctx context.Context, calls []message.ToolCall, checkpoint toolResultCheckpoint, argsGen map[string]time.Time, totals *ToolPhaseTotals) (message.Message, error) {
	resolvedCalls := make([]resolvedToolCall, len(calls))
	for i := range calls {
		resolvedCalls[i] = runner.resolveToolCall(calls[i])
		if started, ok := argsGen[calls[i].ID]; ok {
			resolvedCalls[i].argsGenStartedAt = started
		}
	}
	if err := runner.preflightToolPermissions(ctx, resolvedCalls); err != nil {
		return message.Message{}, err
	}

	results := make([]message.ToolResult, 0, len(calls))
	for start := 0; start < len(calls); {
		resolvedStart := resolvedCalls[start]
		if isToolCallConcurrencySafe(resolvedStart) {
			end := start + 1
			for end < len(calls) && isToolCallConcurrencySafe(resolvedCalls[end]) {
				end++
			}
			batchResults, err := runner.runToolCallBatch(ctx, resolvedCalls[start:end], start, checkpoint, totals)
			if err != nil {
				return message.Message{}, err
			}
			results = append(results, batchResults...)
			start = end
			continue
		}

		result, finished, err := runner.runResolvedToolCallWithCheckpoint(ctx, resolvedStart, start, checkpoint)
		if err != nil {
			return message.Message{}, err
		}
		accumulateToolPhaseTotals(totals, finished)
		results = append(results, result)
		start++
	}
	return buildToolResultsMessage(results), nil
}

func (runner *Engine) runToolCallBatch(ctx context.Context, calls []resolvedToolCall, offset int, checkpoint toolResultCheckpoint, totals *ToolPhaseTotals) ([]message.ToolResult, error) {
	execStartedAt := runner.now()
	captures := make([]*fileMutationCapture, len(calls))
	for i, resolved := range calls {
		calls[i].execStartedAt = execStartedAt
		captures[i] = runner.prepareFileMutation(resolved)
		if err := runner.emitToolCall(calls[i], captures[i]); err != nil {
			return nil, err
		}
	}
	for i := range calls {
		if err := runner.recordToolStarted(ctx, calls[i], offset+i); err != nil {
			return nil, err
		}
	}

	results := make([]message.ToolResult, len(calls))
	mutations := make([]*ui.FileMutationSnapshot, len(calls))
	execFinished := make([]time.Time, len(calls))
	var checkpointMu sync.Mutex
	var checkpointErr error
	var wg sync.WaitGroup
	for i := range calls {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runner.executeResolvedToolCall(ctx, calls[i])
			execFinished[i] = runner.now()
			mutations[i] = completeFileMutationCapture(captures[i], results[i])
			if checkpoint != nil {
				if err := checkpoint(offset+i, results[i]); err != nil {
					checkpointMu.Lock()
					if checkpointErr == nil {
						checkpointErr = err
					}
					checkpointMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if checkpointErr != nil {
		return nil, checkpointErr
	}

	for i, result := range results {
		resolved := calls[i]
		resolved.execFinishedAt = execFinished[i]
		if err := runner.emitToolResult(resolved, result, mutations[i]); err != nil {
			return nil, err
		}
		accumulateToolPhaseTotals(totals, resolved)
	}
	return results, nil
}

func (runner *Engine) runToolCall(ctx context.Context, call message.ToolCall) (message.ToolResult, error) {
	return runner.runToolCallWithCheckpoint(ctx, call, 0, nil)
}

func (runner *Engine) runToolCallWithCheckpoint(ctx context.Context, call message.ToolCall, callIndex int, checkpoint toolResultCheckpoint) (message.ToolResult, error) {
	result, _, err := runner.runResolvedToolCallWithCheckpoint(ctx, runner.resolveToolCall(call), callIndex, checkpoint)
	return result, err
}

// runResolvedToolCallWithCheckpoint 执行单个工具调用并返回带耗时阶段标记的
// resolved 副本（参数生成/执行起止），供调用方聚合 turn 级阶段耗时。
func (runner *Engine) runResolvedToolCallWithCheckpoint(ctx context.Context, resolved resolvedToolCall, callIndex int, checkpoint toolResultCheckpoint) (message.ToolResult, resolvedToolCall, error) {
	resolved.execStartedAt = runner.now()
	capture := runner.prepareFileMutation(resolved)
	if err := runner.emitToolCall(resolved, capture); err != nil {
		return message.ToolResult{}, resolved, err
	}
	if err := runner.recordToolStarted(ctx, resolved, callIndex); err != nil {
		return message.ToolResult{}, resolved, err
	}

	result := runner.executeResolvedToolCall(ctx, resolved)
	resolved.execFinishedAt = runner.now()
	mutation := completeFileMutationCapture(capture, result)
	if checkpoint != nil {
		if err := checkpoint(callIndex, result); err != nil {
			return message.ToolResult{}, resolved, err
		}
	}
	if err := runner.emitToolResult(resolved, result, mutation); err != nil {
		return message.ToolResult{}, resolved, err
	}

	return result, resolved, nil
}

// accumulateToolPhaseTotals 把单个工具调用的阶段耗时累加进 turn 聚合：
// 参数生成段（args_gen）与纯执行段（exec），无流式窗口时 args_gen 为 0。
func accumulateToolPhaseTotals(totals *ToolPhaseTotals, resolved resolvedToolCall) {
	if totals == nil || resolved.execStartedAt.IsZero() || resolved.execFinishedAt.IsZero() {
		return
	}
	totals.Calls++
	if !resolved.argsGenStartedAt.IsZero() && resolved.argsGenStartedAt.Before(resolved.execStartedAt) {
		totals.ArgsGenMS += resolved.execStartedAt.Sub(resolved.argsGenStartedAt).Milliseconds()
	}
	if resolved.execFinishedAt.After(resolved.execStartedAt) {
		totals.ExecMS += resolved.execFinishedAt.Sub(resolved.execStartedAt).Milliseconds()
	}
}

func (runner *Engine) recordToolStarted(ctx context.Context, resolved resolvedToolCall, callIndex int) error {
	if resolved.resolveError != "" || resolved.permissionDenied != "" || resolved.selectedTool == nil {
		return nil
	}
	lifecycle := runner.toolGate.currentToolLifecycle()
	if lifecycle == nil {
		return nil
	}
	owner, _ := TurnOwnerFromContext(ctx)
	return lifecycle.ToolStarted(ctx, ToolStart{
		SessionID: owner.SessionID, TurnID: owner.TurnID,
		ToolCallID: resolved.call.ID, ToolName: resolved.call.Name,
		CallIndex: callIndex, CanonicalPath: resolved.approvedReadPath,
		ArgsGenStartedAt: resolved.argsGenStartedAt,
	})
}

type fileMutationCapture struct {
	target   tool.FileMutationTarget
	before   string
	beforeOK bool
}

func (runner *Engine) prepareFileMutation(resolved resolvedToolCall) *fileMutationCapture {
	if resolved.resolveError != "" {
		return nil
	}
	if runner.display == nil || !runner.display.ConsumesFileMutations() || resolved.selectedTool == nil {
		return nil
	}
	mutationTool, ok := resolved.selectedTool.(tool.FileMutationTool)
	if !ok {
		return nil
	}
	target, err := mutationTool.FileMutationTarget(resolved.call.Input)
	if err != nil {
		return nil
	}
	capture := &fileMutationCapture{target: target}
	if target.BeforeExists {
		data, err := os.ReadFile(target.Path)
		if err != nil {
			return nil
		}
		capture.before = string(data)
		capture.beforeOK = true
	}
	return capture
}

func completeFileMutationCapture(capture *fileMutationCapture, result message.ToolResult) *ui.FileMutationSnapshot {
	if capture == nil || result.IsError {
		return nil
	}
	data, err := os.ReadFile(capture.target.Path)
	if err != nil {
		return nil
	}
	return &ui.FileMutationSnapshot{
		Before:       capture.before,
		After:        string(data),
		BeforeExists: capture.target.BeforeExists && capture.beforeOK,
		AfterExists:  true,
	}
}

func (runner *Engine) emitToolCall(resolved resolvedToolCall, capture *fileMutationCapture) error {
	call := resolved.call
	_, isFileMutation := resolved.selectedTool.(tool.FileMutationTool)
	event := ui.ToolCallEvent{
		ID:                call.ID,
		Name:              call.Name,
		Input:             append(json.RawMessage(nil), call.Input...),
		FileMutationKnown: resolved.selectedTool != nil,
		IsFileMutation:    isFileMutation,
		ArgsGenStartedAt:  resolved.argsGenStartedAt,
	}
	if capture != nil {
		event.FileMutation = &ui.FileMutationSnapshot{
			Before:       capture.before,
			BeforeExists: capture.target.BeforeExists && capture.beforeOK,
		}
	}
	runner.recordTraceEvent("tool_call", map[string]any{
		"tool_use_id": call.ID,
		"name":        call.Name,
		"input":       json.RawMessage(append([]byte(nil), call.Input...)),
	})
	return runner.display.Publish(DisplayEvent{Kind: DisplayToolCall, ToolCall: event})
}

func (runner *Engine) emitToolResult(resolved resolvedToolCall, result message.ToolResult, mutation *ui.FileMutationSnapshot) error {
	call := resolved.call
	_, isFileMutation := resolved.selectedTool.(tool.FileMutationTool)
	traceData := map[string]any{
		"tool_use_id": result.ToolUseID,
		"name":        call.Name,
		"is_error":    result.IsError,
		"content":     result.Content,
	}
	// 阶段耗时：参数生成段（args_gen_ms，无流式窗口则缺省）与纯执行段（exec_ms）。
	if !resolved.argsGenStartedAt.IsZero() && !resolved.execStartedAt.IsZero() && resolved.argsGenStartedAt.Before(resolved.execStartedAt) {
		traceData["args_gen_ms"] = resolved.execStartedAt.Sub(resolved.argsGenStartedAt).Milliseconds()
	}
	if !resolved.execStartedAt.IsZero() && resolved.execFinishedAt.After(resolved.execStartedAt) {
		traceData["exec_ms"] = resolved.execFinishedAt.Sub(resolved.execStartedAt).Milliseconds()
	}
	runner.recordTraceEvent("tool_result", traceData)
	return runner.display.Publish(DisplayEvent{Kind: DisplayToolResult, ToolResult: ui.ToolResultEvent{
		ToolUseID:         result.ToolUseID,
		Name:              call.Name,
		Content:           result.Content,
		IsError:           result.IsError,
		FileMutationKnown: resolved.selectedTool != nil,
		IsFileMutation:    isFileMutation,
		FileMutation:      mutation,
	}})
}

func isToolCallConcurrencySafe(resolved resolvedToolCall) bool {
	if resolved.resolveError != "" {
		return false
	}
	if _, streaming := resolved.selectedTool.(tool.StreamTool); streaming {
		// Engine exposes one active-tool cancellation slot, so streamed tools
		// must run outside concurrent batches.
		return false
	}
	safeTool, ok := resolved.selectedTool.(tool.ConcurrencySafeTool)
	return ok && safeTool.IsConcurrencySafe(append(json.RawMessage(nil), resolved.call.Input...))
}

func (runner *Engine) executeResolvedToolCall(ctx context.Context, resolved resolvedToolCall) message.ToolResult {
	if resolved.resolveError != "" {
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: resolved.resolveError, IsError: true}
	}
	if resolved.permissionDenied != "" {
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: resolved.permissionDenied, IsError: true}
	}
	if resolved.approvedReadPath != "" {
		readTool, ok := resolved.selectedTool.(tool.PermissionReadTool)
		if !ok {
			return message.ToolResult{ToolUseID: resolved.call.ID, Content: "Read permission adapter is unavailable", IsError: true}
		}
		output, err := readTool.RunApprovedRead(ctx, resolved.call.Input, resolved.approvedReadPath)
		if err != nil {
			return message.ToolResult{ToolUseID: resolved.call.ID, Content: err.Error(), IsError: true}
		}
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: output}
	}

	if streamed, ok := resolved.selectedTool.(tool.StreamTool); ok {
		return runner.executeStreamToolCall(ctx, resolved, streamed)
	}

	output, err := resolved.selectedTool.Run(ctx, resolved.call.Input)
	if err != nil {
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: err.Error(), IsError: true}
	}

	return message.ToolResult{ToolUseID: resolved.call.ID, Content: output}
}

func (runner *Engine) executeStreamToolCall(ctx context.Context, resolved resolvedToolCall, streamed tool.StreamTool) message.ToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	toolCtx, cancel := context.WithCancel(ctx)
	runner.registerActiveTool(resolved.call.ID, resolved.call.Name, cancel)
	defer func() {
		cancel()
		runner.clearActiveTool(resolved.call.ID)
	}()

	output, interrupted, err := streamed.Stream(toolCtx, resolved.call.Input)
	if err != nil {
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: err.Error(), IsError: true}
	}
	if interrupted {
		content := "interrupted"
		if output != "" {
			content += "\n" + output
		}
		return message.ToolResult{ToolUseID: resolved.call.ID, Content: content, IsError: true}
	}
	return message.ToolResult{ToolUseID: resolved.call.ID, Content: output}
}
