# Paw Plan Mode 运行时设计（Plan 独立于 Goal）

- 状态：已批准，待实现
- 范围：Plan 独立运行时 + 独立输入模式 + Goal 侧 Plan 追踪清理
- 前置设计：`docs/goal-mode-design.md`（本设计取代其 §5 Plan/Todo、§10 /goal plan、§11 Phase 4 中关于 Goal 附属 Plan 的部分）
- 决策记录：2026-08-08 与用户逐项确认

## 1. 摘要

原设计中 Plan 是 Goal 的附属执行追踪器（`PlanStep` + 依赖图 + 按 GoalID 索引）。本设计将 Plan 彻底拆出：

- **Plan 是独立于 Goal 的文档创作模式**：产出 `docs/superpowers/plans/YYYY-MM-DD-<slug>-plan.md` 规格文档，划定 spec/scope 与执行内容。
- **Goal 回归长程自主任务**：agent 在 goal 内自行探索，不再追踪 PlanStep；goal 状态机保留 `planning/replanning` 作为内部阶段。
- **交互流程**：用户提交需求 → agent 多轮 question/select 澄清 → 写文档 → 展示 → 最终 question【执行/修改】→ 执行则定稿落盘并切回普通 chat 模式执行；修改则继续迭代。
- **权限边界**：plan 轮中 agent 只读工作区 + Write 仅限 plans 目录，借鉴 OpenCode plan agent 的权限隔离。

## 2. 架构

```text
internal/plan/                独立 Plan 运行时
  types.go                    PlanDoc / PlanID / PlanStatus / 事件
  state.go                    SessionStatus 状态机
  store.go                    FileStore（docs/superpowers/plans/ 为真相）
  prompt.go                   plan 轮系统指令（brainstorming→writing-plans 流程）
  evaluator.go                turn 后判定：doc approved → complete
  runtime.go                  PlanRuntime（复用 loop.TaskOrchestrator + Runner.GoalTurnExecutor）
  tool.go                     plan_finalize 工具（仅 plan 轮可见）

internal/loop/                Runner 新增按轮工具过滤
  runner.go                   ToolFilter 字段 + SetTurnToolFilter
  model_turn.go               构建模型工具列表时按过滤裁剪
  tool_execution.go           resolveToolCall 时按过滤拒绝

cmd/agent/                    TUI 接线
  plan_controller.go          sessionPlanController（适配 Runtime → Bubble Tea）
  interactive.go              装配

internal/ui/bubble/           TUI 接入
  bubble.go                   PlanController 接口 + UI.SendMessage
  input.go                    submitPlan → controller.Start
  types.go / app.go           planWorking / planFinalizedMsg 处理
  command_registry.go         /plan 命令；/goal 收缩
```

## 3. 数据模型

```go
type PlanID string
type PlanStatus string // draft | approved

type PlanDoc struct {
    ID        PlanID      // plan-<n>
    Title     string
    Path      string      // docs/superpowers/plans/YYYY-MM-DD-<slug>-plan.md
    Content   string      // markdown 全文
    Status    PlanStatus  // draft → approved
    CreatedAt, UpdatedAt time.Time
}
```

FileStore 以文件为真相：`Create/Get/Update/List/MarkApproved`。文件名 slug 由标题生成；标题缺失时用需求前若干字符。

## 4. 会话状态机

```text
clarifying → drafting → awaiting_approval → approved（终态）
                              ↘ drafting（用户选"修改"）
```

- `clarifying`：agent 逐条提问澄清（question/文本），直到需求明确。
- `drafting`：agent 撰写文档（Write 到 plans 目录）。
- `awaiting_approval`：agent 展示文档全文，调用 question【执行/修改】。
- 用户选"修改" → `drafting`；选"执行" → agent 调用 `plan_finalize` → `approved`。

## 5. PlanRuntime

