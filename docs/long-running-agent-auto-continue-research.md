# 长程 Agent 任务提前结束：工程实践调研与 Paw 改进建议

> 调研对象：当前 Paw 项目、OpenCode、Codex CLI，以及本地 `claude-code-sourcemap-main` 对 Claude Code 的可见实现。
>
> 证据范围：源码级阅读为主；没有把无法从公开源码直接确认的 Claude Code 行为当作事实。

## 摘要

长程任务中的“提前结束”通常不是单一的模型能力问题，而是四类状态被混在一起导致的控制流问题：

1. **模型本轮自然结束**：模型返回了文本，但任务实际上尚未完成。
2. **工具循环被错误终止**：工具调用、工具结果、流结束事件或异常之间的边界处理不完整。
3. **上下文压力或流式错误导致中断**：系统把“暂时无法继续”当成“任务完成”。
4. **任务状态不可恢复**：进程、请求或 turn 中断后，系统没有可靠地恢复未完成工作。

主流 Agent CLI 的共同做法不是简单地无限追加“请继续”，而是建立一个**外层可恢复状态机**：

- OpenCode 用 `processor.process()` 返回 `continue | compact | stop`，由 session 层决定是否自动开启下一轮；自动压缩完成后会注入一个 synthetic continuation user message。
- Codex CLI 把一次用户任务建模为可包含多个 sampling step 的 `run_turn` 循环：只要模型请求工具、存在 follow-up、stop hook 要求继续或需要中途 compact，就不会结束 turn。
- 当前 Paw 已经具备很好的基础：`maxToolRounds=500`、增量 journal、工具结果 checkpoint、recovery snapshot、上下文维护、todo 快照。但当前主循环仍然主要依赖“模型是否返回 tool call”作为继续条件，缺少一个显式的**任务完成判定与 continuation policy**。

最值得优先实施的方案是：

> **保留“模型自然结束”的语义，但在自然结束后增加一个确定性的 completion gate；只有通过 gate 才能提交 turn 为 completed，否则生成一次结构化 continuation prompt，并继续同一个可恢复 task。**

这比无条件 auto-continue 更安全，也比仅依靠 prompt 中“不要提前结束”更可靠。

---

## 1. 研究问题

本调研回答三个问题：

- **RQ1：** 长程 Agent 为什么会在任务未完成时提前结束？哪些失败属于模型决策，哪些属于 orchestrator 的工程缺陷？
- **RQ2：** OpenCode、Codex CLI 和可见的 Claude/Anthropic 相关实现，分别如何处理 continuation、compaction、失败恢复和终止判定？
- **RQ3：** Paw 当前架构最应该补哪一层能力，才能在不制造无限循环的前提下提高长程任务完成率？

### 术语约定

- **turn**：一次用户输入触发的前台运行。
- **model step / sampling step**：turn 内的一次模型请求及其工具处理。
- **continuation**：模型自然结束后，系统判断任务仍未完成，再追加一轮模型请求。
- **compaction**：压缩或总结历史，释放上下文空间后继续任务。
- **completion gate**：在最终提交前检查任务是否真的满足完成条件的确定性门禁。

---

## 2. 调研方法与证据边界

### 2.1 当前项目

通过代码图谱检查 Paw 的 turn loop、工具执行、journal、recovery、context maintenance 和 todo 状态。关键源码位置：

- `internal/loop/runner.go`
- `internal/loop/context_pressure.go`
- `internal/loop/context_compaction.go`
- `internal/session/journal.go`
- `internal/todo/tool.go`
- `internal/ui/bubble/todo_state.go`

### 2.2 OpenCode

核验了公开仓库源码：

- `packages/opencode/src/session/processor.ts`
- `packages/opencode/src/session/compaction.ts`
- `packages/opencode/src/session/llm.ts`
- `packages/opencode/src/session/run-state.ts`

仓库：<https://github.com/anomalyco/opencode>

### 2.3 Codex CLI

