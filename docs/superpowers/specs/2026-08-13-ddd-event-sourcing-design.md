# DDD Event Sourcing 设计（核心运行时）

**状态：** 已实现（2026-08-13，Task 0-9 完成；Task 10 evidence/checkpoint 事件化完成）
**配套计划：** `docs/superpowers/plans/2026-08-13-ddd-event-sourcing-plan.md`
**范围：** Goal、Plan、Session/Turn、Tool 调用、Todo

---

## 1. 背景与动机

当前核心运行时的持久化模型是「状态字段直接落盘」：

- `goal` 状态纯内存，进程退出即丢失；
- `plan` 以 `PlanDoc` 文件为事实来源，`Status` 由会话生命周期派生；
- `session` 已是 append-only JSONL transcript（`Record` + 连续 `seq` + `TurnJournal`），但无统一事件契约，goal/plan/todo 域未接入；
- 多处生命周期枚举（`GoalStatus`/`SessionStatus`/`TurnStatus`/`TaskStatus`）各自为政，无统一审计，无法时点回溯、无法修复错误状态、丢失「为什么」（暂停原因、失败原因）。

**目标：** 引入 DDD Event Sourcing —— 事件为唯一持久化事实，状态为事件流的派生投影。状态可重建、可审计、可修复；写模型保留状态机校验，读模型无状态投影。

## 2. 现状调研

### 2.1 状态机清单

| 位置 | 状态机 | 类型 |
|---|---|---|
| `internal/goal/state.go` | Goal 生命周期（`CanTransition` + `(*Goal).Transition` + 测试） | 完整规则表 |
| `internal/plan/state.go` | Plan 会话（`CanTransition` + `(*Session).Transition` + 测试） | 完整规则表 |
| `internal/session/turns.go` | `TurnStatus`：completed / failed / stopped | 枚举 |
| `internal/subagent/manager.go` | `TaskStatus`：running → completed / failed / stopped / interrupted；not_found 查询标记 | 枚举 |
| `internal/loop/task_orchestrator.go` | 任务编排：running / completed / paused / failed | 枚举 |
| `internal/todo/types.go` | `Status`：pending / in_progress / completed | 枚举 |
| `internal/goal/evidence.go` | `EvidenceStatus`：passed / failed | 枚举 |
| `internal/streamma/types.go` | `RunStatus`：completed / failed | 枚举 |

> 范围说明：`internal/subagent`、`internal/loop/task_orchestrator`、`internal/streamma` 为执行层状态机，**本次迁移范围外**（保持现有枚举，由上层聚合驱动）；未来可按需扩展事件域（如 `task.*`）。

### 2.2 存储现状

| 域 | 现状 | 影响 |
|---|---|---|
| Session | `jsonl_store.go`：append-only JSONL，`Record` 带连续 `seq`，`TurnJournal`（BeginTurn/AppendAssistant/AppendToolResult/CompleteTurn/FailTurn），`SyncPolicy`，尾部损坏恢复 | 事件溯源雏形；迁移 = 统一信封 + 域扩展 |
| Goal | `store_memory.go` 纯内存 | 事件溯源 = 新增持久化能力，无旧数据迁移 |
| Plan | `FileStore` 持久化 `PlanDoc` 文件（文件名含日期+标题，原子写；注释明确「file at Path is the source of truth」） | 基线导入 + 投影化，文件格式须兼容 |
| Todo | 按 session 存取，无独立生命周期 | 归入 Session 聚合流 |

### 2.3 既有事件概念

- `internal/goal/events.go`：运行时 `EventType`（`goal.started` / `goal.paused` / `goal.completed` / `goal.evidence.added` …）与 `EventSink func(Event)` —— **进程内通知总线**，非持久化。命名与持久化事件目录对齐。
- `internal/session/journal.go`：`JournalKind`（message / turn_started / assistant_message / assistant_partial / tool_result / turn_completed / turn_failed）—— 持久化 transcript 记录类型。

**关键区分：** 持久化事件 = 改变聚合状态或必须审计的事实；运行时 EventSink 事件 = 进程内通知。两者命名对齐，语义分离。

## 3. 参考架构：deepseek-harness（用户指定调研）

- append-only `SessionEvent` log 为唯一事实来源，seq 连续，消息历史从日志派生、不单独存储；
- 事件类型 merge-extensible（`SessionEventMap` 声明合并），核心与扩展事件共目录；
- 持久化 seam 可插拔，内存 store 为运行模型；
- surface 事件（UI 投影）与 log-only 事件（审计）分层；
- turn/step 生命周期：`turn/start → step/start → user/message → assistant/chunk* → assistant/message → tool/call* → tool/result* → step/end → turn/end`，每步模型可见事实追加后才进入下一步推导。

