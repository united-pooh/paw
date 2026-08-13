package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"paw/internal/session"
)

// EventRecorder 在状态文件写入成功后记录审计事件（cmd/agent 注入为
// session.AppendStateEvent；nil 时跳过，best-effort）。
type EventRecorder func(ctx context.Context, kind session.StateEventKind, summary string) error

const (
	// MaxMemoryRunes 是全局 memory.md 的长度上限（设计文档 §5.1，8 KiB）。
	MaxMemoryRunes = 8 * 1024
	// MaxAriadneRunes 是 ariadne.md 的长度上限（设计文档 §5.2，16 KiB）。
	MaxAriadneRunes = 16 * 1024
)

// UpdateMemoryTool 写全局长期习惯文件（~/.paw/memory.md）。
type UpdateMemoryTool struct {
	path   string
	record EventRecorder
}

func NewUpdateMemory(path string, record EventRecorder) *UpdateMemoryTool {
	return &UpdateMemoryTool{path: path, record: record}
}

// Bind 在路径与事件记录器就绪后绑定（cmd/agent 延迟注入模式）。
func (t *UpdateMemoryTool) Bind(path string, record EventRecorder) {
	t.path = path
	t.record = record
}

func (t *UpdateMemoryTool) Name() string { return "update_memory" }

func (t *UpdateMemoryTool) Description() string {
	return "更新全局记忆（~/.paw/memory.md）：记录用户长期使用习惯、偏好、项目约定等跨会话信息。不要在每次回复时调用，只在习惯/偏好有实质变化时更新。内容上限 8 KiB。"
}

func (t *UpdateMemoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"content": {"type": "string", "description": "memory.md 的完整新内容（Markdown）"}
		},
		"required": ["content"],
		"additionalProperties": false
	}`)
}

func (t *UpdateMemoryTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("update_memory: 解析参数失败: %w", err)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return "", fmt.Errorf("update_memory: content 不能为空")
	}
	if t.path == "" {
		return "", fmt.Errorf("update_memory: memory 路径未绑定")
	}
	if len([]rune(content)) > MaxMemoryRunes {
		return "", fmt.Errorf("update_memory: 内容超过 %d 字符上限（当前 %d）", MaxMemoryRunes, len([]rune(content)))
	}
	if err := atomicWrite(t.path, content); err != nil {
		return "", err
	}
	if t.record != nil {
		summary := summarize(content)
		if err := t.record(ctx, session.StateEventMemory, summary); err != nil {
			// best-effort：事件失败不影响文件写入结果。
			return fmt.Sprintf("memory.md 已更新（事件记录失败：%v）", err), nil
		}
	}
	return "memory.md 已更新", nil
}

// UpdateAriadneTool 写会话方向记忆（<session>/ariadne.md，五段式）。
type UpdateAriadneTool struct {
	path   string
	record EventRecorder
}

func NewUpdateAriadne(path string, record EventRecorder) *UpdateAriadneTool {
	return &UpdateAriadneTool{path: path, record: record}
}

// Bind 在路径与事件记录器就绪后绑定（cmd/agent 延迟注入模式）。
func (t *UpdateAriadneTool) Bind(path string, record EventRecorder) {
	t.path = path
	t.record = record
}

func (t *UpdateAriadneTool) Name() string { return "update_ariadne" }

func (t *UpdateAriadneTool) Description() string {
	return "更新当前会话的方向记忆（ariadne.md）：供恢复/压缩时替代长对话的结构化状态。必须包含五个 section：## 方向 / ## 进度 / ## 关键决策 / ## 下一步 / ## 教训。进度必须保留所有未完成事项（含早期任务）。内容上限 16 KiB。"
}

func (t *UpdateAriadneTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"content": {"type": "string", "description": "ariadne.md 的完整新内容（Markdown，五段式）"}
		},
		"required": ["content"],
		"additionalProperties": false
	}`)
}

var ariadneSections = []string{"## 方向", "## 进度", "## 关键决策", "## 下一步", "## 教训"}

func validateAriadne(content string) error {
	for _, section := range ariadneSections {
		if !strings.Contains(content, section) {
			return fmt.Errorf("ariadne 缺少 section %q（必须包含：方向/进度/关键决策/下一步/教训）", section)
		}
	}
	return nil
}

func (t *UpdateAriadneTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("update_ariadne: 解析参数失败: %w", err)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return "", fmt.Errorf("update_ariadne: content 不能为空")
	}
	if t.path == "" {
		return "", fmt.Errorf("update_ariadne: ariadne 路径未绑定")
	}
	if len([]rune(content)) > MaxAriadneRunes {
		return "", fmt.Errorf("update_ariadne: 内容超过 %d 字符上限（当前 %d）", MaxAriadneRunes, len([]rune(content)))
	}
	if err := validateAriadne(content); err != nil {
		return "", err
	}
	if err := atomicWrite(t.path, content); err != nil {
		return "", err
	}
	if t.record != nil {
		summary := summarize(content)
		if err := t.record(ctx, session.StateEventAriadne, summary); err != nil {
			return fmt.Sprintf("ariadne.md 已更新（事件记录失败：%v）", err), nil
		}
	}
	return "ariadne.md 已更新", nil
}

func summarize(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= 200 {
		return string(runes)
	}
	return string(runes[:200]) + "…"
}

func atomicWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换状态文件失败: %w", err)
	}
	return nil
}
