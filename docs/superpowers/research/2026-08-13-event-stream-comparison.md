# 事件流与上下文历史：Paw × deepseek-harness × astrcodey 对比

**日期：** 2026-08-13
**状态：** 调研报告（deep-research 交付）
**范围：** 三方的事件流建模、上下文历史（消息派生）、压缩、崩溃恢复、事件目录演进。不评估 UI/工具生态。

## 0. 调研 Brief（RQ）

1. Paw 当前的事件流与上下文历史机制是什么？（本地源码实证）
2. deepseek-harness（dsh，TS/Cordis）如何设计 session 事件日志与投影？
3. astrcodey（Rust code agent）如何设计事件日志、投影与压缩？
4. 三方在事件建模、上下文管理、恢复机制上的本质区别是什么？

## 1. 三方一页概览

| | **Paw**（Go，本仓库） | **deepseek-harness**（TS，25.8k★） | **astrcodey**（Rust，~104k 行/26 crates） |
|---|---|---|---|
| 定位 | 单机 TUI agent（本项目本体） | 插件化 agent harness（everything is a plugin） | 全栈 code agent 平台（TUI/Web/Tauri/ACP） |
| 事实来源 | **双轨**：`transcript.jsonl`（对话）+ 每聚合独立事件流（goal/plan） | 单 session 事件日志（`SessionEvent` 唯一事实来源） | 单 session 事件日志（`EventLog` 唯一事实来源） |
| 消息历史 | `LoadSnapshot` 派生（UI 消息 / 模型安全历史 / 恢复状态三分离） | `deriveMessages()` 从日志派生 | `SessionReadModel` 投影归约（reducer） |
| 压缩 | **只压缩内存投影，持久化日志绝不可变**；原文归档到磁盘 | 不修改日志；`compaction/*` 作为**持久化事件**记录；投影压缩 | **`TranscriptRewrite` 事件写回日志** + checkpoint |
| 崩溃恢复 | torn-tail 物理截断 + 内存 `RecoveryState`（不写回） | checkpoint-policy + `repair` **合成确定性事件写回**日志 | 流校验器 + 版本化快照（≤4 个）加速恢复 |
| 事件目录 | 固定 `RegisterEvents` registry + schema version | `SessionEventMap` 声明合并 + `ignorable` 标记 + 生成式目录 | typed enum（`deny_unknown_fields`） |

**一句话结论：三家都选了「append-only 事件日志 = 事实来源」，但在三个决策点上分道扬镳：事件载荷语义（diff vs whole-value）、压缩是否写回、恢复是否写回。**

---

## 2. 逐维度对比

### 2.1 事实来源模型：单流 vs 双轨

- **dsh**：一个 session 一个日志，header 行 + 事件行（`packages/session/session-persistence-jsonl/src/format.ts`）。**所有**事实入同一流：`turn/start|end`、`step/start|end`、`tool/call|result`、`assistant/chunk|message`、`goal/change`、`todo/write`、`approval/*`、`compaction/*`、`hook/*` 等 43 类（`packages/core/session/src/known-event-types.ts`，脚本生成）。fork = 用已有日志 seed 新 session（header 记 `parentSession`/`seedLength`）。
- **astrcodey**：同样单流：`SessionStarted`（首事件强制）、`UserInputAccepted`/`UserMessage`、`AssistantMessageCompleted`、`TurnStarted/Completed/AbortedContext`、`AgentSessionSpawned/Completed/Failed/Recycled`、`TranscriptRewrite`、`SystemPromptConfigured` 等（`crates/astrcode-core/src/event/payload.rs`）。子代理生命周期**是持久化事件**。
- **Paw**：**双轨**——`internal/session` 的 `transcript.jsonl`（`Record{Seq,Kind,TurnID,Message,ToolResult,TodoSnapshot}`，消息 + 少量元事件混合）承载对话；`internal/es` 的 per-aggregate `<id>.events.jsonl` 承载 goal/plan 领域事件。领域事件（goal/plan）与对话流分离，各有 seq。

> 差异本质：dsh/astrcodey 把「对话 + 领域 + 系统」全部放进一条可重放日志，回放即全史；Paw 的对话流与领域聚合流分离，跨流时序需要时间戳对齐（设计文档已记录为低优先级项 R5「跨流链接」）。

### 2.2 消息历史派生（投影机制）

