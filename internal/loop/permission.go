package loop

import (
	"context"
	"fmt"
	"time"

	"paw/internal/tool"
)

type PermissionDecision string

const (
	PermissionAllowOnce PermissionDecision = "allow_once"
	PermissionDeny      PermissionDecision = "deny"
)

type PermissionRequest struct {
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	ToolCallID    string `json:"tool_call_id"`
	CanonicalPath string `json:"canonical_path"`
}

type PermissionGate interface {
	Decide(context.Context, PermissionRequest) (PermissionDecision, error)
}

type ToolStart struct {
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	ToolCallID    string `json:"tool_call_id"`
	ToolName      string `json:"tool_name"`
	CallIndex     int    `json:"call_index"`
	CanonicalPath string `json:"canonical_path,omitempty"`
	// ArgsGenStartedAt 是工具参数开始流式生成的时刻（无流式窗口时为零值），
	// 用于「参数生成→执行完成」合并耗时口径。
	ArgsGenStartedAt time.Time `json:"args_gen_started_at,omitempty"`
}

type ToolLifecycle interface {
	ToolStarted(context.Context, ToolStart) error
}

func (runner *Engine) SetPermissionGate(gate PermissionGate) {
	if runner == nil {
		return
	}
	runner.toolGate.setPermissionGate(gate)
}

func (runner *Engine) SetToolLifecycle(lifecycle ToolLifecycle) {
	if runner == nil {
		return
	}
	runner.toolGate.setToolLifecycle(lifecycle)
}

func (runner *Engine) preflightToolPermissions(ctx context.Context, calls []resolvedToolCall) error {
	owner, _ := TurnOwnerFromContext(ctx)
	for i := range calls {
		resolved := &calls[i]
		if resolved.resolveError != "" || resolved.selectedTool == nil {
			continue
		}
		readTool, ok := resolved.selectedTool.(tool.PermissionReadTool)
		if !ok || runner.YoloMode() {
			continue
		}
		canonicalPath, outside, err := readTool.PermissionReadTarget(resolved.call.Input)
		if err != nil {
			resolved.resolveError = err.Error()
			continue
		}
		if !outside {
			continue
		}
		request := PermissionRequest{
			SessionID: owner.SessionID, TurnID: owner.TurnID,
			ToolCallID: resolved.call.ID, CanonicalPath: canonicalPath,
		}
		gate := runner.toolGate.currentPermissionGate()
		if gate == nil {
			resolved.permissionDenied = fmt.Sprintf("Read permission denied for %s", canonicalPath)
			continue
		}
		decision, err := gate.Decide(ctx, request)
		if err != nil {
			return err
		}
		switch decision {
		case PermissionAllowOnce:
			resolved.approvedReadPath = canonicalPath
		case PermissionDeny:
			resolved.permissionDenied = fmt.Sprintf("Read permission denied for %s", canonicalPath)
		default:
			return fmt.Errorf("invalid permission decision %q", decision)
		}
	}
	return nil
}
