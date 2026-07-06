# Paw × go-code WebSocket 对齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 填补 go-code 后端与 Paw 前端之间的四处 WebSocket 接口差距：target_agent_id 路由、subagent persona 推送、usage_update 广播、历史消息回放。

**Architecture:** 新建 `AgentRegistry`（`internal/wsserver/agent_registry.go`）持有 40 个 persona 槽和可选的活跃 Runner（通过注入的 `RunnerFactory` 按需创建）；Handler 按 `target_agent_id` 分流到 Registry；Manager 通过 duck-type 接口在 task 启动/结束时通知 WSUI；Server 在新连接建立时单播 snapshot + 历史。

**Tech Stack:** Go 1.22+、gorilla/websocket、crypto/sha256（标准库）、Swift 5.9、SwiftUI

## Global Constraints

- go-code 根目录：`/Users/united_pooh/PyProjects/go-code`
- Paw 根目录：`/Users/united_pooh/PyProjects/Paw`
- go-code module 名：`codex-agent-go`
- 所有 JSON 字段名用 snake_case
- 不引入新的外部 Go 依赖
- 测试命令在 go-code 根目录下执行

---

## File Map

| 文件 | 操作 | 职责 |
|------|------|------|
| `internal/loop/session_event.go` | 改 | 添加 TargetAgentID、AgentInfo、subagents_snapshot 类型 |
| `internal/loop/runner.go` | 改 | 添加 `EventStore()` 公开 accessor |
| `internal/subagent/persona.go` | 改 | 导出 `PersonaDefinition` + `Personas()` |
| `internal/subagent/manager.go` | 改 | duck-type lifecycle hooks，两处调用点 |
| `internal/wsserver/agent_registry.go` | 新建 | AgentRegistry、RunnerFactory、排序快照 |
| `internal/wsserver/handler.go` | 改 | 按 target_agent_id 路由 |
| `internal/wsserver/server.go` | 改 | ServerDeps、BuildMux、pushSnapshot、pushHistory |
| `internal/wsserver/wsui.go` | 改 | SetRegistry、OnTaskStarted/Finished、OnModelUsage |
| `cmd/agent/main.go` | 改 | 串联所有新组件 |
| `Paw/Paw/Networking/SessionEventDecoder.swift` | 改 | AgentInfoPayload、SubagentsSnapshotPayload、新 kind |
| `Paw/Paw/ViewModels/AppViewModel.swift` | 改 | 处理 subagents_snapshot 事件 |

---

### Task 1: session_event.go — 数据模型扩展

**Files:**
- Modify: `internal/loop/session_event.go`
- Create: `internal/loop/session_event_alignment_test.go`

**Interfaces:**
- Produces:
  - `SessionUserInputPayload.TargetAgentID string` (JSON: `target_agent_id,omitempty`)
  - `EventKindSubagentsSnapshot SessionEventKind = "subagents_snapshot"`
  - `AgentInfo struct{ID, Name, Color, Status string; StartedAt *time.Time}`
  - `SessionSubagentsSnapshotPayload struct{Agents []AgentInfo}`
  - `SessionEvent.SubagentsSnapshot *SessionSubagentsSnapshotPayload`

- [ ] **Step 1: 写失败的测试**

新建 `internal/loop/session_event_alignment_test.go`：

```go
package loop_test

import (
	"encoding/json"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
)

func TestSessionUserInputPayload_TargetAgentID_roundtrip(t *testing.T) {
	p := loop.SessionUserInputPayload{Text: "hello", TargetAgentID: "abc-uuid"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got loop.SessionUserInputPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.TargetAgentID != "abc-uuid" {
		t.Errorf("TargetAgentID: got %q want %q", got.TargetAgentID, "abc-uuid")
	}
}

func TestSessionUserInputPayload_TargetAgentID_omitempty(t *testing.T) {
	p := loop.SessionUserInputPayload{Text: "hello"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["target_agent_id"]; ok {
		t.Error("target_agent_id must be omitted when empty")
	}
}

func TestEventKindSubagentsSnapshot_roundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ev := loop.SessionEvent{
		Kind: loop.EventKindSubagentsSnapshot,
		SubagentsSnapshot: &loop.SessionSubagentsSnapshotPayload{
			Agents: []loop.AgentInfo{
				{ID: "id1", Name: "Alice", Color: "#FF0000", Status: "idle"},
				{ID: "id2", Name: "Bob", Color: "#00FF00", Status: "running", StartedAt: &now},
			},
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got loop.SessionEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != loop.EventKindSubagentsSnapshot {
		t.Errorf("kind: got %q want %q", got.Kind, loop.EventKindSubagentsSnapshot)
	}
	if got.SubagentsSnapshot == nil {
		t.Fatal("SubagentsSnapshot is nil")
	}
	if len(got.SubagentsSnapshot.Agents) != 2 {
		t.Fatalf("agents len: got %d want 2", len(got.SubagentsSnapshot.Agents))
	}
	if got.SubagentsSnapshot.Agents[1].StartedAt == nil {
		t.Error("StartedAt should survive round-trip")
	}
}

func TestAgentInfo_StartedAt_omitempty(t *testing.T) {
	ai := loop.AgentInfo{ID: "x", Name: "X", Color: "#000", Status: "idle"}
	data, _ := json.Marshal(ai)
	var m map[string]interface{}
	_ = json.Unmarshal(data, &m)
	if _, ok := m["started_at"]; ok {
		t.Error("started_at must be omitted when nil")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/loop/... -run "TestSessionUserInputPayload_TargetAgentID|TestEventKindSubagentsSnapshot|TestAgentInfo" -v
```

Expected: FAIL — `TargetAgentID`、`EventKindSubagentsSnapshot`、`AgentInfo` 未定义

- [ ] **Step 3: 修改 session_event.go**

**3a.** 在 `SessionUserInputPayload` 加 `TargetAgentID` 字段（当前在 `session_event.go` 约 78 行）：

```go
type SessionUserInputPayload struct {
	Text          string `json:"text"`
	TargetAgentID string `json:"target_agent_id,omitempty"` // 新增
}
```

**3b.** 在 const 块（与其他 `EventKind...` 常量对齐）加：

```go
// EventKindSubagentsSnapshot pushes a full snapshot of all persona slots to the client.
EventKindSubagentsSnapshot SessionEventKind = "subagents_snapshot"
```

**3c.** 在文件末尾加新类型：

```go
// AgentInfo is the state of a single persona slot in a subagents_snapshot event.
type AgentInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Color     string     `json:"color"`
	Status    string     `json:"status"` // "idle" | "pending" | "running" | "done"
	StartedAt *time.Time `json:"started_at,omitempty"`
}

// SessionSubagentsSnapshotPayload is the payload for EventKindSubagentsSnapshot.
type SessionSubagentsSnapshotPayload struct {
	Agents []AgentInfo `json:"agents"`
}
```