- **dsh**：`deriveMessages()` 是显式派生函数；`assistant/message` 是派生的**权威**，原始 `assistant/chunk` 仅用于 token 级 replay 保真。投影框架化：`SessionProjectionMap` + `ProjectionDefinition{init, apply, view}` 纯同步三元组，框架驱动 apply、watermark（seq）缓存、change feed、`stateVersion` 使持久化缓存失效（`packages/session/session-projection/src/index.ts`）。
- **astrcodey**：`SessionReadModelProjection.apply → reduce(event, model)`（`crates/astrcode-session-projection/src/reducer.rs`）；错误枚举精确到 `InvalidFirstEvent`（首事件必须 `SessionStarted`）、`NonContiguousSequence`、`DuplicateSessionStarted`。读模型即投影。
- **Paw**：`session.JSONLStore.LoadSnapshot` 派生**三种视图**：`Messages`（UI 展示）、`ActiveHistory`（发给模型的**安全历史**——剔除孤儿 tool-call 组）、`RecoveryState`（未完成 turn 的恢复元数据）。goal/plan 侧是「写模型聚合（状态机 + 乐观锁）+ 无状态读取投影」。

> 差异本质：dsh/astrcodey 的投影是**纯函数式的**（事件 → 状态，无副作用），Paw 的 goal/plan 写入路径保留**命令校验状态机**（`CanTransition`），事件追加前做领域校验——这是 Paw 设计文档 2.1.1 的刻意决策（保留状态机、废除持久化状态）。

### 2.3 事件载荷语义：whole-value vs diff（**关键分歧一**）

- **dsh**：**Whole-value rule（承重规则）**：「state-carrying event MUST carry the complete post-change state, never a bare delta」——状态承载事件必须携带完整新状态（`session-projection/src/index.ts` 文档原文）。理由：投影单元转换廉价、每个服务值自描述。
- **astrcodey**：混合——`UserMessage{text, attachments}` 全量、`AssistantMessageCompleted{text}` 全量、`TurnCompleted{finish_reason}` 轻量。按事件性质取舍。
- **Paw**：**字段级 diff 事件**（设计文档 4.x）：`plan.doc_updated` 携带**全量 Content** + 变化字段（接近 whole-value）；`plan.status_changed` / goal 状态事件只携带变化字段（diff 式）；`goal.evidence.added` 携带完整 evidence。介于两者之间，但**没有像 dsh 那样把 whole-value 立为规则**——`status_changed` 是纯 delta，重放时依赖前置事件才能还原完整状态。

> 影响：dsh 的投影单元可以无状态地「看一个事件即自描述」；Paw 的 delta 事件省空间但投影必须按序重放、且无法独立校验单事件完整性。Paw 的 `plan.doc_updated` 全量 Content 实际上已满足 whole-value，但 goal 状态事件是 delta——**不一致**（这是可讨论的改进点，见 §4）。

### 2.4 压缩/上下文维护（**关键分歧二**）

- **Paw**（最保守）：多级压力阈值（soft → tool-result snip → prune → summary compaction，`internal/loop/context_pressure.go`）；**只压缩内存中的模型投影（ActiveHistory），持久化 transcript 永不重写**；被折叠的原始消息**归档到磁盘**（`internal/loop/compaction_archive.go`）；pinned prefix（system + 首条 user + 既有 summary）保持稳定。
- **dsh**：**不修改日志**，派生成本随日志增长 → 压缩是投影层缓解（dsh-compaction 插件）。但 `compaction/start|end|summary|prune` 本身是**持久化事件**（压缩发生的事实入日志）。容量事实由 **adapter 拥有**（`resolveModel` 返回 `contextWindow`），token 测量 model-agnostic（dsh-token-meter 固定 replay fold），策略按精确 `{provider, model}` 路由（`routed-model-context-and-compaction-policy.md`）。
- **astrcodey**（最激进）：**`TranscriptRewrite` 事件写回日志**——压缩时重写 provider transcript 前缀（`rewrite_transcript_for_compaction`，`crates/astrcode-session/src/session_compaction.rs`），源 seq 被记录（`InvalidTranscriptRewriteSource` 校验），随后 checkpoint。即压缩**改变事实日志**（有 `compact_persist_conflict.rs` 测试专门处理压缩与持久化的冲突）。

> 差异本质：压缩是否属于「事实」？Paw：压缩是**运行时视图优化**，事实不可变；dsh：压缩**不碰事实**但压缩动作本身是事实；astrcodey：压缩**改写事实**（recap 替换旧 transcript）。三者对「append-only 不可变性」的承诺强度依次递减。

### 2.5 崩溃恢复（**关键分歧三**）

- **Paw**：torn-tail（崩溃尾部残行）在 append 前**物理截断**（es + session 双处，D8）；未完成 turn 的恢复 = **内存派生 `RecoveryState`**（CompletedToolResults / DroppedToolCalls），**不写回日志**，重启后由 ActiveHistory + Recovery 元数据驱动模型侧修复。
- **dsh**：`repair.ts` 的 `interruptedTurnClosers`——扫描日志发现未闭合 turn，**合成确定性事件写回**：未匹配的 tool call 先补错误结果（`TOOL_NOT_STARTED` / `TOOL_OUTCOME_UNKNOWN`），再补 `step/end` + interrupted `turn/end`，时间戳复用最后真实事件（确定性）。
- **astrcodey**：`EventStreamValidator`（seq 连续 + session_id + epoch-zero 时间戳警告）+ 版本化快照（`snapshots/snapshot-<cursor>.json`，`SNAPSHOT_VERSION=4`，最多保留 4 个，temp+rename 原子写，明确「恢复加速器，不参与 seq 分配」）。

