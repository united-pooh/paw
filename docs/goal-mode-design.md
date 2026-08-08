# Paw Goal Mode 设计

- 状态：提案，待实现
- 范围：会话级 Goal、持久化 Goal、自动续行、Plan/Todo、完成证据与风险暂停
- 相关调研：`docs/long-running-agent-auto-continue-research.md`
- 相关设计：`docs/long-running-agent-auto-continue-design.md`

## 1. 摘要

Paw 当前已经有任务级 Auto-Continue：`CompletionGate` 根据 Todo、预算和无进展计数决定 `complete | continue | compact | pause`，`TaskOrchestrator` 负责跨 turn 执行，Todo Broker 负责状态传播，session journal/recovery 负责历史和中断恢复。

但现有语义仍然是“Todo 尚未完成时继续若干轮”，还不是一个可长期运行、可恢复、可验证的 Goal 模式。本方案不重写现有 Runner，而是在其上增加 Goal Runtime：

```text
Goal Runtime
  = Goal 生命周期
  + 两层 Plan/Todo
  + Completion Gate
  + Verification Evidence
  + Risk-Pause Policy
  + Durable Checkpoint
  + 现有 TaskOrchestrator
```

目标不是无限运行，而是在三种状态间可靠切换：

```text
已完成                    -> 明确结束
未完成且可推进              -> 自动继续
未完成但阻塞/有风险/无进展  -> 暂停并保留恢复信息
```

## 2. 当前状态与缺口

### 2.1 已有能力

| 能力 | 当前实现 | 作用 |
|---|---|---|
| 单 turn 执行 | `Runner` | 执行一次模型/工具闭环 |
| 跨 turn 续行 | `TaskOrchestrator` | 根据 evaluator 继续或暂停 |
| 完成判断 | `CompletionGate` | 检查 Todo、预算、无进展 |
| Todo 状态 | `todo.Broker` | 发布、订阅和读取最新快照 |
| 会话恢复 | session journal / recovery | 恢复消息、工具结果和 Todo 投影 |
| 上下文维护 | context pressure/compaction | 压缩后继续运行 |
| UI 状态 | Bubble Tea Todo 投影 | 展示当前 Todo 和自动续行通知 |

### 2.2 主要缺口

1. 没有独立的 Goal 实体、生命周期和持久化 checkpoint。
2. Auto-Continue 主要依赖 Todo，不能表达验收标准、阶段依赖和长期目标。
3. Todo 没有和 Plan 阶段建立稳定关系。
4. “模型说完成”与“任务可验收完成”尚未完全分离。
5. 没有统一的验证证据模型，测试结果可能过期后仍被误用。
6. 暂停原因、恢复条件和重规划原因没有结构化表示。
7. 重启后没有明确的 `ready_to_resume` / `paused` Goal 恢复语义。

## 3. 与 Codex CLI、Claude Code、OpenCode 的差异

### 3.1 结论边界

Codex 和 OpenCode 的相关循环、工具生命周期与 compaction 行为可以从公开源码核验。Claude Code 的完整主循环在当前可见 sourcemap 中无法确认，因此以下 Claude 部分只采用可确认的 SDK/工作流实践，不断言其未公开内部实现。

### 3.2 能力矩阵

