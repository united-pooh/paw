package loop

import (
	"sync"

	"paw/internal/model"
)

// usageMeter 收敛 token 用量账本：context 是当前上下文窗口用量（最近一次
// 请求返回或历史估算），session 是整个会话的累计用量。两者语义不同，
// ContextStats 分别读取。自带锁，避免与 runner.mu 纠缠。
type usageMeter struct {
	mu           sync.RWMutex
	context      model.Usage
	contextKnown bool
	session      model.Usage
	sessionKnown bool
}

// setContextKnown 覆盖当前上下文用量与可信标记（估算路径可能 known=false）。
func (m *usageMeter) setContextKnown(usage model.Usage, known bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.context = usage
	m.contextKnown = known
}

// setCurrent 记录一次确定的当前上下文用量。
func (m *usageMeter) setCurrent(usage model.Usage) {
	m.setContextKnown(usage, true)
}

func (m *usageMeter) contextUsage() (model.Usage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.context, m.contextKnown
}

// addSession 累加一轮的用量增量到会话总量。
func (m *usageMeter) addSession(delta tokenUsageTotals) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := usageTotalsFromUsage(m.session, m.sessionKnown)
	current = current.add(delta)
	m.session = usageFromTotals(current)
	m.sessionKnown = true
}

func (m *usageMeter) sessionUsage() (model.Usage, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.session, m.sessionKnown
}

// setSessionKnown 直接覆盖会话累计（测试/恢复路径使用）。
func (m *usageMeter) setSessionKnown(usage model.Usage, known bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = usage
	m.sessionKnown = known
}

// resetSession 清零会话累计（切换会话时）。
func (m *usageMeter) resetSession() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = model.Usage{}
	m.sessionKnown = false
}

// resetAll 清零全部用量（重置历史时）。
func (m *usageMeter) resetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.context = model.Usage{}
	m.contextKnown = false
	m.session = model.Usage{}
	m.sessionKnown = false
}