核验了公开仓库源码：

- `codex-rs/core/src/tasks/regular.rs`
- `codex-rs/core/src/session/turn.rs`
- `codex-rs/core/src/tasks/mod.rs`
- `codex-rs/core/src/session/turn_context.rs`
- `codex-rs/core/src/turn_metadata.rs`

仓库：<https://github.com/openai/codex>

### 2.4 Claude Code sourcemap

本地项目 `../claude-code-sourcemap-main` 中没有检索到足以确认 Claude Code 主循环 `auto-continue` 的独立源码符号。可确认的相邻证据是 Anthropic SDK 的 `CompactionControl` continuation summary：它要求模型生成结构化、可恢复的 continuation summary，以便在下一 context window 中继续工作。

因此本文不会把“Claude Code 一定采用某个具体 auto-continue 主循环”作为结论；关于 Claude Code 的部分仅使用可验证的 SDK / sourcemap 证据，并明确标注推断边界。

---

## 3. 为什么长程任务会提前结束

### 3.1 把“模型停止生成”误认为“任务完成”

LLM 的 stop reason 只说明当前 response 完成，不说明外部任务完成。一个代码任务可能在以下状态下返回普通文本：

- 只修改了部分文件；
- 测试尚未执行；
- 测试失败但模型没有继续修复；
- todo 中仍有 `pending` 项；
- 只完成了调查或计划阶段；
- 工具调用失败后模型选择解释问题而不是恢复执行。

如果 orchestrator 仅按以下条件结束：

```text
assistant message 没有 tool call => turn completed
```

那么系统在语义上必然会提前结束。

### 3.2 工具循环的“协议完成”和“任务完成”是两个概念

当前 Paw 的 `runTurnWithTiming` 已经正确地区分了：

```go
assistantMessage -> toolCalls -> toolResults -> 下一轮模型
assistantMessage -> 无 toolCalls -> 提交完成
```

这能保证工具协议层闭环，但还不能保证任务层闭环。`toolCalls == 0` 只能说明模型暂时没有请求工具，不能说明用户目标已经满足。

### 3.3 流结束、异常和取消的边界容易丢状态

Paw 的 `finishWithoutDone` 会在模型流没有发送完成事件时返回错误，这是正确方向。但长程任务还需要区分：

- provider 正常结束；
- provider 达到输出 token 上限；
- 上下文超限；
- 用户取消；
- 工具执行中途取消；
- 进程崩溃或网络断开；
- 已经产生部分工具结果但尚未形成完整的 tool-result message。

如果这些状态没有持久化成不同的 terminal / resumable 状态，恢复时就会出现“看起来已经完成”或“只能重新开始”的问题。

### 3.4 上下文压缩可能被误解为任务终止

长程任务的 context maintenance 必须是：

```text
context pressure -> prune / summarize -> inject continuation -> continue
```

不能是：

```text
context pressure -> return current answer
```

OpenCode 和 Codex 都把 compaction 设计成 turn 内或 turn 后的可继续状态，而不是普通错误。

### 3.5 无条件 auto-continue 会产生 doom loop

如果每次普通文本结束都自动发“继续”，会产生：

- 模型重复解释；
- 同一工具反复调用；
- 没有新进展却持续消耗 token；
- 任务本来已完成却继续修改；
- 用户本来需要澄清，却被系统擅自推进。

因此工程目标不是“永不停止”，而是：

> **只在有证据表明仍有未完成工作时继续，并为无进展、重复行为和预算设置硬停止。**

---

## 4. OpenCode 的实践

### 4.1 processor 不直接决定整个会话是否结束

OpenCode `SessionProcessor.process()` 返回：

```ts
type Result = "compact" | "stop" | "continue"
```

其处理逻辑是：

- stream 正常完成后，如果需要 compaction，返回 `"compact"`；
- blocked 或 assistant error，返回 `"stop"`；
- 否则返回 `"continue"`。