| 能力 | Codex CLI | Claude Code | OpenCode | Paw 当前 | Paw Goal Mode |
|---|---|---|---|---|---|
| 单轮模型执行 | `SessionTask` / `run_turn` | SDK/CLI 工作流 | Session processor | Runner | 保留 Runner |
| 任务与 turn 分层 | 明确 | 宿主/工作流侧实现 | Session + processor | TaskOrchestrator 初步具备 | Goal > Task > Turn |
| 普通 response 后继续 | pending input、follow-up、stop hook | 通常由工作流/宿主控制 | processor 返回 `continue` | Todo 未完成时继续 | Completion Gate 决策 |
| 工具生命周期 | task/turn 管理 | 权限与工具工作流 | tool call cleanup、aborted 状态 | journal/recovery 基础 | 复用并纳入 Goal checkpoint |
| compaction 后继续 | turn 内 rollover | continuation summary 可确认 | synthetic continuation message | context maintenance 基础 | compaction 是可恢复状态 |
| Doom loop 防护 | 预算、hook、状态边界 | 权限/人工边界 | 同工具同输入阈值 | 无统一 Goal 级规则 | progress hash + 重复调用检测 |
| Plan | 任务/工作流隐式或外部 | 多为模型工作流隐式 | agent/subtask 结构 | 无 Goal Plan | 显式 PlanStep |
| Todo | 任务状态/工作流 | 常用工作清单 | session/agent 状态 | Broker + UI 恢复 | 当前 PlanStep 的短期队列 |
| 完成证据 | 工具/turn 结果 | 测试和用户判断 | 工具结果/状态 | 尚无统一模型 | Evidence Gate |
| 权限边界 | policy/session | 强权限确认 | Permission Service | 工具安全边界 | risk-pause，不绕过现有策略 |
| 持久化恢复 | session/task 生命周期 | session/SDK 能力 | DB/session 状态 | journal/recovery | Goal checkpoint + control events |

### 3.3 可借鉴的核心设计

- **Codex**：区分 Task、Turn 和 sampling loop；pending work、stop hook 和 compaction 都是 turn 结束后的再次判断点。
- **Claude Code**：权限是独立边界；Goal 只能请求继续，不能自动提升权限。跨上下文恢复需要结构化 continuation summary。
- **OpenCode**：处理器明确返回 `continue | compact | stop`；工具需要 cleanup；重复工具调用要触发 doom-loop 防护。

## 4. Goal 模型

### 4.1 生命周期

```text
draft -> planning -> running
running -> completed
running -> paused
running -> replanning
running -> failed
running -> cancelled
replanning -> running
paused -> running
```

`paused` 不等同于 `failed`：它表示存在可恢复状态，但需要预算、权限、用户输入、重规划或人工确认。

建议类型：

```go
type GoalStatus string

const (
    GoalDraft      GoalStatus = "draft"
    GoalPlanning   GoalStatus = "planning"
    GoalRunning    GoalStatus = "running"
    GoalPaused     GoalStatus = "paused"
    GoalReplanning GoalStatus = "replanning"
    GoalCompleted  GoalStatus = "completed"
    GoalFailed     GoalStatus = "failed"
    GoalCancelled  GoalStatus = "cancelled"
)

type Goal struct {
    ID                 string
    SessionID          string
    Objective          string
    AcceptanceCriteria []string
    Status             GoalStatus
    PlanID             string
    ContinuationUsed   int
    NoProgressCount    int
    CreatedAt          time.Time
    UpdatedAt          time.Time
}
```

### 4.2 两种作用域

#### Session Goal

通过 `/goal start <目标>` 创建，立即绑定当前 session。适用于一次交互中的长任务。session 结束时保留状态，但不应在重启后无提示自动执行高风险动作。

#### Persistent Goal

将 Goal 控制事件和 checkpoint 写入 session journal 或独立 Goal Store。适用于跨 turn、跨 context、跨进程恢复。默认重启后恢复为 `paused`/`ready_to_resume`，用户显式 `/goal resume` 后继续。

## 5. Plan 与 Todo：两层混合

### 5.1 Plan 是稳定层

Plan 表达长期阶段和依赖，不应因为每个工具结果而频繁重写。

```go
type Plan struct {
    ID        string
    GoalID    string
    Version   int
    Steps     []PlanStep
    Current   string
    CreatedAt time.Time
    UpdatedAt time.Time
}

type PlanStep struct {
    ID                 string
    Title              string
    Description        string
    DependsOn          []string
    Status             PlanStepStatus
    AcceptanceCriteria []string
    Verification       []VerificationSpec
}
```

PlanStep 状态：`pending | ready | running | blocked | completed | skipped | failed`。

### 5.2 Todo 是执行层

复用现有 `todo.Snapshot` 和 `todo.Broker`。每个当前 PlanStep 拥有一个 Todo snapshot，Todo 负责当前阶段的短期操作队列：

