package bubble

import (
	"strings"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"github.com/charmbracelet/x/ansi"
)

// TestStatusLineExactWidthAcrossWidths 验证 B 布局 status 行在任意宽度下严格等于
// width 个 cell——数据内容绝不破坏布局。
func TestStatusLineExactWidthAcrossWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()
	for _, width := range []int{8, 12, 20, 32, 60, 80, 100} {
		line := model.renderDockStatusLine(width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d status cell width=%d line=%q", width, got, ansi.Strip(line))
		}
	}
}

// TestStatusLineModeIndicator 验证模式标记随输入状态切换。
func TestStatusLineModeIndicator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Now()

	if dock := ansi.Strip(model.renderDockStatusLine(80)); !strings.Contains(dock, "chat") {
		t.Fatalf("default mode = %q, want chat", dock)
	}

	model.input.SetValue("!ls")
	if dock := ansi.Strip(model.renderDockStatusLine(80)); !strings.Contains(dock, "!shell") {
		t.Fatalf("bang mode = %q, want !shell", dock)
	}

	model.input.SetValue("line1\nline2")
	if dock := ansi.Strip(model.renderDockStatusLine(80)); !strings.Contains(dock, "multiline") {
		t.Fatalf("multiline mode = %q, want multiline", dock)
	}
}

// TestStatusLineSpinnerWhenGenerating 验证生成态出现 spinner，idle 不出现。
func TestStatusLineSpinnerWhenGenerating(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()

	idle := ansi.Strip(model.renderDockStatusLine(80))
	if strings.ContainsAny(idle, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		t.Fatalf("idle dock should not show spinner: %q", idle)
	}
	if !strings.Contains(idle, "idle") {
		t.Fatalf("idle dock = %q, want 'idle'", idle)
	}

	model.isGenerating = true
	model.turnStartedAt = time.Now().Add(-12 * time.Second)
	gen := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.ContainsAny(gen, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		t.Fatalf("generating dock should show spinner: %q", gen)
	}
	if !strings.Contains(gen, "generating") {
		t.Fatalf("generating dock = %q, want 'generating'", gen)
	}
}

// TestStatusLineEqualizerDualState 验证均衡器双态：idle 显示 free%，generating 显示 cache%。
func TestStatusLineEqualizerDualState(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, CacheTokens: 5000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()

	idle := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.Contains(idle, "free") {
		t.Fatalf("idle dock should show free%%: %q", idle)
	}

	model.isGenerating = true
	model.turnStartedAt = time.Now()
	gen := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.Contains(gen, "cache") {
		t.Fatalf("generating dock should show cache%%: %q", gen)
	}
}

// TestStatusLineEqualizerAnimatesAcrossFrames 验证生成态均衡器随帧变化（波浪横移）。
func TestStatusLineEqualizerAnimatesAcrossFrames(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()
	model.isGenerating = true
	model.turnStartedAt = time.Now()

	a := ansi.Strip(model.renderDockStatusLine(80))
	model.spinnerFrameIdx += 9 // 跳 ~3 个 spinner 帧周期，波浪应已偏移
	b := ansi.Strip(model.renderDockStatusLine(80))
	if a == b {
		t.Fatalf("equalizer should animate across frames: frame0=%q == frame9=%q", a, b)
	}
}

// TestWaveAmpTransitionSmooth 验证 idle→generating 振幅 400ms 缓动连续无跳变。
func TestWaveAmpTransitionSmooth(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	base := time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)

	// idle 稳态：amp=0
	model.cursorFrameAt = base
	model.updateWaveAmp(base)
	if model.waveAmpCurrent != 0 {
		t.Fatalf("idle amp = %v, want 0", model.waveAmpCurrent)
	}

	// 翻转到 generating：amp 从 0 缓升到 1，400ms 完成。
	model.isGenerating = true
	model.turnStartedAt = base
	prev := model.waveAmpCurrent
	for ms := 0; ms <= 500; ms += 50 {
		now := base.Add(time.Duration(ms) * time.Millisecond)
		model.cursorFrameAt = now
		model.updateWaveAmp(now)
		cur := model.waveAmpCurrent
		// amp 单调非降（进入生成）。
		if cur < prev-1e-9 {
			t.Fatalf("ms=%d amp decreased: %v < %v (should rise)", ms, cur, prev)
		}
		// 任意点不超 1。
		if cur > 1+1e-9 {
			t.Fatalf("ms=%d amp=%v > 1", ms, cur)
		}
		prev = cur
	}
	if model.waveAmpCurrent < 1-1e-9 {
		t.Fatalf("after 500ms amp=%v, want ~1 (settled)", model.waveAmpCurrent)
	}
}

