# 使用与配置

[文档导航](../README.md)

## 入口

长程任务可以使用 [Goal 指标探索与 Plan 对话规划](../reference/goal-plan.md)：Goal 接受可量化验收目标并自主探索实现方法；Plan 用于讨论、保存方案，明确 `/plan execute` 后才实施。

文件: [cmd/paw/main.go](../../cmd/paw/main.go)

### `main()`

按以下顺序分发，具体运行时由对应入口创建：

| 输入 | 入口 | 用途 |
| --- | --- | --- |
| `tracer` | `tracer.Run` | 独立只读全局消耗看板，不装配对话运行时 |
| `serve` | `serve.Run` | 本机浏览器工作台 |
| `-task-worker-pool` | `worker.RunPool` | 父进程启动的常驻 worker |
| `-task-worker` | `worker.Run` | 父进程启动的单次 worker |
| `-p "xxx"` | `oneshot.Run` | 无界面单轮对话 |
| 无上述参数 | `interactive.Run` | Bubble Tea 终端界面 |

不负责:
- 直接调用模型
- 直接执行工具
- 维护对话状态

#### `parseOptions() options`

职责:
- 读取 `-p`
- 读取 `-s`
- 读取内部参数 `-task-worker` / `-task-worker-pool` / `-sandbox-limits`
- 读取 `-yolo` / `-dangerously`
- 读取 `-streamma` / `-token-tracer` / `-token-tracer-open` / `-token-tracer-port`（均有 `PAW_*` 环境变量默认值）

行为:
- `-p` 有值: 执行单轮
- `-p` 为空: 进入交互式对话界面
- `-s` 有值: 绑定到指定 session 并恢复历史
- `-s` 为空: 每次启动都创建一个全新空 session
- worker: 以子进程方式运行，使用父进程转发 MCP 调用的双向 JSON 行协议
- `serve` 和 `tracer` 在 `parseOptions` 前分发，各用独立的 FlagSet

#### 运行时装配

各个 [entry 入口](../../internal/entry) 把参数转为 `app.WorkspaceRuntimeOptions`，直接调用 `app.BuildWorkspaceRuntime`。可执行入口只委托给 `entry/cli.Main`，不再保留中间装配别名或转发包装。

真正的依赖装配点是 `app.BuildWorkspaceRuntime`：加载配置、创建模型客户端与会话存储、连接 settings/task/MCP/遥测、注册工具，并创建 `sessionactor.Host` 和 `loop.Engine`。调用者通过 runtime 字段使用这些依赖，通过 `Close()` 统一释放；`runtime.SessionHost` 的类型是 `*sessionactor.Host`，不是旧版 `loop.Engine`。

当前持久化目录统一以 `PAW_CONFIG_HOME` 为根，未设置时为 `~/.paw`，不再使用 `os.UserConfigDir()/Paw`。以下路径均相对该根目录（完整配置契约见下文“配置系统 v2”）：
- `config.jsonc`（Provider/Model registry、YOLO、模型发现、JSONC、Schema 与热重载；不再读取旧 `config.json`）
- `settings.json`、`mcp.toml`、`skills/`
- `projects/<id>/config.jsonc`（项目级活动模型和安全模型参数覆盖；不再读取工作区 `.paw/config.jsonc`）
- `projects/<id>/sessions/<sessionID>/`（会话日志及 `compactions/` 压缩归档）
- `projects/<id>/attachments/`、`projects/<id>/tasks/`、`projects/<id>/actors/`
- `projects/<id>/locks/`（文件变更锁，目录 `0700`、锁文件 `0600`）
- `projects/<id>/exports/`（默认对话导出）

`<id>` 为工作区目录名生成的 slug 加规范化绝对路径的 SHA-256 前 8 位，与原全局 session 项目目录命名兼容；同名的不同工作区相互隔离。工作区路径只用于身份识别，不作为内部数据写入位置。旧工作区数据不删除；旧会话和任务仅兼容读取或向全局复制，不回写原处。用户显式导出文件和项目文档仍可写入工作区。

