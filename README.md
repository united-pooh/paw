# paw

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
internal/model/request_body.go

internal/tool/tool.go
internal/tool/register.go
internal/tool/file/path.go
internal/tool/file/ls.go
internal/tool/file/read.go
internal/tool/file/read_state.go
internal/tool/file/write.go
internal/tool/file/edit.go
internal/tool/file/atomic.go
internal/tool/file/mutation_path.go
internal/tool/file/grep.go
internal/tool/file/glob.go
internal/tool/exec/bash.go
internal/tool/webfetch/webfetch.go
internal/tool/select/
internal/tool/mcp/tool.go

internal/session/jsonl_store.go
internal/session/journal.go
internal/settings/settings.go
internal/skill/registry.go
internal/subagent/manager.go
internal/streamma/

internal/ui/ui.go
internal/ui/headless/headless.go
internal/ui/bubble/

internal/loop/runner.go
internal/loop/prompt_builder.go
internal/loop/instruction_manager.go
internal/loop/context_compaction.go
internal/loop/turn_timing.go
internal/loop/token_tracer.go

internal/tokentracer/
```

## 快速使用

```bash
go run ./cmd/agent -p "hello"
go run ./cmd/agent
go run ./cmd/agent -s <session-id>
```

- 不加参数直接启动时，每次都会创建一个全新的空会话。
- 需要恢复历史会话时，使用 `-s <session-id>` 指定会话 ID；也可在交互界面输入 `/sessions` 浏览并恢复历史会话。

当前运行目录会作为工作区 root，同时也是 `.paw/` 状态目录的基准路径。

## 自动发布（pre-push hook）

仓库自带一个随仓库分发的 git pre-push 钩子：**每次推送 `dev` 分支时，自动把最新 dev 快照构建成 `paw` 可执行文件并安装到 `~/go/bin/paw`**，方便直接用 `paw` 命令启动。

启用方式（克隆仓库后执行一次）：

```bash
git config core.hooksPath .githooks
```

行为说明：

- 钩子文件：`.githooks/pre-push`（源码副本 `scripts/pre-push.sh`）
- 触发条件：推送目标为 `refs/heads/dev`；其他分支直接放行
- 构建命令：`go build -trimpath -ldflags "-s -w" -o ~/go/bin/paw ./cmd/agent`
- 版本一致性：用被推送的 `refs/heads/dev` 快照构建（不在 dev 上时会自动创建临时 worktree），保证二进制与推送内容一致
- 构建失败会中止本次 push；安装目录可用 `GOBIN` 环境变量覆盖
- 钩子只在本机生效，不会影响 CI

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
- 读取 `-subagent-worker`
- 读取 `-streamma` / `-token-tracer` / `-token-tracer-open` / `-token-tracer-port`（均有 `PAW_*` 环境变量默认值）

行为:
- `-p` 有值: 执行单轮
- `-p` 为空: 进入交互式对话界面
- `-s` 有值: 绑定到指定 session 并恢复历史
- `-s` 为空: 每次启动都创建一个全新空 session
- `-subagent-worker`: 以子进程方式运行，使用父进程转发 MCP 调用的双向 JSON 行协议

#### `buildRunner(ctx, sessionIDFlag, output) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, *session.JSONLStore, *mcp.Manager, error)`

职责:
- 加载模型配置
- 创建模型客户端和会话存储
- 装配 settings 控制器和 subagent 管理器
- 注册文件、shell、webfetch、subagent、select 相关工具
- 启动主进程持有的 MCP Manager，并注册 namespaced MCP 工具
- 返回 `Runner`、sessionID 和运行时控制器

这是当前的依赖装配点。

当前会注册的持久化目录:
- `~/.paw/config.json`（全局配置，模型通过 `modelProfiles` 和 `activeModelProfileId` 保存；缺失时自动创建）
- `~/.paw/settings.json`（全局 settings；项目内 `.paw/settings.json` 不再读取或迁移）
- `.paw/exports/`
- `.paw/sessions/<sessionID>/`

#### `registerTools(registry, root, readRoots, subagentManager, sessionID, broker) error`

职责:
- 注册文件工具（LS / Read / Write / Edit / Grep / Glob），其中 Write 与 Edit 共享同一个 `ReadStateStore`
- 注册 Bash、WebFetch
- 注册 subagent 相关的 `Subagent` / `SubagentStatus` / `SubagentStop`
- 注册 MCP namespaced 工具（`ReplaceNamespace("mcp", ...)`）

交互模式额外通过 `registerInteractiveTools` 注册 `Select` 工具（绑定 TUI selection broker）。

### MCP / CodeGraph

Paw 是 MCP client，主 Agent 在启动时读取 `~/.paw/mcp.toml`，通过本地 stdio 启动配置的 MCP server。文件不存在时会自动创建为空文件；启用的 server 初始化或能力发现失败会阻止本次启动。

配置沿用 Codex 风格的 `mcp_servers` 表，例如：

```toml
[mcp_servers.codegraph]
command = "codegraph"
args = ["serve", "--mcp"]
cwd = "."
enabled = true
```

发现的工具使用 `<server>__<tool>` 名称，例如 `codegraph__codegraph_explore`。资源、资源模板和 prompts 会映射为对应的 namespaced 虚拟工具；交互模式输入 `/mcp` 可查看配置路径、进程状态、能力数量和诊断信息。

主 Agent 持有唯一的 MCP server 会话。外部 subagent 不会重复启动 CodeGraph，而是通过父进程的 `mcp.call` / `mcp.result` request-ID 协议转发调用；能力列表变化时父进程会推送 `mcp.snapshot` 更新代理工具名称空间。`tool.Registry` 提供 `ReplaceNamespace` / `RemoveNamespace` 原子替换能力，MCP 快照更新会实时同步进注册表。

#### `runSingleTurnMode(ctx, opts) error`

职责:
- 在 headless UI 中执行一次 `runner.RunTurn`

输出:
- assistant 最终结果写 stdout
- 当前 sessionID 写 stderr

#### `runInteractiveMode(ctx, opts) error`

职责:
- 启动 Bubble Tea 主界面
- 创建 Select tool 的 `selecttool.Broker` 并注入 Bubble UI
- 注入模型配置、settings、subagent 控制器
- 以当前 session 进入可恢复的交互式对话
- 按参数启动 Token Tracer dashboard

#### `runSubagentWorkerMode(ctx)`

职责:
- 以子进程模式运行（由主 Agent 进程 fork 启动）
- 从 stdin 读取 JSON 格式的 `WorkerRequest`（含 `ParentSessionID`、`Prompt`、`Tools`、`MaxTurns` 等）
- 构建带有 subagent 上下文的 Runner（通过 `subagentRuntimeContext` 控制递归深度）
- 执行 `runner.RunTurn()`，将 `WorkerResult`（含 TaskID、SessionID、Content、Error、ExitCode、Usage）写入 stdout

#### 当前交互命令

当前 slash command 由 `internal/ui/bubble/command_registry.go` 统一注册，`/help` 会显示参数提示。

- `/help`
- `/model [status|<profile>|<model>]`
- `/export [filename]`
- `/setting`
- `/theme`
- `/sessions`
- `/subagent [--fork|--empty] [--background|--sync] <prompt>`
- `/streamma [--profile adaptive|paper] [--topology adaptive|chain|tree|graph] [--agents N] [--steps N] [--protocol stream|single] <prompt>`
- `/streamma-trace [--profile adaptive|paper] [--topology adaptive|chain|tree|graph] [--agents N] [--steps N] [--protocol stream|single] <prompt>`
- `/tasks`
- `/pipeline`
- `/skills`
- `/token-tracer` / `/tt`
- `/status`
- `/mcp`
- `/compact [focus]`
- `/clear`
- `/exit` / `/quit`

当前行为:
- `/model` 无参数时打开 profile → model 二级向导；profile 和模型列表全部来自全局 `~/.paw/config.json`；没有活动 profile/model 时默认使用配置中第一个 profile 的第一个模型；`status` 输出当前配置和可用模型；输入已配置的 profile ID、provider、名称或模型名即可切换并持久化活动选择
- `/export` 默认导出到 `.paw/exports/conversation-YYYY-MM-DD-HHMMSS.txt`，也支持工作区内显式路径；导出文件权限为 `0600`
- `/setting` 通过向导保存默认 subagent context/run mode，以及 context meter 的位置和 token limit
- `/theme` 打开内置主题选择器；↑/↓、j/k、Home/End 会实时预览，Enter 保存到 `~/.paw/settings.json`，Esc 恢复打开前的主题且不写盘
- `/sessions` 列出所有历史会话（ID 前缀、日期、文件大小、首条消息），选中条目后直接恢复该会话
- `/subagent` 支持 `empty` 与 `fork` 两种上下文模式，以及 `sync` 与 `background` 两种运行模式；后台任务完成后会发 UI 系统通知，并把截断后的结果作为补充上下文注入后续模型轮次（完整结果仍在任务 output/transcript 路径中）
- `/streamma <prompt>` 显式把当前任务交给 StreamMA runtime；runtime 会按任务选择一个小型 DAG，并把每个 StreamMA worker 映射为真实 subagent。一次 run 内同一个 logical agent 复用同一个 subagent session 作为真实 `ctx_a`；首次调用写入 agent base context + problem，后续调用只追加新 inbound step。只有同步到精确 `END_STEP` step 后才继续在 DAG 中传播；缺失 `END_STEP` 会失败而不是在 agent `Done` 时兜底传播，最终由 finalizer 的最后一步作为 assistant 回复写回会话历史。可选参数包括 `--profile`、`--topology`、`--agents`/`--a`、`--steps`/`--s`、`--protocol`；默认 `adaptive` profile 会保留任务模板图，显式 `--topology` 或 `paper` profile 可按指定拓扑生成 chain/tree/graph 形状
- `/streamma-trace <prompt>` 使用同一套真实 StreamMA/subagent 路径，并额外输出 live runtime trace（如 `subagent.started`、`agent.step.committed`、`control.upstream_eof`、per-invocation usage/cache），用于观察 step fanout 是否发生在上游 agent `Done` 前，以及同一 agent 是否复用同一 session
- `multi-agent-pipeline` skill 是 Codex/Paw 的阶段化工作流指导，不会自动要求 StreamMA runtime。`/streamma` 和 `/streamma-trace` 是显式 runtime 调试入口；如果只想测试 skill、slash completion、普通 subagent 或 Token Tracer，可用 `PAW_STREAMMA=0` 或 `-streamma=false` 关闭这两个入口
- `/tasks` 展示当前后台 subagent 任务及 transcript 路径
- `/pipeline` 展示当前 pipeline activity 面板
- `/skills` 展示当前可发现的本地 skills 及其 `SKILL.md` 路径
- `/token-tracer` 展示当前启动的 Token Tracer dashboard URL；交互模式默认启动本地 HTTP 服务，`/` 为实时页面，`/api/state` 为完整快照，`/events` 为 SSE 实时事件流
- `/compact [focus]` 在保留完整 journal 的前提下压缩模型上下文；`focus` 可指定聚焦压缩方向，压缩会保留最近消息、用户约束与未完成工作原文

Token Tracer:
- StreamMA 可用 `PAW_STREAMMA=0` 或 `-streamma=false` 手动关闭；关闭后输入 `/streamma` 或 `/streamma-trace` 会直接提示已禁用，不会启动 worker，也不会触发 `END_STEP` parser
- 交互模式启动时默认拉起本地 dashboard；可用 `PAW_TOKEN_TRACER=0` 或 `-token-tracer=false` 关闭
- `-token-tracer-port <port>` 指定端口，默认 `8999`；`PAW_TOKEN_TRACER_PORT` 也可设置默认端口
- `-token-tracer-open` 或 `PAW_TOKEN_TRACER_OPEN=1` 会自动在浏览器打开 dashboard
- Dashboard 聚合普通对话、工具调用、StreamMA runtime events、StreamMA subagent usage/cache、后台 subagent 任务生命周期，并按 `pipeline -> stage -> agent` 语义展示 token lane；output token 单独统计，不参与 context lane 宽度

#### 当前输入区状态

context meter 默认展示在消息历史区下方、输入框上方；输入框保持在窗口底部，不再显示 `Input`、`Waiting`、`Terminal` 标签。状态行内联展示 worktree 元信息（git 仓库名、当前分支/HEAD、clean/dirty/conflict 状态），空间不足时自动让位给 token 信息。

输入补全:
- 在行首输入 `/` 或 `/query` 会显示全部命令与 skill 候选；前面已有内容时只显示 `/subagent`、`/streamma` 和全部 skill，并仅使用末尾当前斜杠词筛选；URL、路径或普通文本内部的斜杠不会触发
- 接受斜杠候选时只替换当前斜杠词，并保留此前输入的文本
- 在输入框中输入 `@` 会弹出工作区文件路径候选列表
- 在输入框中输入 `$` 会弹出 skill 候选列表；Tab 或 Enter 会写入 `[$skill](.../SKILL.md)` 引用
- 使用 ↑↓ 键在候选项之间导航，Tab 或 Enter 确认补全，Esc 关闭弹窗

Skills:
- skills 统一从 `~/.paw/skills/<name>/SKILL.md` 加载，不读取项目目录、`$CODEX_HOME` 或其他 skills 目录
- 输入中出现 `$skill` 或 `[$skill](/abs/path/SKILL.md)` 时，Runner 会在本轮 system prompt 中注入对应 `SKILL.md` 的完整内容；该注入只对当前 turn 生效，不写入会话历史
- `/subagent` 的 prompt 中显式提到 skill 时，subagent worker 会按同样规则加载；`/streamma` 和 `/streamma-trace` 会把本轮选中的 skill context 传入每个 StreamMA worker 的 system prompt

context meter 的 token 数只来自模型服务端返回的真实 `usage` 字段；不会根据草稿、历史文本或本地字符数做估算。左侧 `↑/↓` 数字展示本次打开后的 session 累计 token 消耗，每次启动从 0 开始，`/clear` 也会清零；每次模型请求的 input/prompt、output/completion 与 cache hit 会按 provider 返回值入账，同一条流里多次 `usage` 会先合并成该请求的累计值再计入 session，避免 `message_start` / `message_delta` 重复计数。进度条、used 百分比、cache hit 百分比和 `free(...)` 仍然展示最近一次真实 usage 对应的当前上下文窗口占用；新一轮请求尚未返回 usage 时，会继续显示上一条上下文窗口 usage。

context meter 左侧显示紧凑 token 与比例，例如 `260k↑ 2.05k↓ 25%(10%)`：`↑` 是 session 累计上传/input token，`↓` 是 session 累计回答/output token，两个百分比分别是当前 context 用量和当前 cache hit 用量占总 limit 的比例。右侧只显示当前 context 剩余比例，例如 `free(75%)`。超过三位的 token 会压缩成 `k`，超过 `999k` 会压缩成 `M`，数字最多保留三位有效数字。

快捷键:
- `ctrl+v`: 从剪贴板粘贴图片时插入 `[Image N]` 图片芯片；连续粘贴会按顺序生成多个芯片，芯片可以像一个整体一样删除。剪贴板没有图片时保持原有文本粘贴行为。
- `ctrl+o`: 展开/折叠模型 thinking 过程；折叠时 thinking 仍保存在 transcript 中，但不渲染到 viewport。
- `ctrl+g`: 打开右侧 Subagents 选择器；使用 ↑↓ 选择，Enter 预览该 subagent transcript，Esc 返回主 session transcript；输入框内容和提交目标保持主 session 不变。

鼠标选择:
- 在消息历史区按住左键拖拽即可选择文本；拖到面板顶部/底部会自动滚动；释放时把选中内容写入系统剪贴板（本地剪贴板 + OSC 52 终端剪贴板双写，SSH/远程会话同样可用），并在状态栏短暂提示「已复制 N 字符」。
- **双击**选中一个词（中文按词/标点断开，不切开 emoji 与宽字符），**三击**选中整行；双击/三击后继续拖拽会按词/行边界扩展选区。
- 单击不复制、不创建选区：链接、todo 和工具行等可点击位置在双击判定窗口（250ms）结束后触发动作，避免「单击已生效、双击又选中」互相冲突。
- 16 色及以下终端自动把选区降级为反色渲染，与终端原生选区观感一致。

应用内选区与终端原生选择并存：paw 启用鼠标捕获后，普通拖拽是应用内选区；按住下方表格中的修饰键拖拽，终端会接管并做原生选择（用于复制到终端自己的剪贴板、跨应用选词等）：

| 终端 | 原生选择修饰键 |
| --- | --- |
| Ghostty / kitty / WezTerm / Alacritty | **Shift** + 拖拽 |
| iTerm2 | **Option** + 拖拽 |

可选：希望「Shift+拖拽」的原生选区与 paw 自绘选区同色时，可在 Ghostty 配置中加入（kitty 为 `selection_background`/`selection_foreground`，WezTerm 为 `colors.selection_bg`/`colors.selection_fg`）：

```ghostty
selection-background = #4c5064   # tokyo-night 主题示例；其他主题见 docs/mouse-selection-research.md §4.3
selection-foreground = #c0caf5
```

图片输入:
- 当前支持 PNG、JPEG、GIF、WebP 和 BMP；macOS 使用系统 `NSPasteboard`（JXA/AppKit，兼容截图常见的 PNG/TIFF 类型），Linux 优先尝试 `wl-paste`，并保留 `xclip` 适配入口。
- 图片会保存到当前项目的 `.paw/attachments/`，使用内容哈希去重并以 `0600` 权限保存；会话 JSONL 只记录相对附件引用，不写入 base64 图片。
- 提交图片需要当前 provider 的多模态模型：OpenAI-compatible endpoint 使用 `image_url` data URL，Anthropic-compatible endpoint 使用 base64 `image` block。纯文本请求仍保持原有字符串格式；不支持图片的 endpoint 会报错并保留输入草稿。

当前默认 settings:

```json
{
  "subagent": {
    "default_context_mode": "empty",
    "default_run_mode": "sync"
  },
  "ui": {
    "theme": "default",
    "context_limit_tokens": 1048576,
    "context_meter_location": "input-above"
  },
  "context_maintenance": {
    "soft_compact_ratio": 0.5,
    "tool_result_snip_ratio": 0.6,
    "compact_ratio": 0.8,
    "compact_force_ratio": 0.9,
    "compact_target_ratio": 0.5,
    "tail_tokens": 16384,
    "min_tool_result_bytes": 1024,
    "keep_errors": true,
    "keep_user_marked": true,
    "archive_enabled": true
  }
}
```

上下文维护按 50%/60%/80%/90% 压力阈值依次提示、裁剪旧大型 Tool Result、执行摘要压缩并在高压时强制压缩。被重写的原始消息会先归档到 `.paw/sessions/<session-id>/compactions/`；session journal（`transcript.jsonl`）仍保留完整 Tool Result，归档只服务于压缩投影、去重和恢复。

内置 True Color 主题 ID：`default`、`tokyo-night`、`tokyo-night-storm`、`tokyo-night-light`、`catppuccin-mocha`、`dracula`、`gruvbox-dark`。首版不支持自定义主题文件、256 色降级或 `NO_COLOR`。

## 分层

当前分为 5 层。

补充:
- `session`、`settings`、`subagent`、`streamma` 是新增的运行时支撑模块，负责 `.paw/` 持久化、用户默认配置、子代理调度和 StreamMA runtime；主对话链路仍按下面的 5 层理解即可。
- `internal/skill` 负责发现本地 `skill-name/SKILL.md`、解析输入中的 skill 引用，并把选中的 skill 文件格式化为当前 turn 的 system context。
- `internal/streamma` 是独立的内存版 multi-agent runtime，目前覆盖 fake model + runtime 验收，并通过 `/streamma <prompt>` 接入 `loop.Runner` 的显式分支；交互入口会使用真实 subagent worker 作为 StreamMA agent，生产版 NATS、Postgres、MinIO 适配器仍未接入。
- `internal/tokentracer` 提供 token 用量追踪与本地 HTTP dashboard。

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
- `Parts` (`[]ContentPart`) — 有序 text/image 片段（富文本多模态消息；`Content` 保留为兼容表示）
- `ToolUse` (`*ToolCall`) — 工具调用请求（assistant 发出，单个）
- `ToolUses` (`[]ToolCall`) — 工具调用请求（assistant 发出，多个）
- `ToolResult` (`*ToolResult`) — 工具执行结果（user 角色发出，单个）
- `ToolResults` (`[]ToolResult`) — 工具执行结果（user 角色发出，多个）

##### `type ContentPart struct`

字段:
- `Type` — `text` 或 `image`
- `Text` — 文本内容
- `Image` (`*ImagePart`) — 图片片段

##### `type ImagePart struct`

字段:
- `MIMEType` — 图片 MIME 类型
- `Attachment` — 附件相对引用（持久化时写入，不写 base64）
- `Data` (`[]byte`) — 仅在提交/物化请求时内存持有，JSON 序列化忽略

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
- 读取 `AGENTS.md` 项目指令并注入 system prompt
- 触发上下文自动压缩（保留完整 journal）
- 通过 Turn Journal 增量持久化每轮消息（turn_started / assistant_message / tool_result / turn_completed / turn_failed）

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
- `Parts`
- `ToolUse`
- `ToolUses`
- `ToolResult`
- `ToolResults`

用法:
- 普通文本消息: `Role + Content`
- 富文本多模态消息: `Role + Parts`
- 工具调用消息: `Role + ToolUse` / `Role + ToolUses`
- 工具结果消息: `Role + ToolResult` / `Role + ToolResults`

#### `type ContentPart` / `type ImagePart`

用途:
- 描述有序的 text/image 片段
- 图片以附件引用持久化，以内存 `Data` 物化到 provider 请求

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
- `ProfileID` / `ProfileName`
- `Provider` / `Transport`
- `APIBaseURL`
- `APIPath`
- `APIKey`
- `APIKeyEnvName`
- `Model`
- `Models`
- `ExtraBody`（`RequestBody`，合并进每个 provider 请求体的任意 JSON 对象）
- `ModelExtraBody`（`map[string]RequestBody`，按模型名附加的请求体）
- `ContextLimitTokens` / `ModelContextLimitTokens`（上下文 token 上限，可按模型覆盖）
- `Timeout`
- `RetryCount`（持久化为 `~/.paw/config.json` 当前 model profile 的 `retryCount`；网络请求失败或遇到 408/425/429/5xx 时的重试次数，默认 3）
- `Stream`（持久化为 `~/.paw/config.json` 当前 model profile 的 `stream`；默认 `true`，只有显式写为 `false` 才使用非流式请求）
- `Profiles`（全部已配置 profile）

##### `type Profile struct`

字段:
- `ID` / `Name` / `Provider` / `Transport`
- `APIBaseURL` / `APIPath` / `APIKey` / `APIKeyEnvName`
- `Model` / `Models`
- `ExtraBody` / `ModelExtraBody`
- `ContextLimitTokens` / `ModelContextLimitTokens`
- `Timeout` / `RetryCount` / `Stream` / `StreamSet`
- `CredentialID`

##### `LoadConfigFromEnv() (Config, error)`

职责:
- 从环境变量构造 `Config`
- 启动时按顺序尝试加载当前目录下的 `.env`、`.env.local`
- 读取 `~/.paw/config.json` 中 `modelProfiles` 的 profile；优先使用 `activeModelProfileId`，没有时使用第一个 profile
- 每个 profile 的 `models` 是 `/model` 向导的二级模型列表；profile 没有单独的 `model` 时使用 `models[0]`
- 文件不存在时自动创建空的 `config.json`，不会注入 provider、API URL、API path 或模型名；保存时保留其他全局配置字段
- `.env.local` 会覆盖 `.env` 和外部 shell 继承进来的同名变量

配置示例（字段名可按 profile 实际情况调整）：

```json
{
  "schemaVersion": 1,
  "modelProfiles": [
    {
      "id": "local-gateway",
      "name": "Local Gateway",
      "provider": "local-gateway",
      "transport": "openai-compatible",
      "baseUrl": "http://127.0.0.1:8317/v1",
      "apiPath": "/chat/completions",
      "apiKeyEnvName": "LOCAL_GATEWAY_API_KEY",
      "models": ["model-a", "model-b"]
    }
  ],
  "activeModelProfileId": "local-gateway"
}
```

代码只负责按 `transport` 和 profile 中的 endpoint 发请求；不会内置 provider、API URL、API path、API key 或模型名。

##### `type RequestBody map[string]any`

任意 JSON 对象，通过 `extraBody` / `modelExtraBody` 合并进 provider 请求体。受保护字段（如 `model`、`system`、`messages`、`tools`、`stream` 等）不允许出现在 extra body 中；`ValidateExtraRequestBodies` 会在加载配置时校验。

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
- 解析流式响应

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

##### `StreamMessage(ctx, messages, tools) (<-chan StreamEvent, error)`

职责:
- 发起流式请求（按 `transport` 分发到 OpenAI-compatible 或 Anthropic-compatible 流式解析）
- 返回事件 channel
- `tools` 为原生工具定义（`[]model.ToolDefinition`），用于 LLM API 的原生工具调用请求

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

#### Anthropic 流式层

文件: [anthropic_stream.go](./internal/model/anthropic_stream.go)

职责:
- 针对 Anthropic Messages API 的流式解析（`message_start` / `content_block_start` / `content_block_delta` / `message_delta` 等）
- 处理 thinking、text、tool_use 内容块
- 支持 system prompt 的 `cache_control`（`type: "ephemeral"`）
- 把 provider 返回的 usage 汇总到 `StreamEvent.Usage`

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
- `RequireBoundary`

当前默认:
- `Boundary` 为空时使用 `END_STEP`
- step contract 是 `Exact+END_STEP`
- `RequireBoundary=true` 时，缺失最终 sentinel 的内容会触发 parser fatal，不产生 recovered step，也不会传播给下游 agent

##### `type AgentStreamer interface`

方法:
- `StreamAgent(ctx, invocation) (<-chan model.StreamEvent, error)`

职责:
- 接收结构化 `AgentInvocation`，包含 `run_id`、`agent_id`、role、system prompt、problem、invocation index、input event、新 inbound step 和 transcript snapshot
- 真实 `/streamma` 路径使用该接口把同一 logical agent 绑定到同一个 subagent session，并按论文模型增量 append 新 step

##### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx, messages, tools) (<-chan model.StreamEvent, error)`

