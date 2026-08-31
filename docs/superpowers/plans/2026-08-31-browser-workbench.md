# Paw 浏览器工作台实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 在保留 Paw TUI 的同时，增加一个本机 loopback、单二进制发布、以浏览器对话为主的现代 Web 工作台。

**架构：** 先把当前 `cmd/agent` 组合根提取为显式 workspace root 的 `internal/app` 应用层，再在其上建立 WorkspaceCoordinator、EventHub、REST/SSE 和 React UI。TUI 与 Web 共享 runtime、跨进程控制锁、会话/turn/interaction 语义，但不共享 Bubble 展示状态；生产通过 `go:embed` 内嵌 React 产物，开发通过 Vite proxy。

**技术栈：** Go 1.25、现有 loop/sessionactor/session/task/MCP/config 包、`net/http`、SSE、React、TypeScript、Vite、Vitest、Testing Library、Playwright。

**规格：** `docs/superpowers/specs/2026-08-31-browser-workbench-design.md`

---

## 范围拆分与执行顺序

这是一个产品目标，但实现必须按以下七个可独立验证的阶段执行。每个阶段末尾都必须提交，且不得在同一提交中混入当前工作树里既有的 `internal/model`、`internal/message` 或 `memory/progress.md` 改动。

1. 显式 root runtime 与跨进程控制锁
2. Supervisor、资源预算、Coordinator 与 store-only session 查询
3. EventHub、快照、事件 DTO 和命令 receipt
4. 安全只读 Web Server 与嵌入式 React shell
5. submit / steer / queue / cancel 聊天闭环
6. question / permission / trace detail / 刷新恢复
7. 多工作区、进程重启语义、性能、视觉和完整验证

计划必须覆盖规格中的全部 API：`POST /api/auth/exchange`、`GET /api/bootstrap`、`GET /api/recent-workspaces`、`DELETE /api/recent-workspaces/{workspace_id}`、`POST /api/workspaces/open`、`POST /api/workspaces/{workspace_id}/close`、session list/create/snapshot/fork、`/messages`、`/steer`、`/queue`、`/cancel`、`/interactions/{request_id}/answer|decision`、`/events`、`/trace/{event_id}` 与 `/export`。

## 文件结构

### 应用层

- 创建 `internal/app/runtime.go`：`WorkspaceRuntime` 资源所有权与 `Close()`。
- 创建 `internal/app/runtime_builder.go`：显式 root 的 runtime 组合根。
- 创建 `internal/app/runtime_options.go`：`WorkspaceRuntimeOptions` 和依赖注入点。
- 创建 `internal/app/controller_lease.go` 及平台文件：顶层进程跨进程控制锁。
- 创建 `internal/app/workspace_path.go`：canonical path 与 workspace ID。
- 创建 `internal/app/supervisor.go`：最近工作区、最多两个 runtime、淘汰规则。
- 创建 `internal/app/resource_governor.go`：共享 worker slot 与 runtime 容量。
- 创建 `internal/app/coordinator.go`：workspace 串行状态提交边界。
- 创建 `internal/app/services.go`：Workspace/Session/Turn/Interaction 服务契约。
- 创建 `internal/app/session_projection.go`：store-only session list/snapshot/fork。
- 创建 `internal/app/event.go`：`AppEvent`、payload DTO 和 schema version。
- 创建 `internal/app/event_hub.go`：stream epoch、ring、原子 subscribe、慢消费者 reset。
- 创建 `internal/app/snapshot.go`：一致 session snapshot 与分页 cursor。
- 创建 `internal/app/command_receipt.go`：command ID、receipt 和确定性 resource ID。
- 创建 `internal/app/ui_adapter.go`：实现 `internal/ui.UI` 可选扩展并投影到 Coordinator/EventHub。
- 创建 `internal/app/toolset.go`：为每个 runtime 构建并持有独立的 built-in、todo、state、transcript、question 与 MCP 工具实例，消除 `cmd/agent` 的进程全局工具变量。

### 现有后端修改

- 修改 `cmd/agent/bootstrap.go`：委托 `internal/app` 构建 runtime，保留 task worker 配置入口。
- 修改 `cmd/agent/main.go`、`cmd/agent/options.go`：识别 `serve` 子命令且保持 legacy flags。
- 创建 `cmd/agent/serve.go`：serve FlagSet、Server 启停、`--open`。
- 修改 `cmd/agent/interactive.go`：TUI 获取同一 ControllerLease。
- 修改 `cmd/agent/worker.go`：继承顶层 instance ID，不重复获取 lease。
- 修改 `internal/session/jsonl_store.go`：新增显式 project/workspace root 构造器。
- 修改所有仍依赖 cwd 的 config/settings/skill/task 构造调用点，仅注入显式 root；禁止 `os.Chdir`。
- 修改 `internal/sessionactor/host.go`：补充 store-only 或固定 active turn 所需的窄接口，不让浏览器导航切换 Engine。
- 修改 question/permission broker 的 request ID 与状态投影，使其携带 workspace/session/turn/tool 归属并可在同进程刷新期间查询。

### Web 后端

- 创建 `internal/web/server.go`：loopback HTTP server、超时、关闭。
- 创建 `internal/web/assets.go`：`//go:embed ui/dist`。
- 创建 `internal/web/auth.go`：bootstrap token exchange 与 session cookie。
- 创建 `internal/web/middleware.go`：Host、Origin、Sec-Fetch-Site、CSP、no-store、body limit。
- 创建 `internal/web/errors.go`：稳定错误信封。
- 创建 `internal/web/handlers_bootstrap.go`。
- 创建 `internal/web/handlers_workspaces.go`。
- 创建 `internal/web/handlers_sessions.go`。
- 创建 `internal/web/handlers_turns.go`。
- 创建 `internal/web/handlers_interactions.go`。
- 创建 `internal/web/handlers_trace.go`。
- 创建 `internal/web/handlers_export.go`。
- 创建 `internal/web/sse.go`。
- 创建 `internal/web/static.go`：SPA fallback 与 `/api` 排除。

### React 前端

- 创建 `internal/web/ui/package.json`、lockfile、`tsconfig.json`、`vite.config.ts`、`eslint.config.js`、`playwright.config.ts`。
- 创建 `internal/web/ui/src/api/types.ts`：与 Go DTO 一致的类型。
- 创建 `internal/web/ui/src/api/client.ts`：REST client、错误信封。
- 创建 `internal/web/ui/src/api/eventStream.ts`：cookie EventSource、游标与 reset。
- 创建 `internal/web/ui/src/app/store.ts`、`reducer.ts`、`queries.ts`。
- 创建 workspace/session/conversation/trace/interactions feature 目录。
- 创建 `internal/web/ui/src/components/WorkbenchShell.tsx`、`Composer.tsx`、Markdown renderer。
- 创建 `internal/web/ui/src/styles/tokens.css`、`workbench.css`：DSH 风格但不复制品牌。
- 创建 `scripts/build-web.sh`、`scripts/dev-web.sh`。
- 提交 `internal/web/ui/dist`。

### 视觉与 E2E

- 创建 `internal/web/ui/e2e/workbench.spec.ts`。
- 创建 `internal/web/testdata/webfixture/`：可确定重放 assistant/reasoning/tool/question/permission 的 fixture server/runtime。
- 创建 `.agent/visual/browser-workbench-*.md` 与对应 PNG 证据。

---

## 阶段 1：显式 root runtime 与跨进程控制锁

### 任务 1：建立 workspace canonical path 契约

**文件：**
- 创建：`internal/app/workspace_path.go`
- 创建：`internal/app/workspace_path_test.go`

- [ ] **步骤 1：编写失败测试，固定 canonical path 和 ID**

