package loop

import (
	"context"
	"fmt"
	"log"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"strings"
)

func buildUserMessages(input string) []message.Message {
	return []message.Message{
		{
			Role:    message.RoleUser,
			Content: input,
		},
	}
}

func (runner *Runner) SubmitSupplement(input string) bool {
	input = strings.TrimSpace(input)
	if runner == nil || input == "" {
		return false
	}
	runner.mu.Lock()
	runner.supplements = append(runner.supplements, input)
	runner.mu.Unlock()
	return true
}

func (runner *Runner) PendingSupplementCount() int {
	if runner == nil {
		return 0
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return len(runner.supplements)
}

func (runner *Runner) buildTurnHistory(input message.Message) ([]message.Message, []string) {
	runner.mu.RLock()
	history := append([]message.Message(nil), runner.history...)
	runner.mu.RUnlock()
	supplements := runner.drainSupplements()
	history = append(history, buildSupplementMessages(supplements)...)
	history = append(history, input)
	return history, supplements
}

func (runner *Runner) appendPendingSupplements(history []message.Message) ([]message.Message, []string) {
	supplements := runner.drainSupplements()
	if len(supplements) == 0 {
		return history, nil
	}
	return append(history, buildSupplementMessages(supplements)...), supplements
}

func (runner *Runner) drainSupplements() []string {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.supplements) == 0 {
		return nil
	}
	supplements := append([]string(nil), runner.supplements...)
	runner.supplements = nil
	return supplements
}

func (runner *Runner) prependSupplements(supplements []string) {
	if runner == nil || len(supplements) == 0 {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	combined := make([]string, 0, len(supplements)+len(runner.supplements))
	combined = append(combined, supplements...)
	combined = append(combined, runner.supplements...)
	runner.supplements = combined
}

func buildSupplementMessages(supplements []string) []message.Message {
	messages := make([]message.Message, 0, len(supplements))
	for _, supplement := range supplements {
		supplement = strings.TrimSpace(supplement)
		if supplement == "" {
			continue
		}
		messages = append(messages, message.Message{
			Role:    message.RoleUser,
			Content: "Supplemental instruction submitted while this turn was running:\n" + supplement,
		})
	}
	return messages
}

const recoveryMessagePrefix = "[Paw recovery]"

func (runner *Runner) turnJournal() session.TurnJournal {
	if runner == nil || runner.store == nil {
		return nil
	}
	journal, _ := runner.store.(session.TurnJournal)
	return journal
}

func (runner *Runner) persistPartialAssistant(ctx context.Context, journal session.TurnJournal, turnID string, msg message.Message) error {
	if journal == nil || msg.Role != message.RoleAssistant || strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	partialJournal, ok := journal.(session.PartialAssistantJournal)
	if !ok {
		return nil
	}
	if err := partialJournal.AppendPartialAssistant(ctx, runner.sessionID, turnID, msg); err != nil {
		return fmt.Errorf("保存 partial assistant turn 失败: %w", err)
	}
	return nil
}

func (runner *Runner) resolveTurnID(timing *TurnTiming) (string, error) {
	if timing != nil && strings.TrimSpace(timing.TurnID) != "" {
		return strings.TrimSpace(timing.TurnID), nil
	}
	id, err := session.GenerateSessionID()
	if err != nil {
		return "", fmt.Errorf("生成 turn ID 失败: %w", err)
	}
	return "turn-" + id, nil
}

func (runner *Runner) currentHistory() []message.Message {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return append([]message.Message(nil), runner.history...)
}

func (runner *Runner) setHistory(history []message.Message) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.history = append([]message.Message(nil), history...)
	runner.mu.Unlock()
}

func (runner *Runner) syncContextUsageFromHistory(history []message.Message) {
	if runner == nil {
		return
	}
	usage := model.Usage{}
	known := len(history) > 0
	if known {
		messages := make([]message.Message, 0, len(history)+1)
		messages = append(messages, buildSystemMessage(runner.buildSystemPrompt()))
		messages = append(messages, history...)
		usage = usageFromTotals(tokenUsageTotals{used: estimateMessageTokens(messages)})
	}
	runner.mu.Lock()
	runner.usage = usage
	runner.usageKnown = known
	runner.mu.Unlock()
}

func (runner *Runner) setRecovery(recovery *session.RecoveryState) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.recovery = copyRecoveryState(recovery)
	runner.mu.Unlock()
}

