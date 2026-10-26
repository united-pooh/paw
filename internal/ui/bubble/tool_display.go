package bubble

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	filetool "paw/internal/capability/tool/file"
	"paw/internal/todo"
)

type toolDisplay struct {
	name   string
	target string
}

type toolDisplayRule struct {
	server     string
	action     string
	targetKeys []string
	pathTarget bool
}

func workspaceRootOf(runner Runner) string {
	if provider, ok := runner.(WorkspaceRootProvider); ok {
		return strings.TrimSpace(provider.WorkspaceRoot())
	}
	return ""
}

var toolDisplayRules = []toolDisplayRule{
	{server: "codegraph", action: "read_url", targetKeys: []string{"url"}},
	{server: "codegraph", action: "read_page", targetKeys: []string{"url", "path"}},
	{server: "codegraph", action: "search", targetKeys: []string{"query", "q", "path"}},
	{server: "codegraph", action: "search_web", targetKeys: []string{"query", "q", "url"}},
}

var toolDisplayActions = map[string]string{
	"read_url":   "读取页面",
	"read_page":  "读取页面",
	"search":     "搜索",
	"search_web": "搜索",
}

// buildToolDisplay converts a raw tool call into the compact one-line label
// used by the transcript while leaving the raw name available on the entry.
func buildToolDisplay(name string, input json.RawMessage, workspaceRoot string) toolDisplay {
	return toolDisplay{
		name:   displayToolName(name),
		target: displayToolTarget(name, input, workspaceRoot),
	}
}

// displayToolName hides MCP namespaces and keeps native tool names compact.
// Namespaced tools are rendered as "Server: action"; native tools as
// "Tool:" so a following target reads naturally in the terminal row.
func displayToolName(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "update_todo") {
		return "Todo:"
	}
	server, action, namespaced := splitToolName(name)
	if namespaced {
		server = displayServerName(server)
		return server + ": " + displayToolAction(action)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	return name + ":"
}

func displayToolAction(name string) string {
	name = normalizeToolPart(name)
	if action, ok := toolDisplayActions[name]; ok {
		return action
	}
	if name == "" {
		return "工具"
	}
	return name
}

func displayToolTarget(name string, input json.RawMessage, workspaceRoot string) string {
	if strings.EqualFold(strings.TrimSpace(name), "update_todo") {
		return "update"
	}
	fields := toolInputFields(input)
	server, action, namespaced := splitToolName(name)
	keys := primaryToolInputKeys(strings.ToLower(strings.TrimSpace(name)))
	pathTarget := isNativePathTool(name)
	if namespaced {
		rule := findToolDisplayRule(server, action)
		if rule != nil {
			keys = rule.targetKeys
			pathTarget = rule.pathTarget
		} else {
			keys = primaryToolInputKeys(action)
		}
	}

	for _, key := range keys {
		if value := toolDisplayFieldValue(fields, key); value != "" {
			return formatToolTarget(value, pathTarget, workspaceRoot)
		}
	}
	for _, field := range fields {
		if shouldHideToolDetailField(name, field) {
			continue
		}
		if value := strings.TrimSpace(field.value); value != "" {
			return formatToolTarget(value, pathTarget, workspaceRoot)
		}
	}
	return ""
}

func compactUpdateTodoResult(content string) string {
	var result todo.UpdateResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || !result.Accepted {
		return "updated"
	}
	snapshot, err := todo.ValidateSnapshot(result.Snapshot)
	if err != nil {
		return "updated"
	}
	if snapshot.Cleared() {
		return "cleared"
	}
	return fmt.Sprintf("updated %d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
}

func toolDisplayFieldValue(fields []toolDisplayField, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, field := range fields {
		if strings.ToLower(strings.TrimSpace(field.key)) == key {
			return strings.Join(strings.Fields(strings.TrimSpace(field.value)), " ")
		}
	}
	return ""
}

func formatToolTarget(value string, pathTarget bool, workspaceRoot string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if !pathTarget {
		if formatted, ok := humanizeURLTarget(value); ok {
			return formatted
		}
		return value
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return value
	}
	return filetool.DisplayPath(workspaceRoot, value)
}

var searchQueryKeys = []string{"q", "query", "search_query", "wd", "keyword", "keywords", "p"}

// humanizeURLTarget 将带查询串或百分号编码的网页 URL 提炼为高可读文本：
// 1. 含常见搜索词参数（q/query/...）的 URL 提炼为 `域名 · "搜索词"`（自动去外层重复引号）；
// 2. 其他含 %XX / + 编码的 URL 自动解码，避免 `%22Query-Conditioned+...` 刷屏。
func humanizeURLTarget(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	if parsed.RawQuery != "" {
		values, qErr := url.ParseQuery(parsed.RawQuery)
		if qErr == nil {
			for _, key := range searchQueryKeys {
				if q := strings.TrimSpace(values.Get(key)); q != "" {
					q = strings.Join(strings.Fields(q), " ")
					q = strings.Trim(q, `"`)
					host := strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
					return fmt.Sprintf(`%s · "%s"`, host, q), true
				}
			}
		}
	}
	if strings.Contains(raw, "%") {
		if decoded, dErr := url.QueryUnescape(raw); dErr == nil && decoded != raw {
			return strings.Join(strings.Fields(decoded), " "), true
		}
	}
	return "", false
}

// extractRawToolURL 提取工具输入中的原始 http(s) URL（供 OSC 8 超链接跳转原网页使用）。
func extractRawToolURL(name string, input json.RawMessage) string {
	fields := toolInputFields(input)
	server, action, namespaced := splitToolName(name)
	keys := primaryToolInputKeys(strings.ToLower(strings.TrimSpace(name)))
	if namespaced {
		if rule := findToolDisplayRule(server, action); rule != nil {
			keys = rule.targetKeys
		} else {
			keys = primaryToolInputKeys(action)
		}
	}
	for _, key := range keys {
		if v := toolDisplayFieldValue(fields, key); isClickableTerminalURL(v) {
			return v
		}
	}
	for _, field := range fields {
		if v := strings.TrimSpace(field.value); isClickableTerminalURL(v) {
			return v
		}
	}
	return ""
}

func splitToolName(name string) (server, action string, namespaced bool) {
	name = strings.TrimSpace(name)
	parts := strings.SplitN(name, "__", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", name, false
	}
	return normalizeToolPart(parts[0]), normalizeToolPart(parts[1]), true
}

func normalizeToolPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func displayServerName(server string) string {
	switch normalizeToolPart(server) {
	case "codegraph":
		return "CodeGraph"
	case "github":
		return "GitHub"
	default:
		server = strings.TrimSpace(server)
		if server == "" {
			return "MCP"
		}
		return strings.ToUpper(server[:1]) + server[1:]
	}
}

func findToolDisplayRule(server, action string) *toolDisplayRule {
	server = normalizeToolPart(server)
	action = normalizeToolPart(action)
	for index := range toolDisplayRules {
		rule := &toolDisplayRules[index]
		if rule.server == server && rule.action == action {
			return rule
		}
	}
	return nil
}

func isNativePathTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range []string{"ls", "glob", "read", "write", "edit", "update"} {
		if name == candidate {
			return true
		}
	}
	return false
}
