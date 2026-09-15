// 本文件定义 /model 切换成功条目的结构化 <model> 块：生成、检测、解析与
// transcript 状态卡渲染，模式对齐 transcript.go 的 <task> 完成块。
package bubble

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"paw/internal/capability/model"
)

// escapeTaskBlockAttrValue 转义结构化块属性值，与 unescapeTaskBlockAttr 对偶。
func escapeTaskBlockAttrValue(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(value)
}

// formatModelSwitchBlock 生成模型切换成功条目的结构化块。
// 属性固定顺序，空值整体省略；retries 恒输出（0 也是有效配置）。
func formatModelSwitchBlock(cfg model.Config, generalLimit int) string {
	type attr struct{ key, value string }
	pairs := []attr{
		{"provider", strings.TrimSpace(cfg.Provider)},
		{"model", strings.TrimSpace(cfg.Model)},
		{"base", strings.TrimSpace(cfg.APIBaseURL)},
		{"path", strings.TrimSpace(cfg.APIPath)},
	}
	if limit := model.ResolveContextLimitTokens(cfg, generalLimit); limit > 0 {
		pairs = append(pairs, attr{"context", strconv.Itoa(limit)})
	}
	pairs = append(pairs, attr{"retries", strconv.Itoa(cfg.RetryCount)})
	if env := strings.TrimSpace(cfg.APIKeyEnvName); env != "" {
		pairs = append(pairs, attr{"key_env", env})
	}
	rendered := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair.value == "" {
			continue
		}
		rendered = append(rendered, fmt.Sprintf(`%s="%s"`, pair.key, escapeTaskBlockAttrValue(pair.value)))
	}
	return "<model " + strings.Join(rendered, " ") + ">\n</model>"
}

type modelCardInfo struct {
	Provider string
	Model    string
	Base     string
	Path     string
	Context  int
	Retries  int
	KeyEnv   string
}

// isModelCardBlock 检测 <model> 切换块，模式与 isTaskCompletionBlock 一致。
func isModelCardBlock(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<model ") && strings.HasSuffix(trimmed, "</model>")
}

// parseModelCardBlock 解析 <model> 块头部属性，复用 task 块的属性正则与反转义。
func parseModelCardBlock(body string) (modelCardInfo, bool) {
	trimmed := strings.TrimSpace(body)
	if !isModelCardBlock(trimmed) {
		return modelCardInfo{}, false
	}
	headerEnd := strings.IndexByte(trimmed, '>')
	if headerEnd < 0 {
		return modelCardInfo{}, false
	}
	info := modelCardInfo{}
	for _, match := range taskBlockAttrPattern.FindAllStringSubmatch(trimmed[:headerEnd], -1) {
		value := unescapeTaskBlockAttr(match[2])
		switch match[1] {
		case "provider":
			info.Provider = value
		case "model":
			info.Model = value
		case "base":
			info.Base = value
		case "path":
			info.Path = value
		case "context":
			info.Context, _ = strconv.Atoi(value)
		case "retries":
			info.Retries, _ = strconv.Atoi(value)
		case "key_env":
			info.KeyEnv = value
		}
	}
	return info, true
}

func renderModelSwitchCard(body string, width int) string {
	return renderModelSwitchNotice(body, width, false)
}

func renderModelSwitchNotice(body string, width int, expanded bool) string {
	width = maxInt(1, width)
	info, ok := parseModelCardBlock(body)
	if !ok {
		return bodyStyle.Width(width).Render(sanitizeTerminalText(body))
	}
	titleStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorWorktreeClean)).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
	name := strings.TrimSpace(info.Model)
	if name == "" {
		name = "unknown"
	}
	disclosure := "▸ 详情"
	if expanded {
		disclosure = "▾ 收起"
	}
	header := titleStyle.Render("✓") + " " + mutedStyle.Render("已切换模型 · ") + bodyStyle.Render(sanitizeTerminalText(name))
	if info.Provider != "" {
		header += mutedStyle.Render(" · " + sanitizeTerminalText(info.Provider))
	}
	disclosure = mutedStyle.Render("  " + disclosure)
	if width > terminalCellWidth(disclosure)+3 {
		header = truncateStyledCellLine(header, width-terminalCellWidth(disclosure)) + disclosure
	} else {
		header = truncateStyledCellLine(header, width)
	}
	if !expanded {
		return header
	}
	lines := []string{header, bodyStyle.Render(sanitizeTerminalText(name))}

	metaParts := make([]string, 0, 3)
	if info.Provider != "" {
		metaParts = append(metaParts, sanitizeTerminalText(info.Provider))
	}
	if info.Context > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d ctx", info.Context))
	}
	if info.Retries > 0 {
		metaParts = append(metaParts, fmt.Sprintf("retry ×%d", info.Retries))
	}
	if len(metaParts) > 0 {
		lines = append(lines, mutedStyle.Render(strings.Join(metaParts, " · ")))
	}

	type detailRow struct{ label, value string }
	details := make([]detailRow, 0, 3)
	if info.Base != "" {
		details = append(details, detailRow{"base", info.Base})
	}
	if info.Path != "" {
		details = append(details, detailRow{"path", info.Path})
	}
	if info.KeyEnv != "" {
		details = append(details, detailRow{"key env", info.KeyEnv})
	}
	if len(details) > 0 {
		labelWidth := 0
		for _, r := range details {
			labelWidth = maxInt(labelWidth, len(r.label))
		}
		labelWidth += 2
		for _, r := range details {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("%-*s", labelWidth, r.label))+bodyStyle.Render(sanitizeTerminalText(r.value)))
		}
	}

	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, wrapStyledCellText(line, width)...)
	}
	return strings.Join(content, "\n")
}

func (m *appModel) toggleModelNoticeAtRow(row int) bool {
	for _, location := range m.transcriptEntryLocationsAt() {
		if row != location.startRow {
			continue
		}
		index, ok := m.transcriptIndexForViewEntry(location.transcriptIndex)
		if !ok {
			return false
		}
		entry := &m.transcript[index]
		if entry.kind != entrySystem || !isModelCardBlock(entry.body) {
			continue
		}
		expanded := m.showThinking
		if entry.modelDetailsExpanded != nil {
			expanded = *entry.modelDetailsExpanded
		}
		expanded = !expanded
		entry.modelDetailsExpanded = &expanded
		m.touchTranscriptEntryAt(index)
		m.refreshViewportPreservingOffset()
		return true
	}
	return false
}
