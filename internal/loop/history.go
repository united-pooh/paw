package loop

import (
	"context"
	"fmt"
	"log"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"strings"
	"sync"
)

func buildUserMessages(input string) []message.Message {
	return []message.Message{
		{
			Role:    message.RoleUser,
			Content: input,
		},
	}
}

// promptState 收敛系统提示增补与轮间 pending 输入，自带锁。
type promptState struct {
	mu               sync.RWMutex
	supplements      []pendingSupplement
	acceptingSteers  bool
	systemSupplement string
}

type pendingSupplement struct {
	content           string
	forceContinuation bool
}

func (p *promptState) appendSupplement(input string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.supplements = append(p.supplements, pendingSupplement{content: input})
}

func (p *promptState) beginSteerAdmission() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acceptingSteers = true
}

func (p *promptState) endSteerAdmission() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acceptingSteers = false
}

func (p *promptState) admitSteer(input string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.acceptingSteers {
		return false
	}
	p.supplements = append(p.supplements, pendingSupplement{
		content:           input,
		forceContinuation: true,
	})
	return true
}

func (p *promptState) trySealSteerAdmission() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, supplement := range p.supplements {
		if supplement.forceContinuation {
			return false
		}
	}
	p.acceptingSteers = false
	return true
}

func (p *promptState) pendingCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.supplements)
}

func (p *promptState) drainPending() []pendingSupplement {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.supplements) == 0 {
		return nil
	}
	supplements := append([]pendingSupplement(nil), p.supplements...)
	p.supplements = nil
	return supplements
}

func (p *promptState) drain() []string {
	return pendingSupplementContents(p.drainPending())
}

func (p *promptState) prependPending(supplements []pendingSupplement) {
	if len(supplements) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	combined := make([]pendingSupplement, 0, len(supplements)+len(p.supplements))
	combined = append(combined, supplements...)
	combined = append(combined, p.supplements...)
	p.supplements = combined
}

func (p *promptState) prepend(supplements []string) {
	p.prependPending(genericPendingSupplements(supplements))
}

func (p *promptState) setSystemSupplement(supplement string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systemSupplement = strings.TrimSpace(supplement)
}

func (p *promptState) currentSystemSupplement() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.systemSupplement
}

func (p *promptState) resetSupplements() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.supplements = nil
	p.acceptingSteers = false
}

func (runner *Engine) SubmitSupplement(input string) bool {
	input = strings.TrimSpace(input)
	if runner == nil || input == "" {
		return false
	}
	runner.promptCtx.appendSupplement(input)
	return true
}

func (runner *Engine) SubmitSteer(input string) bool {
	input = strings.TrimSpace(input)
	if runner == nil || input == "" {
		return false
	}
	return runner.promptCtx.admitSteer(input)
}

func (runner *Engine) PendingSupplementCount() int {
	if runner == nil {
		return 0
	}
	return runner.promptCtx.pendingCount()
}

func (runner *Engine) buildTurnHistory(input message.Message) ([]message.Message, []string) {
	runner.mu.RLock()
	history := message.CloneMessages(runner.history)
	runner.mu.RUnlock()
	supplements := runner.drainSupplements()
	history = append(history, buildSupplementMessages(supplements)...)
	history = append(history, input)
	return history, supplements
}

func (runner *Engine) appendPendingSupplements(history []message.Message) ([]message.Message, []pendingSupplement) {
	supplements := runner.drainPendingSupplements()
	if len(supplements) == 0 {
		return history, nil
	}
	return append(history, buildSupplementMessages(pendingSupplementContents(supplements))...), supplements
}

func (runner *Engine) drainPendingSupplements() []pendingSupplement {
	if runner == nil {
		return nil
	}
	return runner.promptCtx.drainPending()
}

func (runner *Engine) drainSupplements() []string {
	return pendingSupplementContents(runner.drainPendingSupplements())
}

func (runner *Engine) prependPendingSupplements(supplements []pendingSupplement) {
	if runner == nil || len(supplements) == 0 {
		return
	}
	runner.promptCtx.prependPending(supplements)
}