**3d.** 在 `SessionEvent` struct 里与其他 payload 字段对齐，加：

```go
SubagentsSnapshot *SessionSubagentsSnapshotPayload `json:"subagents_snapshot,omitempty"`
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/loop/... -run "TestSessionUserInputPayload_TargetAgentID|TestEventKindSubagentsSnapshot|TestAgentInfo" -v
```

Expected: PASS

- [ ] **Step 5: 确认原有 loop 测试仍通过**

```bash
go test ./internal/loop/... -v 2>&1 | tail -5
```

Expected: all PASS（无 FAIL）

- [ ] **Step 6: Commit**

```bash
git add internal/loop/session_event.go internal/loop/session_event_alignment_test.go
git commit -m "feat(loop): add TargetAgentID and subagents_snapshot event types"
```

---

### Task 2: runner.go — EventStore() accessor

**Files:**
- Modify: `internal/loop/runner.go`（在 `SetEventStore` 方法约第 830 行之后追加）
- Modify: `internal/loop/runner_test.go`（追加两个测试）

**Interfaces:**
- Consumes: `runner.eventStore SessionEventStore`（私有字段，已存在）
- Produces: `(*Runner).EventStore() SessionEventStore`

- [ ] **Step 1: 写失败的测试**

在 `internal/loop/runner_test.go` 末尾追加：

```go
func TestRunner_EventStore_returns_set_store(t *testing.T) {
	store := NewInMemorySessionEventStore()
	runner := &Runner{}
	runner.SetEventStore(store)
	got := runner.EventStore()
	if got == nil {
		t.Fatal("EventStore() returned nil")
	}
	if got != store {
		t.Error("EventStore() should return the exact store set via SetEventStore()")
	}
}

func TestRunner_EventStore_nil_runner(t *testing.T) {
	var runner *Runner
	if got := runner.EventStore(); got != nil {
		t.Error("nil receiver EventStore() should return nil")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/loop/... -run "TestRunner_EventStore" -v
```

Expected: FAIL — `runner.EventStore undefined`

- [ ] **Step 3: 在 runner.go 里加 accessor**

在 `SetEventStore` 方法（第 830 行）之后添加：

```go
// EventStore returns the SessionEventStore configured via SetEventStore.
// Returns nil if the runner is nil or no store has been set.
func (runner *Runner) EventStore() SessionEventStore {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.eventStore
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/loop/... -run "TestRunner_EventStore" -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/loop/runner.go internal/loop/runner_test.go
git commit -m "feat(loop): add Runner.EventStore() public accessor"
```

---

### Task 3: subagent 包 — Personas() 导出 + lifecycle hooks

**Files:**
- Modify: `internal/subagent/persona.go`
- Modify: `internal/subagent/manager.go`（两处 hook 调用 + 两个新私有方法）
- Modify: `internal/subagent/manager_test.go`（追加测试，使用 `package subagent` 白盒访问）

**Interfaces:**
- Produces:
  - `subagent.PersonaDefinition struct{Name, Color string}`
  - `subagent.Personas() []PersonaDefinition`
  - `manager.notifyTaskStarted(task TaskSnapshot)` — 私有，duck-type dispatch
  - `manager.notifyTaskLifecycleFinished(task TaskSnapshot)` — 私有，duck-type dispatch
  - Duck-type interface（`wsserver` 包的 WSUI 实现此 shape）：
    ```go
    interface { OnTaskStarted(TaskSnapshot); OnTaskFinished(TaskSnapshot) }
    ```

- [ ] **Step 1: 写失败的测试**

在 `internal/subagent/manager_test.go` 末尾追加（注意：该文件应已使用 `package subagent`，可直接访问私有字段）：

```go
// lifecycleCapture implements Notifier and the duck-type lifecycle shape.
type lifecycleCapture struct {
	started  []TaskSnapshot
	finished []TaskSnapshot
}

func (c *lifecycleCapture) OnSystemMessage(_ ui.SystemEvent) error { return nil }
func (c *lifecycleCapture) OnTaskStarted(t TaskSnapshot)           { c.started = append(c.started, t) }
func (c *lifecycleCapture) OnTaskFinished(t TaskSnapshot)          { c.finished = append(c.finished, t) }

func TestManager_notifyTaskStarted_calls_hook(t *testing.T) {
	cap := &lifecycleCapture{}
	m := &Manager{notifier: cap}
	task := TaskSnapshot{ID: "t1", Name: "TestAgent"}
	m.notifyTaskStarted(task)
	if len(cap.started) != 1 {
		t.Errorf("OnTaskStarted called %d times, want 1", len(cap.started))
	}
	if cap.started[0].ID != "t1" {
		t.Errorf("task ID: got %q want t1", cap.started[0].ID)
	}
}

func TestManager_notifyTaskLifecycleFinished_calls_hook(t *testing.T) {
	cap := &lifecycleCapture{}
	m := &Manager{notifier: cap}
	task := TaskSnapshot{ID: "t2", Name: "TestAgent"}
	m.notifyTaskLifecycleFinished(task)
	if len(cap.finished) != 1 {
		t.Errorf("OnTaskFinished called %d times, want 1", len(cap.finished))
	}
}

func TestManager_notifyTaskStarted_nil_notifier(t *testing.T) {
	m := &Manager{notifier: nil}
	// Must not panic
	m.notifyTaskStarted(TaskSnapshot{ID: "t3"})
}

func TestPersonas_count_and_fields(t *testing.T) {
	ps := Personas()
	if len(ps) != 40 {
		t.Errorf("Personas() returned %d, want 40", len(ps))
	}
	seen := make(map[string]bool)
	for _, p := range ps {
		if p.Name == "" {
			t.Error("empty persona Name")
		}
		if p.Color == "" {
			t.Error("empty persona Color")
		}
		if seen[p.Name] {
			t.Errorf("duplicate persona name: %q", p.Name)
		}
		seen[p.Name] = true
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/subagent/... -run "TestManager_notify|TestPersonas_count" -v
```

Expected: FAIL — `notifyTaskStarted`、`notifyTaskLifecycleFinished`、`Personas` 未定义

- [ ] **Step 3: 在 persona.go 添加导出类型和函数**

在 `internal/subagent/persona.go` 文件末尾追加：

```go
// PersonaDefinition is the exported name/color pair for a persona.
type PersonaDefinition struct {
	Name  string
	Color string
}

// Personas returns all 40 default persona definitions in their original order.
func Personas() []PersonaDefinition {
	result := make([]PersonaDefinition, len(defaultPersonas))
	for i, p := range defaultPersonas {
		result[i] = PersonaDefinition{Name: p.Name, Color: p.Color}
	}
	return result
}
```

