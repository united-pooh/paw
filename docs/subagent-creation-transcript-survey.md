# Subagent 创建机制与 transcript 展示横评：Pi / OpenCode / Codex

> 调查日期：2026-08-05。全部结论基于官方文档与源码一手资料（见文末来源）。
> 背景：Paw 当前 subagent 模式为 `Subagent` 工具（`run_mode: sync|background`）+ `SubagentStatus` 轮询 + `SubagentStop`。用户反馈：创建后台 subagent 后模型只能靠反复调用 `SubagentStatus` 轮询，不优雅。本文回答三个问题：
> - **RQ1 创建机制**：Pi / OpenCode / Codex 各自用什么工具/API 创建 subagent？同步还是异步？结果如何回到主 agent？
> - **RQ2 transcript 展示**：三个工具在 TUI / transcript / Web UI 里如何呈现 subagent 活动？
> - **RQ3 对 Paw 的借鉴**：相比"创建即成功 + 轮询 status"，更优雅的模式是什么？

---

## 0. 结论速览（TL;DR）

三个工具**都保留了"创建工具调用立即返回 handle（agent id / task id）"的异步形态**——这一点和 Paw 相同，不是问题所在。真正的"优雅"差异体现在三件事上：

1. **完成时 push，而不是调用方 poll**
   - OpenCode：后台任务完成时，宿主把一条合成消息（`Background task completed: <desc>` + 结构化 `<task>` 块）**注入父会话**，模型下一轮自然看到结果；
   - Codex：`wait_agent` 的 spec 明确写着"agent 到达终态时会收到一条包含相同终态的 notification message"；`spawn_agent` 描述写"its final answer will be provided to you when it finishes"；
   - Pi：异步完成事件 `subagent:async-complete` 只投递到发起 session，"You will be notified automatically when it finishes"。

2. **提供阻塞式 wait 工具，替代反复 status 查询**
   - Codex `wait_agent`：一次工具调用阻塞直到任一目标完成或超时（默认分钟级，spec 原文 "Prefer longer waits (minutes) to avoid busy polling"），支持同时等多个 agent；
   - Pi `subagent_wait`：阻塞版保持工具调用打开直到任务变化；`nonBlocking` 版返回订阅 token、完成时唤醒该 session；
   - OpenCode：前台 `Task` 工具本身就是"等结果"（同步子会话跑完返回）。

3. **工具描述里显式禁止轮询，并引导"去做别的有用工作"**
   - OpenCode 后台返回文本："DO NOT sleep, poll for progress, ask the task for status, or duplicate this task's work"；
   - Codex spawn 指南："Call wait_agent very sparingly… Do not repeatedly wait by reflex. While the subagent is running in the background, do meaningful non-overlapping work immediately"；
   - Pi 的 wait 工具同样避免忙轮询。

**transcript 展示**的共识是：主线程只出现"summary + 可打开的入口"，完整噪音留在子会话/子线程——Codex 的 **agent thread**（从主线程 activity 打开）、OpenCode 的 **child session 导航**（Leader+Down 进入子会话）、Pi 的 **FleetView / fleet inspector**。Paw 已有 `/tasks`、`Ctrl+G` subagent picker 和 token tracer，方向上已接近，缺的是"完成即推送到对话"和"阻塞 wait 工具"。

---

## 1. 研究方法与一手来源

| 工具 | 主要一手来源 | 性质 |
|---|---|---|
| Pi | `pi-subagents` README（nicobailon/pi-subagents，`raw.githubusercontent.com/.../main/README.md`） | 扩展的完整行为契约（工具、参数、事件、UI） |
| Pi | pi.dev/packages 包目录 | 生态（pi-subagents、@tintinweb/pi-subagents、pi-dynamic-workflows 等） |
| OpenCode | `packages/opencode/src/tool/task.ts`（sst/opencode dev 分支） | Task 工具完整源码 |
| OpenCode | opencode.ai/docs/agents 官方文档 | agent 配置、权限、child session 导航键位 |
| OpenCode | cefboud.com "How Coding Agents Actually Work: Inside OpenCode" | 架构剖析（子会话、SSE、tool part） |
| Codex | developers.openai.com/codex/subagents 官方文档 | subagent workflow、agent thread、自定义 agent 配置 |
| Codex | `codex-rs/core/src/tools/handlers/multi_agents_spec.rs`（openai/codex main） | spawn_agent / wait_agent / send_input 等完整工具 spec |
| Codex | `codex-rs/core/src/tools/registry.rs` | spawn_agent 的 hook 接线（multi_agent_v1 namespace） |
| Codex | learn.chatgpt.com/docs/codex/cli | CLI 功能清单（subagents、codex cloud、codex exec） |

