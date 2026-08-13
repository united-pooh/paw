package bubble

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestScheduleUIAnimationFrameDeduplicatesPendingTick(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = false

	first := model.scheduleUIAnimationFrame()
	second := model.scheduleUIAnimationFrame()

	if first == nil {
		t.Fatal("first frame wakeup returned nil")
	}
	if second != nil {
		t.Fatal("second frame wakeup scheduled a duplicate tick")
	}
	if !model.uiAnimationFrameScheduled {
		t.Fatal("frame should remain marked scheduled until cursorFrameMsg arrives")
	}
}

func TestCursorFrameStopsWhenIdle(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = true
	model.waveAmpStartedAt = time.Time{}
	model.tokenRippleExitAt = time.Time{}
	model.transcriptRefreshPending = false

	at := time.Unix(100, 0)
	next, _ := model.Update(cursorFrameMsg(at))
	model = next.(appModel)

	// Update may still return the one-shot pipeline poll command. The scheduler
	// marker is the source of truth for whether a successor animation tick exists.
	if model.uiAnimationFrameScheduled {
		t.Fatal("consumed idle frame should clear the scheduled marker")
	}
}

func TestStartChatTurnWakesAnimationBeforeFirstDelta(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = false

	next, cmd := model.startChatTurn("hello")

	if cmd == nil {
		t.Fatal("model turn should return work and animation commands")
	}
	if !next.queryGuard.IsModelRunning() {
		t.Fatal("model guard should be running")
	}
	if !next.uiAnimationFrameScheduled {
		t.Fatal("model turn should wake animation before the first delta")
	}
}

func TestQueuedTurnWakesAnimationBeforeFirstDelta(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = false
	if _, ok := model.chatQueue.Enqueue("queued"); !ok {
		t.Fatal("failed to enqueue test turn")
	}

	cmd := model.startNextQueuedTurn()

	if cmd == nil {
		t.Fatal("queued turn should return work and animation commands")
	}
	if !model.queryGuard.IsModelRunning() {
		t.Fatal("queued turn should start model guard")
	}
	if !model.uiAnimationFrameScheduled {
		t.Fatal("queued turn should wake animation")
	}
}

func TestWorkingCursorFrameSchedulesSuccessorWithoutModelDelta(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = true
	if !model.queryGuard.StartModel() {
		t.Fatal("failed to start model guard")
	}
	model.syncRunningFlags()

	next, cmd := model.Update(cursorFrameMsg(time.Unix(200, 0)))
	model = next.(appModel)

	if cmd == nil {
		t.Fatal("working frame should schedule a successor without a model delta")
	}
	if !model.uiAnimationFrameScheduled {
		t.Fatal("successor frame should be marked scheduled")
	}
}

func TestSyncSubagentWakesAnimation(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.subagents = &fakeSubagentController{}
	model.uiAnimationFrameScheduled = false

	cmd := model.handleSubagentCommand("/subagent --sync inspect")

	if cmd == nil {
		t.Fatal("sync subagent should return work and animation commands")
	}
	if !model.queryGuard.IsModelRunning() {
		t.Fatal("sync subagent should start model guard")
	}
	if !model.uiAnimationFrameScheduled {
		t.Fatal("sync subagent should wake animation")
	}
}

func TestTurnCompletionKeepsFramesAliveForRippleExit(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = false
	if !model.queryGuard.StartModel() {
		t.Fatal("failed to start model guard")
	}
	model.syncRunningFlags()
	model.cursorFrameAt = time.Unix(0, 0).Add(time.Second)

	next, _ := model.Update(turnFinishedMsg{})
	model = next.(appModel)

	if model.tokenRippleExitAt.IsZero() {
		t.Fatal("turn completion should record a ripple exit deadline")
	}
	if !model.uiAnimationFrameScheduled {
		t.Fatal("turn completion should wake and mark a ripple exit frame")
	}
}

