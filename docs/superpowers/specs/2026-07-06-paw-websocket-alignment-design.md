# Paw × go-code WebSocket 接口对齐设计

**日期**：2026-07-06  
**状态**：已批准  
**涉及仓库**：`go-code`（主要）、`Paw`（轻量补充）

---

## 背景

Paw 是一个 macOS Swift 客户端，通过 `ws://localhost:8765/ws` 与 go-code 后端通信。  
此前分析发现四处差距：

| # | 差距 | 症状 |
|---|------|------|
| 1 | `target_agent_id` 被忽略 | 定向消息全部路由到主 Runner |
| 2 | Subagent persona 未推送 | Paw 的 Agent Panel 永远空白 |
| 3 | `usage_update` 未广播 | Paw context 面板 token 统计不更新 |
| 4 | 历史消息未回放 | 断线重连后会话内容丢失 |

---

## 目标

1. `target_agent_id` 路由到对应 subagent 的独立 `loop.Runner`
2. Paw 连上时立即收到全部 40 个 persona 的状态快照（含排序）
3. Subagent 生命周期变化时实时广播更新快照
4. `usage_update` 在每轮结束后广播
5. 新连接建立时回放当前 session 的历史消息

---

## 架构总览

变更集中在 `internal/wsserver/` 包，新增一个文件，修改三个现有文件；Paw 只做两处轻量补充。

```
┌─────────────────────────────────────────────────────┐
│                     go-code                         │
│                                                     │
│  cmd/agent/main.go                                  │
│    └─ 创建 AgentRegistry（含40个persona槽）          │
│    └─ 把 Registry 注入 Handler 和 WSUI              │
│                                                     │
│  wsserver/agent_registry.go  ← 新文件               │
│    AgentRegistry(factory RunnerFactory)             │
│      personas: map[uuid]PersonaSlot                 │
│      Activate(name)  → idle→running, factory()      │
│      Deactivate(name)→ running→done                 │
│      RouteInput(ctx,id,text) → runner.RunTurn()     │
│      Snapshot()              → sorted []AgentInfo   │
│                                                     │
│  wsserver/handler.go  ← 改                          │
│    target_agent_id == ""  → 主Runner                │
│    target_agent_id != ""  → AgentRegistry.Route()   │
│                                                     │
│  wsserver/server.go  ← 改                           │
│    新连接建立后：单播 subagents_snapshot             │
│    新连接建立后：单播 history_message × N            │
│                                                     │
│  wsserver/wsui.go  ← 改                             │
│    OnTaskStarted  → registry.Activate + Broadcast   │
│    OnTaskFinished → registry.Deactivate + Broadcast │
│    OnModelUsage   → Broadcast usage_update           │
│                                                     │
│  loop/session_event.go  ← 改                        │
│    SessionUserInputPayload += TargetAgentID          │
│    新增 EventKindSubagentsSnapshot                   │
│    新增 SessionSubagentsSnapshotPayload              │
└─────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────┐
│                      Paw                            │
│  SessionEventDecoder.swift  ← 改                    │
│    新增 subagents_snapshot case + payload            │
│                                                     │
│  AppViewModel.swift  ← 改                           │
│    handleAgentEvent 里处理 subagents_snapshot        │
│    → 全量替换 self.subagents 数组                    │
└─────────────────────────────────────────────────────┘
```

**正向数据流**

1. Paw 连上 → Server 单播 `subagents_snapshot`（40个idle槽）+ 历史消息
2. 用户发 `user_input`（无 target）→ 主 Runner → 流式广播 `delta_chunk`
3. 主 Runner 调 Subagent 工具 → Manager 调用 `OnTaskStarted` → WSUI → Registry.Activate（内部调 factory 创建 Runner）→ 广播新 `subagents_snapshot`
4. 用户发 `user_input`（带 `target_agent_id`）→ Handler → Registry.RouteInput → 目标 Runner → 流式广播

---

## 数据模型

### go-code：session_event.go 修改

```go
// UserInput payload 增加路由字段
type SessionUserInputPayload struct {
    Text          string `json:"text"`
    TargetAgentID string `json:"target_agent_id,omitempty"` // 新增
}

// 新事件类型
const EventKindSubagentsSnapshot SessionEventKind = "subagents_snapshot"

// 新 payload：全量 agent 列表
type SessionSubagentsSnapshotPayload struct {
    Agents []AgentInfo `json:"agents"`
}

type AgentInfo struct {
    ID        string     `json:"id"`          // 稳定 UUID（由 persona 名 hash 生成）
    Name      string     `json:"name"`
    Color     string     `json:"color"`
    Status    string     `json:"status"`      // "running"|"pending"|"done"|"idle"
    StartedAt *time.Time `json:"started_at,omitempty"`
}
```

