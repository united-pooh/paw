package actor

import (
	"sync"
)

// defaultMailboxCap 是单个 actor 邮箱上限（spec §10 常驻内存预算）。
const defaultMailboxCap = 256

// mailbox 是有界 FIFO。容量满时 Push 返回 ErrMailboxFull，由调用方
// （Tell/Outbox 补发）重试——at-least-once 语义不受损。
type mailbox struct {
	mu     sync.Mutex
	items  []delivery
	cap    int
	closed bool
}

// delivery 是一次投递单元：来自路由的消息或内部控制任务。
type delivery struct {
	msg     Msg
	control func() // 非 nil 时为控制任务，优先级同消息（FIFO）
}

func newMailbox(capacity int) *mailbox {
	if capacity <= 0 {
		capacity = defaultMailboxCap
	}
	return &mailbox{cap: capacity}
}

func (m *mailbox) push(item delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStopped
	}
	if len(m.items) >= m.cap {
		return ErrMailboxFull
	}
	m.items = append(m.items, item)
	return nil
}

func (m *mailbox) pop() (delivery, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return delivery{}, false
	}
	item := m.items[0]
	m.items = m.items[1:]
	return item, true
}

func (m *mailbox) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

func (m *mailbox) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}
