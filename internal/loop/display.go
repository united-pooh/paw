package loop

import (
	"fmt"
	"sync"

	"paw/internal/model"
	"paw/internal/ui"
)

type DisplayEventKind string

const (
	DisplayAssistantDelta DisplayEventKind = "assistant.delta"
	DisplayThinkingDelta  DisplayEventKind = "thinking.delta"
	DisplayReasoningStart DisplayEventKind = "reasoning.start"
	DisplayReasoningDelta DisplayEventKind = "reasoning.delta"
	DisplayReasoningEnd   DisplayEventKind = "reasoning.end"
	DisplayToolCall       DisplayEventKind = "tool.call"
	DisplayToolResult     DisplayEventKind = "tool.result"
	DisplayDone           DisplayEventKind = "turn.done"
	DisplaySystem         DisplayEventKind = "system.message"
	DisplayModelUsage     DisplayEventKind = "model.usage"
)

// DisplayEvent is intentionally ephemeral. Durable conversation and control
// state is persisted separately; this event only drives live presentation.
type DisplayEvent struct {
	Kind       DisplayEventKind
	Text       string
	PartIndex  int
	Redacted   bool
	ToolCall   ui.ToolCallEvent
	ToolResult ui.ToolResultEvent
	System     ui.SystemEvent
	Usage      model.Usage
}

// DisplaySubscriber is the display seam used by the ordered in-memory bus.
type DisplaySubscriber interface {
	Publish(DisplayEvent) error
	ConsumesFileMutations() bool
}

// DisplayBus serializes live display events for one Engine/session.
type DisplayBus struct {
	mu          sync.Mutex
	subscribers []DisplaySubscriber
}

func NewDisplayBus(subscribers ...DisplaySubscriber) *DisplayBus {
	return &DisplayBus{subscribers: append([]DisplaySubscriber(nil), subscribers...)}
}

func newUIDisplayBus(output ui.UI) *DisplayBus {
	if output == nil {
		return nil
	}
	return NewDisplayBus(NewUIDisplayAdapter(output))
}

func (b *DisplayBus) Publish(event DisplayEvent) error {
	if b == nil {
		return fmt.Errorf("display bus is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		if subscriber == nil {
			continue
		}
		if err := subscriber.Publish(event); err != nil {
			return err
		}
	}
	return nil
}

func (b *DisplayBus) ConsumesFileMutations() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		if subscriber != nil && subscriber.ConsumesFileMutations() {
			return true
		}
	}
	return false
}

type uiDisplayAdapter struct {
	output ui.UI
}

func NewUIDisplayAdapter(output ui.UI) DisplaySubscriber {
	return &uiDisplayAdapter{output: output}
}

func (a *uiDisplayAdapter) Publish(event DisplayEvent) error {
	if a == nil || a.output == nil {
		return nil
	}
	switch event.Kind {
	case DisplayAssistantDelta:
		return a.output.OnAssistantDelta(event.Text)
	case DisplayThinkingDelta:
		if receiver, ok := a.output.(ui.ThinkingDeltaReceiver); ok {
			return receiver.OnThinkingDelta(event.Text)
		}
	case DisplayReasoningStart:
		if receiver, ok := a.output.(ui.AssistantPartReceiver); ok {
			return receiver.OnReasoningStart(event.PartIndex, event.Redacted)
		}
	case DisplayReasoningDelta:
		if receiver, ok := a.output.(ui.AssistantPartReceiver); ok {
			return receiver.OnReasoningDelta(event.PartIndex, event.Text)
		}
	case DisplayReasoningEnd:
		if receiver, ok := a.output.(ui.AssistantPartReceiver); ok {
			return receiver.OnReasoningEnd(event.PartIndex)
		}
	case DisplayToolCall:
		return a.output.OnToolCall(event.ToolCall)
	case DisplayToolResult:
		return a.output.OnToolResult(event.ToolResult)
	case DisplayDone:
		return a.output.OnDone()
	case DisplaySystem:
		if receiver, ok := a.output.(ui.SystemNotifier); ok {
			return receiver.OnSystemMessage(event.System)
		}
	case DisplayModelUsage:
		if receiver, ok := a.output.(modelUsageReceiver); ok {
			receiver.OnModelUsage(event.Usage)
		}
	default:
		return fmt.Errorf("unknown display event: %s", event.Kind)
	}
	return nil
}

func (a *uiDisplayAdapter) ConsumesFileMutations() bool {
	if a == nil || a.output == nil {
		return false
	}
	consumer, ok := a.output.(ui.FileMutationConsumer)
	return ok && consumer.ConsumesFileMutations()
}
