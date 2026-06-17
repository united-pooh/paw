# Contributing to go-code

> 欢迎贡献 go-code 项目！本文档将帮助你快速了解项目的开发流程、代码规范以及如何扩展功能。

---

## Table of Contents

- [1. 开发环境搭建](#1-开发环境搭建)
- [2. 代码风格和规范](#2-代码风格和规范)
- [3. 如何添加新工具](#3-如何添加新工具)
- [4. 如何添加新命令](#4-如何添加新命令)
- [5. 如何运行测试](#5-如何运行测试)
- [6. PR 提交流程](#6-pr-提交流程)

---

## 1. 开发环境搭建

### 前置要求

- **Go** 1.21+（推荐 1.22+）
- **Git**
- 一个兼容 OpenAI 或 Anthropic API 的模型服务端点（如 DeepSeek、OpenAI、自部署的 API 服务）

### 克隆项目

```bash
git clone https://github.com/<your-org>/go-code.git
cd go-code
```

### 配置 API 密钥

通过环境变量或 `.env.local` 文件设置 API 密钥（`.env.local` 不会进入 Git 版本控制）：

```bash
# 方式一：环境变量
export NEWAPI_API_KEY=your_key_here
# 或
export DEEPSEEK_API_KEY=your_key_here

# 方式二：在项目根目录创建 .env.local 文件
# NEWAPI_API_KEY=your_key_here
# DEEPSEEK_API_KEY=your_key_here
```

### 验证环境

```bash
# 确保 Go 版本满足要求
go version

# 构建项目，确认编译通过
go build ./cmd/agent

# 运行交互式界面（可选，需要 API 密钥已配置）
go run ./cmd/agent
```

如果看到 Bubble Tea TUI 界面正常启动，说明环境搭建成功。

### 可选：模型配置

首次启动时，项目会在当前用户目录下生成 `~/.ccagent/model.json` 配置文件。你也可以手动编辑该文件来切换模型端点：

```json
{
  "provider": "custom",
  "base_url": "https://api.example.com/v1",
  "model": "your-model-name"
}
```

在运行中使用 `/model` 命令可以切换预置配置。

---

## 2. 代码风格和规范

### Go 代码规范

- **格式化**：所有 Go 代码必须通过 `gofmt`（或 `goimports`）格式化。
- **命名**：遵循 Go 标准命名惯例——驼峰式，首字母大小写控制可见性。
- **错误处理**：错误应当被显式检查，不要忽略错误返回值。对于工具执行中的错误，建议使用 `fmt.Errorf` 包装上行文。
- **日志**：交互式项目中避免使用 `log.Fatal`（会导致 TUI 崩溃），推荐通过 UI 接口向用户展示错误信息。
- **导入分组**：按以下顺序分组，组间空行分隔：
  1. 标准库
  2. 第三方包（包括 `charmbracelet/bubbletea` 等）
  3. 项目内部包（`go-code/internal/...`）

### 项目分层规范

项目采用**五层架构**，各层职责清晰，开发时请遵守以下约束：

| 层 | 目录 | 职责 | 禁止依赖 |
|---|---|---|---|
| **message** | `internal/message/` | 统一消息模型 | 不可依赖 model、tool、ui、loop |
| **model** | `internal/model/` | 模型通信和流式解析 | 不可依赖 tool、ui、loop |
| **tool** | `internal/tool/` | 工具接口和实现 | 不可依赖 model、ui、loop |
| **ui** | `internal/ui/` | 输出渲染和交互 | 不可依赖 model、tool |
| **loop** | `internal/loop/` | 核心 turn loop 协调 | 可依赖其余所有层 |

> **基本原则**：下层不可依赖上层。`loop` 是唯一可以引用所有层的模块。

### 测试规范

- 单元测试文件与被测代码放在同一包中，命名格式为 `*_test.go`。
- 表驱动测试（table-driven tests）是首选风格。
- 避免在单元测试中发出真实的 HTTP 请求，建议使用接口 mock 或 httptest。

### Git 提交规范

建议使用以下类型的 commit message 前缀：

- `feat:` — 新功能
- `fix:` — 修复 bug
- `refactor:` — 重构
- `docs:` — 文档更新
- `test:` — 测试相关
- `chore:` — 构建/工具链变更

---

## 3. 如何添加新工具

> 工具（Tool）是 agent 与环境交互的原子能力。添加新工具只需三步。

### 步骤详解

#### 第一步：实现工具接口

创建一个新的包目录，例如 `internal/tool/<your-tool>/`，然后实现 `tool.Tool` 接口：

```go
// internal/tool/tool.go 中定义的接口：
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage           // JSON Schema for the tool's parameters
    Run(parameters json.RawMessage) (string, error)
}
```

**示例**：假设我们要添加一个天气查询工具 `internal/tool/weather/weather.go`：

```go
package weather

import (
    "encoding/json"
    "fmt"
)

type WeatherTool struct{}

func (t *WeatherTool) Name() string {
    return "Weather"
}

func (t *WeatherTool) Description() string {
    return "查询指定城市的天气信息"
}

func (t *WeatherTool) Schema() json.RawMessage {
    // 返回 JSON Schema，定义工具参数
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "city": {
                "type": "string",
                "description": "城市名称，如北京、上海"
            }
        },
        "required": ["city"]
    }`)
}

func (t *WeatherTool) Run(parameters json.RawMessage) (string, error) {
    var params struct {
        City string `json:"city"`
    }
    if err := json.Unmarshal(parameters, &params); err != nil {
        return "", fmt.Errorf("解析参数失败: %w", err)
    }
    // TODO: 实现实际的天气查询逻辑
    return fmt.Sprintf("查询 %s 的天气...", params.City), nil
}
```

> **参考**：已有的工具实现位于 `internal/tool/file/`、`internal/tool/exec/`、`internal/tool/webfetch/`，可作为实现参考。

#### 第二步：在注册表中注册

打开 `internal/tool/register.go`，在 `NewRegistry` 函数（或类似位置）中将新工具添加到注册表：

```go
func NewRegistry() *Registry {
    r := &Registry{
        tools: make(map[string]Tool),
    }
    // 已有工具...
    r.Register(&bash.BashTool{})
    r.Register(&file.LsTool{})
    r.Register(&file.ReadTool{})
    r.Register(&file.WriteTool{})
    r.Register(&file.GrepTool{})
    r.Register(&file.GlobTool{})
    r.Register(&webfetch.WebFetchTool{})
    
    // 注册新工具
    r.Register(&weather.WeatherTool{})  // ⬅️ 新增
    
    return r
}
```

#### 第三步：在 buildRunner 中挂载（可选）

如果新工具有额外的初始化依赖（比如子代理管理器中的 Launcher、Notifier 等），需要在 `cmd/agent/main.go` 的 `buildRunner` 函数中完成装配。简单的工具只需注册即可。

> **完成！** 重新编译运行后，模型在对话中即可调用你的新工具。

### 最佳实践

- **参数校验**：在 `Run` 方法中对入参做充分校验，返回清晰的错误信息。
- **安全性**：对于执行 shell 命令的工具，做好参数转义和命令白名单。
- **幂等性**：尽量让工具调用幂等（多次调用结果一致），避免副作用累积。
- **超时机制**：如果工具需要网络调用或耗时操作，建议引入 context 超时。

---

## 4. 如何添加新命令

> 命令（Slash Command）是用户在 TUI 界面中以斜杠 `/` 开头的交互指令。

### 步骤详解

#### 第一步：实现命令处理函数

在 `internal/ui/bubble/command_registry.go` 中注册新命令。该文件负责维护命令名称到处理函数的映射。

```go
func init() {
    // 已有命令...
    registerCommand("/help", "显示帮助信息", handleHelp)
    registerCommand("/model", "切换/查看模型配置", handleModel)
    registerCommand("/export", "导出会话", handleExport)
    registerCommand("/setting", "打开设置向导", handleSetting)
    registerCommand("/sessions", "浏览/恢复历史会话", handleSessions)
    registerCommand("/subagent", "启动子代理", handleSubagent)
    registerCommand("/tasks", "查看后台任务", handleTasks)
    registerCommand("/status", "查看状态", handleStatus)
    registerCommand("/clear", "清空当前会话", handleClear)
    registerCommand("/exit", "退出程序", handleExit)
    registerCommand("/quit", "退出程序", handleQuit)
    
    // 注册新命令
    registerCommand("/ping", "检查服务连通性", handlePing)  // ⬅️ 新增
}
```

然后实现对应的处理函数：

```go
func handlePing(ctx *CommandContext, args []string) error {
    // args 包含用户输入中命令后的参数（以空格分割）
    // CommandContext 包含当前模型、会话等上下文信息
    
    // 向对话追加回应
    ctx.SendMessage("Pong! 服务正常。")
    return nil
}
```

#### 第二步：建议一并更新帮助文档

如果希望在 `/help` 命令中显示新命令，请更新帮助信息的定义（通常在同一个文件或 `help.go` 中）。

### 命令处理函数的签名

```go
type CommandHandler func(ctx *CommandContext, args []string) error
```

其中 `CommandContext` 提供了以下关键能力：

- `SendMessage(text string)` — 向用户显示一条消息
- `GetModel()` — 获取当前模型配置
- `GetSession()` — 获取当前会话

### 路径补全（可选）

如果命令支持文件路径参数，可以在 Bubble Tea 的 update 循环中注册路径自动补全逻辑（参考已有实现）。

---

## 5. 如何运行测试

### 运行所有测试

```bash
# 运行所有包的测试
go test ./...

# 带详细输出
go test -v ./...
```

### 运行特定包的测试

```bash
# 测试消息模型层
go test ./internal/message/...

# 测试工具层
go test ./internal/tool/...

# 测试具体某个工具
go test ./internal/tool/file/...
```

### 运行单个测试用例

```bash
go test -run TestFunctionName ./internal/message/
```

### 测试覆盖率

```bash
# 生成覆盖率报告
go test -coverprofile=coverage.out ./...

# 查看 HTML 格式报告
go tool cover -html=coverage.out -o coverage.html
```

### 测试注意事项

- **网络依赖**：`internal/model/` 层的测试可能涉及 API 调用。建议在 CI 中设置 mock，或使用 `-short` 标志跳过集成测试：

```bash
go test -short ./...
```

- **TUI 测试**：Bubble Tea 应用可以通过 `tea.NewProgram` 的测试模式（`tea.WithInput`、`tea.WithOutput`）进行集成测试。

- **CI 测试**：项目推荐在 GitHub Actions 中运行以下命令：

```yaml
- name: Run tests
  run: |
    go test -race -count=1 -coverprofile=coverage.out ./...
    go vet ./...
```

---

## 6. PR 提交流程

### 提交流程总览

```
Fork → Clone → Create Branch → Commit → Push → Open PR → Review → Merge
```

### 详细步骤

#### 1. Fork 并 Clone 仓库

```bash
# Fork 项目到你的 GitHub 账号下，然后：
git clone https://github.com/<your-username>/go-code.git
cd go-code
git remote add upstream https://github.com/<original-org>/go-code.git
```

#### 2. 创建功能分支

从最新的 main 分支创建分支：

```bash
git fetch upstream
git checkout -b feat/my-new-feature upstream/main
```

分支命名建议：

| 分支类型 | 命名示例 |
|---|---|
| 新功能 | `feat/add-weather-tool` |
| Bug 修复 | `fix/crash-on-empty-input` |
| 重构 | `refactor/extract-model-client` |
| 文档 | `docs/update-contributing` |

#### 3. 开发并本地验证

- 实现你的改动
- 确保代码通过 `gofmt` 格式化
- 运行 `go vet ./...` 检查静态问题
- 运行测试：`go test ./...`
- 手动测试：构建后运行并通过场景验证

```bash
gofmt -w ./
go vet ./...
go test ./...
go build ./cmd/agent
```

#### 4. 提交代码

```bash
git add .
git commit -m "feat: add weather tool for querying city weather"
```

Commit message 格式参考：[Conventional Commits](https://www.conventionalcommits.org/)

#### 5. 推送到远程并创建 PR

```bash
git push origin feat/my-new-feature
```

在 GitHub 上打开 Pull Request，请确保：

- **目标分支**：`main`
- **PR 标题**：清晰、简洁，与 commit message 风格一致
- **PR 描述**：包含以下内容：
  - 改动概述（做了什么、为什么）
  - 如何验证（测试步骤、截图等）
  - 相关 issue 编号（如果有）
  - 是否涉及 breaking change

#### 6. 代码审查

- 等待维护者 review
- 收到修改意见后，在本地修改并再次 push：

```bash
git commit -m "fix: address review comments"
git push origin feat/my-new-feature
```

- 不要 rebase 已经 push 的分支，除非被要求。

#### 7. 合并

当 PR 获得至少一名维护者的批准后，将由维护者执行合并。合并后你可以删除远程分支：

```bash
git checkout main
git pull upstream main
git branch -d feat/my-new-feature
git push origin --delete feat/my-new-feature
```

### PR 检查清单

在提交 PR 前，请逐项确认：

- [ ] 代码通过了 `go vet ./...`
- [ ] 代码通过了 `go test ./...`
- [ ] 新增代码有对应的单元测试
- [ ] 修改了接口或新增了工具，相关文档已更新
- [ ] 是否兼容现有的五层架构依赖规则
- [ ] 没有将敏感信息（API Key 等）提交到仓库
- [ ] Commit message 遵循规范格式
- [ ] 分支已 rebase 到最新的 `upstream/main`

---

## 附录：架构速查

### 五层依赖图

```
┌──────────────────────────────────┐
│            loop (协调层)           │ ◄── 可以依赖所有层
├──────────────────────────────────┤
│     ui (渲染/交互)                 │ ◄── 不可依赖 model、tool
├──────────────────────────────────┤
│     tool (工具实现)                │ ◄── 不可依赖 model、ui、loop
├──────────────────────────────────┤
│     model (模型通信)               │ ◄── 不可依赖 tool、ui、loop
├──────────────────────────────────┤
│     message (统一消息模型)          │ ◄── 不可被下层依赖，不依赖任何层
└──────────────────────────────────┘
```

### 核心运行链路

```
main → buildRunner → loop.Runner.RunTurn
    → model.Client.StreamMessage
    → ui.OnThinkingDelta / ui.OnAssistantDelta / ui.OnDone
    → parse tool_use
    → tool.Registry.Get → tool.Run
    → ui.OnToolCall / ui.OnToolResult
    → model.Client.StreamMessage (next turn)
    → final assistant message
```

### 已有扩展点一览

| 扩展点 | 接口/位置 | 说明 |
|---|---|---|
| 新增工具 | `tool.Tool` 接口 + `register.go` | 实现 Name、Description、Schema、Run |
| 替换 UI | `ui.UI` 接口 + `main.go` 接入 | 可实现终端、WebSocket 等不同 UI |
| 替换模型 | `loop.ModelStreamer` 接口 | 接入不同的 LLM 后端 |
| 新增命令 | `command_registry.go` 注册 | 在 TUI 中增加斜杠命令 |
| 自定义 Subagent | Launcher、Notifier、SettingsProvider | 替换子代理的启动/通知/配置逻辑 |

---

感谢你的贡献！如果有任何疑问，欢迎在 GitHub Issues 中提出。
