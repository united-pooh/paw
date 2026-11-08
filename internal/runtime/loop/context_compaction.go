package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"paw/internal/capability/model"
	"paw/internal/message"
)

const (
	defaultCompactRatio       = 0.80
	defaultCompactTargetRatio = 0.50
	defaultCompactTailTokens  = 16 * 1024
	minimumCompactMessages    = 2
	minimumRecentMessages     = 2
	maximumPinnedUserTokens   = 1500
	compactionMaxOutputTokens = 8192
)

const (
	compactionSummaryOpen  = "<compaction-summary>"
	compactionSummaryClose = "</compaction-summary>"
)

const compactionSummaryPrompt = `You are compacting the earlier part of a coding agent conversation.
Create a terse, reliable briefing under these headings, omitting empty headings:

## Standing facts & constraints
## Goal
## Decisions & rationale
## Files & code
## Commands & outcomes
## Errors & fixes
## Pending & next step

The original user task and subsequent explicit user updates are authoritative. Preserve the latest requested goal, delivery paths, acceptance conditions, and constraints. Do not infer a different task from exploratory commands, failed attempts, or an earlier summary.
Preserve exact executable and file paths, identifiers, versions, numbers, edits, verified command results, and unresolved work. Distinguish successful verification from failed or unavailable checks; do not turn an attempted command into a completed result. Retain the latest TODO statuses, dependencies, and external blockers without marking pending work completed.
Consolidate earlier summaries with the new work, retaining their still-relevant facts and removing repetition. Use bullets and fragments. Do not invent facts.
For this request, return only the briefing. Do not continue the task, answer the user, or call tools; tool schemas and prior tool exchanges are context only.`

const compactionTimeout = 90 * time.Second

type ContextCompactionResult struct {
	BeforeMessages       int
	AfterMessages        int
	FoldedMessages       int
	SnippedResults       int
	PrunedResults        int
	EstimatedTokensSaved int
	Summary              string
	ArchivePaths         []string
	Mechanical           bool
}

func initialContextLimitTokens(streamer ModelStreamer) int {
	if reader, ok := streamer.(modelConfigReader); ok {
		return model.EffectiveContextLimitTokens(reader.CurrentModelConfig())
	}
	return model.DefaultContextLimitTokens
}

// BindContextLimitSource reads a live effective limit whenever context pressure or
// default ContextStats needs it. While bound, the source takes precedence over
// SetContextLimitTokens, which only updates the unbound literal. Passing nil
// unbinds and restores that literal. A non-positive source result uses the model
// default (131072), never disables protection. The source runs outside engine
// locks, must be concurrency-safe, and must not recursively resolve engine limits.
func (runner *Engine) BindContextLimitSource(source func() int) {
	if runner == nil {
		return
	}
	runner.compact.bindLimitSource(source)
}

// SetContextLimitTokens sets the unbound automatic-compaction limit. A
// non-positive value disables automatic compaction only when no source is bound.
// It does not clear a bound source. The durable transcript is never rewritten:
// only the prompt projection used for future model calls is compacted.
func (runner *Engine) SetContextLimitTokens(limit int) {
	if runner == nil {
		return
	}
	runner.compact.setLimitTokens(limit)
}

func (runner *Engine) maybeCompactHistory(ctx context.Context, history []message.Message) ([]message.Message, *ContextCompactionResult, error) {
	if runner == nil || len(history) == 0 {
		return history, nil, nil
	}
	limit := runner.compact.limit()
	if limit <= 0 {
		return history, nil, nil
	}

	promptTokens := runner.contextPressureTokens(history)
	if promptTokens < int(float64(limit)*defaultCompactRatio) {
		return history, nil, nil
	}
	return runner.compactHistory(ctx, history, "", false)
}