```text
Goal
  └── Plan
       ├── Step 1
       │    └── Todo snapshot
       ├── Step 2
       │    └── Todo snapshot
       └── Step 3
            └── Todo snapshot
```

简单任务可以使用隐式单步 Plan：

```text
Goal -> implicit PlanStep -> Todo
```

### 5.3 何时重规划

仅在以下情况触发 `replanning`：

- 当前 PlanStep 的前置条件失效；
- 工作区变化使步骤不可执行；
- 连续无进展达到阈值；
- 验证失败无法通过当前步骤修复；
- 用户明确要求改变目标或约束。

重规划必须保留原 Plan 版本、触发原因和已完成步骤，不能静默覆盖执行历史。

## 6. Completion Gate 与完成证据

### 6.1 完成条件

```text
Goal completed =
    所有必需 PlanStep completed
    AND Todo 无 pending/in_progress
    AND Acceptance Criteria 满足
    AND 必需 Verification 全部通过
    AND 没有高风险阻塞
```

模型自然结束只是候选结果，不是完成依据。

### 6.2 Evidence

```go
type EvidenceKind string

const (
    EvidenceTestPassed       EvidenceKind = "test_passed"
    EvidenceBuildPassed      EvidenceKind = "build_passed"
    EvidenceLintPassed       EvidenceKind = "lint_passed"
    EvidenceCommandSucceeded EvidenceKind = "command_succeeded"
    EvidenceReviewPassed     EvidenceKind = "review_passed"
    EvidenceFileChanged      EvidenceKind = "file_changed"
    EvidenceUserApproved     EvidenceKind = "user_approved"
)

type Evidence struct {
    ID        string
    GoalID    string
    StepID    string
    Kind      EvidenceKind
    Command   string
    Status    string
    Summary   string
    Scope     []string
    Digest    string
    CreatedAt time.Time
    Stale     bool
}
```

Evidence 必须关联产生它的命令、工具调用或用户批准，并保存作用范围和摘要。若证据覆盖的文件随后发生变化，则标记为 `stale`，不能继续作为完成证据。

## 7. Auto-Continue 规则

### 7.1 现有行为

当前 `CompletionGate` 的主要规则是：

```text
有 pending/in_progress Todo
  + auto-continue enabled
  + 未达到预算
  + 未达到无进展阈值
  -> continue
```

默认配置是 `BaseBudget=2`、`AbsoluteMax=12`、`MaxNoProgress=2`。这应作为普通 session 的兼容行为保留。

### 7.2 Goal 行为

Goal Runtime 在每个 turn 后统一评估：

```text
Goal 未完成
  + 可执行下一步
  + 没有高风险审批需求
  + 未达到 Goal 预算
  + 存在可验证进展或允许一次重规划
  -> continue
```

建议 Goal 预算独立于单次 Runner：

```go
type GoalBudget struct {
    MaxTurns          int
    MaxToolCalls      int
    MaxWallTime       time.Duration
    MaxContinuations  int
    MaxNoProgress     int
}
```

“持续 auto-continue”只表示在 Goal 未完成且策略允许时自动唤醒，不表示移除硬上限。硬上限到达后必须 `paused`，并说明具体原因。

### 7.3 Continuation prompt

续行消息必须带状态，不发送无上下文的“继续”：

```text
你正在继续执行同一个 Goal，不要重新开始，也不要重复已经完成的工作。

目标：{objective}
当前阶段：{step}
已完成：{completed}
未完成：{pending}
最近修改：{changed_files}
最近验证：{verification}
暂停/续行原因：{reason}

先检查当前状态，执行下一项最有价值的工作。只有目标满足验收条件、需要用户澄清/授权，或无法取得进展时才停止。
```

## 8. Risk-Pause 策略

用户选择的默认策略是“风险即暂停”。建议统一原因枚举：

```go
type PauseReason string

const (
    PausePermissionRequired PauseReason = "permission_required"
    PauseDangerousCommand    PauseReason = "dangerous_command"
    PauseNoProgress          PauseReason = "no_progress"
    PauseBudgetExhausted     PauseReason = "budget_exhausted"
    PauseVerificationFailed  PauseReason = "verification_failed"
    PauseBlocked             PauseReason = "blocked"
    PausePlanStale           PauseReason = "plan_stale"
)
```

