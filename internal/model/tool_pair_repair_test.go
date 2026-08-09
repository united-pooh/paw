package model

import (
	"encoding/json"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestRepairToolCallPairsLeavesHealthyHistoryUntouched(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"go.mod"}`)},
		}},
		{Role: message.RoleUser, ToolResults: []message.ToolResult{
			{ToolUseID: "call_1", Content: "module paw", IsError: false},
		}},
		{Role: message.RoleAssistant, Content: "done"},
	}

	repaired, stats := RepairToolCallPairs(history)
	if len(repaired) != len(history) {
		t.Fatalf("len = %d, want %d", len(repaired), len(history))
	}
	if stats.RepairedToolCalls != 0 || stats.OrphanedResults != 0 {
		t.Fatalf("stats = %+v, want zero", stats)
	}
	if &repaired[0] != &history[0] {
		t.Fatalf("healthy history was copied; want zero-copy return")
	}

	// 幂等：再次修复 stats 仍为零。
	if _, again := RepairToolCallPairs(repaired); again.RepairedToolCalls != 0 || again.OrphanedResults != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairToolCallPairsIsolatesOrphanedResults(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_1", Name: "Read", Input: json.RawMessage(`{}`)},
		}},
		{Role: message.RoleUser, Content: "mixed text", ToolResults: []message.ToolResult{
			{ToolUseID: "call_1", Content: "module paw"},
			{ToolUseID: "call_orphan", Content: "stale result from crashed turn"},
		}},
	}

	repaired, stats := RepairToolCallPairs(history)
	if stats.OrphanedResults != 1 || stats.RepairedToolCalls != 0 {
		t.Fatalf("stats = %+v, want one orphaned result", stats)
	}
	if len(stats.OrphanedResultIDs) != 1 || stats.OrphanedResultIDs[0] != "call_orphan" {
		t.Fatalf("orphaned ids = %v, want [call_orphan]", stats.OrphanedResultIDs)
	}
	results := messageToolResults(repaired[2])
	if len(results) != 1 || results[0].ToolUseID != "call_1" {
		t.Fatalf("results after repair = %#v, want only call_1", results)
	}
	if repaired[2].Content != "mixed text" {
		t.Fatalf("text content lost: %q", repaired[2].Content)
	}

	// 幂等。
	if _, again := RepairToolCallPairs(repaired); again.OrphanedResults != 0 || again.RepairedToolCalls != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairToolCallPairsAddsSyntheticResultForDanglingCall(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_a", Name: "Read", Input: json.RawMessage(`{}`)},
			{ID: "call_b", Name: "Grep", Input: json.RawMessage(`{}`)},
		}},
		{Role: message.RoleUser, ToolResults: []message.ToolResult{
			{ToolUseID: "call_a", Content: "module paw"},
		}},
		{Role: message.RoleUser, Content: "continue"},
	}

	repaired, stats := RepairToolCallPairs(history)
	if stats.RepairedToolCalls != 1 || stats.OrphanedResults != 0 {
		t.Fatalf("stats = %+v, want one repaired dangling call", stats)
	}
	if len(stats.RepairedCallIDs) != 1 || stats.RepairedCallIDs[0] != "call_b" {
		t.Fatalf("repaired ids = %v, want [call_b]", stats.RepairedCallIDs)
	}
	// 合成结果追加在 call_b 之后的第一条 user 消息（即 call_a 的结果消息）。
	results := repaired[2].ToolResults
	if len(results) != 2 {
		t.Fatalf("results after repair = %#v, want call_a + synthetic call_b", results)
	}
	if results[1].ToolUseID != "call_b" || !results[1].IsError || !strings.Contains(results[1].Content, "not executed") {
		t.Fatalf("synthetic result = %#v", results[1])
	}
	// call_a 的原始结果保持原样。
	if results[0].ToolUseID != "call_a" || results[0].Content != "module paw" || results[0].IsError {
		t.Fatalf("original result mutated: %#v", results[0])
	}

	// 幂等。
	if _, again := RepairToolCallPairs(repaired); again.RepairedToolCalls != 0 || again.OrphanedResults != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairToolCallPairsKeepsTrailingDanglingCallUntouched(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_trailing", Name: "Read", Input: json.RawMessage(`{}`)},
		}},
	}

	repaired, stats := RepairToolCallPairs(history)
	if stats.RepairedToolCalls != 0 || stats.OrphanedResults != 0 {
		t.Fatalf("stats = %+v, want zero for trailing call", stats)
	}
	if len(repaired) != len(history) {
		t.Fatalf("len = %d, want %d", len(repaired), len(history))
	}
}

func TestRepairToolCallPairsSupportsSingularFields(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{}`)}},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "call_orphan", Content: "stale"}},
	}

	repaired, stats := RepairToolCallPairs(history)
	if stats.OrphanedResults != 1 {
		t.Fatalf("stats = %+v, want one orphaned result", stats)
	}
	// 孤儿结果被隔离后，call_1 变成悬空调用，按 CodeWhale 语义补合成错误结果。
	if stats.RepairedToolCalls != 1 || len(stats.RepairedCallIDs) != 1 || stats.RepairedCallIDs[0] != "call_1" {
		t.Fatalf("stats = %+v, want repaired dangling call_1", stats)
	}
	results := messageToolResults(repaired[2])
	if len(results) != 1 || results[0].ToolUseID != "call_1" || !results[0].IsError || !strings.Contains(results[0].Content, "not executed") {
		t.Fatalf("results after repair = %#v, want synthetic error result for call_1", results)
	}

	// 幂等。
	if _, again := RepairToolCallPairs(repaired); again.RepairedToolCalls != 0 || again.OrphanedResults != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairToolCallPairsAppendsSyntheticToNextUserAfterMultipleDanglingCalls(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "one"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{{ID: "call_1", Name: "A", Input: json.RawMessage(`{}`)}}},
		{Role: message.RoleUser, Content: "two"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{{ID: "call_2", Name: "B", Input: json.RawMessage(`{}`)}}},
		{Role: message.RoleUser, Content: "three"},
	}

	repaired, stats := RepairToolCallPairs(history)
	if stats.RepairedToolCalls != 2 {
		t.Fatalf("stats = %+v, want 2 repaired calls", stats)
	}
	// call_1 的合成结果落在 index 2，call_2 的合成结果落在 index 4。
	first := messageToolResults(repaired[2])
	if len(first) != 1 || first[0].ToolUseID != "call_1" {
		t.Fatalf("first synthetic = %#v", first)
	}
	second := messageToolResults(repaired[4])
	if len(second) != 1 || second[0].ToolUseID != "call_2" {
		t.Fatalf("second synthetic = %#v", second)
	}
}