### 配置系统 v2（`config.jsonc`）

Provider/Model、YOLO、安全沙箱与连接配置只以全局 `config.jsonc` 为事实来源；旧 `~/.paw/config.json`（schema v1）不再自动迁移、读取或回写。UI/上下文维护设置仍单独保存在 `settings.json`；首次切换配置目录时仍可独占复制旧 `settings.json`、`mcp.toml` 与 `skills/`，但不会读取其中的模型配置。

最小自定义 provider 示例：

```jsonc
{
  "$schema": "./schemas/config-v2.schema.json",
  "schemaVersion": 2,
  "activeModel": "gateway/gpt",
  "yolo": false,
  "providers": {
    "gateway": {
      "transport": "openai-compatible",
      "endpoint": "https://gateway.example/v1",
      "auth": { "env": ["GATEWAY_API_KEY"] },
      "discovery": {
        "enabled": true,
        "path": "models",
        "format": "openai-list",
        "autoRegister": true
      }
    }
  },
  "models": {
    "gateway/gpt": { "provider": "gateway", "name": "gpt-example" }
  }
}
```

- `yolo` 默认 `false`；`/config` →“通用”可持久化并热应用到主 Agent 和后续 worker。`--yolo` / `--dangerously` 设置本次启动初值，`/yolo` 仍是会话内运行期开关。
- 顶层进程启动时会发现所有合并预设后 `discovery.enabled == true` 的 provider；task worker 不执行 discovery。
- `/config` 新建自定义 OpenAI 兼容 provider 时默认使用 `models` + `openai-list`；服务商新增或修改后会显式刷新一次。
- `discovery.autoRegister` 默认 `false`。开启后，一次**成功**的发现会把上游模型列表写回 `config.jsonc`：上游新增的模型自动注册，上游已不再提供的模型（含手写条目）直接删除；若被删除的正是当前模型，`activeModel` 回退到该 provider 剩下最小的模型 ID。发现失败或写盘失败只产生 warning，不修改配置。
- 普通模型切换和被动文件热重载不重复发起请求，只复用已确认结果或 endpoint/path/format 指纹匹配的缓存。
- 发现需要合法同源 HTTP(S) URL、受支持格式，以及可从 `auth.env` 解析的凭据；失败只产生安全化 warning，并回退到匹配缓存或手工模型。
- JSONC 定向更新保留注释、尾随逗号和未知字段，并通过 revision + 文件内容 CAS 防止并发覆盖。

#### `app.RegisterBuiltinTools`

职责:
- 注册文件工具（LS / Read / Write / Edit / Grep / Glob），其中 Write 与 Edit 共享同一个 `ReadStateStore`
- 注册 Bash、WebFetch
- 注册子任务工具 `Task` / `TaskStatus` / `TaskStop` / `TaskWait`
- 注册 MCP namespaced 工具（`ReplaceNamespace("mcp", ...)`）

交互模式额外通过 `registerInteractiveTools` 注册 `Select` 工具（绑定 TUI selection broker）。

### MCP / CodeGraph

Paw 是 MCP client，主 Agent 在启动时读取统一全局配置目录中的 `mcp.toml`，通过本地 stdio 启动配置的 MCP server。文件不存在时会自动创建为空文件；启用的 server 初始化或能力发现失败会阻止本次启动。

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

#### `oneshot.Run(ctx, opts) error`

职责:
- 在 headless UI 中执行一次 `runner.RunTurn`

输出:
- assistant 最终结果写 stdout
- 当前 sessionID 写 stderr

#### `interactive.Run(ctx, opts) error`

职责:
- 启动 Bubble Tea 主界面
- 创建 Select tool 的 `selecttool.Broker` 并注入 Bubble UI
- 注入模型配置、settings、subagent 控制器
- 以当前 session 进入可恢复的交互式对话
- 按参数启动 Token Tracer dashboard

#### `worker.Run` / `worker.RunPool`

