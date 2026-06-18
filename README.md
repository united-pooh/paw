# go-code

一个最小可运行的本地 coding agent。

本文档只描述当前代码中已经实现的内容，按入口、分层、结构体、接口、函数、扩展点组织，风格接近 API 文档。

## 术语约定

- Go 没有“类”，本文中的“类”对应 `struct`
- “抽象层”对应接口或包边界
- “扩展点”只指当前代码已经预留、可以稳定接入的位置

## 目录

```text
cmd/agent/main.go

internal/message/types.go

internal/model/config.go
internal/model/types.go
internal/model/client.go
internal/model/stream.go
internal/model/anthropic_stream.go

internal/tool/tool.go
internal/tool/register.go
internal/tool/file/path.go
internal/tool/file/ls.go
internal/tool/file/read.go
internal/tool/file/write.go
internal/tool/file/grep.go
internal/tool/file/glob.go
internal/tool/exec/bash.go
internal/tool/webfetch/webfetch.go

internal/session/jsonl_store.go
internal/settings/settings.go
internal/subagent/manager.go
internal/streamma/

internal/ui/ui.go
internal/ui/headless/headless.go
internal/ui/bubble/

internal/loop/runner.go
```

## 快速使用

```bash
go run ./cmd/agent -p "hello"
go run ./cmd/agent
go run ./cmd/agent -s <session-id>
```

- 不加参数直接启动时，每次都会创建一个全新的空会话。
- 需要恢复历史会话时，使用 `-s <session-id>` 指定会话 ID；也可在交互界面输入 `/sessions` 浏览并恢复历史会话。

当前运行目录会作为工作区 root，同时也是 `.ccagent/` 状态目录的基准路径。

## 入口

文件: [cmd/agent/main.go](./cmd/agent/main.go)

### `main()`

职责:
- 解析命令行参数
- 构造 `Runner`
- 三选一运行不同模式：
  1. `-subagent-worker` → `runSubagentWorkerMode()`
  2. `-p "xxx"` → `runSingleTurnMode()`
  3. 无参数 → `runInteractiveMode()`

不负责:
- 直接调用模型
- 直接执行工具
- 维护对话状态

#### `parseOptions() options`

职责:
- 读取 `-p`
- 读取 `-s`
- 读取隐藏的 `-subagent-worker`

行为:
- `-p` 有值: 执行单轮
- `-p` 为空: 进入交互式对话界面
- `-s` 有值: 绑定到指定 session 并恢复历史
- `-s` 为空: 每次启动都创建一个全新空 session
- `-subagent-worker`: 以子进程方式运行，从 stdin 读取 `WorkerRequest`（JSON），执行后输出 `WorkerResult` 到 stdout

#### `buildRunner(ctx, sessionIDFlag, output) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, error)`

职责:
- 加载模型配置
- 创建模型客户端和会话存储
- 装配 settings 控制器和 subagent 管理器
- 注册文件、shell、webfetch、subagent 相关工具
- 返回 `Runner`、sessionID 和运行时控制器

这是当前的依赖装配点。

当前会注册的持久化目录:
- `.ccagent/model.json`
- `.ccagent/settings.json`
- `.ccagent/exports/`
- `.ccagent/sessions/<sessionID>/`

#### `runSingleTurnMode(ctx, opts) error`

职责:
- 在 headless UI 中执行一次 `runner.RunTurn`

输出:
- assistant 最终结果写 stdout
- 当前 sessionID 写 stderr

#### `runInteractiveMode(ctx, opts) error`

职责:
- 启动 Bubble Tea 主界面
- 注入模型配置、settings、subagent 控制器
- 以当前 session 进入可恢复的交互式对话

#### `runSubagentWorkerMode(ctx)`

职责:
- 以子进程模式运行（由主 Agent 进程 fork 启动）
- 从 stdin 读取 JSON 格式的 `WorkerRequest`（含 `ParentSessionID`、`Prompt`、`Tools`、`MaxTurns` 等）
- 构建带有 subagent 上下文的 Runner（通过 `subagentRuntimeContext` 控制递归深度）
- 执行 `runner.RunTurn()`，将 `WorkerResult`（含 TaskID、SessionID、Content、Error、ExitCode）写入 stdout

#### 当前交互命令

当前 slash command 由 `internal/ui/bubble/command_registry.go` 统一注册，`/help` 会显示参数提示。

- `/help`
- `/model [status|custom|deepseek]`
- `/export [filename]`
- `/setting`
- `/sessions`
- `/subagent [--fork|--empty] [--background|--sync] <prompt>`
- `/streamma <prompt>`
- `/tasks`
- `/status`
- `/clear`
- `/exit` / `/quit`

