package model

import (
	"encoding/json"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestRepairResponsesInputItemsLeavesHealthyInputUntouched(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"user","content":"inspect"}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"module paw"}`),
		json.RawMessage(`{"type":"message","role":"assistant","content":"done"}`),
	}

	repaired, stats := repairResponsesInputItems(items)
	if len(repaired) != len(items) {
		t.Fatalf("len = %d, want %d", len(repaired), len(items))
	}
	if stats.RepairedToolCalls != 0 || stats.OrphanedResults != 0 {
		t.Fatalf("stats = %+v, want zero", stats)
	}
	if &repaired[0] != &items[0] {
		t.Fatalf("healthy input was copied; want zero-copy return")
	}
	if _, again := repairResponsesInputItems(repaired); again.RepairedToolCalls != 0 || again.OrphanedResults != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairResponsesInputItemsIsolatesOrphanedOutputs(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"module paw"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_ghost","output":"stale from crashed turn"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"","output":"no id at all"}`),
	}

	repaired, stats := repairResponsesInputItems(items)
	if stats.OrphanedResults != 2 || stats.RepairedToolCalls != 0 {
		t.Fatalf("stats = %+v, want two orphaned outputs", stats)
	}
	if len(repaired) != 2 {
		t.Fatalf("len = %d, want 2 after isolation", len(repaired))
	}
	for _, raw := range repaired {
		var view responsesItem
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		if view.Type == "function_call_output" && view.CallID != "call_1" {
			t.Fatalf("orphan survived repair: %s", raw)
		}
	}
}

func TestRepairResponsesInputItemsAddsSyntheticOutputForDanglingCall(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"user","content":"inspect"}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_a","output":"module paw"}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_b","name":"Grep","arguments":"{}"}`),
		json.RawMessage(`{"type":"message","role":"user","content":"continue"}`),
	}

	repaired, stats := repairResponsesInputItems(items)
	if stats.RepairedToolCalls != 1 || stats.OrphanedResults != 0 {
		t.Fatalf("stats = %+v, want one repaired dangling call", stats)
	}
	if len(stats.RepairedCallIDs) != 1 || stats.RepairedCallIDs[0] != "call_b" {
		t.Fatalf("repaired ids = %v, want [call_b]", stats.RepairedCallIDs)
	}
	if len(repaired) != len(items)+1 {
		t.Fatalf("len = %d, want %d with synthetic output", len(repaired), len(items)+1)
	}
	// 合成 output 紧随 call_b 之后（index 4），不能改变其他项顺序。
	var synthetic responsesFunctionCallOutputItem
	if err := json.Unmarshal(repaired[4], &synthetic); err != nil {
		t.Fatal(err)
	}
	if synthetic.Type != "function_call_output" || synthetic.CallID != "call_b" || !strings.Contains(synthetic.Output, "not executed") {
		t.Fatalf("synthetic = %#v", synthetic)
	}
	// 幂等。
	if _, again := repairResponsesInputItems(repaired); again.RepairedToolCalls != 0 || again.OrphanedResults != 0 {
		t.Fatalf("second repair stats = %+v, want zero", again)
	}
}

func TestRepairResponsesInputItemsCoversProviderDataReplayInconsistency(t *testing.T) {
	// 模拟崩溃场景：ProviderData 重放的 function_call 存在，
	// 但结构化 ToolResults 中多了一条孤儿 output（重放 items 与结构化字段不一致）。
	cfg := Config{Provider: "openai", ProfileID: "openai-main", Transport: "openai-responses", Adapter: "gpt", Model: "gpt-test"}
	messages := []message.Message{
		{Role: message.RoleAssistant, GeneratedBy: messageOriginForConfig(cfg, responsesProviderTransport), ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{}"}]}`)},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "call_ghost", Content: "orphan"}},
	}

	items, err := buildResponsesInput(cfg, messages)
	if err != nil {
		t.Fatal(err)
	}
	repaired, stats := repairResponsesInputItems(items)
	if stats.OrphanedResults != 1 {
		t.Fatalf("stats = %+v, want orphaned replay output isolated", stats)
	}
	for _, raw := range repaired {
		var view responsesItem
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		if view.Type == "function_call_output" && view.CallID == "call_ghost" {
			t.Fatalf("orphaned replay output survived: %s", raw)
		}
	}
}
