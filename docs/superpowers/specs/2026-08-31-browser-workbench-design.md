# Paw 浏览器工作台设计规格

日期：2026-08-31

状态：已完成设计与自检，待用户书面审查

## 1. 背景与目标

Paw 当前以 Bubble Tea TUI 为主要交互入口。模型循环、会话存储、任务、MCP、配置与 Actor 运行时已经位于独立包中，但运行时组装和部分交互控制仍集中在 `cmd/agent` 与 Bubble model。

本项目新增一个以浏览器对话为主的现代 Web 工作台，同时保留现有 TUI。第一版面向本机单用户，视觉以 DeepSeek Harness 为主要参考：左侧工作区/会话导航、宽对话画布、对话/轨迹双页签、底部悬浮输入框。Paw 特有的 reasoning、工具、任务、权限与问题交互集中在轨迹页和待处理状态中。

### 1.1 第一版目标

- 新增 `paw serve --open` 浏览器入口，现有 TUI 继续作为并列入口。
- 支持最近工作区列表和受控绝对路径打开。
- 最多同时加载两个工作区运行时；每个工作区最多一个前台模型回合，两个工作区可并行运行。
- 完成核心聊天闭环：会话列表、新建、恢复、分叉、发送、流式输出、steer、queue、cancel、question 与 permission。
- 对话页以用户消息和最终回答为主；轨迹页展示 reasoning、tool、task、permission、question 与 turn 状态。
- 在 Go 服务进程持续运行的前提下，浏览器刷新或 SSE 断线后恢复当前快照并补发事件。
- Go 服务进程重启后恢复已持久化会话；未完成 turn 标记为 `interrupted`，未决 question/permission 标记为 `expired`。
- 生产环境仍发布单一 Go 二进制；开发环境使用 Vite dev server 代理 Go API。

### 1.2 可验收成功标准

1. 浏览器可独立完成新建/恢复会话、发送、流式响应、steer、queue、cancel、question 和 permission，不依赖 TUI 操作。
2. 对话页、轨迹页、运行中、question、permission 和断线恢复六个状态均有 1440×900 与 1920×1080 截图证据；布局符合本规格原型，且不复制 DeepSeek 品牌标识。
3. SSE 在服务未重启、合并后事件流量不超过 128 KiB/s 时，断线 60 秒可直接补发；超出 replay 窗口时客户端通过一次快照重载恢复一致状态。
4. 500-turn 测试会话只加载最近 30 个 turn，首屏不下载完整历史；历史可分页读取。
5. 同一规范化工作区的第二个顶层 Paw 控制进程无法获得写控制权，必须明确失败，不能并发控制同一 store。
6. `go test ./...`、前端 typecheck、lint、unit test、production build 和浏览器端到端测试全部通过。

## 2. 第一版非目标

第一版不包含：

- 多用户、租户、远程账号、非回环监听或云服务部署。
- 同一工作区内多个前台会话同时运行模型回合。
- TUI 与浏览器同时控制同一个工作区；第二个顶层控制进程只会收到锁冲突错误，不提供只读降级。
- 手机端完整适配；目标为 1280px 以上桌面浏览器，窄屏只保证基本可读。
- 完整复刻 TUI 配置中心、Plan/Goal 管理、全部快捷键和所有 Activity 操作。
- 在浏览器中切换模型、修改权限模式或编辑配置；Composer 仅显示当前模型和模式。
- 图片/文件上传、任意本机文件引用和外部图片自动加载。
- Go 服务进程重启后继续未完成 turn、question 或 permission。
- 引入数据库替代当前 JSONL 会话存储。
- 超过两个已加载工作区运行时，或后台 daemon 式无限工作区驻留。

## 3. 已选方案与备选方案

### 3.1 采用：应用服务层 + Web Adapter

将 `cmd/agent/bootstrap.go` 中可复用的运行时组装提取到 `internal/app`，再实现 `internal/web`。TUI 与 Web 共享应用服务、工作区控制锁和核心运行时，但各自维护展示状态。

优点：

- HTTP handler 不直接操作 Bubble model。
- TUI 与 Web 复用同一套 workspace、session、turn、interaction 和 task 语义。
- 多工作区所需的 root 参数化、资源预算和跨进程控制权有明确归属。

