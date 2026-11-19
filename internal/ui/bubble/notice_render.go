package bubble

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// contextCompactionTitle 是运行时上下文整理提示的条目标题。
const contextCompactionTitle = "context-compaction"

// renderCompactionNotice 把上下文整理提示渲染成一行弱化文字，续行悬挂对齐正文。
func renderCompactionNotice(body string, width int) string {
	style := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorTextMuted))
	text := strings.Join(strings.Fields(sanitizeTerminalText(body)), " ")
	lines := wrapStyledCellTextAtSemanticBoundaries(text, maxInt(1, width-2))
	for i, line := range lines {
		lead := "  "
		if i == 0 {
			lead = glyphNotice + " "
		}
		lines[i] = style.Render(lead + line)
	}
	return strings.Join(lines, "\n")
}

var (
	modelAPIErrorPattern = regexp.MustCompile(`(?s)^(.*?)返回异常状态 (\d{3}): (.*)$`)
	requestIDPattern     = regexp.MustCompile(`\s*\(request_id:\s*([^)]+)\)`)
)

// modelAPIError 是从「X返回异常状态 N: <响应体>」里拆出的可读字段；
// 错误类型、request_id 等细节留在折叠的原始响应里。
type modelAPIError struct {
	source, status, message string
	raw                     string // 仅 JSON 响应体保留，供折叠展开
}

func parseModelAPIError(body string) (modelAPIError, bool) {
	match := modelAPIErrorPattern.FindStringSubmatch(strings.TrimSpace(body))
	if match == nil {
		return modelAPIError{}, false
	}
	parsed := modelAPIError{source: strings.TrimSpace(match[1]), status: match[2]}
	payload := strings.TrimSpace(match[3])
	var decoded map[string]any
	if json.Unmarshal([]byte(payload), &decoded) != nil {
		parsed.message = strings.Join(strings.Fields(payload), " ")
		return parsed, true
	}
	parsed.raw = payload
	fields := decoded
	switch nested := decoded["error"].(type) {
	case map[string]any:
		fields = nested
	case string:
		parsed.message = nested
	}
	if message, ok := fields["message"].(string); ok {
		parsed.message = message
	}
	parsed.message = strings.TrimSpace(requestIDPattern.ReplaceAllString(parsed.message, ""))
	return parsed, true
}

// renderErrorEntry 渲染错误条目：首行红色 ✗ 标题，其余文字用弱化色；
// 模型接口的 JSON 响应体折叠在「原始响应」后，点击展开。
func renderErrorEntry(entry transcriptEntry, width int) string {
	width = maxInt(1, width)
	headStyle := statusTextStyle("error")
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorTextMuted))
	var lines []string
	appendWrapped := func(text string, style lipgloss.Style, lead string) {
		for i, line := range wrapStyledCellTextAtSemanticBoundaries(text, maxInt(1, width-2)) {
			prefix := "  "
			if i == 0 && lead != "" {
				prefix = lead
			}
			lines = append(lines, style.Render(prefix+line))
		}
	}

	body := sanitizeTerminalText(entry.body)
	parsed, ok := parseModelAPIError(body)
	if !ok {
		text := strings.TrimSpace(body)
		// recovery 正文已自带「上一轮失败」说明，不再重复英文标题。
		if title := strings.TrimSpace(sanitizeTerminalText(entry.title)); title != "" && title != "error" && title != "recovery" {
			text = title + ": " + text
		}
		head, rest, _ := strings.Cut(text, "\n")
		appendWrapped(head, headStyle, glyphFailure+" ")
		for _, line := range strings.Split(strings.TrimSpace(rest), "\n") {
			if strings.TrimSpace(line) != "" {
				appendWrapped(line, mutedStyle, "")
			}
		}
		return strings.Join(lines, "\n")
	}

	head := parsed.source + "错误 " + parsed.status
	if parsed.message != "" {
		head += " · " + parsed.message
	}
	appendWrapped(head, headStyle, glyphFailure+" ")
	if parsed.raw == "" {
		return strings.Join(lines, "\n")
	}
	marker := glyphFolded
	if entry.errorDetailsExpanded {
		marker = glyphExpanded
	}
	lines = append(lines, mutedStyle.Render("  "+marker+" "+copyErrorRawResponse))
	if entry.errorDetailsExpanded {
		var pretty bytes.Buffer
		raw := parsed.raw
		if json.Indent(&pretty, []byte(raw), "", "  ") == nil {
			raw = pretty.String()
		}
		for _, line := range strings.Split(raw, "\n") {
			// 续行保持 JSON 原有缩进，层级不被折行打乱。
			content := strings.TrimLeft(line, " ")
			indent := "    " + line[:len(line)-len(content)]
			for _, wrapped := range wrapStyledCellTextAtSemanticBoundaries(content, maxInt(1, width-terminalCellWidth(indent))) {
				lines = append(lines, mutedStyle.Render(indent+wrapped))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func errorEntryHasRawResponse(entry transcriptEntry) bool {
	parsed, ok := parseModelAPIError(sanitizeTerminalText(entry.body))
	return ok && parsed.raw != ""
}

// toggleErrorDetailsAtRow 在点击落在可折叠错误条目内时切换原始响应。
func (m *appModel) toggleErrorDetailsAtRow(row int) bool {
	for _, location := range m.transcriptEntryLocationsAt() {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		index, ok := m.transcriptIndexForViewEntry(location.transcriptIndex)
		if !ok {
			return false
		}
		entry := &m.transcript[index]
		if entry.kind != entryError || !errorEntryHasRawResponse(*entry) {
			return false
		}
		entry.errorDetailsExpanded = !entry.errorDetailsExpanded
		m.touchTranscriptEntryAt(index)
		m.refreshViewportPreservingOffset()
		return true
	}
	return false
}
