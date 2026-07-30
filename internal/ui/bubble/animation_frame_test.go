package bubble

import (
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