用途:
- 作为内存 runtime / fake model / 兼容测试的 adapter 输入
- adapter 会从 `Transcript` 构造完整 prompt，并以 `tools = nil` 调用模型；stream 中的 tool event 默认不会触发工具执行

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
- `RequireBoundary=false` 且缺失最终 sentinel 但仍有内容时才 forced close，并设置 `BoundaryRecovered`
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
- 在真实 `/streamma` 路径中作为 runtime 审计投影；真实 `ctx_a` 落在每个 logical agent 的 subagent session history 中

##### `BuildPrompt(transcript) []message.Message`

职责:
- 从 transcript 构造模型输入
- 固定 system prompt 和 original problem
- 不把 event id、timestamp、trace、seq 等动态 metadata 写进 prompt
- 只用于 fake model、内存 runtime 兼容和审计验收；真实 subagent 路径不会反复发送完整 `base + transcript`

##### `BuildPromptSegments(transcript) []PromptSegment`

职责:
- 暴露 cache-stable prompt segment 投影
- 供 prefix cache 相关验收和后续适配器复用

#### Runtime

文件: [runtime.go](./internal/streamma/runtime.go)

##### `NewRuntime(config) (*Runtime, error)`

职责:
- 编译 DAG
- 注入 `AgentStreamer`；旧 `ModelStreamer` 会通过兼容 adapter 包装为 `AgentStreamer`
- 初始化 broker、event log 和 agent runtime state