必须暂停的场景包括：

- 现有工具权限系统拒绝或要求审批；
- 删除、批量覆盖、发布、提交、推送或外部写入等高风险动作；
- 需要用户选择或澄清；
- 连续无进展或重复失败；
- 达到预算、时间或工具调用上限。

Goal Runtime 不得绕过既有工具安全边界，也不得通过改写命令规避权限检查。

## 9. 持久化与恢复

### 9.1 控制事件

在现有 session journal 机制上增加结构化 Goal 事件：

```text
goal.created
goal.started
goal.plan_created
goal.plan_revised
goal.step_started
goal.step_completed
goal.evidence_added
goal.continued
goal.compacted
goal.paused
goal.resumed
goal.completed
goal.failed
goal.cancelled
```

### 9.2 Checkpoint

```go
type GoalCheckpoint struct {
    GoalID            string
    SessionID         string
    Status            GoalStatus
    Objective         string
    PlanVersion       int
    CurrentStep       string
    TodoSnapshot      todo.Snapshot
    EvidenceIDs       []string
    ContinuationUsed  int
    NoProgressCount   int
    LastDecision      CompletionDecision
    ProgressHash      string
    PauseReason       PauseReason
    CreatedAt         time.Time
}
```

恢复流程：

```text
加载 Goal checkpoint
  -> 加载最新 Plan/Todo/Evidence
  -> 重新采集当前工作区和验证状态
  -> 将过期 Evidence 标记 stale
  -> 重新评估预算和风险
  -> paused/ready_to_resume
  -> 用户显式 /goal resume
```

当前工作区是事实来源，旧 checkpoint 不得覆盖工作区。

## 10. CLI 与 UI

建议命令：

```text
/goal start <objective>
/goal status
/goal plan
/goal evidence
/goal pause
/goal resume
/goal replan
/goal retry
/goal stop
/goal budget
```

状态展示至少包括：目标、当前 PlanStep、Todo 未完成数、续行次数/上限、最近验证、Evidence 数量、暂停原因和恢复命令。

## 11. 实施路线

### Phase 0：观测

不改变行为，记录：

- goal/task/turn ID；
- Todo pending/in_progress 数；
- 工具调用和错误数；
- 工作区修改摘要；
- 最近验证结果；
- continuation 次数；
- progress hash；
- terminal decision。

### Phase 1：Goal Runtime MVP

- 新增 Goal、GoalStatus、GoalBudget、PauseReason 类型；
- `/goal start/status/pause/resume/stop`；
- 复用 `TaskOrchestrator` 执行多个 turn；
- 复用当前 `CompletionGate`；
- Todo 未完成时自动续行；
- 预算和无进展时暂停。

### Phase 2：Evidence Gate

- 引入 Evidence 和 VerificationSpec；
- 测试/构建/命令结果写入 evidence；
- 完成前执行 Todo + Evidence 双门禁；
- 文件变更后使相关 evidence stale。

### Phase 3：Durable Goal

- 将 Goal 控制事件写入 journal；
- 保存 GoalCheckpoint；
- 重启后恢复为 paused/ready_to_resume；
- `/goal resume` 恢复执行。

### Phase 4：Plan/Replan

- 显式 PlanStep 和依赖；
- 当前步骤绑定 Todo snapshot；
- Plan 版本化；
- 检测 stale plan 并保留历史后重规划。

### Phase 5：后台 Goal（可选）

- detach/attach；
- 后台 Goal 列表；
- deadline 和资源配额；
- 外部通知与更完整指标。

## 12. 验收标准

1. Goal 有 session 级和持久化两种作用域。
2. Goal 未完成且可推进时能自动继续。
3. Todo 完成但缺少必需验证时不能宣告完成。
4. 测试/构建失败时能继续修复，连续失败后暂停。
5. 高风险工具调用不会被自动重试绕过权限。
6. 达到预算、无进展或计划失效时会暂停并说明原因。
7. 暂停 checkpoint 能恢复目标、Plan、Todo、Evidence 和下一步。
8. compaction 不会误报完成，而是作为可恢复的继续状态。
9. 已完成 Goal 不会无意义继续修改。
10. Goal Runtime 不破坏普通 Runner 的现有 Auto-Continue 兼容行为。