这说明 OpenCode 将“本次模型 stream 结束”和“session 是否应该继续”分成两层。

### 4.2 工具生命周期是显式可追踪的

`processor.ts` 对 tool call 使用 `ctx.toolcalls` 保存状态，并提供：

- `ensureToolCall`
- `updateToolCall`
- `completeToolCall`
- `failToolCall`
- `cleanup`

cleanup 阶段会等待工具完成一小段时间，并把仍处于 running 的工具标记为：

```text
Tool execution aborted
metadata.interrupted = true
```

这避免了“模型消息结束但工具仍然悬挂”的假完成状态。

### 4.3 compaction 后显式注入 continuation prompt

OpenCode 的 `compaction.ts` 在 compaction 成功且允许自动继续时，创建新的 synthetic user message，内容类似：

```text
Continue if you have next steps, or stop and ask for clarification if you are unsure how to proceed.
```

这个 prompt 有两个重要特点：

1. 它不是把旧请求全文重复发送，而是在新的上下文边界上建立 continuation。
2. 它允许模型在确实没有下一步时停止，而不是强制继续。

OpenCode 还支持 plugin hook `experimental.compaction.autocontinue`，说明 auto-continue 是可配置策略，而非硬编码的绝对行为。

### 4.4 doom loop 检测

OpenCode 设置 `DOOM_LOOP_THRESHOLD = 3`。如果最近三次 tool call：

- 工具名相同；
- 输入 JSON 相同；
- 且调用已经不是 pending；

就触发 `doom_loop` permission。

这是一种很实用的工程折中：不禁止单次重复，但对连续重复行为升级为用户确认 / 阻断。

### 4.5 OpenCode 的可迁移经验

适合 Paw 直接借鉴的不是某个 prompt，而是以下分层：

```text
LLM stream processor
    -> tool lifecycle / cleanup
    -> Result: continue | compact | stop
Session runner
    -> compaction follow-up
    -> new model request
Run state
    -> busy / idle / cancel / ensureRunning
```

---

## 5. Codex CLI 的实践

### 5.1 RegularTask 外层 loop

Codex `regular.rs` 的核心结构是：

```rust
loop {
    let last_agent_message = run_turn(...).await?;
    if !sess.input_queue.has_pending_input(...).await {
        return Ok(last_agent_message);
    }
    next_input = Vec::new();
}
```

这说明 Codex 把一个任务的生命周期放在 task 层，而不是把一次 `run_turn` 当作整个任务。只要有 pending input，就继续下一次 run。

### 5.2 run_turn 内部是 sampling loop

Codex `session/turn.rs` 明确说明：每次 sampling request 后，模型可能：

- 请求 function call；系统执行工具并把结果发送回模型；
- 返回 assistant message；系统才认为 turn 可以结束。

但源码实际还有更多 continuation 条件：

```text
needs_follow_up = model_needs_follow_up || has_pending_input
```

此外还会处理：

- context window rollover；
- mid-turn auto compact；
- stop hooks；
- legacy after-agent hook；
- 用户在模型执行期间提交的 input。

### 5.3 stop hook 可以把“结束”转换成 continuation

当模型本来没有 follow-up 时，Codex 仍会运行 `run_turn_stop_hooks`。如果 hook 返回 continuation fragments，Codex 会把它们构造为 hook prompt message，写入 input queue，然后继续 loop。

这非常接近一个确定性的 completion gate：

```text
model says done
    -> stop hooks / checks
    -> if blocked: inject corrective context and continue
    -> otherwise: finish
```

### 5.4 compaction 是 turn 内状态迁移

如果采样后发现需要 rollover，Codex 执行 `run_auto_compact`，然后根据模型是否需要 follow-up 设置：

```text
can_drain_pending_input = !model_needs_follow_up
continue
```

也就是说，compaction 不会把任务降级为失败；它只是把上下文状态迁移到可继续的表示。

### 5.5 统一的 task lifecycle