```go
func TestCanonicalWorkspaceRejectsRelativeAndBrokenSymlink(t *testing.T) {
    _, err := CanonicalWorkspace("relative/path")
    if !errors.Is(err, ErrWorkspacePathNotAbsolute) {
        t.Fatalf("err = %v", err)
    }

    root := t.TempDir()
    broken := filepath.Join(root, "broken")
    if err := os.Symlink(filepath.Join(root, "missing"), broken); err != nil {
        t.Fatal(err)
    }
    _, err = CanonicalWorkspace(broken)
    if !errors.Is(err, ErrWorkspacePathUnresolvable) {
        t.Fatalf("err = %v", err)
    }
}

func TestCanonicalWorkspaceResolvesSymlinkAndStableID(t *testing.T) {
    root := t.TempDir()
    link := filepath.Join(t.TempDir(), "workspace")
    if err := os.Symlink(root, link); err != nil {
        t.Fatal(err)
    }
    direct, err := CanonicalWorkspace(root)
    if err != nil { t.Fatal(err) }
    viaLink, err := CanonicalWorkspace(link)
    if err != nil { t.Fatal(err) }
    if direct.Path != viaLink.Path || direct.ID != viaLink.ID {
        t.Fatalf("direct=%+v link=%+v", direct, viaLink)
    }
}
```

- [ ] **步骤 2：运行测试，确认因符号不存在而失败**

运行：`go test ./internal/app -run 'TestCanonicalWorkspace' -count=1`

预期：FAIL，`CanonicalWorkspace` 或错误常量未定义。

- [ ] **步骤 3：实现最小 path 规范化**

```go
type WorkspaceID string

type WorkspacePath struct {
    ID   WorkspaceID
    Path string
    Name string
}

func CanonicalWorkspace(input string) (WorkspacePath, error) {
    if !filepath.IsAbs(input) {
        return WorkspacePath{}, ErrWorkspacePathNotAbsolute
    }
    absolute, err := filepath.Abs(filepath.Clean(input))
    if err != nil { return WorkspacePath{}, err }
    resolved, err := filepath.EvalSymlinks(absolute)
    if err != nil { return WorkspacePath{}, fmt.Errorf("%w: %v", ErrWorkspacePathUnresolvable, err) }
    info, err := os.Stat(resolved)
    if err != nil || !info.IsDir() { /* 返回稳定错误 */ }
    sum := sha256.Sum256([]byte(normalizeWorkspacePath(resolved)))
    return WorkspacePath{ID: WorkspaceID(hex.EncodeToString(sum[:16])), Path: resolved, Name: filepath.Base(resolved)}, nil
}
```

Windows 的 `normalizeWorkspacePath` 放入平台文件，大小写与卷标比较写独立测试。

- [ ] **步骤 4：运行测试并验证通过**

运行：`go test ./internal/app -run 'TestCanonicalWorkspace' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/workspace_path*.go
git commit -m "✨ feat(app): add canonical workspace identity"
```

### 任务 2：实现顶层 ControllerLease

**文件：**
- 创建：`internal/app/controller_lease.go`
- 创建：`internal/app/controller_lease_unix.go`
- 创建：`internal/app/controller_lease_windows.go`
- 创建：`internal/app/controller_lease_unsupported.go`
- 创建：`internal/app/controller_lease_test.go`
- 创建：`internal/app/testdata/leasehelper/main.go`

- [ ] **步骤 1：编写跨进程失败测试**

测试启动 lease helper A 获取 `<projectStore>/controller.lock`，再启动 helper B；断言 B 输出结构化 `workspace_locked`，包含 A 的 PID 和 mode。杀死 A 后启动 C，断言 C 成功。

```go
func TestControllerLeaseExclusiveAcrossProcesses(t *testing.T) {
    helper := buildLeaseHelper(t)
    dir := t.TempDir()
    first := startLeaseHelper(t, helper, dir, "tui")
    t.Cleanup(first.Stop)
    first.WaitReady(t)

    second := runLeaseHelper(t, helper, dir, "web")
    if second.Code != "workspace_locked" || second.OwnerPID != first.PID() {
        t.Fatalf("second = %+v", second)
    }

    first.Stop()
    third := runLeaseHelper(t, helper, dir, "web")
    if third.Code != "acquired" {
        t.Fatalf("third = %+v", third)
    }
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run TestControllerLeaseExclusiveAcrossProcesses -count=1`

预期：FAIL，lease helper/API 未定义。

- [ ] **步骤 3：实现 lease API 和 OS 锁**

```go
type ControllerMode string
const (
    ControllerModeTUI ControllerMode = "tui"
    ControllerModeWeb ControllerMode = "web"
)

type ControllerLease struct {
    file       *os.File
    instanceID string
    once       sync.Once
}

func AcquireControllerLease(storeRoot string, mode ControllerMode) (*ControllerLease, error)
func (l *ControllerLease) InstanceID() string
func (l *ControllerLease) Close() error
```

OS 锁成功后再截断并写入诊断 JSON；锁冲突读取 JSON 仅用于错误详情。unsupported 平台返回 `ErrControllerLockUnsupported`，不得无锁运行。

- [ ] **步骤 4：运行单元与跨进程测试**

运行：`go test ./internal/app -run 'TestControllerLease' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/controller_lease* internal/app/testdata/leasehelper
git commit -m "🔒️ feat(app): lock workspace controller across processes"
```

### 任务 3：为 session store 增加显式 root 构造器

**文件：**
- 修改：`internal/session/jsonl_store.go`
- 修改：`internal/session/jsonl_store_test.go`
- 修改：`internal/session/jsonl_store_migration_test.go`

- [ ] **步骤 1：编写测试，证明 store 不依赖 cwd**

```go
func TestNewJSONLStoreForWorkspaceIgnoresProcessCWD(t *testing.T) {
    workspace := t.TempDir()
    other := t.TempDir()
    old, err := os.Getwd()
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = os.Chdir(old) })
    if err := os.Chdir(other); err != nil { t.Fatal(err) }

    store, err := NewJSONLStoreForWorkspace(workspace)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(store.Root(), projectNameFor(workspace)) {
        t.Fatalf("root = %q", store.Root())
    }
}
```

- [ ] **步骤 2：运行定向测试确认失败**

运行：`go test ./internal/session -run TestNewJSONLStoreForWorkspaceIgnoresProcessCWD -count=1`

预期：FAIL，新构造器不存在。

- [ ] **步骤 3：实现显式 workspace 构造器**

保留 `NewJSONLStoreInCwd()` 作为兼容包装，但其内部只调用：

```go
func NewJSONLStoreForWorkspace(workspaceRoot string) (*JSONLStore, error) {
    projectRoot, err := projectSessionsRoot(workspaceRoot)
    if err != nil { return nil, err }
    return newJSONLStore(projectRoot), nil
}
```

迁移测试同时覆盖显式 root 和 legacy 路径。

- [ ] **步骤 4：运行 session 测试**

运行：`go test ./internal/session -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/session/jsonl_store.go internal/session/jsonl_store*_test.go
git commit -m "♻️ refactor(session): parameterize workspace store root"
```

### 任务 4：提取 `WorkspaceRuntime` 组合根

**文件：**
- 创建：`internal/app/runtime_options.go`
- 创建：`internal/app/runtime.go`
- 创建：`internal/app/runtime_builder.go`
- 创建：`internal/app/runtime_test.go`
- 创建：`internal/app/toolset.go`
- 创建：`internal/app/toolset_test.go`
- 修改：`cmd/agent/bootstrap.go`
- 修改：`cmd/agent/interactive.go`
- 修改：`cmd/agent/tool_registration.go`
- 修改：`cmd/agent/bootstrap_test.go`
- 修改：`cmd/agent/register_test.go`

- [ ] **步骤 1：写失败测试固定 root 和关闭顺序**

