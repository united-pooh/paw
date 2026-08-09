package model

import (
	"encoding/json"
	"strings"
)

// repairResponsesInputItems 在 wire 层面修复 Responses input items 的工具调用配对：
//  1. 隔离（移除）call_id 无对应 function_call 的 function_call_output（含空 call_id）；
//  2. 给输入中没有任何 function_call_output 引用的 function_call 紧随其后补合成错误 output。
//
// 覆盖 ProviderData 重放与结构化字段不一致的崩溃场景（消息层面修复看不到
// 重放出的 raw items，这里是最终 wire 形态的兜底）。幂等；无问题时零拷贝返回。
func repairResponsesInputItems(items []json.RawMessage) ([]json.RawMessage, ToolPairRepairStats) {
	var stats ToolPairRepairStats

	callIDs := make(map[string]struct{})
	for _, raw := range items {
		var view responsesItem
		if json.Unmarshal(raw, &view) != nil {
			continue
		}
		if view.Type == "function_call" && strings.TrimSpace(view.CallID) != "" {
			callIDs[view.CallID] = struct{}{}
		}
	}

	out := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, raw := range items {
		var view responsesItem
		if json.Unmarshal(raw, &view) != nil || view.Type != "function_call_output" {
			out = append(out, raw)
			continue
		}
		if strings.TrimSpace(view.CallID) == "" {
			stats.OrphanedResults++
			stats.OrphanedResultIDs = append(stats.OrphanedResultIDs, view.CallID)
			changed = true
			continue
		}
		if _, ok := callIDs[view.CallID]; !ok {
			stats.OrphanedResults++
			stats.OrphanedResultIDs = append(stats.OrphanedResultIDs, view.CallID)
			changed = true
			continue
		}
		out = append(out, raw)
	}

	// 悬空 function_call：输入中没有任何 output 引用它 → 紧随其后补合成 output。
	// 请求发出时输入里的 call 全部是历史记录，未配对的调用永远不会再被执行，
	// 因此一律补齐（对齐 CodeWhale anthropic wire 修复的按序占位精神）。
	answered := make(map[string]struct{})
	for _, raw := range out {
		var view responsesItem
		if json.Unmarshal(raw, &view) == nil && view.Type == "function_call_output" && strings.TrimSpace(view.CallID) != "" {
			answered[view.CallID] = struct{}{}
		}
	}
	repaired := make([]json.RawMessage, 0, len(out)+1)
	for _, raw := range out {
		repaired = append(repaired, raw)
		var view responsesItem
		if json.Unmarshal(raw, &view) != nil || view.Type != "function_call" {
			continue
		}
		callID := strings.TrimSpace(view.CallID)
		if callID == "" {
			continue
		}
		if _, ok := answered[callID]; ok {
			continue
		}
		synthetic, err := json.Marshal(responsesFunctionCallOutputItem{
			Type:   "function_call_output",
			CallID: callID,
			Output: syntheticRepairResultContent,
		})
		if err != nil {
			continue
		}
		repaired = append(repaired, synthetic)
		stats.RepairedCallIDs = append(stats.RepairedCallIDs, callID)
		stats.RepairedToolCalls++
		changed = true
	}

	if !changed {
		return items, stats
	}
	return repaired, stats
}
