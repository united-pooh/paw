package model

import (
	"strings"

	"paw/internal/message"
)

// ToolPairRepairStats 报告一次工具调用配对修复的收据。
// 对齐 CodeWhale tool_history_repair 的 repaired/orphan 诊断。
type ToolPairRepairStats struct {
	RepairedToolCalls int
	OrphanedResults   int
	RepairedCallIDs   []string
	OrphanedResultIDs []string
}

// syntheticRepairResultContent 是悬空工具调用补发的合成错误结果内容
// （对齐 CodeWhale 的 "Tool call interrupted by process exit" 占位）。
const syntheticRepairResultContent = "[Paw repair] tool call was not executed."

// RepairToolCallPairs 在消息层面修复工具调用配对：
//  1. 隔离（移除）引用不存在 tool_use 的孤儿 tool result；
//  2. 给“已声明但从未有结果”的悬空 tool_use 在其后的第一条 user 消息补合成错误结果。
//
// 规则与 CodeWhale tool_history_repair.rs 对齐：
//   - 幂等：修复输出再次修复时 stats 为零；
//   - 零拷贝：无问题时返回原切片；
//   - 末尾悬空调用（之后没有 user 消息）保持不动：请求发出时该调用可能仍在
//     执行中，或属于补全前缀语义（对齐 CodeWhale for_provider 不追加尾部回执）。
func RepairToolCallPairs(messages []message.Message) ([]message.Message, ToolPairRepairStats) {
	var stats ToolPairRepairStats

	callIDs := make(map[string]struct{})
	for _, msg := range messages {
		for _, call := range messageToolCalls(msg) {
			if strings.TrimSpace(call.ID) != "" {
				callIDs[call.ID] = struct{}{}
			}
		}
	}

	changed := false
	out := make([]message.Message, len(messages))
	copy(out, messages)

	// 1. 隔离孤儿结果：ToolUseID 引用了不存在的 tool_use。
	//    空 ToolUseID 的结果无法验证引用，保留（文本协议可承载；
	//    Responses wire 层会对空 call_id 做最终兜底）。
	for i := range out {
		results := messageToolResults(out[i])
		if len(results) == 0 {
			continue
		}
		filtered := make([]message.ToolResult, 0, len(results))
		for _, result := range results {
			if _, ok := callIDs[result.ToolUseID]; ok || strings.TrimSpace(result.ToolUseID) == "" {
				filtered = append(filtered, result)
				continue
			}
			stats.OrphanedResults++
			stats.OrphanedResultIDs = append(stats.OrphanedResultIDs, result.ToolUseID)
			changed = true
		}
		if len(filtered) != len(results) {
			setRepairedToolResults(&out[i], filtered)
		}
	}

	// 2. 悬空调用补合成错误结果：call 存在但整个历史中没有任何 result 引用它。
	answered := make(map[string]struct{})
	for _, msg := range out {
		for _, result := range messageToolResults(msg) {
			if strings.TrimSpace(result.ToolUseID) != "" {
				answered[result.ToolUseID] = struct{}{}
			}
		}
	}
	for i := range out {
		calls := messageToolCalls(out[i])
		if len(calls) == 0 {
			continue
		}
		index := nextUserMessageIndex(out, i+1)
		if index < 0 {
			continue
		}
		var synthetic []message.ToolResult
		for _, call := range calls {
			if strings.TrimSpace(call.ID) == "" {
				continue
			}
			if _, ok := answered[call.ID]; ok {
				continue
			}
			synthetic = append(synthetic, message.ToolResult{
				ToolUseID: call.ID,
				Content:   syntheticRepairResultContent,
				IsError:   true,
			})
			stats.RepairedCallIDs = append(stats.RepairedCallIDs, call.ID)
		}
		if len(synthetic) == 0 {
			continue
		}
		appendSyntheticResults(&out[index], synthetic)
		stats.RepairedToolCalls += len(synthetic)
		changed = true
	}

	if !changed {
		return messages, stats
	}
	return out, stats
}

// nextUserMessageIndex 返回从 start 开始的第一个 user 角色消息下标；不存在返回 -1。
func nextUserMessageIndex(messages []message.Message, start int) int {
	for i := start; i < len(messages); i++ {
		if messages[i].Role == message.RoleUser {
			return i
		}
	}
	return -1
}

// setRepairedToolResults 把修复后的结果列表写回消息的结构化字段。
func setRepairedToolResults(msg *message.Message, results []message.ToolResult) {
	if msg == nil {
		return
	}
	switch len(results) {
	case 0:
		msg.ToolResult = nil
		msg.ToolResults = nil
	case 1:
		msg.ToolResult = &results[0]
		msg.ToolResults = nil
	default:
		msg.ToolResult = nil
		msg.ToolResults = results
	}
}

// appendSyntheticResults 把合成结果追加到消息现有结果之后。
// 复制现有切片，避免与原始消息共享底层数组。
func appendSyntheticResults(msg *message.Message, synthetic []message.ToolResult) {
	if msg == nil || len(synthetic) == 0 {
		return
	}
	existing := messageToolResults(*msg)
	merged := make([]message.ToolResult, 0, len(existing)+len(synthetic))
	merged = append(merged, existing...)
	merged = append(merged, synthetic...)
	setRepairedToolResults(msg, merged)
}
