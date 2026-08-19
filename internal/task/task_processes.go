package task

import "sync"

type taskProcessTable struct {
	mu        sync.Mutex
	processes map[string]Process
	active    map[string]struct{}
}

func newTaskProcessTable() *taskProcessTable {
	return &taskProcessTable{processes: make(map[string]Process), active: make(map[string]struct{})}
}

func (t *taskProcessTable) bind(id string, process Process) {
	if t == nil || id == "" || process == nil {
		return
	}
	t.mu.Lock()
	t.processes[id] = process
	t.active[id] = struct{}{}
	t.mu.Unlock()
}

func (t *taskProcessTable) track(id string) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	t.active[id] = struct{}{}
	t.mu.Unlock()
}

func (t *taskProcessTable) take(id string) Process {
	if t == nil || id == "" {
		return nil
	}
	t.mu.Lock()
	process := t.processes[id]
	delete(t.processes, id)
	delete(t.active, id)
	t.mu.Unlock()
	return process
}

func (t *taskProcessTable) contains(id string) bool {
	if t == nil || id == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.active[id]
	return ok
}

func (t *taskProcessTable) activeIDs() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.active))
	for id := range t.active {
		ids = append(ids, id)
	}
	return ids
}

func (t *taskProcessTable) stopAll() []Process {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	processes := make([]Process, 0, len(t.processes))
	for id, process := range t.processes {
		processes = append(processes, process)
		delete(t.processes, id)
	}
	clear(t.active)
	return processes
}

func (t *taskProcessTable) snapshot() []Process {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Process, 0, len(t.processes))
	for _, process := range t.processes {
		out = append(out, process)
	}
	return out
}