// CompactContext manually compacts the in-memory model projection. The session
// journal is intentionally untouched, so exports, restores, and audit history
// retain every original message. focus is appended to the summarizer prompt.
func (runner *Engine) CompactContext(ctx context.Context, focus string) (ContextCompactionResult, error) {
	if runner == nil {
		return ContextCompactionResult{}, fmt.Errorf("runner is nil")
	}
	if runner.historyIsNil() && runner.store != nil {
		if journal := runner.turnJournal(); journal != nil {
			snapshot, err := journal.LoadSnapshot(ctx, runner.sessionID)
			if err != nil {
				return ContextCompactionResult{}, err
			}
			runner.setHistory(snapshot.ActiveHistory)
			runner.setRecoveryIfNil(snapshot.Recovery)
		} else {
			history, err := runner.store.LoadResolvedHistory(ctx, runner.sessionID)
			if err != nil {
				return ContextCompactionResult{}, err
			}
			runner.setHistory(history)
		}
	}
	history := runner.currentHistory()
	if len(history) == 0 {
		return ContextCompactionResult{}, fmt.Errorf("no conversation history to compact")
	}
	compacted, result, err := runner.compactHistory(ctx, history, focus, true)
	if err != nil {
		return ContextCompactionResult{}, err
	}
	if result == nil {
		return ContextCompactionResult{BeforeMessages: len(history), AfterMessages: len(history)}, nil
	}
	runner.setHistory(compacted)
	runner.syncContextUsageFromHistory(compacted)
	return *result, nil
}

func (runner *Engine) compactHistory(ctx context.Context, history []message.Message, focus string, force bool) ([]message.Message, *ContextCompactionResult, error) {
	return runner.compactHistoryWithProtectedTail(ctx, history, focus, force, -1)
}

func (runner *Engine) compactHistoryWithProtectedTail(ctx context.Context, history []message.Message, focus string, force bool, protectedTail int) ([]message.Message, *ContextCompactionResult, error) {
	limit := runner.contextLimit()
	head, tail := planHistoryCompaction(history, limit)
	minimum := minimumCompactMessages
	if force {
		minimum = 1
		head, tail = planManualHistoryCompaction(history, limit)
	}
	if protectedTail >= head && protectedTail < tail {
		tail = protectedTail
	}
	if tail-head < minimum {
		return history, nil, nil
	}
	region := history[head:tail]
	kept, fold := partitionCompactionRegionWithPolicy(region, limit, keepPolicy{errors: true, userMarked: true})
	if len(fold) == 0 {
		return history, nil, nil
	}
	// Fixed system/tool overhead can keep pressure high after a successful fold.
	// Rewriting the same small summary cannot make that overhead fit the window.
	if strings.TrimSpace(focus) == "" && len(fold) == 1 && isCompactionSummary(fold[0]) && estimateMessageTokens(fold) < minimumEconomicFoldTokens {
		return history, nil, nil
	}

	archiveRequests := foldArchiveRequests(fold, head)
	archive := runner.compact.currentArchive()
	if archive == nil {
		var archiveErr error
		archive, archiveErr = newCompactionArchive(runner.workRoot, runner.sessionID, runner.compact.currentMaintenance().archiveEnabled)
		if archiveErr != nil {
			return history, nil, archiveErr
		}
	}
	archived, archiveErr := archive.archive(archiveRequests)
	if archiveErr != nil {
		return history, nil, archiveErr
	}

	if retained := compactionRetainedContext(history, tail); retained != "" {
		focus = strings.TrimSpace(focus + "\n\n" + retained)
	}
	summary, _, err := runner.summarizeHistoryWithRetry(ctx, history[:tail], focus)
	mechanical := false
	if err != nil {
		mechanical = true
		summary = mechanicalFoldSummary(len(fold), archived.Paths)
		for _, msg := range fold {
			if isCompactionSummary(msg) {
				kept = append(kept, msg)
			}
		}
	}

	compacted := make([]message.Message, 0, head+len(kept)+1+len(history)-tail)
	compacted = append(compacted, history[:head]...)
	compacted = append(compacted, kept...)
	compacted = append(compacted, message.Message{
		Role: message.RoleUser,
		Content: compactionSummaryOpen + "\n" +
			"Summary of earlier conversation (older assistant/tool work was compacted to save context):\n" +
			strings.TrimSpace(summary) + "\n" + compactionSummaryClose,
	})
	compacted = append(compacted, history[tail:]...)
	runner.syncContextUsageFromHistory(compacted)
	return compacted, &ContextCompactionResult{
		BeforeMessages:       len(history),
		AfterMessages:        len(compacted),
		FoldedMessages:       len(fold),
		EstimatedTokensSaved: maxInt(0, estimateMessageTokens(history)-estimateMessageTokens(compacted)),
		Summary:              strings.TrimSpace(summary),
		ArchivePaths:         append([]string(nil), archived.Paths...),
		Mechanical:           mechanical,
	}, nil
}