func (runner *Engine) prependSupplements(supplements []string) {
	runner.prependPendingSupplements(genericPendingSupplements(supplements))
}

func pendingSupplementContents(supplements []pendingSupplement) []string {
	if len(supplements) == 0 {
		return nil
	}
	contents := make([]string, 0, len(supplements))
	for _, supplement := range supplements {
		contents = append(contents, supplement.content)
	}
	return contents
}

func genericPendingSupplements(supplements []string) []pendingSupplement {
	if len(supplements) == 0 {
		return nil
	}
	pending := make([]pendingSupplement, 0, len(supplements))
	for _, supplement := range supplements {
		pending = append(pending, pendingSupplement{content: supplement})
	}
	return pending
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

func (runner *Engine) turnJournal() session.TurnJournal {
	if runner == nil || runner.store == nil {
		return nil
	}
	journal, _ := runner.store.(session.TurnJournal)
	return journal
}

func (runner *Engine) persistPartialAssistant(ctx context.Context, journal session.TurnJournal, turnID string, msg message.Message) error {
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

func (runner *Engine) resolveTurnID(timing *TurnTiming) (string, error) {
	if timing != nil && strings.TrimSpace(timing.TurnID) != "" {
		return strings.TrimSpace(timing.TurnID), nil
	}
	id, err := session.GenerateSessionID()
	if err != nil {
		return "", fmt.Errorf("生成 turn ID 失败: %w", err)
	}
	return "turn-" + id, nil
}

func (runner *Engine) currentHistory() []message.Message {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return message.CloneMessages(runner.history)
}

func (runner *Engine) setHistory(history []message.Message) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.history = message.CloneMessages(history)
	runner.mu.Unlock()
}

func (runner *Engine) syncContextUsageFromHistory(history []message.Message) {
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
	runner.usage.setContextKnown(usage, known)
}

func (runner *Engine) setRecovery(recovery *session.RecoveryState) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.recovery = copyRecoveryState(recovery)
	runner.mu.Unlock()
}

func (runner *Engine) setRecoveryIfNil(recovery *session.RecoveryState) {
	if runner == nil || recovery == nil {
		return
	}
	runner.mu.Lock()
	if runner.recovery == nil {
		runner.recovery = copyRecoveryState(recovery)
	}
	runner.mu.Unlock()
}

func (runner *Engine) takeRecovery() *session.RecoveryState {
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

func (runner *Engine) commitHistory(ctx context.Context, history []message.Message) (int64, error) {
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
	runner.history = message.CloneMessages(history)
	runner.mu.Unlock()
	return lastSeq, nil
}

func (runner *Engine) ResetHistory() {
	runner.mu.Lock()
	runner.history = []message.Message{}
	runner.recovery = nil
	runner.mu.Unlock()
	runner.usage.resetAll()
	runner.promptCtx.resetSupplements()
}

func (runner *Engine) LoadSession(ctx context.Context, sessionID string) (SessionLoadResult, error) {
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
		result.Messages = message.CloneMessages(snapshot.Messages)
		result.Recovery = copyRecoveryState(snapshot.Recovery)
		activeHistory = message.CloneMessages(snapshot.ActiveHistory)
	} else {
		messages, err := runner.store.LoadResolvedHistory(ctx, sessionID)
		if err != nil {
			return SessionLoadResult{}, err
		}
		result.Messages = message.CloneMessages(messages)
		activeHistory = message.CloneMessages(messages)
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
	runner.recovery = copyRecoveryState(result.Recovery)
	runner.mu.Unlock()
	runner.usage.resetSession()
	runner.promptCtx.resetSupplements()
	runner.syncContextUsageFromHistory(activeHistory)
	runner.hooks.dispatchSessionLoaded(sessionID)
	return result, nil
}

func (runner *Engine) LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	result, err := runner.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (runner *Engine) historyIsNil() bool {
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.history == nil
}

func (runner *Engine) setHistoryIfNil(history []message.Message) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.history == nil {
		runner.history = message.CloneMessages(history)
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
	if len(msg.AssistantParts) > 0 {
		calls := make([]message.ToolCall, 0)
		for _, part := range msg.AssistantParts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
		return calls
	}
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