### Paw：SessionEventDecoder.swift 修改

```swift
// 新增 kind
case subagentsSnapshot = "subagents_snapshot"

// 新增 payload 类型
struct AgentInfoPayload: Codable {
    let id: String
    let name: String
    let color: String
    let status: String
    let startedAt: String?

    enum CodingKeys: String, CodingKey {
        case id, name, color, status
        case startedAt = "started_at"
    }
}

struct SubagentsSnapshotPayload: Codable {
    let agents: [AgentInfoPayload]
}

// SessionEvent 新增字段
let subagentsSnapshot: SubagentsSnapshotPayload?
// CodingKeys: subagentsSnapshot = "subagents_snapshot"
```

---

## AgentRegistry（新文件）

**文件**：`internal/wsserver/agent_registry.go`

```go
type AgentStatus string

const (
    AgentStatusIdle    AgentStatus = "idle"
    AgentStatusPending AgentStatus = "pending"
    AgentStatusRunning AgentStatus = "running"
    AgentStatusDone    AgentStatus = "done"
)

type PersonaSlot struct {
    ID        string
    Name      string
    Color     string
    Status    AgentStatus
    Runner    *loop.Runner  // nil when idle/done
    StartedAt *time.Time
}

type AgentRegistry struct {
    mu     sync.RWMutex
    slots  []*PersonaSlot         // 保留 persona.go 定义顺序
    byID   map[string]*PersonaSlot
    byName map[string]*PersonaSlot
}
```

**方法**

| 方法 | 说明 |
|------|------|
| `NewAgentRegistry(factory RunnerFactory)` | 按 persona.go 40个角色初始化，全部 idle；factory 用于在 Activate 时按需创建 Runner |
| `Activate(name string) (id string, ok bool)` | idle→running，调用 factory 创建 Runner；返回稳定 ID |
| `Deactivate(name string)` | running→done，Runner 引用置 nil |
| `RouteInput(ctx, agentID, text) error` | 投递到目标 Runner.RunTurn() |
| `Snapshot() []AgentInfo` | 返回排序后的全量列表 |

`RunnerFactory` 定义：

```go
// RunnerFactory 按 sessionID 构造一个新 Runner，供 AgentRegistry 在激活 persona 时使用。
// sessionID 由 AgentRegistry 内部生成（= personaID）。
type RunnerFactory func(ctx context.Context, sessionID string) (*loop.Runner, error)
```

工厂由 `main.go` 注入，内部调用 `buildRunnerWithSubagentContext` 并丢弃不需要的返回值。

**稳定 ID 生成**

```go
func personaID(name string) string {
    h := sha256.Sum256([]byte("paw-persona:" + name))
    b := h[:16]
    b[6] = (b[6] & 0x0f) | 0x40 // version 4
    b[8] = (b[8] & 0x3f) | 0x80 // variant
    return fmt.Sprintf("%x-%x-%x-%x-%x",
        b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
```

**排序规则**（Snapshot 输出顺序，从上到下）

| 优先级 | 状态 | 二级排序 |
|--------|------|---------|
| 1 | `running` | StartedAt 降序（最近启动→最上） |
| 2 | `pending` | StartedAt 降序 |
| 3 | `done` | StartedAt 降序 |
| 4 | `idle` | persona.go 原始顺序 |

**并发安全**：读写均持 `sync.RWMutex`；`RouteInput` 持读锁取 Runner 引用后释放锁，再调用 `RunTurn`（RunTurn 不在锁内）。

---

## Handler 路由改动

```go
// NewHandler 签名变更
func NewHandler(runner *loop.Runner, registry *AgentRegistry) *Handler

// HandleConn 路由逻辑
case loop.EventKindUserInput:
    if event.UserInput == nil {
        continue
    }
    if event.UserInput.TargetAgentID == "" {
        _, procErr = h.runner.RunTurn(ctx, event.UserInput.Text)
    } else {
        procErr = h.registry.RouteInput(ctx,
            event.UserInput.TargetAgentID,
            event.UserInput.Text)
    }
```

---

## Server：新连接推送

`ListenAndServe` 签名改为接收 `ServerDeps`：