**Paw 差异（D5）：** 分聚合独立流（Session/Goal/Plan 各一条流），而非单一 session log；事件类型按域前缀分区。

## 4. 目标架构

### 4.1 分层与数据流

```text
命令入口（现有 runtime/loop 调用点）
      │  命令（GoalStart/GoalPause/PlanApprove/SessionBeginTurn/…）
      ▼
聚合（写模型，有状态，状态由事件流重建）
      │  命令校验（保留 CanTransition 规则表）→ 产出领域事件
      ▼
EventStore（internal/es，追加 JSONL，seq 连续，fsync）
      │
      ├──▶ 事件流（唯一事实：sessions/ goals/ plans/ 各一条 .events.jsonl）
      │
      ▼
投影器（读模型，无状态 handler）
      ├── Transcript / GoalStatus / PlanDoc / Todo / TurnMetadata
      ├── 快照（.snapshot.json，缓存非事实，加速重建）
      └── 运行时 EventSink 广播（进程内通知，供 UI/日志）
```

### 4.2 聚合边界

| 聚合 | 事件流文件 | 事件域 | 关联 |
|---|---|---|---|
| Session | `<base>/sessions/<id>.events.jsonl` | `session.*` + `todo.*` | 唯一 ID；Turn 生命周期 |
| Goal | `<base>/goals/<id>.events.jsonl` | `goal.*` | 挂在 session 上下文，聚合 ID = GoalID |
| Plan | `<base>/plans/<id>.events.jsonl` | `plan.*` | 独立于 Goal；聚合 ID = PlanID |

跨流一致性：各流独立 seq，跨流仅靠 `occurred_at` 关联；如后续需要强一致（如 goal 完成与 session turn 结束时序），引入显式关联事件（当前不设计）。

### 4.3 事件契约（`internal/es` 新包）

#### 4.3.1 信封

```json
{
  "seq": 42,
  "type": "goal.paused",
  "occurred_at": "2026-08-13T12:00:00Z",
  "schema_version": 1,
  "payload": { "reason": "no_progress", "turn_count": 7 }
}
```

- `seq`：流内连续单调递增（从 1 开始），加载时校验连续；
- `type`：注册表内类型，点分域前缀（`session.` / `goal.` / `plan.` / `todo.`）；
- `occurred_at`：UTC RFC3339，命令处理时间（非事件产生后的回填）；
- `schema_version`：payload 结构版本，默认 1；legacy 读取记 0；
- `payload`：JSON 对象，结构由类型注册表定义。

#### 4.3.2 类型注册表与校验

- `Register(spec TypeSpec)`：`TypeSpec{ Type, Decode func(json.RawMessage) (Payload, error), Validate func(Payload) error }`；
- 加载时：未知类型 → 拒绝；payload 解码失败 → 拒绝；校验失败 → 拒绝；seq 断号/重复 → 拒绝（或触发截断恢复流程，见 4.4）；
- 注册表在包 init 或显式注册（测试可用最小注册表），生产注册表由各域包（session/goal/plan）贡献。

#### 4.3.3 事件目录（全量定义，与现有代码对照）

**Session 域**（对照 `JournalKind`；`tool_call` 为新增显式记录，现含于 message 流转）：

| 事件类型 | payload | 对应现有 |
|---|---|---|
| `session.turn_started` | `{turn_id}` | `JournalTurnStarted` |
| `session.user_message` | `{message}` | `JournalMessage`（legacy message） |
| `session.assistant_partial` | `{turn_id, chunk}` | `JournalAssistantPartial` |
| `session.assistant_message` | `{turn_id, message, usage?}` | `JournalAssistant` |
| `session.tool_call` | `{turn_id, call_index, name, arguments}` | 新增显式记录 |
| `session.tool_result` | `{turn_id, call_index, result}` | `JournalToolResult` |
| `session.turn_completed` | `{turn_id}` | `JournalTurnCompleted` |
| `session.turn_failed` | `{turn_id, error}` | `JournalTurnFailed` |
| `session.turn_stopped` | `{turn_id, reason?}` | `TurnStatusStopped`（用户主动停止） |
| `session.todo_upserted` | `{snapshot}` | 新增（Todo 投影源） |

