// 顶部 header 渲染层。
//
// 设计遵循 UI/数据隔离原则：
//   - headerSnapshot 是纯数据快照，由 collectHeaderData 从各 provider 收集；
//   - renderHeader 是纯函数，只吃快照+宽度，按 cell 预算截断，不访问任何状态。
//
// 数据内容绝不破坏布局：每个字段有 cell 预算上限，超长用 truncateDisplayWidth
// 截断；终端过窄时按优先级（右→左）丢弃字段，最终输出严格等于 width 个 cell。
package bubble

import (
	"strconv"
	"strings"
	"time"
)

// headerSnapshot 是 header 渲染所需的全部数据。字段已 sanitize，渲染层不再
// 触碰数据源。
type headerSnapshot struct {
	modelLabel    string        // 已 sanitize 的模型名
	turnElapsed   time.Duration // 0 表示 idle
	generating    bool
	sessionTokens int           // 会话累计 token
	now           time.Time     // 渲染时刻（wall clock 来源）
}

// headerFieldSeparator 是字段间的分隔符，占 3 个 cell。
const headerFieldSeparator = " · "

// renderHeader 把快照渲染成严格 width cell 宽的一行。纯函数。
// 两端对齐：左组 [model · timer] 贴左，右组 [session · clock] 贴右，中间弹性空格。
func renderHeader(s headerSnapshot, width int) string {
	if width <= 0 {
		return ""
	}

	// 模型名是唯一的用户可控长字符串，给独立预算并截断。
	modelBudget := clampInt(width/4, 6, 24)
	model := truncateDisplayWidth(s.modelLabel, modelBudget)
	timer := formatTurnTimer(s.turnElapsed, s.generating)
	left := model + headerFieldSeparator + timer

	session := "Σ " + formatCompactTokenCount(s.sessionTokens)
	clock := s.now.Format("15:04")

	// 右组字段按优先级从低到高丢弃（clock 先丢、session 后丢），直到装得下。
	rightFields := []string{clock, session} // 尾递减：先尝试丢 clock
	var right string
	for drop := 0; drop <= len(rightFields); drop++ {
		keep := rightFields[drop:] // drop 个被丢弃
		parts := make([]string, 0, len(keep))
		for _, f := range keep {
			parts = append(parts, f)
		}
		// 反转回 session, clock 顺序拼接。
		if len(parts) == 0 {
			right = ""
		} else {
			rev := make([]string, len(parts))
			for i, p := range parts {
				rev[len(parts)-1-i] = p
			}
			right = strings.Join(rev, headerFieldSeparator)
		}
		leftW := terminalCellWidth(left)
		rightW := terminalCellWidth(right)
		if leftW+rightW == 0 {
			return ""
		}
		if rightW == 0 {
			// 只有左组：左对齐 + 右补空格。
			return padOrTruncateToWidth(left, width)
		}
		gap := width - leftW - rightW
		if gap >= len(headerFieldSeparator) {
			// 两端对齐：左 + gap空格 + 右。
			return left + strings.Repeat(" ", gap) + right
		}
		// 装不下当前右组，继续丢弃。
	}
	return padOrTruncateToWidth(left, width)
}

// formatTurnTimer 把轮次耗时格式化为 header 用字符串。生成中显示 mm:ss，
// 空闲显示 "idle"。
func formatTurnTimer(elapsed time.Duration, generating bool) string {
	if !generating || elapsed <= 0 {
		return "⏱ idle"
	}
	secs := int(elapsed.Seconds())
	return "⏱ " + formatTwoDigitPadded(secs/60) + ":" + formatTwoDigitPadded(secs%60)
}

// formatTwoDigitPadded 返回两位补零的数字字符串（0-99）。
func formatTwoDigitPadded(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 99 {
		n = 99
	}
	if n < 10 {
		return "0" + strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatInt(int64(n), 10)
}

// padOrTruncateToWidth 把 text 对齐到精确 width cell：不足右补空格，超宽截断。
func padOrTruncateToWidth(text string, width int) string {
	w := terminalCellWidth(text)
	if w == width {
		return text
	}
	if w < width {
		return text + strings.Repeat(" ", width-w)
	}
	return truncateDisplayWidth(text, width)
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

	var elapsed time.Duration
	generating := m.isGenerating
	if generating && !m.turnStartedAt.IsZero() {
		elapsed = now.Sub(m.turnStartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
	}

	return headerSnapshot{
		modelLabel:    label,
		turnElapsed:   elapsed,
		generating:    generating,
		sessionTokens: m.contextStats().SessionUsedTokens,
		now:           now,
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