成本：

- 第一阶段必须先完成有针对性的组合根重构。
- 必须定义稳定 DTO、事件游标与命令契约。

### 3.2 未采用：直接 Web Adapter

在 `cmd/agent` 内直接启动 HTTP Server 并复用当前 runner。上线更快，但会复制生命周期和控制逻辑，使 Web API 与 CLI/TUI 内部耦合。

### 3.3 未采用：常驻多工作区 Daemon

独立 daemon 可长期管理大量工作区和客户端，但会引入进程发现、升级、无限资源驻留和多客户端仲裁。第一版只在单个 `paw serve` 进程内最多加载两个工作区运行时。

## 4. 总体架构

```text
Production
┌──────────────────── React SPA ────────────────────┐
│ 工作区/会话树 · 对话 · 轨迹 · Composer · 交互层 │
└──────────────── REST + SSE ───────────────────────┘
                         │
┌──────────────── internal/web ─────────────────────┐
│ HTTP 路由 · DTO · 认证 · SSE · go:embed 静态资源 │
└──────────────── application API ──────────────────┘
                         │
┌──────────────── internal/app ─────────────────────┐
│ Supervisor · WorkspaceRuntime · Coordinator       │
│ SessionService · TurnService · InteractionService │
│ EventHub · ControllerLease · ResourceGovernor     │
└──────────────── existing core ────────────────────┘
                         │
  loop · sessionactor · session · task · mcp · config · settings

Development
Vite dev server ── /api proxy ── paw serve --no-static
```

## 5. CLI、目录与构建

### 5.1 CLI 兼容

现有入口使用 legacy flags，并有 task worker 隐藏参数。实现必须在 legacy `flag.Parse()` 前识别 `serve` 子命令：

```text
paw                                  # 现有 TUI
paw [现有 flags]                     # 行为不变
paw serve [--listen 127.0.0.1:0] [--open]
paw serve --no-static --listen 127.0.0.1:8765
```

- `serve` 使用独立 `flag.FlagSet`。
- worker 隐藏 flags 和进程池启动协议保持不变。
- 第一版 `--listen` 只接受 loopback host；非回环地址直接拒绝。
- `scripts/dev-web.sh` 固定 Go API 端口 8765，并把该地址注入 Vite proxy；生产使用端口 0 自动选择空闲端口。

### 5.2 前端固定目录

```text
internal/web/
├── assets.go
├── server.go
├── handlers_*.go
└── ui/
    ├── src/
    │   ├── api/
    │   ├── app/
    │   ├── features/workspaces/
    │   ├── features/sessions/
    │   ├── features/conversation/
    │   ├── features/trace/
    │   ├── features/interactions/
    │   └── components/
    ├── dist/
    ├── package.json
    ├── tsconfig.json
    └── vite.config.ts
```

`internal/web/assets.go` 使用：

```go
//go:embed ui/dist
var assets embed.FS
```

`ui/dist` 提交到仓库，使 clean checkout 在没有 Node 时仍能运行 `go test ./...` 和 `go build ./...`。唯一前端构建入口为 `scripts/build-web.sh`：执行锁定依赖安装、typecheck、lint、test、Vite build，并验证生成的 `ui/dist` 无未提交差异。静态 handler 只暴露 `/`、`/index.html` 和 `/assets/*`；`/api/*` 永不进入 SPA fallback。入口 HTML 使用 `no-store`，哈希资源使用 immutable cache。

## 6. 后端组件边界

### 6.1 `internal/app.Supervisor`

职责：

- 管理最近工作区元数据与最多两个已加载 `WorkspaceRuntime`。
- 校验规范化路径并生成稳定 workspace ID。
- 获取顶层控制进程的跨进程 `ControllerLease`。
- 为所有 runtime 提供共享 `ResourceGovernor`。
- 打开第三个工作区时，关闭最久未使用且完全空闲的 runtime；若两个 runtime 均不可关闭，返回 `runtime_capacity`。

