// 底部 status 行渲染层（方案 B · 细线分隔）。
//
// 布局：[spinner+状态] · [均衡器 百分比 · cache/free] · [模式]
// 生成态前导 braille 旋转 spinner；均衡器 idle=进度填充、generating=波浪起伏。
// 遵循 UI/数据隔离：各段从 appModel accessor 读数据，按 cell 预算截断，整体
// 严格等于 width 个 cell，数据内容绝不破坏布局。
package bubble

import (
	"time"
)

// statusSegmentSeparator 是 B 布局各段之间的点号分隔符（轻盈）。
const statusSegmentSeparator = " · "

// spinnerFrames 是 braille 旋转 spinner 帧序列，生成态前导。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// equalizerAmpDuration 是 idle↔working 振幅过渡时长。
const equalizerAmpDuration = 400 * time.Millisecond

// isAgentWorking 报告 agent 是否处于"工作中"态：流式生成 或 工具调用进行中。
// 区别于 isGenerating（仅流式 delta 期间为 true）：toolCallMsg 会把 isGenerating
// 置 false，但整轮未结束（queryGuard 仍 running），此时应保持 working 态——
// 均衡器继续起伏、状态词显示 working，而非退回 idle。
func (m appModel) isAgentWorking() bool {
	return m.isGenerating || m.isModelWorkRunning()
}

// updateWaveAmp 在 cursorFrameMsg 每帧推进振幅过渡（指针接收者，写持久模型）。
// 目标态由 isAgentWorking 派生（流式 + 工具调用期间均满振幅），翻转时记录起点
// amp 与时刻，用 easeOutCubic 在 400ms 内缓动。结果写入 waveAmpCurrent 供渲染只读。
func (m *appModel) updateWaveAmp(now time.Time) {
	target := m.isAgentWorking()
	if target != m.waveAmpTarget {
		m.waveAmpFrom = m.waveAmpCurrent
		m.waveAmpTarget = target
		m.waveAmpStartedAt = now
	}
	if m.waveAmpStartedAt.IsZero() {
		if target {
			m.waveAmpCurrent = 1
		} else {
			m.waveAmpCurrent = 0
		}
		return
	}
	p := clamp01(float64(now.Sub(m.waveAmpStartedAt)) / float64(equalizerAmpDuration))
	eased := easeOutCubic(p)
	if target {
		m.waveAmpCurrent = m.waveAmpFrom + (1-m.waveAmpFrom)*eased
	} else {
		m.waveAmpCurrent = m.waveAmpFrom * (1 - eased)
	}
}

// spinnerFrame 按 30fps 帧索引降速到 ~10fps 返回当前 spinner 字符。
// idx/3 让每 3 帧切一帧（~100ms/帧，1s/周期），既流畅又不晃眼。
func spinnerFrame(idx int) string {
	if len(spinnerFrames) == 0 {
		return ""
	}
	return spinnerFrames[(idx/3)%len(spinnerFrames)]
}

// renderDockStatusLine 渲染输入框上方的单行状态条。
func (m appModel) renderDockStatusLine(width int) string {
	width = maxInt(1, width)

	left := m.renderStatusLeftSegment()
	mode := m.renderModeIndicator()

	// 模式右对齐，预算固定小。
	modeBudget := clampInt(width/6, 4, 10)
	mode = truncateDisplayWidth(mode, modeBudget)
	modeW := terminalCellWidth(mode)

	// 状态词收紧到实际宽度（⠋ generating ≈12 格），不再预留 width/4 浪费均衡器空间。
	left = truncateDisplayWidth(left, 14)
	leftW := terminalCellWidth(left)

	sepW := terminalCellWidth(statusSegmentSeparator)
	// 中段吃剩余宽度：均衡器拉伸填满，无死空白。amp 由 cursorFrameMsg 预算。
	midBudget := width - leftW - modeW - 2*sepW
	mid := m.renderStatusMidSegment(midBudget, m.waveAmpCurrent)

	// 左 · 中 · 右：均衡器吃满 midBudget，不再有 mid-mode 间空格 gap。
	line := left + statusSegmentSeparator + mid + statusSegmentSeparator + mode
	return padOrTruncateToWidth(line, width)
}

// renderStatusLeftSegment 返回左段：工作中态显示 spinner + 状态词，空闲显示 "● idle"。
// 三态词：流式生成="generating"、工具调用中="working"、空闲="idle"。前两者都带 spinner。
func (m appModel) renderStatusLeftSegment() string {
	if m.isGenerating {
		return generatingStatusStyle.Render(spinnerFrame(m.spinnerFrameIdx) + " generating")
	}
	if m.isModelWorkRunning() {
		return generatingStatusStyle.Render(spinnerFrame(m.spinnerFrameIdx) + " working")
	}
	return idleStatusStyle.Render("● idle")
}

// renderStatusMidSegment 返回中段：均衡器（拉伸填满）+ 百分比 + 辅助（cache%/free%）。
// 预算紧张时按优先级丢弃辅助→百分比→均衡器缩到 min 4 格。返回宽度 <= budget。
// amp 由 cursorFrameMsg 经 updateWaveAmp 缓动后存入 waveAmpCurrent，此处传入。
func (m appModel) renderStatusMidSegment(budget int, amp float64) string {
	if budget <= 0 {
		return ""
	}
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	usedPct := percent(stats.UsedTokens, limit)
	pctLabel := formatContextPercent(stats.UsedTokens, limit)

	// 辅助：工作中态（流式或工具调用）显示 cache%，空闲显示 free%。
	var aux string
	if m.isAgentWorking() {
		aux = "cache " + formatContextPercent(stats.CacheTokens, limit)
	} else {
		aux = "free " + formatContextPercent(maxInt(0, limit-stats.UsedTokens), limit)
	}

	pctPart := equalizerActiveStyle.Render(" " + pctLabel)
	auxPart := equalizerDimStyle.Render(" · " + aux)

	// 均衡器拉伸：格数 = budget − 文本宽度，clamp [4, 40]。
	textW := terminalCellWidth(pctPart) + terminalCellWidth(auxPart)
	eqBudget := budget - textW
	cells := clampInt(eqBudget, 4, equalizerMaxCells)

	eq := equalizerActiveStyle.Render(renderEqualizer(usedPct, amp, m.spinnerFrameIdx, cells))
	full := eq + pctPart + auxPart
	if terminalCellWidth(full) <= budget {
		return full
	}
	// 装不下辅助：均衡器 + 百分比。
	core := eq + pctPart
	if terminalCellWidth(core) <= budget {
		return core
	}
	// 极窄：只显示百分比。
	return truncateDisplayWidth(pctPart, budget)
}

// renderModeIndicator 返回右侧模式标记。优先级：terminal > !bang > multiline > chat。
func (m appModel) renderModeIndicator() string {
	switch {
	case m.isTerminalInputActive() || m.runningTerminal:
		return modeTerminalStyle.Render("terminal")
	case hasBangPrefix(m.input.Value()):
		return modeShellStyle.Render("!shell")
	case m.hasMultilineInput():
		return modeMultilineStyle.Render("multiline")
	default:
		return modeChatStyle.Render("chat")
	}
}

// equalizerActiveStyle 是均衡器+百分比样式（cyan，不加粗避免块字符过重）。
var equalizerActiveStyle = contextUsedStyle.Copy().UnsetBold()

// equalizerDimStyle 是辅助信息（cache%/free%）样式，低饱和。
var equalizerDimStyle = contextFreeStyle.Copy()
