package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"paw/internal/ui"
)

type EventDetail struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type UIAdapter struct {
	mu sync.Mutex

	workspaceID WorkspaceID
	coordinator *WorkspaceCoordinator
	hub         *EventHub
	now         func() time.Time

	sessionID string
	turnID    string
	assistant *DeltaBatcher
	reasoning map[int]*DeltaBatcher
	toolStart map[string]time.Time
	details   map[string]EventDetail
	lastErr   error
}

func NewUIAdapter(workspaceID WorkspaceID, coordinator *WorkspaceCoordinator, hub *EventHub) *UIAdapter {
	return &UIAdapter{
		workspaceID: workspaceID, coordinator: coordinator, hub: hub, now: time.Now,
		reasoning: make(map[int]*DeltaBatcher), toolStart: make(map[string]time.Time), details: make(map[string]EventDetail),
	}
}

func (a *UIAdapter) BindTurn(sessionID, turnID string) error {
	if a == nil {
		return fmt.Errorf("UI adapter is nil")
	}
	a.mu.Lock()
	assistant := a.assistant
	reasoning := make([]*DeltaBatcher, 0, len(a.reasoning))
	for _, batcher := range a.reasoning {
		reasoning = append(reasoning, batcher)
	}
	a.assistant = nil
	a.reasoning = make(map[int]*DeltaBatcher)
	a.toolStart = make(map[string]time.Time)
	a.lastErr = nil
	a.sessionID = strings.TrimSpace(sessionID)
	a.turnID = strings.TrimSpace(turnID)
	a.mu.Unlock()
	if assistant != nil {
		assistant.Close()
	}
	for _, batcher := range reasoning {
		batcher.Close()
	}
	return nil
}

func (a *UIAdapter) OnAssistantDelta(text string) error {
	if text == "" {
		return a.currentError()
	}
	a.mu.Lock()
	if err := a.ensureBoundLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.assistant == nil {
		partID := stablePartID(a.turnID, "assistant", 0)
		if err := a.startPartLocked(StreamingPart{PartID: partID, SessionID: a.sessionID, TurnID: a.turnID, Kind: "assistant"}, EventAssistantPartStarted, AssistantPartStartedPayload{PartID: partID, PartIndex: 0, Kind: "assistant"}); err != nil {
			a.mu.Unlock()
			return err
		}
		a.assistant = NewDeltaBatcher(partID, nil, a.emitAssistantBatch)
	}
	batcher := a.assistant
	err := a.lastErr
	a.mu.Unlock()
	if err != nil {
		return err
	}
	batcher.Add(text)
	return a.currentError()
}

func (a *UIAdapter) OnThinkingDelta(text string) error {
	if text == "" {
		return a.currentError()
	}
	if err := a.ensureReasoning(-1, false); err != nil {
		return err
	}
	a.mu.Lock()
	batcher := a.reasoning[-1]
	err := a.lastErr
	a.mu.Unlock()
	if err != nil {
		return err
	}
	batcher.Add(text)
	return a.currentError()
}

func (a *UIAdapter) OnReasoningStart(partIndex int, redacted bool) error {
	return a.ensureReasoning(partIndex, redacted)
}

func (a *UIAdapter) OnReasoningDelta(partIndex int, text string) error {
	if err := a.ensureReasoning(partIndex, false); err != nil {
		return err
	}
	a.mu.Lock()
	batcher := a.reasoning[partIndex]
	err := a.lastErr
	a.mu.Unlock()
	if err != nil {
		return err
	}
	batcher.Add(text)
	return a.currentError()
}

func (a *UIAdapter) OnReasoningEnd(partIndex int) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	batcher := a.reasoning[partIndex]
	a.mu.Unlock()
	if batcher == nil {
		return nil
	}
	batcher.Close()
	partID := stablePartID(a.turnID, "reasoning", partIndex)
	part, _, err := a.coordinator.CompletePart(partID)
	if err != nil {
		return err
	}
	return a.publish(EventReasoningCompleted, AssistantPartCompletedPayload{PartID: partID, FinalLength: len(part.Text)})
}

