// 本文件提供 TUI 渲染和事件展示过程中使用的通用小工具。
package bubble

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rivo/uniseg"
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
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	fields := toolInputFields(input)
	if strings.EqualFold(name, "Subagent") {
		return formatSubagentToolCallBody(name, fields)
	}
	if isFileMutationTool(name) {
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
	target := firstNonEmptyField(fields, "file_path", "path")
	lines := []string{name}
	if target != "" {
		lines = append(lines, target)
	}
	if diff := fileMutationDiffPreview(fields, oldContent); diff != "" {
		lines = append(lines, diff)
	}
	return strings.Join(lines, "\n")
}

func fileMutationDiffPreview(fields []toolDisplayField, oldContent string) string {
	// Edit 工具自带 old_content/old_string；Write 工具的旧内容由调用方在工具执行前读取后传入
	if fc := firstNonEmptyField(fields, "old_content", "old_string", "before"); fc != "" {
		oldContent = fc
	}
	newContent := firstNonEmptyField(fields, "new_content", "new_string", "replacement", "content", "after")

	if oldContent == "" && newContent == "" {
		return ""
	}
	// 新建文件（无旧内容）：只显示 + 行
	if oldContent == "" {
		return strings.Join(limitDiffPreviewLines(numberedLines("+", newContent)), "\n")
	}
	// 删除文件或清空（无新内容）：只显示 - 行
	if newContent == "" {
		return strings.Join(limitDiffPreviewLines(numberedLines("-", oldContent)), "\n")
	}

	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	ops := lcsEditScript(oldLines, newLines)

	// 参考 Claude Code numberDiffLines 策略：单列行号（基于旧文件行号）
	// nochange/remove: 递增行号计数器
	// add: 显示当前行号但不推进（插入不占旧文件行号）
	// remove 块: 各行显示递增行号，块结束后回退（删除的行在旧文件中连续占位）
	type numberedOp struct {
		kind rune
		line int
		text string
	}
	numbered := make([]numberedOp, 0, len(ops))
	lineNum := 1
	for idx := 0; idx < len(ops); {
		op := ops[idx]
		switch op.kind {
		case ' ':
			numbered = append(numbered, numberedOp{' ', lineNum, op.text})
			lineNum++
			idx++
		case '+':
			// 插入行不占旧文件行号，显示当前位置但不递增
			numbered = append(numbered, numberedOp{'+', lineNum, op.text})
			idx++
		case '-':
			// 收集连续的 remove 块，各行显示递增行号，结束后回退
			// 删除的行在旧文件中占位，但新文件中不存在，后续行从同一位置继续
			numRemoved := 0
			for idx < len(ops) && ops[idx].kind == '-' {
				numbered = append(numbered, numberedOp{'-', lineNum, ops[idx].text})
				lineNum++
				numRemoved++
				idx++
			}
			lineNum -= numRemoved
		}
	}

	// 计算行号宽度
	maxLine := 0
	for _, n := range numbered {
		if n.line > maxLine {
			maxLine = n.line
		}
	}
	width := 1
	if maxLine > 0 {
		width = len(fmt.Sprintf("%d", maxLine))
	}

	const context = 3
	visible := make([]bool, len(numbered))
	for i, n := range numbered {
		if n.kind != ' ' {
			for j := maxInt(0, i-context); j <= minInt(len(numbered)-1, i+context); j++ {
				visible[j] = true
			}
		}
	}

	out := []string{}
	prevVisible := false
	for i, n := range numbered {
		if !visible[i] {
			prevVisible = false
			continue
		}
		if !prevVisible && i > 0 {
			out = append(out, "···")
		}
		prevVisible = true
		switch n.kind {
		case '-':
			out = append(out, fmt.Sprintf("%*d - │ %s", width, n.line, n.text))
		case '+':
			out = append(out, fmt.Sprintf("%*d + │ %s", width, n.line, n.text))
		default:
			out = append(out, fmt.Sprintf("%*d   │ %s", width, n.line, n.text))
		}
	}
	return strings.Join(limitDiffPreviewLines(out), "\n")
}

// lcsEditScript 使用 Myers 算法计算最短编辑脚本。
func lcsEditScript(a, b []string) []struct {
	kind rune
	oi   int
	ni   int
	text string
} {
	type op = struct {
		kind rune
		oi   int
		ni   int
		text string
	}
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[:i] and b[:j]
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	// 回溯
	ops := []op{}
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append(ops, op{' ', i, j, a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, op{'+', 0, j, b[j-1]})
			j--
		default:
			ops = append(ops, op{'-', i, 0, a[i-1]})
			i--
		}
	}
	// 反转（回溯是倒序的）
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func numberedLines(prefix, content string) []string {
	lines := splitLines(content)
	total := len(lines)
	width := len(fmt.Sprintf("%d", total))
	out := make([]string, total)
	for i, l := range lines {
		out[i] = fmt.Sprintf("%*d %s │ %s", width, i+1, prefix, l)
	}
	return out
}

func formatNumberedDiffLine(prefix string, lineNumber, total int, content string) string {
	width := len(fmt.Sprintf("%d", total))
	return fmt.Sprintf("%*d %s │ %s", width, lineNumber, prefix, content)
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
	return setToolCallBodyStatus(formatToolCallBody(name, input, oldContent), "running")
}

func completeRunningToolCallBody(body, status string) string {
	return setToolCallBodyStatus(body, status)
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
	lines[0] = strings.Join(parts, " · ")
	return strings.Join(lines, "\n")
}

func splitToolSummaryParts(summary string) []string {
	rawParts := strings.Split(summary, " · ")
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
	lines := []string{name + " · " + status}
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

	suffix := strings.Join(nonEmptyStrings(runMode, contextMode), " · ")
	summary := name
	if suffix != "" {
		summary += " · " + suffix
	}

	lines := []string{summary}
	if description != "" {
		lines = append(lines, "description  "+description)
	}
	if contextMode != "" || runMode != "" {
		lines = append(lines, "mode  "+strings.Join(nonEmptyStrings(runMode, contextMode), " · "))
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
	fields := toolInputFields(input)
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, key := range primaryToolInputKeys(lower) {
		if value := strings.TrimSpace(fieldValue(fields, key)); value != "" {
			return strings.Join(strings.Fields(value), " ")
		}
	}
	for _, field := range fields {
		if shouldHideToolDetailField(name, field) {
			continue
		}
		if value := strings.TrimSpace(field.value); value != "" {
			return strings.Join(strings.Fields(value), " ")
		}
	}
	return ""
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

func shortTaskID(id string) string {
	id = strings.TrimSpace(id)
	if len([]rune(id)) <= 12 {
		return id
	}
	runes := []rune(id)
	return string(runes[:10]) + "…"
}

func sanitizeAssistantVisibleBody(body string) string {
	body = stripToolUseFences(body)
	body = stripEmbeddedToolUseJSON(body)
	return strings.TrimSpace(body)
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
