// 本文件提供 TUI 渲染和事件展示过程中使用的通用小工具。
package bubble

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rivo/uniseg"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui"
)

// summarizeToolContent 将工具输出压缩为单行短预览，避免 transcript 被长结果撑开。
func summarizeToolContent(content string) string {
	trimmed := strings.Join(strings.Fields(content), " ")
	if trimmed == "" {
		return ""
	}
	const maxPreviewGraphemes = 140
	graphemes := uniseg.NewGraphemes(trimmed)
	parts := make([]string, 0, maxPreviewGraphemes)
	for graphemes.Next() {
		if len(parts) == maxPreviewGraphemes {
			return strings.Join(parts, "") + "..."
		}
		parts = append(parts, graphemes.Str())
	}
	return trimmed
}

// prettyJSON 尽量将原始 JSON 压缩为稳定的一行文本，解析失败时返回原文。
func prettyJSON(raw json.RawMessage) string {
	input := strings.TrimSpace(string(raw))
	if input == "" {
		return "{}"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return input
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return input
	}
	return string(encoded)
}

type toolDisplayField struct {
	key   string
	value string
}

func formatToolCallBody(name string, input json.RawMessage, oldContent string) string {
	return formatToolCallBodyResolved(name, input, oldContent, true)
}

func formatToolCallBodyResolved(name string, input json.RawMessage, oldContent string, allowNameMutation bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	fields := toolInputFields(input)
	if strings.EqualFold(name, "question") {
		return formatQuestionToolCallBody(name, input)
	}
	if strings.EqualFold(name, "Task") {
		return formatSubagentToolCallBody(name, fields)
	}
	if allowNameMutation && isFileMutationTool(name) {
		return formatFileMutationToolCallBody(name, fields, oldContent)
	}

	lines := []string{name}
	for _, field := range fields {
		if shouldHideToolDetailField(name, field) {
			continue
		}
		lines = append(lines, field.value)
	}
	return strings.Join(lines, "\n")
}

func formatQuestionToolCallBody(name string, input json.RawMessage) string {
	lines := []string{name}
	var payload struct {
		Questions []json.RawMessage `json:"questions"`
	}
	if json.Unmarshal(input, &payload) == nil && len(payload.Questions) > 0 {
		noun := "questions"
		if len(payload.Questions) == 1 {
			noun = "question"
		}
		lines = append(lines, fmt.Sprintf("%d %s", len(payload.Questions), noun))
	}
	return strings.Join(lines, "\n")
}

func selectToolCallTarget(name string, input json.RawMessage) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(name), "question") {
		return "", false
	}
	return "", true
}

func questionBatchSummary(content string) (string, bool) {
	var batch selecttool.BatchResult
	if json.Unmarshal([]byte(content), &batch) != nil || len(batch.Results) == 0 {
		return "", false
	}
	for _, result := range batch.Results {
		if result.Cancelled {
			return "cancelled", true
		}
	}
	noun := "questions"
	if len(batch.Results) == 1 {
		noun = "question"
	}
	return fmt.Sprintf("answered %d %s", len(batch.Results), noun), true
}

func selectToolResultTarget(name, status, content string) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(name), "question") || status != "ok" {
		return "", false
	}
	return questionBatchSummary(content)
}

func completeToolCallBody(name, body, status, content string) string {
	if !strings.EqualFold(strings.TrimSpace(name), "question") || status != "ok" {
		return completeRunningToolCallBody(body, status)
	}
	summary, ok := questionBatchSummary(content)
	if !ok {
		return completeRunningToolCallBody(body, status)
	}
	summary = "question  " + summary
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 {
		return summary
	}
	lines[0] = summary
	return strings.Join(lines, "\n")
}

func shouldHideToolDetailField(_ string, field toolDisplayField) bool {
	key := strings.ToLower(strings.TrimSpace(field.key))
	value := strings.TrimSpace(field.value)
	switch key {
	case "cwd":
		return value == "" || value == "."
	default:
		return false
	}
}

func isFileMutationTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write", "edit", "update":
		return true
	default:
		return false
	}
}

func formatFileMutationToolCallBody(name string, fields []toolDisplayField, oldContent string) string {
	return formatFileMutationToolCallBodyWithSnapshot(name, fields, oldContent, nil)
}

func formatFileMutationToolCallBodyWithSnapshot(name string, fields []toolDisplayField, oldContent string, snapshot *ui.FileMutationSnapshot) string {
	summary := name
	preview := ""
	if lines, totals, ok := snapshotDiff(snapshot); ok {
		summary = fmt.Sprintf("%s  +%d -%d", name, totals.added, totals.removed)
		preview = renderDiffPreview(lines)
	} else if snapshot == nil {
		if totals, ok := fileMutationChangeCounts(fields, oldContent); ok {
			summary = fmt.Sprintf("%s  +%d -%d", name, totals.added, totals.removed)
		}
		preview = fileMutationDiffPreview(fields, oldContent)
	}
	lines := []string{summary}
	if target := firstNonEmptyField(fields, "file_path", "path"); target != "" && !strings.EqualFold(strings.TrimSpace(name), "Edit") {
		lines = append(lines, target)
	}
	if preview != "" {
		lines = append(lines, preview)
	}
	return strings.Join(lines, "\n")
}

