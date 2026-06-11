# Go 重写清单

## 目标

用 Go 重建这个类 Claude Code 项目的核心能力，从最小可运行的 agent loop 开始，再按阶段逐步补齐功能。

这份清单刻意不按当前仓库里的 TypeScript 还原结构做 1:1 迁移。这个仓库里的源码是基于 source map 还原出来的结果，不是原始上游开发仓库。因此，重写时应以“产品能力”为边界，而不是以“目录结构”作为约束。

## 设计原则

1. 先做 headless CLI，再做 REPL 或复杂 TUI。
2. agent loop 必须和 UI、工具实现、存储层解耦。
3. 第一批工具只做 `read_file`、`list_files`、`exec`。
4. transcript 持久化和 resume 要尽早做，不要拖到后期。
5. MCP、插件、子代理、远程会话和复杂交互全部后置，先把单代理本地闭环做稳。

## 功能推进顺序

### 阶段 0：脚手架

先实现一个最小命令：

```bash
ccagent -p "hello"
```

要求：

- 读取配置
- 创建模型客户端
- 发送一次请求
- 将纯文本流式输出到 stdout
- 暂时不接工具

完成标准：

- 稳定完成单轮流式文本回答

### 阶段 1：最小 Agent Loop

实现真正的单轮闭环：

```text
user -> model -> tool_use -> execute -> tool_result -> model -> final
```

第一批工具：

- `read_file`
- `list_files`
- `exec`

完成标准：

- agent 能在本地代码仓库中调用工具、读取结果，并在同一轮中继续回答

### 阶段 2：工具注册与执行层

建立正式的工具抽象：

- tool interface
- tool registry
- 输入 schema 暴露
- JSON 解码与校验
- 超时
- 取消
- 统一错误格式

完成标准：

- 新增一个工具时，不需要改动 loop 核心逻辑

### 阶段 3：Transcript 持久化

把消息和工具结果持久化到本地。

要求：

- 使用 append-only 的 `jsonl` 或类似格式
- 生成稳定的 session ID
- 支持 resume
- 能可靠恢复历史上下文

完成标准：

- 中断后的会话可以安全恢复

### 阶段 4：安全文件编辑

加入编辑能力：

- `write_file`
- `edit_file`

规则：

- 优先使用 patch 风格编辑，而不是整文件覆盖
- 保留 diff 或最小编辑摘要
- 默认只允许在工作区内写入，除非显式授权

完成标准：

- agent 可以做小范围代码修改，且文件更新行为可预测

### 阶段 5：Shell 执行强化

把 `exec` 从简单的子进程包装升级为可控 shell 工具。

要求：

- 支持 working directory
- 支持 timeout
- 捕获 stdout/stderr
- 限制输出长度
- 处理退出码
- 提供基础环境变量控制

完成标准：

- agent 可以稳定执行测试、查看日志、运行命令，而不会把 transcript 冲爆

### 阶段 6：权限系统

加入策略层，最少区分：

- `read`
- `write`
- `exec`

决策模式：

- `allow`
- `ask`
- `deny`

完成标准：

- 危险操作不会被默认直接执行

### 阶段 7：交互式 REPL

等 headless loop 稳定后，再做交互层：

- 多行输入
- 流式渲染
- 中断处理
- 输入历史
- 基础状态显示

完成标准：

- 交互模式的体验足以替代 `-p` 模式进行日常使用

### 阶段 8：上下文管理

补齐长会话控制能力：

- token 预算
- 大型 tool result 截断
- transcript 压缩
- 长轮次恢复
- 简单摘要

完成标准：

- 连续长会话时，agent 不会迅速失控或明显退化

### 阶段 9：仓库感知

加入代码仓库上下文能力：

- 工作区说明文件
- 常用文件自动注入
- repo root 检测
- 基础 git 感知

完成标准：

- 在一个新仓库中的首轮回答质量明显提升

### 阶段 10：Git 工作流支持

加入聚焦的 git 能力：

- status
- diff
- log
- commit message 草稿
- 变更摘要生成

完成标准：

- 本地从修改代码到生成变更说明的工作流完整闭环

### 阶段 11：计划与任务跟踪

加入长任务支持：

- todo/task 列表
- plan mode
- 进度更新
- 阻塞时向用户提问

完成标准：

- agent 能拆解并跟踪多步骤任务

### 阶段 12：扩展机制

加入外部扩展能力：

- 类插件加载
- 类 MCP 远程工具
- 动态工具发现

完成标准：

- 不改核心二进制也可以扩展新能力

### 阶段 13：子代理与后台任务

只有单代理路径足够稳定后再做：

- 子任务委派
- 后台执行
- 结果回收
- 状态同步

完成标准：

- 并行或委派执行能提升吞吐，而不会破坏正确性

### 阶段 14：生产化

最后做收尾加固：

- 崩溃恢复
- 遥测
- 打包
- 更新
- 远程/IDE 集成

完成标准：

- 达到可重复、可稳定使用的发布标准

## 建议的 Go 项目结构

