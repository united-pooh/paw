package actor

import (
	"sort"
	"sync"
	"time"
)

// Clock 抽象时间源：真实模式用 time.Timer，确定性测试用虚拟时钟推进。
type Clock interface {
	Now() time.Time
	// After 返回在 d 之后触发的取消器。
	After(d time.Duration, fn func()) (cancel func())
}

// RealClock 是生产时钟。
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) After(d time.Duration, fn func()) func() {
	t := time.AfterFunc(d, fn)
	return func() { t.Stop() }
}

// VirtualClock 是确定性测试时钟：After 仅登记，Advance 按到期序触发。
// 同一时刻多个定时器按注册顺序触发。
type VirtualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers []*vcTimer
}

type vcTimer struct {
	deadline time.Time
	seq      int
	fn       func()
	canceled bool
}

// NewVirtualClock 起点固定为 Unix 纪元，保证跨运行确定性。
func NewVirtualClock() *VirtualClock {
	return &VirtualClock{now: time.Unix(0, 0)}
}

func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *VirtualClock) After(d time.Duration, fn func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &vcTimer{deadline: c.now.Add(d), seq: c.seq, fn: fn}
	c.timers = append(c.timers, t)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		t.canceled = true
	}
}

// Advance 推进虚拟时间并触发所有到期定时器（到期序，同刻按注册序）。
func (c *VirtualClock) Advance(d time.Duration) {
	target := c.Now().Add(d)
	for {
		c.mu.Lock()
		var next *vcTimer
		for _, t := range c.timers {
			if t.canceled || t.deadline.After(target) {
				continue // 已取消或未到期
			}
			if next == nil || t.deadline.Before(next.deadline) ||
				(t.deadline.Equal(next.deadline) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.compact()
			c.mu.Unlock()
			return
		}
		c.now = next.deadline
		next.canceled = true
		fn := next.fn
		c.mu.Unlock()
		fn() // 回调在锁外执行，回调内可再注册定时器
	}
}

// compact 移除已触发/取消的定时器（调用方持锁）。
func (c *VirtualClock) compact() {
	kept := c.timers[:0]
	for _, t := range c.timers {
		if !t.canceled {
			kept = append(kept, t)
		}
	}
	c.timers = kept
}

// PendingTimers 返回未触发定时器的到期时刻（确定性测试断言用）。
func (c *VirtualClock) PendingTimers() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, 0, len(c.timers))
	for _, t := range c.timers {
		if !t.canceled {
			out = append(out, t.deadline.Sub(c.now))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