当前行为:
- `/model` 无参数时打开 provider 向导；`status` 只输出当前配置；`custom`、`deepseek` 直接切换并持久化到 `.ccagent/model.json`；`deepseek` 需要 `DEEPSEEK_API_KEY`
- `/export` 默认导出到 `.ccagent/exports/conversation-YYYY-MM-DD-HHMMSS.txt`，也支持工作区内显式路径；导出文件权限为 `0600`
- `/setting` 通过向导保存默认 subagent context/run mode，以及 context meter 的位置和 token limit
- `/sessions` 列出所有历史会话（ID 前缀、日期、文件大小、首条消息），选中条目后直接恢复该会话
- `/subagent` 支持 `empty` 与 `fork` 两种上下文模式，以及 `sync` 与 `background` 两种运行模式；后台任务完成后会发 UI 系统通知，并把截断后的结果作为补充上下文注入后续模型轮次（完整结果仍在任务 output/transcript 路径中）
- `/streamma <prompt>` 显式把当前任务交给 StreamMA A→B→D runtime；内部按 `END_STEP` step 边界流式 fanout，默认不向 agent 传工具，最终由 D 的最后一步作为 assistant 回复写回会话历史
- `/tasks` 展示当前后台 subagent 任务及 transcript 路径

#### 当前输入区状态

context meter 默认展示在消息历史区下方、输入框上方；输入框保持在窗口底部，不再显示 `Input`、`Waiting`、`Terminal` 标签。

输入补全:
- 在输入框中输入 `/` 会弹出斜杠命令候选列表
- 在输入框中输入 `@` 会弹出工作区文件路径候选列表
- 使用 ↑↓ 键在候选项之间导航，Tab 或 Enter 确认补全，Esc 关闭弹窗

context meter 的 token 数只来自模型服务端返回的真实 `usage` 字段；不会根据草稿、历史文本或本地字符数做估算。左侧 `↑/↓` 数字展示本次打开后的 session 累计 token 消耗，每次启动从 0 开始，`/clear` 也会清零；每次模型请求的 input/prompt、output/completion 与 cache hit 会按 provider 返回值入账，同一条流里多次 `usage` 会先合并成该请求的累计值再计入 session，避免 `message_start` / `message_delta` 重复计数。进度条、used 百分比、cache hit 百分比和 `free(...)` 仍然展示最近一次真实 usage 对应的当前上下文窗口占用；新一轮请求尚未返回 usage 时，会继续显示上一条上下文窗口 usage。

context meter 左侧显示紧凑 token 与比例，例如 `260k↑ 2.05k↓ 25%(10%)`：`↑` 是 session 累计上传/input token，`↓` 是 session 累计回答/output token，两个百分比分别是当前 context 用量和当前 cache hit 用量占总 limit 的比例。右侧只显示当前 context 剩余比例，例如 `free(75%)`。超过三位的 token 会压缩成 `k`，超过 `999k` 会压缩成 `M`，数字最多保留三位有效数字。

快捷键:
- `ctrl+o`: 展开/折叠模型 thinking 过程；折叠时 thinking 仍保存在 transcript 中，但不渲染到 viewport。

当前默认 settings:

```json
{
  "subagent": {
    "default_context_mode": "empty",
    "default_run_mode": "sync"
  },
  "ui": {
    "context_limit_tokens": 1048576,
    "context_meter_location": "input-above"
  }
}
```

## 分层

当前分为 5 层。

补充:
- `session`、`settings`、`subagent`、`streamma` 是新增的运行时支撑模块，负责 `.ccagent/` 持久化、用户默认配置、子代理调度和 StreamMA runtime；主对话链路仍按下面的 5 层理解即可。
- `internal/streamma` 是独立的内存版 multi-agent runtime，目前覆盖 fake model + runtime 验收，并通过 `/streamma <prompt>` 接入 `loop.Runner` 的显式分支；生产版 NATS、Postgres、MinIO 适配器仍未接入。

### 1. `message`

职责:
- 定义统一消息模型

边界:
- 不知道模型协议细节
- 不知道 UI
- 不知道工具注册和执行

#### 核心类型

##### `type Role string`

三个角色常量：
- `RoleSystem` — 系统提示
- `RoleUser` — 用户消息
- `RoleAssistant` — 助手（模型）消息

##### `type Message struct`

字段:
- `Role` — 消息角色
- `Content` — 文本内容
- `ToolUse` (`*ToolCall`) — 工具调用请求（assistant 发出）
- `ToolResult` (`*ToolResult`) — 工具执行结果（user 角色发出）