---

## 2. Pi（pi.dev 生态：pi-subagents 扩展）

Pi 本体（earendil-works/pi，即 pi-mono）本身**没有内置 subagent**；subagent 能力由扩展提供，事实标准是 `pi-subagents`（`pi install npm:pi-subagents`）。Pi 是"最小内核 + 扩展"哲学，因此 subagent 的契约完全体现在扩展的工具与事件里。

### 2.1 创建机制（RQ1）

- 暴露给模型的工具名就是 **`subagent`**，形态是"一个工具、多种 action"：
  - 执行：`{ agent: "scout", task: "分析 auth 流", async: true }`
  - 管理：`{ action: "list" | "get" | "create" | "update" | "delete" | "disable" | "enable" }`
  - 控制：`{ action: "status" | "steer" | "interrupt" | "stop" | "resume" | "append-step" | "doctor" }`
  - 编排：`{ workflowScript: "const scan = await runs.run(...); return (await runs.all([...])).map(r => r.output)" }`
- **异步是默认**："Tool calls start background work by default. Set `async: false` when the current turn needs a foreground result"。
- **前台**："Foreground runs stream in the conversation while they run"——前台结果直接流式进对话；默认 30 分钟 wall-clock 超时。
- **后台**：控制权立即交还；完成时**自动通知**发起 session（事件 `subagent:async-complete`，"You will be notified automatically when it finishes"）。成功完成保持安静（避免未读标记），失败/暂停立即通知；多个成功完成会按 `completionBatch` 合并为一条分组通知。
- **等待工具 `subagent_wait`**：
  - 阻塞版：`subagent_wait({ id: "..." })` 让当前工具调用保持打开，直到该 run 状态变化；
  - `nonBlocking: true`：立即返回订阅 token，完成/失败/超时唤醒该 session（非阻塞订阅，不算活动子任务）。
- **可观测性**：每个异步 run 在 `asyncDir` 下写 `status.json`、`events.jsonl`、`output-<n>.log`、`subagent-log-<runId>.md`，最终 summary 写 `<runId>.json`。状态字段含 `runId/sessionId/mode/state/startedAt/.../totalTokens/totalCost/model/toolCount/turnCount/children`（嵌套 fanout 时 children 呈树）。
- **续跑/复用**：`resume`（从旧 session 文件复活）、`steer`（对运行中 child 发消息）、`append-step`（给运行中的 chain 追加步骤）；child 可通过 `contact_supervisor` 反向联系父 session。
- 递归防护：`maxSubagentDepth`（默认 2 层：main → sub → sub-sub）；子 agent 默认**不注册** `subagent` 工具（除非显式 `tools: subagent`）。

### 2.2 transcript 展示（RQ2）

- **FleetView**：编辑器下方常驻面板，默认显示 `main` + 活动 children 的"任务 / 已运行时长 / token 合计"；`↓`/`←` 激活后用 `↑↓`/`j/k` 选中、`Enter` 打开该 child 的 transcript。
- **`/subagents-fleet`**：live fleet inspector——当前会话的前台工作 + 近期异步 children + 结构化 Markdown/tool transcript + 完成后的 output/session 路径；对选中的 live child 可 `s`（steer）或 `D`（stop）。
- **in-chat workflow card**：前台 workflow（`async:false`）在同一 repo 时渲染 live in-chat card（`chatProgress: live-card`）。
- 状态/进度：前台 run 显示紧凑 live progress（当前工具、token 数、成本、时长、chain 流程线 `done scout → running planner`）。
- 会话共享：`share: true` 可导出 HTML session 到 gist。

### 2.3 小结

Pi 的模式 = **异步默认 + 完成 push 通知 + 阻塞/订阅式 wait 工具 + 常驻 fleet 面板**。它是三者中 UI 最"仪表盘化"的。

---

## 3. OpenCode（sst/opencode）

OpenCode 的 subagent 是**一等公民**：`mode: "subagent"` 的 agent 定义 + 内置 `task` 工具。用户可用 `@mention` 手动调用，模型可经 Task 工具自动调用。

### 3.1 创建机制（RQ1）

