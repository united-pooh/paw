# paw

一个最小可运行的本地 coding agent。

先按下面的源码导航定位职责；详细说明见[文档导航](./docs/README.md)。

## 术语约定

- Go 没有“类”，本文中的“类”对应 `struct`
- “抽象层”对应接口或包边界
- “扩展点”只指当前代码已经预留、可以稳定接入的位置

## 源码导航

普通对话的阅读顺序：入口 → 运行时装配 → 会话宿主 → 对话引擎 → 模型 / 工具。

| 要理解或修改什么 | 从这里开始 | 职责边界 |
| --- | --- | --- |
| 启动模式、参数 | [cmd/paw/main.go](./cmd/paw/main.go)、[options.go](./internal/entry/cli/options.go) | 分发 TUI、单轮、worker、serve、tracer，不执行模型协议 |
| 工作区装配和关闭 | [internal/app/runtime_builder.go](./internal/app/runtime_builder.go)、[runtime.go](./internal/app/runtime.go) | `BuildWorkspaceRuntime` 创建并持有一个工作区的依赖 |
| 会话恢复、权限、持久化 | [internal/runtime/sessionactor](./internal/runtime/sessionactor)、[internal/storage/session](./internal/storage/session) | `Host` 承接会话命令；actor/事件存储提供恢复基础 |
| 一轮对话、工具循环、压缩 | [internal/runtime/loop/engine.go](./internal/runtime/loop/engine.go)、[model_turn.go](./internal/runtime/loop/model_turn.go) | `Engine` 编排请求与工具，不负责 HTTP 协议 |
| 模型协议、usage、重试 | [internal/capability/model/client.go](./internal/capability/model/client.go)、[usage.go](./internal/capability/model/usage.go)、[responses_stream.go](./internal/capability/model/responses_stream.go) | 供应商响应转成统一消息和事件；完成校验后才发布可执行工具 |
| 工具和子任务 | [internal/capability/tool](./internal/capability/tool)、[internal/runtime/task/manager.go](./internal/runtime/task/manager.go) | `Registry` 注册工具；`task.Manager` 调度 worker；[StreamMA](./internal/runtime/streamma) 是显式多代理编排入口 |
| 配置、内部数据路径 | [internal/platform/config](./internal/platform/config)、[internal/platform/settings](./internal/platform/settings)、[internal/platform/pawpath/paths.go](./internal/platform/pawpath/paths.go) | 模型配置、UI 设置和路径解析分工；工具工作区不等于内部存储根 |
| 终端 / 浏览器工作台 | [internal/ui/bubble](./internal/ui/bubble)、[internal/ui/web](./internal/ui/web) | 呈现与交互；浏览器工作台使用 app 的服务和协调器 |
| 跨项目 token 看板 | [internal/tokentracer](./internal/tokentracer)、[dashboard/src/global](./internal/tokentracer/dashboard/src/global) | `Recorder` 写账本，`LedgerReader` 查询，global 展示；`src/app` 保留当前实例 Dockview 调试页 |

常改位置：新增工具查 `tool.Tool` 和 `app.RegisterBuiltinTools`；新增终端命令查 [command_registry.go](./internal/ui/bubble/command_registry.go)；修改上下文上限查 `model.ResolveContextLimitTokens`；修改 usage 先读 `model.Usage` / `UsageAccumulator` 的测试。

不要为复用合并这些不同语义：Responses 展示文本与原始流字节；累计 usage 快照与增量；未报告与显式零；工作区操作路径与 Paw 内部存储路径。

## 快速使用

```bash
go run ./cmd/paw -p "hello"
go run ./cmd/paw
go run ./cmd/paw -s <session-id>
go run ./cmd/paw serve --open
go run ./cmd/paw tracer --open
```

- 不加参数直接启动时，每次都会创建一个全新的空会话。
- 需要恢复历史会话时，使用 `-s <session-id>` 指定会话 ID；也可在交互界面输入 `/sessions` 浏览并恢复历史会话。

当前运行目录会作为工作区 root，用于工具操作和项目身份识别；Paw 不再自动创建工作区 `.paw/`。内部数据统一保存到 `PAW_CONFIG_HOME`（默认 `~/.paw`），项目数据位于其中的 `projects/<id>/`。

开发验证：在仓库根目录运行 `go test ./... -count=1`、`go vet ./...`、`go build ./...`。修改 Token Tracer 前端时，在 `internal/tokentracer/dashboard` 运行 `npm test`、`npm run build`；构建产物由 Go embed 使用，浏览器回归入口是 `npm run e2e`。

## 构建与文档

```sh
make build        # ~/go/bin/paw
make test         # Go 全量测试
make check        # vet 和构建检查
```

构建入口现为 `cmd/paw`，原 `cmd/agent` 已移除。参数解析和各运行模式位于 `internal/entry`。

`make build` 默认更新 `~/go/bin/paw`；需要输出到其他位置时，使用 `make build BINDIR=bin`。构建前会校验工作台和 Token Tracer 的 embed 资产指纹，前端源码比 `dist` 新时会中止并提示运行对应目录的 `npm run build`。

- [执行层次与目录地图](./docs/architecture/layout.md)
- [使用与配置](./docs/guides/usage.md)
- [开发、构建与扩展](./docs/guides/development.md)
- [运行时职责](./docs/architecture/runtime.md)
- [包级参考](./docs/reference/README.md)

长期文档维护在 docs/architecture、docs/guides、docs/reference；本地计划与调研位于 Git 忽略的 docs/local，跨会话工作记录位于 memory。Paw 的 Plan 新文档默认写入 docs/local/plans，旧 docs/superpowers/plans 按需复制到新目录且不覆盖新副本，旧文件保持不变。
