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

internal/ui/ui.go
internal/ui/headless/headless.go

internal/loop/runner.go
```

## 入口

### 程序入口

文件: [cmd/agent/main.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/cmd/agent/main.go)

#### `main()`

职责:
- 解析命令行参数
- 构造 `Runner`
- 选择单轮模式或 REPL 模式

不负责:
- 直接调用模型
- 直接执行工具
- 维护对话状态

#### `parseOptions() options`

职责:
- 读取 `-p`

行为:
- `-p` 有值: 执行单轮
- `-p` 为空: 进入交互式对话 loop

#### `buildRunner() (*loop.Runner, error)`

职责:
- 加载模型配置
- 创建模型客户端
- 创建 UI
- 注册工具
- 返回 `Runner`

这是当前的依赖装配点。

#### `run(ctx, runner, opts) error`

职责:
- 模式分发

分支:
- `runSingleTurn`
- `runREPL`

#### `runSingleTurn(ctx, runner, prompt) error`

职责:
- 调一次 `runner.RunTurn`

#### `runREPL(ctx, runner, in, out) error`

职责:
- 提供最小交互式对话循环

步骤:
1. 打印帮助
2. 读取用户输入
3. 处理本地命令
4. 调用 `runner.RunTurn`

#### `handleREPLCommand(out, runner, line) (bool, error)`

当前支持:
- `/help`
- `/clear`

退出命令在 `runREPL` 中直接处理:
- `/exit`
- `/quit`

## 分层

当前分为 5 层。

### 1. `message`

职责:
- 定义统一消息模型

边界:
- 不知道模型协议细节
- 不知道 UI
- 不知道工具注册和执行

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

这是当前系统的协调层。

## 运行链路

当前运行链路如下:

```text
main
  -> buildRunner
  -> loop.Runner.RunTurn
      -> model.Client.StreamMessage
      -> ui.OnAssistantDelta / ui.OnDone
      -> parse tool_use
      -> tool.Registry.Get
      -> tool.Run
      -> ui.OnToolCall / ui.OnToolResult
      -> model.Client.StreamMessage
      -> final assistant message
```

## 包级 API

### `internal/message`

文件: [types.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/message/types.go)

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

文件: [config.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/model/config.go)

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
- `.env.local` 会覆盖 `.env` 和外部 shell 继承进来的同名变量

当前环境变量:
- `NEWAPI_API_KEY`（优先）
- `DEEPSEEK_API_KEY`

当前默认值:
- base url: `http://localhost:9000`
- path: `/v1/chat/completions`
- model: `deepseek-chat`

#### 请求/响应类型

文件: [types.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/model/types.go)

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

文件: [client.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/model/client.go)

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

文件: [stream.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/model/stream.go)

##### `type StreamEvent struct`

字段:
- `Delta`
- `Done`
- `Err`

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

### `internal/tool`

#### 抽象层

文件: [tool.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/tool.go)

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

文件: [register.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/register.go)

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

#### 文件工具

文件:
- [path.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/path.go)
- [ls.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/ls.go)
- [read.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/read.go)
- [write.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/write.go)
- [grep.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/grep.go)
- [glob.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/file/glob.go)

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

文件: [bash.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/exec/bash.go)

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

文件: [webfetch.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/tool/webfetch/webfetch.go)

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

文件: [ui.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/ui/ui.go)

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
- `OnToolCall(event ToolCallEvent) error`
- `OnToolResult(event ToolResultEvent) error`
- `OnDone() error`

这是 loop 层唯一依赖的 UI 抽象。

#### Headless 实现

文件: [headless.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/ui/headless/headless.go)

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

文件: [runner.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/internal/loop/runner.go)

#### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error)`

作用:
- 抽象模型流式能力

这让 `Runner` 不直接依赖具体 provider 类型。

#### `type Runner struct`

字段:
- `model`
- `ui`
- `registry`
- `history`

职责:
- 驱动单次 agent turn
- 在成功 turn 后维护内存中的多轮历史

#### `NewRunner(model, output, registry) *Runner`

职责:
- 创建调度器

#### `RunTurn(ctx, input) (message.Message, error)`

职责:
- 执行一次完整 turn

当前逻辑:
1. 校验依赖
2. 用当前 `history + 新输入` 组成本轮上下文
3. 调模型流式输出
4. 聚合 assistant 消息
5. 若是 `tool_use`，执行工具并回灌结果
6. 循环直到拿到最终 assistant 文本
7. 成功后提交历史

#### `ResetHistory()`

职责:
- 清空内存历史

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
- 在 [main.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/cmd/agent/main.go#L63) 注册

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
- [main.go](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/cmd/agent/main.go#L61)

### 替换模型提供方

方式 1:
- 直接改 `internal/model` 的 HTTP 实现

方式 2:
- 新建一个实现 `loop.ModelStreamer` 的客户端
- 在 `buildRunner()` 中替换 `model.NewClient(cfg)`

### 增加本地命令

接入点:
- [handleREPLCommand](/Users/united_pooh/python project/claude-code-sourcemap-main/go-code/cmd/agent/main.go#L130)

适合加入:
- `/history`
- `/session`
- `/resume`

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