- 工具名 **`task`**（`packages/opencode/src/tool/task.ts`），参数：
  - `description`（3-5 词任务简述）、`prompt`（任务全文）、`subagent_type`（agent 名）
  - `task_id`（可选）：**续跑**——传入之前的 task_id 会继续同一个子会话而不是新建
  - `command`、`background`（可选，实验特性，需 `OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS=true`）
- 工具描述由运行时动态生成：列出所有 `mode !== "primary"` 的 agent 及其描述；`permission.task` 设为 `deny` 的 agent 会**从描述中整个移除**（模型根本不知道它存在）。
- **前台（默认）**：Task 工具像普通工具一样同步执行——`execute` 内部：
  1. `sessions.create({ parentID: ctx.sessionID, title: description + " (@agent subagent)" })` 创建 child session；
  2. 在 child session 上跑 `Session.prompt(...)`（child 使用自己的 agent 配置：model/tools/permission）；
  3. 取 child 最后一条 text part 作为工具输出，包成结构化文本返回：
     ```
     <task id="<sessionID>" state="completed"><task_result>…</task_result></task>
     ```
- **后台（实验）**：立即返回 `<task id=… state="running">` + 固定提示文本（`BACKGROUND_STARTED`）：
  > "The task is working in the background. You will be notified automatically when it finishes. **DO NOT sleep, poll for progress, ask the task for status, or duplicate this task's work** — avoid working with the same files or topics it is using. Work on non-overlapping tasks…"
  完成时 `injectBackgroundResult` 把合成消息注入父会话（`Background task completed: <desc>` + 同样的 `<task>` 块）。
- 深度限制：`subagent_depth`（默认 1，即只允许一层）；权限：`permission.task` 支持 glob 精细控制（如 `{"*": "deny", "orchestrator-*": "allow"}`）。
- 子会话权限从父会话**派生**（`deriveSubagentSessionPermission`），并默认禁止 child 再调用 `task`/`todowrite`（防递归）。

### 3.2 transcript 展示（RQ2）

- **child session 导航**（官方 docs）：
  - `session_child_first`（默认 `<Leader>+Down`）：从父会话进入第一个子会话；
  - `session_child_cycle`（`Right`）/ `session_child_cycle_reverse`（`Left`）：子会话间切换；
  - `session_parent`（`Up`）：返回父会话。
  即 TUI 里可以像文件树一样进入子 agent 的完整 transcript。
- 工具调用本身是结构化 message part（`tool-call` → running → `tool-result`），TUI 渲染为可展开/折叠的工具块；Task 工具的结果以 `<task>` 块呈现。
- 第三方 runtime（assistant-ui OpenCode runtime）的描述："task tool calls expose their child session through tool metadata… projects their transcripts into `ToolCallMessagePart.messages`"——子会话 transcript 可被投影进父级消息树。

### 3.3 小结

OpenCode 的模式 = **前台同步默认（一次工具调用拿到完整结果）+ 结构化 `<task>` 结果 + 后台完成 push 注入 + 子会话可导航**。它是三者中"工具语义最接近普通工具"的（后台需要显式 opt-in）。

---

## 4. Codex（openai/codex + Codex cloud）

Codex 在 2026 年把 multi-agent 做成了内置能力（`agents.enabled` 默认 true），CLI/桌面端/IDE 一致可用。

### 4.1 创建机制（RQ1）

工具集定义在 `codex-rs/core/src/tools/handlers/multi_agents_spec.rs`，v1 是 `multi_agent_v1` namespace，v2 为扁平函数；核心工具：

| 工具 | 参数 | 语义 |
|---|---|---|
| `spawn_agent` | `message`（初始任务）、`items`、`agent_type`（default/worker/explorer/自定义；省略则继承父类型 + full-history fork）、`fork_context`（v1 bool）/ `fork_turns`（v2：`none`/`all`/正整数）、`model`、`reasoning_effort`、`service_tier`；v2 另需 `task_name` | **创建即返回**：v1 返回 `{agent_id, nickname}`，v2 返回 `{task_name, nickname}`。描述："Spawn a sub-agent for a well-scoped task… The spawned agent will have the same tools as you and the ability to spawn its own subagents." |
| `wait_agent` | `targets`（v1，可多个，等待**任一**先完成）、`timeout_ms`（默认分钟级） | 阻塞等待。返回按 agent id 的终态表；"Once the agent reaches a final status, **a notification message will be received** containing the same completed status"。spec 明确："Prefer longer waits (minutes) to avoid busy polling" |
| `send_input` / `send_message` / `followup_task` | `target` + `message`（+`interrupt`） | 给已有 agent 发消息/续任务；`send_input` 描述："You should reuse the agent by send_input if you believe your assigned task is highly dependent on the context of a previous task" |
| `list_agents` | `path_prefix` 可选 | 列出当前 root thread tree 的 live agents 及状态 |
| `close_agent` | `target` | 关闭 agent 及其后代；"Completed agents remain open and count toward the concurrency limit until closed" |
| `interrupt_agent` | `target` | 中断当前 turn，agent 仍可接收后续消息 |
| `resume_agent` | `id` | 恢复已关闭 agent 以继续收消息 |