```text
cmd/ccagent/main.go

internal/app/app.go
internal/config/config.go
internal/config/session.go

internal/message/types.go
internal/message/codec.go

internal/model/client.go
internal/model/stream.go
internal/model/anthropic/client.go

internal/loop/runner.go
internal/loop/turn.go

internal/tool/tool.go
internal/tool/registry.go
internal/tool/exec.go
internal/tool/file/read.go
internal/tool/file/edit.go
internal/tool/file/write.go
internal/tool/shell/exec.go

internal/policy/policy.go
internal/policy/defaults.go

internal/session/store.go
internal/session/jsonl_store.go

internal/repo/workspace.go
internal/repo/git.go

internal/prompt/system.go
internal/prompt/builder.go

internal/ui/headless/headless.go
internal/ui/repl/repl.go

internal/logging/logger.go
internal/trace/trace.go

testdata/transcripts/
testdata/workspaces/
```

## 核心接口

这些接口应尽量保持小而稳定。

```go
type Role string

const (
	SystemRole    Role = "system"
	UserRole      Role = "user"
	AssistantRole Role = "assistant"
)

type Block struct {
	Type       string          `json:"type"` // text | tool_use | tool_result
	Text       string          `json:"text,omitempty"`
	ToolUse    *ToolCall       `json:"tool_use,omitempty"`
	ToolResult *ToolCallResult `json:"tool_result,omitempty"`
}

type Message struct {
	ID        string    `json:"id"`
	Role      Role      `json:"role"`
	Blocks    []Block   `json:"blocks"`
	CreatedAt time.Time `json:"created_at"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolCallResult struct {
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Content   string `json:"content"`
}

type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Run(ctx context.Context, call ToolCall, env RunEnv) (ToolCallResult, error)
}

type ModelClient interface {
	Stream(ctx context.Context, req ModelRequest) (<-chan StreamEvent, error)
}

type SessionStore interface {
	Load(ctx context.Context, sessionID string) ([]Message, error)
	Append(ctx context.Context, sessionID string, msgs ...Message) error
}

type PolicyEngine interface {
	Decide(ctx context.Context, action Action) Decision
}

type UI interface {
	OnAssistantDelta(text string)
	OnToolCall(call ToolCall)
	OnToolResult(result ToolCallResult)
	OnFinal(msg Message)
}
```

## Loop 设计

runner 必须独立于 UI 细节和具体工具实现。

```go
func (r *Runner) RunTurn(ctx context.Context, sessionID string, input string) error {
	history := r.store.Load(...)
	history = append(history, NewUserTextMessage(input))

	for {
		stream := r.model.Stream(ctx, BuildRequest(history, r.registry.Describe()))
		assistantMsg, toolCalls := ConsumeStream(stream, r.ui)
		history = append(history, assistantMsg)

		if len(toolCalls) == 0 {
			return r.store.Append(ctx, sessionID, assistantMsg)
		}

		results := r.exec.RunAll(ctx, toolCalls)
		history = append(history, NewToolResultMessage(results...))
	}
}
```

## 第一周开发计划

### 第 1 天

- 初始化 Go module
- 创建 `cmd/ccagent/main.go`
- 加入配置加载
- 加入 headless stdout 流式输出
- 先跑通一次无工具模型请求

### 第 2 天

- 固定消息类型
- 定义 `Tool`、`ModelClient`、`SessionStore`、`UI`
- 确定 transcript 存储格式

### 第 3 天

- 实现 `Runner.RunTurn()`
- 支持 assistant 流式输出与最终收尾

### 第 4 天

- 实现 `list_files`
- 实现 `read_file`
- 将这两个工具限制在工作区根目录内

### 第 5 天

- 实现 `exec`
- 加入 cwd 校验
- 加入 timeout 和输出截断
- 在真实仓库上跑一次 agent 读取代码并总结的 demo

### 第 6 天

- 实现 `jsonl` session store
- 加入 `resume`
- 增加 transcript 回放测试

### 第 7 天

- 清理接口
- 增补失败路径测试
- 写使用文档
- 输出稳定的 demo 脚本

## 第一周验收标准

- `ccagent -p "explain this repo entrypoint"` 可以正常工作
- 模型可以发起 `read_file` 和 `list_files`
- tool result 会被回灌到同一轮对话中
- transcript 可以持久化并恢复
- `exec` 有 timeout 和输出长度边界
- loop 包不直接依赖具体 UI 或具体工具实现

## 早期版本明确不做

- 丰富 TUI 渲染
- MCP
- 插件市场
- 子代理
- 远程会话
- 语音
- IDE 桥接
- 与当前还原 TypeScript 项目的全部命令完全对齐

## 建议的下一步

下一步直接把 Go 骨架搭起来：

1. `go mod init`
2. `cmd/ccagent/main.go`
3. `internal/message/types.go`
4. `internal/model/client.go`
5. `internal/loop/runner.go`
6. `internal/tool/tool.go`
7. `internal/session/jsonl_store.go`

这些文件一旦存在，项目就可以从“规划阶段”进入“实现阶段”，不用再反复讨论架构。