func (runner *Runner) setRecoveryIfNil(recovery *session.RecoveryState) {
	if runner == nil || recovery == nil {
		return
	}
	runner.mu.Lock()
	if runner.recovery == nil {
		runner.recovery = copyRecoveryState(recovery)
	}
	runner.mu.Unlock()
}

func (runner *Runner) takeRecovery() *session.RecoveryState {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	recovery := copyRecoveryState(runner.recovery)
	runner.recovery = nil
	return recovery
}

func copyRecoveryState(recovery *session.RecoveryState) *session.RecoveryState {
	if recovery == nil {
		return nil
	}
	copyValue := *recovery
	copyValue.CompletedToolResults = append([]message.ToolResult(nil), recovery.CompletedToolResults...)
	copyValue.DroppedToolCalls = append([]message.ToolCall(nil), recovery.DroppedToolCalls...)
	return &copyValue
}

func recoveryMessage(recovery *session.RecoveryState) message.Message {
	if recovery == nil {
		return message.Message{}
	}
	var builder strings.Builder
	builder.WriteString(recoveryMessagePrefix)
	builder.WriteString("\nThe previous Paw turn ended before completion.\n")
	if errText := strings.TrimSpace(recovery.Error); errText != "" {
		builder.WriteString("Failure: ")
		builder.WriteString(errText)
		builder.WriteByte('\n')
	}
	if len(recovery.CompletedToolResults) > 0 {
		builder.WriteString("Completed tool results are authoritative; do not repeat them:\n")
		for _, result := range recovery.CompletedToolResults {
			builder.WriteString("- ")
			builder.WriteString(result.ToolUseID)
			builder.WriteString(": ")
			builder.WriteString(result.Content)
			builder.WriteByte('\n')
		}
	}
	if len(recovery.DroppedToolCalls) > 0 {
		builder.WriteString("The following tool calls were not completed and may be reconsidered only if needed:\n")
		for _, call := range recovery.DroppedToolCalls {
			builder.WriteString("- ")
			builder.WriteString(call.Name)
			builder.WriteString(" (")
			builder.WriteString(call.ID)
			builder.WriteString(")\n")
		}
	}
	builder.WriteString("Continue from the recorded state and answer the new user request.")
	return message.Message{Role: message.RoleUser, Content: builder.String()}
}

func insertRecoveryMessage(history []message.Message, recovery *session.RecoveryState) []message.Message {
	if recovery == nil {
		return history
	}
	msg := recoveryMessage(recovery)
	if msg.Content == "" {
		return history
	}
	insertAt := len(history)
	if len(history) > 0 && history[len(history)-1].Role == message.RoleUser {
		insertAt = len(history) - 1
	}
	result := make([]message.Message, 0, len(history)+1)
	result = append(result, history[:insertAt]...)
	result = append(result, msg)
	result = append(result, history[insertAt:]...)
	return result
}

func stripRecoveryMessages(history []message.Message) []message.Message {
	result := make([]message.Message, 0, len(history))
	for _, msg := range history {
		if msg.Role == message.RoleUser && strings.HasPrefix(strings.TrimSpace(msg.Content), recoveryMessagePrefix) {
			continue
		}
		result = append(result, msg)
	}
	return result
}

func (runner *Runner) commitHistory(ctx context.Context, history []message.Message) (int64, error) {
	lastSeq := int64(-1)
	if runner.store != nil {
		// 在更新 runner.history 之前，先用旧长度算出本轮新增的消息。
		// runner.history 是上一轮结束时的状态，history 是本轮完整状态。
		runner.mu.RLock()
		prevLen := len(runner.history)
		runner.mu.RUnlock()
		newMsgs := history[prevLen:]
		if len(newMsgs) > 0 {
			if sequencedStore, ok := runner.store.(SequencedHistoryStore); ok {
				var err error
				_, lastSeq, err = sequencedStore.AppendWithSequences(ctx, runner.sessionID, newMsgs...)
				if err != nil {
					return -1, fmt.Errorf("保存会话历史失败: %w", err)
				}
			} else if err := runner.store.Append(ctx, runner.sessionID, newMsgs...); err != nil {
				return -1, fmt.Errorf("保存会话历史失败: %w", err)
			}
		}
	}

	// 持久化成功后再更新内存副本。
	runner.mu.Lock()
	runner.history = append([]message.Message(nil), history...)
	runner.mu.Unlock()
	return lastSeq, nil
}