职责:
- 以子进程模式运行（由主 Agent 进程 fork 启动）
- 从 stdin 读取任务协议消息，其中 `WorkerRequest` 携带任务身份、会话、提示词和工具设置
- 经 `runWorkerTurn` 构建带有 `taskRuntimeContext` 的工作区运行时，控制递归深度和父进程 MCP broker
- 执行 `runner.RunTurn()`，将 `WorkerResult`（含 TaskID、SessionID、Content、Error、ExitCode、Usage）写入 stdout

#### 当前交互命令

当前 slash command 由 `internal/ui/bubble/command_registry.go` 统一注册，`/help` 会显示参数提示。

- `/help`
- `/model [status|<profile>|<model>]`
- `/export [filename]`
- `/setting [translate on|off]`
- `/config [reload|status|path]`
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
- `/model` 无参数时打开 Provider → Model 快速切换器；也可以输入 v2 稳定模型 ID（如 `deepseek/chat`）切换并只持久化 `activeModel`
- `/export` 默认导出到 `~/.paw/projects/<id>/exports/conversation-YYYY-MM-DD-HHMMSS.txt`（`PAW_CONFIG_HOME` 可替换 `~/.paw`）；优先按 runner 的显式工作区定位项目，未提供时使用当前目录。也支持工作区内显式路径，保持 `.txt` 导出；导出文件权限为 `0600`
- `/setting` 无参数时与 `/config` 打开统一配置中心；`/setting translate on|off` 是只改内存、立即生效且不写盘的会话级动态开关；`/config reload|status|path` 用于热重载、诊断和路径查询
- `/theme` 打开内置主题选择器；↑/↓、j/k、Home/End 会实时预览，Enter 保存到统一全局配置目录的 `settings.json`，Esc 恢复打开前的主题且不写盘
- `default` 跟随终端：使用终端默认前景/背景与 ANSI 调色板，选区反色；Ghostty 的透明度由 Ghostty 自身设置控制。其他内置主题继续使用固定配色。切回 `default` 会恢复终端光标颜色。
- `/sessions` 列出所有历史会话（ID 前缀、日期、文件大小、首条消息），选中条目后直接恢复该会话
- `/subagent` 支持 `empty` 与 `fork` 两种上下文模式，以及 `sync` 与 `background` 两种运行模式；后台任务完成后会发 UI 系统通知，并把截断后的结果作为补充上下文注入后续模型轮次（完整结果仍在任务 output/transcript 路径中）
- `/streamma <prompt>` 显式把当前任务交给 StreamMA runtime；runtime 会按任务选择一个小型 DAG，并把每个 StreamMA worker 映射为真实 subagent。一次 run 内同一个 logical agent 复用同一个 subagent session 作为真实 `ctx_a`；首次调用写入 agent base context + problem，后续调用只追加新 inbound step。只有同步到精确 `END_STEP` step 后才继续在 DAG 中传播；缺失 `END_STEP` 会失败而不是在 agent `Done` 时兜底传播，最终由 finalizer 的最后一步作为 assistant 回复写回会话历史。可选参数包括 `--profile`、`--topology`、`--agents`/`--a`、`--steps`/`--s`、`--protocol`；默认 `adaptive` profile 会保留任务模板图，显式 `--topology` 或 `paper` profile 可按指定拓扑生成 chain/tree/graph 形状
- `/streamma-trace <prompt>` 使用同一套真实 StreamMA/subagent 路径，并额外输出 live runtime trace（如 `subagent.started`、`agent.step.committed`、`control.upstream_eof`、per-invocation usage/cache），用于观察 step fanout 是否发生在上游 agent `Done` 前，以及同一 agent 是否复用同一 session
- `multi-agent-pipeline` skill 是 Codex/Paw 的阶段化工作流指导，不会自动要求 StreamMA runtime。`/streamma` 和 `/streamma-trace` 是显式 runtime 调试入口；如果只想测试 skill、slash completion、普通 subagent 或 Token Tracer，可用 `PAW_STREAMMA=0` 或 `-streamma=false` 关闭这两个入口
- `/tasks` 展示当前后台 subagent 任务及 transcript 路径
- `/pipeline` 展示当前 pipeline activity 面板
- `/skills` 展示当前可发现的本地 skills 及其 `SKILL.md` 路径
- `/token-tracer` 展示当前启动的 Token Tracer dashboard URL；`/` 为全局消耗页，`/?view=debug` 为当前实例的 Dockview 调试页。`/api/global` 提供全局遥测快照，既有 `/api/state` 和 `/events` 保留单实例调试语义。
- `/compact [focus]` 在保留完整 journal 的前提下压缩模型上下文；`focus` 可指定聚焦压缩方向，压缩会保留最近消息、用户约束与未完成工作原文

