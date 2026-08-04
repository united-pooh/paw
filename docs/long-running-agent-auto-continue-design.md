# Paw 长程任务自动续行与任务级恢复设计

- 状态：已批准设计，方案 A 待实现
- 日期：2026-08-04
- 范围：先在现有 Runner 中实现 Completion Gate MVP，后续演进为 Task Orchestrator
- 相关调研：`docs/long-running-agent-auto-continue-research.md`

## 1. 背景与目标

当前 Paw 在处理长程任务时，模型可能在只完成部分工作、只输出计划或验证失败后直接结束 turn。模型的最终文本不能作为任务完成的唯一依据，因此需要在现有 Runner 的 turn 完成路径中引入任务级完成判定。

本设计采用分阶段路线：

1. **方案 A：Completion Gate MVP**：不重构现有 Runner，在当前 turn 完成分支增加确定性的完成判断、自动续行、预算、无进展检测和检查点保存。
2. **方案 B：Task Orchestrator**：将任务级生命周期从 Runner 中抽取出来，统一管理多个 turn、暂停/恢复、上下文压缩、用户输入和观测指标。

已确认的策略：

- 激进型：尽量自动跑完任务；
- 自适应续行预算，同时设置绝对上限；
- 检测到未完成信号时自动重规划并继续，而不是发送固定的“继续”；
- 工具错误采用分级处理；
- 预算耗尽、连续无进展或重复失败时暂停并保留检查点。

## 2. 非目标

方案 A 不做以下事情：

- 不立即重写 Runner 或引入完整的任务调度服务；
- 不把模型自报的“完成”强制替换为单一硬规则；
- 不自动绕过权限、审批或高风险操作；
- 不在第一阶段引入复杂的时间预算和分布式任务持久化；
- 不以旧的 workspace snapshot 覆盖当前真实工作区。

## 3. 方案 A 控制流

```text
用户任务开始
  ↓
现有 Runner 执行一次 model turn
  ↓
处理工具调用、todo、diff、事件
  ↓
模型输出完成
  ↓
Completion Gate
  ├─ complete → 正常结束
  ├─ continue → 构造 continuation context，发起下一轮
  ├─ compact → 压缩上下文后继续
  ├─ pause → 保存检查点并暂停
  ├─ blocked → 保存检查点并等待用户介入
  └─ failed → 保存失败状态并结束
```

`Completion Gate` 应是纯判断组件，接收一次 turn 结束后的观察结果并返回带原因的决策：

```go
type CompletionAction string

const (
    ActionComplete CompletionAction = "complete"
    ActionContinue CompletionAction = "continue"
    ActionCompact  CompletionAction = "compact"
    ActionPause    CompletionAction = "pause"
    ActionBlocked  CompletionAction = "blocked"
    ActionFailed   CompletionAction = "failed"
)

type CompletionDecision struct {
    Action CompletionAction
    Reason string
    Retry  bool
}
```

第一阶段允许类型落在现有包中，但应避免把判断逻辑继续堆积在 Runner 的条件分支内。

## 4. Gate 输入与判定顺序

### 4.1 观察对象

```text
CompletionObservation
├─ modelResult：最终文本、工具调用、完成/阻塞声明
├─ todoSnapshot：pending、in_progress、最近变化
├─ workspaceSnapshot：diff、未跟踪文件、最近修改文件
├─ validationSnapshot：最近验证命令及结果
├─ turnHistory：turn 数、最近动作、错误、无进展次数
└─ budget：已使用、剩余和绝对上限
```

### 4.2 判定优先级

1. **高风险或明确阻塞**：缺少用户决策、权限拒绝、外部副作用或需要审批时返回 `blocked`。
2. **不可恢复失败**：模型调用、Runner、checkpoint 或上下文恢复不可恢复时返回 `failed`。
3. **上下文需要维护**：接近上下文上限时返回 `compact`，compact 后重新评估，不直接视为完成。
4. **明确未完成**：todo 尚未完成、验证失败、只完成部分计划、存在未处理工具结果时返回 `continue`。
5. **预算或无进展边界**：达到绝对上限或连续无进展阈值时返回 `pause`。
6. **完成**：没有未完成 todo、没有未处理验证失败、没有阻塞且模型产生最终结果时返回 `complete`。
7. **激进模式最终检查**：信号不足但任务复杂时允许一次低成本检查；该检查消耗预算并受无进展检测约束。

