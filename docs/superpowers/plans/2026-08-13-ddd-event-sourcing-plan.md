# DDD Event Sourcing 迁移计划（完成记录）

**状态：** 全部任务完成（Task 0-9），实现基线 `64ad871`，最终文档提交见末尾。
**日期：** 2026-08-13
**范围：** Paw 核心运行时 —— Goal、Plan、Session/Turn、Tool 调用、Todo
**设计文档：** `docs/superpowers/specs/2026-08-13-ddd-event-sourcing-design.md`

本记录描述已评审并通过的代码，不再保留与实现矛盾的预期伪代码。

## 澄清决策（用户拍板）

| # | 决策点 | 结论 |
|---|---|---|
| D1 | 设计来源 | 仓库无现有 DDD/ES 设计文档 → 新增设计文档（`docs/superpowers/specs/2026-08-13-ddd-event-sourcing-design.md`） |
| D2 | 业务边界 | 核心运行时：Goal、Plan、Session/Turn、Tool 调用、Todo |
| D3 | 迁移策略 | 直接整体替换：统一事件契约 + 事件溯源作为唯一事实来源 |
| D4 | 事件存储 | 本地 JSONL 事件库（零新依赖） |
| D5 | 聚合边界 | 分聚合独立流：Session / Goal / Plan 各持独立事件流；Todo 归 Session 流 |
| D6 | 状态机 vs 无状态 | 写模型保留状态机聚合（状态为事件流投影）；读模型无状态投影器；参考 deepseek-harness 架构调研 |

**核心结论：状态机要留，状态落盘要废。** 事件是唯一持久化事实，状态是事件流的派生投影。

## 最终架构

- `internal/es`（新包）：统一事件信封（seq/type/occurred_at/schema_version/payload）、类型注册表（payload 解码 + 校验、未知类型拒绝）、append-only JSONL EventStore（seq 连续校验、尾部 torn 物理截断修复、8 MiB payload 上限、快照读写）、聚合加载管道（快照 + 尾部重放）与命令管线（加载 → 校验 → 追加 → 应用）。
- **Session 域**：`session.JSONLStore` 写入升级为统一信封（`session.*` 事件，与 `JournalKind` 一一对应），读取兼容 legacy Record（`schema_version=0` 语义，双格式检测）；seq 保持 0 基线（历史语义）；新增 `session.todo_upserted` 事件 + `AppendTodoSnapshot`；torn 尾部写入前物理截断。
- **Goal 域**：`goal.EventStore` 实现 `GoalStore`（Create/Get/Update/List/Delete），事件流 `goals/<id>.events.jsonl`；`CanTransition` 规则表原样保留为 Update diff 的命令校验；Update 通过字段 diff 产出领域事件（`goal.created/started/paused/blocked/resumed/replanned/completed/failed/cancelled/deleted/stats.updated`），不可变字段变更拒绝、revision 乐观锁保留；`goal.deleted` 墓碑；纯进度通知仅走运行时 `EventSink`（不入库）；goal 由纯内存变为**事件溯源持久化**。
- **Plan 域**：`plan.EventStore` 实现 `DocStore`（组合 FileStore 作投影写入端），事件流 `plans/<id>.events.jsonl`；`.md` 文件保持为投影产物（front matter 格式不变，git-friendly）；文档首次经 Finalize 持久化时自动基线导入（`plan.baseline`）；`MarkApproved`/`RecordSessionStatus` 产出 `plan.status_changed`；`SessionStatus` 状态机保留。
- **接线**：`cmd/agent` 的 goal/plan controller 使用事件溯源存储（store 为 nil 时回退内存/纯文件）；`wireTodoEvents` 把 todo 快照更新接线为 `session.todo_upserted` 事件（best-effort，失败不影响工具结果）。

## 实际文件范围

### 新增

