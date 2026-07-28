package bubble

import (
	"strings"
	"testing"
)

// amp=0 时退化为纯进度填充（idle 形态）。
func TestEqualizerAmpZeroIsPureFill(t *testing.T) {
	got := renderEqualizer(0, 0, 0, 8)
	if got != "▁▁▁▁▁▁▁▁" {
		t.Fatalf("amp=0, 0%% = %q, want all ▁", got)
	}
	got = renderEqualizer(100, 0, 0, 8)
	if got != "████████" {
		t.Fatalf("amp=0, 100%% = %q, want all █", got)
	}
	got = renderEqualizer(50, 0, 0, 8)
	if got != "████▁▁▁▁" {
		t.Fatalf("amp=0, 50%% = %q, want ████▁▁▁▁", got)
	}
}

// amp=0 时与 frameIdx 无关（纯填充，无波浪）。
func TestEqualizerAmpZeroIgnoresFrame(t *testing.T) {
	a := renderEqualizer(50, 0, 0, 8)
	b := renderEqualizer(50, 0, 99, 8)
	if a != b {
		t.Fatalf("amp=0 should ignore frameIdx: f0=%q != f99=%q", a, b)
	}
}

// amp>0 时出现波浪：不同帧产生不同 pattern。
func TestEqualizerAmpPositiveProducesWave(t *testing.T) {
	f0 := renderEqualizer(0, 1, 0, 8)
	f5 := renderEqualizer(0, 1, 5, 8)
	if f0 == f5 {
		t.Fatalf("amp=1 wave static: f0=%q == f5=%q", f0, f5)
	}
	for _, r := range f0 {
		if !strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
			t.Fatalf("non-block rune %q in %q", r, f0)
		}
	}
}

// amp 在 (0,1) 之间：介于纯填充与满波浪之间，且随 amp 增大偏离填充越多。
func TestEqualizerAmpIntermediate(t *testing.T) {
	fill := renderEqualizer(50, 0, 5, 8)
	full := renderEqualizer(50, 1, 5, 8)
	mid := renderEqualizer(50, 0.5, 5, 8)
	// amp=0.5 的波形应与 amp=0 和 amp=1 都不同（介于两者）。
	if mid == fill {
		t.Fatalf("amp=0.5 == amp=0 (fill): %q", mid)
	}
	if mid == full {
		t.Fatalf("amp=0.5 == amp=1 (full wave): %q", mid)
	}
}

// 连续 amp 过渡无跳变：amp 从 0 缓增到 1，相邻 amp 间的 pattern 差异应平滑（无突变）。
func TestEqualizerAmpTransitionSmooth(t *testing.T) {
	prev := renderEqualizer(50, 0, 5, 8)
	for _, amp := range []float64{0.05, 0.1, 0.2, 0.35, 0.5, 0.7, 0.85, 1.0} {
		cur := renderEqualizer(50, amp, 5, 8)
		// 相邻 amp 最多变化少量格（平滑过渡，不是整排翻转）。
		diffs := 0
		for i := range cur {
			if i < len(prev) && cur[i] != prev[i] {
				diffs++
			}
		}
		if diffs > 4 {
			t.Fatalf("amp %.2f→%.2f changed %d cells (too abrupt): %q vs %q", amp-0.05, amp, diffs, prev, cur)
		}
		prev = cur
	}
}

// 格数：返回 rune 数 == cells。
func TestEqualizerCellCount(t *testing.T) {
	got := renderEqualizer(100, 0, 0, 5)
	if len([]rune(got)) != 5 {
		t.Fatalf("cells=5 got %d runes, want 5: %q", len([]rune(got)), got)
	}
	got = renderEqualizer(60, 1, 3, 20)
	if len([]rune(got)) != 20 {
		t.Fatalf("cells=20 got %d runes, want 20", len([]rune(got)))
	}
}

func TestEqualizerZeroCellsEmpty(t *testing.T) {
	if got := renderEqualizer(50, 1, 0, 0); got != "" {
		t.Fatalf("cells=0 should return empty, got %q", got)
	}
}

// 级别始终在 0..7（任意组合）。
func TestEqualizerLevelRange(t *testing.T) {
	for _, pct := range []float64{0, 1, 12.5, 33.3, 50, 87.5, 99, 100} {
		for _, amp := range []float64{0, 0.3, 0.7, 1.0} {
			for f := 0; f < 30; f++ {
				for i := 0; i < 8; i++ {
					lv := equalizerLevel(pct, amp, f, i, 8)
					if lv < 0 || lv > 7 {
						t.Fatalf("level out of range: pct=%v amp=%v f=%d i=%d -> %d", pct, amp, f, i, lv)
					}
				}
			}
		}
	}
}