func TestRippleExitFrameStopsAfterDeadline(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = true
	model.waveAmpStartedAt = time.Time{}
	model.width = 80
	model.tokenRippleExitAt = time.Unix(500, 0)

	deadline := model.tokenRippleExitAt.Add(time.Duration(80+tokenRippleTail) * tokenRippleSpeed)

	next, _ := model.Update(cursorFrameMsg(deadline))
	model = next.(appModel)

	// Ignore the unrelated one-shot pipeline poll command; only the scheduler
	// marker identifies an animation successor.
	if model.uiAnimationFrameScheduled {
		t.Fatal("completed ripple exit should clear scheduled marker")
	}
}

func TestTurnOutcomesWakeRippleExit(t *testing.T) {
	cases := []struct {
		name string
		msg  turnFinishedMsg
	}{
		{name: "success", msg: turnFinishedMsg{}},
		{name: "error", msg: turnFinishedMsg{err: errors.New("boom")}},
		{name: "expected cancellation", msg: turnFinishedMsg{err: context.Canceled}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.uiAnimationFrameScheduled = false
			if !model.queryGuard.StartModel() {
				t.Fatal("failed to start model guard")
			}
			model.syncRunningFlags()
			model.cursorFrameAt = time.Unix(0, 0).Add(2 * time.Second)
			if errors.Is(tc.msg.err, context.Canceled) {
				model.modelCancelRequested = true
			}

			next, _ := model.Update(tc.msg)
			model = next.(appModel)

			if !model.uiAnimationFrameScheduled {
				t.Fatal("turn outcome should wake ripple exit animation")
			}
			if model.tokenRippleExitAt.IsZero() {
				t.Fatal("turn outcome should set ripple exit deadline")
			}
		})
	}
}

// TestScheduleClockTickDeduplicatesPendingTick 验证空闲时钟链与动画帧链一样
// 有去重标志：在途 tick 未消费前不会重复调度。
func TestScheduleClockTickDeduplicatesPendingTick(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.clockTickScheduled = false

	first := model.scheduleClockTick()
	second := model.scheduleClockTick()

	if first == nil {
		t.Fatal("first clock tick returned nil")
	}
	if second != nil {
		t.Fatal("second clock tick scheduled a duplicate")
	}
	if !model.clockTickScheduled {
		t.Fatal("clock tick should remain marked scheduled until clockTickMsg arrives")
	}
}

// TestClockTickAdvancesFrameTimeWhileIdle 验证空闲时钟帧推进 cursorFrameAt，
// header 时钟随之刷新，并续排下一帧时钟。
func TestClockTickAdvancesFrameTimeWhileIdle(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Unix(10, 0)
	model.clockTickScheduled = false

	next, cmd := model.Update(clockTickMsg(time.Unix(11, 0)))
	model = next.(appModel)

	if model.cursorFrameAt.Unix() != 11 {
		t.Fatalf("cursorFrameAt = %v, want advanced to t1", model.cursorFrameAt)
	}
	if !model.clockTickScheduled {
		t.Fatal("clock tick should re-arm the idle clock chain")
	}
	if cmd == nil {
		t.Fatal("clock tick should return the successor tick command")
	}
	header := ansi.Strip(model.renderHeaderEmbedded(80))
	if !strings.Contains(header, time.Unix(11, 0).Format("15:04")) {
		t.Fatalf("header = %q, want clock %q after idle tick", header, time.Unix(11, 0).Format("15:04"))
	}
}

// TestClockTickSkipsRepaintDuringRecentInput 验证最近 3s 内有键盘输入时，
// 时钟帧不推进 cursorFrameAt（跳过重绘），但仍续排下一帧稍后重试。
func TestClockTickSkipsRepaintDuringRecentInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Unix(10, 0)
	t1 := time.Unix(11, 0)
	model.lastKeyEventAt = t1.Add(-time.Second)

	next, cmd := model.Update(clockTickMsg(t1))
	model = next.(appModel)

	if !model.cursorFrameAt.Equal(time.Unix(10, 0)) {
		t.Fatalf("cursorFrameAt = %v, want unchanged during recent input", model.cursorFrameAt)
	}
	if !model.clockTickScheduled {
		t.Fatal("skipped clock tick should re-arm for a later retry")
	}
	if cmd == nil {
		t.Fatal("skipped clock tick should still return the successor tick command")
	}
}