> 差异本质：**恢复是否写回日志**。Paw 恢复是「读取侧派生」（日志保持纯净，但重启后丢失部分恢复上下文）；dsh 恢复是「写回合成事件」（日志补全为平衡态，可重放，但日志含有合成事件需与真实事件区分）；astrcodey 恢复是「校验 + 快照加速」（不写回，靠快照跳过长重放）。

### 2.6 快照/缓存

| | Paw | dsh | astrcodey |
|---|---|---|---|
| 位置 | `<id>.snapshot.json`（es 缓存） | session-projection-cache（watermark + stateVersion） | `snapshots/snapshot-<cursor>.json` |
| 语义 | 缓存，可删除重建（legacy 不兼容时删缓存全量重放） | 持久化缓存，版本失效 | 恢复加速器，版本化、≤4 个、原子写 |
| seq 参与 | 不参与 seq 分配 | watermark 对齐 seq | 明确「不参与追加 seq 分配」 |

三家共识：**快照是加速器不是事实**，与事件日志主从关系一致。

### 2.7 事件目录演进

- **dsh**：`SessionEventMap` 声明合并（插件可扩展）+ 生成式 `KNOWN_SESSION_EVENT_TYPES`（脚本生成 + 验证）+ `ignorable` 标记（未知类型可跳过）/ 无标记则拒绝读取 + `SESSION_FORMAT_VERSION`（结构性变更才 bump，普通新增事件不 bump）。
- **astrcodey**：typed enum + `deny_unknown_fields`（严格拒绝未知字段）+ `SNAPSHOT_VERSION`（投影语义变更时 bump）。
- **Paw**：`RegisterEvents(registry)` 固定注册 + payload schema version（envelope 带 `SchemaVersion`）。无 ignorable 概念——未知事件类型直接拒绝。

> Paw 与 astrcodey 同为「严格拒绝」，dsh 的 ignorable 提供向前兼容（新版本写入的日志老版本可跳过非承重事件继续读）。

### 2.8 运行时通知 vs 持久化事实的边界

