// 本文件提供 TUI 渲染和事件展示过程中使用的通用小工具。
package bubble

import (
	"encoding/json"
	"strings"
)

// summarizeToolContent 将工具输出压缩为单行短预览，避免 transcript 被长结果撑开。
func summarizeToolContent(content string) string {
	trimmed := strings.Join(strings.Fields(content), " ")
	if trimmed == "" {
		return ""
	}
	const maxPreviewRunes = 140
	runes := []rune(trimmed)
	if len(runes) <= maxPreviewRunes {
		return trimmed
	}
	return string(runes[:maxPreviewRunes]) + "..."
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