顺序中，预算和无进展需要防止无限循环，但不能掩盖明确的阻塞或不可恢复错误。

## 5. 自动续行与重规划

`continue` 不发送无上下文的固定字符串，而是构造结构化 continuation context：

```text
任务尚未完成，请继续执行，不要只做总结。

当前状态：
- 未完成 todo：...
- 最近变更：...
- 最近验证：...
- 最近失败：...
- 已连续无进展：N 次

要求：
1. 先检查当前状态；
2. 选择并执行下一项最有价值的工作；
3. 必要时更新 todo；
4. 修改代码后执行相关验证；
5. 只有确认目标完成后才输出最终总结。
```

续行上下文应尽量复用现有消息、todo 和 journal 表达，避免复制完整历史造成额外上下文膨胀。每次续行必须记录原因、使用的预算和产生的 turn 标识。

## 6. 自适应预算

第一阶段以续行次数作为主要预算：

```text
budget = min(
    base(2)
    + min(pendingTodoCount, 5)
    + min(toolCallCount / 5, 3)
    + validationFailureCount
    + min(changedFileCount / 5, 3),
    absoluteMax(12),
)
```

实现上应明确区分：

- 当前任务已使用的自动续行数；
- 当前剩余预算；
- 用户主动发起的新任务或显式恢复是否重置预算；
- 达到绝对上限后的暂停原因。

初始默认值可以配置，测试不得依赖不可控的全局常量。第一阶段暂不实现时间预算；方案 B 可增加任务 deadline 和总执行时长。

## 7. 无进展检测

每轮结束生成进展指纹，至少覆盖：

- todo 状态；
- workspace diff 摘要；
- 最近验证结果；
- 工具调用摘要；
- 模型输出摘要。

如果连续两轮 todo、diff、验证结果均未变化，且没有有效工具调用或只是重复总结，则递增 `noProgressCount`：

```text
第一次无进展 → 自动发送一次明确的重规划提示
第二次连续无进展 → pause，保存检查点
```

进展指纹不应包含易变化的时间戳、随机 ID 或完整模型输出，以免同一状态被误判为进展。摘要应避免记录敏感内容，必要时只保存 hash 和短原因。

## 8. 工具错误分级

### Level 1：瞬态错误

网络超时、临时进程失败等允许有限次数自动重试，并使用退避。超过重试次数后交给模型诊断。

### Level 2：可修复执行错误

编译、测试、lint、参数或 patch 失败记录到 observation，允许 continuation 让模型修复并验证。

### Level 3：高风险或需要用户决策

权限拒绝、批量删除/覆盖、发布、提交、推送或外部系统写入等进入 `blocked`。自动续行不得通过不断改写命令绕过已有安全边界。

## 9. 检查点与恢复

检查点应复用现有 recovery/journal 能力；新增字段的目标是让暂停任务可解释、可恢复：

```go
type TaskCheckpoint struct {
    TaskID             string
    ParentPrompt       string
    Status             string
    TodoSnapshot       TodoSnapshot
    WorkspaceSnapshot  WorkspaceSnapshot
    ValidationSnapshot ValidationSnapshot
    LastModelSummary   string
    LastDecision       CompletionDecision
    ContinuationUsed   int
    NoProgressCount    int
    RecentEvents       []TaskEvent
    CreatedAt          time.Time
}
```

至少保证能回答：原始任务是什么、已完成什么、还剩什么、为何暂停、恢复后下一步是什么。

恢复流程：

```text
load checkpoint
  ↓
重新采集当前 todo / diff / 验证状态
  ↓
检查 checkpoint 是否过期
  ↓
重新计算预算
  ↓
继续或请求用户确认
```

恢复必须以当前工作区为事实来源，不能盲目使用旧 workspace snapshot 覆盖当前状态。

