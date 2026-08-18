package actor

import (
	"sync"
	"time"
)

// supervisor 实现 §7 监督策略：panic → 指数退避重启（从 journal 重建
// 状态）→ 窗口内 3 次 → 死信隔离（终态，人工介入）。
type supervisor struct {
	mu        sync.Mutex
	window    time.Duration
	maxPanics int
	panics    map[ActorID][]time.Time
	dead      []DeadLetter
}

// DeadLetter 是隔离记录（终态事实，供 UI/审计消费）。
type DeadLetter struct {
	Actor ActorID
	Msg   Msg
	Err   string
	At    time.Time
}

func newSupervisor(window time.Duration, maxPanics int) *supervisor {
	if window <= 0 {
		window = time.Minute
	}
	if maxPanics <= 0 {
		maxPanics = 3
	}
	return &supervisor{window: window, maxPanics: maxPanics, panics: make(map[ActorID][]time.Time)}
}

// onPanic 在 cell 分片线程内被 recover 调用：记录窗口、决策重启/隔离。
func (s *supervisor) onPanic(c *cell, msg Msg, cause error) {
	now := c.system.clock.Now()
	s.mu.Lock()
	recent := s.pruneLocked(c.id, now)
	recent = append(recent, now)
	s.panics[c.id] = recent
	quarantine := len(recent) >= s.maxPanics
	var backoff time.Duration
	if !quarantine {
		backoff = s.backoffLocked(len(recent))
	}
	if quarantine {
		s.dead = append(s.dead, DeadLetter{Actor: c.id, Msg: msg, Err: cause.Error(), At: now})
	}
	s.mu.Unlock()

	c.system.logger("actor: %s panic (%d in window): %v", c.id, len(recent), cause)
	if quarantine {
		c.deadLetter(msg, cause)
		return
	}
	// 退避后从 journal 重建（reactivate 内部走 cell 分片，保 I1）。
	c.system.clock.After(backoff, func() { c.reactivate() })
}

func (s *supervisor) pruneLocked(id ActorID, now time.Time) []time.Time {
	var recent []time.Time
	for _, ts := range s.panics[id] {
		if now.Sub(ts) <= s.window {
			recent = append(recent, ts)
		}
	}
	return recent
}

// backoffLocked 指数退避：100ms × 2^(n-1)，上限 5s。
func (s *supervisor) backoffLocked(n int) time.Duration {
	d := 100 * time.Millisecond << uint(n-1)
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// DeadLetters 返回隔离记录快照。
func (s *supervisor) DeadLetters() []DeadLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadLetter, len(s.dead))
	copy(out, s.dead)
	return out
}
