# go-code 项目概览

> 一个最小可运行的本地 coding agent，支持交互式对话、单轮执行和子代理（subagent）模式。

---

## 一、项目结构

```text
cmd/agent/main.go              # 入口
internal/
├── message/types.go            # 统一消息模型
├── model/
│   ├── config.go               # 模型配置（环境变量 + .ccagent/model.json）
│   ├── types.go                # 请求/响应类型
│   ├── client.go               # HTTP 客户端
│   ├── stream.go               # SSE 流式解析
│   └── anthropic_stream.go     # Anthropic 格式流
├── tool/
│   ├── tool.go                 # Tool 接口定义
│   ├── register.go             # 工具注册表
│   ├── file/                   # 文件操作工具
│   │   ├── path.go
│   │   ├── ls.go
│   │   ├── read.go
│   │   ├── write.go
│   │   ├── grep.go
│   │   └── glob.go
│   ├── exec/bash.go            # Shell 执行工具
│   └── webfetch/webfetch.go    # 网页抓取工具
├── session/jsonl_store.go      # JSONL 会话存储
├── settings/settings.go        # 用户默认配置管理
├── subagent/manager.go         # 子代理管理器（含 3 个 LLM 工具）
├── ui/
│   ├── ui.go                   # UI 接口定义
│   ├── headless/headless.go    # 非交互式 UI
│   └── bubble/                 # Bubble Tea TUI 交互界面
└── loop/runner.go              # 核心循环（协调层）
```

---

## 二、五层架构

| 层 | 职责 | 不负责 |
|---|---|---|
| **message** | 定义统一消息模型（Role、Message、ToolCall、ToolResult） | 模型协议、UI、工具执行 |
| **model** | 与模型服务通信、HTTP 请求/响应、流式事件解析 | 对话循环、工具执行、渲染 |
| **tool** | 定义工具接口、注册表、具体工具实现 | 模型调用、UI、turn loop |
| **ui** | 接收渲染事件、决定输出方式 | 调用模型、执行工具 |
| **loop** | 驱动 agent turn、调模型、识别 tool use、调工具、维护历史 | 直接调用模型细节 |

---

## 三、运行模式

### 1. 交互式模式（默认）
```bash
go run ./cmd/agent
```
Bubble Tea TUI 界面，支持斜杠命令、路径补全、context meter。

### 2. 单轮模式
```bash
go run ./cmd/agent -p "hello"
```
非交互式，一次 prompt 一轮对话，结果输出到 stdout。

### 3. Subagent 工作模式
```bash
go run ./cmd/agent -subagent-worker < worker_request.json
```
由主 agent fork 的子进程模式，从 stdin 读取请求，输出结果到 stdout。

---

## 四、核心运行链路

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
      -> model.Client.StreamMessage (下一轮)
      -> final assistant message
```

---

## 五、Subagent（子代理）

Manager 提供了三个 LLM 可调用的工具：

| 工具名 | 功能 |
|---|---|
| **Subagent** | 启动子 agent，支持 sync/background 两种模式 |
| **SubagentStatus** | 按 ID 查询任务状态，或列出所有任务 |
| **SubagentStop** | 按 ID 停止运行中的任务 |

### 支持的模式

| 上下文模式 | 说明 |
|---|---|
| `empty` | 空上下文启动子 agent |
| `fork` | 继承父会话历史 |

| 运行模式 | 说明 |
|---|---|
| `sync` | 同步等待结果 |
| `background` | 后台异步运行 |

---

## 六、支持的斜杠命令

| 命令 | 功能 |
|---|---|
| `/help` | 显示帮助 |
| `/model [status\|custom\|deepseek]` | 切换/查看模型配置 |
| `/export [filename]` | 导出会话 |
| `/setting` | 打开设置向导 |
| `/sessions` | 浏览/恢复历史会话 |
| `/subagent [--fork\|--empty] [--background\|--sync] <prompt>` | 启动子代理 |
| `/tasks` | 查看后台任务 |
| `/status` | 查看状态 |
| `/clear` | 清空当前会话 |
| `/exit` / `/quit` | 退出 |

---

## 七、扩展点

1. **新增工具**：实现 `tool.Tool` 接口，在 `buildRunner()` 注册
2. **替换 UI**：实现 `ui.UI` 接口，在 `main.go` 接入
3. **替换模型**：实现 `loop.ModelStreamer` 接口
4. **增加本地命令**：在 `internal/ui/bubble/command_registry.go` 注册
5. **自定义 Subagent**：替换 `Launcher`、`Notifier`、`SettingsProvider`

---

## 八、环境变量

```bash
export NEWAPI_API_KEY=your_key
# 或
export DEEPSEEK_API_KEY=your_key
```

支持 `.env.local` 文件（不进 git）。

---

## 九、技术栈

- **语言**：Go
- **TUI 框架**：Bubble Tea (charmbracelet/bubbletea)
- **数据存储**：JSONL 文件
- **模型协议**：OpenAI-compatible / Anthropic Messages API
- **运行时**：命令行二进制
