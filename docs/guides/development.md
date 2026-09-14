# 开发与扩展

[文档导航](../README.md)

构建入口为 `cmd/paw`，不保留 `cmd/agent`。在仓库根运行 `make build` 默认生成或覆盖 `~/go/bin/paw`（目录不存在时自动创建）；可用 `make build BINDIR=bin` 指定输出目录。构建前会校验两套 embed 前端资产的内容指纹，源码修改后未重新执行对应目录的 `npm run build` 会直接报错，而不会静默打包旧 UI；仅在明确接受旧资产时可用 `PAW_SKIP_WEB_DIST_CHECK=1 make build` 跳过。`make test` 运行完整 Go 测试，`make check` 运行 vet 和构建检查。直接运行用 `go run ./cmd/paw`。

浏览器工作台开发目录为 `internal/ui/web/ui`，看板仍在 `internal/tokentracer/dashboard`；两套资产分别由所在 Go 包 embed。修改前端后运行该目录的 `npm test`、`npm run build`，并运行相应 E2E。依赖未安装时先运行 `npm ci`。

`npm run build` 仅在 typecheck 和 Vite 成功后更新 `dist/.paw-source-sha256`，记录构建输入与产物的 SHA-256。输入包括 `src`、`public`、入口 HTML、依赖清单/lockfile、TypeScript/Vite/PostCSS/Tailwind 配置、`.env*` 和指纹脚本。源码改动、产物丢失/被覆盖或指纹缺失均会中止构建；仅修改时间戳不会误报。提交前端修改时须同时提交生成的 `dist`（含指纹文件），不能单独运行脚本的 `--write` 给旧资产补指纹。

可在仓库根运行 `make check-web-dist` 单独检查；检查只需要 Bash 和 SHA-256 工具，不运行 Node/npm，不修改 `dist`。报错时只需在提示的前端目录手动重建，例如：

```bash
npm --prefix internal/ui/web/ui run build
make build
```

Token Tracer 对应 `npm --prefix internal/tokentracer/dashboard run build`。`make web-build` 是工作台的完整 npm ci/lint/test/build 流程，既有全量 lint 失败需要单独修复，不影响上述检查机制；直接 `npm run build` 不等于 lint 已通过。

门禁覆盖 `make build` 和 pre-push 的安装路径；直接 `go build`、`go run`、`make check` 不经过它。不要并行运行前端重建和消费 `dist` 的 Go 编译，因为 Vite 会先清空输出目录。内容指纹不保证不同 Node/环境变量下的构建可复现，也不替代测试或发布签名。

## 自动发布（pre-push hook）

仓库自带一个随仓库分发的 git pre-push 钩子：**每次推送 `dev` 分支时，自动把最新 dev 快照构建成 `paw` 可执行文件并安装到 `~/go/bin/paw`**，方便直接用 `paw` 命令启动。

启用方式（克隆仓库后执行一次）：

```bash
git config core.hooksPath .githooks
```

行为说明：

- 钩子文件：`.githooks/pre-push`（源码副本 `scripts/pre-push.sh`）
- 触发条件：推送目标为 `refs/heads/dev`；其他分支直接放行
- 构建前门禁：先运行 `scripts/check-web-dist.sh`，embed 前端产物陈旧时中止推送
- 构建命令：`go build -trimpath -ldflags "-s -w" -o ~/go/bin/paw ./cmd/paw`
- 版本一致性：用被推送的 `refs/heads/dev` 快照构建（不在 dev 上时会自动创建临时 worktree），保证二进制与推送内容一致
- 构建失败会中止本次 push；安装目录可用 `GOBIN` 环境变量覆盖
- 钩子只在本机生效，不会影响 CI

## 扩展点

这里只列当前稳定扩展点。

### 增加一个新工具

位置:
- 新建 `internal/capability/tool/<name>/...`
- 在 [runtime_builder.go](../../internal/app/runtime_builder.go) 注册

要求:
- 实现 `tool.Tool`

最小步骤:
1. 定义 `struct`
2. 实现 `Name`
3. 实现 `Description`
4. 实现 `InputSchema`
5. 实现 `Run`
6. 在 `app.RegisterBuiltinTools` 中 `registry.Register(...)`

可选能力:
- 实现 `IsConcurrencySafe` 启用并行批处理
- 实现 `FileMutationTarget` 让 UI 展示真实文件差异

### 替换 UI

位置:
- 新建一个实现 `ui.UI` 的包

接入点:
- [交互入口](../../internal/entry/interactive/run.go) 或 [单轮入口](../../internal/entry/oneshot/run.go)，通过 `WorkspaceRuntimeOptions.Output` 传入 UI 实现。

### 替换模型提供方

方式 1:
- 直接改 `internal/capability/model` 的 HTTP 实现

方式 2:
- 新建一个实现 `loop.ModelStreamer` 的客户端
- 在 `app.BuildWorkspaceRuntime` 中调整模型客户端装配

### 增加本地命令

接入点:
- Bubble Tea 命令注册表 `internal/ui/bubble/command_registry.go`

### 自定义 Subagent 行为

位置:
- `internal/runtime/task/manager.go` 中的 `Manager` 结构

扩展方式:
- 通过 `Manager` 的 `Config` 结构传入自定义 `Launcher`、`Notifier`、`SettingsProvider`
- 修改 `maxDepth` 限制递归深度
- 实现新的 `Store` 接口替换默认 JSONL 存储

### 接入新 MCP server

位置:
- `~/.paw/mcp.toml`（设置 `PAW_CONFIG_HOME` 时为 `$PAW_CONFIG_HOME/mcp.toml`）
- `internal/capability/mcp/`（协议实现）

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
- `app.BuildWorkspaceRuntime`
- `task.Manager`
- `tool.Registry.ReplaceNamespace`（动态工具命名空间）
