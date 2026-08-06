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

	"paw/internal/message"
	"paw/internal/model"
)

const (
	defaultCompactRatio       = 0.80
	defaultCompactTargetRatio = 0.50
	defaultCompactTailTokens  = 16 * 1024
	minimumCompactMessages    = 2
	minimumRecentMessages     = 2
	maximumPinnedUserTokens   = 1500
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

Preserve paths, identifiers, versions, numbers, user constraints, edits, command results, and unresolved work exactly. Use bullets and fragments. Do not invent facts.`

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

// SetContextLimitTokens enables automatic model-history compaction. A non-positive
// value disables it. The durable transcript is never rewritten: only the prompt
// projection used for future model calls is compacted.
func (runner *Runner) SetContextLimitTokens(limit int) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.contextLimitTokens = maxInt(0, limit)
	runner.mu.Unlock()
}

func (runner *Runner) maybeCompactHistory(ctx context.Context, history []message.Message) ([]message.Message, *ContextCompactionResult, error) {
	if runner == nil || len(history) == 0 {
		return history, nil, nil
	}
	runner.mu.RLock()
	limit := runner.contextLimitTokens
	usage := runner.usage
	usageKnown := runner.usageKnown
	runner.mu.RUnlock()
	if limit <= 0 {
		return history, nil, nil
	}

	promptTokens := estimateMessageTokens(history)
	if usageKnown && usage.PromptTokenCount() > promptTokens {
		promptTokens = usage.PromptTokenCount()
	}
	if promptTokens < int(float64(limit)*defaultCompactRatio) {
		return history, nil, nil
	}
	return runner.compactHistory(ctx, history, "", false)
}

// CompactContext manually compacts the in-memory model projection. The session
// journal is intentionally untouched, so exports, restores, and audit history
// retain every original message. focus is appended to the summarizer prompt.
func (runner *Runner) CompactContext(ctx context.Context, focus string) (ContextCompactionResult, error) {
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

func (runner *Runner) compactHistory(ctx context.Context, history []message.Message, focus string, force bool) ([]message.Message, *ContextCompactionResult, error) {
	return runner.compactHistoryWithProtectedTail(ctx, history, focus, force, -1)
}

func (runner *Runner) compactHistoryWithProtectedTail(ctx context.Context, history []message.Message, focus string, force bool, protectedTail int) ([]message.Message, *ContextCompactionResult, error) {
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

	archiveRequests := foldArchiveRequests(fold, head)
	archive := runner.compactionArchive
	if archive == nil {
		var archiveErr error
		archive, archiveErr = newCompactionArchive(runner.workRoot, runner.sessionID, runner.contextMaintenance.archiveEnabled)
		if archiveErr != nil {
			return history, nil, archiveErr
		}
	}
	archived, archiveErr := archive.archive(archiveRequests)
	if archiveErr != nil {
		return history, nil, archiveErr
	}

	summary, summaryUsage, err := runner.summarizeHistoryWithRetry(ctx, fold, focus)
	mechanical := false
	if err != nil {
		mechanical = true
		summary = mechanicalFoldSummary(len(fold), archived.Paths)
	}
	if summaryUsage != nil {
		runner.addSessionUsage(usageTotalsFromUsage(*summaryUsage, true))
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

func (runner *Runner) contextLimit() int {
	if runner == nil {
		return model.DefaultContextLimitTokens
	}
	runner.mu.RLock()
	limit := runner.contextLimitTokens
	runner.mu.RUnlock()
	if limit <= 0 {
		return model.DefaultContextLimitTokens
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
	for i < len(history) && isCompactionSummary(history[i]) {
		i++
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
		total += estimateTextTokens(msg.Content)
		for _, part := range msg.Parts {
			total += estimateTextTokens(part.Text)
			if part.Image != nil {
				// Images are provider-specific; reserve a modest fixed budget rather
				// than treating their transient binary bytes as prompt text.
				total += 256
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

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	byBytes := (len(text) + 3) / 4
	if runes := utf8.RuneCountInString(text); runes > byBytes {
		return runes
	}
	return byBytes
}

func (runner *Runner) summarizeHistory(ctx context.Context, history []message.Message, focus string) (string, *model.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, compactionTimeout)
	defer cancel()
	systemPrompt := compactionSummaryPrompt
	if focus = strings.TrimSpace(focus); focus != "" {
		systemPrompt += "\n\nAdditional focus for this compaction (prioritize retaining it):\n" + focus
	}
	events, err := runner.model.StreamMessage(ctx, []message.Message{
		{Role: message.RoleSystem, Content: systemPrompt},
		{Role: message.RoleUser, Content: renderCompactionTranscript(history)},
	}, nil)
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	var usage *model.Usage
	for {
		select {
		case <-ctx.Done():
			return "", usage, ctx.Err()
		case event, ok := <-events:
			if !ok {
				summary := strings.TrimSpace(text.String())
				if summary == "" {
					return "", usage, fmt.Errorf("compaction summarizer returned empty output")
				}
				return summary, usage, nil
			}
			if event.Err != nil {
				return "", usage, event.Err
			}
			text.WriteString(event.Delta)
			if event.Usage != nil {
				copyUsage := *event.Usage
				usage = &copyUsage
			}
			if event.Done {
				summary := strings.TrimSpace(text.String())
				if summary == "" {
					return "", usage, fmt.Errorf("compaction summarizer returned empty output")
				}
				return summary, usage, nil
			}
		}
	}
}

func (runner *Runner) summarizeHistoryWithRetry(ctx context.Context, history []message.Message, focus string) (string, *model.Usage, error) {
	summary, usage, err := runner.summarizeHistory(ctx, history, focus)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return summary, usage, err
	}
	return runner.summarizeHistory(ctx, history, focus)
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