Codex `tasks/mod.rs` 在 session 层统一处理：

- task start；
- cancellation token；
- abort grace period；
- task finish；
- turn complete / turn aborted / turn error 生命周期事件；
- pending input drain；
- idle 后重新启动 pending work。

这对 Paw 的启示是：应把“模型 turn”与“用户任务 task”显式分离。当前 Paw 的 `RunTurnWithTiming` 更像一个 turn API，下一步需要一个可恢复的 task controller。

---

## 6. Claude Code / Anthropic 可见实践

### 6.1 可确认事实

在本地 sourcemap 依赖中，Anthropic SDK 的 `CompactionControl` 提供了 continuation summary prompt，要求摘要包含能够让下一 context window 恢复工作的结构化信息。

这类摘要通常应包括：

- 当前任务目标；
- 已完成工作；
- 未完成工作；
- 关键决策；
- 修改过的文件；
- 当前测试状态；
- 下一步行动；
- 不能丢失的约束。

### 6.2 不能过度推断的部分

在当前可见的 `claude-code-sourcemap-main` 中，没有找到可以直接确认 Claude Code 主循环 auto-continue 细节的源码。因此不能严谨地说它一定采用：

- 固定次数自动继续；
- 某个具体的 completion classifier；
- 与 OpenCode 完全相同的 `continue | compact | stop` 状态机。

但可恢复摘要这一实践本身非常值得 Paw 采用：它解决的是**跨 context / 跨进程恢复时状态不丢失**的问题，而不是单纯让模型多生成一轮。

---

## 7. Paw 当前实现诊断

### 7.1 已经做得好的地方

Paw 当前代码并不是缺少基础设施，反而已经具备多个成熟 Agent runtime 的关键组件：

#### A. 工具轮次上限较高且有明确硬停止

`internal/loop/runner.go`：

```go
const maxToolRounds = 500
```

主循环在 assistant 没有 tool call 时提交完成，超过 500 轮则返回错误。这避免了真正的无限 tool loop。

#### B. 工具结果增量 checkpoint

`runToolCallsWithCheckpoint` 会在每个工具结果完成后调用 checkpoint：

```go
checkpoint(callIndex, result)
```

这意味着并行工具执行过程中，即使后续工具失败或进程中断，已经完成的 tool result 仍可以被 journal 记录。

#### C. journal / recovery 设计已经接近可恢复 runtime

`TurnJournal` 提供：

- `BeginTurn`
- `AppendAssistant`
- `AppendToolResult`
- `CompleteTurn`
- `FailTurn`
- `LoadSnapshot`

`RecoveryState` 记录：

- `TurnID`
- 错误原因；
- 已完成的 tool results；
- 被丢弃的 tool calls；
- 是否 interrupted。

`safeTurnHistory` 还会避免把缺少完整 tool result 的 assistant call 直接放入下一次模型 history。

#### D. context maintenance 已经是分阶段策略

Paw 的 context pressure 有：

- soft pressure；
- tool result snip；
- prune；
- summary compaction；
- archive；
- consecutive compaction stuck 检测。

这比简单按消息数量截断更可靠。

#### E. todo 有完整快照与恢复投影

`update_todo` 要求每次提交完整 ordered snapshot，且最多一个 `in_progress`。UI 也能从 journal / transcript 恢复 todo 状态。

这为 completion gate 提供了天然的结构化信号。

### 7.2 当前最可能的提前结束点

当前关键逻辑位于 `runTurnWithTiming` 的 389–461 行附近：

```go
for round := 0; round < maxToolRounds; round++ {
    assistantMessage, err := runner.runModelTurn(ctx, history)
    ...
    toolCalls := toolCallsFromMessage(assistantMessage)
    if len(toolCalls) == 0 {
        journal.CompleteTurn(...)
        runner.setHistory(...)
        return runner.completeTurnExecution(...), nil
    }
    toolResult, err := runner.runToolCallsWithCheckpoint(...)
    history = append(history, toolResult)
}
```