## 13. 不采用的方案

- 只修改 system prompt：无法处理权限、异常、恢复和证据。
- 每次自然结束都无条件发送“继续”：会制造 doom loop。
- 只提高 `maxToolRounds`：解决不了模型自然结束后的任务级提前停止。
- 把所有 pending Todo 强制转成继续：无法区分工作、阻塞、等待外部条件和用户澄清。
- 第一版对每次结束都调用第二个 LLM 评审：成本和误判过高，应先使用确定性信号。

## 14. 推荐决策

采用“增量 Goal Runtime”而非重写：

```text
Runner                 保持单 turn 执行
TaskOrchestrator       保持跨 turn 续行内核
CompletionGate         扩展为 Goal 完成门禁
Todo Broker            作为当前阶段执行状态
新增 Goal Runtime      管理生命周期、Plan、Evidence、Policy、Checkpoint
Session Journal        持久化 Goal 控制事件
```

这条路线可以先交付可关闭、可观测的 Goal MVP，再逐步增加持久化和复杂 Plan，不会把现有稳定的工具循环和 UI 恢复逻辑一次性重写。

## 15. Jina 一手源码复核：OpenCode 与 Codex CLI 的 Plan/Goal 真实边界

本节基于 2026-08-07 通过 Jina 抓取的上游源码复核，重点区分“Plan 展示/执行模式”和“Goal 持久化/完成契约”。结论是：OpenCode 与 Codex CLI 都提供 Plan 能力，但二者都没有直接等价于本方案的独立、长期、证据门禁式 Goal 实体。

### 15.1 OpenCode：Plan 是一种受权限约束的工作模式，不是长期 Goal

已核验的关键实现：

- `packages/opencode/src/agent/agent.ts`
  - `plan` 是内置 primary agent。
  - Plan agent 禁止普通 `edit`，只允许写入计划目录中的计划文件。
  - `build` agent 允许实际编辑，并允许 `plan_enter`。
  - `plan_exit` 在 plan agent 中可用，`plan_enter` 在 build agent 中可用。
- `packages/opencode/src/tool/plan.ts`
  - `plan_exit` 读取当前计划文件路径。
  - 通过 `Question.Service` 请求用户批准。
  - 用户选择 Yes 后创建 synthetic user message，切换到 `build` agent。
  - synthetic message 内容是“计划已批准，可以编辑文件，执行计划”。
- `packages/opencode/src/session/processor.ts`
  - 每次模型 stream 结束返回 `compact | stop | continue`。
  - 工具调用有 pending/running/completed/error 生命周期。
  - 最近三次同工具同输入会触发 `doom_loop` 权限询问。
- `packages/opencode/src/session/compaction.ts`
  - compaction 是独立 session 流程，并生成摘要。
  - compaction 后通过 plugin hook 决定是否自动继续。
  - 自动继续时写入 synthetic user message，但允许模型在没有下一步或需要澄清时停止。

OpenCode 的实际模型是：

```text
Plan Agent（只读/写计划文件）
  -> plan_exit
  -> Question：用户批准？
  -> synthetic user message
  -> Build Agent（执行文件修改）
```

因此 OpenCode 的 Plan 解决的是“先规划再执行”和“权限隔离”，而不是：

- 跨进程可恢复的 Goal 状态机；
- 多阶段依赖图；
- Todo + 验证证据的强完成门禁；
- 一个 Goal 可挂载多个 session/task 的生命周期。

### 15.2 Codex CLI：Plan 是 turn item/用户可见状态，Goal 更接近 Session Task

已核验的关键实现：

- `codex-rs/core/src/tasks/regular.rs`
  - `RegularTask` 承载任务级生命周期。
  - 外层循环调用 `run_turn`。
  - 只有 `input_queue` 没有 pending input 时，RegularTask 才返回。
  - 因而 Codex 的 task 不是单个模型 response；pending input 可以让任务继续。