- `internal/es/envelope.go` + `envelope_test.go`
- `internal/es/registry.go` + `registry_test.go`
- `internal/es/store.go` + `store_test.go`
- `internal/es/aggregate.go` + `aggregate_test.go`
- `internal/session/record_envelope.go` + `record_envelope_test.go`
- `internal/goal/es_events.go`、`es_state.go`、`es_store.go`、`es_store_test.go`、`es_recovery_test.go`
- `internal/plan/es_events.go`、`es_state.go`、`es_store.go`、`es_store_test.go`、`es_migration_test.go`
- `docs/superpowers/specs/2026-08-13-ddd-event-sourcing-design.md`

### 修改

- `internal/session/journal.go`（`JournalTodoSnapshot` kind）、`jsonl_store.go`（Envelope 写入、双格式读取、torn 修复、`Dir()`、`AppendTodoSnapshot`）
- `internal/goal/types.go`（`deleted` 标记）、`internal/goal/store_memory.go`（不变，保留为回退）
- `internal/plan/store.go`（`DocStore` 接口）、`internal/plan/runtime.go`（`Store` 类型改接口、`storeSession` 记录会话状态）
- `cmd/agent/tool_registration.go`（`mainTodoTool` + `wireTodoEvents`）、`goal_controller.go`、`plan_controller.go`、`interactive.go`（生产接线）
- `internal/todo/tool.go`（`OnUpsert` 回调）
- `internal/plan/runtime_test.go`（修复既有 race：`finalized` 用 `atomic.Pointer`）

## 任务与提交

### Task 0 — 设计文档落盘与事件目录定稿
- [x] `docs/superpowers/specs/2026-08-13-ddd-event-sourcing-design.md`；事件目录与 `JournalKind`/`GoalStatus`/`SessionStatus`/`TurnStatus`/`PlanStatus` 对照定稿。
- 定稿修正：`session.turn_stopped` 补入（对照 `TurnStatusStopped`）；不引入 `step` 级事件（Paw 为 turn 级）；subagent/loop/streamma 执行层状态机明确本次范围外。
- 验证：文档评审；无代码改动。

### Task 1 — `internal/es` 事件信封 + 类型注册表 + 校验
- [x] `Envelope`（seq≥0 允许 0 基线；schema_version>0 必须 occurred_at；payload 必须 JSON 对象）、`TypeSpec`（Decode/Validate）、`Registry`（重复注册拒绝、未知类型拒绝、错误可操作）。
- 提交：`e45c8e8` `feat(es): add event envelope and type registry`
- 验证：focused tests + vet + gofmt。

### Task 2 — `internal/es` JSONL EventStore
- [x] 追加写（分配连续 seq、单次 Sync）、全流读取（seq 连续校验、尾部 torn 截断、中部损坏报错）、8 MiB payload 上限、快照原子读写、per-stream 并发安全、aggregate ID 路径安全。
- 提交：`6456ed7` `feat(es): add append-only JSONL event store with snapshots`
- 验证：focused + race + vet + 全仓库。

### Task 3 — 聚合基座
- [x] `State`（Apply/Snapshot/Restore）、`Command`、`Loader.Load`（快照 + 尾部重放）、`Loader.Commit`（加载 → 命令 → 追加 → 应用 → 间隔快照）。
- 提交：`c8de5f7` `feat(es): add aggregate loader and command pipeline`
- 验证：focused + race + vet + 全仓库。

### Task 4 — Session 域接入统一信封
- [x] `recordToEnvelope`/`envelopeToRecord`/`isEnvelopeLine` 双向映射（payload 与 Record 字段无损失）；`appendRecords` 写 Envelope；`readOwnRecords` 双格式读取（legacy `Record` 兼容）；torn 尾部逻辑覆盖两种格式；`AppendTodoSnapshot` + `JournalTodoSnapshot`。
- 提交：`618552a` `feat(session): unify transcript journal onto event envelopes with legacy read compatibility`
- 验证：全部现有 session 测试保持绿（行为不变）+ 新增格式/legacy 测试；race。

### Task 5 — Goal 聚合迁移
- [x] `goal.EventStore`（事件流 + 快照重建）实现 `GoalStore`；`CanTransition` 保留为 Update diff 校验；Update diff 产出领域事件；不可变字段拒绝；revision 乐观锁；墓碑删除；快照间隔触发。
- 提交：`5c24cbe` `feat(goal): event-sourced goal store with state machine preserved`
- 验证：focused（生命周期/冲突/重启/快照等价）+ race + 全仓库。