Token Tracer:

- StreamMA 可用 `PAW_STREAMMA=0` 或 `-streamma=false` 手动关闭；关闭后输入 `/streamma` 或 `/streamma-trace` 会直接提示已禁用，不会启动 worker，也不会触发 `END_STEP` parser
- 交互模式启动时默认拉起本地 dashboard；可用 `PAW_TOKEN_TRACER=0` 或 `-token-tracer=false` 关闭
- `-token-tracer-port <port>` 指定端口，默认 `8999`；`PAW_TOKEN_TRACER_PORT` 也可设置默认端口
- `-token-tracer-open` 或 `PAW_TOKEN_TRACER_OPEN=1` 会自动在浏览器打开 dashboard
- Dockview 调试页聚合普通对话、工具调用、StreamMA runtime events、StreamMA subagent usage/cache、后台 subagent 任务生命周期，并按 `pipeline -> stage -> agent` 语义展示 token lane；output token 单独统计，不参与 context lane 宽度。

#### 一个看板查看多个项目

```bash
# 在本仓库启动独立看板；无需模型配置，也不取得项目执行锁
go run ./cmd/paw tracer --open
# 已构建/安装新版 Paw 时，在任意目录运行
paw tracer --open
# 可指定本机地址；默认端口 0 自动选空闲端口
paw tracer --listen 127.0.0.1:8998
```

在不同项目窗口正常启动新版 Paw 即可。各进程与看板使用相同 `PAW_CONFIG_HOME`（默认 `~/.paw`）时自动汇聚；不同配置根互相隔离。无需先启动看板，进程退出后仍可查看本次升级以后采集的历史。旧二进制及已有会话日志不会自动回填。相同目录的执行锁仍保留，多项目汇聚不等于解除同目录 controller ownership。

全局页支持期间筛选、项目/会话/模型/工具下钻、请求与实际 HTTP attempt 明细、JSON 导出及深浅主题。当前实例的 Dockview 入口由交互模式服务提供；独立 `paw tracer` 仅提供全局只读视图，不控制其他 Paw 进程。

计量约定：

- 请求数按逻辑 invocation 计数，HTTP 重试按 attempts 记录；累计 usage 快照替换合并，不逐帧重复相加。Responses 在同一逻辑请求、同一 transport 内有相同 provider response ID 时，消费汇总合并该响应的累计快照；仍保留每个 attempt 的原始报告与输入 exposure。缺少响应身份时不猜测去重，不跨逻辑请求合并。
- usage 字段是否存在与最终性分开；失败、截断、未确认完成和旧记录的最终量保持未知，已收到的部分数字仍可见。成功重试不能替前一次失败发送补出最终 usage。
- 输入/输出/缓存按真实协议归一化；缓存是输入子集，reasoning 是输出子集，不再重复加进总量。异常子集不参与细分，保留原始字段并提示 `usage_inconsistent`。
- 系统提示词、输入提示词、历史回答、工具定义/参数/结果按实际出站 occurrence 分解。Chat/Anthropic 的 Paw 文本工具信封通过结构化消息来源校验，不将其误算为用户提示词；工具结果在后续请求重发会再次计入。
- `mixed_text_v1` 是粗估：ASCII 每 4 字符、非 ASCII 每字符约 1 token；不是模型 tokenizer 或逐项账单。图片等未支持内容为未知。普通本地工具执行本身不是 LLM token，工具内部模型调用另计；参数行目前指历史参数的再次输入，不是输出参数的精确计费。
- 费用尚未接价格来源，显示未知；组件估算与供应商报告分开，不按比例虚构费用。缺失 usage/细分显示未知，已报告部分保留可见。