- `codex-rs/core/src/session/turn.rs`
  - turn 内是 sampling loop。
  - 模型请求工具时执行工具并继续 sampling。
  - 没有工具且模型结束后，会检查 pending input、token 状态、auto compact 和 stop hooks。
  - stop hook 可以注入 `HookPrompt`，把“应该结束”转成继续。
  - 上下文 rollover/auto compact 是 turn 内状态迁移，而不是任务完成。
- `codex-rs/protocol/src/items.rs`
  - `TurnItem::Plan(PlanItem)` 是协议中的可展示 Plan item。
  - `PlanItem` 只有 `id` 和 `text`，它不是结构化 PlanStep、依赖图或完成证据。
  - 同一协议也把 `AgentMessage`、`CommandExecution`、`FileChange`、`ContextCompaction` 等作为 turn items 记录。

Codex 的实际模型更接近：

```text
SessionTask
  -> 多个 Turn
       -> 多次 sampling/tool loop
       -> pending input / stop hook / compact 后继续
  -> 无 pending work 后结束
```

Codex 的 PlanItem 主要承担模型计划的展示、协议记录和 UI 反馈；它不能单独证明任务完成。Codex 的强项是 Task/Turn/Step 生命周期、用户输入队列、stop hook、context rollover 和事件记录，而不是一个公开的 Goal/AcceptanceCriteria 数据模型。

### 15.3 精确差异矩阵

| 维度 | OpenCode | Codex CLI | Paw 当前 | Paw Goal Mode 设计 | 结论 |
|---|---|---|---|---|---|
| Plan 的本质 | plan agent + 计划文件 | `TurnItem::Plan` 文本事件 | Todo 快照/任务续行 | 版本化 Plan + PlanStep | Paw 应吸收两者，但不能把 Plan 当 Goal |
| 规划与执行关系 | plan/build agent 切换 | 同一 SessionTask 内由模型输出 PlanItem | Runner/Todo 混合 | Plan 阶段绑定当前 Todo | OpenCode 的权限隔离值得直接借鉴 |
| 是否需要用户批准 | `plan_exit` 明确询问 Yes/No | stop hook/输入队列可介入，但 PlanItem 本身不负责批准 | 风险策略待 Goal Runtime 接入 | risk-pause；高风险或计划切换暂停 | 批准是 Policy，不应由 Plan 数据结构隐式承担 |
| Plan 持久化 | 计划文件路径/Session 存储 | turn/session item 与历史 | journal 可恢复 Todo | Goal checkpoint + Plan 版本 + journal 事件 | Paw 比两者更明确地保存 Plan 版本和恢复点 |
| Plan 结构 | 文件内容，结构由模型决定 | `id + text` | Todo items | step、依赖、验收、验证 | 不要照搬 Codex 的扁平 PlanItem |
| Goal 实体 | 未见独立长期 Goal | `SessionTask` 是最接近的运行时任务 | 无独立 Goal | Goal  Task  Turn | Paw 的独立 Goal 是新增价值 |
| 自动续行 | processor `continue`；compaction hook 可关闭 | pending input、model follow-up、stop hook、compact | Todo + budget + no-progress | Goal Gate + Policy + Evidence | 续行应由 Runtime 决定，不只看 Plan/Todo |
| 完成判定 | stream/permission/session 状态 | sampling 完成 + hook + pending input 为空 | Todo 未完成则继续 | Plan + Todo + Acceptance + Evidence | Evidence Gate 是 Paw 的增强点 |
| 无进展保护 | doom loop 最近 3 次同输入 | 依赖 task/turn 边界、hook、预算等 | progress hash | progress hash + 同工具签名 + replan/pause | 采用 OpenCode 的明确三次阈值更可操作 |
| 上下文压缩 | summary agent + synthetic continuation | inline auto compact/rollover | context maintenance | compact 事件 + checkpoint 后重新评估 | compaction 不能直接完成 Goal |
| 工具权限 | agent permission ruleset，plan/build 不同 | policy/session/tool router | 工具安全机制 | risk-pause，禁止绕过权限 | Goal 不得提升权限 |
| 子任务/多代理 | general subagent、agent mode | collaboration tools/agent threads | StreamMA 另有图运行时 | 可后续映射为 PlanStep | 先不把子任务等同于 Goal |