注：不引入 `step` 级事件——Paw 的 `TurnJournal` 是 turn 级（无 step 概念），避免参考 deepseek-harness 时的过度设计；如未来需要 step 粒度再扩展。
注：`session.tool_call` 独立事件**不引入**——tool call 信息保留在 `assistant_message` payload 内（与现有 `JournalKind` 一一对应），避免新增写入 API 与读取投影改动；如未来需要显式 tool-call 审计再扩展。

**Goal 域**（对照 `events.go` EventType + `GoalStatus` + `PauseReason` + `EvidenceKind/Status`）：

| 事件类型 | payload | 对应现有 |
|---|---|---|
| `goal.created` | `{goal_id, objective, budget}` | 新增（内存态无对应） |
| `goal.started` | `{task_id?}` | `EventStarted` |
| `goal.task.started` | `{task_id}` | `EventTaskStarted` |
| `goal.turn.completed` | `{turn_number}` | `EventTurnDone`（影响 no_progress 推导，持久化） |
| `goal.continued` | `{turn_number}` | `EventContinued` |
| `goal.compacted` | `{summary?, kept_events?}` | `EventCompacted` |
| `goal.paused` | `{reason}` | `EventPaused` + `PauseReason`（8 种） |
| `goal.blocked` | `{reason}` | `EventBlocked` |
| `goal.resumed` | `{}` | `EventResumed` |
| `goal.completed` | `{}` | `EventCompleted` |
| `goal.failed` | `{reason}` | `EventFailed` |
| `goal.cancelled` | `{}` | `EventCancelled` |
| `goal.evidence.added` | `{evidence_id, goal_id, kind, status, summary, scope, digest, created_at}` | `EventEvidenceAdded` + `EvidenceKind/Status` |
| `goal.evidence.stale_marked` | `{changed_files}` | 新增（`MarkStaleByChangedFiles`；声明式，Apply 按 scope 匹配，重放幂等） |
| `goal.checkpoint.saved` | `{checkpoint}` | `EventCheckpointSaved` |
| `goal.checkpoint.deleted` | `{}` | 新增（清空该 goal 全部 checkpoint；`MemoryCheckpointStore.Delete` 语义） |

不持久化的运行时通知（仅 EventSink，不改变聚合状态）：`goal.input.received`。

**Plan 域**（对照 `SessionStatus` + `PlanStatus`）：

| 事件类型 | payload | 对应现有 |
|---|---|---|
| `plan.created` | `{plan_id, title}` | 新增 |
| `plan.clarified` | `{}` | `SessionClarifying` |
| `plan.draft_started` | `{}` | `SessionDrafting` |
| `plan.draft_finished` | `{}` | → `SessionAwaitingApprov` |
| `plan.awaiting_approval` | `{}` | `SessionAwaitingApprov` |
| `plan.approved` | `{}` | `SessionApproved` + `FileStore.MarkApproved` |
| `plan.doc_updated` | `{title, path, content}` | `FileStore.Update` |
| `plan.paused` | `{reason}` | `SessionPaused` |
| `plan.failed` | `{reason}` | `SessionFailed` |
| `plan.cancelled` | `{}` | `SessionCancelled` |
| `plan.baseline` | `{doc}` | 迁移专用（一次导入） |

### 4.4 EventStore（JSONL 实现）

- **写入**：追加一行（含换行的 JSON 转义保证单行语义）+ `Sync`（复用 session `SyncPolicy` 经验）；流文件 `0600` 原子语义：append-only 无覆盖，无需 CAS。
- **读取**：按行解码 → 校验信封 → seq 连续性校验；尾部截断（最后一行不完整/损坏）→ 截断恢复（丢弃尾部坏行，保留完好前缀）；seq 断号在中部 → 报错（数据损坏，不静默修复）。
- **大小上限**：单事件 payload 上限（默认 8 MiB，复用 config/model_cache 经验），超限拒绝；超限 tool result 由调用方外置或截断（Task 4 评审定夺）。
- **快照**：每聚合每 N 事件（默认 200，可配置）写 `<stream>.snapshot.json`（聚合状态 JSON + `snapshot_seq`）；加载 = 快照 + 尾部事件重放。快照非事实，可删除重建；快照写入失败不影响事件流（下次加载全量重放）。
- **并发**：进程内 per-stream 互斥（复用 session `sessionLock` 模式）；跨进程同流并发追加不设计（单进程架构，与现状一致）。

### 4.5 聚合与命令处理（状态机保留）

通用管线：

```go
agg, err := es.Load(ctx, store, aggregateID, registry) // 快照 + 尾部重放
events, err := agg.Handle(ctx, command)                 // 校验 → 产出事件（0..n）
seq, err := store.Append(ctx, aggregateID, events...)   // 追加，seq 连续
agg.Apply(events...)                                    // 状态前进
```