- [ ] **Step 4: 在 manager.go 添加 duck-type 接口和 helper 方法**

**4a.** 在 `manager.go` 文件里 `Notifier` 接口定义之后添加 package-private 接口：

```go
// taskLifecycleNotifier is an optional extension checked via type assertion.
// wsserver.WSUI implements this to receive persona lifecycle callbacks.
type taskLifecycleNotifier interface {
	OnTaskStarted(task TaskSnapshot)
	OnTaskFinished(task TaskSnapshot)
}
```

**4b.** 在 `manager.go` 末尾（或 `notifyTaskFinished` 附近）添加两个 helper 方法：

```go
// notifyTaskStarted calls OnTaskStarted on the notifier if it supports it.
func (m *Manager) notifyTaskStarted(task TaskSnapshot) {
	if m.notifier == nil {
		return
	}
	if n, ok := m.notifier.(taskLifecycleNotifier); ok {
		n.OnTaskStarted(task)
	}
}

// notifyTaskLifecycleFinished calls OnTaskFinished on the notifier if it supports it.
func (m *Manager) notifyTaskLifecycleFinished(task TaskSnapshot) {
	if m.notifier == nil {
		return
	}
	if n, ok := m.notifier.(taskLifecycleNotifier); ok {
		n.OnTaskFinished(task)
	}
}
```

**4c.** 在 `startTask` 方法末尾，在 `m.recordTaskStarted(task)` 之后、`return task, process, nil` 之前（约第 539 行），插入：

```go
	m.recordTaskStarted(task)
	m.notifyTaskStarted(task) // 新增
	return task, process, nil
```

**4d.** 在 `finishTask` 方法末尾，在 `m.recordTaskFinished(task)` 之后、`return task` 之前（约第 667 行），插入：

```go
	m.recordTaskFinished(task)
	m.notifyTaskLifecycleFinished(task) // 新增
	return task
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/subagent/... -run "TestManager_notify|TestPersonas_count" -v
```

Expected: PASS

- [ ] **Step 6: 确认原有 subagent 测试仍通过**

```bash
go test ./internal/subagent/... -v 2>&1 | tail -5
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/subagent/persona.go internal/subagent/manager.go internal/subagent/manager_test.go
git commit -m "feat(subagent): export Personas() + lifecycle hook dispatch"
```

---

### Task 4: agent_registry.go — AgentRegistry（新文件）

**Files:**
- Create: `internal/wsserver/agent_registry.go`
- Create: `internal/wsserver/agent_registry_test.go`

**Interfaces:**
- Consumes:
  - `subagent.Personas() []PersonaDefinition`（Task 3）
  - `loop.AgentInfo`, `loop.SessionSubagentsSnapshotPayload`（Task 1）
  - `(*loop.Runner).RunTurn(ctx, text)`（已有）
- Produces:
  - `type RunnerFactory func(ctx context.Context, sessionID string) (*loop.Runner, error)`
  - `type AgentStatus string` — `"idle"/"pending"/"running"/"done"`
  - `type AgentRegistry struct`
  - `NewAgentRegistry(factory RunnerFactory) *AgentRegistry`
  - `(*AgentRegistry).Activate(ctx context.Context, name string) (id string, ok bool)`
  - `(*AgentRegistry).Deactivate(name string)`
  - `(*AgentRegistry).RouteInput(ctx context.Context, agentID string, text string) error`
  - `(*AgentRegistry).Snapshot() []loop.AgentInfo`

- [ ] **Step 1: 写失败的测试**

新建 `internal/wsserver/agent_registry_test.go`：

```go
package wsserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
)

// noopFactory returns a nil Runner with no error (for unit tests that don't need routing).
var noopFactory = wsserver.RunnerFactory(func(_ context.Context, _ string) (*loop.Runner, error) {
	return nil, nil
})

func TestNewAgentRegistry_40_idle_slots(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	if len(snap) != 40 {
		t.Fatalf("got %d slots, want 40", len(snap))
	}
	for _, a := range snap {
		if a.Status != "idle" {
			t.Errorf("slot %q: want idle, got %s", a.Name, a.Status)
		}
		if a.ID == "" {
			t.Errorf("slot %q: ID is empty", a.Name)
		}
		if a.Color == "" {
			t.Errorf("slot %q: Color is empty", a.Name)
		}
	}
}

func TestAgentRegistry_personaID_stable_across_instances(t *testing.T) {
	r1 := wsserver.NewAgentRegistry(noopFactory)
	r2 := wsserver.NewAgentRegistry(noopFactory)
	s1, s2 := r1.Snapshot(), r2.Snapshot()
	for i := range s1 {
		if s1[i].ID != s2[i].ID {
			t.Errorf("slot %d ID not stable: %q vs %q", i, s1[i].ID, s2[i].ID)
		}
	}
}

func TestAgentRegistry_Activate_sets_running(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	firstName := r.Snapshot()[0].Name

	id, ok := r.Activate(context.Background(), firstName)
	if !ok {
		t.Fatal("Activate returned ok=false")
	}
	if id == "" {
		t.Fatal("Activate returned empty id")
	}

	for _, a := range r.Snapshot() {
		if a.Name == firstName {
			if a.Status != "running" {
				t.Errorf("status: got %q want running", a.Status)
			}
			if a.StartedAt == nil {
				t.Error("StartedAt must be set after Activate")
			}
			return
		}
	}
	t.Errorf("slot %q not found in snapshot", firstName)
}

func TestAgentRegistry_Activate_unknown_name_returns_false(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	_, ok := r.Activate(context.Background(), "nobody")
	if ok {
		t.Error("Activate with unknown name should return ok=false")
	}
}

func TestAgentRegistry_Activate_factory_error_rolls_back(t *testing.T) {
	failFactory := wsserver.RunnerFactory(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, errors.New("factory error")
	})
	r := wsserver.NewAgentRegistry(failFactory)
	name := r.Snapshot()[0].Name

	_, ok := r.Activate(context.Background(), name)
	if ok {
		t.Error("Activate should return ok=false when factory fails")
	}
	for _, a := range r.Snapshot() {
		if a.Name == name && a.Status != "idle" {
			t.Errorf("slot %q should be idle after factory failure, got %s", name, a.Status)
		}
	}
}

func TestAgentRegistry_Deactivate_sets_done(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	name := r.Snapshot()[0].Name

	r.Activate(context.Background(), name)
	r.Deactivate(name)

	for _, a := range r.Snapshot() {
		if a.Name == name {
			if a.Status != "done" {
				t.Errorf("status after Deactivate: got %q want done", a.Status)
			}
			return
		}
	}
	t.Error("slot not found after Deactivate")
}

func TestAgentRegistry_RouteInput_nil_runner_returns_error(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory) // noopFactory gives nil runner
	snap := r.Snapshot()
	name := snap[0].Name
	id, _ := r.Activate(context.Background(), name) // runner will be nil

	err := r.RouteInput(context.Background(), id, "hello")
	if err == nil {
		t.Error("RouteInput with nil runner should return error")
	}
}

func TestAgentRegistry_RouteInput_unknown_id_returns_error(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	err := r.RouteInput(context.Background(), "no-such-id", "hello")
	if err == nil {
		t.Error("RouteInput with unknown id should return error")
	}
}

func TestAgentRegistry_Snapshot_running_before_idle(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	name := snap[5].Name // pick a non-first slot

	r.Activate(context.Background(), name)
	snap2 := r.Snapshot()

	// The running slot must appear before all idle slots
	runningIdx := -1
	for i, a := range snap2 {
		if a.Name == name {
			runningIdx = i
		}
		if a.Status == "idle" && runningIdx >= 0 && i < runningIdx {
			t.Errorf("idle slot at %d appeared before running slot at %d", i, runningIdx)
		}
	}
	if runningIdx < 0 {
		t.Error("running slot not found in snapshot")
	}
}

func TestAgentRegistry_Snapshot_most_recent_running_first(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	name1, name2 := snap[0].Name, snap[1].Name

	r.Activate(context.Background(), name1)
	time.Sleep(2 * time.Millisecond)
	r.Activate(context.Background(), name2)

	snap2 := r.Snapshot()
	// name2 started later → shorter runtime → should appear first
	if snap2[0].Name != name2 {
		t.Errorf("most recently activated %q should be first, got %q", name2, snap2[0].Name)
	}
	if snap2[1].Name != name1 {
		t.Errorf("earlier activated %q should be second, got %q", name1, snap2[1].Name)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/wsserver/... -run "TestNewAgentRegistry|TestAgentRegistry" -v
```