func TestProviderHTTPErrorIsToolPairingInvalidRequest(t *testing.T) {
	pairingBody := `{"error":{"message":"No tool call found for tool output with call_id call_00_mh6sW0rvruqSIul2iXci7762.","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`
	err := newProviderHTTPError(400, nil, []byte(pairingBody), "模型接口")
	if !err.IsToolPairingInvalidRequest() {
		t.Fatalf("IsToolPairingInvalidRequest() = false for pairing error: %s", err.Error())
	}

	// 普通 400（如上下文超限）不应命中。
	contextBody := `{"error":{"message":"maximum context length is 131072 tokens","type":"invalid_request_error"}}`
	if contextErr := newProviderHTTPError(400, nil, []byte(contextBody), "模型接口"); contextErr.IsToolPairingInvalidRequest() {
		t.Fatalf("IsToolPairingInvalidRequest() = true for context error: %s", contextErr.Error())
	}

	// 非 400 状态码不应命中。
	if tooMany := newProviderHTTPError(429, nil, []byte(pairingBody), "模型接口"); tooMany.IsToolPairingInvalidRequest() {
		t.Fatalf("IsToolPairingInvalidRequest() = true for 429")
	}

	// type 缺失但特征文本明确时也应命中。
	noTypeBody := `{"error":{"message":"No tool call found for tool output with call_id x"}}`
	if noTypeErr := newProviderHTTPError(400, nil, []byte(noTypeBody), "模型接口"); !noTypeErr.IsToolPairingInvalidRequest() {
		t.Fatalf("IsToolPairingInvalidRequest() = false for type-less pairing error")
	}

	// nil 安全。
	var nilErr *ProviderHTTPError
	if nilErr.IsToolPairingInvalidRequest() {
		t.Fatalf("nil error classified as pairing invalid request")
	}
}