使用 fake closers 断言先停止接收新命令，再按 `mcp → task → runner → config → events` 关闭；每阶段最多等待 5 秒，两次或并发 Close 不重复执行并聚合错误。builder 接受 `Root`，测试过程中改变 cwd 仍使用原 root；另起两个 runtime，断言 todo/transcript/state/question/plan-finalize 工具实例与 session 绑定互不串线。

```go
func TestWorkspaceRuntimeCloseOrderAndIdempotence(t *testing.T) {
    var order []string
    runtime := newTestRuntime(recordCloser(&order, "mcp"), recordCloser(&order, "task"), recordCloser(&order, "runner"), recordCloser(&order, "config"), recordCloser(&order, "events"))
    if err := runtime.Close(); err != nil { t.Fatal(err) }
    if err := runtime.Close(); err != nil { t.Fatal(err) }
    if diff := cmp.Diff([]string{"mcp", "task", "runner", "config", "events"}, order); diff != "" {
        t.Fatal(diff)
    }
}
```

- [ ] **步骤 2：运行定向测试确认失败**

运行：`go test ./internal/app ./cmd/agent -run 'TestWorkspaceRuntime|TestBuildRunner' -count=1`

预期：FAIL，runtime 类型/构造器不存在。

- [ ] **步骤 3：实现 options 和 runtime ownership**

```go
type WorkspaceRuntimeOptions struct {
    Root             string
    SessionID        string
    Output           ui.UI
    AllowOutsideRead bool
    AllowIncomplete  bool
    WorkerContext    WorkerContext
    SelectionBroker  *selecttool.Broker
    TodoBroker       *todo.Broker
    ControllerLease  *ControllerLease
    ResourceGovernor *ResourceGovernor
}
```

`BuildWorkspaceRuntime(ctx, opts, configurators...)` 承接现有 `buildRunnerWithTaskContext` 内容。`WorkerContext` 与 `ToolConfigurator` 在 `internal/app` 导出，CLI 只做参数适配。所有 path resolution 使用 `opts.Root`；不得调用 `os.Chdir`，也不得在打开新工作区时调用会 `os.Setenv` 的 `.env` loader。`paw serve` 仅使用进程启动时已继承/加载的环境，新 workspace 中的 `.env` 必须由测试证明不会污染其他 runtime。

- [ ] **步骤 4：把工具注册改为 runtime-owned `Toolset`**

`Toolset` 持有当前包级 `mainTodoTool`、`mainSearchTranscriptTool`、memory/ariadne tool 与 `finalizeTool` 的替代实例，并提供 `BindSession(store, sessionID)`。删除这些进程全局变量；`registerTools`/`registerMainAgentTools` 迁成 `internal/app` 可复用函数，`cmd/agent/tool_registration.go` 仅保留 CLI adapter。测试创建两个 toolset，交错 rebind 后分别执行工具，断言事件、文件路径和 session ID 不串线。

- [ ] **步骤 5：让 CLI 包装调用新 builder**

`cmd/agent/buildRunner*` 保留签名，获取当前 root 后构造 options 并委托 `internal/app`，避免一次提交改完所有调用者。TUI 启动先获取 ControllerLease，并在 runtime Close 时释放；task worker 通过 `WorkerContext.InstanceID` 继承顶层身份而不重复获取 lease。

- [ ] **步骤 6：运行定向和全量测试**

运行：

```bash
go test ./internal/app ./cmd/agent ./internal/session ./internal/task -count=1
go test ./...
```

预期：全部 PASS，TUI 现有测试不变。

- [ ] **步骤 7：提交**

```bash
git add internal/app/runtime*.go internal/app/toolset*.go cmd/agent/bootstrap.go cmd/agent/interactive.go cmd/agent/tool_registration.go cmd/agent/bootstrap_test.go cmd/agent/register_test.go
git commit -m "♻️ refactor(app): extract isolated workspace runtimes"
```

---

## 阶段 2：Supervisor、资源预算与 Session 投影

### 任务 5：实现共享 `ResourceGovernor`

**文件：**
- 创建：`internal/app/resource_governor.go`
- 创建：`internal/app/resource_governor_test.go`
- 修改：`internal/task/manager.go`
- 修改：`internal/task/pool.go`
- 修改：`internal/task/manager_test.go`

- [ ] **步骤 1：编写 worker slot 失败测试**

创建 governor capacity=2，三个不同 runtime 并发 Acquire；断言第三个阻塞，Release 后获得，context cancel 返回 `resource_capacity`/context error。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run TestResourceGovernor -count=1`

预期：FAIL，类型不存在。

- [ ] **步骤 3：实现 bounded semaphore**

```go
type ResourceGovernor struct { workerSlots chan struct{} }
func NewResourceGovernor(maxWorkers int) *ResourceGovernor
func (g *ResourceGovernor) AcquireWorker(ctx context.Context) (release func(), err error)
```

release 用 `sync.Once` 防双释放。

- [ ] **步骤 4：把 governor 注入 task launcher**

`task.Manager` 新增可选 governor capability；worker 真正启动前 Acquire，进程终止后 Release。nil governor 保持现有 TUI/测试行为。

- [ ] **步骤 5：运行 task race 测试**

运行：

```bash
go test ./internal/app ./internal/task -count=1
go test -race ./internal/task -run 'TestManager|TestPool' -count=1
```

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/resource_governor* internal/task/manager.go internal/task/pool.go internal/task/*test.go
git commit -m "✨ feat(app): share worker capacity across workspaces"
```

### 任务 6：实现 Supervisor 两 runtime 上限与淘汰

**文件：**
- 创建：`internal/app/supervisor.go`
- 创建：`internal/app/supervisor_test.go`
- 创建：`internal/app/recent_workspaces.go`
- 创建：`internal/app/recent_workspaces_test.go`

- [ ] **步骤 1：写 table tests 固定容量规则**

覆盖：打开两个 runtime；第三个淘汰最旧空闲；active turn/task/pending interaction/queue 不可淘汰；两个均 busy 返回 `ErrRuntimeCapacity`；Close 与 ForgetRecent 分离。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run 'TestSupervisor|TestRecentWorkspace' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 runtime factory 和 busy snapshot**

```go
type RuntimeFactory func(context.Context, WorkspaceRuntimeOptions) (*WorkspaceRuntime, error)
type RuntimeActivity struct {
    ActiveTurn, ActiveTasks, PendingInteractions, QueuedInputs, ActiveWrites int
}
```

Supervisor 所有 map/LRU 操作在 mutex 下；实际 Build/Close 不持锁，使用 opening/closing 状态防重复。

- [ ] **步骤 4：实现最近工作区存储**

复用现有 Paw 用户设置目录，新建独立 JSON 文件或 settings 字段，只持久化 canonical path、name、last_opened_at。原子临时文件 rename 写入。

- [ ] **步骤 5：运行 race 测试**

运行：`go test -race ./internal/app -run 'TestSupervisor|TestRecentWorkspace' -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/supervisor* internal/app/recent_workspaces*
git commit -m "✨ feat(app): supervise bounded workspace runtimes"
```

### 任务 7：建立 WorkspaceCoordinator 状态机

**文件：**
- 创建：`internal/app/coordinator.go`
- 创建：`internal/app/coordinator_test.go`
- 创建：`internal/app/state.go`

- [ ] **步骤 1：编写状态机失败测试**

