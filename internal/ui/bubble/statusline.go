package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// statusline.go：底部唯一规范状态行。
// 格式：`⚒ {动作}…  ctrl+c 中断 · {耗时} · ↓{tok/s}`
// ⚒ 是静态字符——耗时与 tok/s 数字本身在跳动，即是活动指示（同 commandcode）。
// 空闲时整行不渲染（返回 ""）。

type workStatusKind int

const (
	workStatusIdle workStatusKind = iota
	workStatusThinking
	workStatusTool
	workStatusPermission
	workStatusResuming
	workStatusDone
)

// renderWorkStatusLine 组装状态行。elapsed/tokRate 为已格式化字符串
// （如 "2m 28s"、"1.2k"）；为空的分段自动省略。idle 返回空串。
func renderWorkStatusLine(kind workStatusKind, toolName, elapsed, tokRate string) string {
	if kind == workStatusIdle {
		return ""
	}
	accent := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorAccent)).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))

	var action string
	switch kind {
	case workStatusThinking:
		action = copyStatusThinking + "…"
	case workStatusTool:
		label := chipLabel(toolName)
		if strings.TrimSpace(toolName) != "" {
			label = renderToolChip(toolName)
		}
		action = fmt.Sprintf(copyStatusRunningTool, label) + "…"
	case workStatusPermission:
		action = copyStatusPermission + "…"
	case workStatusResuming:
		action = copyStatusResuming + "…"
	case workStatusDone:
		action = copyStatusDone
	}

	parts := []string{accent.Render(glyphStatus + " " + action)}
	if kind != workStatusDone && kind != workStatusResuming {
		parts = append(parts, dim.Render(copyStatusInterrupt))
	}
	if elapsed = strings.TrimSpace(elapsed); elapsed != "" {
		parts = append(parts, dim.Render(elapsed))
	}
	if tokRate = strings.TrimSpace(tokRate); tokRate != "" {
		parts = append(parts, dim.Render("↓ "+tokRate+" tok"))
	}
	// 动作与提示之间空两格，其后分段用 " · "（对齐 commandcode 的疏密节奏）。
	head := parts[0]
	rest := strings.Join(parts[1:], dim.Render(" · "))
	if rest == "" {
		return " " + head
	}
	return " " + head + "  " + rest
}
