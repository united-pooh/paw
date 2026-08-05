# SubagentWait 与结构化任务块设计

日期：2026-08-06
状态：已批准（终端 + 视觉伴侣验收）
关联调研：`docs/subagent-creation-transcript-survey.md`

## 背景与问题

当前 subagent 流程：

1. `Subagent` 工具创建后台任务，立即返回 task id（handle）。
2. 模型如需结果，只能反复调用 `SubagentStatus` 轮询。

两个实际问题：

- **轮询不优雅**：模型被迫发起多轮无意义的工具调用，直到任务完成。
- **Status 返回过大**：`SubagentStatus` 无 id 时返回 `ListTasks()` 全量历史，每个任务携带 `Content` 全文，注入巨大的 JSON，污染上下文（已观察到 KB 级响应）。

对 OpenAI Codex（`multi_agents_spec.rs`）、OpenCode（`task.ts`）、Pi（pi-subagents）的调研结论：三家都采用"创建即返回 handle"，真正的差异在于——

1. **完成 push**：OpenCode 注入 background-completion 消息；Codex 在 `wait_agent` 返回通知；Pi 有 `subagent:async-complete` 事件。
2. **阻塞 wait 工具**：Codex `wait_agent`、Pi `subagent_wait`、OpenCode 前台 Task，取代状态轮询。
3. **工具描述明确禁止轮询**（OpenCode、Codex）。

Paw 已有完成 push 的骨架（`submitTaskContext` → `SubmitSupplement`，仅格式为纯文本），缺的是阻塞 wait 与展示层。

## 设计决策（已批准）

| # | 决策 | 来源 |
|---|------|------|
| 1 | 新增 `SubagentWait`：多任务任一完成即返回（Codex 风格） | 澄清问答 |
| 2 | 超时返回快照 + `timed_out` 标记，**非错误**（Codex 风格） | 澄清问答 |
| 3 | 完成 push 改为结构化 `<task>` 块，TUI 渲染框线块 | 视觉伴侣验收（方案 A） |
| 4 | `SubagentStatus` 瘦身：永不返回 `Content`；无 id 只列运行中任务 | 问题驱动 |
| 5 | Transcript 运行中任务卡：右边界、垂直居中；完成时移除 | 视觉伴侣验收（方案 A） |
| 6 | **不做**自动弹出 popup | 用户确认范围裁剪 |
| 7 | 不做任务 resume、并发限制（P2 再议） | YAGNI |

## 组件设计

### 1. SubagentWait 工具（P0）

新增 `waitTool`（`internal/subagent/manager.go`），构造器 `NewWaitTool(manager *Manager)`，注册于 `cmd/agent/tool_registration.go`。

**Name**：`SubagentWait`

**Description**：

> Block until any of the given subagent tasks finishes (completed, failed, or stopped). Returns the latest snapshot of all requested tasks with timed_out=false once at least one finishes; on timeout returns timed_out=true with the current snapshot (not an error). Do not poll SubagentStatus — use SubagentWait instead.

**InputSchema**：

```json
{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "task_ids": {"type": "array", "items": {"type": "string"}, "minItems": 1},
    "timeout_ms": {"type": "number"}
  },
  "required": ["task_ids"]
}
```

**Run 语义**：

1. 解析输入；`timeout_ms` 缺省取 `settings.subagent.wait_timeout_ms`（默认 600000 = 10 分钟）。
2. 去重 `task_ids`；逐个取当前快照（`Status(id)`），不存在的记 `not_found` 标记（宽容，不报错）。
3. 调用 `m.WaitAny(ctx, ids, timeout)`。
4. 返回：

```json
{
  "timed_out": false,
  "tasks": [
    {"id": "task-02", "name": "...", "status": "completed",
     "started_at": "...", "finished_at": "...", "exit_code": 0,
     "error": "", "output_path": "...", "transcript_path": "...", "not_found": false}
  ]
}
```

摘要不含 `Content`（与 Status 瘦身一致，见 §3）。

**IsConcurrencySafe**：`true`（阻塞型工具，无共享写入）。

#### Manager.WaitAny(ctx, ids, timeout)

广播通知机制，避免新增轮询 goroutine：

- Manager 增加字段 `notifyCh chan struct{}`（初始化 `make(chan struct{})`）。
- 终态转换点（`finishTask` 已持锁处、`Stop` 的终态分支）执行：

```go
m.mu.Lock()
// ... 更新 tasks / 删除 running ...
close(m.notifyCh)            // 唤醒所有等待者
m.notifyCh = make(chan struct{})
m.mu.Unlock()
```

- `WaitAny` 循环：

```go
for {
    // 持锁检查：所有目标 id 是否已终态 → 汇总返回
    // 持锁取当前 notifyCh 引用
    select {
    case <-ch:
        continue // 重新检查
    case <-time.After(timeout):
        return timed_out=true 快照
    case <-ctx.Done():
        return err
    }
}
```

- 竞态安全：notifyCh 的 close/替换只在 `m.mu` 内进行；WaitAny 每次迭代在锁内获取 channel 引用并检查终态，保证不丢失唤醒（检查在等待前，channel 在锁内取）。
- 不触碰 `Process.Wait()` / worker 层：等待者只观察状态转换，任务本身的等待仍由既有 `waitBackground` goroutine 完成。