##### `type ToolCall struct`

字段:
- `ID` — 调用唯一标识
- `Name` — 工具名称
- `Input` (`json.RawMessage`) — 调用参数（原始 JSON 字节，延迟解析）

##### `type ToolResult struct`

字段:
- `ToolUseID` — 对应 ToolCall 的 ID
- `Content` — 执行结果文本
- `IsError` — 是否出错

消息模型遵循 Claude/Anthropic 风格的对话格式：assistant 消息可包含工具调用请求，user 消息可包含工具执行结果，实现多轮工具闭环。

### 2. `model`

职责:
- 负责和模型服务通信
- 负责 HTTP 请求/响应
- 负责把流式响应转成事件

边界:
- 不负责对话循环
- 不负责工具执行
- 不负责 stdout 渲染

### 3. `tool`

职责:
- 定义工具接口
- 提供工具注册表
- 提供具体工具实现

边界:
- 不知道模型
- 不知道 UI
- 不知道 turn loop

### 4. `ui`

职责:
- 接收 loop 产生的渲染事件
- 决定如何输出到终端

边界:
- 不调用模型
- 不执行工具

### 5. `loop`

职责:
- 驱动 agent turn
- 调模型
- 识别 tool use
- 调工具
- 回灌 tool result
- 维护内存中的对话历史
- 记录最近一次模型流返回的真实 usage，供 context meter 展示

这是当前系统的协调层。

## 运行链路

当前运行链路如下:

```text
main
  -> buildRunner
  -> loop.Runner.RunTurn
      -> model.Client.StreamMessage
      -> ui.OnThinkingDelta / ui.OnAssistantDelta / ui.OnDone
      -> parse tool_use
      -> tool.Registry.Get
      -> tool.Run
      -> ui.OnToolCall / ui.OnToolResult
      -> model.Client.StreamMessage
      -> final assistant message
```

Subagent 工作模式链路:

```text
main (-subagent-worker)
  -> read WorkerRequest from stdin
  -> buildRunnerWithSubagentContext (with depth/parentTaskID)
  -> runner.RunTurn
  -> write WorkerResult to stdout
  -> exit
```

## 包级 API

### `internal/message`

文件: [types.go](./internal/message/types.go)

#### `type Role string`

角色枚举。

当前值:
- `RoleSystem`
- `RoleUser`
- `RoleAssistant`

#### `type Message struct`

统一消息结构。

字段:
- `Role`
- `Content`
- `ToolUse`
- `ToolResult`

用法:
- 普通文本消息: `Role + Content`
- 工具调用消息: `Role + ToolUse`
- 工具结果消息: `Role + ToolResult`

#### `type ToolCall struct`

表示 assistant 发起的一次工具调用。

字段:
- `ID`
- `Name`
- `Input`

#### `type ToolResult struct`

表示工具执行结果。

字段:
- `ToolUseID`
- `Content`
- `IsError`

### `internal/model`

#### 配置层

文件: [config.go](./internal/model/config.go)

##### `type Config struct`

模型连接参数。

字段:
- `APIBaseURL`
- `APIPath`
- `APIKey`
- `Model`
- `Timeout`

##### `LoadConfigFromEnv() (Config, error)`

职责:
- 从环境变量构造 `Config`
- 启动时按顺序尝试加载当前目录下的 `.env`、`.env.local`
- 读取并合并 `.ccagent/model.json` 中的持久化 provider 配置
- `.env.local` 会覆盖 `.env` 和外部 shell 继承进来的同名变量

当前环境变量:
- `NEWAPI_API_KEY`
- `DEEPSEEK_API_KEY`

当前 provider:
- `custom`
- `deepseek`

当前默认值（`custom`）:
- base url: `http://localhost:8317/v1`
- path: `/chat/completions`
- model: `gpt-5.5`
- 缺省 key: `sk-dummy`

当前默认值（`deepseek`）:
- base url: `https://api.deepseek.com`
- path: `/chat/completions`
- model: `deepseek-chat`
- 缺少 `DEEPSEEK_API_KEY` 时启动会报错
- 流式调用会优先尝试 DeepSeek Anthropic Messages 端点以尽早获得 `message_start.usage.input_tokens`；如果 Anthropic 建流失败，则回退到 `/chat/completions` OpenAI-compatible 流。

#### 请求/响应类型

文件: [types.go](./internal/model/types.go)

##### `type ChatCompletionsRequest struct`

字段:
- `Model`
- `Messages`
- `Stream`

