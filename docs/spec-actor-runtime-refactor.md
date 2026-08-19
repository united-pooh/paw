# 重构 Spec：Actor 运行时与分层解耦（loop God Package 终局方案）

| 项 | 值 |
|---|---|
| 状态 | Approved · P0–P4 完成（2026-08-18；P4 TaskActor/RegistryActor 换壳完成，构建/vet/全量测试/指定 `-race` 全绿） |
| 日期 | 2026-08-18 |
| 范围 | internal/loop、internal/task、internal/session、新建 internal/actor |
| 前置文档 | 架构评审结论（loop 扇出 11 包、Runner 40+ 字段、eventing 死包、装配根 9 元组） |

---

## 1. 问题陈述

`internal/loop` 是事实上的 God Package：`Runner` 持有 40+ 可变字段、一把 RWMutex 保护全部状态，
职责覆盖轮转、工具调度、上下文压缩、子代理、技能、todo、追踪、恢复、权限。
后续规划要求"多 actor 持久化运行"，当前结构无法承载。本 spec 定义目标架构、决策记录、
持久化协议、迁移计划与验收标准。

## 2. 目标 / 非目标

**目标**
1. G1 消灭 Runner 状态癌症：字段 ≤ 15，锁职责可陈述；
2. G2 建立可持久化 actor 运行时（单机版），支撑后台任务与子代理长期运行、崩溃可恢复；
3. G3 消息处理事务化（Journal-First + Outbox），杜绝崩溃丢消息/重放双发；
4. G4 现有功能行为零回归（golden 事件流比对）。

**非目标**
1. N1 不做分布式/多机部署（仅预留存储接口与租约位）；
2. N2 不引入外部数据库（保持本地 JSONL）；
3. N3 不更换 UI 框架、不修改 `ui.UI` 端口；
4. N4 不重写模型适配层（internal/model 不动）。

## 3. 终态架构

```
L4 编排层    Saga/TaskOrchestrator Actor（跨 actor 工作流、补偿）
L3 领域层    SessionActor(loop引擎宿主) / TaskActor / SubagentActor / TodoActor
L2 运行时    internal/actor：Ref / Context / System / 邮箱 / 分片调度 / 监督 / 激活钝化
L1 事件层    internal/es（现有）+ Outbox/Inbox 记录 + 快照（现有 Loader 扩展）
```

- 虚拟 actor：Actor = {ID, 事件流, 状态缓存, 邮箱}；执行体与身份解耦；
- 恢复模式：事件溯源状态重建（fold），**永不重执行 handler**（ADR 索引见 §4）；
- loop 引擎（hook 微内核）作为库住在 SessionActor.Receive 内部；主循环结构不变。

## 4. 决策记录（ADR）

| # | 决策 | 要点 |
|---|---|---|
| ADR-1 | 双通道 | 事实走邮箱+事件流；流式 delta 走内存订阅总线直发 UI，不持久化 |
| ADR-2 | 状态串行、执行扇出 | SessionActor 独占状态变更；工具执行 fan-out 至 executor，结果以消息回投 |
| ADR-3 | Ask 语义 | Ask=Tell+MsgID 关联回执；超时仅放弃等待；回复按 MsgID 去重；仅许向下游方向 |
| ADR-4 | 三级持久化 | Durable（逐条 fsync）/ Buffered（group-commit，turn 边界 flush）/ Ephemeral（不落盘） |
| ADR-5 | 投递语义 | actor 内 exactly-once（运行时保证）；跨 actor at-least-once + 防御性幂等（提供 ctx.Once） |
| ADR-6 | UI 不入编 | ui.UI 端口不变，经订阅总线接收显示事件 |
| ADR-7 | 恢复语义 | ToolStarted 先行落盘；已完成工具绝不重跑；进行中 LLM 放弃；落 TurnInterrupted 交用户裁决 |
| ADR-8 | 内核测试门槛 | 确定性调度器 + 崩溃注入矩阵 + 不变量属性测试；覆盖 <90% 不接 citizen |
| ADR-9 | 事件命名空间 | Envelope 增 Kind(runtime/domain)；sys.* 由运行时两段式 fold 先行消费 |
| ADR-10 | 自建运行时 | 借鉴 ProtoActor process registry；不引入其依赖 |
| ADR-11 | 歼灭条款 | citizen 迁移完成的同组 PR 必须删除旧路径；并存 ≤1 迭代 |
| ADR-12 | 分阶段价值 | Phase 1-5 全部单机；分布式仅预留接口，不预付成本 |

## 5. internal/actor 接口契约（v0 草案）