### 2. 完成 push 结构化（P0）

**现状**：`renderTaskContextUpdate(task)`（`internal/subagent/manager.go:1015`）输出纯文本行（`Background subagent completed.` + kv），经 `submitTaskContext` → `contextSink.SubmitSupplement` 注入父会话。

**改造**：`renderTaskContextUpdate` 重写为结构化块：

```
<task id="task-02" state="completed" name="OpenCode 调研" duration_ms="42000" output_size="12700">
summary: <truncateForParentContext 截断后的摘要>
output: /path/output.json
transcript: /path/transcript.jsonl
</task>
```

- `state` ∈ `completed` / `failed` / `stopped`。
- `duration_ms` = `FinishedAt - StartedAt`；`FinishedAt` 为 nil 时省略该属性。
- `output_size` = output.json 文件字节数（读取失败则省略）。
- `summary` 继续使用 `truncateForParentContext`（`parentContextResultMaxRunes` 截断，超长指向 output 文件）。
- 失败时追加 `error: <task.Error>` 行。
- 注入路径不变（仅 background + 有 ParentSessionID）。

**TUI 渲染（框线块，视觉已验收）**：`internal/ui/bubble/transcript.go` 识别以 `<task ` 开头、`</task>` 结尾的 supplement 块，渲染为：

```
┌─ ✓ task-02 完成 ──────────────┐
│ OpenCode 调研 · 42s · 12.4KB  │
│ 摘要: ...                     │
└───────────────────────────────┘
```

- 颜色：completed 绿 / failed 红 / stopped 黄；标题行含 `✓/✗/◼` 标记。
- 旧历史中的 `Background subagent completed.` 纯文本照常显示，不特殊处理（向后兼容）。

### 3. SubagentStatus 瘦身（P1）

`statusTool.Run` 改造（`internal/subagent/manager.go:1369`）：

- 有 id：`Status(id)` → 摘要。
- 无 id：`ListTasks()` 过滤 `status == running` → 摘要列表；无运行中任务返回空数组（而非全量历史）。
- 摘要格式（新增 `summarizeTask(t TaskSnapshot)`）：`id / name / status / started_at / finished_at / depth / parent_task_id / exit_code / error / output_path / transcript_path`。**永不包含** `content`、`prompt`、`system_prompt`、`usage`。
- 需要看结果 → 模型用 `Read` 读 `output_path`。
- Description 更新为：

> Summarize subagent tasks. Without id, lists only running tasks. Results are not included — read the task's output_path. Never poll — use SubagentWait instead.

### 4. Transcript 任务卡（P1）

- 位置：`internal/ui/bubble/transcript.go`（运行中任务卡渲染在 transcript 视口右边界内侧、垂直居中）。
- 数据源：Manager 新增 `RunningTasks() []TaskSnapshot`（运行中摘要）；UI 通过既有事件通道（`Notifier`/`recordTaskFinished` 所在链路）订阅 创建/完成 事件触发重绘。具体接线在实现时按 UI 现有订阅模式确认。
- 卡片内容：标题 `subagents · N 运行中` + 每项 `◐ spinner + name + 短 id`。
- 完成时：该项从卡片移除（push 框线块作为持久记录保留在流中）。

## 错误处理

| 场景 | 行为 |
|------|------|
| `task_ids` 为空 / 非法 | 工具返回输入错误 |
| 未知 task id | `not_found: true` 标记，不中断其他任务 |
| 超时 | `timed_out: true` + 当前快照，非错误 |
| ctx 取消（回合取消） | 返回错误，由工具层正常处理 |
| `SubmitSupplement` 返回 false | 静默忽略（现状行为） |

## 配置

- `settings.subagent.wait_timeout_ms`：默认 600000（10 分钟）。仅此一项新增。

## 测试

`internal/subagent/manager_test.go`：

- `TestWaitAnyReturnsWhenAnyTaskCompletes`：多任务等待，任一完成立即返回，`timed_out=false`。
- `TestWaitAnyAlreadyFinished`：调用前已终态的任务立即返回。
- `TestWaitAnyTimeoutReturnsSnapshot`：超时 → `timed_out=true`，快照含当前状态。
- `TestWaitAnyContextCancel`：ctx 取消返回错误。
- `TestWaitAnyUnknownIDs`：`not_found` 标记，其余正常。
- `renderTaskCompletionBlock` 格式测试：completed / failed（含 error 行）/ stopped、摘要截断。
- Status 瘦身：无 id 只列运行中、响应不含 `content` 字段、单 id 返回摘要。

`internal/ui/bubble/`：

- transcript 框线块渲染测试（三种状态颜色/标记）。
- 任务卡创建/移除测试。

## 范围外（YAGNI）

- 自动弹出 popup（用户已确认裁剪）。
- 任务 resume / `task_id` 恢复语义（P2）。
- subagent 并发限制配置（P2）。
- Codex 式同步前台 run_agent（现状不适用）。