覆盖：同工作区只能 Begin 一个 turn；导航读取其他 session 不改变 active session；steer/queue/cancel 验证 active turn ID；terminal 状态清空 active turn；Activity 快照阻止淘汰。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run TestWorkspaceCoordinator -count=1`

预期：FAIL。

- [ ] **步骤 3：实现串行 coordinator**

优先使用 mutex + 小而纯的 state transition；不要在锁内执行模型调用或文件 IO。每个变更方法返回不可变 snapshot/change set，后续任务接入 journal/EventHub。

```go
type WorkspaceState struct {
    ActiveSessionID string
    ActiveTurnID    string
    SessionVersion  map[string]uint64
    Queue           map[string][]InputDraft
    Pending         map[string]InteractionState
}
```

- [ ] **步骤 4：运行 race 测试**

运行：`go test -race ./internal/app -run TestWorkspaceCoordinator -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/coordinator.go internal/app/coordinator_test.go internal/app/state.go
git commit -m "✨ feat(app): coordinate workspace turn state"
```

### 任务 8：实现 store-only SessionService

**文件：**
- 创建：`internal/app/services.go`
- 创建：`internal/app/session_projection.go`
- 创建：`internal/app/session_projection_test.go`
- 修改：`internal/session/jsonl_store.go`
- 修改：`internal/sessionactor/host.go`
- 修改：`internal/sessionactor/host_test.go`

- [ ] **步骤 1：写失败测试证明 list/snapshot 不激活 Host**

用 Host spy 记录 Load/New/Fork 调用；调用 SessionService.List/Snapshot 后应为 0。Create/Fork 写 store metadata，但 active session 不变。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app ./internal/sessionactor -run 'TestSessionService|TestHostActiveSession' -count=1`

预期：FAIL。

- [ ] **步骤 3：给 store 增加分页 projector API**

```go
type SessionPageRequest struct { Cursor string; Limit int }
type TurnPageRequest struct { Before string; Limit int }
```

列表默认 50，最大 100；snapshot 默认最近 30 turn。cursor 是 base64url 编码的稳定排序键，不暴露文件路径。

- [ ] **步骤 4：实现 Create/Fork store-only 路径**

Fork 固定使用目标 session 最后持久化 sequence；不包含内存 delta，不调用 Host 激活。需要新增的 store 方法必须有 journal lineage 测试。

- [ ] **步骤 5：运行 session/sessionactor/app 测试**

运行：`go test ./internal/app ./internal/session ./internal/sessionactor -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/services.go internal/app/session_projection* internal/session/jsonl_store.go internal/sessionactor/host*
git commit -m "✨ feat(app): project sessions without activating engine"
```

---

## 阶段 3：EventHub、快照和命令 receipt

### 任务 9：定义版本化 AppEvent DTO

**文件：**
- 创建：`internal/app/event.go`
- 创建：`internal/app/event_test.go`

- [ ] **步骤 1：为事件表逐项写 JSON golden tests**

至少覆盖 assistant delta、tool completed、question requested、permission requested、reset required，断言 `schema_version=1`、稳定字段名、detail 不进入摘要事件。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run TestAppEventJSON -count=1`

预期：FAIL。

- [ ] **步骤 3：实现信封和 typed payload**

```go
type AppEvent struct {
    SchemaVersion uint16          `json:"schema_version"`
    StreamID      string          `json:"stream_id"`
    Sequence      uint64          `json:"sequence"`
    WorkspaceID   WorkspaceID     `json:"workspace_id"`
    SessionID     string          `json:"session_id,omitempty"`
    TurnID        string          `json:"turn_id,omitempty"`
    Type          EventType       `json:"type"`
    Time          time.Time       `json:"time"`
    EntityVersion uint64          `json:"entity_version,omitempty"`
    Payload       json.RawMessage `json:"payload"`
}
```

每种 payload 用独立 Go struct；禁止 handler 构造任意 map。

- [ ] **步骤 4：运行测试**

运行：`go test ./internal/app -run TestAppEventJSON -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/event*.go
git commit -m "✨ feat(app): define versioned workbench events"
```

### 任务 10：实现 EventHub 原子 replay/live 切换

**文件：**
- 创建：`internal/app/event_hub.go`
- 创建：`internal/app/event_hub_test.go`

- [ ] **步骤 1：编写确定性竞态测试**

测试 hook 暂停 Subscribe 在注册 subscriber 后、释放锁前；并发 Publish 必须在 replay 后进入 subscriber queue且不丢失。另测错误 stream、游标超前、ring 淘汰、慢消费者 reset。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run TestEventHub -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 ring 的事件/字节/时间限制**

Ring 记录 event 和 encoded size；上限 10,000 events / 16 MiB / 120 seconds。subscriber queue 同时按 1,000 events / 1 MiB 限制。

- [ ] **步骤 4：实现原子 Subscribe**

```go
type Subscription struct {
    Replay []AppEvent
    Events <-chan AppEvent
    Reset  <-chan ResetReason
    Close  func()
}
func (h *EventHub) Subscribe(cursor EventCursor) (Subscription, error)
```

在同一锁内验证 epoch、注册 subscriber、复制 replay。溢出只发 reset 并关闭，不 drop。

- [ ] **步骤 5：运行 race 测试**

运行：`go test -race ./internal/app -run TestEventHub -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/event_hub*
git commit -m "✨ feat(app): replay workspace events without gaps"
```

### 任务 11：实现一致 Snapshot 与 delta offset

**文件：**
- 创建：`internal/app/snapshot.go`
- 创建：`internal/app/snapshot_test.go`
- 修改：`internal/app/coordinator.go`
- 修改：`internal/app/coordinator_test.go`

- [ ] **步骤 1：写状态/水位竞态测试**

通过 hook 控制事件发生在 snapshot 前或后：若在前，snapshot 包含投影且 watermark 包含事件；若在后，snapshot 不含但 SSE replay 必含。再测 part offset duplicate/gap。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run 'TestSessionSnapshot|TestPartOffset' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 snapshot 临界区**

Coordinator 持锁复制 state 和 EventHub 当前 cursor；Coordinator 与 EventHub 的提交顺序固定，避免反向锁顺序。推荐 EventHub sequence 分配由 coordinator commit 调用，EventHub 不回调 coordinator。

- [ ] **步骤 4：实现 25ms / 16KiB delta batcher**

创建内部 batcher，输出携带稳定 `part_id` 与 UTF-8 byte offset。完成 part 前 flush。Fake clock 测试时间阈值，不使用 sleep。

- [ ] **步骤 5：运行 race 测试**

运行：`go test -race ./internal/app -run 'TestSessionSnapshot|TestPartOffset|TestDeltaBatcher' -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/snapshot* internal/app/coordinator* internal/app/delta_batcher*
git commit -m "✨ feat(app): snapshot streaming state consistently"
```

### 任务 12：实现 command receipt 与幂等语义

**文件：**
- 创建：`internal/app/command_receipt.go`
- 创建：`internal/app/command_receipt_test.go`
- 修改：`internal/session/journal.go`
- 修改：`internal/session/record_envelope.go`
- 修改：`internal/session/record_envelope_test.go`
- 修改：`internal/session/jsonl_store.go`
- 修改：`internal/session/jsonl_store_test.go`

- [ ] **步骤 1：写 receipt 重放测试**

同一 command ID 两次 Create/Submit 返回同 resource ID，store 只追加一次；进程内重建 service 后仍能从 journal 找到 receipt。Cancel 和 interaction 重试返回原终态。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/session -run 'TestCommandReceipt|TestIdempotent' -count=1`

预期：FAIL。

- [ ] **步骤 3：增加 journal command receipt 记录**

若新增 JournalKind，必须同步 `internal/session/record_envelope.go` 的 `kindToEvent`/`eventToKind` 映射和 `record_envelope_test.go` round-trip 测试。receipt 字段：command ID、kind、resource ID、status、session version、timestamp。

- [ ] **步骤 4：实现确定性/持久映射**

Create/Fork/Submit 在写领域事实的同一事务序列中写 receipt；重试先查 receipt。不要缓存 validation error；只持久化 accepted/completed 结果。

- [ ] **步骤 5：运行全量 session/app 测试**

运行：`go test ./internal/app ./internal/session ./internal/es -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/command_receipt* internal/session/journal.go internal/session/record_envelope* internal/session/jsonl_store*
git commit -m "✨ feat(app): persist idempotent command receipts"
```

### 任务 13：实现核心事件 UI Adapter