- `RuntimeConfig{Store, Executor, Filter, Events, Now, Policy}`。
- `Start(requirement)`：建 draft PlanDoc → `clarifying` → `runAsync`。
- `Run`：`loop.TaskOrchestrator{Executor, Evaluator: planEvaluator, Events}` 执行；输入 = requirement。
- 每轮执行前：`runner.SetTurnToolFilter(planFilter)` + `runner.SetSystemSupplement(planInstructions)`；执行后恢复。
- 完成判定：turn 后检查 `doc.Status == approved` → `ActionComplete`；预算/无进展 → paused（`/plan resume` 恢复）。
- 事件：`plan.started / turn.completed / plan.finalized / plan.paused / plan.failed / plan.cancelled`。
- 定稿后通过 `OnFinalized func(PlanDoc)` 回调通知控制器 → UI 切回 chat 并自动开始执行轮。

## 6. 工具过滤

```go
type ToolFilter func(name string, input json.RawMessage) error
```

生效点：
- `runModelTurn`：`registry.Definitions()` 按名裁剪（被禁工具不出现在模型提示）。
- `resolveToolCall`：执行前检查，拒绝返回 `"tool not allowed in plan mode: <name>"`。

plan 轮允许集：
- 只读：Read / Glob / Grep / LS / codegraph* / WebFetch / question（提问）。
- 写：Write（路径必须落在 plans 目录内）。
- 流程：plan_finalize。
- 禁止：Bash / Edit / Subagent* / todo 等。

## 7. plan_finalize 工具

- input `{plan_id}`；校验属于当前活动 plan 会话；`MarkApproved` 落盘；返回文档路径。
- 无活动会话时返回错误（普通模式即使误调用也安全）。
- 确定性信号：evaluator 不解析模型文本，只检查文档状态。

## 8. UI 与命令

- plan 模式（Tab 循环 `chat → goal → plan`）Enter → `submitPlan` → `planController.Start(requirement)`。
- `planWorking` 状态；状态栏 `mode=plan`。
- `planFinalizedMsg{Path}`：收到后 `planMode=false`、`planWorking=false`，注入一条普通 chat turn：`开始执行已批准的计划：<path>`（全工具、同一会话历史）。
- `/plan status|list|show <id>|stop`（+ `new <requirement>`）。
- `/goal` 收缩为 `start|status|pause|resume|stop|budget`；删除 `plan|evidence|replan|retry` 与 `ExtendedGoalController`；`Budget()` 并入基础接口。
- `bubble.UI` 新增导出 `SendMessage(tea.Msg)` 供控制器回传完成事件。

## 9. Goal 侧清理

- 删除 `internal/goal/plan.go`（Plan/PlanStep/PlanStore）。
- `Goal.PlanID`、`GoalSnapshot.PlanID`、`GoalCheckpoint.PlanVersion/CurrentStep`、`RuntimeConfig.Plans`、`Runtime.plans`、`LatestPlan`、`Replan`、`SaveCheckpoint` 的 plan 分支全部移除。
- `Evidence.StepID` 类型从 `PlanStepID` 改为 `string`。
- **保留**：`GoalPlanning/GoalReplanning` 状态枚举与迁移（goal 内部阶段）、Evidence 模型与 `EvidenceForGoal`（goal 完成门禁的一部分，暂不接线 UI）。

## 10. 验收标准

1. 无 goal 时 plan 模式可用：提交需求即开始 plan 会话。
2. plan 轮中模型工具集被裁剪，写文件仅限 plans 目录，Bash/Edit 被拒。
3. 澄清 → 草稿 → 展示 →【执行/修改】流程完整；修改可迭代，执行后定稿落盘。
4. 定稿后自动切回 chat 模式并启动执行轮，plan 文档路径作为上下文。
5. `/plan` 命令可用；`/goal` 不再包含 plan/evidence/replan/retry。
6. goal 包无 Plan 残留；`go build ./...` 与 `go test ./...` 通过。

## 11. 不采用的方案

- Plan 作为 Goal 附属执行追踪器（原设计）：与"goal 由 agent 自主探索"冲突。
- 纯提示不限制工具：无法强制只读 + 只写 plan 文件。
- plan 轮不落盘仅内存：不可提交、不可复用。