Expected: FAIL — `wsserver.NewAgentRegistry undefined`

- [ ] **Step 3: 实现 agent_registry.go**

新建 `internal/wsserver/agent_registry.go`：

```go
package wsserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/subagent"
)

// AgentStatus is the lifecycle state of a persona slot.
type AgentStatus string

const (
	AgentStatusIdle    AgentStatus = "idle"
	AgentStatusPending AgentStatus = "pending"
	AgentStatusRunning AgentStatus = "running"
	AgentStatusDone    AgentStatus = "done"
)

// RunnerFactory creates a new Runner for a persona's conversation session.
// sessionID is the persona's stable UUID (derived from the persona name).
type RunnerFactory func(ctx context.Context, sessionID string) (*loop.Runner, error)

// personaSlot holds the state of a single persona.
type personaSlot struct {
	id        string
	name      string
	color     string
	status    AgentStatus
	runner    *loop.Runner // nil when idle or done
	startedAt *time.Time
	index     int // original order in defaultPersonas, for stable idle sorting
}

// AgentRegistry manages all 40 persona slots and routes user input to their Runners.
type AgentRegistry struct {
	mu      sync.RWMutex
	slots   []*personaSlot
	byID    map[string]*personaSlot
	byName  map[string]*personaSlot
	factory RunnerFactory
}

// statusPriority maps AgentStatus to sort priority (lower = higher in list).
var statusPriority = map[AgentStatus]int{
	AgentStatusRunning: 0,
	AgentStatusPending: 1,
	AgentStatusDone:    2,
	AgentStatusIdle:    3,
}

// personaID generates a stable UUID v4-format string from a persona name.
// The same name always produces the same ID regardless of restart.
func personaID(name string) string {
	h := sha256.Sum256([]byte("paw-persona:" + name))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // set version 4
	b[8] = (b[8] & 0x3f) | 0x80 // set variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewAgentRegistry initialises all 40 persona slots in idle state.
// factory is called by Activate to create a Runner when a persona becomes active.
func NewAgentRegistry(factory RunnerFactory) *AgentRegistry {
	personas := subagent.Personas()
	r := &AgentRegistry{
		byID:    make(map[string]*personaSlot, len(personas)),
		byName:  make(map[string]*personaSlot, len(personas)),
		factory: factory,
	}
	for i, p := range personas {
		id := personaID(p.Name)
		slot := &personaSlot{
			id:     id,
			name:   p.Name,
			color:  p.Color,
			status: AgentStatusIdle,
			index:  i,
		}
		r.slots = append(r.slots, slot)
		r.byID[id] = slot
		r.byName[p.Name] = slot
	}
	return r
}

// Activate transitions the named persona to running and creates its Runner via factory.
// Returns the persona's stable ID and ok=true on success.
// Returns ok=false if the name is unknown or the factory returns an error (slot is rolled back).
func (r *AgentRegistry) Activate(ctx context.Context, name string) (id string, ok bool) {
	r.mu.Lock()
	slot, exists := r.byName[name]
	if !exists {
		r.mu.Unlock()
		return "", false
	}
	slot.status = AgentStatusRunning
	now := time.Now().UTC()
	slot.startedAt = &now
	id = slot.id
	factory := r.factory
	r.mu.Unlock()

	// Create runner outside the lock — factory may be slow.
	runner, err := factory(ctx, id)
	if err != nil {
		r.mu.Lock()
		slot.status = AgentStatusIdle
		slot.startedAt = nil
		r.mu.Unlock()
		return "", false
	}

	r.mu.Lock()
	slot.runner = runner
	r.mu.Unlock()
	return id, true
}

// Deactivate transitions the named persona to done and releases its Runner.
// No-op if the name is unknown.
func (r *AgentRegistry) Deactivate(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.byName[name]
	if !ok {
		return
	}
	slot.status = AgentStatusDone
	slot.runner = nil
}

// RouteInput delivers text to the named agent's Runner.RunTurn().
// Returns an error if agentID is unknown or the slot has no active Runner.
func (r *AgentRegistry) RouteInput(ctx context.Context, agentID string, text string) error {
	r.mu.RLock()
	slot, ok := r.byID[agentID]
	if !ok || slot.runner == nil {
		r.mu.RUnlock()
		return fmt.Errorf("agent %s: not found or not running", agentID)
	}
	runner := slot.runner
	r.mu.RUnlock()

	_, err := runner.RunTurn(ctx, text)
	return err
}

// Snapshot returns a sorted copy of all agent states.
// Order: running (newest first) > pending > done > idle (original persona order).
func (r *AgentRegistry) Snapshot() []loop.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]loop.AgentInfo, len(r.slots))
	for i, slot := range r.slots {
		infos[i] = loop.AgentInfo{
			ID:        slot.id,
			Name:      slot.name,
			Color:     slot.color,
			Status:    string(slot.status),
			StartedAt: slot.startedAt,
		}
	}

	sort.SliceStable(infos, func(i, j int) bool {
		pi := statusPriority[AgentStatus(infos[i].Status)]
		pj := statusPriority[AgentStatus(infos[j].Status)]
		if pi != pj {
			return pi < pj
		}
		// Within running/pending/done: most recently started first.
		si, sj := infos[i].StartedAt, infos[j].StartedAt
		if si != nil && sj != nil {
			return si.After(*sj)
		}
		// Idle: SliceStable preserves the original slot order (slot.index order).
		return false
	})

	return infos
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/wsserver/... -run "TestNewAgentRegistry|TestAgentRegistry" -v
```