```go
type ServerDeps struct {
    Handler   *Handler
    Registry  *AgentRegistry
    Store     loop.SessionEventStore
    SessionID string
}

func (s *Server) ListenAndServe(ctx context.Context, deps ServerDeps) error
```

新连接升级后、进入 `HandleConn` 前：

1. `pushSnapshot(conn, deps.Registry)` — 单播 `subagents_snapshot`
2. `pushHistory(conn, deps.Store, deps.SessionID)` — 单播历史 `history_message` 事件序列

历史回放只推 `history_message` 类型，不推 `delta_chunk` 等实时事件。

---

## WSUI 改动

```go
// 新增字段和方法
type WSUI struct {
    server    *Server
    sessionID string
    registry  *AgentRegistry  // 新增
}

func (w *WSUI) SetRegistry(r *AgentRegistry)

// subagent.Notifier 扩展实现（生命周期）
// 当前 Notifier 接口只有 OnSystemMessage；需扩展为：
//   type Notifier interface {
//       OnSystemMessage(event ui.SystemEvent) error
//       OnTaskStarted(task TaskSnapshot)
//       OnTaskFinished(task TaskSnapshot)
//   }
// Manager 在 startTask / finishTask 时调用这两个新方法。
func (w *WSUI) OnTaskStarted(task subagent.TaskSnapshot) {
    w.registry.Activate(task.Name)
    w.broadcastSnapshot()
}

func (w *WSUI) OnTaskFinished(task subagent.TaskSnapshot) {
    w.registry.Deactivate(task.Name)
    w.broadcastSnapshot()
}

// usage_update
func (w *WSUI) OnModelUsage(usage model.Usage) {
    ev := w.newEvent(loop.EventKindUsageUpdate)
    ev.Usage = &loop.SessionUsagePayload{Usage: usage, IsSession: false}
    w.server.Broadcast(ev)
}
```

WSUI 实现扩展后的 `subagent.Notifier` 接口以接收 Manager 的生命周期回调。

---

## main.go 接线

```go
func runWSMode(ctx context.Context, opts options) error {
    server := wsserver.NewServer()

    // factory：为每个被激活的 persona 创建独立 Runner
    factory := func(fctx context.Context, sessionID string) (*loop.Runner, error) {
        r, _, _, _, _, _, err := buildRunnerWithSubagentContext(
            fctx, sessionID, headless.New(io.Discard),
            subagentRuntimeContext{depth: 1, maxDepth: 4},
        )
        return r, err
    }
    registry := wsserver.NewAgentRegistry(factory)

    wsui := wsserver.NewWSUI(server, "")
    wsui.SetRegistry(registry)

    runner, sessionID, _, _, _, _, err := buildRunner(ctx, opts.sessionID, wsui)
    // ...
    wsui.SetSessionID(sessionID)

    handler := wsserver.NewHandler(runner, registry)

    // runner.EventStore() 是新增的 accessor，返回其内部 InMemorySessionEventStore
    deps := wsserver.ServerDeps{
        Handler:   handler,
        Registry:  registry,
        Store:     runner.EventStore(),
        SessionID: sessionID,
    }
    go func() { serverErr <- server.ListenAndServe(ctx, deps) }()
    // ...
}
```

`loop.Runner` 需新增 `EventStore() loop.SessionEventStore` 公开 accessor，暴露内部已通过 `runner.SetEventStore(loop.NewInMemorySessionEventStore())` 设置的 store。

---

## Paw：AppViewModel 改动

```swift
case .subagentsSnapshot:
    guard let snap = event.subagentsSnapshot else { return }
    subagents = snap.agents.map { info in
        SubagentModel(
            id: UUID(uuidString: info.id) ?? UUID(),
            personaName: info.name,
            personaColor: info.color
        )
    }
```

---

## 错误处理

| 场景 | 处理方式 |
|------|---------|
| `target_agent_id` 不存在或 Runner 为 nil | `RouteInput` 返回 error，Handler 记 log，不 crash |
| 历史回放失败（store 读不到） | 跳过回放，正常服务；log warning |
| `Activate` 找不到 persona 名 | 返回 `ok=false`，WSUI 记 log，snapshot 不变 |
| 连接断开时有 RouteInput 进行中 | ctx 取消，RunTurn 返回 error，Handler 退出循环 |

---

## 不在本次范围内

- Paw 端 `SubagentModel` 增加 `status` 字段的 UI 展示（颜色/图标区分状态）
- subagent Runner 的流式输出带 `agent_id` 标签（目前广播时无法区分来源 Runner）
- 多 session 支持（当前设计仅维护单一 sessionID）