##### `type ChatCompletionsResponse struct`

字段:
- `Choices[].Message`
- `Error`

这是当前最小响应投影，不是供应商完整 schema。

#### 客户端

文件: [client.go](./internal/model/client.go)

##### `type Client struct`

职责:
- 发送 HTTP 请求
- 解析同步响应

字段:
- `httpClient`
- `cfg`

##### `NewClient(cfg Config) *Client`

职责:
- 创建模型客户端

##### `RunMessage(ctx, messages) (string, error)`

职责:
- 发起一次非流式请求

当前状态:
- CLI 主路径不依赖它
- 仍然保留作为同步调用能力

#### 流式层

文件: [stream.go](./internal/model/stream.go)

##### `type StreamEvent struct`

字段:
- `Delta`
- `Thinking`
- `Done`
- `Err`
- `Usage`

约定:
- 一次事件只表达一种状态

##### `StreamMessage(ctx, messages) (<-chan StreamEvent, error)`

职责:
- 发起流式请求
- 返回事件 channel

##### 关键内部函数

这些函数是 `model` 包内部的流式拆分点，不是外部扩展接口:

###### `newStreamScanner(body) *bufio.Scanner`

职责:
- 创建并配置 SSE 行扫描器

###### `handleStreamLine(ctx, line, events) (done bool, err error)`

职责:
- 处理单行 SSE 文本

###### `handleStreamPayload(ctx, payload, events) (done bool, err error)`

职责:
- 处理 `data:` 后的 payload

###### `decodeStreamChunk(payload) (chatCompletionsStreamResponse, error)`

职责:
- JSON 解码

###### `emitChunkEvents(ctx, chunk, events) bool`

职责:
- 把 chunk 转成 `StreamEvent`

###### `consumeStream(ctx, resp, events)`

职责:
- 后台消费整个 SSE 响应流

### `internal/streamma`

目录: [internal/streamma](./internal/streamma)

职责:
- 提供内存版 StreamMA multi-agent runtime
- 用 fake model 验证 `Exact+END_STEP` step 契约、增量 step fanout、append-only 上下文、DAG 调度、EOF/drain、fail-fast 和 replay

边界:
- 不接入真实模型 provider、NATS、Postgres 或 MinIO
- 不执行 stream 中出现的工具动作
- 不改变 `loop.Runner` 的单 agent 主链路

#### 核心类型

##### `type GraphSpec struct`

字段:
- `RunID`
- `Protocol`
- `StepPolicy`
- `Agents`
- `Edges`

职责:
- 描述 Chain、Tree、Graph DAG 拓扑
- source 节点接收 problem
- 每个 step 边界闭合后立即广播给 direct successors，不等待上游模型流整体结束

##### `type AgentSpec struct`

字段:
- `ID`
- `Role`
- `SystemPrompt`

职责:
- 描述单个 StreamMA agent 的稳定系统提示

##### `type EdgeSpec struct`

字段:
- `From`
- `To`

职责:
- 描述 DAG 中一条 direct successor 关系

##### `type StepPolicy struct`

字段:
- `Boundary`
- `MaxStepBytes`

当前默认:
- `Boundary` 为空时使用 `END_STEP`
- step contract 是 `Exact+END_STEP`

##### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx, messages, tools) (<-chan model.StreamEvent, error)`

当前 StreamMA runtime 会以 `tools = nil` 调用模型；stream 中的 tool event 默认不会触发工具执行。

##### `type StepPacket struct`

职责:
- Runtime 内部的结构化 step 包
- `Content.Text` 保存模型自然语言响应中的 step 原文内容，只移除独占一行的 sentinel 行

边界:
- LLM 原生输出不是 JSON 或结构体
- `StepPacket` 是系统包装层

##### `type FinalAnswerPacket struct`

职责:
- sink agent drain 后产生最终回答
- 记录 `Answer.Text` 和 `Support.UsedSteps`

##### `type RunResult struct`

字段:
- `RunID`
- `Status`
- `Final`
- `Events`
- `Error`

职责:
- 返回完成或失败后的可审计结果

#### Parser

文件: [parser.go](./internal/streamma/parser.go)

##### `ParseStream(ctx, events, config) ([]StepPacket, error)`

行为:
- 只把单独成行且精确等于 `END_STEP` 的行作为 step 边界
- 保留 step 原文内容，包括前后空白和普通换行
- 代码块内、普通句子内、带额外空白的 `END_STEP` 不触发边界
- 缺失最终 sentinel 但仍有内容时 forced close，并设置 `BoundaryRecovered`
- 超过 `MaxStepBytes` 时返回 parser fatal error

