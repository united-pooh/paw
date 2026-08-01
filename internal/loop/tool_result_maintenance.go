package loop

import (
	"strings"

	"paw/internal/message"
)

type keepPolicy struct {
	errors     bool
	userMarked bool
}

func isErrorToolResult(result message.ToolResult) bool {
	if result.IsError {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(result.Content))
	return strings.HasPrefix(text, "error:") || strings.HasPrefix(text, "blocked:")
}

func isUserMarkedMessage(msg message.Message) bool {
	if msg.Role != message.RoleUser || len(toolResultsFromMessage(msg)) != 0 {
		return false
	}
	text := strings.ToLower(strings.TrimLeft(msg.Content, " \t\r\n"))
	for _, marker := range []string{"[[keep]]", "[keep]", "<keep>", "<!-- keep -->"} {
		if strings.HasPrefix(text, marker) {
			return true
		}
	}
	return false
}

func keepMessageIndexes(history []message.Message, policy keepPolicy) map[int]bool {
	keep := make(map[int]bool)
	start := latestCompactionSummaryEnd(history)
	for i := start; i < len(history); i++ {
		msg := history[i]
		if policy.userMarked && isUserMarkedMessage(msg) {
			keep[i] = true
		}
		if !policy.errors {
			continue
		}
		results := toolResultsFromMessage(msg)
		if len(results) == 0 || !containsErrorToolResult(results) {
			continue
		}
		keep[i] = true
		if callIndex := assistantCallIndexForResults(history, start, i, results); callIndex >= 0 {
			keep[callIndex] = true
			// A multi-tool result message is one protocol unit. Marking the whole
			// message protects all sibling results together with the assistant call.
			if callIndex+1 < len(history) && len(toolResultsFromMessage(history[callIndex+1])) > 0 {
				keep[callIndex+1] = true
			}
		}
	}
	return keep
}

func latestCompactionSummaryEnd(history []message.Message) int {
	start := 0
	for i := range history {
		if isCompactionSummary(history[i]) {
			start = i + 1
		}
	}
	return start
}

func containsErrorToolResult(results []message.ToolResult) bool {
	for _, result := range results {
		if isErrorToolResult(result) {
			return true
		}
	}
	return false
}

func assistantCallIndexForResults(history []message.Message, start, resultIndex int, results []message.ToolResult) int {
	ids := make(map[string]bool, len(results))
	for _, result := range results {
		ids[result.ToolUseID] = true
	}
	for i := resultIndex - 1; i >= start; i-- {
		calls := toolCallsFromMessage(history[i])
		if len(calls) == 0 {
			continue
		}
		for _, call := range calls {
			if ids[call.ID] {
				return i
			}
		}
	}
	return -1
}

func protectedTailStart(history []message.Message, head, budgetTokens, minMessages int) int {
	if head < 0 {
		head = 0
	}
	if head > len(history) {
		head = len(history)
	}
	if budgetTokens < 1 {
		budgetTokens = 1
	}
	if minMessages < 1 {
		minMessages = 1
	}

	tail := len(history)
	used := 0
	for i := len(history) - 1; i >= head; i-- {
		cost := estimateMessageTokens([]message.Message{history[i]})
		keptCount := len(history) - i
		if keptCount > minMessages && used+cost > budgetTokens {
			break
		}
		used += cost
		tail = i
	}
	return moveTailBeforeToolCallGroup(history, head, tail)
}

func moveTailBeforeToolCallGroup(history []message.Message, head, tail int) int {
	if tail < head {
		return head
	}
	if tail >= len(history) || len(toolResultsFromMessage(history[tail])) == 0 {
		return tail
	}
	results := toolResultsFromMessage(history[tail])
	if callIndex := assistantCallIndexForResults(history, head, tail, results); callIndex >= head {
		return callIndex
	}
	for tail > head && len(toolResultsFromMessage(history[tail])) > 0 {
		tail--
	}
	return tail
}

func partitionCompactionRegionWithPolicy(region []message.Message, limit int, policy keepPolicy) (kept, fold []message.Message) {
	userBudget := maximumPinnedUserTokens
	if byWindow := int(float64(limit) * 0.15); byWindow < userBudget {
		userBudget = byWindow
	}
	protected := keepMessageIndexes(region, policy)
	for i, msg := range region {
		if protected[i] || isCompactionSummary(msg) ||
			(msg.Role == message.RoleUser && len(toolResultsFromMessage(msg)) == 0 && estimateMessageTokens([]message.Message{msg}) <= userBudget) {
			kept = append(kept, msg)
			continue
		}
		fold = append(fold, msg)
	}
	return kept, fold
}
