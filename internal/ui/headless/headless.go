package headless

import (
	"fmt"
	"io"
	"paw/internal/ui"
	"strings"
	"sync"
)

// UI 提供最小的 headless stdout 渲染能力。
// 设计目标：
// 1) 增量内容立即输出（模拟打字流）
// 2) 一轮结束时统一补换行
// 3) 在并发场景下保持输出与状态一致
type UI struct {
	// out 是输出目标，通常为 os.Stdout。
	out io.Writer
	// mu 保护 out 写入和 wrote 状态，避免并发调用时交叉写。
	mu sync.Mutex
	// wrote 标记“本轮是否已经写过内容”，用于决定 OnDone 是否补换行。
	wrote bool
}

var _ ui.UI = (*UI)(nil)

// New 创建 headless UI。
func New(out io.Writer) *UI {
	return &UI{out: out}
}

// OnAssistantDelta 输出增量文本。
// 调用方每收到一段 delta 就调用一次。
func (u *UI) OnAssistantDelta(text string) error {
	// 空增量无需输出，减少无意义写操作。
	if text == "" {
		return nil
	}

	// 锁住“写输出 + 更新状态”这两个动作，保证它们原子化。
	u.mu.Lock()
	defer u.mu.Unlock()

	// 将当前增量直接写到终端，不做缓存，保证流式体验。
	if _, err := io.WriteString(u.out, text); err != nil {
		return err
	}
	// 记录本轮已输出过内容，供 OnDone 判断是否需要补换行。
	u.wrote = true
	return nil
}

// OnToolCall 输出工具调用事件。
// 工具事件单独成行，避免和 assistant 流式文本混在一起。
func (u *UI) OnToolCall(event ui.ToolCallEvent) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.startEventLineLocked(); err != nil {
		return err
	}

	input := strings.TrimSpace(string(event.Input))
	if input == "" {
		input = "{}"
	}

	_, err := fmt.Fprintf(u.out, "[tool] %s %s\n", event.Name, input)
	return err
}

// OnToolResult 输出工具结果事件。
// 这里默认只打印简短摘要，避免把大块工具输出直接刷到终端。
func (u *UI) OnToolResult(event ui.ToolResultEvent) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.startEventLineLocked(); err != nil {
		return err
	}

	status := "ok"
	if event.IsError {
		status = "error"
	}

	preview := summarizeToolContent(event.Content)
	if preview == "" {
		_, err := fmt.Fprintf(u.out, "[tool-result] %s %s\n", event.Name, status)
		return err
	}

	_, err := fmt.Fprintf(u.out, "[tool-result] %s %s: %s\n", event.Name, status, preview)
	return err
}

// OnSystemMessage 输出后台任务等系统事件。
func (u *UI) OnSystemMessage(event ui.SystemEvent) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if err := u.startEventLineLocked(); err != nil {
		return err
	}
	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "system"
	}
	_, err := fmt.Fprintf(u.out, "[system] %s: %s\n", title, strings.TrimSpace(event.Body))
	return err
}

// OnDone 在一轮流式输出结束后补一个换行。
// 这样下一次日志/提示不会紧贴在模型输出后面。
func (u *UI) OnDone() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// 本轮无输出时不补换行，避免产生空白行噪声。
	if !u.wrote {
		return nil
	}

	// 补一个换行，完成本轮渲染收尾。
	if _, err := io.WriteString(u.out, "\n"); err != nil {
		return err
	}
	// 重置轮次状态，准备下一轮输出。
	u.wrote = false
	return nil
}

func (u *UI) startEventLineLocked() error {
	if !u.wrote {
		return nil
	}
	if _, err := io.WriteString(u.out, "\n"); err != nil {
		return err
	}
	u.wrote = false
	return nil
}

func summarizeToolContent(content string) string {
	trimmed := strings.Join(strings.Fields(content), " ")
	if trimmed == "" {
		return ""
	}

	const maxPreviewRunes = 120
	runes := []rune(trimmed)
	if len(runes) <= maxPreviewRunes {
		return trimmed
	}
	return string(runes[:maxPreviewRunes]) + "..."
}
