package streamma

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeChainPropagatesToDirectSuccessor(t *testing.T) {
	spec := GraphSpec{
		RunID:  "run-chain",
		Agents: []AgentSpec{{ID: "a"}, {ID: "b"}},
		Edges:  []EdgeSpec{{From: "a", To: "b"}},
	}
	model := newFakeModel(
		fakeTextResponse("from a\nEND_STEP\n"),
		fakeTextResponse("from b final\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "from b final" {
		t.Fatalf("unexpected final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if !strings.Contains(promptText(calls[1].Messages), "from a") {
		t.Fatalf("successor prompt missing upstream step: %q", promptText(calls[1].Messages))
	}
}

func TestRuntimeTreeBroadcastsToMultipleDirectSuccessors(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-tree",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "c", SystemPrompt: "agent C"},
			{ID: "d", SystemPrompt: "agent D"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("A.step1\nEND_STEP\n"),
		fakeTextResponse("B.fromA\nEND_STEP\n"),
		fakeTextResponse("C.fromA\nEND_STEP\n"),
		fakeTextResponse("D.afterB\nEND_STEP\n"),
		fakeTextResponse("D.afterBC\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "D.afterBC" {
		t.Fatalf("D did not finalize after consuming both B and C outputs: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 5 {
		t.Fatalf("calls = %d, want 5", len(calls))
	}
	promptsBySystem := map[string][]string{}
	for _, call := range calls {
		promptsBySystem[call.Messages[0].Content] = append(promptsBySystem[call.Messages[0].Content], promptText(call.Messages))
	}
	if len(promptsBySystem["agent B"]) != 1 {
		t.Fatalf("B calls = %d, want 1: %#v", len(promptsBySystem["agent B"]), calls)
	}
	if len(promptsBySystem["agent C"]) != 1 {
		t.Fatalf("C calls = %d, want 1: %#v", len(promptsBySystem["agent C"]), calls)
	}
	if !strings.Contains(promptsBySystem["agent B"][0], "A.step1") {
		t.Fatalf("b prompt missing source step: %q", promptsBySystem["agent B"][0])
	}
	if !strings.Contains(promptsBySystem["agent C"][0], "A.step1") {
		t.Fatalf("c prompt missing source step: %q", promptsBySystem["agent C"][0])
	}
	dFinalPrompt := promptText(calls[4].Messages)
	if !strings.Contains(dFinalPrompt, "B.fromA") || !strings.Contains(dFinalPrompt, "C.fromA") {
		t.Fatalf("D final prompt did not include both B and C outputs: %q", dFinalPrompt)
	}
}

func TestRuntimeGraphMultiPredecessorIsArrivalTriggered(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-graph",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "c", SystemPrompt: "agent C"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "d", SystemPrompt: "agent D"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "c"},
			{From: "c", To: "d"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("A.step1\nEND_STEP\n"),
		fakeTextResponse("C.afterAShortcut\nEND_STEP\n"),
		fakeTextResponse("B.fromA\nEND_STEP\n"),
		fakeTextResponse("D.afterCShortcut\nEND_STEP\n"),
		fakeTextResponse("C.afterB\nEND_STEP\n"),
		fakeTextResponse("D.afterCAndB\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Final == nil || result.Final.Answer.Text != "D.afterCAndB" {
		t.Fatalf("unexpected graph final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(calls))
	}
	cFirstPrompt := promptText(calls[1].Messages)
	if calls[1].Messages[0].Content != "agent C" {
		t.Fatalf("call 2 target = %q, want agent C", calls[1].Messages[0].Content)
	}
	if calls[2].Messages[0].Content != "agent B" {
		t.Fatalf("call 3 target = %q, want agent B", calls[2].Messages[0].Content)
	}
	if !strings.Contains(cFirstPrompt, "A.step1") {
		t.Fatalf("first c prompt missing a step: %q", cFirstPrompt)
	}
	if strings.Contains(cFirstPrompt, "B.fromA") {
		t.Fatalf("c waited for b before first invocation: %q", cFirstPrompt)
	}
	cSecondPrompt := ""
	for _, call := range calls[3:] {
		if call.Messages[0].Content == "agent C" {
			cSecondPrompt = promptText(call.Messages)
			break
		}
	}
	if cSecondPrompt == "" {
		t.Fatalf("missing second C invocation: %#v", calls)
	}
	if !strings.Contains(cSecondPrompt, "A.step1") || !strings.Contains(cSecondPrompt, "B.fromA") {
		t.Fatalf("second c prompt did not consume both shortcut and B steps: %q", cSecondPrompt)
	}
}
