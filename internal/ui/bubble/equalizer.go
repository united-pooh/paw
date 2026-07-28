// 均衡器（equalizer）进度组件 —— fill + wave overlay 统一模型。
//
// idle 与 generating 是同一组件，仅振幅 amp 不同：
//   - amp=0：纯进度填充（idle 形态 ▆▆▆▆▃▁▁▁）。
//   - amp=1：满振幅波浪（generating 形态，fill ± 3 格）。
//   - 0<amp<1：波浪渐起/渐落，过渡连续无跳变。
//
// 字符集 ▁▂▃▄▅▆▇█ 共 8 级高度，每级 1 cell。纯函数：只吃数据+amp+帧+格数。
package bubble

import (
	"math"
	"strings"
)

// equalizerBlocks 是 8 级 block 字符，从低到高。索引 0=▁（最低），7=█（满）。
var equalizerBlocks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

const (
	// equalizerDefaultCells 是均衡器默认格数（兜底）。
	equalizerDefaultCells = 8
	// equalizerMaxCells 是均衡器格数上限，用于 status bar 拉伸。
	equalizerMaxCells = 40
	// equalizerWaveStep 是波浪相位步进。30fps 下 0.025 → 周期约 8.4s，缓慢起伏。
	equalizerWaveStep = 0.025
	// equalizerWaveAmp 是满振幅时波浪偏离填充的半幅格数。3.5 → 峰谷差 7 级，
	// 覆盖 block 字符近一整个高度（0..7 共 8 级），波浪醒目不显矮。
	equalizerWaveAmp = 3.5
)

// renderEqualizer 渲染一排 block 字符（fill + wave overlay）。
//   - usedPct: context 已用百分比 0-100，决定填充基线。
//   - amp: 波浪振幅 0-1。0=纯填充，1=满波浪。
//   - frameIdx: 帧索引（cursorFrameMsg 驱动），用于波浪相位横移。
//   - cells: 格数，>=1。调用方按预算决定，本函数不截断上限。
func renderEqualizer(usedPct, amp float64, frameIdx int, cells int) string {
	if cells < 1 {
		return ""
	}
	amp = clamp01(amp)
	var b strings.Builder
	for i := 0; i < cells; i++ {
		level := equalizerLevel(usedPct, amp, frameIdx, i, cells)
		b.WriteString(equalizerBlocks[level])
	}
	return b.String()
}

// equalizerLevel 返回第 i 格（0-indexed）的高度级别 0..7。
// 公式：level = clamp(round(fill + amp*waveAmp*sin(2π·(i/cells − phase))), 0, 7)
func equalizerLevel(usedPct, amp float64, frameIdx int, i, cells int) int {
	base := equalizerFillLevel(usedPct, i, cells)
	if amp <= 0 {
		return base
	}
	phase := float64(frameIdx) * equalizerWaveStep
	t := 2 * math.Pi * (float64(i)/float64(cells) - phase)
	h := float64(base) + amp*equalizerWaveAmp*math.Sin(t)
	return clampInt(int(math.Round(h)), 0, 7)
}

// equalizerFillLevel 返回第 i 格的纯填充高度（amp=0 形态）0..7。
// 每格代表 100/cells % 的区间；满区间=█(7)，空区间=▁(0)，跨区间按比例。
func equalizerFillLevel(usedPct float64, i, cells int) int {
	segStart := float64(i) * 100 / float64(cells)
	segEnd := float64(i+1) * 100 / float64(cells)
	switch {
	case usedPct >= segEnd:
		return 7
	case usedPct <= segStart:
		return 0
	default:
		frac := (usedPct - segStart) / (100 / float64(cells))
		return clampInt(int(math.Round(frac*7)), 0, 7)
	}
}

// clamp01 定义于 cursor_animation.go，此处复用。