遥测写入 `PAW_CONFIG_HOME/tracer/instances/<随机实例ID>/`，每实例独立 JSONL 文件和心跳 metadata，默认目录 `0700`、文件 `0600`。仅保存身份、用量和原子统计，不新增提示词/工具正文存储；工作区路径、会话/任务 ID、模型及工具名属于 metadata。`-token-tracer=false` 关闭交互看板，不关闭全局采集。写入失败会警告但不阻止 Paw 执行。

当前保留边界为每实例 128 × 16 MiB 文件、全局读模型 4096 实例/100000 请求/100000 原子；达到边界会显示采集缺口，不静默清理数据。单轮扫描预算 64 MiB，积压会标为 `scan_backlog`。不承诺无限历史或零丢失计费审计。

全局查询每 2 秒增量扫描账本，返回当前筛选的完整汇总、24 桶趋势和一页记录（默认 50，可选 100/250），不会在普通轮询中传回全部原子。点击请求才读取完整明细，显式 JSON 导出包含全部匹配记录，不限当前页。项目/会话/模型/工具列表也支持分页；实时新增记录可能移动页边界，汇总不受分页影响。`/api/global` 分页响应版本为 2，原始账本/导出快照仍为 1；升级服务后应刷新浏览器以载入匹配前端。

#### 当前输入区状态

context meter 默认展示在消息历史区下方、输入框上方；输入框保持在窗口底部，不再显示 `Input`、`Waiting`、`Terminal` 标签。状态行内联展示 worktree 元信息（git 仓库名、当前分支/HEAD、clean/dirty/conflict 状态），空间不足时自动让位给 token 信息。

输入补全:
- 在行首输入 `/` 或 `/query` 会显示全部命令与 skill 候选；前面已有内容时只显示 `/subagent`、`/streamma` 和全部 skill，并仅使用末尾当前斜杠词筛选；URL、路径或普通文本内部的斜杠不会触发
- 接受斜杠候选时只替换当前斜杠词，并保留此前输入的文本
- 在输入框中输入 `@` 会弹出工作区文件路径候选列表
- 在输入框中输入 `$` 会弹出 skill 候选列表；Tab 或 Enter 会写入 `[$skill](.../SKILL.md)` 引用
- 使用 ↑↓ 键在候选项之间导航，Tab 或 Enter 确认补全，Esc 关闭弹窗

Skills:
- skills 统一从 `~/.paw/skills/<name>/SKILL.md` 加载（设置 `PAW_CONFIG_HOME` 时改为该目录下的 `skills/`），不读取项目目录、`$CODEX_HOME` 或其他 skills 目录
- 输入中出现 `$skill` 或 `[$skill](/abs/path/SKILL.md)` 时，Runner 会在本轮 system prompt 中注入对应 `SKILL.md` 的完整内容；该注入只对当前 turn 生效，不写入会话历史
- `/subagent` 的 prompt 中显式提到 skill 时，subagent worker 会按同样规则加载；`/streamma` 和 `/streamma-trace` 会把本轮选中的 skill context 传入每个 StreamMA worker 的 system prompt

输入框上沿显示上下文容量条：`▒` 表示已知缓存部分，`█` 表示其余已用部分，`░` 表示剩余容量。宽度足够时显示 `CTX` 百分比和用量；未知时显示 `CTX —`。容量沿用最近一次主会话请求的 input + output 占用，不加入子任务的累计消耗，也不代表任务完成进度。超过配置窗口时条形填满，数字保留实际占用与超限百分比。请求尚未返回用量时沿用上次记录；恢复缺少用量记录的历史会话时沿用原有的历史估算。草稿不计入容量条。

