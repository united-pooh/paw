package ui

import "encoding/json"

// ToolCallEvent 描述一次工具调用事件。
// UI 只关心展示，因此这里保留最小字段集合。
type ToolCallEvent struct {
	ID         string
	Name       string
	Input      json.RawMessage
	OldContent string // 文件变更工具执行前的旧文件内容（Write/Edit），由调用方在工具执行前同步读取
}

// ToolResultEvent 描述一次工具执行结果事件。
// Name 单独保留一份，避免 UI 还要再从其他结构推断工具名。
type ToolResultEvent struct {
	ToolUseID string
	Name      string
	Content   string
	IsError   bool
}

// SystemEvent 描述由后台任务或控制器产生的系统消息。
type SystemEvent struct {
	Title string
	Body  string
	Color string // 可选：标题颜色（lipgloss 颜色字符串），与 subagents 面板保持一致
}

// UI 定义 loop 层依赖的最小输出接口。
// 当前既要支持纯文本流式渲染，也要支持工具调用/结果事件展示。
type UI interface {
	OnAssistantDelta(text string) error
	OnToolCall(event ToolCallEvent) error
	OnToolResult(event ToolResultEvent) error
	OnDone() error
}

// ThinkingDeltaReceiver 是 UI 的可选扩展，用于接收模型 thinking 流。
type ThinkingDeltaReceiver interface {
	OnThinkingDelta(text string) error
}

// SystemNotifier 是 UI 的可选扩展，用于接收后台任务完成等系统事件。
type SystemNotifier interface {
	OnSystemMessage(event SystemEvent) error
}

// OldContentConsumer 是 UI 的可选扩展，声明该 UI 会消费 ToolCallEvent.OldContent。
// 未实现此接口的 UI（如 sinkUI）不会触发磁盘读取，避免浪费 I/O。
type OldContentConsumer interface {
	ConsumesOldContent() bool
}
