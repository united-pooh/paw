package actor

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// wheel 管理持久化定时器的武装/取消：触发后落 sys.timer.fired 并向
// 所属 actor 投递消息（MsgID=timerID，进收件账本天然去重）。
// 真实时钟用 time.AfterFunc；虚拟时钟由测试 Advance 驱动。
type wheel struct {
	clock Clock
	sys   *System

	mu      sync.Mutex
	cancels map[string]func()
}

func newWheel(clock Clock, sys *System) *wheel {
	return &wheel{clock: clock, sys: sys, cancels: make(map[string]func())}
}

func timerKey(id ActorID, timerID string) string { return id.String() + "#" + timerID }

// arm 武装一个定时器（重复武装同一 timerID 时先取消旧的）。
func (w *wheel) arm(id ActorID, timerID string, afterMs int64, msg Msg) {
	key := timerKey(id, timerID)
	w.mu.Lock()
	if cancel, ok := w.cancels[key]; ok {
		cancel()
	}
	w.mu.Unlock()

	cancel := w.clock.After(time.Duration(afterMs)*time.Millisecond, func() { w.fire(id, timerID, msg) })

	w.mu.Lock()
	w.cancels[key] = cancel
	w.mu.Unlock()
}

// cancel 取消（钝化/隔离时防泄漏）。
func (w *wheel) cancel(id ActorID, timerID string) {
	key := timerKey(id, timerID)
	w.mu.Lock()
	cancel, ok := w.cancels[key]
	delete(w.cancels, key)
	w.mu.Unlock()
	if ok {
		cancel()
	}
}

func (w *wheel) fire(id ActorID, timerID string, msg Msg) {
	key := timerKey(id, timerID)
	w.mu.Lock()
	cancel, ok := w.cancels[key]
	delete(w.cancels, key)
	w.mu.Unlock()
	if !ok {
		return // 已取消
	}
	_ = cancel
	w.sys.submitByID(id, func() {
		cell := w.sys.router.get(id)
		if cell == nil {
			// 已钝化：重新激活后由恢复扫描重武装（本轮丢弃）。
			return
		}
		cell.mu.Lock()
		ledger := cell.ledger
		cell.mu.Unlock()
		if ledger != nil {
			_ = w.sys.journal.appendSysMsg(context.Background(), id, sysTimerFired, timerFiredPayload{TimerID: timerID})
			ledger.noteFired(timerID)
		}
		if msg.MsgID == "" {
			msg.MsgID = timerID
		}
		_ = w.sys.route(id, msg)
	})
}

func (w *wheel) drop(id ActorID) {
	prefix := id.String() + "#"
	w.mu.Lock()
	for key, cancel := range w.cancels {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			cancel()
			delete(w.cancels, key)
		}
	}
	w.mu.Unlock()
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}