##### `RunGraph(ctx, spec, model, problem) (RunResult, error)`

行为:
- Chain、Tree、Graph 都由同一 DAG runtime 执行
- Runtime 边读模型 stream 边提交 `StepPacket`，step 一闭合就 `FanoutStep`
- 下游 agent 可在上游 agent 尚未 `Done` 时启动自己的模型调用
- 多前驱节点是 arrival-triggered，任意前驱 step 到达即可调用，不等待同步 barrier
- 单个 agent 内部仍按队列顺序串行处理 inbound delivery，避免同一 transcript 被多个 invocation 同时改写
- 真实 subagent 路径按 `run_id + agent_id` 维护 session pool：同一 logical agent 多次 invocation 复用同一个 sessionID，不同 agent 和不同 run 不共享 session
- 首次 subagent invocation 写入 stable system prompt、original problem 和当前输入；后续 invocation 只写入新增 inbound step，避免重复写入旧 transcript
- `/streamma-trace` 的 `subagent.started` / `subagent.finished` 会显示 invocation index、sessionID 和 provider 返回的 usage/cache 字段；cache 命中只来自 provider usage，不做本地估算
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

##### `type ConcurrencySafeTool interface`

方法:
- `IsConcurrencySafe(input json.RawMessage) bool`

用途:
- 声明某次调用是否可并发执行；runner 会把连续的安全工具调用并行批处理，非安全调用仍串行