底部“缓存占比”以已用上下文（input + output）为分母；侧栏的 `Cache hit` 以累计 input 为分母，两者统计范围和分母不同。

Activity 侧栏默认展开，上半自动显示最近有实际内容更新的 Todo 或子智能体，下半固定显示 `TOKEN HISTORY · MAIN TURN`。无需切页或切换焦点，可以边输入边看曲线。

- `Turn avg`：本次前台任务累计输出 token ÷ 从任务开始至当前的总耗时，包含等待和工具执行时间；跨多次模型请求及自动恢复累计，任务结束后冻结，下次任务重新计量。`Σout` 是同一范围的累计输出，不叠加子任务。优先采用服务端 output 用量；缺失时估算实际收到的片段，早期用量之后的新片段也暂作估算，最终用量到达后校正。含估算时显示 `≈`，未知显示 `—`。
- 曲线：主会话实际接收的正文、可见推理和工具参数片段的近 60 秒变化；`recent` 为近 3 秒平均 TPS。`≈` 表示按片段字符估算，不包含服务端未传回的隐藏思考。采样独立于正文显示缓冲，完成时的整段回填不会制造尖峰；当前请求没有真实片段时显示 `—`。有采样的请求结束后 recent TPS 为 0。
- 读数：当前会话及同工作区内可证明归属的子任务的服务端输入、输出、缓存命中 token 和命中率。缺失字段显示 `—`，部分记录显示覆盖数。
- `Tools in`：工具定义、参数及结果回传进入模型输入的估算量，已包含于 input；历史结果被反复携带时会重复占用输入。工具汇总不因原子明细保留上限而丢失；来源缺失时仍标未知或部分覆盖。`main calls` 是当前主轮次的工具调用次数。

侧栏可见时约每秒刷新一次账本；收起或终端不足 65 列时停止轮询。空闲连续输入期间延后刷新以保护输入法，模型工作时继续更新。账本可跨重启读取，曲线只保留当前进程的采样；切换会话会清除旧会话的实时状态。重复轮询和相同内容的 Todo 重播不会抢占上半区域。

模型返回损坏的工具协议或参数时，Paw 保留已完成的工作，在后台要求模型重新生成尚未执行的工具调用，并记录失败、重试和恢复结果。同一次连续故障最多重试 10 次，成功响应后清零；普通工具执行失败会作为错误结果回给模型，由模型修正请求继续。取消和权限拒绝仍然有效，已完成的调用不会由恢复机制重新执行。

快捷键:
- 模型工作时，`Enter` 会把纯文本作为 steering 指令送入当前 turn 的下一次模型请求，不会插入已经建立的 token stream；`Tab` 会把输入排入后续独立 turn。若当前 turn 已封口或 runner 不支持 steering，`Enter` 会安全降级为 queue。
- 模型工作时，`ctrl+c` 仍用于取消当前工具/turn。带图片的输入按 `Enter` 时不会尝试 steering，而会保留附件并进入 queue。
- `ctrl+v`: 从剪贴板粘贴图片时插入 `[Image N]` 图片芯片；连续粘贴会按顺序生成多个芯片，芯片可以像一个整体一样删除。剪贴板没有图片时保持原有文本粘贴行为。
- `ctrl+o`: 展开/折叠模型 thinking 过程；折叠时 thinking 仍保存在 transcript 中，但不渲染到 viewport。
- `ctrl+g`: 展开/收起默认常驻的 Activity 侧栏；草稿、补全和输入焦点保留。80 列终端仍分栏，更窄至不足 65 列时自动隐藏侧栏、恢复宽度后再次显示。输入始终提交到主 session。
- 侧栏不拦截 `Tab`、方向键、`f/h/j/k/l` 或 `Ctrl+W`，这些键继续用于输入和原有编辑操作。Esc 从已有 task preview 返回主 transcript。