func (runner *Engine) contextLimit() int {
	if runner == nil {
		return model.DefaultContextLimitTokens
	}
	limit := runner.compact.limit()
	if limit <= 0 {
		return initialContextLimitTokens(runner.model)
	}
	return limit
}

func planManualHistoryCompaction(history []message.Message, limit int) (head, tail int) {
	head = pinnedHistoryPrefix(history, limit)
	tail = len(history) - minimumRecentMessages
	if tail < head {
		tail = head
	}
	for tail > head && tail < len(history) && len(toolResultsFromMessage(history[tail])) > 0 {
		tail--
	}
	return head, tail
}

func latestToolCallGroupStart(history []message.Message, head int) int {
	for resultIndex := len(history) - 1; resultIndex >= head; resultIndex-- {
		results := toolResultsFromMessage(history[resultIndex])
		if len(results) == 0 {
			continue
		}
		if callIndex := assistantCallIndexForResults(history, head, resultIndex, results); callIndex >= head {
			return callIndex
		}
	}
	return len(history)
}

func planHistoryCompaction(history []message.Message, limit int) (head, tail int) {
	return planHistoryCompactionWithConfig(history, limit, defaultContextMaintenanceConfig())
}

func planHistoryCompactionWithConfig(history []message.Message, limit int, cfg contextMaintenanceConfig) (head, tail int) {
	head = pinnedHistoryPrefix(history, limit)
	budget := cfg.tailTokens
	if budget <= 0 {
		budget = defaultCompactTailTokens
	}
	targetRatio := cfg.compactTargetRatio
	if targetRatio <= 0 {
		targetRatio = defaultCompactTargetRatio
	}
	if byWindow := int(float64(limit) * targetRatio); byWindow < budget {
		budget = byWindow
	}
	return head, protectedTailStart(history, head, budget, minimumRecentMessages)
}

func pinnedHistoryPrefix(history []message.Message, limit int) int {
	i := 0
	if i < len(history) && history[i].Role == message.RoleSystem {
		i++
	}
	if i < len(history) && history[i].Role == message.RoleUser && !isCompactionSummary(history[i]) {
		budget := maximumPinnedUserTokens
		if byWindow := int(float64(limit) * 0.15); byWindow < budget {
			budget = byWindow
		}
		if estimateMessageTokens([]message.Message{history[i]}) <= budget {
			i++
		}
	}
	return i
}

func partitionCompactionRegion(region []message.Message, limit int) (kept, fold []message.Message) {
	userBudget := maximumPinnedUserTokens
	if byWindow := int(float64(limit) * 0.15); byWindow < userBudget {
		userBudget = byWindow
	}
	for _, msg := range region {
		if isCompactionSummary(msg) || (msg.Role == message.RoleUser && len(toolResultsFromMessage(msg)) == 0 && estimateMessageTokens([]message.Message{msg}) <= userBudget) {
			kept = append(kept, msg)
			continue
		}
		fold = append(fold, msg)
	}
	return kept, fold
}

func isCompactionSummary(msg message.Message) bool {
	return msg.Role == message.RoleUser && strings.HasPrefix(strings.TrimSpace(msg.Content), compactionSummaryOpen)
}

func estimateMessageTokens(messages []message.Message) int {
	total := 0
	for _, msg := range messages {
		total += 4
		total += estimateMessageContentTokens(msg)
		if origin := msg.GeneratedBy; msg.Role == message.RoleAssistant && origin != nil && origin.Transport == "openai-compatible" && model.UsesDeepSeekChatHistory(model.Config{
			Provider: origin.Provider, Model: origin.Model, Adapter: origin.Adapter, Transport: origin.Transport,
		}) {
			for _, part := range msg.AssistantParts {
				if part.Reasoning != nil && !part.Reasoning.Redacted {
					total += estimateTextTokens(part.Reasoning.Text)
				}
			}
		}
		for _, call := range toolCallsFromMessage(msg) {
			total += 8 + estimateTextTokens(call.ID) + estimateTextTokens(call.Name) + estimateTextTokens(string(call.Input))
		}
		for _, result := range toolResultsFromMessage(msg) {
			total += 8 + estimateTextTokens(result.ToolUseID) + estimateTextTokens(result.Content)
		}
	}
	return total
}