### Task 6 — Plan 聚合迁移
- [x] `plan.EventStore` 组合 FileStore（投影写入端）；`plan.created/baseline/doc_updated/status_changed` 事件；外部写入文件首次 Finalize 时自动基线导入；`DocStore` 接口；runtime 会话状态经 `RecordSessionStatus` 记录（FileStore 不实现 → 现状保持）。
- 附带修复：既有 `TestRunFinalizesWhenDocApproved` 的 race（`atomic.Pointer`）。
- 提交：`ed828b6` `feat(plan): event-sourced plan store with file projection and baseline import`
- 验证：focused + race + 全仓库（subagent 时序 flaky 与本次改动无关，重复运行通过）。

### Task 7 — 投影层与消费者适配
- [x] `session.JSONLStore.Dir()`；`todo.Tool.OnUpsert`（best-effort）；`cmd/agent` 接线：goal/plan controller 使用事件溯源存储（nil 回退）、`wireTodoEvents` 把 todo 更新写为 session 事件。
- 提交：`c34f3ad` `feat(agent): wire goal/plan event stores and todo session events`
- 验证：`TestWireTodoEventsPersistsSessionEvent`（工具 → 事件流端到端）+ todo 单测 + 全仓库 + race。

### Task 8 — 迁移 dry-run + 崩溃恢复专项
- [x] plan 迁移 dry-run：FileStore 旧文档 → 基线导入 → 重建 → 与旧文件逐字节一致 + 迁移后 Update 正常 + 流为 `[baseline, doc_updated]`。
- [x] goal 崩溃恢复：torn 尾部（无换行）→ 截断重建 + 崩溃后继续写入；中部损坏报错；无快照全量重放等价；stale revision 冲突。
- [x] **发现并修复真实 bug**：torn 尾部未物理截断时，后续 `O_APPEND` 会把新事件拼进损坏行（该行永远无法解析，后续记录全部丢失）——es 与 session 双处 `repairTornTail` 修复。
- 提交：`64ad871` `fix(es): repair torn stream tails before append; add crash recovery tests`
- 验证：focused（含新 es torn 测试）+ race + 全仓库（subagent flaky 复跑通过）。

### Task 9 — 清理旧写路径、全量验证、文档收尾
- [x] 残留检查：所有 `Transition` 调用点均经 store 持久化（goal: Update diff；plan: RecordSessionStatus）；session 无 `Record` 直写残留。
- [x] `go build ./...`、`go test ./... -count=1`、`go test -race`（es/session/goal/plan/cmd.agent/todo）、`go vet ./...`、`gofmt -l` 全绿。
- [x] 设计文档偏差表定稿（D1-D8）；本计划文档转为完成记录。

## 最终验证

- `go build ./...` — PASS。
- `go test ./... -count=1` — PASS（subagent 存在两个与本 feature 无关的既有时序 flaky：`TestWaitToolAppliesDefaultTimeoutFromSettings`、`TestLaunchAssignsUniqueRunningPersonas`，focused 复跑通过，未修改无关代码）。
- `go test -race ./internal/es ./internal/session ./internal/goal ./internal/plan ./cmd/agent ./internal/todo -count=1` — PASS。
- `go vet ./...` — PASS。
- `gofmt -l`（全部改动文件）— 无输出。
- 全部本地 fixture，未连接外部服务。

## 设计偏差（详见设计文档 5.1）

D1 `session.tool_call` 不引入（保留在 assistant_message 内）；D2 session seq 0 基线（es 信封校验放宽，es 流仍从 1）；D3 todo 事件接线 best-effort；D4 session 写入不强制 8 MiB（es 流为准）；D5 plan 外部编辑由下次投影覆盖（与现状一致）+ 首次 Finalize 自动基线导入；D6 plan Finalize 双 approved 记录（幂等）；D7 goal 纯进度事件不入库、evidence/checkpoint 独立 store；D8 torn 尾部物理截断修复（es + session）。
