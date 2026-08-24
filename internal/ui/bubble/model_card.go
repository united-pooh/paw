// 本文件定义 /model 切换成功条目的结构化 <model> 块：生成、检测、解析与
// transcript 状态卡渲染，模式对齐 transcript.go 的 <task> 完成块。
package bubble

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"paw/internal/model"
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
func formatModelSwitchBlock(cfg model.Config) string {
	type attr struct{ key, value string }
	pairs := []attr{
		{"provider", strings.TrimSpace(cfg.Provider)},
		{"model", strings.TrimSpace(cfg.Model)},
		{"base", strings.TrimSpace(cfg.APIBaseURL)},
		{"path", strings.TrimSpace(cfg.APIPath)},
	}
	if limit := model.EffectiveContextLimitTokens(cfg); limit > 0 {
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

// renderModelSwitchCard 把 <model> 切换块渲染为绿框状态卡，
// 边框与宽度算法对齐 renderTaskCompletionCard。
func renderModelSwitchCard(body string, width int) string {
	info, ok := parseModelCardBlock(body)
	if !ok {
		return bodyStyle.Width(width).Render(sanitizeTerminalText(body))
	}
	borderColor := colorManager.LipglossColor(colorWorktreeClean)
	titleStyle := lipgloss.NewStyle().Foreground(borderColor).Bold(true)
	nameStyle := lipgloss.NewStyle().Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))

	lines := []string{titleStyle.Render("✓ 模型已生效"), ""}
	name := strings.TrimSpace(info.Model)
	if name == "" {
		name = "unknown"
	}
	lines = append(lines, nameStyle.Render(name))

	metaParts := make([]string, 0, 3)
	if info.Provider != "" {
		metaParts = append(metaParts, info.Provider)
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
		lines = append(lines, "")
		labelWidth := 0
		for _, r := range details {
			labelWidth = maxInt(labelWidth, len(r.label))
		}
		labelWidth += 2
		for _, r := range details {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("%-*s", labelWidth, r.label))+r.value)
		}
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
	horizontalBorder := cardStyle.GetHorizontalBorderSize()
	styleWidth := maxInt(1, width-horizontalBorder)
	bodyWidth := maxInt(1, styleWidth-cardStyle.GetHorizontalPadding())
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, fitStyledCellLine(truncateStyledCellLine(line, bodyWidth), bodyWidth))
	}
	return cardStyle.Width(styleWidth).Render(strings.Join(content, "\n"))
}
