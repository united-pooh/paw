package actor

import "sync"

// router 维护 ActorID → cell 的活跃表。钝化即逐出；下次 Tell 重新激活。
// 只做地址映射，不执行任何 actor 逻辑（System 组合件之一）。
type router struct {
	mu    sync.RWMutex
	cells map[ActorID]*cell
}

func newRouter() *router { return &router{cells: make(map[ActorID]*cell)} }

// get 返回活跃 cell（可能 nil）。
func (r *router) get(id ActorID) *cell {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cells[id]
}

// ensure 返回 cell，不存在则惰性创建并登记。
func (r *router) ensure(sys *System, id ActorID) *cell {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cells[id]; ok {
		return c
	}
	c := newCell(sys, id)
	r.cells[id] = c
	return c
}

// evict 钝化逐出（仅当表内仍是同一实例，防竞态误删新建 cell）。
func (r *router) evict(id ActorID, c *cell) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cells[id] == c {
		delete(r.cells, id)
	}
}

func (r *router) snapshot() []*cell {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*cell, 0, len(r.cells))
	for _, c := range r.cells {
		out = append(out, c)
	}
	return out
}