// estimateMessageContentTokens 与模型适配器保持一致：图片消息采用 Parts，
// 纯文本采用 Content，避免重复计算富消息的兼容文本。图片仍为本地粗估。
func estimateMessageContentTokens(msg message.Message) int {
	hasImage := false
	for _, part := range msg.Parts {
		if part.Type == message.ContentPartImage && part.Image != nil {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return estimateTextTokens(msg.Content)
	}
	total := 0
	for _, part := range msg.Parts {
		switch part.Type {
		case message.ContentPartText:
			total += estimateTextTokens(part.Text)
		case message.ContentPartImage:
			if part.Image != nil {
				total += 256
			}
		}
	}
	return total
}

func estimateTextTokens(text string) int {
	ascii, nonASCII := 0, 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII
}

func compactionRetainedContext(history []message.Message, tail int) string {
	var out strings.Builder
	for _, msg := range history[tail:] {
		if msg.Role == message.RoleUser && len(toolResultsFromMessage(msg)) == 0 && !isCompactionSummary(msg) {
			fmt.Fprintf(&out, "\n[Later user instruction]\n%s\n", msg.Content)
		}
	}
	toolNames := toolNamesByUseID(history)
latestTodo:
	for i := len(history) - 1; i >= tail; i-- {
		for _, result := range toolResultsFromMessage(history[i]) {
			if toolNames[result.ToolUseID] == "update_todo" && !result.IsError {
				fmt.Fprintf(&out, "\n[Latest retained TODO result]\n%s\n", result.Content)
				break latestTodo
			}
		}
	}
	if out.Len() == 0 {
		return ""
	}
	return "The following later instructions and task state remain verbatim after the summarized prefix. Use them to identify the current goal; they are subsequent context, not events from the earlier prefix. Later explicit user instructions take precedence over earlier summaries.\n" + out.String()
}

func (runner *Engine) summarizeHistory(ctx context.Context, history []message.Message, focus string) (string, *model.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, compactionTimeout)
	defer cancel()
	ctx = model.WithMaxOutputTokens(ctx, compactionMaxOutputTokens)
	instruction := compactionSummaryPrompt
	if focus = strings.TrimSpace(focus); focus != "" {
		instruction += "\n\nAdditional focus for this compaction (prioritize retaining it):\n" + focus
	}
	messages := []message.Message{
		{Role: message.RoleSystem, Content: instruction},
		{Role: message.RoleUser, Content: renderCompactionTranscript(history)},
	}
	var tools []model.ToolDefinition
	if runner.usesNativeToolCalling() {
		var err error
		turn, _ := ctx.Value(modelTurnStateContextKey{}).(*TurnState)
		messages, err = runner.buildModelMessages(ctx, history, turn)
		if err != nil {
			return "", nil, err
		}
		messages = append(messages, message.Message{Role: message.RoleUser, Content: instruction})
		tools = runner.modelToolDefinitions()
	}
	state := turnState{requestPurpose: "compaction"}
	state.traceStageID, state.traceAgentID = runner.currentTraceIDs()
	if state.traceStageID == "" {
		state.traceStageID = "compaction"
	}
	runner.beginUsageRequest(&state)
	events, err := runner.model.StreamMessage(runner.usageRequestContext(ctx, &state), messages, tools)
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	var usage *model.Usage
	complete := func() (string, *model.Usage, error) {
		summary := strings.TrimSpace(text.String())
		if summary == "" {
			return "", usage, fmt.Errorf("compaction summarizer returned empty output")
		}
		if compactionSummaryIsToolResponse(summary) {
			return "", usage, fmt.Errorf("compaction summarizer returned a tool call")
		}
		return summary, usage, nil
	}
	for {
		select {
		case <-ctx.Done():
			return "", usage, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return complete()
			}
			if event.Usage != nil {
				runner.recordUsageEvent(&state, *event.Usage)
				copyUsage := state.requestUsage
				usage = &copyUsage
			}
			if event.Err != nil {
				return "", usage, event.Err
			}
			if event.FinishReason == model.FinishReasonLength {
				return "", usage, fmt.Errorf("compaction summarizer output was truncated")
			}
			if len(event.ToolCalls) > 0 || event.FinishReason == model.FinishReasonToolCalls || (event.AssistantPart != nil && event.AssistantPart.Type == "tool_call") {
				return "", usage, fmt.Errorf("compaction summarizer returned a tool call")
			}
			if event.AssistantPart != nil {
				if event.AssistantPart.Type == "text" && event.AssistantPart.Lifecycle == model.AssistantPartLifecycleDelta {
					text.WriteString(event.AssistantPart.Delta)
				}
			} else {
				text.WriteString(event.Delta)
			}
			if event.Done {
				return complete()
			}
		}
	}
}