// TestClockTickExitsWhileWorking 验证工作态下时钟链退出：不再续排，由
// 30fps 动画帧链接管屏幕。
func TestClockTickExitsWhileWorking(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if !model.queryGuard.StartModel() {
		t.Fatal("failed to start model guard")
	}
	model.syncRunningFlags()
	model.clockTickScheduled = true

	next, cmd := model.Update(clockTickMsg(time.Unix(11, 0)))
	model = next.(appModel)

	if model.clockTickScheduled {
		t.Fatal("working clock tick should clear the scheduled marker")
	}
	if cmd != nil {
		t.Fatalf("working clock tick returned command %v, want nil", cmd)
	}
}

// TestIdleMinuteClockKeepsAdvancing 是步骤 6.1 手工验证的自动化代理：
// 模拟一次完整的空闲交接（最后一次 30fps 动画帧 → 15s 时钟链接手 →
// 一整分钟 4 次时钟帧），断言 cursorFrameAt 持续推进、动画帧链不复活、
// header 时钟跨分钟刷新。
func TestIdleMinuteClockKeepsAdvancing(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.uiAnimationFrameScheduled = true
	model.waveAmpStartedAt = time.Time{}
	model.tokenRippleExitAt = time.Time{}
	model.transcriptRefreshPending = false

	t0 := time.Date(2026, 8, 8, 10, 59, 30, 0, time.Local)
	model.cursorFrameAt = t0

	// 最后一次动画帧到达（空闲分支）：动画帧链停止，时钟链接手。
	next, cmd := model.Update(cursorFrameMsg(t0))
	model = next.(appModel)
	if model.uiAnimationFrameScheduled {
		t.Fatal("idle animation frame should stop the 30fps chain")
	}
	if !model.clockTickScheduled {
		t.Fatal("idle animation frame should arm the idle clock chain")
	}
	if cmd == nil {
		t.Fatal("idle animation frame should return the clock chain command")
	}

	// 一整分钟空闲：4 次 15s 时钟帧（第 2 次跨分钟边界）。
	for i := 1; i <= 4; i++ {
		tickAt := t0.Add(time.Duration(i) * idleClockInterval)
		next, cmd = model.Update(clockTickMsg(tickAt))
		model = next.(appModel)
		if !model.clockTickScheduled {
			t.Fatalf("tick %d: idle clock chain should remain armed", i)
		}
		if cmd == nil {
			t.Fatalf("tick %d: clock tick should re-arm its successor", i)
		}
		if !model.cursorFrameAt.Equal(tickAt) {
			t.Fatalf("tick %d: cursorFrameAt = %v, want %v", i, model.cursorFrameAt, tickAt)
		}
		if model.uiAnimationFrameScheduled {
			t.Fatalf("tick %d: clock tick must not revive the animation frame chain", i)
		}
	}

	oldClock := t0.Format("15:04")
	newClock := model.cursorFrameAt.Format("15:04")
	if newClock == oldClock {
		t.Fatal("test setup: simulated minute did not cross a clock boundary")
	}
	header := ansi.Strip(model.renderHeaderEmbedded(80))
	if !strings.Contains(header, newClock) {
		t.Fatalf("header = %q, want clock %q after one idle minute", header, newClock)
	}
}

// TestKeyEventsRecordLastInputAt 验证键盘输入（非 raw mouse 碎片）会记录
// 最后输入时刻，供时钟链的 IME 安全窗口使用。
func TestKeyEventsRecordLastInputAt(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = next.(appModel)

	if model.lastKeyEventAt.IsZero() {
		t.Fatal("key event should record the last input time")
	}
}