##### `type FileMutationTool interface`

方法:
- `FileMutationTarget(input json.RawMessage) (FileMutationTarget, error)`

用途:
- runner 在不执行工具的前提下安全检查目标文件（路径 + 写入前是否存在）
- 当前由 `WriteTool` 和 `EditTool` 实现，用于向 UI 提供真实修改差异

##### `type FileMutationTarget struct`

字段:
- `Path` — 解析后的工作区绝对路径
- `BeforeExists` — 写入前目标是否存在

#### 注册层

文件: [register.go](./internal/tool/register.go)

##### `type Registry struct`

职责:
- 按名称保存工具实例
- 支持 namespace 级原子替换（MCP 动态工具）

##### `NewRegistry() *Registry`

职责:
- 创建注册表

##### `Register(tool Tool)`

职责:
- 注册工具

##### `Get(name string) (Tool, bool)`

职责:
- 按名称查找工具

##### `ReplaceNamespace(namespace string, tools []Tool) error`

职责:
- 原子替换属于某逻辑 namespace 的全部工具
- 不与其它 namespace 的工具发生覆盖冲突

用途:
- MCP 能力快照变化时更新注册表

##### `RemoveNamespace(namespace string)`

职责:
- 移除某逻辑 namespace 的全部工具

##### `Describe() []string`