func compactionSummaryIsToolResponse(summary string) bool {
	payload := strings.TrimSpace(summary)
	if strings.HasPrefix(payload, "```") {
		body, end, ok := extractFenceBodyAt(payload, 0)
		if !ok || strings.TrimSpace(payload[end:]) != "" {
			return false
		}
		payload = strings.TrimSpace(body)
	}
	if _, ok := decodeToolUseEnvelopes(payload); ok {
		return true
	}
	if toolUseProtocolStart.MatchString(payload) || dsmlProtocolStart.MatchString(payload) {
		return true
	}
	lower := strings.ToLower(payload)
	return strings.HasPrefix(lower, "<invoke") || strings.HasPrefix(lower, "<tool_call") ||
		strings.HasPrefix(lower, "<tool ") || strings.HasPrefix(lower, "<tool>")
}

func (runner *Engine) summarizeHistoryWithRetry(ctx context.Context, history []message.Message, focus string) (string, *model.Usage, error) {
	summary, usage, err := runner.summarizeHistory(ctx, history, focus)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return summary, usage, err
	}
	summary, retryUsage, err := runner.summarizeHistory(ctx, history, focus)
	if usage == nil {
		return summary, retryUsage, err
	}
	total := usage.Breakdown()
	if retryUsage != nil {
		total = total.Add(retryUsage.Breakdown())
	}
	combined := total.ModelUsage()
	return summary, &combined, err
}

func foldArchiveRequests(fold []message.Message, offset int) []archiveRequest {
	requests := make([]archiveRequest, 0, len(fold))
	for index, msg := range fold {
		content, _ := json.Marshal(msg)
		requests = append(requests, archiveRequest{
			Operation:       "fold",
			MessageIndex:    offset + index,
			ToolResultIndex: -1,
			ToolUseID:       fmt.Sprintf("fold-message-%d", offset+index),
			OriginalBytes:   len(content),
			Message:         cloneMessage(msg),
			OriginalContent: string(content),
		})
	}
	return requests
}

func mechanicalFoldSummary(folded int, archivePaths []string) string {
	archiveNote := "The original messages were not archived."
	if len(archivePaths) > 0 {
		archiveNote = "The complete original messages were archived before folding."
	}
	return fmt.Sprintf("%d earlier message(s) were folded because the automatic summary was unavailable. %s Ask the user if details from before this point are needed.", folded, archiveNote)
}

func renderCompactionTranscript(history []message.Message) string {
	var out strings.Builder
	for _, msg := range history {
		switch {
		case len(toolCallsFromMessage(msg)) > 0:
			for _, call := range toolCallsFromMessage(msg) {
				fmt.Fprintf(&out, "[assistant calls %s] %s\n", call.Name, summarizeCompactionToolInput(call.Input))
			}
			out.WriteByte('\n')
		case len(toolResultsFromMessage(msg)) > 0:
			for _, result := range toolResultsFromMessage(msg) {
				fmt.Fprintf(&out, "[tool result %s error=%t]\n%s\n\n", result.ToolUseID, result.IsError, result.Content)
			}
		default:
			fmt.Fprintf(&out, "[%s]\n%s\n\n", msg.Role, msg.Content)
		}
	}
	return out.String()
}

func summarizeCompactionToolInput(input json.RawMessage) string {
	if len(input) == 0 {
		return "{}"
	}
	var object map[string]any
	if err := json.Unmarshal(input, &object); err != nil {
		return fmt.Sprintf("(%d bytes)", len(input))
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "{" + strings.Join(keys, ", ") + "}"
}
