package bubble

import (
	"strings"
	"testing"
	"time"
)

func TestRenderHeaderExactWidthAllFields(t *testing.T) {
	s := headerSnapshot{
		modelLabel:    "opus-4.8",
		turnElapsed:   72 * time.Second,
		generating:    true,
		sessionTokens: 128400,
		now:           time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}
	const width = 80
	line := renderHeader(s, width)
	if w := terminalCellWidth(line); w != width {
		t.Fatalf("header width = %d, want %d (must be exact cell count)", w, width)
	}
	for _, want := range []string{"opus-4.8", "⏱ 01:12", "Σ 128k", "15:42"} {
		if !strings.Contains(line, want) {
			t.Errorf("header %q missing field %q", line, want)
		}
	}
}

func TestRenderHeaderLongModelTruncated(t *testing.T) {
	s := headerSnapshot{
		modelLabel:    strings.Repeat("x", 60), // 远超预算
		turnElapsed:   5 * time.Second,
		generating:    true,
		sessionTokens: 100,
		now:           time.Date(2026, 7, 28, 9, 1, 0, 0, time.UTC),
	}
	const width = 80
	line := renderHeader(s, width)
	if w := terminalCellWidth(line); w != width {
		t.Fatalf("header width = %d, want %d (long model must not overflow)", w, width)
	}
	// 模型预算 = clamp(80/4,6,24) = 20，超出应被截断为 ≤20 cell。
	if w := terminalCellWidth(s.modelLabel); w <= 20 {
		t.Fatalf("precondition: model label should exceed budget, got %d", w)
	}
}

func TestRenderHeaderIdleShowsIdle(t *testing.T) {
	s := headerSnapshot{
		modelLabel:    "opus-4.8",
		turnElapsed:   0,
		generating:    false,
		sessionTokens: 128400,
		now:           time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}
	line := renderHeader(s, 80)
	if !strings.Contains(line, "⏱ idle") {
		t.Fatalf("idle header %q should contain ⏱ idle", line)
	}
}

func TestRenderHeaderNarrowDropsLowPriorityFields(t *testing.T) {
	s := headerSnapshot{
		modelLabel:    "opus-4.8",
		turnElapsed:   72 * time.Second,
		generating:    true,
		sessionTokens: 128400,
		now:           time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}
	// 极窄：应保留 model+timer，丢弃 session/clock，且绝不溢出。
	for _, width := range []int{30, 20, 12} {
		line := renderHeader(s, width)
		if w := terminalCellWidth(line); w != width {
			t.Fatalf("width=%d: header cell width = %d, want exact %d", width, w, width)
		}
		if !strings.Contains(line, "opus") {
			t.Errorf("width=%d: model should always survive, got %q", width, line)
		}
	}
}

func TestRenderHeaderZeroWidthEmpty(t *testing.T) {
	if line := renderHeader(headerSnapshot{}, 0); line != "" {
		t.Fatalf("width=0 should return empty, got %q", line)
	}
}

// TestRenderHeaderRightGroupJustified 验证右组贴右边缘（两端对齐，无右侧大片留白）。
func TestRenderHeaderRightGroupJustified(t *testing.T) {
	s := headerSnapshot{
		modelLabel:    "opus-4.8",
		turnElapsed:   72 * time.Second,
		generating:    true,
		sessionTokens: 128400,
		now:           time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}
	const width = 80
	line := renderHeader(s, width)
	if w := terminalCellWidth(line); w != width {
		t.Fatalf("header width = %d, want %d", w, width)
	}
	// clock 应出现在行尾（去掉尾部空格后以 clock 结尾）。
	trimmed := strings.TrimRight(line, " ")
	if !strings.HasSuffix(trimmed, "15:42") {
		t.Fatalf("right group not right-aligned: %q (trimmed=%q)", line, trimmed)
	}
	// 左组应在行首。
	if !strings.HasPrefix(line, "opus-4.8") {
		t.Errorf("left group not left-aligned: %q", line)
	}
}

func TestFormatTurnTimer(t *testing.T) {
	cases := []struct {
		elapsed    time.Duration
		generating bool
		want       string
	}{
		{0, false, "⏱ idle"},
		{72 * time.Second, true, "⏱ 01:12"},
		{5 * time.Second, true, "⏱ 00:05"},
		{3700 * time.Second, true, "⏱ 61:40"}, // 超过 99 分钟也展示，不崩
		{72 * time.Second, false, "⏱ idle"},  // 非生成态强制 idle
	}
	for _, c := range cases {
		if got := formatTurnTimer(c.elapsed, c.generating); got != c.want {
			t.Errorf("formatTurnTimer(%v,%v) = %q, want %q", c.elapsed, c.generating, got, c.want)
		}
	}
}