职责:
- 生成工具说明文本
- 当前也会附带 `input_schema`

用途:
- 给 `Runner` 拼 system prompt

##### `DescribeBrief() []string`

职责:
- 生成不带 schema 的工具说明文本

##### `Definitions() []model.ToolDefinition`

职责:
- 返回 `[]model.ToolDefinition`，用于 LLM API 的原生工具调用请求

##### `IsConcurrencySafe(name string, input []byte) bool`

职责:
- 查询某次调用是否可并发执行

#### 文件工具

文件:
- [path.go](./internal/tool/file/path.go)
- [ls.go](./internal/tool/file/ls.go)
- [read.go](./internal/tool/file/read.go)
- [read_state.go](./internal/tool/file/read_state.go)
- [write.go](./internal/tool/file/write.go)
- [edit.go](./internal/tool/file/edit.go)
- [atomic.go](./internal/tool/file/atomic.go)
- [mutation_path.go](./internal/tool/file/mutation_path.go)
- [grep.go](./internal/tool/file/grep.go)
- [glob.go](./internal/tool/file/glob.go)

##### `type LSTool struct`

字段:
- `Root`
- `ReadRoots`

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
- `ReadRoots`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `IsConcurrencySafe(input) bool`
- `Run(ctx, input)`

职责:
- 读取工作区内文件内容
- 读取成功后把内容哈希记录到 `ReadStateStore`，作为后续 Edit/Write 的基线

