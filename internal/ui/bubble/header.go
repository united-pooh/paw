// 顶部 header 渲染层。
//
// 设计遵循 UI/数据隔离原则：
//   - headerSnapshot 是纯数据快照，由 collectHeaderData 从各 provider 收集；
//   - renderHeader 是纯函数，只吃快照+宽度，按 cell 预算截断，不访问任何状态。
//
// 数据内容绝不破坏布局：每个字段有 cell 预算上限，超长用 truncateStyledCellLine
// 截断；终端过窄时按优先级（右→左）丢弃字段，最终输出严格等于 width 个 cell。
package bubble

import (
	"fmt"
	"strings"
	"time"
)

// headerSnapshot 是 header 渲染所需的全部数据。字段已 sanitize，渲染层不再
// 触碰数据源。
type headerSnapshot struct {
	modelLabel  string
	statusLabel string
	now         time.Time
}

// headerFieldSeparator 是字段间的分隔符，占 3 个 cell。
const headerFieldSeparator = "  "

// renderHeader 把快照渲染成严格 width cell 宽的一行。纯函数。
// 左侧显示模型和运行状态，当前时间固定贴右。
func renderHeader(s headerSnapshot, width int) string {
	if width <= 0 {
		return ""
	}

	// 模型名是唯一的用户可控长字符串，给独立预算并截断。
	modelBudget := clampInt(width/3, 6, 28)
	model := truncateStyledCellLine(s.modelLabel, modelBudget)
	status := strings.TrimSpace(s.statusLabel)
	if status == "" {
		status = "ready"
	}
	left := model + headerFieldSeparator + status
	clock := s.now.Format("15:04")

	leftW := terminalCellWidth(left)
	rightW := terminalCellWidth(clock)
	if leftW+rightW+terminalCellWidth(headerFieldSeparator) <= width {
		return left + strings.Repeat(" ", width-leftW-rightW) + clock
	}
	if width >= rightW {
		return fitStyledCellLine(truncateStyledCellLine(left, maxInt(1, width-rightW))+clock, width)
	}
	return fitStyledCellLine(truncateStyledCellLine(left, width), width)
}

// formatTurnTimer 把轮次耗时格式化为 header 用字符串。
// 运行中的短任务显示秒数，超过一分钟显示 mm:ss。
func formatTurnTimer(startedAt, now time.Time) string {
	if startedAt.IsZero() || now.Before(startedAt) {
		return "0s"
	}
	seconds := int(now.Sub(startedAt) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
}

// collectHeaderData 从现有 provider 读出 header 数据快照。这是 UI 与数据源的
// 唯一汇聚点：renderHeader 只读返回的快照，不再触碰 runner/loop/settings。
// now 是渲染时刻（由调用方传入 cursorFrameAt，保证与帧同步且可测试）。
func (m appModel) collectHeaderData(now time.Time) headerSnapshot {
	cfg := m.currentModelConfig()
	label := strings.TrimSpace(sanitizeTerminalText(cfg.Model))
	if label == "" {
		label = strings.TrimSpace(sanitizeTerminalText(cfg.Provider))
	}
	if label == "" {
		label = "model"
	}
	status := "ready"
	if m.isAgentWorking() {
		status = spinnerFrame(m.spinnerFrameIdx) + " " + formatTurnTimer(m.turnStartedAt, now) + " working"
	}

	return headerSnapshot{
		modelLabel:  label,
		statusLabel: status,
		now:         now,
	}
}

// renderHeaderLine 渲染顶部 header 行。数据快照与渲染分离：collectHeaderData
// 只读数据源，renderHeader 只吃快照+宽度按 cell 预算渲染。
func (m appModel) renderHeaderLine(width int) string {
	if width <= 0 {
		return ""
	}
	now := m.cursorFrameAt
	if now.IsZero() {
		now = time.Now()
	}
	return renderHeader(m.collectHeaderData(now), width)
}
