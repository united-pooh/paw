package bubble

import (
	"context"
	"errors"
	"testing"
	"time"
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
	model.tokenRippleHideAt = time.Time{}
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
	if !model.chatQueue.Enqueue("queued") {
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

	if model.tokenRippleHideAt.IsZero() {
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
	model.tokenRippleHideAt = time.Unix(500, 0)

	next, _ := model.Update(cursorFrameMsg(model.tokenRippleHideAt))
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
			if model.tokenRippleHideAt.IsZero() {
				t.Fatal("turn outcome should set ripple exit deadline")
			}
		})
	}
}