输入格式:

```json
{
  "file_path": "go.mod"
}
```

##### `type WriteTool struct`

字段:
- `Root`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `FileMutationTarget(input) (FileMutationTarget, error)`
- `Run(ctx, input)`

职责:
- 覆盖写入工作区内文件（`atomicWriteFile` 原子写入）
- 若模型此前 Read 过该文件，写入前校验磁盘内容仍匹配记录基线，防止丢失更新

输入格式:

```json
{
  "file_path": "notes.txt",
  "content": "hello"
}
```

##### `type EditTool struct`

字段:
- `Root`
- `ReadState` (`*ReadStateStore`)

方法:
- `Name()`
- `Description()`
- `InputSchema()`
- `FileMutationTarget(input) (FileMutationTarget, error)`
- `Run(ctx, input)`

职责:
- 对工作区内已先用 Read 读取的文件做精确字符串替换（对齐 Claude Code 的 Edit 契约）
- 目标必须是常规文件、必须先被 Read 记录基线、`old_string` 必须逐字节匹配且默认唯一
- 写入使用 `atomicWriteFile` 原子替换；成功后更新 ReadState 基线

输入格式:

```json
{
  "file_path": "internal/foo.go",
  "old_string": "return 1",
  "new_string": "return 2",
  "replace_all": false
}
```

行为:
- `old_string` 未命中 → 报错并提示必须与文件内容精确匹配
- 命中多处且未设 `replace_all` → 报错并提示补充上下文或设置 `replace_all=true`
- 自上次 Read 后文件被外部修改 → 报错并要求重新 Read

##### `type ReadStateStore struct`

职责:
- 按路径记录最近一次 Read 的内容哈希（sha256）
- 为 Edit/Write 提供 stale-write / lost-update 保护

方法:
- `Record(path, content)` — 记录基线
- `Verify(path, current) error` — 有基线时校验当前内容仍匹配；无基线时宽松返回 nil
- `VerifyRequired(path, current) error` — 必须有基线且匹配（Edit 使用）
- `RecordAfterWrite(path, content)` — 写入后刷新基线，避免连续 Edit 误报

##### `atomicWriteFile(target, content, mode) error`

职责:
- 同目录写临时文件后 rename，崩溃不会留下半写文件
- 自动创建父目录并显式应用权限位

##### `resolveMutationPath(root, target, allowMissing) (string, bool, error)`

职责:
- 解析工作区内路径
- 阻止路径经符号链接逃出工作区根目录
- `allowMissing=true` 时解析最近存在的祖先，校验缺失后缀不越界

##### `resolvePathWithinRoot(root, target) (string, error)`

职责:
- 解析文件路径
- 阻止路径逃出工作区根目录

##### `type GrepTool struct`

字段:
- `Root`
- `ReadRoots`

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
- `ReadRoots`

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
- 响应体截断到 32 KiB，超出时追加 `[response truncated]`

输入格式:

```json
{
  "url": "https://example.com",
  "timeout_seconds": 30
}
```

#### 选择工具（交互）

目录: [select](./internal/tool/select/)

职责:
- 在主 TUI 中渲染阻塞式单选/多选 prompt，等待用户提交或取消
- 通过 `Broker` 把工具调用桥接到 Bubble Tea UI

##### `type Broker struct`

职责:
- 维护请求队列与活动请求
- `Ask(ctx, request)` 阻塞等待用户结果
- `NextEvent(ctx)` / `Complete(id, result)` / `Close()` 供 UI 侧消费与回填

##### `type Request struct`

字段:
- `ID` — 由 broker 分配的请求 ID（`select-<n>`）
- `Prompt`
- `Mode` — `single` / `multiple`
- `Options` — `[]Option{ID, Label, Description}`
- `InitialSelectedIDs`
- `MinSelect` / `MaxSelect`

##### `type Result struct`

字段:
- `Cancelled`
- `SelectedOptions` — `[]SelectedOption{ID, Label}`

行为:
- 单选模式必须恰好选中一个选项；多选模式校验 `min_select` / `max_select`
- 支持一个 `custom_option` 自定义选项（`id` 为保留值 `custom_option`，需提供 label）
- 结果会与请求的 canonical 选项做一致性校验，非法结果返回错误

输入格式:

```json
{
  "prompt": "Pick one",
  "mode": "single",
  "options": [
    {"id": "a", "label": "Option A"},
    {"id": "b", "label": "Option B"}
  ]
}
```

#### MCP 工具适配

文件: [mcp/tool.go](./internal/tool/mcp/tool.go)

##### `type Tool struct`

字段:
- `spec` (`coremcp.ToolSpec`)
- `broker` (`coremcp.Broker`)

职责:
- 把一个 MCP 能力适配为 Paw 的普通工具接口
- broker 可以是主进程 Manager，也可以是 subagent 转发代理

方法:
- `Name()` / `Description()` / `InputSchema()` / `Namespace()` / `Run(ctx, input)` / `Spec()`

### `internal/ui`

#### 抽象层

文件: [ui.go](./internal/ui/ui.go)

##### `type ToolCallEvent struct`

字段:
- `ID`
- `Name`
- `Input`
- `FileMutationKnown` — 是否知道文件变更目标
- `IsFileMutation` — 是否文件变更类工具
- `FileMutation` (`*FileMutationSnapshot`) — 变更前后快照

##### `type ToolResultEvent struct`

字段:
- `ToolUseID`
- `Name`
- `Content`
- `IsError`
- `FileMutationKnown`
- `IsFileMutation`
- `FileMutation` (`*FileMutationSnapshot`)

##### `type FileMutationSnapshot struct`

字段:
- `Before` / `After` — 变更前后内容
- `BeforeExists` / `AfterExists` — 变更前后文件是否存在

用途:
- 只用于 UI 展示真实修改差异，不写入 `message.ToolResult` 或 tracing payload

##### `type SystemEvent struct`

字段:
- `Title`
- `Body`
- `Color`

##### `type UI interface`