```go
type Ref interface {
    Tell(ctx context.Context, msg Msg) error              // at-least-once
    Ask(ctx context.Context, msg Msg, timeout time.Duration) (Msg, error)
}

type ActorID struct{ Type, Key string }                    // 事件流路径: {type}/{key}.jsonl

type Actor interface {
    ID() ActorID
    Receive(ctx *Context, msg Msg)
}

type Context struct{ /* 全部能力指向"自己" */ }
// ctx.Self() Ref                    自身地址
// ctx.Send(to Ref, msg Msg)         经 Outbox：先 Append(MessageSent) 再投递
// ctx.Persist(evt any, d Durability) 状态变迁落流（Durable/Buffered）
// ctx.State() S                     快照+事件 fold 的当前状态
// ctx.Once(key string) bool         幂等原语（跨重投去重）
// ctx.Schedule(after, msg Msg)      持久化定时器（重启重新武装）
// ctx.Suspend(reason)               挂起等待外部输入（human-in-the-loop）

type System struct{ /* 激活/钝化/分片/监督 */ }
// system.Tell(id, msg)              幂等激活：不在内存则从事件流加载
// 分片: hash(ActorID) → N shard（N=CPU×4），shard 内串行 ⇒ 单写者由构造保证

type Durability int
// Durable（权限/task 生命周期/todo）| Buffered（领域事件）| Ephemeral（仅 ADR-1 显示流）
```

**不变量（属性测试对象）**：
I1 任意时刻一个 Actor 至多被一个 worker 处理；I2 Outbox 落盘先于投递；
I3 每流 seq 连续单调；I4 快照可独立 fold 出等价状态；I5 sys.* 不进入领域 reducer；
I6 L2 运行时禁止 import 任何 L3/L4 领域包（CI 用 go list 依赖审计强制，双向无环由编译保证，单向约束由审计保证）。

**上帝类防线（代码评审硬标准）**：
- `System` 仅做组合，实现拆为 Router / Scheduler / Journal / Supervisor / Snapshotter，单件 < 250 行；
- `Context` 为纯能力句柄袋：零业务逻辑、字段仅指向 Self 资源、导出+未导出字段合计 ≤ 12；
- `SessionActor` 为薄壳（邮箱→引擎调用，≤ 200 行）；loop 引擎为纯库，禁止 import internal/actor；
- 引擎宿主化后，现有 runner 级测试必须继续在不启动 actor 运行时的条件下可运行。

## 6. 持久化协议

### 6.1 消息处理序列（Journal-First）

```
① Append(sys.inbox.received{msgID, payload})     [Durable]
② Receive：状态变迁经 ctx.Persist；外发经 ctx.Send（Outbox 先落盘）
③ Append(sys.inbox.done{msgID})                  [Durable]
④ 每 K 条事件 / 钝化时：快照
```

### 6.2 崩溃恢复矩阵

| 崩溃点 | 恢复所见 | 处置 |
|---|---|---|
| ① 前 | 无 | 发送方重发（at-least-once），收端 MsgID 去重 |
| ①② 间 | received 无 done | 重投消息（actor 内 exactly-once 由 ADR-5 原语保证） |
| ② 中 | 部分领域事件 | 合法：事件记录了半做事实，状态机按事件语义收敛 |
| Outbox 落盘后未投递 | MessageSent | 扫描未确认 Outbox 补发（事务性 Outbox） |

### 6.3 事件分类

- `sys.inbox.received / sys.inbox.done / sys.outbox.sent / sys.timer.registered`：Kind=runtime；
- 领域事件沿用现有 `es.Envelope.Type`（如 `tool.result`、`turn.completed`）：Kind=domain；
- `es.Loader` 扩展两段式 fold：先消费 sys.* 重建投递/定时器账本，再 fold 领域状态。

## 7. 调度与生命周期

- 激活：快照 + 尾部事件 fold（目标 < 5ms/千事件）；
- 钝化：邮箱空 + 空闲 5min → 快照 → 逐出；
- 监督：panic → 指数退避重启（快照+事件恢复）→ 窗口内 3 次 → 隔离区（死信 + 人工介入）；
- 定时器：staleTodo 轮询、task 超时收割迁移至 ctx.Schedule。

## 8. 迁移计划（绞杀者，每 Phase 独立可上线、可回滚）

| Phase | 内容 | 退出标准 | 回滚 |
|---|---|---|---|
| P0 清场 | 删 eventing/ipynb/py/hello；二进制入 .gitignore | 仓库异物清零 | git revert |
| P1 装配收敛 ✅ | buildRunner 9 元组 → AppContext{…,Close()}；plan/goal_controller 迁入 internal/ | 全部调用点编译过、现有测试绿 | ~~保留旧签名一迭代~~ 实际采用原子替换 + git revert（owner 2026-08-18 审查知情：单仓库、3 调用点同批更新、未提交前可整体回退；保留旧签名一迭代的维护成本 > 收益） |
| P2 hook 收编 ✅ | sessionLoadedHooks 泛化为 Hook 链（hooks.go，RegisterHook + 能力接口）；观察型关注点提取为 12 个内聚协作者（usage/trace/gate/skills/streamMA/compact/stateCfg/promptCtx/taskEnv/turnCtl/toolGate，各自持锁）；yolo 回调并入 toolGate 协作者。实测：Runner 字段 51→**23**，loop 测试/`-race`/全仓库测试全绿（行为等价）。遗留：tracer/todo/skill 完全 Hook 化与 yolo 决策落事件随 P3/P5 actor 化一并收益 | Runner 字段 ≤ 25；行为 golden 流一致 | hook 关闭开关（SetSessionLoadedHook 兼容糖保留，行为不变） |
| P3 actor 内核 ✅ | 新建 internal/actor（含测试三件套） | 不变量属性测试全绿、覆盖 ≥90%（实测 92.4%）、崩溃矩阵全过 | 纯新增，直接删 |
| P4 TaskActor ✅ | task/manager 换壳；StopOwnedTasks→Tell(Stop)；task 流事件化；Manager 保留 facade 配置与外部 adapter，删除 lifecycle maps/channels/cache | 后台任务行为等价；golden/崩溃恢复/metadata 兼容用例与全量门禁通过（ADR-11） | 上一个 tag |
| P5 SessionActor | loop 引擎入住；session JSONL 与 es 合流；权限门→Suspend+Decision 消息；goal/plan 会话恢复（见 §13.5：fold 重建 activeGoalID/activePlanID） | golden 事件流等价；Runner（Engine）字段 ≤15；context_recovery 语义保持 | feature flag 双跑一迭代 |