- **结果回传**：完成时宿主把终态 + 最终回答交给主 agent（"its final answer will be provided to you when it finishes"）。官方文档："ChatGPT or Codex handles orchestration across agents, including spawning new subagents, routing follow-up instructions, waiting for results, and closing agent threads. When many agents are running, Codex waits until all requested results are available, then returns a consolidated response."
- **触发方式**：用户直接要求（"spawn two agents" / "delegate this work in parallel"），或 AGENTS.md / skill instructions 要求。工具描述里严格限制："Do not spawn sub-agents unless the user or applicable AGENTS.md/skill instructions explicitly ask… Requests for depth, thoroughness, research… do not count as permission to spawn."
- **防轮询约束**（工具描述原文）："Call wait_agent very sparingly. Only call wait_agent when you need the result immediately for the next critical-path step… Do not repeatedly wait by reflex. While the subagent is running in the background, do meaningful non-overlapping work immediately."
- **agent 配置**：内置 `default` / `worker` / `explorer`；自定义 agent = `~/.codex/agents/*.toml`（个人）或 `.codex/agents/*.toml`（项目），必填 `name`/`description`/`developer_instructions`，可选 `model`/`model_reasoning_effort`/`sandbox_mode`/`mcp_servers`/`skills.config`；全局 `[agents]` 表控制 `enabled`、`max_concurrent_threads_per_session`、`default_subagent_model`、`default_subagent_reasoning_effort`、`interrupt_message`。优先级：显式 spawn 值 > `[agents]` 默认 > 父值。
- **`codex exec` / cloud**（补充形态）：`codex exec` 是脚本/CI 的非交互单跑；`codex cloud` 可浏览/提交 cloud chats、从终端把结果 apply 回本地仓库；Codex cloud 支持并行 cloud tasks。
- **多写注意**：官方文档建议并行 agent 用于读重任务（exploration/tests/triage/summarization），写重并行需谨慎（文件冲突）。

### 4.2 transcript 展示（RQ2）

- 官方文档："The app surfaces **each subagent thread** so you can inspect its work and the summary returned to the main chat."
- "Open a subagent thread **from the activity shown in the main thread** to inspect its work"——主线程里出现 activity 条目，点击展开对应 agent thread（含完整中间输出）；主线程本身只收 summary。
- 设计动机明确：避免 context pollution / context rot——"Keep the main agent focused on requirements, decisions, and final outputs. Run specialized subagents in parallel for exploration… Return summaries from subagents instead of raw intermediate output."
- CLI 文档把 `subagents` 列为能力："Split up a larger investigation. Ask Codex to delegate focused work to specialized agents, then bring their findings back into the main terminal session."

### 4.3 小结

Codex 的模式 = **完整生命周期工具集（spawn → 并行工作 → 阻塞 wait / 完成通知 → close）+ 主线程只见 summary、子线程可打开 + 严格的防轮询/防误用 prompt 约束**。它是三者中"工具面最完整、约束最细"的。

---

## 5. 横向对比