这个逻辑在**协议层**是正确的，在**任务层**可能过早：

```text
no tool calls == assistant response complete
```

但用户任务可能尚未完成。

### 7.3 其它风险点

- `maxToolRounds=500` 只限制 tool loop，不能限制“自然结束后继续”的 continuation 次数。
- todo 当前是 UI / model-facing state，还没有成为 runner 的强制 completion contract。
- 没有看到统一的 `CompletionStatus` / `ContinueReason` 类型，导致 stop、compact、error、natural completion 可能由不同层隐式判断。
- `TurnMetadata.Status` 目前记录 completed / failed / stopped，但没有 `needs_follow_up`、`paused`、`resumable` 等中间终态。
- `finishWithoutDone` 能识别流协议不完整，但需要确保它不会与“模型主动自然结束”混淆。
- 当前 recovery 主要解决“中断后历史如何安全恢复”，还没有解决“模型正常结束但任务未完成时如何恢复”。

---

## 8. 推荐的目标架构

### 8.1 把一次运行拆成 Task、Turn、Step 三层

建议引入以下概念：

```text
Task
  一个用户目标，例如“完成这个重构并通过测试”

Turn
  task 中的一次模型请求周期；可以因 compaction 或 continuation 产生多个 turn

Step
  turn 中的一次模型采样、工具调用和工具结果闭环
```

状态关系：

```text
Task running
  -> Turn running
      -> Step sampling
      -> Tool execution
      -> Step finished
  -> Completion gate
      -> completed
      -> continue
      -> compact
      -> blocked / ask_user
      -> failed
      -> paused / resumable
```

### 8.2 引入显式 continuation decision

建议定义：

```go
type ContinueReason string

const (
    ContinueToolCalls       ContinueReason = "tool_calls"
    ContinuePendingTodo      ContinueReason = "pending_todo"
    ContinueCompletionGate  ContinueReason = "completion_gate"
    ContinueStopHook        ContinueReason = "stop_hook"
    ContinueCompaction      ContinueReason = "compaction"
    ContinueRecovery        ContinueReason = "recovery"
)

type TurnDecision string

const (
    DecisionCompleted TurnDecision = "completed"
    DecisionContinue  TurnDecision = "continue"
    DecisionCompact   TurnDecision = "compact"
    DecisionAskUser   TurnDecision = "ask_user"
    DecisionFailed    TurnDecision = "failed"
    DecisionPaused    TurnDecision = "paused"
)

type CompletionAssessment struct {
    Decision       TurnDecision
    Reason         ContinueReason
    Evidence       []string
    Confidence     float64
    ProgressHash   string
}
```

核心要求：模型 response 解析只产生候选结果，最终由 policy 计算 `TurnDecision`。

### 8.3 completion gate 不应一开始就依赖第二个 LLM

第一版建议使用确定性 gate，信号按强度排序：

#### 强信号：必须继续

- todo 存在 `pending` 或 `in_progress`；
- 最近一次工具调用返回错误且没有修复动作；
- 用户明确要求“实现并测试”，但测试未执行；
- 代码修改后没有运行项目配置中的验证命令；
- journal 中存在未完成 tool call；
- 上一次响应以“我还需要…”、“下一步…”、“尚未…”结尾。

#### 中等信号：建议继续一次

- 本轮发生了文件修改，但没有验证命令；
- 只调用了 Read / Grep / Glob 等观察工具，没有产生修改或最终报告；
- assistant 文本包含计划清单，但只完成其中一部分；
- todo 刚刚从 `in_progress` 更新为 `completed`，但还没有最终答复或验证。

#### 强停止信号

- 所有 todo 完成；
- 用户目标是纯问答 / 调查，且必要资料已返回；
- 验证命令成功；
- 模型明确表示需要用户选择、权限或澄清；
- 连续 continuation 没有状态变化。