Phase 6（可选、非本 spec 承诺）：存储接口多实现（SQLite/PG）+ 激活租约 → 多机。

## 9. 测试策略

1. 内核三件套（ADR-8）：确定性调度器（虚拟时钟）、崩溃注入（协议 ①-④ 每步 SIGKILL）、不变量属性测试（I1-I5）；
2. citizen 级：golden 事件流比对（迁移前后同输入产生等价事件序列）；
3. 集成：现有 59k 行测试全绿为准入门槛；
4. 混沌：钝化/激活循环 × 消息风暴下的内存与 fd 稳定性。

## 10. 性能预算

| 指标 | 预算 |
|---|---|
| Durable 消息摊销开销 | < 1ms（本地 NVMe，group commit） |
| 激活延迟 | < 50ms（快照 + ≤1k 尾事件） |
| 流式 delta 端到端 | 不劣于现状 ±10%（Ephemeral 通道） |
| 常驻内存 | 热 actor ≤ 512KB 状态 + 邮箱 256 消息上限 |

## 11. 风险登记册

| 风险 | 等级 | 缓解 |
|---|---|---|
| 内核并发 bug 全局放大 | 高 | ADR-8 门槛 + race detector 全量 + 不变量属性测试 |
| 双体系并存失控（eventing 重演） | 高 | ADR-11 歼灭条款，CI 检查旧路径引用 |
| 恢复语义与现网行为漂移 | 中 | golden 事件流 + context_recovery 等价测试先行 |
| Outbox 补发造成重复副作用 | 中 | MsgID 去重 + ctx.Once + 接收端幂等审计清单 |
| 迁移周期过长士气损耗 | 中 | 每 Phase 独立价值；P1/P2 无新抽象即可完成 |

## 12. 验收标准

1. `go vet`/`race`/现有测试 100% 绿；
2. Runner（终名 Engine）导出字段 ≤ 15，内部可变字段 ≤ 15，包扇出 ≤ 8；
3. internal/actor 覆盖率 ≥ 90%，I1-I6 属性测试进入 CI；
4. 崩溃注入矩阵（SIGKILL × 4 协议步 × 3 actor 类型）全部恢复正确；
5. golden 事件流等价（P4、P5 各一组）；
6. 性能预算全部达标（基准脚本入库 scripts/）。

## 13. 开放问题（需 owner 拍板）

1. SessionActor 的用户输入消息是否 Durable（影响离线 headless 恢复语义）；
2. 【已落实，2026-08-18】TaskActor/RegistryActor 直接使用 `internal/actor` 分片调度器；Host 当前固定单 shard，Process 句柄仅保存在 ephemeral Host table；
3. P5 双跑 feature flag 的默认开向（建议默认旧路径，一个迭代后翻转）；
4. 时间盒：P0-P5 建议跨度 ≤ 2 个迭代，超时触发 spec 复审；
5. 【已拍板 2026-08-18，Owner 选 B】goal/plan 的会话恢复：现状为 MVP 已知限制（goal/plan 控制器固化初始 sessionID，/resume 后不跟随；goal.Store 按 sessionID 查询能力已存在但未接线）。决定推至 P5：SessionActor 激活时经事件流 fold 重建 activeGoalID/activePlanID，goal=按 SessionID 找回最新未终结 goal，plan=找回最近未终结文档的交互现场。在 P5 落地前，/resume 后新建 goal 仍归旧会话（数据不丢失，仅归属不跟随）。

## 14. 附：与评审发现的对应关系

| 评审发现 | 由本 spec 哪项消解 |
|---|---|
| P0-1 loop God Package | P2 hook 收编 + P5 Engine 塌缩 |
| P0-2 装配根 9 元组 | P1 AppContext |
| P0-3 eventing 死包 | P0 清场 + P3 正规运行时取而代之 |
| P1-4 ui 端口发胖 | ADR-6 订阅总线（端口维持现状，后续独立 spec） |
| P1-5 session 硬编码领域 | P5 事件合流 + Kind 命名空间 |
| P1-6 goal/plan 双胞胎 | 不在本 spec 范围；P4 后提取聚合骨架（另立 spec） |
