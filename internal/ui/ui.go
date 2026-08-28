package ui

import (
	"encoding/json"
	"time"
)

// FileMutationSnapshot contains the UI-only before/after state of a file mutation.
// It is intentionally kept out of message.ToolResult and tracing payloads.
type FileMutationSnapshot struct {
	Before       string
	After        string
	BeforeExists bool
	AfterExists  bool
}

// ToolCallEvent 描述一次工具调用事件。
// UI 只关心展示，因此这里保留最小字段集合。
type ToolCallEvent struct {
	ID                string
	Name              string
	Input             json.RawMessage
	FileMutationKnown bool
	IsFileMutation    bool
	FileMutation      *FileMutationSnapshot
	// ArgsGenStartedAt 是工具参数开始流式生成的时刻（无流式窗口时为零值）：
	// UI 工具耗时的计费起点（参数生成→执行完成合并口径）。
	ArgsGenStartedAt time.Time
}

// ToolResultEvent describes one tool execution result event.
type ToolResultEvent struct {
	ToolUseID         string
	Name              string
	Content           string
	IsError           bool
	FileMutationKnown bool
	IsFileMutation    bool
	FileMutation      *FileMutationSnapshot
}

// SystemEvent 描述由后台任务或控制器产生的系统消息。
type SystemEvent struct {
	Title string
	Body  string
	Color string // 可选：标题颜色（lipgloss 颜色字符串），与 taskController 面板保持一致
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

// AssistantPartReceiver 是 UI 的可选扩展，用于接收有序助理 part 生命周期事件。
// partIndex 是 provider block 索引，对应 model.AssistantPartEvent.BlockIndex。
// 实现者应处理 live reasoning 渲染、完成折叠等。
type AssistantPartReceiver interface {
	OnReasoningStart(partIndex int, redacted bool) error
	OnReasoningDelta(partIndex int, text string) error
	OnReasoningEnd(partIndex int) error
}

// SystemNotifier 是 UI 的可选扩展，用于接收后台任务完成等系统事件。
type SystemNotifier interface {
	OnSystemMessage(event SystemEvent) error
}

// FileMutationConsumer is an optional UI capability. Only consumers opting in
// cause the runner to inspect mutation targets or read file snapshots.
type FileMutationConsumer interface {
	ConsumesFileMutations() bool
}