### 8.4 continuation prompt 要结构化且允许停止

建议不要只发送“继续”。可采用：

```text
你正在继续执行同一个任务，不要重新开始，也不要重复已经完成的工作。

任务目标：{goal}

当前状态：
- 已完成：{completed}
- 未完成：{pending}
- 最近修改：{changed_files}
- 最近验证：{checks}
- 最近错误：{errors}

请先检查当前状态，然后执行剩余工作。
只有在满足完成条件、验证通过，或确实需要用户澄清/授权时才停止。
如果任务已经完成，请给出简短最终结果，不要继续修改。
```

这比把完整 transcript 重复注入更稳定，也与 OpenCode 的 compaction continuation 和 Anthropic SDK 的 continuation summary 思路一致。

---

## 9. 对 Paw 的分阶段实施方案

### Phase 0：先补观测，不改变行为

增加每次 turn 的结构化记录：

```text
turn_id
step_index
assistant_finish_reason
has_tool_calls
tool_call_count
tool_error_count
todo_pending_count
todo_in_progress_count
files_changed
verification_commands
context_pressure
continuation_count
progress_hash
terminal_decision
```

重点指标：

- `premature_stop_rate`：后续恢复 / 用户继续后发现仍有工作；
- `continuation_success_rate`：自动继续后真正完成的比例；
- `no_progress_continuation_rate`；
- `doom_loop_rate`；
- `context_compaction_resume_rate`；
- `recovery_resume_success_rate`。

没有这些指标，无法知道 auto-continue 是改进还是制造噪声。

### Phase 1：把 completion gate 接在当前 `len(toolCalls)==0` 分支

当前逻辑可以改成：

```go
if len(toolCalls) == 0 {
    assessment := runner.assessCompletion(history, assistantMessage)
    switch assessment.Decision {
    case DecisionContinue:
        history = append(history, assistantMessage)
        history = append(history, buildContinuationMessage(assessment))
        recordContinuationCheckpoint(...)
        continue
    case DecisionCompact:
        ...
        continue
    case DecisionAskUser:
        ...
    case DecisionCompleted:
        commitAndComplete(...)
    }
}
```

初期只启用低风险规则：

- todo 未完成；
- 有修改但没有测试；
- 明确的验证命令失败；
- recovery 中有 dropped tool calls；
- continuation 次数不超过 1–2 次。

### Phase 2：引入任务级预算和无进展检测

每次 continuation 需要检查：

```text
continuation_count <= max_continuations
elapsed_time <= task_timeout
session_tokens <= task_budget
progress_hash != previous_progress_hash
same_tool_signature_count < doom_threshold
```

`progress_hash` 可以由以下内容组成：

- 工作区 diff hash；
- todo snapshot hash；
- 最近验证结果 hash；
- journal 中最新 assistant/tool sequence。

如果连续两次 continuation 的 progress hash 不变，应停止并向用户报告“未检测到进展”，不要继续消耗模型调用。

### Phase 3：将 todo 从提示性工具升级为任务契约

当前 `update_todo` 的规范已经很好，但需要让 runner 能读取 snapshot，而不是只让 UI 读取。

建议：

- Runner 持有 `TodoSnapshotProvider`；
- 每次 tool result 成功后更新内存快照；
- completion gate 能看到完整 todo；
- `completed` todo 仍需要验证信号才能最终完成；
- 任务结束时，如果 todo 仍有 `pending` / `in_progress`，默认不能直接 `CompleteTurn`。

注意：不能简单地“有 pending 就永远继续”。用户可能故意要求先规划，或任务被权限 / 澄清阻塞。因此还需要区分：

```text
pending_work
blocked_on_user
blocked_on_permission
waiting_external_process
```

### Phase 4：任务级 journal 与 resume

现有 `TurnJournal` 已经是良好基础，建议增加：

```text
TaskStarted
TurnStarted
StepFinished
ContinuationRequested
CompactionStarted
CompactionCompleted
CompletionAssessed
TaskPaused
TaskCompleted
```

