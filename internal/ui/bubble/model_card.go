// 本文件定义 /model 切换成功条目的结构化 <model> 块：生成、检测、解析与
// transcript 状态卡渲染，模式对齐 transcript.go 的 <task> 完成块。
package bubble

import (
	"fmt"
	"strconv"
	"strings"

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
