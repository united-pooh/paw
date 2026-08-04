package loop

import (
	"encoding/json"
	"fmt"
	"html"
	"paw/internal/message"
	"strconv"
	"strings"
)

func parseAssistantMessage(content string) message.Message {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return buildAssistantMessage("")
	}

	envelopes := extractToolUseEnvelopes(trimmed)
	if len(envelopes) == 0 {
		return buildAssistantMessage(content)
	}

	calls := make([]message.ToolCall, 0, len(envelopes))
	for _, envelope := range envelopes {
		calls = append(calls, message.ToolCall{
			ID:    envelope.ID,
			Name:  envelope.Name,
			Input: envelope.Input,
		})
	}

	return buildAssistantToolCallMessage(calls)
}

func appendStreamToolCalls(state *turnState, calls []message.ToolCall) {
	if state == nil || len(calls) == 0 {
		return
	}
	state.toolCalls = append(state.toolCalls, calls...)
	state.pending.Reset()
	state.outputMode = outputModeSuppressed
}

func buildAssistantToolCallMessage(calls []message.ToolCall) message.Message {
	calls = normalizeToolCalls(calls)
	msg := message.Message{Role: message.RoleAssistant}
	if len(calls) == 1 {
		call := calls[0]
		msg.ToolUse = &call
	} else if len(calls) > 1 {
		msg.ToolUses = calls
		call := calls[0]
		msg.ToolUse = &call
	}
	return msg
}

func normalizeToolCalls(calls []message.ToolCall) []message.ToolCall {
	normalized := make([]message.ToolCall, 0, len(calls))
	for i, call := range calls {
		if strings.TrimSpace(call.Name) == "" {
			continue
		}
		if call.ID == "" {
			if len(calls) == 1 {
				call.ID = call.Name
			} else {
				call.ID = fmt.Sprintf("%s_%d", call.Name, i+1)
			}
		}
		if len(call.Input) == 0 {
			call.Input = json.RawMessage(`{}`)
		} else {
			call.Input = append(json.RawMessage(nil), call.Input...)
		}
		normalized = append(normalized, call)
	}
	return normalized
}

func extractToolUseEnvelope(trimmed string) (toolUseEnvelope, bool) {
	envelopes := extractToolUseEnvelopes(trimmed)
	if len(envelopes) == 0 {
		return toolUseEnvelope{}, false
	}
	return envelopes[0], true
}

func extractToolUseEnvelopes(trimmed string) []toolUseEnvelope {
	if envelopes, ok := decodeToolUseEnvelopes(trimmed); ok {
		return envelopes
	}

	if payload, ok := extractFencedToolUsePayload(trimmed); ok {
		if envelopes, matched := decodeToolUseEnvelopes(payload); matched {
			return envelopes
		}
	}

	if envelopes := extractEmbeddedToolUseEnvelopes(trimmed); len(envelopes) > 0 {
		return envelopes
	}

	if envelope, ok := extractInvokeToolUseEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	if envelope, ok := extractToolCallEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	if envelope, ok := extractTagNameToolEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	return nil
}

func extractTagNameToolEnvelope(content string) (toolUseEnvelope, bool) {
	for start := 0; start < len(content); {
		openIdx := strings.IndexByte(content[start:], '<')
		if openIdx == -1 {
			break
		}
		openIdx += start

		// 跳过结束标签
		if openIdx+1 < len(content) && content[openIdx+1] == '/' {
			start = openIdx + 2
			continue
		}

		// 找到标签结束 >
		closeAngle := strings.IndexByte(content[openIdx:], '>')
		if closeAngle == -1 {
			break
		}
		closeAngle += openIdx

		tagName := content[openIdx+1 : closeAngle]
		if !isSimpleXMLTagName(tagName) {
			start = closeAngle + 1
			continue
		}

		// 查找对应的结束标签
		closeTag := "</" + tagName + ">"
		closeIdx := strings.Index(strings.ToLower(content[closeAngle+1:]), strings.ToLower(closeTag))
		if closeIdx == -1 {
			start = closeAngle + 1
			continue
		}
		closeIdx += closeAngle + 1

		body := strings.TrimSpace(content[closeAngle+1 : closeIdx])
		// 标签体必须是有效的 JSON 对象
		if !strings.HasPrefix(body, "{") || !json.Valid([]byte(body)) {
			start = closeAngle + 1
			continue
		}

		return toolUseEnvelope{
			Type:  toolUseResponseType,
			Name:  tagName,
			Input: json.RawMessage(body),
		}, true
	}
	return toolUseEnvelope{}, false
}