// TestWaveAmpExitTransition 验证 generating→idle 振幅从 1 缓降到 0。
func TestWaveAmpExitTransition(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	base := time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)
	// 先让 generating 稳态到 amp=1：翻转后推进 500ms。
	model.isGenerating = true
	model.turnStartedAt = base
	model.cursorFrameAt = base
	model.updateWaveAmp(base)
	for ms := 50; ms <= 500; ms += 50 {
		now := base.Add(time.Duration(ms) * time.Millisecond)
		model.cursorFrameAt = now
		model.updateWaveAmp(now)
	}
	if model.waveAmpCurrent < 1-1e-9 {
		t.Fatalf("precondition: settled amp=%v, want 1", model.waveAmpCurrent)
	}

	// 退出生成：amp 从 1 降到 0。
	exitBase := model.cursorFrameAt
	model.isGenerating = false
	prev := model.waveAmpCurrent
	for ms := 0; ms <= 500; ms += 50 {
		now := exitBase.Add(time.Duration(ms) * time.Millisecond)
		model.cursorFrameAt = now
		model.updateWaveAmp(now)
		cur := model.waveAmpCurrent
		if cur > prev+1e-9 {
			t.Fatalf("ms=%d amp increased: %v > %v (should fall)", ms, cur, prev)
		}
		prev = cur
	}
	if model.waveAmpCurrent > 1e-9 {
		t.Fatalf("after 500ms exit amp=%v, want ~0 (settled)", model.waveAmpCurrent)
	}
}

// TestEqualizerStretchesWide 验证宽屏下均衡器格数 > 8（拉伸填满中段）。
func TestEqualizerStretchesWide(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()
	model.isGenerating = true
	model.turnStartedAt = time.Now()
	model.updateWaveAmp(model.cursorFrameAt)

	dock := ansi.Strip(model.renderDockStatusLine(120))
	// 统计连续 block 字符段长度。
	maxRun := 0
	cur := 0
	for _, r := range dock {
		if strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
			cur++
			if cur > maxRun {
				maxRun = cur
			}
		} else {
			cur = 0
		}
	}
	if maxRun <= 8 {
		t.Fatalf("equalizer not stretched: max block run=%d (want >8) in %q", maxRun, dock)
	}
}

// TestStatusLineWorkingDuringToolCall 验证工具调用期间（isGenerating=false 但
// 整轮未结束）状态显示 "working" 且均衡器继续起伏，不退回 idle。
func TestStatusLineWorkingDuringToolCall(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()

	// 模拟工具调用中：流式已停（isGenerating=false），但整轮仍 running。
	model.isGenerating = false
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	if !model.isModelWorkRunning() {
		t.Fatal("precondition: isModelWorkRunning should be true during tool call")
	}

	// 推进 amp 到稳态。
	for ms := 0; ms <= 500; ms += 50 {
		now := model.cursorFrameAt.Add(time.Duration(ms) * time.Millisecond)
		model.cursorFrameAt = now
		model.updateWaveAmp(now)
	}
	if model.waveAmpCurrent < 1-1e-9 {
		t.Fatalf("tool-call amp=%v, want ~1 (equalizer should keep moving)", model.waveAmpCurrent)
	}

	dock := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.Contains(dock, "working") {
		t.Fatalf("tool-call dock should show 'working': %q", dock)
	}
	if strings.Contains(dock, "idle") {
		t.Fatalf("tool-call dock should not show 'idle': %q", dock)
	}

	// 均衡器跨帧仍变化（波浪未停）。
	a := ansi.Strip(model.renderDockStatusLine(80))
	model.spinnerFrameIdx += 12
	b := ansi.Strip(model.renderDockStatusLine(80))
	if a == b {
		t.Fatalf("equalizer static during tool call: %q == %q", a, b)
	}
}

// TestStatusLineGeneratingLabel 验证流式期间显示 "generating"（非 working）。
func TestStatusLineGeneratingLabel(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Now()
	model.isGenerating = true
	model.turnStartedAt = time.Now()
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	model.updateWaveAmp(model.cursorFrameAt)

	dock := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.Contains(dock, "generating") {
		t.Fatalf("streaming dock should show 'generating': %q", dock)
	}
}