| 维度 | Pi (pi-subagents) | OpenCode | Codex | Paw 现状 |
|---|---|---|---|---|
| 创建工具 | `subagent`（agent+task，或 workflowScript） | `task`（description/prompt/subagent_type/task_id/background） | `spawn_agent`（message/agent_type/fork_turns/model/…） | `Subagent`（prompt/description/context_mode/run_mode） |
| 默认运行模式 | async（后台） | foreground（后台需实验开关） | 后台 + 宿主编排 | background（默认） |
| 创建返回 | 立即返回（async）；前台则流式进对话 | 前台：`<task state="completed">`；后台：`<task state="running">` + id | 立即返回 `{agent_id, nickname}` / `{task_name, nickname}` | 立即返回 TaskSnapshot（id/status/…） |
| 完成回传 | 自动通知发起 session（`subagent:async-complete`）；成功静默、失败即时报 | 后台完成时注入合成消息 `Background task completed: …` + `<task>` 块 | notification message 含终态；"final answer will be provided to you when it finishes" | UI 系统通知 + `SubmitSupplement` 注入截断结果（有基础，机制未完整） |
| 阻塞等待 | `subagent_wait`（阻塞长等待 / nonBlocking 订阅 token） | 前台即同步等待；无独立 wait 工具 | `wait_agent`（多 targets、分钟级超时、防忙轮询） | 无（只有 `SubagentStatus` 轮询） |
| 轮询约束 | 提示"将被自动通知"，避免忙轮询 | "DO NOT sleep, poll for progress, ask the task for status" | "Do not repeatedly wait by reflex" | 工具描述未约束 → 模型盲轮询 |
| 状态查询 | `subagent({action:"status"})`（可按 id / fleet / transcript view） | 无独立 status 工具（进子会话看） | `list_agents`（live agents + 状态） | `SubagentStatus`（全量任务 + 完整 Content，输出巨大） |
| 续跑/复用 | `resume` / `steer` / `append-step` / `contact_supervisor` | `task_id` 续跑同一 child session | `send_input` / `followup_task` / `resume_agent` | — |
| 停止 | `stop` / `interrupt` | 子会话 abort | `interrupt_agent` / `close_agent` | `SubagentStop` |
| transcript 展示 | FleetView 常驻面板、`/subagents-fleet`、in-chat card、live progress | child session 导航（Leader+Down 进入）、结构化 tool part、`<task>` 块 | agent thread 从主线程 activity 打开，主线程只见 summary | `/tasks`、`Ctrl+G` picker、token tracer dashboard（方向接近） |
| 并发/深度限制 | `maxSubagentDepth`、`maxSubagentSpawnsPerSession`、`globalConcurrencyLimit` | `subagent_depth`（默认 1）、`permission.task` | `max_concurrent_threads_per_session`、自定义 agent 沙箱 | `maxDepth=4`（递归深度） |

---

## 6. 对 Paw 的可执行建议（RQ3）

按"改动小、收益大"排序：

### 6.1 【P0】新增阻塞式 `SubagentWait` 工具，消灭盲轮询

- 契约对齐 Codex `wait_agent` / Pi `subagent_wait`：
  ```json
  { "id": "<task-id>", "timeout_ms": 120000 }
  ```
- 实现：`Manager` 已有 `waitBackground`/`finishTask`/`Process.Wait()`，加一个 `Wait(ctx, id, timeout)` 即可——按 id 取 `running[id]`，`select` 在 `process.Wait()` 完成与超时之间；完成返回完整 `TaskSnapshot`（含 Content），超时返回 `timed_out: true` + 当前状态。
- 语义要点（照抄三家）：**一次调用等到完成**；支持一次等多个（可选 `ids: []`，任一完成即返回，对应 Codex targets）；超时默认给长一点（分钟级），避免模型把它当轮询用。

### 6.2 【P0】后台完成 push 入对话（模型可见），而不是只发 UI 通知

- 现有 `submitTaskContext`（`ContextSink.SubmitSupplement`）已经注入"截断后的结果"，但存在两个缺口：
  1. supplement 是"补充指令"，在 Runner 的 supplements 队列里按轮次检查，**模型正在生成过程中完成的**任务其结果可能错过当轮（README：supplement 注入"只对当前 turn 生效"、`RunTurn` 在"每轮开始时检查"）；
  2. 注入文本是自由格式的 key-value 文本，UI/模型都没有结构化标记。
- 建议：完成时构造 OpenCode 式结构化块并**保证进入下一次模型请求**：
  ```
  <task id="…" state="completed"><task_result>…</task_result></task>
  ```
  要么把完成事件写进 session journal 作为一条 synthetic user message（重启/恢复也能看到），要么保证 supplement 在下一轮请求前必达（必要时由 loop 在请求前 flush 完成队列）。
- 同时更新 `Subagent` 工具描述：说明后台模式"完成时自动通知，请勿轮询；确需同步等待请用 `run_mode: sync` 或 `SubagentWait`"。