**文件：**
- 创建：`internal/app/ui_adapter.go`
- 创建：`internal/app/ui_adapter_test.go`
- 修改：`internal/ui/ui.go`（只在缺失事件字段时最小扩展）
- 修改：`internal/loop/turn.go` 或现有事件发射点（仅为稳定 part/tool IDs）

- [ ] **步骤 1：写 adapter 事件序列测试**

模拟 reasoning start/delta/end、assistant delta、tool call/result、done，断言 EventHub 得到稳定顺序、part ID、offset、tool duration 摘要，完整结果只进入 detail store。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app -run TestUIAdapter -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 `ui.UI` 与可选扩展**

Adapter 实现 `UI`、`ThinkingDeltaReceiver`、`AssistantPartReceiver`、`SystemNotifier`、`FileMutationConsumer`。调用必须只提交 coordinator change，不做 HTTP 工作。

- [ ] **步骤 4：运行 loop/app 测试**

运行：`go test ./internal/app ./internal/loop ./internal/ui -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/ui_adapter* internal/ui/ui.go internal/loop
git commit -m "✨ feat(app): project loop events for web clients"
```

---

## 阶段 4：安全只读 Web Server 与 React Shell

### 任务 14：增加 `serve` 子命令且保持 CLI 兼容

**文件：**
- 修改：`cmd/agent/main.go`
- 修改：`cmd/agent/options.go`
- 创建：`cmd/agent/serve.go`
- 修改：`cmd/agent/main_test.go`
- 创建：`cmd/agent/serve_test.go`

- [ ] **步骤 1：写 CLI table tests**