- **Paw**：显式双通道——`EventSink`（进程内通知，`goal.input.received`、`goal.turn.completed` 等**纯进度事件不入库**，D7）+ 持久化事件（改变聚合状态或须审计的事实）。
- **dsh**：三域边界规则——`session/*`（durable fact log，JSON-only）、`agent/*`（live runtime，携带 live Agent 句柄，拦截/恢复）、`tools/*`。**边界事件（turn/start 等）只入 session log，不镜像到 agent/* 事件**（曾经有镜像，已删除，见 event-domain-semantics note）。
- **astrcodey**：`DurableEvent` / `LiveEvent` / `StoredEvent` 信封三分（`EventEnvelope` + `Phase`），`SessionEventSink` 有序发布 + deferred 延迟发布 + 1024 有界队列（防背压）。

> 三家独立得出同一结论：**必须区分「可重放事实」与「进程内信号」**。Paw 的 D7 决策与 dsh 的域边界规则、astrcodey 的 Live/Durable 信封是同一洞见的三种表达。

### 2.9 子代理/谱系

- dsh：header 记 `parentSession`/`seedLength`/`delegationDepth`（重启后递归预算不丢）；fork = seed。
- astrcodey：`AgentSessionSpawned/Completed/Failed/Recycled` 是**持久化事件**（子代理生命周期可审计、可重放）。
- Paw：子代理是独立 worker 进程（session 天然隔离），**无持久化的父子谱系事件**（`internal/subagent` 不在迁移范围，设计文档 2.1.1 已记录）。

### 2.10 扩展性

- dsh：Cordis 插件（一切皆插件），事件目录声明合并，hook 子系统（Claude Code / Codex 桥）。
- astrcodey：SDK extension + 进程外磁盘 IPC 子进程扩展 + MCP 进程池（跨 turn 复用）+ ACP adapter。
- Paw：进程内 tool registry + skill（无事件插件机制；goal/plan 事件目录固定）。

---

## 3. 三个最重要的分歧（决策矩阵）

| 决策点 | Paw | dsh | astrcodey | 权衡 |
|---|---|---|---|---|
| **事件载荷** | 字段级 diff（部分全量） | whole-value（规则化） | 按事件类型混合 | whole-value：投影廉价/自描述，空间大；diff：空间小，投影依赖序 |
| **压缩是否写回** | 绝不（只压内存投影+归档） | 不写回（但压缩动作入日志） | 写回（TranscriptRewrite） | 写回=日志可重放为最终态但不可变承诺破裂；不写回=事实纯净但重放成本高 |
| **恢复是否写回** | 不写回（内存派生 Recovery） | 写回（合成 closers） | 不写回（校验+快照） | 写回=日志平衡、可重放、但含合成事件；不写回=日志纯净、恢复上下文有限 |

## 4. 对 Paw 的启示（按优先级，均为观察建议，未实施）

1. **（中）whole-value 规则化**：`plan.doc_updated` 已携带全量 Content（事实上的 whole-value），但 goal 状态事件是纯 delta。若未来需要「单事件自描述」或独立投影单元，可把状态承载事件统一为全量 + 变化标记；当前 diff 式在空间上更优，**不紧急**。
2. **（中）压缩动作入日志**：Paw 的压缩完全不落任何痕迹（只有进程内 EventSink 通知）。dsh 的 `compaction/*` 持久化事件提供了「压缩何时发生、fold 了什么」的可审计事实。Paw 若需要审计/恢复可见性，可在 es 或 transcript 增加轻量 `compaction/record` 事件——注意 D7 已定「纯进度事件不入库」，此建议与 D7 冲突，需要单独决策。
3. **（低）ignorable 向前兼容**：Paw 严格拒绝未知事件类型。单机工具场景可接受；若未来有插件/多版本日志共存需求，可引入 dsh 式 ignorable 标记。
4. **（低）恢复写回 vs 内存派生**：Paw 的 RecoveryState 重启后依赖 ActiveHistory 推导，dsh 的合成 closers 让日志自平衡。Paw 当前模型侧修复机制（`safeTurnHistory` + Recovery 元数据）已验证可用，无迫切需求。
5. **（确认）快照语义一致**：Paw 的 es snapshot 缓存（可删重建）与 astrcodey 的快照加速器语义一致，与 dsh 的 projection-cache 也同构——无需改动。

## 5. 证据索引

**Paw（本地仓库）**
- `internal/session/jsonl_store.go`（Record/journalState/LoadSnapshot）、`internal/session/journal.go`（JournalKind/RecoveryState/safeTurnHistory）
- `internal/es/store.go`（per-aggregate JSONL + snapshot cache）、`internal/loop/context_pressure.go`、`internal/loop/context_compaction.go`、`internal/loop/compaction_archive.go`
- `docs/superpowers/specs/2026-08-13-ddd-event-sourcing-design.md`（§2.1.1 执行层边界、§3 参考架构、§4.3.3 事件目录、D5–D8）

**deepseek-harness（GitHub，master 分支，2026-08-13 抓取）**
- `.agents/notes/implemented/architecture/2026-06-11-event-sourced-sessions.md`（日志=事实、deriveMessages、write-behind + flush checkpoint）
- `.agents/notes/implemented/architecture/2026-06-30-event-domain-semantics.md`（session/agent/tools 三域边界）
- `.agents/notes/implemented/architecture/2026-07-20-routed-model-context-and-compaction-policy.md`（adapter 容量 + per-model 压缩策略）
- `packages/core/session/src/known-event-types.ts`（43 事件目录）、`packages/core/session/src/repair.ts`（interruptedTurnClosers）
- `packages/session/session-projection/src/index.ts`（ProjectionDefinition/whole-value rule）
- `packages/session/session-persistence-jsonl/src/format.ts`（header/zstd/truncation-repair）

**astrcodey（GitHub，main 分支，2026-08-13 clone 至 /tmp/astrc-clone）**
- `crates/astrcode-core/src/event/payload.rs`（DurableEventPayload 枚举 / TranscriptRewriteReason）
- `crates/astrcode-session-projection/src/reducer.rs`（apply/reduce + 校验错误枚举）
- `crates/astrcode-session/src/session_compaction.rs`（rewrite_transcript_for_compaction + checkpoint）
- `crates/astrcode-storage/src/event_log.rs`（JSONL 追加 + EventStreamValidator）、`crates/astrcode-storage/src/snapshot.rs`（快照版本/上限/原子写）
- `crates/astrcode-session/src/session_event_sink.rs`（有序发布 + deferred + 1024 容量）

**未验证项（诚实声明）**
- astrcodey 的 `astrcode-context` crate 内部压缩算法细节未深读（仅确认 `CompactResult` 接口与写回路径）。
- dsh 的 compaction 插件源码（dsh-compaction）未逐行读（依据架构笔记 + 事件目录推断）。
- 三方的具体 token 计量实现细节未对比（dsh token-meter 固定 replay fold vs Paw estimateMessageTokens vs astrcodey 计量）。