##### `StreamSteps(ctx, events, config, emit) error`

职责:
- 增量读取模型 stream
- 每识别到一个完整 step 就立即调用 `emit(StepPacket)`
- 不等待 `Done` 才返回所有 step

用途:
- Runtime 用它在上游 agent 仍在生成时，把已闭合 step 立刻投递给下游 agent

#### Context 与 Prompt

文件:
- [context.go](./internal/streamma/context.go)
- [prompt.go](./internal/streamma/prompt.go)

##### `type Transcript struct`

职责:
- 每个 agent 保存 append-only transcript
- inbound step 与 own step 按实际处理顺序追加

##### `BuildPrompt(transcript) []message.Message`

职责:
- 从 transcript 构造模型输入
- 固定 system prompt 和 original problem
- 不把 event id、timestamp、trace、seq 等动态 metadata 写进 prompt

##### `BuildPromptSegments(transcript) []PromptSegment`

职责:
- 暴露 cache-stable prompt segment 投影
- 供 prefix cache 相关验收和后续适配器复用

#### Runtime

文件: [runtime.go](./internal/streamma/runtime.go)

##### `NewRuntime(config) (*Runtime, error)`

职责:
- 编译 DAG
- 注入 `ModelStreamer`
- 初始化 broker、event log 和 agent runtime state

##### `RunGraph(ctx, spec, model, problem) (RunResult, error)`

行为:
- Chain、Tree、Graph 都由同一 DAG runtime 执行
- Runtime 边读模型 stream 边提交 `StepPacket`，step 一闭合就 `FanoutStep`
- 下游 agent 可在上游 agent 尚未 `Done` 时启动自己的模型调用
- 多前驱节点是 arrival-triggered，任意前驱 step 到达即可调用，不等待同步 barrier
- 单个 agent 内部仍按队列顺序串行处理 inbound delivery，避免同一 transcript 被多个 invocation 同时改写
- 非 source agent 只有在所有 predecessors EOF、队列 drain、无 active invocation 后结束
- critical agent 调用失败、parser fatal、EOF 不一致等错误会 fail-fast

#### Event Log 与 Replay

文件:
- [event_log.go](./internal/streamma/event_log.go)
- [replay.go](./internal/streamma/replay.go)

##### `type EventLog struct`

职责:
- 记录 problem、step committed、upstream EOF、final answer、run failed 等事件
- 为事件补全 `Seq`、`EventID`、`Timestamp`

##### `Replay(events) ReplaySummary`

职责:
- 不调用模型，仅根据已提交事件重建 step 序列、final/failed summary、failure point
- 根据 step dependencies 重建每个 agent 的 transcript 投影和 lifecycle 状态

用途:
- 将失败 run 重放到同一失败点
- 给验收测试和后续生产适配器提供可审计日志基础

### `internal/tool`

#### 抽象层

文件: [tool.go](./internal/tool/tool.go)

##### `type Tool interface`

当前工具抽象。

方法:
- `Name() string`
- `Description() string`
- `Run(ctx context.Context, input json.RawMessage) (string, error)`
- `InputSchema() json.RawMessage`

扩展规则:
- 新工具只要实现这个接口，并注册到 `Registry`，不需要改 loop 核心逻辑

#### 注册层

文件: [register.go](./internal/tool/register.go)

##### `type Registry struct`

职责:
- 按名称保存工具实例

##### `NewRegistry() *Registry`

职责:
- 创建注册表

##### `Register(tool Tool)`

职责:
- 注册工具

##### `Get(name string) (Tool, bool)`

职责:
- 按名称查找工具

##### `Describe() []string`

职责:
- 生成工具说明文本
- 当前也会附带 `input_schema`

用途:
- 给 `Runner` 拼 system prompt

##### `Definitions() []model.ToolDefinition`

职责:
- 返回 `[]model.ToolDefinition`，用于 LLM API 的原生工具调用请求

#### 文件工具

文件:
- [path.go](./internal/tool/file/path.go)
- [ls.go](./internal/tool/file/ls.go)
- [read.go](./internal/tool/file/read.go)
- [write.go](./internal/tool/file/write.go)
- [grep.go](./internal/tool/file/grep.go)
- [glob.go](./internal/tool/file/glob.go)

##### `type LSTool struct`

字段:
- `Root`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 列出某个目录下的文件名

输入格式:

```json
{
  "path": "."
}
```

##### `type ReadTool struct`

字段:
- `Root`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 读取工作区内文件内容

输入格式:

```json
{
  "file_path": "go.mod"
}
```

