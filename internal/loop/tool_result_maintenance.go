package loop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"paw/internal/message"
	"paw/internal/tool"
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
	// The latest accepted Todo snapshot is durable state, not disposable
	// transcript detail. Keep its protocol pair in every projection so summary
	// and tool-result maintenance cannot silently change the model's current
	// task list before the next state refresh.
	toolNames := toolNamesByUseID(history)
	for resultIndex := len(history) - 1; resultIndex >= start; resultIndex-- {
		for _, result := range toolResultsFromMessage(history[resultIndex]) {
			if toolNames[strings.TrimSpace(result.ToolUseID)] != "update_todo" || result.IsError {
				continue
			}
			keep[resultIndex] = true
			if callIndex := assistantCallIndexForResults(history, start, resultIndex, []message.ToolResult{result}); callIndex >= start {
				keep[callIndex] = true
			}
			return keep
		}
	}
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

const (
	snippedToolResultMarker = "[snipped tool result — "
	prunedToolResultMarker  = "[elided tool result — "
)

type maintenanceMode uint8

const (
	maintenanceSnip maintenanceMode = iota + 1
	maintenancePrune
)

type maintenanceStats struct {
	results    int
	savedChars int
	archives   []string
}

type maintenanceRequest struct {
	mode      maintenanceMode
	tailStart int
	minBytes  int
	policy    keepPolicy
	archive   *compactionArchive
	registry  *tool.Registry
}

type snipStrategy struct {
	head, tail           int
	headChars, tailChars int
}

var defaultReadOnlySnip = snipStrategy{head: 80, tail: 12, headChars: 10000, tailChars: 2000}
var defaultSideEffectingSnip = snipStrategy{head: 40, tail: 40, headChars: 8000, tailChars: 8000}

type toolResultMarker struct {
	kind          maintenanceMode
	toolName      string
	originalBytes int
	archivePath   string
}

type markerPayload struct {
	Tool          string `json:"tool"`
	OriginalBytes int    `json:"original_bytes"`
	Archive       string `json:"archive"`
}

type maintenanceCandidate struct {
	messageIndex int
	resultIndex  int
	toolName     string
	content      string
	marker       toolResultMarker
	marked       bool
}

func maintainToolResults(history []message.Message, request maintenanceRequest) ([]message.Message, maintenanceStats, error) {
	if request.tailStart < 0 {
		request.tailStart = 0
	}
	if request.tailStart > len(history) {
		request.tailStart = len(history)
	}
	protected := keepMessageIndexes(history, request.policy)
	toolNames := toolNamesByUseID(history)
	var candidates []maintenanceCandidate
	var archiveRequests []archiveRequest
	for messageIndex := 0; messageIndex < request.tailStart; messageIndex++ {
		if protected[messageIndex] {
			continue
		}
		results := toolResultsFromMessage(history[messageIndex])
		for resultIndex, result := range results {
			marker, marked := parseToolResultMarker(result.Content)
			if marked {
				if marker.kind == maintenancePrune || request.mode == maintenanceSnip {
					continue
				}
				candidates = append(candidates, maintenanceCandidate{
					messageIndex: messageIndex, resultIndex: resultIndex,
					toolName: marker.toolName, content: result.Content, marker: marker, marked: true,
				})
				continue
			}
			if len([]byte(result.Content)) < request.minBytes {
				continue
			}
			toolName := toolNames[result.ToolUseID]
			candidate := maintenanceCandidate{messageIndex: messageIndex, resultIndex: resultIndex, toolName: toolName, content: result.Content}
			candidates = append(candidates, candidate)
			archiveRequests = append(archiveRequests, archiveRequest{
				Operation: operationForMode(request.mode), MessageIndex: messageIndex, ToolResultIndex: resultIndex,
				ToolUseID: result.ToolUseID, ToolName: toolName, OriginalBytes: len([]byte(result.Content)),
				Message: cloneMessage(history[messageIndex]), OriginalContent: result.Content,
			})
		}
	}
	if len(candidates) == 0 {
		return history, maintenanceStats{}, nil
	}

	archivePaths := make(map[string]string)
	var archiveList []string
	if len(archiveRequests) > 0 {
		archived, err := request.archive.archive(archiveRequests)
		if err != nil {
			return history, maintenanceStats{}, err
		}
		archiveList = append(archiveList, archived.Paths...)
		for _, item := range archiveRequests {
			key := archiveKey(request.archive.sessionID, item.ToolUseID, item.OriginalContent)
			archivePaths[candidateKey(item.MessageIndex, item.ToolResultIndex)] = archived.ByKey[key]
		}
	}

	result := cloneMessages(history)
	stats := maintenanceStats{archives: append([]string(nil), archiveList...)}
	seenArchives := make(map[string]bool)
	for _, path := range stats.archives {
		seenArchives[path] = true
	}
	for _, candidate := range candidates {
		results := toolResultsFromMessage(result[candidate.messageIndex])
		before := results[candidate.resultIndex].Content
		var after string
		if candidate.marked {
			after = pruneToolResult(candidate.marker.toolName, "", candidate.marker.archivePath, candidate.marker.originalBytes)
			if candidate.marker.archivePath != "" && !seenArchives[candidate.marker.archivePath] {
				seenArchives[candidate.marker.archivePath] = true
				stats.archives = append(stats.archives, candidate.marker.archivePath)
			}
		} else {
			archivePath := archivePaths[candidateKey(candidate.messageIndex, candidate.resultIndex)]
			if request.mode == maintenancePrune {
				after = pruneToolResult(candidate.toolName, candidate.content, archivePath, len([]byte(candidate.content)))
			} else {
				after = snipToolResult(candidate.toolName, candidate.content, archivePath, snipStrategyFor(request.registry, candidate.toolName))
			}
		}
		results[candidate.resultIndex].Content = after
		setToolResultsOnMessage(&result[candidate.messageIndex], results)
		stats.results++
		if saved := len(before) - len(after); saved > 0 {
			stats.savedChars += saved
		}
	}
	return result, stats, nil
}