#### 4.5.1 Goal 聚合

- 状态机：现有 `CanTransition` 规则表**原样保留**（`draft → planning → running ⇄ replanning / paused / blocked → completed / failed / cancelled`，终态不可再转移）；
- `Transition(to, reason)` 语义变化：校验 → 产出对应 `goal.*` 事件（reason 入 payload）→ 追加 → 状态前进；
- 推导量（由事件重放计算，非持久化字段）：`Status`、`NoProgressCount`（由 `goal.turn.completed` 计数）、`ContinuationUsed`、`Evidence` 列表、预算消耗；
- 命令集：`Create / Start / Pause(reason) / Resume / Replan / Block(reason) / Complete / Fail(reason) / Cancel / AddEvidence / SaveCheckpoint`；
- 现有 `store_memory.go` 的 `Create/Get/Update` 接口保留签名，内部改走事件流（`Update` 语义拆为具体命令，禁止裸状态覆盖）。

#### 4.5.2 Plan 聚合

- 状态机：现有 `CanTransition` 规则表**原样保留**（`clarifying → drafting → awaiting_approval → approved`，`awaiting_approval → drafting` 回退，`paused/failed/cancelled`）；
- `PlanDoc.Status`（draft/approved）保持为派生值，不直接写；
- 命令集：`Create / Clarify / StartDraft / FinishDraft / AwaitApproval / Approve / UpdateDoc / Pause(reason) / Fail(reason) / Cancel`；
- `FileStore` 转为**投影写入端**：命令 → `plan.*` 事件 → 投影生成/更新 `PlanDoc` 文件（文件名、格式、原子写行为不变）。

#### 4.5.3 Session 聚合

- `TurnJournal` 接口语义保留，实现改为产出 `session.*` 事件（legacy 记录读取兼容，见 5）；
- `todo.upserted` 由 todo 工具命令产出，Todo 投影消费。

### 4.6 投影层（无状态读模型）

| 投影 | 消费方 | 事件源 | 接口（保持兼容优先） |
|---|---|---|---|
| Transcript | UI、续跑、subagent | `session.*` | `LoadSnapshot` / `LoadResolvedHistory` / `LoadResolvedRecords` |
| GoalStatus | goal runtime、UI | `goal.*` | `Get/Status/NoProgress/Evidence…`（store 接口签名保留） |
| PlanDoc | plan 消费者、用户可见文件 | `plan.*` | `FileStore.Create/Update/Get/List/MarkApproved`（签名保留） |
| Todo | todo 工具/UI | `session.todo_upserted` | `Snapshot`（per-session） |
| TurnMetadata | UI/统计 | `session.turn_completed/failed` | `TurnMetadataStore`（签名保留） |

投影实现原则：纯函数 `Project(state, event) state`，无副作用（PlanDoc 文件写入是投影的持久化出口，由投影器在内存状态更新后调用文件写入，失败可重放）。

### 4.7 运行时事件与持久化事件的关系

- `goal/events.go` 的 `EventSink` **保留**，语义明确为「持久化事件追加成功后的进程内广播」；
- 持久化事件类型与 EventSink 的 `EventType` 命名对齐（`goal.paused` 等），避免双词汇表；
- 纯进度通知（`goal.input.received` 等不改变状态者）仅走 EventSink，不入库。

## 5. 迁移策略（直接整体替换 + 兼容读取）

1. **Session**：不物理重写旧 JSONL。旧 `Record` 由解码器按 `schema_version=0` 兼容读取（等价基线事件），新事件以统一信封追加。代码回退后旧文件仍可读（回滚路径）。
2. **Goal**：纯内存现状，无旧数据。事件溯源直接成为新持久化；崩溃恢复语义由「丢状态」变为「可重建」，需专项验证 runtime 暂停/恢复路径。
3. **Plan**：一次性基线导入——读现有 `PlanDoc` 文件 → 写 `plan.created` + `plan.baseline`（含 doc 快照）→ 旧文件保留备份 → 之后所有变更走「命令 → 事件 → 投影写 PlanDoc 文件」。迁移 dry-run 校验重建 doc 与旧 doc 逐字节一致。
4. **旧写路径清理**：所有 `status` 字段直接落盘/裸改写的代码点逐个替换为命令调用；Task 9 确认无残留。

## 6. 验证策略