Expected: all 9 registry tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/wsserver/agent_registry.go internal/wsserver/agent_registry_test.go
git commit -m "feat(wsserver): add AgentRegistry with 40 persona slots and routing"
```

---

### Task 5: handler.go — target_agent_id 路由

**Files:**
- Modify: `internal/wsserver/handler.go`
- Create: `internal/wsserver/handler_compile_test.go`

**Interfaces:**
- Consumes:
  - `loop.SessionUserInputPayload.TargetAgentID`（Task 1）
  - `(*AgentRegistry).RouteInput(ctx, agentID, text) error`（Task 4）
- Produces:
  - `NewHandler(runner *loop.Runner, registry *AgentRegistry) *Handler`（签名变更）

- [ ] **Step 1: 写编译测试**

新建 `internal/wsserver/handler_compile_test.go`：

```go
package wsserver_test

import (
	"context"
	"testing"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
)

func TestHandler_new_signature_compiles(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	h := wsserver.NewHandler(nil, registry)
	_ = h
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/wsserver/... -run "TestHandler_new_signature_compiles" -v
```

Expected: FAIL — `NewHandler` 参数个数不匹配

- [ ] **Step 3: 修改 handler.go**

**3a.** 在 `Handler` struct 加 `registry` 字段：

```go
type Handler struct {
	runner     *loop.Runner
	registry   *AgentRegistry // 新增：routes target_agent_id messages
	preHooks   []PreHook
	afterHooks []AfterHook
}
```

**3b.** 修改 `NewHandler`：

```go
// NewHandler creates a Handler. registry routes messages with a target_agent_id;
// messages without a target go to runner.
func NewHandler(runner *loop.Runner, registry *AgentRegistry) *Handler {
	return &Handler{runner: runner, registry: registry}
}
```

**3c.** 修改 `HandleConn` 的 `EventKindUserInput` 分支（当前约第 88 行）：

```go
case loop.EventKindUserInput:
	if event.UserInput != nil {
		if event.UserInput.TargetAgentID != "" && h.registry != nil {
			procErr = h.registry.RouteInput(ctx, event.UserInput.TargetAgentID, event.UserInput.Text)
		} else {
			_, procErr = h.runner.RunTurn(ctx, event.UserInput.Text)
		}
	}
```

- [ ] **Step 4: 运行测试**

```bash
go test ./internal/wsserver/... -v 2>&1 | tail -10
```

Expected: all PASS

- [ ] **Step 5: 确认 wsserver 包编译**

```bash
go build ./internal/wsserver/...
```

Expected: 成功（`cmd/agent` 会因 `NewHandler`/`ListenAndServe` 签名变化而失败，Task 8 修复）

- [ ] **Step 6: Commit**

```bash
git add internal/wsserver/handler.go internal/wsserver/handler_compile_test.go
git commit -m "feat(wsserver): route target_agent_id to AgentRegistry"
```

---

### Task 6: server.go — ServerDeps + on-connect 推送

**Files:**
- Modify: `internal/wsserver/server.go`
- Create: `internal/wsserver/server_connect_test.go`

**Interfaces:**
- Consumes:
  - `(*AgentRegistry).Snapshot() []loop.AgentInfo`（Task 4）
  - `loop.SessionEventStore.Load(ctx, sessionID) ([]SessionEvent, error)`（已有）
  - `loop.EventKindSubagentsSnapshot`（Task 1）
  - `loop.EventKindHistoryMessage`（已有）
- Produces:
  - `type ServerDeps struct{Handler *Handler; Registry *AgentRegistry; Store loop.SessionEventStore; SessionID string}`
  - `(*Server).BuildMux(deps ServerDeps) *http.ServeMux`（新，供测试使用）
  - `(*Server).ListenAndServe(ctx context.Context, deps ServerDeps) error`（签名变更）

- [ ] **Step 1: 写失败的测试**

新建 `internal/wsserver/server_connect_test.go`：

```go
package wsserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
	"github.com/gorilla/websocket"
)

func wsDialTest(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn) loop.SessionEvent {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var ev loop.SessionEvent
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, msg)
	}
	return ev
}

func TestServer_pushes_subagents_snapshot_on_connect(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()
	server := wsserver.NewServer()
	handler := wsserver.NewHandler(nil, registry)
	deps := wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		Store:     store,
		SessionID: "test-sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)
	ev := readEvent(t, conn)

	if ev.Kind != loop.EventKindSubagentsSnapshot {
		t.Errorf("first message kind: got %q want %q", ev.Kind, loop.EventKindSubagentsSnapshot)
	}
	if ev.SubagentsSnapshot == nil {
		t.Fatal("SubagentsSnapshot payload is nil")
	}
	if len(ev.SubagentsSnapshot.Agents) != 40 {
		t.Errorf("agents count: got %d want 40", len(ev.SubagentsSnapshot.Agents))
	}
}

func TestServer_pushes_history_message_on_connect(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()

	// Pre-populate store with one history event
	histEv := loop.SessionEvent{
		ID:        "ev-1",
		SessionID: "test-sess",
		Kind:      loop.EventKindHistoryMessage,
	}
	_ = store.Append(context.Background(), "test-sess", histEv)

	server := wsserver.NewServer()
	handler := wsserver.NewHandler(nil, registry)
	deps := wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		Store:     store,
		SessionID: "test-sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)

	// Message 1: subagents_snapshot
	ev1 := readEvent(t, conn)
	if ev1.Kind != loop.EventKindSubagentsSnapshot {
		t.Fatalf("first message: got %q want subagents_snapshot", ev1.Kind)
	}

	// Message 2: history_message
	ev2 := readEvent(t, conn)
	if ev2.Kind != loop.EventKindHistoryMessage {
		t.Errorf("second message kind: got %q want history_message", ev2.Kind)
	}
	if ev2.ID != "ev-1" {
		t.Errorf("history event ID: got %q want ev-1", ev2.ID)
	}
}