func (a *UIAdapter) OnToolCall(event ui.ToolCallEvent) error {
	if a == nil {
		return nil
	}
	startedAt := event.ArgsGenStartedAt
	if startedAt.IsZero() {
		startedAt = a.now().UTC()
	}
	a.mu.Lock()
	if err := a.ensureBoundLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	a.toolStart[event.ID] = startedAt
	sessionID, turnID := a.sessionID, a.turnID
	a.mu.Unlock()
	payload := ToolStartedPayload{
		ToolUseID: event.ID, Name: event.Name, Target: toolTarget(event.Input),
		ArgsSummary: summarizeEventText(string(event.Input), 240), StartedAt: startedAt.UTC(),
	}
	return a.publishFor(sessionID, turnID, EventToolStarted, payload)
}

func (a *UIAdapter) OnToolResult(event ui.ToolResultEvent) error {
	if a == nil {
		return nil
	}
	finishedAt := a.now().UTC()
	a.mu.Lock()
	if err := a.ensureBoundLocked(); err != nil {
		a.mu.Unlock()
		return err
	}
	startedAt := a.toolStart[event.ToolUseID]
	delete(a.toolStart, event.ToolUseID)
	sessionID, turnID := a.sessionID, a.turnID
	detailID := a.storeDetailLocked("tool_result", event.Content, finishedAt)
	a.mu.Unlock()
	duration := int64(0)
	if !startedAt.IsZero() {
		duration = finishedAt.Sub(startedAt).Milliseconds()
	}
	if event.IsError {
		return a.publishFor(sessionID, turnID, EventToolFailed, ToolFailedPayload{
			ToolUseID: event.ToolUseID, Name: event.Name, ErrorCode: "tool_error",
			Message: summarizeEventText(event.Content, 240), DetailID: detailID, FinishedAt: finishedAt,
		})
	}
	return a.publishFor(sessionID, turnID, EventToolCompleted, ToolCompletedPayload{
		ToolUseID: event.ToolUseID, Name: event.Name, ResultSummary: summarizeEventText(event.Content, 240),
		DetailID: detailID, FinishedAt: finishedAt, DurationMS: duration,
	})
}

func (a *UIAdapter) OnSystemMessage(event ui.SystemEvent) error {
	return a.publish(EventSystemMessage, SystemMessagePayload{
		Level: "info", Code: strings.TrimSpace(event.Title), Title: strings.TrimSpace(event.Title), Body: strings.TrimSpace(event.Body),
	})
}

func (a *UIAdapter) OnDone() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	assistant := a.assistant
	a.mu.Unlock()
	if assistant != nil {
		assistant.Close()
		partID := stablePartID(a.turnID, "assistant", 0)
		part, _, err := a.coordinator.CompletePart(partID)
		if err != nil {
			return err
		}
		if err := a.publish(EventAssistantPartCompleted, AssistantPartCompletedPayload{PartID: partID, FinalLength: len(part.Text)}); err != nil {
			return err
		}
	}
	return a.currentError()
}

func (a *UIAdapter) ConsumesFileMutations() bool { return true }