### 6.3 【P1】`SubagentStatus` 瘦身

- 现状：`ListTasks()` 返回**全部**历史任务（内存+磁盘合并），且 `TaskSnapshot.Content` 携带完整结果——调用一次会收到巨量 JSON（当前主模型每次 status 都吃到几百 KB 历史任务列表）。
- 建议：
  - 无 `id` 时只返回**运行中**任务摘要（id/name/status/elapsed/depth），不返回 content；
  - 指定 `id` 时才返回详情（content 可截断或给出 transcript/output 路径）；
  - 历史任务查询改为分页或按需（`/tasks` 已有 UI 入口，模型侧不必全量）。

### 6.4 【P1】transcript 展示：结构化任务块 + 完成可见性

- 工具结果统一带 `state` 标记（如 `<task id state>` 或 JSON `state` 字段），TUI 的 tool entry 渲染识别后显示为可折叠块（Paw 的 `transcriptEntry` 已有 tool 分组/折叠基础）；
- FleetView 式常驻状态（可选）：Pi 的做法是编辑器下方一行常驻显示活动 children；Paw 可考虑在状态行/queue 面板展示运行中任务数，避免任务"静默"；
- 保留并强化"主线程只见 summary、完整内容在子 transcript"（Paw 已有 transcript_path/output_path，方向正确）。

### 6.5 【P2】生命周期补全

- `Subagent` 支持 `task_id` 续跑语义（OpenCode `task_id` / Codex `send_input` / Pi `resume`）——对"继续上一个 agent 的上下文"很有用；
- 并发上限配置化（`max_concurrent_threads_per_session` 式），替代/补充现在的递归 `maxDepth`；
- 保留 `sync` 模式作为"前台"等价物（对应 OpenCode 默认前台），并在描述里引导：**需要结果就 sync，独立任务才 background**。

### 6.6 为什么这些方案有效（机制解释）

Paw 的"不优雅"不是"创建即返回"，而是**把状态同步完全押在调用方的 status 轮询上**。三家工具证明的替代组合是：

```
创建（立即返回 handle）
  + 完成 push（宿主注入，模型无需主动查）
  + 阻塞 wait（模型确实被阻塞时的一次性等待）
  + 显式"禁止轮询"的 prompt 约束
```

这套组合让模型把 token 花在"继续做其他有用工作"上，而不是花在一连串 `SubagentStatus` 上——这正是当前对话里观察到的浪费模式。

---

## 7. 参考来源

**Pi**
- https://github.com/nicobailon/pi-subagents （README：subagent 工具、subagent_wait、FleetView、事件、生命周期 artifacts）
- https://pi.dev/packages （pi-subagents、@tintinweb/pi-subagents、pi-dynamic-workflows 等包目录）
- https://github.com/badlogic/pi-mono （Pi agent harness 仓库；注意与 OpenClaw 内嵌的 "Pi" 是不同项目）

**OpenCode**
- https://raw.githubusercontent.com/sst/opencode/dev/packages/opencode/src/tool/task.ts （Task 工具源码：参数、child session、renderOutput、BACKGROUND_STARTED、injectBackgroundResult）
- https://opencode.ai/docs/agents （agent 配置、permission.task、child session 导航键位）
- https://cefboud.com/posts/coding-agents-internals-opencode-deepdive （架构剖析：子会话、tool part、SSE、AI SDK loop）
- https://www.assistant-ui.com/docs/runtimes/opencode/overview （task tool 元数据暴露 child session、transcript 投影）

**Codex**
- https://developers.openai.com/codex/subagents （subagent workflow、agent thread、内置/自定义 agent、[agents] 配置）
- https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/tools/handlers/multi_agents_spec.rs （spawn_agent/wait_agent/send_input/list_agents/close_agent/interrupt_agent/resume_agent 完整 spec）
- https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/src/tools/registry.rs （spawn_agent hook 接线、multi_agent_v1 namespace）
- https://learn.chatgpt.com/docs/codex/cli （CLI 能力清单：subagents、codex exec、codex cloud）
- https://developers.openai.com/codex/cli （CLI 概览）

**Paw 现状（本仓库）**
- `internal/subagent/manager.go`（Subagent/SubagentStatus/SubagentStop 工具、Manager.Run/Stream/Launch/Status/ListTasks、submitTaskContext）
- `README.md`（/subagent、/tasks、Ctrl+G、token tracer、SubmitSupplement 语义）