方法:
- `OnAssistantDelta(text string) error`
- `OnToolCall(event ToolCallEvent) error`
- `OnToolResult(event ToolResultEvent) error`
- `OnDone() error`

这是 loop 层唯一依赖的 UI 抽象。

##### `type ThinkingDeltaReceiver interface`

方法:
- `OnThinkingDelta(text string) error`

用途:
- 可选扩展：接收模型 thinking 流

##### `type SystemNotifier interface`

方法:
- `OnSystemMessage(event SystemEvent) error`

用途:
- 可选扩展：接收后台任务完成等系统事件

##### `type FileMutationConsumer interface`

方法:
- `ConsumesFileMutations() bool`

用途:
- 只有选择消费的 UI 才会让 runner 去检查变更目标或读取文件快照

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

#### Bubble Tea 实现

目录: [bubble](./internal/ui/bubble/)

职责:
- 完整 TUI：消息历史、输入框、context meter、worktree 状态行、slash 命令、补全弹窗、subagent 面板、Select dock、图片芯片、thinking 折叠
- 使用 ESC 聚合 reader 防止鼠标/键盘序列被读边界切断（`escCoalescingReader`）
- 输出带光标锚点修正的终端流

### `internal/loop`

文件: [runner.go](./internal/loop/runner.go)

#### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error)`

作用:
- 抽象模型流式能力

这让 `Runner` 不直接依赖具体 provider 类型。

#### `type HistoryStore interface`

方法:
- `LoadResolvedHistory(ctx, sessionID) ([]message.Message, error)`
- `Append(ctx, sessionID, msgs ...message.Message) error`

作用:
- 抽象历史加载/追加

#### `type Runner struct`

字段:
- `model` (`ModelStreamer`) — 最小模型流式接口
- `ui` (`ui.UI`) — 输出界面接口
- `registry` (`*tool.Registry`) — 工具注册表
- `store` (`HistoryStore`) — 历史存储接口
- `sessionID` — 当前会话 ID
- `workRoot` — 工具使用的 workspace 根目录
- `prompt` (`*PromptBuilder`) — 系统提示词构建器（含 AGENTS.md 项目指令）
- `history` — 已成功完成的多轮对话消息列表（内存缓存）
- `usage` / `sessionUsage` — 当前轮/整个会话的 token 用量统计
- `recovery` (`*session.RecoveryState`) — 最近一次未正常完成 turn 的恢复状态
- `supplements` — 用户在当前轮运行期间提交的补充指令（支持并发注入）
- `skillRegistry` / `activeSkillContext` — 本地 skill 发现与当前 turn 的临时 skill 指令上下文
- `streamMAEnabled` / `streamMASubagents` — StreamMA 开关与 subagent runner 适配器
- `tokenTracer` / `traceStageID` / `traceAgentID` — Token Tracer 与当前 pipeline 定位
- `nowFn` — 可注入时钟（测试用）

职责:
- 驱动单次 agent turn
- 在成功 turn 后维护内存中的多轮历史
- 用模型服务端 usage 字段更新 context 计量，不做本地 token 估算
- 通过 Turn Journal 增量持久化每轮消息

#### `NewRunner(model, output, registry) *Runner`

职责:
- 创建调度器（空 instruction root）

#### `NewRunnerWithInstructionRoot(model, output, registry, store, sessionID, instructionRoot) *Runner`

职责:
- 创建带项目指令根目录的调度器
- 构造 `PromptBuilder(NewInstructionManager(root))` 与 skill registry
- 默认开启 StreamMA

#### `RunTurn(ctx, input) (message.Message, error)`

职责:
- 执行一次完整 turn

当前逻辑:
1. 验证与初始化：检查 runner 初始化状态；持久化输入中的图片附件；按输入中的 `$skill` 或 `[$skill](.../SKILL.md)` 解析并加载当前 turn 的 skill 指令；首次运行时从 store 加载历史（含 Turn Journal snapshot 与 recovery 状态）
2. 构建本轮历史副本：复制已提交历史，插入未注入的 supplements，再追加当前用户输入（失败时不污染已提交历史）
3. 多轮工具循环（最多 500 轮）：
   - 每轮开始时检查是否有新注入的 supplements 并追加
   - 首轮前检查是否需要自动上下文压缩（`maybeCompactHistory`），压缩时保留最近消息与用户约束原文
   - 调用 `runModelTurn`：构造 system prompt + 历史消息，通过 `model.StreamMessage` 发送给 LLM 并消费流式事件
   - 若返回消息不含 ToolUse，调用 `commitHistory` 持久化并返回该 assistant 消息
   - 若含 ToolUse，调用 `runToolCall` 执行工具（连续并发安全的调用会并行批处理），将 tool_result 追加到历史副本，继续下一轮
4. 超限保护：超过 500 轮返回错误

#### `ResetHistory()`

职责:
- 清空内存历史
- 清空最近一次 usage 计量

当前用途:
- REPL 的 `/clear`

#### `runModelTurn(ctx, history) (message.Message, error)`

职责:
- 从 registry 取原生工具定义，构造模型消息并消费流式事件

#### `buildModelMessages(history) []message.Message`

职责:
- 把内存中的 `history` 转成喂给模型的消息列表

#### `buildSystemPrompt() string`

职责:
- 生成 system prompt
- 注入工具说明、输入 schema、当前 turn 的 skill 指令和额外 system supplement

#### `renderMessageForModel(msg) message.Message`

职责:
- 把内部消息编码成模型输入消息

#### `consumeStream(ctx, events) (message.Message, error)`

职责:
- 消费模型流式事件

#### `parseAssistantMessage(content) message.Message`

职责:
- 判断模型输出是普通文本还是 `tool_use` JSON

#### `runToolCall(ctx, call) (message.ToolResult, error)`

职责:
- 向 UI 发工具调用事件
- 执行工具
- 向 UI 发工具结果事件
- 返回 `ToolResult` 消息

#### `executeToolCall(ctx, call) message.Message`

职责:
- 只执行工具，不做 UI 输出

#### `prepareFileMutation(call) *fileMutationCapture`

职责:
- 若 UI 消费文件变更快照且工具实现 `FileMutationTool`，在工具执行前捕获目标路径与磁盘内容

#### `maybeCompactHistory(ctx, history) ([]message.Message, *ContextCompactionResult, error)`

职责:
- 上下文接近上限时自动压缩历史（非阻塞、失败可跳过）
- 详情见 `internal/loop/context_compaction.go`