##### `type WriteTool struct`

字段:
- `Root`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 覆盖写入工作区内文件

输入格式:

```json
{
  "file_path": "notes.txt",
  "content": "hello"
}
```

##### `type GrepTool struct`

字段:
- `Root`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 在工作区内按内容搜索文本

输入格式:

```json
{
  "pattern": "RunTurn",
  "path": "internal",
  "literal": true,
  "max_results": 20
}
```

##### `type GlobTool struct`

字段:
- `Root`

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `Run(ctx, input)`

职责:
- 在工作区内按 glob 模式匹配路径

输入格式:

```json
{
  "pattern": "**/*.go",
  "path": "internal",
  "max_results": 50
}
```

##### `resolvePathWithinRoot(root, target) (string, error)`

职责:
- 解析文件路径
- 阻止路径逃出工作区根目录

#### 命令执行工具

文件: [bash.go](./internal/tool/exec/bash.go)

##### `type BashTool struct`

字段:
- `Root`

职责:
- 在工作区内执行 shell 命令

输入格式:

```json
{
  "command": "go test ./...",
  "cwd": ".",
  "timeout_seconds": 30
}
```

##### `Run(ctx, input) (string, error)`

职责:
- 解码输入
- 校验工作目录
- 设置超时
- 执行子进程
- 合并 stdout/stderr
- 限制输出长度

##### 关键内部函数

###### `decodeBashInput(input) (bashInput, error)`

职责:
- 校验 `command`

###### `resolveTimeout(timeoutSeconds) time.Duration`

职责:
- 解析超时

###### `resolveWorkingDir(root, cwd) (string, error)`

职责:
- 解析并校验工作目录不越界

###### `type limitedBuffer struct`

职责:
- 截断工具输出，避免结果无限增长

#### 网络工具

文件: [webfetch.go](./internal/tool/webfetch/webfetch.go)

##### `type Tool struct`

字段:
- `Client`

职责:
- 发起 HTTP(S) GET 请求
- 返回状态行、响应头中的 `Content-Type` 和响应体文本

输入格式:

```json
{
  "url": "https://example.com",
  "timeout_seconds": 30
}
```

### `internal/ui`

#### 抽象层

文件: [ui.go](./internal/ui/ui.go)

##### `type ToolCallEvent struct`

字段:
- `ID`
- `Name`
- `Input`

##### `type ToolResultEvent struct`

字段:
- `ToolUseID`
- `Name`
- `Content`
- `IsError`

##### `type UI interface`

方法:
- `OnAssistantDelta(text string) error`
- `OnThoughtDelta(text string) error`
- `OnToolCall(event ToolCallEvent) error`
- `OnToolResult(event ToolResultEvent) error`
- `OnDone() error`

这是 loop 层唯一依赖的 UI 抽象。

#### Headless 实现

文件: [headless.go](./internal/ui/headless/headless.go)

##### `type UI struct`

字段:
- `out`
- `mu`
- `wrote`

职责:
- 把 assistant delta 写到 stdout
- 把工具事件写成单独行
- 在一轮结束时补换行

##### `New(out io.Writer) *UI`

职责:
- 创建 headless UI

##### `OnAssistantDelta(text) error`

职责:
- 流式输出 assistant 文本

##### `OnToolCall(event) error`

职责:
- 输出工具调用事件

##### `OnToolResult(event) error`

职责:
- 输出工具结果摘要

##### `OnDone() error`

职责:
- 一轮结束时补一个换行

### `internal/loop`

文件: [runner.go](./internal/loop/runner.go)

#### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error)`

作用:
- 抽象模型流式能力

这让 `Runner` 不直接依赖具体 provider 类型。

#### `type Runner struct`

字段:
- `model` (`ModelStreamer`) — 最小模型流式接口
- `ui` (`ui.UI`) — 输出界面接口
- `registry` (`*tool.Registry`) — 工具注册表
- `store` (`HistoryStore`) — 历史存储接口
- `sessionID` — 当前会话 ID
- `prompt` (`*PromptBuilder`) — 系统提示词构建器
- `history` — 已成功完成的多轮对话消息列表（内存缓存）
- `usage` / `sessionUsage` — 当前轮/整个会话的 token 用量统计
- `supplements` — 用户在当前轮运行期间提交的补充指令（支持并发注入）

职责:
- 驱动单次 agent turn
- 在成功 turn 后维护内存中的多轮历史
- 用模型服务端 usage 字段更新 context 计量，不做本地 token 估算

#### `NewRunner(model, output, registry) *Runner`

职责:
- 创建调度器

