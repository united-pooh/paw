package loop

import "sync"

// Hook 是 runner 生命周期钩子的注册句柄。实现者按需实现下述能力接口；
// 未实现的能力不参与分发，零开销。
//
// 纪律（spec P2 / 上帝类防线）：
//   - 钩子之间禁止互相调用；需要通信只能经由事件或注入的状态提供者；
//   - 钩子按注册顺序同步执行，顺序即契约；
//   - 钩子与 runner 同宿主共命运：实现者需自行 recover，panic 会中断所在链。
type Hook interface {
	HookName() string
}

// SessionLoadedHooker 会话切换（/resume、LoadSession）完成后回调，
// 用于重绑会话相关工具与状态。
type SessionLoadedHooker interface {
	OnSessionLoaded(sessionID string)
}

// sessionLoadedFunc 让既有 func(sessionID) 回调直接入链（兼容糖）。
type sessionLoadedFunc func(sessionID string)

func (f sessionLoadedFunc) HookName() string                  { return "session-loaded" }
func (f sessionLoadedFunc) OnSessionLoaded(sessionID string) { f(sessionID) }

// hookChain 持有注册顺序稳定的钩子集合，按能力接口分发。
// 这是 P3/P5 actor 运行时的宿主接入点：外部观察者（tracer、todo 状态块、
// 技能注入等）经由 RegisterHook 挂载，runner 内部只感知能力接口。
type hookChain struct {
	mu    sync.RWMutex
	hooks []Hook
}

func (c *hookChain) register(hook Hook) {
	if hook == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = append(c.hooks, hook)
}

func (c *hookChain) snapshot() []Hook {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Hook(nil), c.hooks...)
}

// dispatchSessionLoaded 按注册顺序回调所有会话切换钩子。
func (c *hookChain) dispatchSessionLoaded(sessionID string) {
	for _, hook := range c.snapshot() {
		if observer, ok := hook.(SessionLoadedHooker); ok {
			observer.OnSessionLoaded(sessionID)
		}
	}
}