type diffTotals struct{ added, removed int }

// fileMutationChangeCounts reports added/removed line counts for the
// file-mutation diff. Returns false when there is no old or new content
// (no diff to summarize).
func fileMutationChangeCounts(fields []toolDisplayField, oldContent string) (diffTotals, bool) {
	old, newContent := fileMutationContents(fields, oldContent)
	if old == "" && newContent == "" {
		return diffTotals{}, false
	}
	if old == "" {
		return diffTotals{added: len(splitLines(newContent))}, true
	}
	if newContent == "" {
		return diffTotals{removed: len(splitLines(old))}, true
	}
	added, removed := diffCounts(structuredDiff(splitLines(old), splitLines(newContent)))
	if added == 0 && removed == 0 {
		return diffTotals{}, false
	}
	return diffTotals{added: added, removed: removed}, true
}

func fileMutationDiffPreview(fields []toolDisplayField, oldContent string) string {
	oldContent, newContent := fileMutationContents(fields, oldContent)
	if oldContent == "" && newContent == "" {
		return ""
	}

	var oldLines, newLines []string
	if oldContent != "" {
		oldLines = splitLines(oldContent)
	}
	if newContent != "" {
		newLines = splitLines(newContent)
	}
	return renderDiffPreview(structuredDiff(oldLines, newLines))
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func limitDiffPreviewLines(lines []string) []string {
	const maxDiffPreviewLines = 32
	if len(lines) <= maxDiffPreviewLines {
		return lines
	}
	hidden := len(lines) - maxDiffPreviewLines
	out := append([]string(nil), lines[:maxDiffPreviewLines]...)
	out = append(out, fmt.Sprintf("... %d more diff lines", hidden))
	return out
}

func firstNonEmptyField(fields []toolDisplayField, keys ...string) string {
	for _, key := range keys {
		if value := fieldValue(fields, key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatRunningToolCallBody(name string, input json.RawMessage, oldContent string) string {
	return formatRunningToolCallBodyWithSnapshot(name, input, oldContent, false, false, nil)
}

func formatRunningToolCallBodyWithSnapshot(name string, input json.RawMessage, oldContent string, mutationKnown, isMutation bool, before *ui.FileMutationSnapshot) string {
	if mutationKnown {
		if !isMutation || before == nil {
			return setToolCallBodyStatus(formatToolCallBodyResolved(name, input, oldContent, false), "running")
		}
	} else if !isFileMutationTool(name) || before == nil {
		return setToolCallBodyStatus(formatToolCallBody(name, input, oldContent), "running")
	}
	fields := toolInputFields(input)
	return setToolCallBodyStatus(formatFileMutationToolCallBodyWithSnapshot(name, fields, oldContent, previewSnapshot(name, fields, before)), "running")
}

func completeRunningToolCallBody(body, status string) string {
	return setToolCallBodyStatus(body, status)
}

func completeFileMutationToolCallBody(name string, input json.RawMessage, legacyOldContent, status string, snapshot *ui.FileMutationSnapshot) string {
	return setToolCallBodyStatus(formatFileMutationToolCallBodyWithSnapshot(name, toolInputFields(input), legacyOldContent, snapshot), status)
}

func setToolCallBodyStatus(body, status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "ok"
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return status
	}
	parts := splitToolSummaryParts(lines[0])
	if len(parts) >= 2 && (parts[1] == "running" || parts[1] == "ok" || parts[1] == "error") {
		parts[1] = status
	} else {
		parts = append(parts[:1], append([]string{status}, parts[1:]...)...)
	}
	lines[0] = strings.Join(parts, "  ")
	return strings.Join(lines, "\n")
}

func splitToolSummaryParts(summary string) []string {
	rawParts := strings.Split(summary, "  ")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func firstToolEntryLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return line
}

func formatToolResultBody(name, status, content string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "ok"
	}
	lines := []string{name + "  " + status}
	if preview := summarizeToolContent(content); preview != "" {
		lines = append(lines, preview)
	}
	return strings.Join(lines, "\n")
}

func formatSubagentToolCallBody(name string, fields []toolDisplayField) string {
	contextMode := fieldValue(fields, "context_mode")
	runMode := fieldValue(fields, "run_mode")
	description := fieldValue(fields, "description")
	prompt := fieldValue(fields, "prompt")

	suffix := strings.Join(nonEmptyStrings(runMode, contextMode), "  ")
	summary := name
	if suffix != "" {
		summary += "  " + suffix
	}

	lines := []string{summary}
	if description != "" {
		lines = append(lines, "description  "+description)
	}
	if contextMode != "" || runMode != "" {
		lines = append(lines, "mode  "+strings.Join(nonEmptyStrings(runMode, contextMode), "  "))
	}
	if prompt != "" {
		lines = append(lines, "prompt  "+summarizeToolContent(prompt))
	}
	return strings.Join(lines, "\n")
}

func toolInputFields(raw json.RawMessage) []toolDisplayField {
	input := strings.TrimSpace(string(raw))
	if input == "" || input == "null" {
		return nil
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return []toolDisplayField{{key: "input", value: summarizeToolContent(input)}}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]toolDisplayField, 0, len(keys))
	for _, key := range keys {
		if value := displayToolFieldValue(key, object[key]); value != "" {
			fields = append(fields, toolDisplayField{key: key, value: value})
		}
	}
	return fields
}

func displayToolFieldValue(key string, value any) string {
	if preservesMultilineToolField(key) {
		if text, ok := value.(string); ok {
			return text
		}
	}
	return displayToolValue(value)
}

func preservesMultilineToolField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "content", "old_content", "new_content", "old_string", "new_string", "replacement", "before", "after":
		return true
	default:
		return false
	}
}

func displayToolValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return summarizeToolContent(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return summarizeToolContent(fmt.Sprint(v))
		}
		return summarizeToolContent(string(data))
	}
}

func primaryToolInput(name string, fields []toolDisplayField) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, key := range primaryToolInputKeys(lower) {
		if value := fieldValue(fields, key); value != "" {
			return key + "=" + value
		}
	}
	if len(fields) == 1 {
		return fields[0].key + "=" + fields[0].value
	}
	return ""
}

// toolSummaryTarget 提取最适合单行工具轨道展示的目标值，不暴露参数键名。
func toolSummaryTarget(name string, input json.RawMessage) string {
	return displayToolTarget(name, input, "")
}

func primaryToolInputKeys(name string) []string {
	switch name {
	case "ls", "glob":
		return []string{"path", "pattern"}
	case "read":
		return []string{"file_path", "path"}
	case "write", "edit":
		return []string{"file_path", "path"}
	case "update":
		return []string{"file_path", "path"}
	case "bash":
		return []string{"command"}
	case "webfetch":
		return []string{"url"}
	case "subagentstop":
		return []string{"id"}
	default:
		return []string{"path", "file_path", "command", "url", "id"}
	}
}

func fieldValue(fields []toolDisplayField, key string) string {
	for _, field := range fields {
		if field.key == key {
			return field.value
		}
	}
	return ""
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func sanitizeAssistantVisibleBody(body string) string {
	// Fenced JSON is ordinary assistant Markdown (often an example), so do not
	// remove it here. Transport tool-use artifacts are filtered only from prose
	// outside fenced blocks below.
	return strings.TrimSpace(stripEmbeddedToolUseJSONOutsideFences(body))
}

// stripEmbeddedToolUseJSONOutsideFences removes transport artifacts from prose
// while leaving fenced Markdown/code blocks byte-for-byte intact.
func stripEmbeddedToolUseJSONOutsideFences(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			out = append(out, line)
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		out = append(out, stripEmbeddedToolUseJSON(line))
	}
	return strings.Join(out, "\n")
}

func stripToolUseFences(content string) string {
	for {
		start := strings.Index(content, "```")
		if start == -1 {
			return content
		}
		rest := content[start+3:]
		newline := strings.IndexByte(rest, '\n')
		if newline == -1 {
			return content
		}
		bodyStart := start + 3 + newline + 1
		closeRel := strings.Index(content[bodyStart:], "```")
		if closeRel == -1 {
			return content
		}
		bodyEnd := bodyStart + closeRel
		if isToolUseJSONPayload(strings.TrimSpace(content[bodyStart:bodyEnd])) {
			content = strings.TrimSpace(content[:start]) + "\n" + strings.TrimSpace(content[bodyEnd+3:])
			continue
		}
		next := bodyEnd + 3
		if next >= len(content) {
			return content
		}
		remaining := stripToolUseFences(content[next:])
		return content[:next] + remaining
	}
}

func stripEmbeddedToolUseJSON(content string) string {
	var out strings.Builder
	last := 0
	for start := strings.IndexByte(content, '{'); start != -1; {
		payload, next, ok := balancedJSONObjectAt(content, start)
		if ok && isToolUseJSONPayload(payload) {
			out.WriteString(content[last:start])
			last = next
			if next >= len(content) {
				break
			}
			relative := strings.IndexByte(content[next:], '{')
			if relative == -1 {
				break
			}
			start = next + relative
			continue
		}
		searchFrom := start + 1
		if ok && next > searchFrom {
			searchFrom = next
		}
		if searchFrom >= len(content) {
			break
		}
		relative := strings.IndexByte(content[searchFrom:], '{')
		if relative == -1 {
			break
		}
		start = searchFrom + relative
	}
	out.WriteString(content[last:])
	return strings.TrimSpace(out.String())
}

func balancedJSONObjectAt(content string, start int) (string, int, bool) {
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

func isToolUseJSONPayload(payload string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &object); err != nil {
		return false
	}
	var typ string
	_ = json.Unmarshal(object["type"], &typ)
	if typ != "" && typ != "tool_use" {
		return false
	}
	if typ == "tool_use" {
		return object["name"] != nil && object["input"] != nil
	}
	return object["id"] != nil && object["name"] != nil && object["input"] != nil
}

// maxInt 返回两个整数中的较大值。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// minInt 返回两个整数中的较小值。
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