func operationForMode(mode maintenanceMode) string {
	if mode == maintenancePrune {
		return "prune"
	}
	return "snip"
}

func candidateKey(messageIndex, resultIndex int) string {
	return fmt.Sprintf("%d:%d", messageIndex, resultIndex)
}

func toolNamesByUseID(history []message.Message) map[string]string {
	result := make(map[string]string)
	for _, msg := range history {
		for _, call := range toolCallsFromMessage(msg) {
			result[call.ID] = call.Name
		}
	}
	return result
}

func cloneMessages(history []message.Message) []message.Message {
	result := make([]message.Message, len(history))
	for i, msg := range history {
		result[i] = cloneMessage(msg)
	}
	return result
}

func cloneMessage(msg message.Message) message.Message {
	return message.CloneMessage(msg)
}

func setToolResultsOnMessage(msg *message.Message, results []message.ToolResult) {
	if msg == nil {
		return
	}
	if msg.ToolResult != nil && len(msg.ToolResults) == 0 && len(results) == 1 {
		result := results[0]
		msg.ToolResult = &result
		return
	}
	msg.ToolResult = nil
	msg.ToolResults = append([]message.ToolResult(nil), results...)
}

func snipToolResult(toolName, content, archivePath string, strategy snipStrategy) string {
	payload := encodeToolResultMarker(snippedToolResultMarker, toolName, len([]byte(content)), archivePath)
	lines := strings.Split(content, "\n")
	if len(lines) > 1 {
		head := minInt(strategy.head, len(lines))
		tail := minInt(strategy.tail, len(lines)-head)
		if head+tail >= len(lines) {
			return payload + "\n" + content
		}
		parts := append([]string(nil), lines[:head]...)
		parts = append(parts, fmt.Sprintf("… %d line(s) omitted …", len(lines)-head-tail))
		parts = append(parts, lines[len(lines)-tail:]...)
		return payload + "\n" + strings.Join(parts, "\n")
	}
	head := firstUTF8Bytes(content, strategy.headChars)
	tail := lastUTF8Bytes(content, strategy.tailChars)
	if len(head)+len(tail) >= len(content) {
		return payload + "\n" + content
	}
	return payload + "\n" + head + fmt.Sprintf("\n… %d byte(s) omitted …\n", len(content)-len(head)-len(tail)) + tail
}

func pruneToolResult(toolName, content, archivePath string, originalBytes ...int) string {
	size := len([]byte(content))
	if len(originalBytes) > 0 {
		size = originalBytes[0]
	}
	return encodeToolResultMarker(prunedToolResultMarker, toolName, size, archivePath)
}

func encodeToolResultMarker(prefix, toolName string, originalBytes int, archivePath string) string {
	payload, _ := json.Marshal(markerPayload{Tool: toolName, OriginalBytes: originalBytes, Archive: filepath.ToSlash(archivePath)})
	return prefix + string(payload) + "]"
}

func parseToolResultMarker(content string) (toolResultMarker, bool) {
	line := content
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = line[:newline]
	}
	var kind maintenanceMode
	var prefix string
	switch {
	case strings.HasPrefix(line, snippedToolResultMarker):
		kind, prefix = maintenanceSnip, snippedToolResultMarker
	case strings.HasPrefix(line, prunedToolResultMarker):
		kind, prefix = maintenancePrune, prunedToolResultMarker
	default:
		return toolResultMarker{}, false
	}
	if !strings.HasSuffix(line, "]") {
		return toolResultMarker{}, false
	}
	var payload markerPayload
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, prefix), "]")), &payload); err != nil || payload.OriginalBytes < 0 {
		return toolResultMarker{}, false
	}
	return toolResultMarker{kind: kind, toolName: payload.Tool, originalBytes: payload.OriginalBytes, archivePath: filepath.FromSlash(payload.Archive)}, true
}

func snipStrategyFor(registry *tool.Registry, name string) snipStrategy {
	strategy := defaultSideEffectingSnip
	registered, ok := registry.Get(name)
	if !ok {
		return strategy
	}
	if readOnly, ok := registered.(tool.ReadOnlyTool); ok && readOnly.ReadOnly() {
		strategy = defaultReadOnlySnip
	}
	if hinter, ok := registered.(tool.SnipHinter); ok {
		hint := hinter.SnipHint()
		if hint.Head >= 0 && hint.Tail >= 0 && hint.HeadChars >= 0 && hint.TailChars >= 0 &&
			(hint.Head > 0 || hint.Tail > 0) && (hint.HeadChars > 0 || hint.TailChars > 0) {
			return snipStrategy{head: hint.Head, tail: hint.Tail, headChars: hint.HeadChars, tailChars: hint.TailChars}
		}
	}
	return strategy
}

func firstUTF8Bytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

func lastUTF8Bytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.ValidString(text[start:]) {
		start++
	}
	return text[start:]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