恢复时不只恢复 `ActiveHistory`，还要恢复：

- task goal；
- todo snapshot；
- continuation count；
- latest completion assessment；
- progress hash；
- pending external dependency；
- last known verification result。

这样可以区别：

- “上一次 turn 正常结束”；
- “上一次 turn 正常结束但 task 仍需继续”；
- “上一次 turn 被中断，存在可恢复工作”；
- “上一次 task 被用户暂停”。

---

## 10. 不建议采用的方案

### 10.1 只修改 system prompt

例如加入：

```text
不要提前结束，必须完成所有工作。
```

这只能改善模型倾向，无法修复：

- 流结束异常；
- tool result 丢失；
- context overflow；
- 进程崩溃；
- todo 与实际状态不一致。

### 10.2 每次自然结束都无条件发“继续”

这是最容易实现、也最容易造成体验恶化的方案。至少需要：

- completion gate；
- continuation budget；
- progress hash；
- doom loop detector；
- ask-user / blocked 分支。

### 10.3 把所有 pending todo 都强制转成继续

pending 可能代表：

- 尚未执行的工作；
- 等待用户确认；
- 等待外部进程；
- 规划阶段尚未开始执行。

必须先区分阻塞原因。

### 10.4 只提高 `maxToolRounds`

Paw 当前已经是 500。继续增大只会延长错误循环，不能解决模型自然结束后的提前停止。

### 10.5 用第二个模型无条件评审每次结束

LLM completion judge 可以作为后续增强，但成本、延迟和误判都较高。第一版应优先使用 todo、diff、测试、journal 等确定性信号。

---

## 11. 推荐的最小可行设计

如果只做一个版本，建议实现以下五个组件：

### 11.1 `CompletionGate`

```go
type CompletionGate interface {
    Assess(ctx context.Context, input CompletionInput) CompletionAssessment
}
```

输入：

- user goal；
- assistant message；
- current history；
- todo snapshot；
- workspace diff；
- verification results；
- recovery state；
- continuation count。

### 11.2 `ContinuationBudget`

默认建议：

```text
max_continuations_per_task = 3
max_no_progress_continuations = 1
max_total_task_duration = configurable
```

### 11.3 `ProgressFingerprint`

至少记录：

```text
todo hash + diff hash + verification hash + last journal sequence
```

### 11.4 `CompletionAssessment` journal 记录

每次自然结束都记录 assessment，不要只记录最终 completed / failed。

### 11.5 可恢复 continuation message

把 continuation 作为 synthetic user message 或 prompt supplement 保存到运行态，但默认不要污染用户可见 transcript；这点可以沿用 Paw 当前 recovery message 的设计。

---

## 12. 对比表

| 能力 | Paw 当前 | OpenCode | Codex CLI | Claude/Anthropic 可见证据 |
|---|---|---|---|---|
| 工具闭环 | 有，500 轮上限 | 有，显式 tool lifecycle | 有，sampling loop | SDK 层有 tool/stream 基础 |
| 工具中断清理 | 有 recovery / journal | cleanup 标记 aborted tool | task abort lifecycle | 可见源码不足 |
| 普通 response 后继续 | 主要按无 tool call 完成 | processor 返回 continue，由 session 驱动 | follow-up、pending input、stop hook | 主循环未能从 sourcemap 确认 |
| compaction 后继续 | 有 context maintenance 和 recovery 基础 | synthetic continuation prompt | turn 内 auto compact 后继续 | continuation summary 明确存在 |
| doom loop | 尚未看到统一 gate | 最近 3 次同工具同输入触发 permission | 有预算 / hook / 状态保护，具体 doom 规则需另行核验 | 未确认 |
| todo 作为完成条件 | UI / tool 状态完整，但尚未成为 runner gate | session todo 持久化 | plan / hook / task 状态体系 | 未确认 |
| 任务级可恢复状态 | journal/recovery 已有较强基础 | run state + session 状态 | session task + turn lifecycle | continuation summary 思路明确 |