func TestServer_skips_non_history_events_in_replay(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()

	// Add a delta_chunk event (should NOT be replayed) and a history_message
	_ = store.Append(context.Background(), "sess", loop.SessionEvent{
		ID:   "chunk-1",
		Kind: loop.EventKindDeltaChunk,
	})
	_ = store.Append(context.Background(), "sess", loop.SessionEvent{
		ID:   "hist-1",
		Kind: loop.EventKindHistoryMessage,
	})

	server := wsserver.NewServer()
	deps := wsserver.ServerDeps{
		Handler:   wsserver.NewHandler(nil, registry),
		Registry:  registry,
		Store:     store,
		SessionID: "sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)

	// Skip snapshot
	readEvent(t, conn)

	// Should receive only hist-1, not chunk-1
	ev := readEvent(t, conn)
	if ev.Kind != loop.EventKindHistoryMessage {
		t.Errorf("expected history_message, got %q", ev.Kind)
	}
	if ev.ID != "hist-1" {
		t.Errorf("expected hist-1, got %q", ev.ID)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/wsserver/... -run "TestServer_pushes|TestServer_skips" -v
```

Expected: FAIL — `ServerDeps undefined`、`BuildMux undefined`

- [ ] **Step 3: 修改 server.go**

**3a.** 在 `server.go` 文件顶部的 `import` 块中确保有 `"context"`（通常已有）。

**3b.** 在 `Server` struct 定义之前添加 `ServerDeps`：

```go
// ServerDeps bundles all dependencies the WebSocket server needs per-session.
type ServerDeps struct {
	Handler   *Handler
	Registry  *AgentRegistry
	Store     loop.SessionEventStore
	SessionID string
}
```

**3c.** 新增 `BuildMux`（提取 mux 构建，供 `ListenAndServe` 和测试共用）：

```go
// BuildMux returns an http.ServeMux with /ws registered. Used by ListenAndServe
// and directly by tests via httptest.NewServer.
func (s *Server) BuildMux(deps ServerDeps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWSWithDeps(deps))
	return mux
}
```

**3d.** 修改 `ListenAndServe` 签名（替换原来的实现）：

```go
// ListenAndServe starts the WebSocket server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, deps ServerDeps) error {
	mux := s.BuildMux(deps)
	srv := &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Printf("WS server listening on %s", s.addr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
```

**3e.** 新增 `handleWSWithDeps`（替换原来的 `HandleWS`）：

```go
// handleWSWithDeps returns an http.HandlerFunc that upgrades to WebSocket,
// sends the initial snapshot + history, then delegates to handler.HandleConn.
func (s *Server) handleWSWithDeps(deps ServerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade error: %v", err)
			return
		}
		id := s.nextID.Add(1)
		s.clients.Store(id, conn)
		defer func() {
			s.clients.Delete(id)
			conn.Close()
		}()

		s.pushSnapshot(conn, deps.Registry)
		s.pushHistory(conn, deps.Store, deps.SessionID)

		if deps.Handler != nil {
			deps.Handler.HandleConn(r.Context(), conn)
		}
	}
}

// pushSnapshot sends a subagents_snapshot event to one connection.
func (s *Server) pushSnapshot(conn *websocket.Conn, registry *AgentRegistry) {
	if registry == nil {
		return
	}
	ev := loop.SessionEvent{
		Kind: loop.EventKindSubagentsSnapshot,
		SubagentsSnapshot: &loop.SessionSubagentsSnapshotPayload{
			Agents: registry.Snapshot(),
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		log.Printf("ws pushSnapshot marshal: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("ws pushSnapshot write: %v", err)
	}
}

// pushHistory replays history_message events from the store to one connection.
// Other event kinds (delta_chunk, tool_call_fired, etc.) are skipped.
func (s *Server) pushHistory(conn *websocket.Conn, store loop.SessionEventStore, sessionID string) {
	if store == nil || sessionID == "" {
		return
	}
	events, err := store.Load(context.Background(), sessionID)
	if err != nil {
		log.Printf("ws pushHistory load: %v", err)
		return
	}
	for _, ev := range events {
		if ev.Kind != loop.EventKindHistoryMessage {
			continue
		}
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws pushHistory write: %v", err)
			return
		}
	}
}
```

**3f.** 删除（或保留但弃用）原 `HandleWS` 方法——检查是否有其他调用：

```bash
grep -rn "\.HandleWS(" /Users/united_pooh/PyProjects/go-code/
```

若只在 `server.go` 自身里使用，直接删除原 `HandleWS` 方法。

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/wsserver/... -run "TestServer_pushes|TestServer_skips" -v
```

Expected: PASS

- [ ] **Step 5: 全量 wsserver 测试**

```bash
go test ./internal/wsserver/... -v 2>&1 | tail -10
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/wsserver/server.go internal/wsserver/server_connect_test.go
git commit -m "feat(wsserver): ServerDeps, BuildMux, on-connect snapshot+history push"
```

---

### Task 7: wsui.go — SetRegistry + lifecycle hooks + usage_update

**Files:**
- Modify: `internal/wsserver/wsui.go`
- Create: `internal/wsserver/wsui_test.go`

**Interfaces:**
- Consumes:
  - `(*AgentRegistry).Activate(ctx, name) (id, ok)`（Task 4）
  - `(*AgentRegistry).Deactivate(name)`（Task 4）
  - `(*AgentRegistry).Snapshot() []loop.AgentInfo`（Task 4）
  - `subagent.TaskSnapshot.Name`（已有）
  - `loop.EventKindUsageUpdate`（已有）
  - `loop.EventKindSubagentsSnapshot`（Task 1）
  - `loop.SessionSubagentsSnapshotPayload`（Task 1）
  - `model.Usage`（已有）
- Produces:
  - `(*WSUI).SetRegistry(r *AgentRegistry)`
  - `(*WSUI).OnTaskStarted(task subagent.TaskSnapshot)`（实现 duck-type taskLifecycleNotifier）
  - `(*WSUI).OnTaskFinished(task subagent.TaskSnapshot)`
  - `(*WSUI).OnModelUsage(usage model.Usage)`

- [ ] **Step 1: 写失败的测试**

新建 `internal/wsserver/wsui_test.go`：

```go
package wsserver_test

import (
	"context"
	"testing"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/wsserver"
)

func TestWSUI_SetRegistry_and_OnTaskStarted_activates_slot(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	wsui.SetRegistry(registry)

	name := registry.Snapshot()[0].Name
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: name})

	for _, a := range registry.Snapshot() {
		if a.Name == name {
			if a.Status != "running" {
				t.Errorf("slot %q: got %q want running", name, a.Status)
			}
			return
		}
	}
	t.Errorf("slot %q not found", name)
}

func TestWSUI_OnTaskFinished_deactivates_slot(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	wsui.SetRegistry(registry)

	name := registry.Snapshot()[0].Name
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: name})
	wsui.OnTaskFinished(subagent.TaskSnapshot{Name: name})

	for _, a := range registry.Snapshot() {
		if a.Name == name {
			if a.Status != "done" {
				t.Errorf("slot %q: got %q want done", name, a.Status)
			}
			return
		}
	}
	t.Errorf("slot %q not found", name)
}

func TestWSUI_OnModelUsage_does_not_panic_with_no_clients(t *testing.T) {
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	// No connected clients; must not panic
	wsui.OnModelUsage(model.Usage{InputTokens: 100, OutputTokens: 50})
}

func TestWSUI_OnTaskStarted_nil_registry_does_not_panic(t *testing.T) {
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	// SetRegistry not called
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: "nobody"})
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/wsserver/... -run "TestWSUI" -v
```

Expected: FAIL — `SetRegistry`、`OnTaskStarted`、`OnTaskFinished`、`OnModelUsage` 未定义

- [ ] **Step 3: 修改 wsui.go**

**3a.** 在 `WSUI` struct 加 `registry` 字段：

```go
type WSUI struct {
	server    *Server
	sessionID string
	registry  *AgentRegistry // routes lifecycle callbacks to persona slots
}
```

**3b.** 添加 `SetRegistry`：

```go
// SetRegistry wires the AgentRegistry so task lifecycle callbacks update persona state.
func (w *WSUI) SetRegistry(r *AgentRegistry) {
	w.registry = r
}
```

**3c.** 添加 `OnTaskStarted` 和 `OnTaskFinished`（满足 `subagent.taskLifecycleNotifier` duck-type）：

```go
// OnTaskStarted activates the named persona and broadcasts an updated snapshot.
// Called by subagent.Manager when a task starts (via taskLifecycleNotifier duck-type).
func (w *WSUI) OnTaskStarted(task subagent.TaskSnapshot) {
	if w.registry == nil {
		return
	}
	w.registry.Activate(context.Background(), task.Name)
	w.broadcastSnapshot()
}

// OnTaskFinished deactivates the named persona and broadcasts an updated snapshot.
func (w *WSUI) OnTaskFinished(task subagent.TaskSnapshot) {
	if w.registry == nil {
		return
	}
	w.registry.Deactivate(task.Name)
	w.broadcastSnapshot()
}
```

**3d.** 添加 `broadcastSnapshot` helper：

```go
func (w *WSUI) broadcastSnapshot() {
	if w.registry == nil {
		return
	}
	ev := w.newEvent(loop.EventKindSubagentsSnapshot)
	ev.SubagentsSnapshot = &loop.SessionSubagentsSnapshotPayload{
		Agents: w.registry.Snapshot(),
	}
	w.server.Broadcast(ev)
}
```

**3e.** 添加 `OnModelUsage`（满足 `loop.modelUsageReceiver` 接口）：

```go
// OnModelUsage broadcasts a usage_update event after each model response.
func (w *WSUI) OnModelUsage(usage model.Usage) {
	ev := w.newEvent(loop.EventKindUsageUpdate)
	ev.Usage = &loop.SessionUsagePayload{Usage: usage, IsSession: false}
	w.server.Broadcast(ev)
}
```

**3f.** 确认 `wsui.go` 的 import 块包含所有新增依赖：

```go
import (
	"context"
	"encoding/json"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/ui"
)
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/wsserver/... -run "TestWSUI" -v
```

Expected: PASS

- [ ] **Step 5: 全量 wsserver 测试**

```bash
go test ./internal/wsserver/... -v 2>&1 | tail -10
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/wsserver/wsui.go internal/wsserver/wsui_test.go
git commit -m "feat(wsserver): WSUI SetRegistry, lifecycle hooks, usage_update"
```

---

### Task 8: main.go — 串联所有组件

**Files:**
- Modify: `cmd/agent/main.go`

**Interfaces:**
- Consumes: 所有 Task 1–7 的导出接口

- [ ] **Step 1: 确认当前编译错误**

```bash
cd /Users/united_pooh/PyProjects/go-code
go build ./... 2>&1
```

Expected: 编译失败（`NewHandler`、`ListenAndServe` 签名不匹配）。记录错误行号备用。

- [ ] **Step 2: 修改 runWSMode 函数**

将 `cmd/agent/main.go` 的 `runWSMode` 函数完整替换为：

```go
func runWSMode(ctx context.Context, opts options) error {
	server := wsserver.NewServer()

	// RunnerFactory: create an independent Runner for each activated persona.
	// Each persona gets its own session (sessionID = persona's stable UUID).
	factory := wsserver.RunnerFactory(func(fctx context.Context, sessionID string) (*loop.Runner, error) {
		r, _, _, _, _, _, err := buildRunnerWithSubagentContext(
			fctx, sessionID, headless.New(io.Discard),
			subagentRuntimeContext{depth: 1, maxDepth: 4},
		)
		return r, err
	})
	registry := wsserver.NewAgentRegistry(factory)

	wsui := wsserver.NewWSUI(server, "")
	wsui.SetRegistry(registry)

	runner, sessionID, _, _, _, _, err := buildRunner(ctx, opts.sessionID, wsui)
	if err != nil {
		return err
	}
	wsui.SetSessionID(sessionID)
	runner.SetStreamMAEnabled(opts.streamMA)

	handler := wsserver.NewHandler(runner, registry)

	deps := wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		Store:     runner.EventStore(), // InMemorySessionEventStore set in buildRunner
		SessionID: sessionID,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe(ctx, deps) }()

	select {
	case err := <-serverErr:
		return fmt.Errorf("WS server failed to start (port conflict?): %w", err)
	case <-time.After(300 * time.Millisecond):
	}

	log.Printf("Agent ready. WS server started. session=%s", sessionID)
	<-ctx.Done()
	return nil
}
```

- [ ] **Step 3: 确认 import 块包含新增依赖**

检查 `cmd/agent/main.go` 的 import 块，确保包含：

```go
"io"
"codex-agent-go/internal/loop"
"codex-agent-go/internal/ui/headless"
"codex-agent-go/internal/wsserver"
```

若 `"io"` 缺失，在 import 块加上。

- [ ] **Step 4: 编译确认**

```bash
go build ./...
```

Expected: PASS（无编译错误）

- [ ] **Step 5: 运行现有测试**

```bash
go test ./cmd/agent/... -v
```

Expected: PASS

- [ ] **Step 6: 冒烟测试（需要终端）**

```bash
# 终端 1：启动服务
go run ./cmd/agent/

# 终端 2（需安装 websocat）：验证初始推送
websocat ws://localhost:8765/ws
# 应立即收到 {"kind":"subagents_snapshot","subagents_snapshot":{"agents":[...]}} (40个idle角色)
```

若没有 `websocat`，用以下 Go 片段验证：

```bash
go run - <<'EOF'
package main
import (
    "fmt"
    "github.com/gorilla/websocket"
)
func main() {
    c, _, _ := websocket.DefaultDialer.Dial("ws://localhost:8765/ws", nil)
    _, msg, _ := c.ReadMessage()
    fmt.Println(string(msg[:100]))
}
EOF
```

- [ ] **Step 7: Commit**

```bash
git add cmd/agent/main.go
git commit -m "feat(main): wire AgentRegistry, RunnerFactory, ServerDeps"
```

---

### Task 9: Paw — SessionEventDecoder.swift 数据模型

**Files:**
- Modify: `Paw/Paw/Networking/SessionEventDecoder.swift`

**Interfaces:**
- Consumes: go-code JSON 字段（Task 1 定义的 `subagents_snapshot`、`agents`、`id`、`name`、`color`、`status`、`started_at`）
- Produces:
  - `SessionEventKind.subagentsSnapshot`
  - `struct AgentInfoPayload: Codable`
  - `struct SubagentsSnapshotPayload: Codable`
  - `SessionEvent.subagentsSnapshot: SubagentsSnapshotPayload?`

- [ ] **Step 1: 在 SessionEventKind enum 加新 case**

在 `Paw/Paw/Networking/SessionEventDecoder.swift` 的 `enum SessionEventKind` 里，在 `case unknown` 之前添加：

```swift
case subagentsSnapshot = "subagents_snapshot"
```

- [ ] **Step 2: 添加新 payload 类型**

在文件中 `struct AssistantDeltaPayload` 定义之后添加：

```swift
// MARK: - Subagents Snapshot Payload

struct AgentInfoPayload: Codable {
    let id: String
    let name: String
    let color: String
    let status: String       // "idle" | "pending" | "running" | "done"
    let startedAt: String?

    enum CodingKeys: String, CodingKey {
        case id, name, color, status
        case startedAt = "started_at"
    }
}

struct SubagentsSnapshotPayload: Codable {
    let agents: [AgentInfoPayload]
}
```

- [ ] **Step 3: 在 SessionEvent struct 加字段**

在 `struct SessionEvent` 里，与其他 `let xxx: XxxPayload?` 字段对齐，添加：

```swift
let subagentsSnapshot: SubagentsSnapshotPayload?
```

在 `enum CodingKeys` 里添加：

```swift
case subagentsSnapshot = "subagents_snapshot"
```

- [ ] **Step 4: 编译确认**

```bash
cd /Users/united_pooh/PyProjects/Paw
xcodebuild -scheme Paw -destination 'platform=macOS' build 2>&1 | grep -E "error:|BUILD"
```

Expected: `BUILD SUCCEEDED`（无 error，warning 可忽略）

- [ ] **Step 5: Commit**

```bash
cd /Users/united_pooh/PyProjects/Paw
git add Paw/Paw/Networking/SessionEventDecoder.swift
git commit -m "feat(networking): add subagents_snapshot payload types"
```

---

### Task 10: Paw — AppViewModel.swift 事件处理

**Files:**
- Modify: `Paw/Paw/ViewModels/AppViewModel.swift`

**Interfaces:**
- Consumes:
  - `SessionEventKind.subagentsSnapshot`（Task 9）
  - `SubagentsSnapshotPayload.agents: [AgentInfoPayload]`（Task 9）
  - `SubagentModel(id:personaName:personaColor:task:status:elapsedSeconds:)`（已有）
  - `SubagentStatus` enum: `.idle / .running / .done / .failed`（已有）

- [ ] **Step 1: 在 handleAgentEvent 添加 case**

在 `AppViewModel.swift` 的 `handleAgentEvent` 方法的 switch 语句里，在 `default: break` 之前添加：

```swift
case .subagentsSnapshot:
    guard let snap = event.subagentsSnapshot else { return }
    subagents = snap.agents.map { info in
        let agentStatus: SubagentStatus = {
            switch info.status {
            case "running": return .running
            case "done":    return .done
            default:        return .idle
            }
        }()
        return SubagentModel(
            id: UUID(uuidString: info.id) ?? UUID(),
            personaName:   info.name,
            personaColor:  info.color,
            task:          "",
            status:        agentStatus,
            elapsedSeconds: 0
        )
    }
```

- [ ] **Step 2: 编译确认**

```bash
cd /Users/united_pooh/PyProjects/Paw
xcodebuild -scheme Paw -destination 'platform=macOS' build 2>&1 | grep -E "error:|BUILD"
```

Expected: `BUILD SUCCEEDED`

- [ ] **Step 3: 手动端到端验证**

1. 在 `/Users/united_pooh/PyProjects/go-code` 启动后端：
   ```bash
   go run ./cmd/agent/
   ```
2. 在 Xcode 启动 Paw
3. 验证：
   - Agent Panel 立即显示 40 个 persona（全部 idle 状态）
   - 向主 Agent 发消息触发 Subagent 工具后，对应 persona 浮到顶部并变为 running
   - Subagent 完成后，该 persona 变为 done 状态

- [ ] **Step 4: Commit**

```bash
cd /Users/united_pooh/PyProjects/Paw
git add Paw/Paw/ViewModels/AppViewModel.swift
git commit -m "feat(vm): handle subagents_snapshot to populate Agent Panel"
```

---

## Self-Review

**Spec 覆盖检查：**

| Spec 要求 | 实现位置 |
|-----------|---------|
| target_agent_id 路由 | Task 1（字段）+ Task 4（Registry.RouteInput）+ Task 5（Handler 分支）|
| subagent persona 推送 | Task 1（类型）+ Task 4（Snapshot）+ Task 6（on-connect push）+ Task 7（broadcastSnapshot）|
| usage_update 广播 | Task 7（OnModelUsage）|
| 历史消息回放 | Task 6（pushHistory，只推 history_message）|
| Paw 数据模型 | Task 9 |
| Paw AppViewModel | Task 10 |
| 排序规则 | Task 4（Snapshot sort: running↓startedAt > pending > done > idle）|
| duck-type Notifier | Task 3（接口定义）+ Task 7（WSUI 实现）|
| runner.EventStore() | Task 2 |
| Personas() 导出 | Task 3 |

**Placeholder 检查：** 无 TBD/TODO，每步都有完整代码。

**类型一致性检查：**
- `RunnerFactory` 定义于 Task 4，使用于 Task 8 ✓
- `ServerDeps` 定义于 Task 6，使用于 Task 8 ✓
- `AgentRegistry.Activate(ctx, name)` 定义于 Task 4，调用于 Task 7 ✓
- `SubagentsSnapshotPayload` 定义于 Task 1（Go）/ Task 9（Swift），使用于 Task 6、7、10 ✓
- `SubagentModel` 构造参数（task, status, elapsedSeconds）与现有 Swift 定义对齐 ✓
