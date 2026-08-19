package loop

import (
	"fmt"
	"reflect"
	"testing"

	"paw/internal/ui"
)

type orderedDisplayUI struct {
	events []string
}

func (u *orderedDisplayUI) OnAssistantDelta(text string) error {
	u.events = append(u.events, "assistant:"+text)
	return nil
}

func (u *orderedDisplayUI) OnThinkingDelta(text string) error {
	u.events = append(u.events, "thinking:"+text)
	return nil
}

func (u *orderedDisplayUI) OnReasoningStart(index int, redacted bool) error {
	u.events = append(u.events, fmt.Sprintf("reasoning-start:%d:%t", index, redacted))
	return nil
}

func (u *orderedDisplayUI) OnReasoningDelta(index int, text string) error {
	u.events = append(u.events, fmt.Sprintf("reasoning-delta:%d:%s", index, text))
	return nil
}

func (u *orderedDisplayUI) OnReasoningEnd(index int) error {
	u.events = append(u.events, fmt.Sprintf("reasoning-end:%d", index))
	return nil
}

func (u *orderedDisplayUI) OnToolCall(event ui.ToolCallEvent) error {
	u.events = append(u.events, "tool-call:"+event.Name)
	return nil
}

func (u *orderedDisplayUI) OnToolResult(event ui.ToolResultEvent) error {
	u.events = append(u.events, "tool-result:"+event.Name)
	return nil
}

func (u *orderedDisplayUI) OnDone() error {
	u.events = append(u.events, "done")
	return nil
}

func (u *orderedDisplayUI) ConsumesFileMutations() bool { return true }

func TestDisplayBusPublishesOrderedEphemeralEvents(t *testing.T) {
	output := &orderedDisplayUI{}
	bus := NewDisplayBus(NewUIDisplayAdapter(output))
	events := []DisplayEvent{
		{Kind: DisplayAssistantDelta, Text: "hello"},
		{Kind: DisplayThinkingDelta, Text: "inspect"},
		{Kind: DisplayReasoningStart, PartIndex: 2, Redacted: true},
		{Kind: DisplayReasoningDelta, PartIndex: 2, Text: "why"},
		{Kind: DisplayReasoningEnd, PartIndex: 2},
		{Kind: DisplayToolCall, ToolCall: ui.ToolCallEvent{Name: "Read"}},
		{Kind: DisplayToolResult, ToolResult: ui.ToolResultEvent{Name: "Read"}},
		{Kind: DisplayDone},
	}
	for _, event := range events {
		if err := bus.Publish(event); err != nil {
			t.Fatalf("Publish(%s): %v", event.Kind, err)
		}
	}
	want := []string{
		"assistant:hello", "thinking:inspect", "reasoning-start:2:true",
		"reasoning-delta:2:why", "reasoning-end:2", "tool-call:Read",
		"tool-result:Read", "done",
	}
	if !reflect.DeepEqual(output.events, want) {
		t.Fatalf("events = %#v, want %#v", output.events, want)
	}
	if !bus.ConsumesFileMutations() {
		t.Fatal("ConsumesFileMutations() = false, want true")
	}
}
