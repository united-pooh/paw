package loop

import (
	"fmt"
	"paw/internal/message"
	"regexp"
	"strings"
)

// ToolProtocolError means an explicit text-encoded tool request could not be
// interpreted safely. Recovery requires a fresh model call, never execution of
// the malformed request or retry as a transport interruption.
type ToolProtocolError struct {
	Format string
}

func (err *ToolProtocolError) Error() string {
	return fmt.Sprintf("模型返回了损坏或不支持的 %s 工具协议，未执行该工具请求", err.Format)
}

var toolUseProtocolStart = regexp.MustCompile(`^[\[,\s]*\{\s*"type"\s*:\s*"tool_use"`)
var dsmlProtocolStart = regexp.MustCompile(`^[\[,\s]*</?｜{1,2}DSML｜{1,2}`)

func toolProtocolProblem(content string) *ToolProtocolError {
	remaining := withoutCompleteToolInvocations(withoutCompleteToolJSON(content))
	hasCompleteCall := remaining != content
	content = remaining
	var fence string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if marker := toolProtocolFenceMarker(trimmed); marker != "" {
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(trimmed[len(marker):]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if !hasCompleteCall && !strings.HasPrefix(strings.TrimSpace(content), "[") && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			continue
		}
		if toolUseProtocolStart.MatchString(trimmed) {
			return &ToolProtocolError{Format: "tool_use"}
		}
		if dsmlProtocolStart.MatchString(trimmed) {
			return &ToolProtocolError{Format: "DSML"}
		}
	}
	return nil
}

func toolProtocolFenceMarker(line string) string {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	end := 1
	for end < len(line) && line[end] == line[0] {
		end++
	}
	if end < 3 {
		return ""
	}
	return line[:end]
}

func withoutCompleteToolJSON(content string) string {
	var remaining strings.Builder
	from := 0
	for start := strings.IndexByte(content, '{'); start >= 0; {
		payload, next, balanced := extractBalancedJSONObject(content, start)
		if balanced {
			if _, valid := decodeToolUseEnvelope(payload); valid {
				remaining.WriteString(content[from:start])
				remaining.WriteString(strings.Repeat("\n", strings.Count(payload, "\n")))
				from = next
			}
		}
		if next >= len(content) {
			break
		}
		relative := strings.IndexByte(content[next:], '{')
		if relative < 0 {
			break
		}
		start = next + relative
	}
	remaining.WriteString(content[from:])
	return remaining.String()
}

func withoutCompleteToolInvocations(content string) string {
	var remaining strings.Builder
	from, search := 0, 0
	lower := strings.ToLower(content)
	for search < len(content) {
		relative := strings.Index(lower[search:], "<invoke")
		if relative < 0 {
			break
		}
		start := search + relative
		closeAt := strings.Index(lower[start:], "</invoke>")
		if closeAt < 0 {
			break
		}
		end := start + closeAt + len("</invoke>")
		if _, valid := extractInvokeToolUseEnvelope(content[start:end]); valid {
			remaining.WriteString(content[from:start])
			remaining.WriteString(strings.Repeat("\n", strings.Count(content[start:end], "\n")))
			from = end
		}
		search = end
	}
	remaining.WriteString(content[from:])
	return remaining.String()
}

func possibleDSMLProtocolStart(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, prefix := range []string{"<｜DSML｜", "</｜DSML｜", "<｜｜DSML｜｜", "</｜｜DSML｜｜"} {
		if strings.HasPrefix(prefix, trimmed) || strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func toolPayloadStartOffset(delta string) int {
	offset := 0
	for _, line := range strings.SplitAfter(delta, "\n") {
		if looksLikeToolPayloadStart(line) {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func toolProtocolPartialMessage(msg message.Message) message.Message {
	// Structured text parts accumulate before the display filter. Keep only text
	// that was actually published so restoring a failed turn cannot reveal it.
	parts := make([]message.AssistantPart, 0, len(msg.AssistantParts))
	for _, part := range msg.AssistantParts {
		if part.Type != message.AssistantPartText {
			parts = append(parts, part)
		}
	}
	if msg.Content != "" {
		parts = append(parts, message.AssistantPart{
			Type:   message.AssistantPartText,
			Status: message.AssistantPartPartial,
			Text:   &message.AssistantTextPart{Text: msg.Content},
		})
	}
	msg.AssistantParts = parts
	return msg
}