runtime 在以下任一条件存在时不可淘汰：前台 turn、后台 task、pending interaction、未处理 queue、活跃写命令。普通浏览器 SSE 订阅本身不阻止淘汰；淘汰前发布 `workspace.closing`，随后关闭该工作区 SSE。

### 6.2 显式 root 参数化

```go
type WorkspaceRuntimeOptions struct {
    Root             string
    Output           ui.UI
    ResourceGovernor *ResourceGovernor
    ControllerLease  *ControllerLease
}
```

实现不得使用 `os.Chdir`。以下依赖全部接受显式 root/path：config、session store、settings、skills、task launcher、plan、goal、tool registry 与 workspace metadata。`paw serve` 启动后不得把新工作区 `.env` 写入全局进程环境；每个 runtime 只使用显式配置和启动时继承的环境快照。

### 6.3 `internal/app.WorkspaceRuntime`

从现有 `buildRunnerWithTaskContext` 提取可复用组合单元，拥有一个工作区的：

- Config Manager / Controller
- Settings Controller
- JSONL Session Store
- Model Client
- SessionActor Host / Loop Engine
- Task Manager / Process Pool
- MCP Manager / Broker
- Tool Registry
- Workspace Coordinator 与 EventHub

`Close()` 关闭顺序与当前 `appContext.Close()` 保持一致：停止接收新命令，停止 MCP，关闭 task，关闭 runner，关闭 config controller，最后关闭 EventHub/SSE。每阶段有 5 秒上限；方法并发安全、幂等，并返回聚合错误。

### 6.4 `internal/app.WorkspaceCoordinator`

每个 workspace 使用单一串行协调器管理：

- 当前 active session / active turn
- session 与 turn 版本
- 流式 part 投影
- queue
- pending interaction
- EventHub 提交和 snapshot 水位

核心状态变化遵循：

1. 需要持久化的事实先写 journal/store。
2. 在协调器临界区更新应用投影。
3. 同一临界区递增实体版本和事件序号并发布事件。
4. `SessionSnapshot()` 在同一临界区复制投影与事件水位。

流式 delta 不承诺进程重启恢复，但其投影与事件序号仍在同一临界区更新，因此浏览器快照与 SSE 不会出现读取状态和读取水位之间的丢事件窗口。

### 6.5 Session 操作不得隐式切换 Engine

- session 列表和历史 snapshot 直接从 store/projector 读取，不激活 Engine。
- 创建 session 只写入新 session metadata，不改变当前 active session。
- 启动 turn 时，协调器在无 active turn 的前提下把 Host 切换到目标 session，并将 `session_id + turn_id` 固定到终态。
- 浏览器导航不会改变 Host 当前 session。
- fork 从指定 session 的最后持久化 journal sequence 创建；运行中未持久化 delta 不进入 fork。fork 不自动激活新 session。
- steer、queue、cancel 必须携带并验证 `active_turn_id`。

### 6.6 `ResourceGovernor`

- 最多两个 loaded runtime。
- 全局 task worker slot 上限等于当前生效 sandbox `MaxWorkers`；各 runtime launcher 启动 worker 前必须获取共享 slot。
- 每个 workspace 最多一个前台 turn；两个 workspace 最多并行两个前台 turn。
- MCP 进程只随 loaded runtime 存在，runtime 淘汰时关闭。
- 资源不足返回 `resource_capacity`，不得静默突破全局上限。

## 7. 跨进程控制权

顶层 TUI 与 Web runtime 使用同一 `ControllerLease`：

- 锁文件位于对应 project store 的 `controller.lock`。
- Darwin/Linux 使用 advisory `flock`，Windows 使用 `LockFileEx`；不支持可靠 OS 锁的平台 fail closed。
- 锁由顶层 Paw 控制进程持有到退出。元数据记录 PID、随机 `instance_id`、模式和启动时间，仅用于诊断；真正所有权由 OS 锁决定，异常退出自动释放。
- task worker 不重复获取顶层 lease；父进程通过受控环境变量传入 `instance_id`，worker 仍依赖现有 per-stream / file mutation 锁完成细粒度写协调。
- 第二个 `paw` 或 `paw serve` 打开相同 canonical workspace 时返回 `workspace_locked`，包含 owner PID 和模式。
- 集成测试必须启动两个真实进程验证互斥和异常退出释放。