#### `RunTurn(ctx, input) (message.Message, error)`

职责:
- 执行一次完整 turn

当前逻辑:
1. 验证与初始化：检查 runner 初始化状态；首次运行时从 store 加载历史
2. 构建本轮历史副本：复制已提交历史，插入未注入的 supplements，再追加当前用户输入（失败时不污染已提交历史）
3. 多轮工具循环（最多 500 轮）：
   - 每轮开始时检查是否有新注入的 supplements 并追加
   - 调用 `runModelTurn`：构造 system prompt + 历史消息，通过 `model.StreamMessage` 发送给 LLM 并消费流式事件
   - 若返回消息不含 `ToolUse`，调用 `commitHistory` 持久化并返回该 assistant 消息
   - 若含 `ToolUse`，调用 `runToolCall` 执行工具，将 tool_result 追加到历史副本，继续下一轮
4. 超限保护：超过 500 轮返回错误

#### `ResetHistory()`

职责:
- 清空内存历史
- 清空最近一次 usage 计量

当前用途:
- REPL 的 `/clear`

#### `buildModelMessages(history) []message.Message`

职责:
- 把内存中的 `history` 转成喂给模型的消息列表

#### `buildSystemPrompt() string`

职责:
- 生成 system prompt
- 注入工具说明和输入 schema

#### `renderMessageForModel(msg) message.Message`

职责:
- 把内部消息编码成模型输入消息

#### `consumeStream(ctx, events) (message.Message, error)`

职责:
- 消费模型流式事件

#### `parseAssistantMessage(content) message.Message`

职责:
- 判断模型输出是普通文本还是 `tool_use` JSON

#### `runToolCall(ctx, call) (message.Message, error)`

职责:
- 向 UI 发工具调用事件
- 执行工具
- 向 UI 发工具结果事件
- 返回 `ToolResult` 消息

#### `executeToolCall(ctx, call) message.Message`

职责:
- 只执行工具，不做 UI 输出

### `internal/subagent`

文件: [manager.go](./internal/subagent/manager.go)

#### `type Manager struct`

字段:
- `model` (`loop.ModelStreamer`) — 子 agent 使用的模型客户端
- `store` (`Store`) — 会话存储
- `root` — 工作区根路径
- `settings` (`SettingsProvider`) — 设置提供者
- `notifier` (`Notifier`) — 通知器（用于 UI 通知）
- `launcher` (`Launcher`) — 进程启动器
- `registry` (`taskRegistry`) — 任务注册表
- `depth` / `maxDepth` — 当前递归深度 / 最大深度（默认 4）
- `parentTaskID` — 父任务 ID
- `tasks` — 内存中的任务快照（map[string]TaskSnapshot）
- `running` — 运行中的进程（map[string]Process）

职责:
- 管理子 agent 任务的生命周期（创建、运行、查询、停止）
- 控制递归嵌套深度防止无限循环

##### `NewManager(cfg Config) *Manager`

职责:
- 创建 Manager 实例，默认 maxDepth=4

##### `Run(ctx, req) (Result, error)`

职责:
- 同步运行子 agent，等待执行完成并返回结果

##### `Launch(ctx, req) (TaskSnapshot, error)`

职责:
- 后台启动子 agent，立即返回任务快照，异步等待完成

##### `Stop(ctx, id) (TaskSnapshot, error)`

职责:
- 停止指定 ID 的后台任务

##### `Status(id) (TaskSnapshot, bool)`

职责:
- 查询指定任务的状态

##### `ListTasks() []TaskSnapshot`

职责:
- 列出所有任务（内存 + 磁盘），按启动时间排序

##### 内置 Tool 实现

Manager 同文件中实现了三个供 LLM 调用的工具：

- **`Subagent`** — 启动子 agent，支持 sync/background 两种运行模式
- **`SubagentStatus`** — 查询任务状态，按 ID 查或列出所有
- **`SubagentStop`** — 按 ID 停止运行中的任务

#### `type Request struct`

字段:
- `ParentSessionID` — 父会话 ID（fork 模式使用）
- `Prompt` — 子任务提示
- `Description` — 任务描述
- `ContextMode` — `"empty"`（空上下文）或 `"fork"`（继承父会话）
- `RunMode` — `"sync"` 或 `"background"`

#### `type Result struct`

字段:
- `AgentID` — 子 agent ID
- `SessionID` — 子会话 ID
- `Content` — 执行结果文本
- `ExitCode` — 退出码
- `Depth` — 递归深度

#### `type TaskSnapshot struct`