func isSimpleXMLTagName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func extractToolCallEnvelope(content string) (toolUseEnvelope, bool) {
	lower := strings.ToLower(content)

	// 格式 B / C：通过 <name>...</name> + <input>...</input> 提取
	if envelope, ok := extractNameInputEnvelope(content, lower); ok {
		return envelope, true
	}

	// 格式 A：通过 <tool name="..."> 提取
	toolIdx := strings.Index(lower, "<tool ")
	if toolIdx == -1 {
		return toolUseEnvelope{}, false
	}

	tagEndRel := strings.IndexByte(content[toolIdx:], '>')
	if tagEndRel == -1 {
		return toolUseEnvelope{}, false
	}
	tagEnd := toolIdx + tagEndRel

	openTag := content[toolIdx : tagEnd+1]
	name := extractTagAttribute(openTag, "name")
	if name == "" {
		return toolUseEnvelope{}, false
	}

	bodyStart := tagEnd + 1
	bodyEnd := len(content)
	restLower := strings.ToLower(content[bodyStart:])
	if closeIdx := strings.Index(restLower, "</tool>"); closeIdx != -1 {
		bodyEnd = bodyStart + closeIdx
	} else if closeIdx := strings.Index(restLower, "</tool_call>"); closeIdx != -1 {
		bodyEnd = bodyStart + closeIdx
	}

	body := strings.TrimSpace(content[bodyStart:bodyEnd])
	input := decodeInvokeInput(body)

	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

func extractNameInputEnvelope(content, lower string) (toolUseEnvelope, bool) {
	nameStart := strings.Index(lower, "<name>")
	if nameStart == -1 {
		return toolUseEnvelope{}, false
	}
	nameEnd := strings.Index(lower[nameStart:], "</name>")
	if nameEnd == -1 {
		return toolUseEnvelope{}, false
	}
	nameEnd += nameStart
	name := strings.TrimSpace(content[nameStart+len("<name>") : nameEnd])
	if name == "" {
		return toolUseEnvelope{}, false
	}

	inputStart := strings.Index(lower, "<input>")
	if inputStart == -1 {
		return toolUseEnvelope{}, false
	}
	inputEnd := strings.Index(lower[inputStart:], "</input>")
	if inputEnd == -1 {
		return toolUseEnvelope{}, false
	}
	inputEnd += inputStart
	inputBody := strings.TrimSpace(content[inputStart+len("<input>") : inputEnd])

	input := decodeInvokeInput(inputBody)
	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

func decodeToolUseEnvelope(payload string) (toolUseEnvelope, bool) {
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, "{") {
		return toolUseEnvelope{}, false
	}

	var envelope toolUseEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return toolUseEnvelope{}, false
	}
	if envelope.Type != toolUseResponseType || envelope.Name == "" {
		return toolUseEnvelope{}, false
	}
	return envelope, true
}

func decodeToolUseEnvelopes(payload string) ([]toolUseEnvelope, bool) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, false
	}
	if envelope, ok := decodeToolUseEnvelope(payload); ok {
		return []toolUseEnvelope{envelope}, true
	}
	if !strings.HasPrefix(payload, "[") {
		return nil, false
	}
	var envelopes []toolUseEnvelope
	if err := json.Unmarshal([]byte(payload), &envelopes); err != nil {
		return nil, false
	}
	if len(envelopes) == 0 {
		return nil, false
	}
	for _, envelope := range envelopes {
		if envelope.Type != toolUseResponseType || envelope.Name == "" {
			return nil, false
		}
	}
	return envelopes, true
}

func extractMarkdownFenceBody(trimmed string) (string, bool) {
	rest := strings.TrimPrefix(trimmed, "```")
	newlineIndex := strings.IndexByte(rest, '\n')
	if newlineIndex == -1 {
		return "", false
	}

	bodyWithClose := rest[newlineIndex+1:]
	closeIndex := strings.LastIndex(bodyWithClose, "```")
	if closeIndex == -1 {
		return "", false
	}

	return bodyWithClose[:closeIndex], true
}

func extractFencedToolUsePayload(trimmed string) (string, bool) {
	searchFrom := 0
	for {
		start := strings.Index(trimmed[searchFrom:], "```")
		if start == -1 {
			return "", false
		}
		start += searchFrom

		body, next, ok := extractFenceBodyAt(trimmed, start)
		if !ok {
			return "", false
		}
		body = strings.TrimSpace(body)
		if _, ok := decodeToolUseEnvelope(body); ok {
			return body, true
		}
		searchFrom = next
	}
}

func extractFenceBodyAt(content string, start int) (body string, next int, ok bool) {
	rest := content[start:]
	if !strings.HasPrefix(rest, "```") {
		return "", len(content), false
	}

	rest = strings.TrimPrefix(rest, "```")
	newlineIndex := strings.IndexByte(rest, '\n')
	if newlineIndex == -1 {
		return "", len(content), false
	}

	bodyWithClose := rest[newlineIndex+1:]
	closeIndex := strings.Index(bodyWithClose, "```")
	if closeIndex == -1 {
		return "", len(content), false
	}

	consumed := start + 3 + newlineIndex + 1 + closeIndex + 3
	return bodyWithClose[:closeIndex], consumed, true
}