## 8. 工作区路径模型

路径规范化算法固定为：

1. 输入必须为绝对路径。
2. 执行 `filepath.Clean` 和 `filepath.Abs`。
3. 必须成功执行 `filepath.EvalSymlinks`；失败则拒绝打开，不回退到未解析路径。
4. 对解析后路径重新 `Stat` 并确认是目录。
5. Windows 额外规范化卷标和大小写比较。
6. workspace ID 为规范化路径字节的 SHA-256 派生标识。

最近工作区只保存 canonical path、显示名和最后打开时间。打开工作区不等于授予工作区外访问权限；Read/Write/Bash 继续执行每次 root containment、yolo 和 permission 检查，不能只信任打开时校验。

关闭 runtime 与忘记最近记录是两个操作：

- `POST /api/workspaces/{id}/close`：关闭空闲 runtime；忙时返回 `workspace_busy`。
- `DELETE /api/recent-workspaces/{id}`：仅删除最近记录；若 runtime 已加载，不隐式关闭。

## 9. 应用服务

```go
type WorkspaceService interface {
    ListRecent(context.Context) ([]WorkspaceSummary, error)
    Open(context.Context, OpenWorkspaceCommand) (WorkspaceSnapshot, error)
    Close(context.Context, WorkspaceID) error
    ForgetRecent(context.Context, WorkspaceID) error
}

type SessionService interface {
    List(context.Context, WorkspaceID, PageRequest) (SessionPage, error)
    Snapshot(context.Context, WorkspaceID, SessionID, SnapshotRequest) (SessionSnapshot, error)
    Create(context.Context, CreateSessionCommand) (CommandReceipt, error)
    Fork(context.Context, ForkSessionCommand) (CommandReceipt, error)
}

type TurnService interface {
    Submit(context.Context, SubmitCommand) (CommandReceipt, error)
    Steer(context.Context, SteerCommand) (CommandReceipt, error)
    Queue(context.Context, QueueCommand) (CommandReceipt, error)
    Cancel(context.Context, CancelCommand) (CommandReceipt, error)
}

type InteractionService interface {
    AnswerQuestion(context.Context, AnswerQuestionCommand) (CommandReceipt, error)
    DecidePermission(context.Context, DecidePermissionCommand) (CommandReceipt, error)
}
```

HTTP handler 只能调用这些服务和 DTO mapper，不得访问 Bubble model、未导出的 Host 状态或内部 map。

## 10. 认证与 HTTP 安全

### 10.1 一次性 bootstrap token

1. `paw serve --open` 生成一次性高熵 bootstrap token，并在 URL fragment 中打开 SPA：`/#bootstrap=<token>`。Fragment 不发送给 HTTP Server。
2. SPA 读取 fragment 后调用 `POST /api/auth/exchange`，token 位于 JSON body。
3. 后端恒定时间比较 token，成功后立即使其失效并设置随机 session cookie。
4. Cookie 属性为 `HttpOnly; SameSite=Strict; Path=/`。第一版为 loopback HTTP，因此不设置会导致本地 HTTP 失效的 `Secure`；未来 HTTPS 模式必须设置。
5. SPA 使用 `history.replaceState` 清除 fragment。
6. 所有 API、SSE、export 和 detail 请求都要求 session cookie。

未使用 `--open` 时，CLI 在终端打印带 fragment 的一次性 URL。服务日志禁止记录 token、cookie 和完整查询参数。

### 10.2 请求硬化

- 只接受预期 loopback `Host`，拒绝 DNS rebinding 形式的其他 Host。
- 不启用 CORS。
- 所有写请求检查 exact Origin 和 `Sec-Fetch-Site: same-origin`。
- JSON 使用 `http.MaxBytesReader`、严格未知字段检查和固定 Content-Type。
- 默认 JSON body 上限 1 MiB；message text 上限 256 KiB。
- 普通 HTTP 设置 header/read/write/idle timeout；SSE 独立使用 15 秒 heartbeat。
- 所有敏感响应设置 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`。
- CSP 至少包含：`default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; img-src 'self' data:`。

## 11. REST API 与命令语义

### 11.1 端点

```text
POST   /api/auth/exchange
GET    /api/bootstrap