#### 指令管理

文件: [instruction_manager.go](./internal/loop/instruction_manager.go)

##### `type InstructionManager struct`

职责:
- 从工作区向上查找 `AGENTS.md`，缓存其内容作为“项目指令”
- 内容作为 inert text 注入 system prompt，不执行

方法:
- `NewInstructionManager(root)`
- `ProjectInstructions() string`

#### 提示词构建

文件: [prompt_builder.go](./internal/loop/prompt_builder.go)

##### `type PromptBuilder struct`

职责:
- 以稳定顺序组装 system prompt：默认指令 → AGENTS.md 项目指令 → 工具说明 → 工具调用格式约定

方法:
- `Build(toolDescriptions []string) string`

#### 上下文压缩

文件: [context_compaction.go](./internal/loop/context_compaction.go)

##### `type ContextCompactionResult struct`

字段:
- `BeforeMessages`
- `AfterMessages`
- `FoldedMessages`
- `Summary`

职责:
- 描述一次压缩前后的消息数量与摘要

##### 行为

- 压缩由模型生成摘要，保留路径、标识符、版本、数字、用户约束、编辑、命令结果与未完成工作原文
- 压缩只影响模型上下文，完整 journal 始终保留
- 超时 90 秒，失败时跳过并在 UI 提示

#### Turn Journal

文件: [journal.go](./internal/session/journal.go)

##### `type TurnJournal interface`

方法:
- `BeginTurn(ctx, sessionID, turnID, messages ...) error`
- `AppendAssistant(ctx, sessionID, turnID, msg) error`
- `AppendToolResult(ctx, sessionID, turnID, callIndex, result) error`
- `CompleteTurn(ctx, sessionID, turnID) error`
- `FailTurn(ctx, sessionID, turnID, err) error`
- `LoadSnapshot(ctx, sessionID) (SessionSnapshot, error)`

职责:
- 增量持久化一轮的每条消息与工具结果
- `SessionSnapshot` 区分 UI 展示消息与喂给模型的 safe history
- `RecoveryState` 记录未完成 turn 的已完成工具结果与丢弃的工具调用，重启后可恢复

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

##### `Stream(ctx, req) (Stream, error)`

职责:
- 以同步方式启动子 agent 并返回流式事件 channel

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

##### `TotalSubagentTokens(parentSessionID string) int`

职责:
- 返回指定父会话下全部已完成任务的 token 总量

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
- `Name` / `Color` — 任务展示名与颜色
- `SessionID` / `ParentSessionID`
- `Description` / `Prompt` / `SystemPrompt`
- `ContextMode` / `RunMode`
- `Status` — running / completed / failed / stopped
- `TranscriptPath` / `OutputPath`
- `PID` / `ExitCode`
- `Depth` / `ParentTaskID`
- `StartedAt` / `FinishedAt`
- `Content` / `Error`
- `UsedTokens` / `Usage`

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
- `Theme` — 内置主题 ID，非法值回退到 `default`
- `ContextLimitTokens` — 上下文 token 上限（默认 1,048,576）
- `ContextMeterLocation` — context meter 位置（`"input-title"` / `"header"` / `"input-above"`）

#### `type Controller struct`

职责:
- 加载、保存、提供运行时配置
- 线程安全读写

##### `NewDefaultController(homeDir HomeDirFunc) (*Controller, error)`

职责:
- 从 `~/.paw/settings.json` 加载全局配置并创建 Controller；`homeDir` 可注入以隔离测试

##### `CurrentSettings() Config`

职责:
- 获取当前配置（线程安全，nil 安全）

##### `SaveSettings(cfg Config) error`

职责:
- 持久化配置到磁盘并更新内存（线程安全）

##### `Normalize(cfg Config) Config`

职责:
- 规范化配置字段值（大小写无关，非法值回退到默认）

### `internal/tokentracer`

目录: [tokentracer](./internal/tokentracer)

职责:
- 记录普通对话、工具调用、StreamMA runtime events、subagent usage/cache 的 token 用量
- 提供本地 HTTP dashboard（`/` 实时页面、`/api/state` 快照、`/events` SSE）

核心类型:
- `Tracer` — 内存聚合器，按 pipeline → stage → agent 组织
- `Snapshot` — 可审计的完整快照（含 timeline 与事件流）
- `Timeline` / `Event` — 时间线视图与事件记录

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
- 可选扩展：`tool.ConcurrencySafeTool`（并发批处理）、`tool.FileMutationTool`（文件变更快照）

### 3. UI 抽象

接口: `ui.UI`

用途:
- 隔离 loop 和具体输出方式

替换方式:
- 可将 `headless` 换成 TUI、日志型 UI、测试 UI
- 可选扩展：`ui.ThinkingDeltaReceiver`、`ui.SystemNotifier`、`ui.FileMutationConsumer`

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

可选能力:
- 实现 `IsConcurrencySafe` 启用并行批处理
- 实现 `FileMutationTarget` 让 UI 展示真实文件差异

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

### 接入新 MCP server

位置:
- `~/.paw/mcp.toml`（用户侧）
- `internal/mcp/`（协议实现）

扩展方式:
- 添加 `[mcp_servers.<name>]` 表并 `enabled = true`
- 发现的能力自动以 `<server>__<tool>` 名称注册进 `tool.Registry`

## 当前不属于扩展面的函数

下列函数是内部实现细节，不建议作为外部依赖面：

- `loop` 中的输出状态函数
- `model/stream.go` 中的 SSE 解析函数
- `bash.go` 中的输入解码和缓冲细节
- `select/input.go` 中的输入解码与校验

如果要扩展功能，优先从这几个位置下手：
- `tool.Tool`
- `ui.UI`
- `loop.ModelStreamer`
- `main.buildRunner()`
- `subagent.Manager`
- `tool.Registry.ReplaceNamespace`（动态工具命名空间）

## 启动要求

### 环境变量

```bash
export LOCAL_GATEWAY_API_KEY=your_key
```

也支持在项目根目录放一个不会进 git 的 `.env.local`：

```bash
cp .env.local.example .env.local
# `apiKeyEnvName` 应与 profile 中声明的环境变量名一致
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
go run ./cmd/agent -subagent-worker
```

该模式需要父进程先发送 `worker.start`；随后 worker 可以发送 `mcp.call`，父进程回传对应的 `mcp.result`，最后以 `worker.result` 结束本轮任务。
