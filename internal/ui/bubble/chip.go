package bubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// chip.go：工具类别 chip 与状态文字组件。
// chip = 彩色小标签（读取/编辑/Shell/搜索/网页/任务/插件），替代裸 "Edit:" 标识符；
// 状态词（完成/运行中/失败）从实底药丸改为彩色文字。

type chipKind int

const (
	chipKindRead chipKind = iota
	chipKindEdit
	chipKindWrite
	chipKindShell
	chipKindSearch
	chipKindWeb
	chipKindTask
	chipKindTodo
	chipKindMemo
	chipKindPlugin
	chipKindTool
)

// chipKindForTool 把原始工具名归一到类别。原生工具名是 PascalCase
// （Read/Write/Edit/Bash/Grep/Glob/LS/WebFetch）；MCP 工具带 server__action
// 命名空间，归到插件。
func chipKindForTool(name string) chipKind {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, _, namespaced := splitToolName(name); namespaced {
		return chipKindPlugin
	}
	switch name {
	case "read":
		return chipKindRead
	case "edit":
		return chipKindEdit
	case "write":
		return chipKindWrite
	case "bash":
		return chipKindShell
	case "grep", "glob", "ls", "search_transcript":
		return chipKindSearch
	case "webfetch":
		return chipKindWeb
	case "update_todo":
		return chipKindTodo
	case "update_memory", "update_ariadne":
		return chipKindMemo
	default:
		return chipKindTool
	}
}

func (k chipKind) label() string {
	switch k {
	case chipKindRead:
		return copyChipRead
	case chipKindEdit:
		return copyChipEdit
	case chipKindWrite:
		return copyChipWrite
	case chipKindShell:
		return copyChipShell
	case chipKindSearch:
		return copyChipSearch
	case chipKindWeb:
		return copyChipWeb
	case chipKindTask:
		return copyChipTask
	case chipKindTodo:
		return copyChipTodo
	case chipKindMemo:
		return copyChipMemo
	case chipKindPlugin:
		return copyChipPlugin
	default:
		return copyChipTool
	}
}

func (k chipKind) colorRole() colorRole {
	switch k {
	case chipKindRead:
		return colorChipRead
	case chipKindEdit, chipKindWrite:
		return colorChipEdit // 同类同色：写入与编辑共用一个色相
	case chipKindShell:
		return colorChipShell
	case chipKindSearch, chipKindWeb:
		return colorChipSearch // 同类同色：网页与搜索共用一个色相
	case chipKindTask, chipKindTodo:
		return colorChipTask
	default:
		return colorChipPlugin
	}
}

// chipLabel 返回纯文本标签，供状态行「执行 读取 中」与折叠组头文件清单使用。
func chipLabel(toolName string) string {
	return chipKindForTool(toolName).label()
}

// chipStyle 构造白字彩底小标签。终端 ANSI 模式下前景固定亮白
// （浅底色 chip——黄/浅灰——用纯黑前景保证可读）；hex 主题沿用
// "药丸=角色底+主题底色字"。
func chipStyle(kind chipKind) lipgloss.Style {
	role := kind.colorRole()
	fg := colorManager.LipglossColor(colorTerminalBackground)
	if colorManager.palette.TerminalBackground == "" {
		if role == colorChipTask || role == colorChipPlugin {
			fg = lipgloss.Color("0")
		} else {
			fg = lipgloss.Color("15")
		}
	}
	return lipgloss.NewStyle().
		Background(colorManager.LipglossColor(role)).
		Foreground(fg).
		Bold(true).
		Padding(0, 1)
}

// renderToolChip 渲染 `[读取]` 这样的彩色 chip（不含目标文件）。
// MCP 工具显示 server 名，无类别 chip 时退化为灰色工具 chip。
func renderToolChip(toolName string) string {
	kind := chipKindForTool(toolName)
	return chipStyle(kind).Render(kind.label())
}

// statusTextStyle 返回状态词对应的彩色文字样式（不再是实底药丸）。
func statusTextStyle(status string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "completed", "complete":
		return lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorStatusSuccess)).Bold(true)
	case "running", "pending", "in_progress":
		return lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorStatusRunning)).Bold(true)
	case "error", "failed", "failure":
		return lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorStatusError)).Bold(true)
	case "canceled", "cancelled":
		return lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree)).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
	}
}

// statusTextLabel 归一化状态词；出错统一改失败。
func statusTextLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "completed", "complete":
		return copyToolStatusOK
	case "running", "pending", "in_progress":
		return copyToolStatusRunning
	case "error", "failed", "failure":
		return copyToolStatusError
	default:
		status = strings.TrimSpace(status)
		if status == "" {
			return copyToolStatusUnknown
		}
		return status
	}
}

// renderStatusText 渲染 `完成 1.2s` 这样的彩色状态文字。
func renderStatusText(status, duration string) string {
	label := statusTextLabel(status)
	if duration = strings.TrimSpace(duration); duration != "" {
		label += " " + duration
	}
	return statusTextStyle(status).Render(label)
}