- 每任务 TDD（先红后绿）、focused tests（`-count=1`）、关键包 `-race`；
- 迁移 dry-run：fixture → 事件流 → 重建 → 与旧状态 diff（PlanDoc 逐字节、session 记录逐条、goal 状态等价）；
- 崩溃恢复专项：模拟进程中断 → 从事件流重建状态一致；
- 最终：`go build ./...`、`go test ./... -count=1`、`go vet ./...`、`gofmt -d` 无输出；
- 全部本地 fixture，不连接外部服务。

## 5.1 实现偏差记录（Task 4 起）

| # | 偏差 | 原因 | 状态 |
|---|---|---|---|
| D1 | `session.tool_call` 独立事件不引入 | tool call 保留在 `assistant_message` payload 内，避免新增写入 API 与读取投影改动；与现有 `JournalKind` 一一对应 | 已定（4.3.3 注） |
| D2 | session 流 seq 保持 **0 基线**（历史语义，fork 依赖），`es.Envelope.Validate` 放宽为 `seq >= 0`；es 自身流仍从 1 分配 | 现有测试断言 seq 从 0 开始，改基线破坏兼容 | 已定 |
| D3 | `todo.upserted` 存储 API（`AppendTodoSnapshot`）已就绪；loop 层接线推迟到 Task 7 | todo 工具层无 sessionID，接线点在 loop 工具执行后处理（消费者适配） | **已完成**（`wireTodoEvents`，best-effort 不失败工具） |
| D4 | session 写入未强制 8 MiB payload 上限（保持现有行为） | 强制上限可能拒绝现有合法大 tool result；es 事件流保留 8 MiB 上限 | **已定**：保持现状，es 流上限为准 |
| D5 | plan 文档被用户外部编辑：下次 Update 时投影覆盖（与现状 FileStore 行为一致）；文档首次经 Finalize 持久化时自动基线导入 | 事件流不存在但投影文件存在 → 读文件写 `plan.baseline` 再 diff 应用 | **已定**（自动导入已实现，Task 8 dry-run 验证） |
| D6 | plan Finalize 会产生两条 approved 记录（Update diff 的 status_changed + storeSession 的 RecordSessionStatus） | 幂等无害（Apply 后状态已 approved，重复事件不改变状态） | 已接受 |
| D7 | goal 聚合不持久化纯进度事件（turn.completed/continued/task.started/compacted 仅走 EventSink） | 符合 4.7 原则（纯通知不入库） | 已定；evidence/checkpoint 在 Task 10 已事件化（见下） |
| D9 | evidence/checkpoint 由独立 store 改为 goal 聚合子状态，随聚合流持久化（`goal.evidence.added` / `goal.evidence.stale_marked` / `goal.checkpoint.saved` / `goal.checkpoint.deleted`）；`MarkStaleByChangedFiles` 无 goalID 参数，写端枚举全部 goal 流按 scope 匹配追加事件 | 消除「有状态但无落点」的独立存储；跨流一致性与 goal 状态事务化；事件声明式重放幂等 | **已完成**（Task 10） |
| D10 | 快照格式演进：goal 快照由裸 Goal JSON 升级为 `{goal, evidence, checkpoints}`；旧格式快照在 loadState 时检测（Restore 返回 `errLegacySnapshot`）→ 删除缓存 → 全量重放 | 快照是缓存非事实；旧快照缺 evidence/checkpoints 子状态，直接恢复会丢数据 | **已完成**（Task 10） |
| D8 | 崩溃恢复：torn 尾部（无换行、最后一行解析失败）在 Append 前**物理截断**（es + session 双处） | 否则 O_APPEND 会把新事件拼进损坏行，后续记录全部丢失（Task 8 发现并修复） | **已修复**（`64ad871`） |

## 7. 风险与开放问题

| 风险/问题 | 应对 |
|---|---|
| PlanDoc 用户可见文件兼容（文件名含日期+标题） | 投影契约明确；dry-run 逐字节 diff |
| session 旧 JSONL 消费者依赖 `Record` 结构 | 读取接口保留 + legacy 解码兼容（schema_version=0） |
| tool result 大 payload 导致流膨胀 | 8 MiB 上限；超限外置/截断（Task 4 评审） |
| goal 内存 → 持久化改变崩溃恢复语义 | Task 8 崩溃恢复专项；runtime 暂停/恢复路径验证 |
| plan 文档被用户外部编辑后投影覆盖 | 开放问题：投影写入前 mtime/内容冲突检测（复用 config CAS 经验），Task 6 评审定夺 |
| 分聚合独立流跨流一致性 | 当前仅 occurred_at 关联；需要时引入显式关联事件 |
| `goal.input.received` 类纯通知不入库 | 明确 EventSink 与持久化事件边界（4.7），评审确认无遗漏 |