GET    /api/recent-workspaces
DELETE /api/recent-workspaces/{workspace_id}
POST   /api/workspaces/open
POST   /api/workspaces/{workspace_id}/close

GET    /api/workspaces/{workspace_id}/sessions?cursor=&limit=
POST   /api/workspaces/{workspace_id}/sessions
GET    /api/workspaces/{workspace_id}/sessions/{session_id}?before=&limit=
POST   /api/workspaces/{workspace_id}/sessions/{session_id}/fork

POST   /api/workspaces/{workspace_id}/sessions/{session_id}/messages
POST   /api/workspaces/{workspace_id}/sessions/{session_id}/steer
POST   /api/workspaces/{workspace_id}/sessions/{session_id}/queue
POST   /api/workspaces/{workspace_id}/sessions/{session_id}/cancel

POST   /api/workspaces/{workspace_id}/sessions/{session_id}/interactions/{request_id}/answer
POST   /api/workspaces/{workspace_id}/sessions/{session_id}/interactions/{request_id}/decision

GET    /api/workspaces/{workspace_id}/events?after={stream_id}:{sequence}
GET    /api/workspaces/{workspace_id}/trace/{event_id}
GET    /api/workspaces/{workspace_id}/sessions/{session_id}/export
```

第一版不提供 session 删除、附件上传、模型切换或权限模式修改端点，因此 UI 中对应元素只读或不显示。

### 11.2 命令与 receipt

创建 session、fork、submit、steer 和 queue 携带 `command_id`。Cancel 以目标终态天然幂等；interaction 以 request 状态天然幂等。

```json
{
  "command_id": "uuid",
  "session_version": 12,
  "active_turn_id": "turn_...",
  "payload": {}
}
```

统一成功响应：

```json
{
  "command_id": "uuid",
  "status": "accepted",
  "resource_id": "turn_...",
  "session_version": 13
}
```

规则：

- create/fork/message 的资源 ID 由 `command_id` 确定性派生或通过 journal 中的 command receipt 持久映射。
- 同一 `command_id` 重试查询既有 receipt，不重复创建资源。
- steer/queue receipt 与输入事实一同写入 journal。
- cancel 对已终止 turn 返回原终态。
- interaction 已解决后重试返回原决定。
- `session_version` 只保护 session 生命周期和队列修改，不随每个流式 delta 递增。
- steer/queue/cancel 额外验证 `active_turn_id`；不匹配返回 `409 active_turn_changed`。
- 向已有前台 turn 的同工作区其他 session 提交消息返回 `409 workspace_busy` 和占用 session/turn。

## 12. 事件协议

### 12.1 SSE 游标与信封

每个 EventHub 创建随机 `stream_id`。进程重启或 runtime 重建后 stream ID 改变，sequence 从 1 开始。

```json
{
  "schema_version": 1,
  "stream_id": "stream_...",
  "sequence": 184,
  "workspace_id": "ws_...",
  "session_id": "session_...",
  "turn_id": "turn_...",
  "type": "tool.completed",
  "time": "2026-08-31T15:48:02.123Z",
  "entity_version": 4,
  "payload": {}
}
```

SSE 帧必须同时写入：

```text
id: {stream_id}:{sequence}
event: {type}
data: {JSON envelope}
```

游标比较只使用 `(stream_id, sequence)`；`entity_version` 只防止旧实体投影覆盖新投影。流式 delta 不使用 session version 做去重。

### 12.2 第一版事件 payload

| Event | 必需 payload 字段 |
|---|---|
| `workspace.updated` | `status`, `display_name`, `active_session_id?`, `active_turn_id?` |
| `workspace.closing` | `reason` |
| `session.created` | `session_id`, `title`, `created_at`, `session_version` |
| `session.updated` | `session_id`, `title`, `updated_at`, `session_version` |
| `turn.started` | `turn_id`, `session_id`, `started_at` |
| `turn.completed` | `turn_id`, `finished_at`, `input_tokens`, `output_tokens` |
| `turn.failed` | `turn_id`, `finished_at`, `error_code`, `message` |
| `turn.cancelled` | `turn_id`, `finished_at`, `reason` |
| `turn.interrupted` | `turn_id`, `detected_at`, `reason` |
| `user.message` | `message_id`, `command_id`, `text` |
| `assistant.part.started` | `part_id`, `part_index`, `kind` |
| `assistant.delta` | `part_id`, `offset`, `text` |
| `assistant.part.completed` | `part_id`, `final_length` |
| `reasoning.started` | `part_id`, `part_index`, `redacted` |
| `reasoning.delta` | `part_id`, `offset`, `text` |
| `reasoning.completed` | `part_id`, `final_length` |
| `tool.started` | `tool_use_id`, `name`, `target?`, `args_summary`, `started_at` |
| `tool.completed` | `tool_use_id`, `name`, `result_summary`, `detail_id?`, `finished_at`, `duration_ms` |
| `tool.failed` | `tool_use_id`, `name`, `error_code`, `message`, `detail_id?`, `finished_at` |
| `task.updated` | `task_id`, `name`, `status`, `agent_name?`, `summary` |
| `question.requested` | `request_id`, `prompt`, `mode`, `options`, `created_at` |
| `question.resolved` | `request_id`, `answer`, `resolved_at` |
| `permission.requested` | `request_id`, `operation`, `canonical_target`, `created_at` |
| `permission.resolved` | `request_id`, `decision`, `resolved_at` |
| `interaction.expired` | `request_id`, `kind`, `expired_at`, `reason` |
| `queue.updated` | `items`, `session_version` |
| `system.message` | `level`, `code`, `title`, `body` |
| `event.reset_required` | `reason`, `current_stream_id`, `latest_sequence` |

未知 `schema_version` 是协议错误并触发升级提示；同版本未知 event type 记录诊断后忽略，不能破坏后续事件。

### 12.3 Delta 合并与尺寸

- 后端将 assistant/reasoning delta 按 25ms 或 16 KiB（任一先到）合并。
- Delta 携带稳定 `part_id` 和 UTF-8 字节 `offset`。
- offset 小于客户端当前长度表示重复并忽略；大于当前长度表示缺口并触发 snapshot reset。
- 普通事件 payload 上限 64 KiB。完整 tool result、错误原文和 diff 不进入 SSE，只返回摘要和 `detail_id`。
- `GET trace/{event_id}` 响应上限 2 MiB，超过时截断并返回 `truncated: true`。

## 13. 快照与 SSE 恢复

### 13.1 一致快照

`SessionSnapshot()` 在 WorkspaceCoordinator 同一临界区复制：

- `stream_id`
- `event_sequence`
- `session_version`
- 最近 30 个 turn 的对话投影
- 当前 active turn 和已累积流式 part
- pending question/permission
- queue
- task 摘要
- 更早历史的分页 cursor

单个 snapshot JSON 上限 2 MiB；达到上限时减少历史 turn 数，但不能截断当前 turn 或 pending interaction。

### 13.2 原子 subscribe

`EventHub.Subscribe(streamID, after)` 在同一把锁下：

1. 验证 stream ID、游标是否超前、是否早于 ring。
2. 注册有界 live subscriber。
3. 复制 `after` 之后的 replay 事件。
4. 释放锁，然后先发送 replay，再消费 subscriber queue。

这样事件无法落在“复制 ring”和“注册 subscriber”之间。

- Ring 上限同时为 10,000 个事件、16 MiB、至少保留最近 120 秒，任一资源上限先到时淘汰最旧事件。
- 每个 subscriber queue 上限 1 MiB 或 1,000 个事件。
- 慢消费者队列溢出时不静默丢事件；发送 `event.reset_required` 后关闭连接。
- stream ID 不匹配、游标早于 ring、游标超前都返回 reset。
- 原生 EventSource 首次连接使用 `?after=stream:sequence`；同一实例自动重连时浏览器使用服务端输出的 `id:` 维护 `Last-Event-ID`。

### 13.3 进程重启

EventHub ring 不是事实来源。服务重启时：

- 从 journal/store 重建已持久化对话和 task 终态。
- 生成新 stream ID，旧游标必然 reset。
- 上次没有持久终态的 turn 标记为 `interrupted`。
- pending question/permission 标记为 `expired`，不尝试继续已消失的等待 goroutine。
- 用户可从恢复后的会话重新发送或继续新回合。

## 14. 前端结构与状态

使用 React、TypeScript 与 Vite。

- 服务器状态：由轻量 query cache、session snapshot 和 event reducer 管理。
- 本地展示状态：选中 tab、侧栏折叠、轨迹详情选中、输入草稿。
- 输入草稿按 `workspace_id/session_id` 存储在 `localStorage`，发送成功后清理。
- 当前模型和权限模式来自 bootstrap/workspace snapshot，只读显示。
- 第一版不显示附件按钮，避免形成不存在的上传能力。

### 14.1 主布局

- 左侧固定工作区/会话树，支持搜索、最近工作区、输入绝对路径打开和新建会话。
- 顶部显示会话标题、工作区/分支、运行状态和 Session log 导出。
- 主区为“对话 / 轨迹”双页签。
- 对话页使用宽阅读画布；用户消息为右侧浅色气泡，助手回答为无外框正文。
- 底部悬浮 Composer 显示当前模式、模型、运行/停止状态和发送按钮。
- 桌面优先；侧栏宽 280–320px，正文最佳阅读宽度 760–920px。

### 14.2 对话页

每个 turn 显示用户消息、最终助手内容和一行过程摘要。过程摘要包含工具数、任务数、耗时和失败数；点击后切换到轨迹页并定位该 turn。

Reasoning 原文和完整工具结果不默认内联。Markdown 禁止 raw HTML并经过严格 sanitizer；链接只允许 `http`、`https` 和 `mailto`。代码、表格、引用和 KaTeX 数学公式可渲染；外部图片不自动加载。工具输入、结果、错误和 diff 默认按纯文本显示。

### 14.3 轨迹页

- 按 turn 分组的纵向事件时间线。
- Reasoning、Tool、Task、Question、Permission、Answer 使用稳定图标与状态色。
- 默认只显示摘要；点击事件后通过 detail API 在右侧查看参数、结果、错误和文件 diff。
- pending question/permission 同时出现在时间线和页顶待处理 banner。
- 展示结构由 AppEvent DTO 构造，不复用 Bubble 的行号、hover 或 WorkSegment 投影。

### 14.4 Composer 状态

- 空闲：Enter 发送新回合，Shift+Enter 换行。
- 当前会话运行中：主按钮变为 Stop；Enter 默认 steer，可显式切换 queue。
- 其他会话占用该工作区：Composer 禁用并提供跳转到占用会话的按钮。
- pending question/permission 不清空或取代消息草稿。
- 当前模型/权限模式是只读标签；修改配置继续通过 TUI 或配置文件完成。

## 15. 错误处理

标准错误信封：

```json
{
  "error": {
    "code": "workspace_busy",
    "message": "另一个会话正在该工作区运行",
    "retryable": false,
    "details": {
      "active_session_id": "session_...",
      "active_turn_id": "turn_..."
    }
  }
}
```

规则：

- 4xx 表示校验、冲突、权限或不存在；5xx 表示服务内部故障。
- 前端只依据稳定 `code` 决定行为，不解析自然语言。
- 可恢复网络错误显示非阻塞状态并指数退避重连。
- 命令结果不确定时使用原 `command_id` 重试。
- Workspace 路径失效时标记 unavailable，禁止新 turn，并允许忘记最近记录。
- Config 不完整时工作区进入 diagnostics 状态；第一版只显示错误和配置文件路径，不提供 Web 配置编辑。
- Runtime 初始化失败只影响目标工作区。
- question/permission 过期时关闭交互层并重新获取快照。

## 16. 测试与验证

### 16.1 Go 单元测试

- root 参数化：所有 runtime 构造器不依赖 `os.Chdir` 或调用时 cwd。
- ControllerLease：跨进程互斥、异常退出释放、worker 授权路径。
- Supervisor：两个 runtime 上限、空闲淘汰、busy 不淘汰、共享 worker slot。
- Coordinator：单工作区一个 turn、session 导航不切换 Host、fork 持久序号、active turn 校验。
- 命令：确定性 resource ID、receipt 重放、cancel 和 interaction 幂等。
- EventHub：单调 sequence、stream epoch、原子 subscribe、ring reset、慢消费者 reset。
- Snapshot：状态与水位一致；事件发生在 snapshot 临界区前后都不丢失。
- Web：auth exchange、cookie、Host、Origin、body limit、错误信封、SSE id/replay/reset。

### 16.2 前端测试

- Reducer：snapshot 水位、stream reset、事件去重、delta offset、gap reset、未知事件。
- Component：工作区树、会话分页、对话流、轨迹详情、Composer 四种状态、question/permission。
- 内容安全：raw HTML、危险 URI、外部图片、超大 Markdown 和 tool text。
- Accessibility：完整键盘路径、焦点圈、dialog 焦点锁、按钮名称和 live status。

### 16.3 集成与端到端测试

1. 打开最近工作区并创建会话。
2. 提交消息并观察 assistant/reasoning/tool 流式事件。
3. 运行中 steer、queue 和 cancel。
4. 回答 question 与 permission。
5. 刷新页面并恢复 pending 状态。
6. 断开 SSE 60 秒后直接补发；伪造过旧或错误 stream 游标后 reset。
7. 第二工作区并行运行；第三 runtime 验证空闲淘汰或 `runtime_capacity`。
8. 同工作区第二会话验证 `workspace_busy`。
9. 两个真实 Paw 顶层进程争抢同一工作区，验证 `workspace_locked`。
10. 服务重启后验证旧 stream reset、未完成 turn interrupted、interaction expired。

### 16.4 性能、回归与视觉证据

- 500-turn fixture 首屏只请求最近 30 turn；旧历史分页加载。
- 30-turn snapshot 不超过 2 MiB；trace detail 不超过 2 MiB。
- 流式 delta 按 25ms/16 KiB 合并，浏览器不按 token 触发 React 全树渲染。
- 所有 Go 改动通过 `go test ./...`；关键 Coordinator/EventHub/Web 包通过 `go test -race`。
- 前端通过 typecheck、lint、unit test 和 production build。
- Playwright 在 1440×900 和 1920×1080 捕获：对话、轨迹、question、permission、运行中、断线恢复。
- 稳定视觉参考提交到：
  - `docs/superpowers/specs/assets/2026-08-31-browser-workbench-conversation.html`
  - `docs/superpowers/specs/assets/2026-08-31-browser-workbench-trace.html`

## 17. 交付切片

1. 提取显式 root 的 `internal/app.WorkspaceRuntime` 与 ControllerLease，TUI 行为不变。
2. Supervisor、ResourceGovernor、Coordinator、store-only SessionService。
3. EventHub、快照、事件 DTO、命令 receipt 与确定性恢复测试。
4. 安全的只读 Web Server、auth exchange、嵌入式 React shell、工作区/会话/对话查看与 SSE。
5. submit、steer、queue、cancel 聊天闭环。
6. question、permission、轨迹 detail 与进程内刷新恢复。
7. 多工作区上限、跨进程互斥、进程重启终态、视觉打磨和完整验证。

每个切片必须独立验证并使用 Gitmoji 提交；不得把 TUI 重构、Web UI 和全部交互合并成不可验证的大提交。

## 18. 已批准决策摘要

- 部署：本机单用户，loopback only。
- UI 关系：Web 与 TUI 并列，但同工作区只允许一个顶层控制进程。
- 技术：React + TypeScript + Vite；生产 `go:embed`，开发 Vite proxy。
- 架构：应用服务层 + Web adapter。
- 视觉：以 DeepSeek Harness 为主的现代浏览器对话工作台。
- 导航：工作区/会话侧栏，对话/轨迹双页签。
- 工作区：最近列表 + 受控绝对路径；最多两个 runtime，每工作区一个前台 turn。
- 屏幕：桌面优先。
- 传输：REST 命令/快照 + cookie-authenticated SSE。
- 恢复：同进程刷新/断线可补发；进程重启后未完成状态明确 interrupted/expired。
- 第一版重点：核心闭环、视觉完成度、刷新恢复和数据完整性。