func (runner *Runner) ResetHistory() {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.history = []message.Message{}
	runner.usage = model.Usage{}
	runner.usageKnown = false
	runner.sessionUsage = model.Usage{}
	runner.sessionUsageKnown = false
	runner.supplements = nil
	runner.recovery = nil
}

func (runner *Runner) LoadSession(ctx context.Context, sessionID string) (SessionLoadResult, error) {
	if runner.store == nil {
		return SessionLoadResult{}, fmt.Errorf("runner store is nil")
	}
	var result SessionLoadResult
	var activeHistory []message.Message
	if journal := runner.turnJournal(); journal != nil {
		snapshot, err := journal.LoadSnapshot(ctx, sessionID)
		if err != nil {
			return SessionLoadResult{}, err
		}
		result.Messages = append([]message.Message(nil), snapshot.Messages...)
		result.Recovery = copyRecoveryState(snapshot.Recovery)
		activeHistory = append([]message.Message(nil), snapshot.ActiveHistory...)
	} else {
		messages, err := runner.store.LoadResolvedHistory(ctx, sessionID)
		if err != nil {
			return SessionLoadResult{}, err
		}
		result.Messages = append([]message.Message(nil), messages...)
		activeHistory = append([]message.Message(nil), messages...)
	}
	// 会话加载时修复工具调用配对（对齐 CodeWhale session_manager 加载修复）：
	// 崩溃/中断后持久化的历史可能带孤儿 tool result 或悬空 tool_use，
	// 先修复再交给后续轮次使用，避免出站请求触发配对类 400。
	activeHistory, repairStats := model.RepairToolCallPairs(activeHistory)
	if repairStats.RepairedToolCalls > 0 || repairStats.OrphanedResults > 0 {
		log.Printf("tool history repair on session load: repaired=%d orphaned=%d repaired_ids=%v orphaned_ids=%v",
			repairStats.RepairedToolCalls, repairStats.OrphanedResults,
			repairStats.RepairedCallIDs, repairStats.OrphanedResultIDs)
		result.Messages = activeHistory
	}
	runner.setHistory(activeHistory)
	runner.mu.Lock()
	runner.sessionID = sessionID
	runner.sessionUsage = model.Usage{}
	runner.sessionUsageKnown = false
	runner.supplements = nil
	runner.recovery = copyRecoveryState(result.Recovery)
	runner.mu.Unlock()
	runner.syncContextUsageFromHistory(activeHistory)
	runner.mu.RLock()
	hooks := append([]SessionLoadedHook(nil), runner.sessionLoadedHooks...)
	runner.mu.RUnlock()
	for _, hook := range hooks {
		hook(sessionID)
	}
	return result, nil
}

func (runner *Runner) LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	result, err := runner.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (runner *Runner) historyIsNil() bool {
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.history == nil
}

func (runner *Runner) setHistoryIfNil(history []message.Message) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.history == nil {
		runner.history = append([]message.Message(nil), history...)
	}
}

func buildSystemMessage(content string) message.Message {
	return message.Message{
		Role:    message.RoleSystem,
		Content: content,
	}
}

func buildAssistantMessage(content string) message.Message {
	return message.Message{
		Role:    message.RoleAssistant,
		Content: content,
	}
}

func buildToolResultMessage(toolUseID, content string, isError bool) message.Message {
	return message.Message{
		Role: message.RoleUser,
		ToolResult: &message.ToolResult{
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		},
	}
}

func buildToolResultsMessage(results []message.ToolResult) message.Message {
	msg := message.Message{Role: message.RoleUser}
	if len(results) == 0 {
		return msg
	}
	if len(results) == 1 {
		result := results[0]
		msg.ToolResult = &result
		return msg
	}
	msg.ToolResults = append([]message.ToolResult(nil), results...)
	return msg
}

func toolCallsFromMessage(msg message.Message) []message.ToolCall {
	if len(msg.ToolUses) > 0 {
		return append([]message.ToolCall(nil), msg.ToolUses...)
	}
	if msg.ToolUse == nil {
		return nil
	}
	return []message.ToolCall{*msg.ToolUse}
}

func toolResultsFromMessage(msg message.Message) []message.ToolResult {
	if len(msg.ToolResults) > 0 {
		return append([]message.ToolResult(nil), msg.ToolResults...)
	}
	if msg.ToolResult == nil {
		return nil
	}
	return []message.ToolResult{*msg.ToolResult}
}
