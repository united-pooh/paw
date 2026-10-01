package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"paw/internal/capability/model"
	"paw/internal/message"
)

const maxToolProtocolRecoveries = 10

const toolProtocolRecoveryInstruction = "[Paw tool protocol recovery]\n上一次响应的工具协议或参数损坏，整批请求均未执行。继续原任务、已有计划和用户约束。仅修正尚未执行的请求，使用当前可用工具的原生工具调用接口，按工具 schema 提供完整 JSON 对象参数，不要输出文字形式的工具协议或尝试拼接损坏片段。历史中已有结果的工具已执行，不要重复；若执行结果失败，请先根据错误修正请求或选择适当工具。不得绕过权限拒绝或用户取消。"

type toolProtocolRecovery struct {
	attempts int
	format   string
}

func toolProtocolErrorFormat(err error) (string, bool) {
	var protocolErr *ToolProtocolError
	if errors.As(err, &protocolErr) {
		return protocolErr.Format, true
	}
	var argumentsErr *model.ToolArgumentsError
	if errors.As(err, &argumentsErr) {
		return "native_arguments", true
	}
	return "", false
}

func (recovery *toolProtocolRecovery) requestHistory(history []message.Message) []message.Message {
	if recovery.attempts == 0 {
		return history
	}
	messages := make([]message.Message, 0, len(history)+1)
	messages = append(messages, history...)
	return append(messages, message.Message{Role: message.RoleUser, Content: toolProtocolRecoveryInstruction})
}

func (recovery *toolProtocolRecovery) retry(ctx context.Context, runner *Engine, cause error) (bool, error) {
	format, ok := toolProtocolErrorFormat(cause)
	if !ok {
		return false, cause
	}
	recovery.format = format
	recovery.record(ctx, runner, "failure")
	if err := ctx.Err(); err != nil {
		recovery.record(ctx, runner, "canceled")
		return false, err
	}
	if recovery.attempts >= maxToolProtocolRecoveries {
		recovery.record(ctx, runner, "exhausted")
		return false, fmt.Errorf("工具协议自动修复已连续重试 %d 次，仍无法安全执行: %w", maxToolProtocolRecoveries, cause)
	}
	recovery.attempts++
	recovery.record(ctx, runner, "retry")
	return true, nil
}

func (recovery *toolProtocolRecovery) complete(ctx context.Context, runner *Engine) {
	if recovery.attempts == 0 {
		return
	}
	recovery.record(ctx, runner, "recovered")
	*recovery = toolProtocolRecovery{}
}

func (recovery *toolProtocolRecovery) record(ctx context.Context, runner *Engine, status string) {
	owner, _ := TurnOwnerFromContext(ctx)
	data := map[string]any{
		"event": "tool_protocol_recovery", "status": status,
		"session_id": owner.SessionID, "turn_id": owner.TurnID,
		"format": recovery.format, "attempt": recovery.attempts, "limit": maxToolProtocolRecoveries,
	}
	encoded, _ := json.Marshal(data)
	// Interactive mode routes the standard logger to tui.log; never print a
	// recovery notice into the transcript or include untrusted tool arguments.
	log.Print(string(encoded))
	runner.recordTraceEvent("tool_protocol_recovery", data)
}