func (a *UIAdapter) Detail(id string) (EventDetail, bool) {
	if a == nil {
		return EventDetail{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	detail, ok := a.details[id]
	return detail, ok
}

func (a *UIAdapter) ensureReasoning(partIndex int, redacted bool) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureBoundLocked(); err != nil {
		return err
	}
	if a.reasoning[partIndex] != nil {
		return a.lastErr
	}
	partID := stablePartID(a.turnID, "reasoning", partIndex)
	payload := ReasoningStartedPayload{PartID: partID, PartIndex: partIndex, Redacted: redacted}
	if err := a.startPartLocked(StreamingPart{PartID: partID, SessionID: a.sessionID, TurnID: a.turnID, Kind: "reasoning"}, EventReasoningStarted, payload); err != nil {
		return err
	}
	index := partIndex
	a.reasoning[partIndex] = NewDeltaBatcher(partID, nil, func(batch DeltaBatch) { a.emitReasoningBatch(index, batch) })
	return nil
}

func (a *UIAdapter) startPartLocked(part StreamingPart, eventType EventType, payload any) error {
	if _, err := a.coordinator.StartPart(part); err != nil {
		return err
	}
	return a.publishLocked(eventType, payload)
}

func (a *UIAdapter) emitAssistantBatch(batch DeltaBatch) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastErr != nil {
		return
	}
	if _, _, err := a.coordinator.AppendPart(batch.PartID, batch.Text); err != nil {
		a.lastErr = err
		return
	}
	a.lastErr = a.publishLocked(EventAssistantDelta, AssistantDeltaPayload{PartID: batch.PartID, Offset: batch.Offset, Text: batch.Text})
}

func (a *UIAdapter) emitReasoningBatch(_ int, batch DeltaBatch) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastErr != nil {
		return
	}
	if _, _, err := a.coordinator.AppendPart(batch.PartID, batch.Text); err != nil {
		a.lastErr = err
		return
	}
	a.lastErr = a.publishLocked(EventReasoningDelta, AssistantDeltaPayload{PartID: batch.PartID, Offset: batch.Offset, Text: batch.Text})
}

func (a *UIAdapter) publish(eventType EventType, payload any) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureBoundLocked(); err != nil {
		return err
	}
	return a.publishLocked(eventType, payload)
}

func (a *UIAdapter) publishLocked(eventType EventType, payload any) error {
	return a.publishForLocked(a.sessionID, a.turnID, eventType, payload)
}

func (a *UIAdapter) publishFor(sessionID, turnID string, eventType EventType, payload any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.publishForLocked(sessionID, turnID, eventType, payload)
}

func (a *UIAdapter) publishForLocked(sessionID, turnID string, eventType EventType, payload any) error {
	if a.hub == nil {
		return fmt.Errorf("event hub is unavailable")
	}
	version := uint64(0)
	if a.coordinator != nil {
		version = a.coordinator.SessionSnapshot(sessionID).SessionVersion
	}
	event, err := NewAppEvent(a.workspaceID, sessionID, turnID, eventType, a.now(), version, payload)
	if err != nil {
		return err
	}
	_, err = a.hub.Publish(event)
	return err
}

func (a *UIAdapter) ensureBoundLocked() error {
	if a.sessionID == "" || a.turnID == "" {
		return fmt.Errorf("UI adapter turn is not bound")
	}
	if a.coordinator == nil || a.hub == nil {
		return fmt.Errorf("UI adapter runtime is incomplete")
	}
	return nil
}

func (a *UIAdapter) currentError() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

func (a *UIAdapter) storeDetailLocked(kind, content string, at time.Time) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + content))
	id := "detail_" + hex.EncodeToString(sum[:12])
	a.details[id] = EventDetail{ID: id, Kind: kind, Content: content, CreatedAt: at}
	return id
}

func stablePartID(turnID, kind string, index int) string {
	return fmt.Sprintf("%s:%s:%d", turnID, kind, index)
}

func summarizeEventText(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return text
}

func toolTarget(input json.RawMessage) string {
	var values map[string]any
	if json.Unmarshal(input, &values) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "url", "command"} {
		if value, ok := values[key].(string); ok {
			return summarizeEventText(value, 120)
		}
	}
	return ""
}

var _ ui.UI = (*UIAdapter)(nil)
var _ ui.ThinkingDeltaReceiver = (*UIAdapter)(nil)
var _ ui.AssistantPartReceiver = (*UIAdapter)(nil)
var _ ui.SystemNotifier = (*UIAdapter)(nil)
var _ ui.FileMutationConsumer = (*UIAdapter)(nil)
