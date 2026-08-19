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
)

type toolResultCheckpoint func(callIndex int, result message.ToolResult) error

type resolvedToolCall struct {
	call             message.ToolCall
	selectedTool     tool.Tool
	resolveError     string
	approvedReadPath string
	permissionDenied string
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
	resolvedCalls := make([]resolvedToolCall, len(calls))
	for i := range calls {
		resolvedCalls[i] = runner.resolveToolCall(calls[i])
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
			batchResults, err := runner.runToolCallBatch(ctx, resolvedCalls[start:end], start, checkpoint)
			if err != nil {
				return message.Message{}, err
			}
			results = append(results, batchResults...)
			start = end
			continue
		}

		result, err := runner.runResolvedToolCallWithCheckpoint(ctx, resolvedStart, start, checkpoint)
		if err != nil {
			return message.Message{}, err
		}
		results = append(results, result)
		start++
	}
	return buildToolResultsMessage(results), nil
}

func (runner *Engine) runToolCallBatch(ctx context.Context, calls []resolvedToolCall, offset int, checkpoint toolResultCheckpoint) ([]message.ToolResult, error) {
	captures := make([]*fileMutationCapture, len(calls))
	for i, resolved := range calls {
		captures[i] = runner.prepareFileMutation(resolved)
		if err := runner.emitToolCall(resolved, captures[i]); err != nil {
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
	var checkpointMu sync.Mutex
	var checkpointErr error
	var wg sync.WaitGroup
	for i := range calls {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runner.executeResolvedToolCall(ctx, calls[i])
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
		if err := runner.emitToolResult(calls[i], result, mutations[i]); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (runner *Engine) runToolCall(ctx context.Context, call message.ToolCall) (message.ToolResult, error) {
	return runner.runToolCallWithCheckpoint(ctx, call, 0, nil)
}

func (runner *Engine) runToolCallWithCheckpoint(ctx context.Context, call message.ToolCall, callIndex int, checkpoint toolResultCheckpoint) (message.ToolResult, error) {
	return runner.runResolvedToolCallWithCheckpoint(ctx, runner.resolveToolCall(call), callIndex, checkpoint)
}

func (runner *Engine) runResolvedToolCallWithCheckpoint(ctx context.Context, resolved resolvedToolCall, callIndex int, checkpoint toolResultCheckpoint) (message.ToolResult, error) {
	capture := runner.prepareFileMutation(resolved)
	if err := runner.emitToolCall(resolved, capture); err != nil {
		return message.ToolResult{}, err
	}
	if err := runner.recordToolStarted(ctx, resolved, callIndex); err != nil {
		return message.ToolResult{}, err
	}

	result := runner.executeResolvedToolCall(ctx, resolved)
	mutation := completeFileMutationCapture(capture, result)
	if checkpoint != nil {
		if err := checkpoint(callIndex, result); err != nil {
			return message.ToolResult{}, err
		}
	}
	if err := runner.emitToolResult(resolved, result, mutation); err != nil {
		return message.ToolResult{}, err
	}

	return result, nil
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
	runner.recordTraceEvent("tool_result", map[string]any{
		"tool_use_id": result.ToolUseID,
		"name":        call.Name,
		"is_error":    result.IsError,
		"content":     result.Content,
	})
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