### 15.4 对当前设计的必要修订

基于上述复核，原设计需要做四个精确化调整：

1. **Plan 不应默认要求完整、稳定且一次生成。**
   - OpenCode 的计划本质上是可编辑计划文件，并在执行前由用户批准。
   - Paw 应支持 `PlanDraft -> PlanApproved -> PlanExecuting`，简单任务仍可跳过显式审批。
2. **Goal 与 Plan/Task 必须严格分层。**
   - Goal：用户目标、验收条件、全局预算和长期生命周期。
   - Plan：实现策略和阶段依赖，可版本化、可重规划。
   - Task：一次可运行的执行实例。
   - Turn：一次模型/工具闭环。
   - Plan 不能直接等价 Goal，Todo 也不能直接等价 Plan。
3. **新增 Plan Approval Policy，而不是把所有 Plan 都 risk-pause。**
   - 只读调研、低风险小修改：自动进入执行。
   - 计划包含高风险动作、外部写入或超出原目标：暂停并询问。
   - 借鉴 OpenCode 的 plan/build 权限分离，但继续使用 Paw 现有工具权限系统。
4. **补充“用户输入队列”作为 Goal 的外部推进信号。**
   - 借鉴 Codex `input_queue.has_pending_input`：运行中的 Goal 可接收用户 steer、批准或澄清。
   - 用户输入不是普通 Todo，也不应污染 Plan 版本；应作为 `GoalInput`/事件记录。

### 15.5 修订后的推荐状态机

```text
Goal draft
  -> planning
      -> plan_draft
      -> plan_approved（低风险可自动批准，风险动作需用户批准）
  -> executing
      -> Task running
          -> Turn / tool loop
          -> Completion Gate
              -> continue
              -> compact
              -> wait_user_input
              -> replan
              -> completed
              -> paused
  -> completed / paused / failed / cancelled
```

Plan 侧单独维护：

```text
PlanDraft -> PlanApproved -> PlanExecuting -> PlanCompleted
                         \-> PlanStale -> PlanRevised
```

### 15.6 实施优先级调整

相较原路线，建议将以下能力提前：

1. **Plan mode / Build mode 权限分离**：先限制规划阶段的编辑范围，避免计划阶段直接修改业务文件。
2. **Plan approval checkpoint**：记录用户 Yes/No、计划版本、批准时间和批准前后的权限模式。
3. **GoalInput queue**：支持运行中用户输入、批准、暂停和澄清，并持久化为控制事件。
4. **PlanItem/PlanStep 事件化**：将计划变化写入事件，而不是只保存最终快照。
5. **Evidence Gate**：仍是最终完成门禁，不能因为 Plan approved 或 Todo 全部完成而直接完成。

建议的新优先级：

```text
P0  Plan/Build 权限边界 + Goal/Task/Turn 分层
P1  GoalInput queue + plan approval + continue/compact/stop 状态
P2  Todo 绑定 PlanStep + Evidence Gate
P3  durable checkpoint + Plan 版本/重规划
P4  后台 Goal、子任务和多代理
```

### 15.7 最终判断

OpenCode 最值得借鉴的是：

```text
Plan mode（只规划）
  -> plan_exit
  -> 用户批准
  -> Build mode（执行）
```

Codex CLI 最值得借鉴的是：

```text
SessionTask
  -> Turn sampling loop
  -> pending input / stop hook / compaction
  -> 继续或结束
```

Paw 不应照搬二者的 Plan 表面形态，而应组合其控制流优势：

```text
Paw Goal
  -> PlanDraft / PlanApproved / PlanExecuting
  -> Task/Turn 执行
  -> GoalInput queue
  -> Completion Gate + Evidence
  -> risk-pause / resumable checkpoint
```

这样 Paw 的 Goal Mode 才同时具备：OpenCode 的规划-执行安全边界、Codex 的可持续 turn 生命周期，以及当前方案独有的验证证据与持久化 Goal 语义。