字段:
- `ID` — 任务 ID
- `Status` — running / completed / failed / stopped
- `StartedAt` — 开始时间
- `FinishedAt` — 结束时间
- `Content` — 结果内容
- `Error` — 错误信息

### `internal/settings`

文件: [settings.go](./internal/settings/settings.go)

#### `type Config struct`

字段:
- `Subagent` (`SubagentConfig`) — 子 agent 默认配置
- `UI` (`UIConfig`) — UI 配置

##### `SubagentConfig`

字段:
- `DefaultContextMode` — `"empty"` 或 `"fork"`
- `DefaultRunMode` — `"sync"` 或 `"background"`

##### `UIConfig`

字段:
- `ContextLimitTokens` — 上下文 token 上限（默认 1,048,576）
- `ContextMeterLocation` — context meter 位置（`"input-title"` / `"header"` / `"input-above"`）

#### `type Controller struct`

职责:
- 加载、保存、提供运行时配置
- 线程安全读写

##### `NewControllerInCwd() (*Controller, error)`

职责:
- 从 `.ccagent/settings.json` 加载配置并创建 Controller

##### `CurrentSettings() Config`

职责:
- 获取当前配置（线程安全，nil 安全）

##### `SaveSettings(cfg Config) error`

职责:
- 持久化配置到磁盘并更新内存（线程安全）

##### `Normalize(cfg Config) Config`

职责:
- 规范化配置字段值（大小写无关，非法值回退到默认）

## 抽象层总结

当前有 3 个核心抽象层。

### 1. 模型抽象

接口: `loop.ModelStreamer`

用途:
- 隔离 `Runner` 和具体模型客户端

替换方式:
- 只要实现 `StreamMessage(...)`，就可以替换 `model.Client`

### 2. 工具抽象

接口: `tool.Tool`

用途:
- 隔离 loop 和具体工具实现

替换方式:
- 新工具实现接口后注册即可

### 3. UI 抽象

接口: `ui.UI`

用途:
- 隔离 loop 和具体输出方式

替换方式:
- 可将 `headless` 换成 TUI、日志型 UI、测试 UI

## 扩展点

这里只列当前稳定扩展点。

### 增加一个新工具

位置:
- 新建 `internal/tool/<name>/...`
- 在 [main.go](./cmd/agent/main.go) 注册

要求:
- 实现 `tool.Tool`

最小步骤:
1. 定义 `struct`
2. 实现 `Name`
3. 实现 `Description`
4. 实现 `InputSchema`
5. 实现 `Run`
6. 在 `buildRunner()` 里 `registry.Register(...)`

### 替换 UI

位置:
- 新建一个实现 `ui.UI` 的包

接入点:
- [main.go](./cmd/agent/main.go)

### 替换模型提供方

方式 1:
- 直接改 `internal/model` 的 HTTP 实现

方式 2:
- 新建一个实现 `loop.ModelStreamer` 的客户端
- 在 `buildRunner()` 中替换 `model.NewClient(cfg)`

### 增加本地命令

接入点:
- Bubble Tea 命令注册表 `internal/ui/bubble/command_registry.go`

### 自定义 Subagent 行为

位置:
- `internal/subagent/manager.go` 中的 `Manager` 结构

扩展方式:
- 通过 `Manager` 的 `Config` 结构传入自定义 `Launcher`、`Notifier`、`SettingsProvider`
- 修改 `maxDepth` 限制递归深度
- 实现新的 `Store` 接口替换默认 JSONL 存储

## 当前不属于扩展面的函数

下列函数是内部实现细节，不建议作为外部依赖面：

- `loop` 中的输出状态函数
- `model/stream.go` 中的 SSE 解析函数
- `bash.go` 中的输入解码和缓冲细节

如果要扩展功能，优先从这几个位置下手：
- `tool.Tool`
- `ui.UI`
- `loop.ModelStreamer`
- `main.buildRunner()`
- `subagent.Manager`

## 启动要求

### 环境变量

```bash
export NEWAPI_API_KEY=your_key
# 或者
export DEEPSEEK_API_KEY=your_key
```

也支持在项目根目录放一个不会进 git 的 `.env.local`：

```bash
cp .env.local.example .env.local
# 默认示例使用 NEWAPI_API_KEY；如果你的网关兼容旧变量名，也可以改用 DEEPSEEK_API_KEY
```

### 单轮模式

```bash
go run ./cmd/agent -p "hello"
```

### REPL 模式

```bash
go run ./cmd/agent
```

### Subagent 工作模式（由主进程自动调用，一般不需要手动执行）

```bash
go run ./cmd/agent -subagent-worker < worker_request.json
```
