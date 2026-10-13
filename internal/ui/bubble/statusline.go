package bubble

import (
	"fmt"
	"strings"
	"time"

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

// workStatusDoneWindow 是回合完成后「完成」态在状态行的停留窗口。
const workStatusDoneWindow = 10 * time.Second

// workStatusState 解析当前状态行应显示的内容。优先级：
// 权限等待 > 工具运行 > 思考中 > 刚完成 > 空闲（不渲染）。
//
// 注意热路径纪律：本函数经 withWorkStatusLine→currentLayout 进入
// filterIdleMouseMotion 的零分配断言路径。空闲（最常见）分支必须保持
// 零分配——只有确认非空闲后才允许格式化字符串/取 token 视图（view()
// 内含 make([]float64, 60)）。
func (m appModel) workStatusState(now time.Time) (workStatusKind, string, string, string) {
	if now.IsZero() {
		now = time.Now()
	}
	if tool := m.latestRunningToolName(); tool != "" {
		if isInteractiveToolName(tool) {
			return workStatusPermission, "", m.turnElapsed(now), m.turnTokRate(now)
		}
		return workStatusTool, tool, m.turnElapsed(now), m.turnTokRate(now)
	}
	if m.isAgentWorking() {
		return workStatusThinking, "", m.turnElapsed(now), m.turnTokRate(now)
	}
	if finished := m.tokenMonitor.live.turnFinished; !finished.IsZero() && now.Sub(finished) < workStatusDoneWindow {
		if turn := m.tokenMonitor.live.view(now).TurnElapsed; turn > 0 {
			return workStatusDone, "", formatTurnSeconds(int64(turn / time.Second)), ""
		}
	}
	return workStatusIdle, "", "", ""
}

// workStatusLineVisible 报告状态行是否占位（空闲时整行不渲染）。
func (m appModel) workStatusLineVisible(now time.Time) bool {
	kind, _, _, _ := m.workStatusState(now)
	return kind != workStatusIdle
}

// latestRunningToolName 返回最后一个仍在运行的工具名；没有则空串。
func (m appModel) latestRunningToolName() string {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		entry := m.transcript[i]
		if isToolTransaction(entry) && toolEntryStatus(entry) == "running" {
			return entry.toolName
		}
	}
	return ""
}

// turnElapsed 格式化当前回合已进行时间；回合未开始返回空串。
func (m appModel) turnElapsed(now time.Time) string {
	if m.turnStartedAt.IsZero() {
		return ""
	}
	return formatTurnTimer(m.turnStartedAt, now)
}

// turnTokRate 返回当前回合输出 tok/s（压缩格式）；无样本返回空串。
func (m appModel) turnTokRate(now time.Time) string {
	v := m.tokenMonitor.live.view(now)
	rate := tokenMonitorRate(v.Current)
	if rate <= 0 {
		return ""
	}
	return formatCompactTokenCount(int(rate))
}

// renderWorkStatusRegion 把状态行渲染成严格 width cell 的一行。
func (m appModel) renderWorkStatusRegion(width int) string {
	width = maxInt(1, width)
	kind, tool, elapsed, tokRate := m.workStatusState(m.animationNow())
	return fitStyledCellLine(renderWorkStatusLine(kind, tool, elapsed, tokRate), width)
}

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