func extractEmbeddedJSONObject(trimmed string) (string, bool) {
	for start := strings.IndexByte(trimmed, '{'); start != -1; {
		payload, next, ok := extractBalancedJSONObject(trimmed, start)
		if ok {
			if _, matched := decodeToolUseEnvelope(payload); matched {
				return payload, true
			}
		}

		if next >= len(trimmed) {
			break
		}
		relative := strings.IndexByte(trimmed[next:], '{')
		if relative == -1 {
			break
		}
		start = next + relative
	}
	return "", false
}

func extractEmbeddedToolUseEnvelopes(trimmed string) []toolUseEnvelope {
	var envelopes []toolUseEnvelope
	for start := strings.IndexByte(trimmed, '{'); start != -1; {
		payload, next, ok := extractBalancedJSONObject(trimmed, start)
		if ok {
			if envelope, matched := decodeToolUseEnvelope(payload); matched {
				envelopes = append(envelopes, envelope)
			}
		}

		if next >= len(trimmed) {
			break
		}
		relative := strings.IndexByte(trimmed[next:], '{')
		if relative == -1 {
			break
		}
		start = next + relative
	}
	return envelopes
}

func extractBalancedJSONObject(content string, start int) (string, int, bool) {
	if start < 0 || start >= len(content) || content[start] != '{' {
		return "", len(content), false
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(content); i++ {
		ch := content[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1], i + 1, true
			}
		}
	}

	return "", len(content), false
}

func extractInvokeToolUseEnvelope(content string) (toolUseEnvelope, bool) {
	lower := strings.ToLower(content)
	start := strings.Index(lower, "<invoke")
	if start == -1 {
		return toolUseEnvelope{}, false
	}

	tagEnd := strings.Index(content[start:], ">")
	if tagEnd == -1 {
		return toolUseEnvelope{}, false
	}
	tagEnd += start

	openTag := content[start : tagEnd+1]
	name := extractTagAttribute(openTag, "name")
	if name == "" {
		return toolUseEnvelope{}, false
	}

	bodyStart := tagEnd + 1
	closeRel := strings.Index(strings.ToLower(content[bodyStart:]), "</invoke>")
	if closeRel == -1 {
		return toolUseEnvelope{}, false
	}

	input := decodeInvokeInput(content[bodyStart : bodyStart+closeRel])
	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

func decodeInvokeInput(body string) json.RawMessage {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "{") && json.Valid([]byte(body)) {
		return json.RawMessage(body)
	}

	params := make(map[string]any)
	searchFrom := 0
	for searchFrom < len(body) {
		openRel := strings.Index(body[searchFrom:], "<")
		if openRel == -1 {
			break
		}
		open := searchFrom + openRel
		if open+1 < len(body) && body[open+1] == '/' {
			searchFrom = open + 2
			continue
		}

		tagEndRel := strings.Index(body[open:], ">")
		if tagEndRel == -1 {
			break
		}
		tagEnd := open + tagEndRel
		openTag := body[open : tagEnd+1]
		paramName := extractTagAttribute(openTag, "name")
		tagName := extractTagName(openTag)
		if tagName == "" {
			searchFrom = tagEnd + 1
			continue
		}
		// 支持 <file_path>value</file_path> 风格（tag 名即参数名）
		if paramName == "" {
			paramName = tagName
		}

		closeTag := "</" + tagName + ">"
		valueStart := tagEnd + 1
		closeRel := strings.Index(body[valueStart:], closeTag)
		if closeRel == -1 {
			searchFrom = tagEnd + 1
			continue
		}
		valueEnd := valueStart + closeRel
		params[paramName] = decodeInvokeParamValue(openTag, body[valueStart:valueEnd])
		searchFrom = valueEnd + len(closeTag)
	}

	if len(params) == 0 {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func decodeInvokeParamValue(openTag, body string) any {
	value := extractTagAttribute(openTag, "value")
	if value == "" {
		value = html.UnescapeString(strings.TrimSpace(body))
	}

	stringAttr := strings.ToLower(strings.TrimSpace(extractTagAttribute(openTag, "string")))
	if stringAttr != "false" {
		return value
	}

	if value == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	if intValue, err := strconv.Atoi(value); err == nil {
		return intValue
	}
	if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
		return floatValue
	}
	if boolValue, err := strconv.ParseBool(value); err == nil {
		return boolValue
	}
	return value
}

func extractTagName(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(tag, "<"))
	tag = strings.TrimSuffix(tag, ">")
	tag = strings.TrimSpace(tag)
	if tag == "" || strings.HasPrefix(tag, "/") {
		return ""
	}
	for i, r := range tag {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '/' {
			return tag[:i]
		}
	}
	return tag
}

func extractTagAttribute(tag, name string) string {
	for _, quote := range []byte{'"', '\''} {
		prefix := name + "=" + string(quote)
		start := strings.Index(tag, prefix)
		if start == -1 {
			continue
		}
		valueStart := start + len(prefix)
		valueEndRel := strings.IndexByte(tag[valueStart:], quote)
		if valueEndRel == -1 {
			return ""
		}
		return html.UnescapeString(tag[valueStart : valueStart+valueEndRel])
	}
	return ""
}