## 10. Runner 接入边界

方案 A 先在现有 Runner 的 `len(toolCalls) == 0` 或等价 turn 完成路径接入。建议形成以下逻辑边界，具体命名以项目现有包结构为准：

```go
observation := runner.buildCompletionObservation(turnResult)
decision := completionGate.Evaluate(observation)

switch decision.Action {
case ActionComplete:
    return finishTask()
case ActionContinue:
    return runner.runContinuation(decision)
case ActionCompact:
    return runner.compactAndContinue(decision)
case ActionPause, ActionBlocked:
    return runner.saveCheckpointAndPause(decision)
case ActionFailed:
    return runner.saveFailureAndFinish(decision)
}
```

第一阶段应预留后续抽取边界：

```go
type TurnExecutor interface {
    ExecuteTurn(ctx context.Context, input TurnInput) (TurnResult, error)
}

type CompletionEvaluator interface {
    Evaluate(CompletionObservation) CompletionDecision
}

type TaskCheckpointStore interface {
    Save(ctx context.Context, checkpoint TaskCheckpoint) error
    Load(ctx context.Context, taskID string) (*TaskCheckpoint, error)
}
```

A 阶段可以仍由 Runner 组合这些能力；B 阶段再把循环控制权迁移到 `TaskOrchestrator`。

## 11. TUI 与可观测性

自动续行必须在现有 queue/status 机制中可见，至少展示：

- 正在检查任务完成状态；
- 自动续行原因；
- `used/limit` 预算；
- 正在 compact 或重规划；
- 因无进展、预算耗尽或阻塞暂停。

建议指标：

```text
auto_continue_total
auto_continue_completed_total
auto_continue_paused_total
auto_continue_blocked_total
auto_continue_budget_exhausted_total
auto_continue_no_progress_total
auto_continue_turns_per_task
auto_continue_false_positive_total
```

## 12. 方案 A 验收标准

### 功能

- todo 未完成时能自动继续；
- 验证失败时能进入修复轮次；
- 上下文接近上限时先 compact 再继续；
- 连续无进展后暂停；
- 达到自适应预算后暂停；
- 权限和高风险操作不会被无限重试；
- 暂停后可从检查点恢复；
- 已完成任务不会无意义续行。

### 测试场景

1. todo 未完成 → `continue`
2. todo 已完成且验证通过 → `complete`
3. 验证失败 → `continue`
4. 连续相同状态 → `pause`
5. 预算耗尽 → `pause`
6. 权限错误 → `blocked`
7. 瞬态错误 → 有限重试
8. context 超限 → `compact`
9. checkpoint 保存和恢复
10. continuation 过程中用户输入到达

## 13. 方案 B 演进路线

方案 A 稳定后，再按以下顺序抽取：

1. 抽取 `TurnExecutor`，让一次模型交互成为独立单元；
2. 抽取 `CompletionEvaluator`、预算和 progress tracker；
3. 定义 `Task`、`Turn`、`TaskEvent` 和持久化语义；
4. 将 checkpoint 变成任务级状态存储；
5. 引入 `TaskOrchestrator`，统一驱动多个 turn；
6. 迁移后台运行、暂停/恢复、用户输入队列和 context compaction；
7. 增加 deadline、stop hook、并发控制和更完整的指标。

方案 B 的目标控制流：

```text
TaskOrchestrator
 ├─ Load task/checkpoint
 ├─ Build turn input
 ├─ TurnExecutor.ExecuteTurn
 ├─ Collect observation
 ├─ CompletionEvaluator.Evaluate
 ├─ Persist event/checkpoint
 └─ Continue / Compact / Pause / Block / Complete
```

## 14. 实施原则

- 先实现可观测、可关闭的 MVP；
- 自动续行必须有明确原因和预算消耗记录；
- 任何安全边界优先于完成率；
- 所有状态判断尽量基于真实 todo、diff、验证和 journal，而不是模型文本；
- 方案 A 的接口和数据结构应服务于方案 B，避免把续行逻辑绑定在单一 UI 或单一模型实现上。