---

## 13. 结论：逐一回答研究问题

### RQ1：为什么提前结束？

根因是把 response 级终止当成 task 级完成。工具循环正确并不等于任务完成；上下文、验证、todo、用户输入和阻塞状态都必须参与最终判定。

### RQ2：参考实现如何解决？

OpenCode 的关键是显式 `continue | compact | stop`、tool cleanup、doom loop 检测和 compaction continuation prompt。Codex 的关键是把一个任务拆成 task / turn / sampling loop，并在普通 response 后继续检查 pending input、stop hooks、context rollover 和 auto compact。Claude/Anthropic 的可确认经验主要是结构化 continuation summary，用于跨 context 恢复；其 Claude Code 主循环的具体 auto-continue 实现不能仅凭当前 sourcemap 确认。

### RQ3：Paw 最应该补什么？

不是继续提高 `maxToolRounds`，也不是单纯强化 prompt，而是在当前 `runTurnWithTiming` 的 `len(toolCalls)==0` 分支加入**显式 completion gate + 有预算的 continuation + 无进展检测**。同时把 todo、验证结果、diff、journal recovery 和 context 状态纳入 assessment，并将 continuation / paused / resumable 作为可持久化状态。

---

## 14. 建议的近期代码改动顺序

1. 新增 `CompletionAssessment`、`TurnDecision`、`ContinueReason` 类型。
2. 新增 runner 内存字段：`continuationCount`、`lastProgressHash`、`taskGoal`。
3. 在自然结束分支先执行确定性 completion gate，再决定 `CompleteTurn` 或 continuation。
4. 第一版只启用低风险规则：pending todo、修改后未验证、最近错误未处理、recovery 中有 dropped calls。
5. 增加 continuation synthetic message，但不污染普通用户 transcript。
6. 增加同一任务最多 3 次 continuation、无进展最多 1 次的硬限制。
7. 为 gate、progress hash、doom loop、compaction resume 增加表格驱动测试。
8. 通过 telemetry 观察 premature stop 与 no-progress continuation，再调规则和阈值。

最终目标不是让 Agent 永远工作，而是让它在三种状态之间可靠切换：

```text
完成 -> 明确结束
未完成且可推进 -> 自动继续
未完成但被阻塞 / 无进展 -> 暂停并向用户说明
```

---

## 参考资料

### 源码

1. OpenCode `SessionProcessor`：<https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/opencode/src/session/processor.ts>
2. OpenCode `SessionCompaction`：<https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/opencode/src/session/compaction.ts>
3. OpenCode `SessionRunState`：<https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/opencode/src/session/run-state.ts>
4. OpenAI Codex `regular.rs`：<https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/tasks/regular.rs>
5. OpenAI Codex `turn.rs`：<https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/session/turn.rs>
6. OpenAI Codex `tasks/mod.rs`：<https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/tasks/mod.rs>
7. OpenAI Codex `turn_context.rs`：<https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/session/turn_context.rs>
8. OpenAI Codex `turn_metadata.rs`：<https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/turn_metadata.rs>

### 当前 Paw 关键文件

- `internal/loop/runner.go`
- `internal/loop/context_pressure.go`
- `internal/loop/context_compaction.go`
- `internal/session/journal.go`
- `internal/todo/tool.go`
- `internal/ui/bubble/todo_state.go`

### 相关 SDK / 文档

- Anthropic SDK compaction control：本地 `claude-code-sourcemap-main/restored-src/node_modules/@anthropic-ai/sdk/lib/tools/CompactionControl.mjs`
- OpenAI Agents 文档：<https://developers.openai.com/api/docs/guides/agents>

> 说明：本报告是工程实现调研，不是对各产品内部未公开实现的断言。产品行为会随版本变化，源码链接应结合 commit / tag 一起复核。