鼠标选择:
- 在消息历史区按住左键拖拽即可选择文本；拖到面板顶部/底部会自动滚动；释放时把选中内容写入系统剪贴板（本地剪贴板 + OSC 52 终端剪贴板双写，SSH/远程会话同样可用），并在状态栏短暂提示「已复制 N 字符」。
- **双击**选中一个词（中文按词/标点断开，不切开 emoji 与宽字符），**三击**选中整行；双击/三击后继续拖拽会按词/行边界扩展选区。
- 单击不复制、不创建选区：链接、todo 和工具行等可点击位置在双击判定窗口（400ms，对齐 macOS/Ghostty 系统双击节奏）结束后触发动作，避免「单击已生效、双击又选中」互相冲突。
- 按下与抬起相差 1 格以内的抖动/漂移仍按单击处理（不复制、不打断双击计数），避免真实鼠标的 1px 抖动把单击误判成拖拽、把双击判成两次单击。
- 16 色及以下终端自动把选区降级为反色渲染，与终端原生选区观感一致。
- **翻译选中词**（`/setting translate on` 动态开启，或 `/setting` 向导里选 `ui.translate_on_double_click=on` 持久化）：双击英文词弹出面板显示 IPA 音标、词性与中文释义；双击中文词显示英文翻译。翻译通过一次独立的单会话请求完成（不进 transcript、不占上下文），应用层用正则自动判定中英文方向；Esc 关闭面板，选区保留。

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
- 图片会保存到全局项目目录 `~/.paw/projects/<id>/attachments/`（`PAW_CONFIG_HOME` 可替换 `~/.paw`），使用内容哈希去重并以 `0600` 权限保存；会话 JSONL 只记录相对附件引用，不写入 base64 图片。
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
    "context_limit_tokens": 0,
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

上下文上限统一按以下优先级解析：模型专属 `contextWindow` > `/config` 通用正整数 `ui.context_limit_tokens` > 内置模型元数据 > `131072` tokens。`0` 表示自动，不会禁用压缩；例如通用值填 `270000` 即 270k，但模型已有专属窗口时仍优先使用专属值。通用值和模型窗口都拒绝负数。旧文件中保存的正数（包括 `1048576`）继续视为显式通用值，不会擅自改为自动；希望恢复元数据请手动设为 `0`。

通用设置成功保存后，同一进程的用量显示、状态信息和后续压缩检查使用同一上限。自动维护仍在新 turn 开始时检查；正在执行的工具轮次不会因改小上限而立即压缩，后续请求保留原有服务端溢出恢复机制；新模型切换卡记录切换时的上限，旧卡保留历史快照。启动、无界面运行和新建 worker 同样使用此规则。已运行的独立 worker 不订阅通用设置变化，下一次建立任务运行时才重新加载；修改客户端窗口不会扩展服务端模型的真实容量。

上下文维护按 50%/60%/80%/90% 压力阈值依次提示、裁剪旧大型 Tool Result、执行摘要压缩并在高压时强制压缩。被重写的原始消息会先归档到 `~/.paw/projects/<id>/sessions/<session-id>/compactions/`（`PAW_CONFIG_HOME` 可替换 `~/.paw`）；session journal（`transcript.jsonl`）仍保留完整 Tool Result，归档只服务于压缩投影、去重和恢复。

内置 True Color 主题 ID：`default`、`tokyo-night`、`tokyo-night-storm`、`tokyo-night-light`、`catppuccin-mocha`、`dracula`、`gruvbox-dark`。首版不支持自定义主题文件、256 色降级或 `NO_COLOR`。

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
go run ./cmd/paw -p "hello"
```

### REPL 模式

```bash
go run ./cmd/paw
```

### Subagent 工作模式（由主进程自动调用，一般不需要手动执行）

```bash
go run ./cmd/paw -task-worker
```

该模式需要父进程先发送 `worker.start`；随后 worker 可以发送 `mcp.call`，父进程回传对应的 `mcp.result`，最后以 `worker.result` 结束本轮任务。