覆盖 `paw`、legacy flags、worker flags、`serve --open`、`serve --listen 0.0.0.0:8000` 拒绝、未知 serve flag。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./cmd/agent -run 'TestParse|TestServeOptions' -count=1`

预期：FAIL。

- [ ] **步骤 3：在 legacy Parse 前路由子命令**

`main` 检查第一个非程序参数是否严格等于 `serve`；serve 使用独立 FlagSet。worker 隐藏 flag 路径先于用户子命令或按现有契约保持。

- [ ] **步骤 4：运行 cmd 测试**

运行：`go test ./cmd/agent -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add cmd/agent/main.go cmd/agent/options.go cmd/agent/serve.go cmd/agent/*test.go
git commit -m "✨ feat(cli): add local web serve command"
```

### 任务 15：实现 bootstrap token exchange 和 HTTP middleware

**文件：**
- 创建：`internal/web/auth.go`
- 创建：`internal/web/auth_test.go`
- 创建：`internal/web/middleware.go`
- 创建：`internal/web/middleware_test.go`
- 创建：`internal/web/errors.go`

- [ ] **步骤 1：写安全失败测试**

覆盖：fragment token 不在请求日志；exchange 一次成功二次失败；cookie HttpOnly/SameSite/Path；无 cookie API 401；错误 Host 421/403；跨 Origin 写请求拒绝；未知 JSON 字段与超大 body 拒绝；安全 headers 存在。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/web -run 'TestAuth|TestMiddleware' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 auth store**

bootstrap token 256-bit random，只存 hash；session cookie 256-bit random，内存 session map 与 server 生命周期一致。恒定时间比较。exchange 成功后删除 bootstrap token。

- [ ] **步骤 4：实现 middleware 链**

顺序：request ID → security headers → Host → auth → write Origin/Sec-Fetch → body limit → handler。SSE 不使用普通 write timeout。

- [ ] **步骤 5：运行 web race 测试**

运行：`go test -race ./internal/web -run 'TestAuth|TestMiddleware' -count=1`

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/web/auth* internal/web/middleware* internal/web/errors.go
git commit -m "🔒️ feat(web): secure loopback browser sessions"
```

### 任务 16：实现 bootstrap/workspace/session/export API 与 serve 启停

**文件：**
- 创建：`internal/web/server.go`
- 创建：`internal/web/server_test.go`
- 创建：`internal/web/handlers_bootstrap.go`
- 创建：`internal/web/handlers_workspaces.go`
- 创建：`internal/web/handlers_sessions.go`
- 创建：`internal/web/handlers_export.go`
- 创建：`internal/web/handlers_read_test.go`
- 创建：`internal/web/handlers_export_test.go`
- 修改：`cmd/agent/serve.go`
- 修改：`cmd/agent/serve_test.go`

- [ ] **步骤 1：写 handler contract tests**

用 fake services 断言 `GET /api/bootstrap`、最近工作区、打开/关闭/忘记工作区、session 列表/快照与 export 的 JSON/下载契约：分页 cursor、最近 30 turn、2 MiB 上限、稳定错误 code、`Content-Disposition` 文件名，以及 Close 与 ForgetRecent 分离。另用临时 listener 验证 `runServe` 启动、打印/打开 fragment bootstrap URL，并在 context 取消后关闭 HTTP Server 与 Supervisor。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/web -run 'TestBootstrap|TestWorkspaceHandlers|TestSessionHandlers' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 server 和 handlers**

所有 DTO 是显式 struct；decoder `DisallowUnknownFields`；response encoder 不暴露 config secret/path 以外敏感字段。Session history 使用 `before/limit`。Export 只从 session store/projector 读取已持久内容，并使用安全文件名。`cmd/agent/serve.go` 组装 Supervisor、AuthStore 与 `internal/web.Server`，`--open` 只把一次性 token 放进 URL fragment；关闭路径聚合 Server/Supervisor 错误。

- [ ] **步骤 4：运行测试**

运行：`go test ./internal/web ./internal/app -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/web/server* internal/web/handlers_bootstrap.go internal/web/handlers_workspaces.go internal/web/handlers_sessions.go internal/web/handlers_export* internal/web/handlers_read_test.go cmd/agent/serve.go cmd/agent/serve_test.go
git commit -m "✨ feat(web): serve workspace session APIs"
```

### 任务 17：实现 cookie-authenticated SSE

**文件：**
- 创建：`internal/web/sse.go`
- 创建：`internal/web/sse_test.go`

- [ ] **步骤 1：写 SSE wire tests**

断言 `id: stream:seq`、`event:`、`data:`、15s heartbeat（fake clock）、query `after`、浏览器 Last-Event-ID fallback、reset frame 后关闭、client cancel 释放 subscription。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/web -run TestSSE -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 SSE handler**

优先 `after` query；无 query 时解析 `Last-Event-ID`。每帧 flush。heartbeat 使用注入 ticker。所有路径 require cookie。

- [ ] **步骤 4：运行 race 测试**

运行：`go test -race ./internal/web -run TestSSE -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/web/sse*
git commit -m "✨ feat(web): stream resumable workspace events"
```

### 任务 18：建立 React/Vite 工具链和 embed 构建

**文件：**
- 创建：`internal/web/assets.go`
- 创建：`internal/web/static.go`
- 创建：`internal/web/static_test.go`
- 创建：`internal/web/ui/package.json`
- 创建：`internal/web/ui/package-lock.json`
- 创建：`internal/web/ui/tsconfig.json`
- 创建：`internal/web/ui/vite.config.ts`
- 创建：`internal/web/ui/eslint.config.js`
- 创建：`internal/web/ui/vitest.config.ts`
- 创建：`internal/web/ui/index.html`
- 创建：`internal/web/ui/src/main.tsx`
- 创建：`internal/web/ui/src/app/App.tsx`
- 创建：`scripts/build-web.sh`
- 创建：`scripts/dev-web.sh`

- [ ] **步骤 1：写 static handler Go 测试**

断言 `/`、`/index.html` 200 + no-store；哈希 asset immutable；未知非 API path fallback 到 index；`/api/unknown` 404 JSON，不返回 SPA。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/web -run TestStaticHandler -count=1`

预期：FAIL。

- [ ] **步骤 3：创建最小 React shell 和 scripts**

package scripts 固定：`typecheck`, `lint`, `test`, `build`, `test:e2e`。依赖版本锁定，优先复用 token tracer dashboard 已有 React/Vite/Vitest/Playwright major version。

- [ ] **步骤 4：构建 dist 并实现 embed**

运行：

```bash
./scripts/build-web.sh
go test ./internal/web -run TestStaticHandler -count=1
```

预期：前端检查与 Go static test PASS；`internal/web/ui/dist` 生成。

- [ ] **步骤 5：验证 stale dist 检查**

再次运行 `./scripts/build-web.sh` 后：`git diff --exit-code -- internal/web/ui/dist`。

预期：无差异。

- [ ] **步骤 6：提交**

```bash
git add internal/web/assets.go internal/web/static* internal/web/ui scripts/build-web.sh scripts/dev-web.sh
git commit -m "✨ feat(web): embed React workbench shell"
```

### 任务 19：实现前端 API 类型、快照 store 与 Event reducer

**文件：**
- 创建：`internal/web/ui/src/api/types.ts`
- 创建：`internal/web/ui/src/api/client.ts`
- 创建：`internal/web/ui/src/api/eventStream.ts`
- 创建：`internal/web/ui/src/app/store.ts`
- 创建：`internal/web/ui/src/app/reducer.ts`
- 创建：`internal/web/ui/src/app/queries.ts`
- 创建：`internal/web/ui/src/app/reducer.test.ts`
- 创建：`internal/web/ui/src/api/eventStream.test.ts`

- [ ] **步骤 1：写 reducer 失败测试**

覆盖 snapshot 初始化、重复 event 忽略、错误 stream reset、sequence gap reset、part offset duplicate/gap、unknown event ignore、session version 不受 delta 干扰。

- [ ] **步骤 2：运行前端测试确认失败**

运行：`npm --prefix internal/web/ui test -- --run src/app/reducer.test.ts`

预期：FAIL，reducer 未定义。

- [ ] **步骤 3：实现 discriminated union 和 reducer**

`AppEvent` 用 `type` 判别联合；schema_version 非 1 返回 fatal upgrade state；未知 type 在 runtime guard 中诊断后忽略。

- [ ] **步骤 4：实现 EventSource 连接器**

首次 URL 带 snapshot cursor；`event.reset_required` 调用 store 的 `reloadSnapshot()`；重连使用指数退避并显示连接状态。

- [ ] **步骤 5：运行前端检查**

运行：

```bash
npm --prefix internal/web/ui run typecheck
npm --prefix internal/web/ui run lint
npm --prefix internal/web/ui test -- --run
```

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/web/ui/src/api internal/web/ui/src/app
./scripts/build-web.sh
git add internal/web/ui/dist
git commit -m "✨ feat(web): reduce snapshots and live events"
```

### 任务 20：实现只读 DSH 风格工作台

**文件：**
- 创建：`internal/web/ui/src/components/WorkbenchShell.tsx`
- 创建：`internal/web/ui/src/features/workspaces/WorkspaceSidebar.tsx`
- 创建：`internal/web/ui/src/features/sessions/SessionList.tsx`
- 创建：`internal/web/ui/src/features/conversation/ConversationView.tsx`
- 创建：`internal/web/ui/src/features/trace/TraceView.tsx`
- 创建：`internal/web/ui/src/features/trace/TraceDetailPanel.tsx`
- 创建：`internal/web/ui/src/components/MarkdownContent.tsx`
- 创建：`internal/web/ui/src/styles/tokens.css`
- 创建：`internal/web/ui/src/styles/workbench.css`
- 创建对应 `*.test.tsx`

- [ ] **步骤 1：写组件失败测试**

断言：侧栏工作区/会话选择；对话只展示用户/助手与过程摘要；点击摘要切轨迹并定位；轨迹点击打开详情；Markdown raw HTML 不执行，危险 URI 被移除，外部图片不加载。

- [ ] **步骤 2：运行组件测试确认失败**

运行：`npm --prefix internal/web/ui test -- --run src/features src/components`

预期：FAIL。

- [ ] **步骤 3：实现布局和设计 token**

以已提交 HTML 资产为视觉基准，使用 CSS variables；不复制 DeepSeek logo/字样。正文 max-width 920px，侧栏 300px，Composer 区先以只读占位显示。

- [ ] **步骤 4：实现安全 Markdown**

禁用 raw HTML；sanitize URL scheme；`img` 只允许 self/data，外部 URL 渲染为可点击占位而非自动请求；tool/diff 使用 `<pre>` 纯文本。

- [ ] **步骤 5：运行前端全检查与 build**

运行：`./scripts/build-web.sh`

预期：PASS，dist 更新。

- [ ] **步骤 6：提交**

```bash
git add internal/web/ui/src internal/web/ui/dist
git commit -m "💄 feat(web): build conversation-first workbench"
```

---

## 阶段 5：聊天写路径

### 任务 21：接通 Session Create/Fork、Submit 与运行状态

**文件：**
- 修改：`internal/web/handlers_sessions.go`
- 创建：`internal/web/handlers_sessions_write_test.go`
- 创建：`internal/web/handlers_turns.go`
- 创建：`internal/web/handlers_turns_test.go`
- 修改：`internal/app/services.go`
- 创建：`internal/app/turn_service.go`
- 创建：`internal/app/turn_service_test.go`
- 修改：`internal/web/ui/src/features/sessions/SessionList.tsx`
- 修改对应 session 前端测试
- 创建：`internal/web/ui/src/components/Composer.tsx`
- 创建：`internal/web/ui/src/components/Composer.test.tsx`

- [ ] **步骤 1：写 Go 失败测试**

Create/Fork 校验 command ID 与 session version，重试返回既有 receipt，且不隐式激活 Host；Submit 校验 session version、workspace busy、command receipt，成功后固定 active session/turn，调用 Host RunTurn，终态清理 coordinator 并发布事件。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/web -run 'TestSubmit|TestMessageHandler' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 TurnService Submit**

先把 `POST /api/workspaces/{workspace_id}/sessions` 与 `POST /api/workspaces/{workspace_id}/sessions/{session_id}/fork` 接到 SessionService，并在 UI 中支持新建、分叉与导航；然后把 message handler 接到 TurnService。HTTP handler 只 decode typed command 并调用 service。模型调用在 coordinator 锁外，终态通过 commit 方法写回；panic/error/cancel 都必须产生终态。

- [ ] **步骤 4：写 Composer 失败测试**

空闲 Enter 提交、Shift+Enter 换行、提交期间不重复 command ID、409 busy 跳转占用会话、网络不确定时原 ID 重试。

- [ ] **步骤 5：实现 Composer Submit 和状态**

草稿按 workspace/session localStorage；receipt accepted 后清空。主按钮空闲为 Send，运行中为 Stop。

- [ ] **步骤 6：运行 Go/前端检查**

运行：

```bash
go test ./internal/app ./internal/web ./internal/sessionactor -count=1
./scripts/build-web.sh
```

预期：PASS。

- [ ] **步骤 7：提交**

```bash
git add internal/app/turn_service* internal/web/handlers_sessions* internal/web/handlers_turns* internal/web/ui/src/features/sessions internal/web/ui/src/components/Composer* internal/web/ui/dist
git commit -m "✨ feat(web): create sessions and submit chat turns"
```

### 任务 22：接通 steer、queue 与 cancel

**文件：**
- 修改：`internal/app/turn_service.go`
- 修改：`internal/app/turn_service_test.go`
- 修改：`internal/web/handlers_turns.go`
- 修改：`internal/web/handlers_turns_test.go`
- 修改：`internal/web/ui/src/components/Composer.tsx`
- 创建：`internal/web/ui/src/features/conversation/QueueIndicator.tsx`
- 修改对应前端测试

- [ ] **步骤 1：写 active_turn 校验失败测试**

错误 turn ID 返回 `active_turn_changed`；当前会话 steer 调用 Host `SubmitSteer`；queue 更新 journal/state/event；cancel 对终态幂等。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/web -run 'TestSteer|TestQueue|TestCancel' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现三条命令路径**

复用现有 Loop steering/queue 语义，不在 Web 层重新定义 admission。queue receipt 与 queued input 同一持久序列写入。

- [ ] **步骤 4：写前端运行中 Composer 测试**

Enter 默认 steer；用户选择 Queue 后 Enter queue；Stop cancel；pending request 不清空普通草稿。

- [ ] **步骤 5：实现 UI 并运行全检查**

运行：

```bash
go test ./internal/app ./internal/web ./internal/loop -count=1
./scripts/build-web.sh
```

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/app/turn_service* internal/web/handlers_turns* internal/web/ui/src
./scripts/build-web.sh && git add internal/web/ui/dist
git commit -m "✨ feat(web): steer queue and cancel active turns"
```

---

## 阶段 6：交互、详情与刷新恢复

### 任务 23：为 question/permission 建立 scoped request 状态

**文件：**
- 创建：`internal/app/interaction.go`
- 创建：`internal/app/interaction_test.go`
- 修改：`internal/tool/select/types.go`
- 修改：`internal/tool/select/broker.go`
- 修改：`internal/tool/select/broker_test.go`
- 修改：`internal/sessionactor/types.go`
- 修改：`internal/sessionactor/state.go`
- 修改：`internal/sessionactor/host.go`
- 修改：`internal/sessionactor/host_test.go`
- 修改：`internal/app/ui_adapter.go`

- [ ] **步骤 1：写 scoped request 失败测试**

request ID 使用随机高熵 ID，状态包含 workspace/session/turn/tool_use；错误 scope 不能回答；解决天然幂等；runtime Close/进程恢复投影为 expired。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/tool/select ./internal/loop -run 'TestInteraction|TestQuestion|TestPermission' -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 bridge，不改变工具协议**

保留工具等待 broker 的执行模型，但 request 创建/解决同步投影到 Coordinator。刷新只重新读取同一进程内状态；不声称重启后继续 goroutine。

- [ ] **步骤 4：运行 race 测试**

运行：`go test -race ./internal/app ./internal/tool/select ./internal/loop -run 'TestInteraction|TestQuestion|TestPermission' -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/app/interaction* internal/app/ui_adapter.go internal/tool/select internal/sessionactor
git commit -m "✨ feat(app): scope pending user interactions"
```

### 任务 24：实现 interaction HTTP 与浏览器 UI

**文件：**
- 创建：`internal/web/handlers_interactions.go`
- 创建：`internal/web/handlers_interactions_test.go`
- 创建：`internal/web/ui/src/features/interactions/QuestionDialog.tsx`
- 创建：`internal/web/ui/src/features/interactions/PermissionBanner.tsx`
- 创建对应测试

- [ ] **步骤 1：写 handler 失败测试**

覆盖错误 scope 404/409、answer/decision 幂等、expired、Origin/auth、unknown fields。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/web -run TestInteractionHandlers -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 handlers**

Question answer 和 permission decision 使用不同 typed command；不能以 payload 猜 kind。

- [ ] **步骤 4：写前端交互测试**

多选/单选/自定义 question、allow once/deny permission、刷新 snapshot 恢复、resolved/expired 自动关闭、dialog focus lock。

- [ ] **步骤 5：实现 UI 并验证**

运行：

```bash
go test ./internal/web ./internal/app -count=1
./scripts/build-web.sh
```

预期：PASS。

- [ ] **步骤 6：提交**

```bash
git add internal/web/handlers_interactions* internal/web/ui/src/features/interactions internal/web/ui/dist
git commit -m "✨ feat(web): answer questions and permissions"
```

### 任务 25：实现 trace detail store 与右侧详情栏

**文件：**
- 创建：`internal/app/trace_detail.go`
- 创建：`internal/app/trace_detail_test.go`
- 创建：`internal/web/handlers_trace.go`
- 创建：`internal/web/handlers_trace_test.go`
- 修改：`internal/web/ui/src/features/trace/TraceDetailPanel.tsx`
- 修改对应测试

- [ ] **步骤 1：写 detail 尺寸/安全测试**

完整 tool input/result/diff 不进入 SSE；detail API scoped auth；2 MiB 截断标记；内容按纯文本 JSON 返回，不返回任意文件路径内容之外的 secret。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/web -run TestTraceDetail -count=1`

预期：FAIL。

- [ ] **步骤 3：实现 bounded detail store**

detail 与 event ID/tool_use ID 关联；内存 detail 在 runtime 生命周期内可查，已持久化 tool result 可按需从 journal 重建。设置单 detail 2 MiB、总 cache 上限。

- [ ] **步骤 4：实现详情栏 loading/error/truncated 状态**

参数、结果、错误、diff 一律 `<pre>` 纯文本；复制按钮使用浏览器 clipboard API。

- [ ] **步骤 5：运行全检查并提交**

```bash
go test ./internal/app ./internal/web -count=1
./scripts/build-web.sh
git add internal/app/trace_detail* internal/web/handlers_trace* internal/web/ui/src/features/trace internal/web/ui/dist
git commit -m "✨ feat(web): inspect bounded tool trace details"
```

### 任务 26：完成 snapshot reload 与断线恢复

**文件：**
- 修改：`internal/web/ui/src/api/eventStream.ts`
- 修改：`internal/web/ui/src/app/store.ts`
- 修改：`internal/web/ui/src/app/reducer.ts`
- 创建：`internal/web/ui/src/app/recovery.test.ts`
- 修改：`internal/web/sse_test.go`

- [ ] **步骤 1：写恢复状态机测试**

模拟：60 秒内 replay；ring 太旧 reset；错误 stream reset；delta gap reset；reload 期间 UI 保留 stale 内容并显示 reconnecting；一次 snapshot 成功后恢复 live。

- [ ] **步骤 2：运行失败测试**

运行：`npm --prefix internal/web/ui test -- --run src/app/recovery.test.ts`

预期：FAIL。

- [ ] **步骤 3：实现单飞 snapshot reload**

多个 reset 只触发一个 reload；reload 成功关闭旧 EventSource 并用新 cursor 建立连接；失败指数退避，上限 30 秒。

- [ ] **步骤 4：运行前后端测试**

运行：

```bash
npm --prefix internal/web/ui test -- --run
npm --prefix internal/web/ui run typecheck
go test ./internal/web ./internal/app -count=1
```

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/web/ui/src/api/eventStream* internal/web/ui/src/app internal/web/sse_test.go
./scripts/build-web.sh && git add internal/web/ui/dist
git commit -m "✨ feat(web): recover browser sessions after disconnects"
```

---

## 阶段 7：多工作区、重启语义和最终验收

### 任务 27：接通 Supervisor 到 Web 工作区切换

**文件：**
- 修改：`internal/web/handlers_workspaces.go`
- 修改：`internal/web/handlers_read_test.go`
- 修改：`internal/web/ui/src/features/workspaces/WorkspaceSidebar.tsx`
- 创建：`internal/web/ui/src/features/workspaces/OpenWorkspaceDialog.tsx`
- 修改对应测试

- [ ] **步骤 1：写第三 runtime 和并行测试**

打开 ws1/ws2，分别运行 turn；打开 ws3 时二者 busy 返回 `runtime_capacity`。ws1 空闲后打开 ws3 淘汰 ws1，并发布 closing。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/web -run 'TestTwoWorkspaces|TestRuntimeCapacity' -count=1`

预期：FAIL 或 Web 尚未接线。

- [ ] **步骤 3：实现 Open/Close/Forget UI**

Dialog 只接受绝对路径字符串；错误显示稳定 code 对应文案。最近列表与 loaded 状态分开显示。

- [ ] **步骤 4：运行全检查并提交**

```bash
go test ./internal/app ./internal/web -count=1
./scripts/build-web.sh
git add internal/web/handlers_workspaces* internal/web/ui/src/features/workspaces internal/web/ui/dist
git commit -m "✨ feat(web): switch bounded local workspaces"
```

### 任务 28：实现进程重启 interrupted/expired 投影

**文件：**
- 创建：`internal/app/recovery.go`
- 创建：`internal/app/recovery_test.go`
- 修改：`internal/session/journal.go`
- 修改：`internal/session/jsonl_store.go`
- 修改相关映射测试

- [ ] **步骤 1：写重启恢复失败测试**

构造 journal：turn.started 无终态、question.requested 无 resolved。新 runtime snapshot 应追加/投影 `turn.interrupted` 与 `interaction.expired`；旧 stream cursor reset。

- [ ] **步骤 2：运行失败测试**

运行：`go test ./internal/app ./internal/session -run TestRecoverInterruptedState -count=1`

预期：FAIL。

- [ ] **步骤 3：实现幂等 recovery pass**

恢复记录必须有稳定 dedupe key，重复启动不重复追加。permission/question 不尝试重新等待。

- [ ] **步骤 4：运行测试并提交**

```bash
go test ./internal/app ./internal/session -count=1
git add internal/app/recovery* internal/session/journal.go internal/session/jsonl_store.go internal/session/*test.go
git commit -m "✨ feat(app): expire interrupted browser interactions"
```

### 任务 29：建立真实 E2E fixture

**文件：**
- 创建：`internal/web/testdata/webfixture/main.go`
- 创建：`internal/web/testdata/webfixture/script.go`
- 创建：`internal/web/ui/e2e/workbench.spec.ts`
- 创建：`internal/web/ui/playwright.config.ts`
- 修改：`internal/web/ui/package.json`

- [ ] **步骤 1：实现确定性 fixture runtime**

Fixture 提供固定 workspace/session，并按脚本发 assistant/reasoning/tool/question/permission；提供测试-only 断开 SSE、填满 ring、模拟重启端点，仅编译到 fixture binary。

- [ ] **步骤 2：写 Playwright 测试**

覆盖规格中的 10 条 E2E：聊天、steer/queue/cancel、question、permission、refresh、replay/reset、双 workspace、busy、restart。

- [ ] **步骤 3：运行 E2E 并修复 fixture/客户端问题**

运行：

```bash
npm --prefix internal/web/ui run test:e2e
```

预期：全部 PASS；失败时保留 trace/screenshot。

- [ ] **步骤 4：提交**

```bash
git add internal/web/testdata/webfixture internal/web/ui/e2e internal/web/ui/playwright.config.ts internal/web/ui/package.json internal/web/ui/package-lock.json
git commit -m "🧪 test(web): cover browser workbench end to end"
```

### 任务 30：性能、无障碍与视觉证据

**文件：**
- 创建：`internal/web/ui/src/test/fixtures/largeSession.ts`
- 创建：`internal/web/ui/src/app/performance.test.tsx`
- 修改：前端组件/CSS，仅处理测得问题
- 创建：`.agent/visual/browser-workbench-conversation-1440.png`
- 创建：`.agent/visual/browser-workbench-trace-1440.png`
- 创建：`.agent/visual/browser-workbench-permission-1920.png`
- 创建：`.agent/visual/browser-workbench-reconnect-1920.png`
- 创建对应 `.md` 证据说明

- [ ] **步骤 1：添加 500-turn 性能 fixture 测试**

断言首屏只请求 30 turn；event burst 使用 batching；React render spy 不随 token 数量线性触发全树渲染。

- [ ] **步骤 2：运行性能与 accessibility 测试**

运行：

```bash
npm --prefix internal/web/ui test -- --run
npm --prefix internal/web/ui run typecheck
npm --prefix internal/web/ui run lint
```

预期：PASS。

- [ ] **步骤 3：启动真实 fixture 并捕获截图**

使用项目 helper：

```bash
./.agent-md/bin/discover_helpers.sh visual
./.agent-md/bin/playwright-capture.sh http://127.0.0.1:8765 .agent/visual/browser-workbench-conversation-1440.png
```

为每个 artifact 写证据 Markdown，包含 changed files、route、viewport、artifact 和 observed result。

- [ ] **步骤 4：独立视觉审查**

让新的 reviewer/subagent 对照：

- `docs/superpowers/specs/assets/2026-08-31-browser-workbench-conversation.html`
- `docs/superpowers/specs/assets/2026-08-31-browser-workbench-trace.html`

只报告具体偏差；修复后重新捕获，不自评通过。

- [ ] **步骤 5：提交视觉和性能改进**

```bash
git add internal/web/ui/src .agent/visual internal/web/ui/dist
git commit -m "💄 feat(web): polish workbench performance and visuals"
```

### 任务 31：最终安全、竞态和全仓验证

**文件：**
- 修改：仅修复验证发现的问题
- 更新：`README.md`
- 更新：`CHANGELOG.md`
- 创建或修改：`agent-md.toml`（若项目尚无确定前端验证命令，则声明 typecheck/lint/test/build）

- [ ] **步骤 1：更新用户文档**

README 记录：

```bash
paw serve --open
./scripts/dev-web.sh
./scripts/build-web.sh
```

说明 loopback only、同工作区控制锁、两个 runtime 上限、进程重启 interrupted/expired。

- [ ] **步骤 2：运行 Go 全量验证**

```bash
git diff --name-only --diff-filter=ACMRT -- '*.go' | xargs gofmt -w
git diff --check
go test ./...
go test -race ./internal/app ./internal/web ./internal/sessionactor ./internal/task
```

预期：全部 PASS。

- [ ] **步骤 3：运行前端全量验证**

```bash
./scripts/build-web.sh
npm --prefix internal/web/ui run test:e2e
git diff --exit-code -- internal/web/ui/dist
```

预期：全部 PASS，dist 无 stale 差异。

- [ ] **步骤 4：运行真实二进制 smoke test**

```bash
go build -trimpath -o /tmp/paw-web-smoke ./cmd/agent
/tmp/paw-web-smoke serve --listen 127.0.0.1:8765
```

从另一个进程验证 `/`、auth exchange、bootstrap、SSE；再启动第二个 Paw 指向同工作区验证 `workspace_locked`。

- [ ] **步骤 5：检查工作树，只暂存本功能文件**

```bash
git status --short
git diff --check
git diff --name-only --cached
```

不得纳入会话开始前已存在的 `internal/model`、`internal/message`、`memory/progress.md` 改动。

- [ ] **步骤 6：最终提交**

```bash
git add README.md CHANGELOG.md agent-md.toml internal/app internal/web cmd/agent internal/session internal/sessionactor internal/task internal/ui internal/loop scripts .agent/visual
git diff --cached --name-only
git commit -m "✅ chore(web): verify browser workbench delivery"
```

---

## 完成定义

- [ ] `paw` TUI 的现有行为与测试未回归。
- [ ] `paw serve --open` 在 loopback 启动单二进制 Web 工作台。
- [ ] 同 canonical workspace 的第二个顶层 Paw 进程无法获得写控制权。
- [ ] 最多两个 workspace runtime，可并行两个前台 turn，共享全局 worker slot。
- [ ] 浏览器核心闭环、轨迹、question、permission、SSE reload/reset 全部工作。
- [ ] 服务重启后未完成 turn/interaction 明确变为 interrupted/expired。
- [ ] 500-turn 会话分页、事件/detail/snapshot 尺寸上限生效。
- [ ] HTTP 认证、Host/Origin/CSP、Markdown/tool 内容安全测试通过。
- [ ] Go 全量、关键 race、前端全量、E2E、真实 binary smoke 和视觉证据全部通过。
- [ ] 所有提交使用 Gitmoji，工作树中用户原有改动未被误提交。
