# Goal 指标探索与 Plan 对话规划实施计划

> For Codex workers: 按切片实施，保留工作树既有改动；每个行为先观察失败回归，再实现并验证。不要提交、推送或替换日常安装。

**Goal:** 用户给出可量化验收指标、未知实现方法时，模型自主探索、实验、测量并改进；Plan 支持持续讨论和保存，明确执行后才实施。

**Architecture:** 保留 SessionActor/Engine 真实执行链、现有权限与事件存储。Goal 增加冻结指标及宿主从真实工具结果提取的测量，完成不再依赖 TODO。Plan 的 ready 与 approved 分离，用户输入继续同一会话。

**Tech Stack:** Go、Bubble Tea、JSONL/event store；现有本地 fake model、race 与 ANSI/View 截图验证，不新增依赖或调用真实模型。

## 已确认的产品语义

- Plan：在当前对话调查、澄清、修改方案；可以保存计划，明确要求执行才实施。
- Goal：用户提供量化验收指标，方法、过程和路线由模型自主探索；指标未达标可更换方法，TODO 仅是临时工作清单。
- 两者接受补充输入，区分等待用户、用户暂停、预算暂停、完成和取消；控制命令优先于模式输入。
- 本轮保留可见预算，不新增无限后台运行、自动提权或跨会话自动恢复。

## 切片一：Goal 量化契约和执行循环

Files: `internal/runtime/goal/types.go`, `evaluator.go`, `runtime.go`, `controller.go`；新指标工具及对应测试文件；按数据、执行、恢复三个有界子切片修改持久化代码。

- [x] 先失败回归：无 TODO / TODO 全完成不能宣告目标完成。
- [x] 增加冻结的指标定义：名称、比较方向、目标值、单位、评测命令/工作目录/JSON Pointer。只根据用户指标解释，口径有歧义必须澄清；不允许模型放宽已冻结目标。
- [x] `goal_set_metric` 设置契约；`goal_record_metric` 只接受真实工具调用 ID，不接受模型自报数值或 passed。
- [x] 从当前 Goal 的已完成 Bash 调用/结果中提取有限 JSON 数字；必须匹配冻结命令和目录，拒绝旧调用、重复调用、错误/截断/非 JSON/错路径与过期测量。
- [x] 未达标时继续探索和实验；失败实验可构成新证据，文字改写不算进展；达标才完成。
- [x] 补充输入可 steer 或恢复原目标；恢复重新开启预算窗口而保留累计统计和指标；随机 ID 避免重启碰撞。
- [x] 持久化指标/测量并恢复；旧记录缺指标保持未完成。恢复提示携带指标、当前值和已有结果。

Run: `go test ./internal/runtime/goal -count=1`。验证低值→换方法→高值、假完成拒绝、口径不可改、持久化/重启及预算恢复。

## 切片二：Plan 持续对话与明确执行

Files: `internal/runtime/plan/{types,state,prompt,evaluator,runtime,controller,tool}.go`；对应测试，按状态/交互/执行授权分切片。

- [x] 先失败回归：普通澄清答案继续当前计划；普通回答后等待用户，不自动催问。
- [x] 新增 `awaiting_input` / `ready`；`plan_finalize` 只保存 ready。
- [x] `Submit` 首次创建，后续补充继续同 ID；运行中复用 PrepareSteer，不能接纳后丢输入。
- [x] 明确用户 `/plan execute` 经 Controller.Execute 才批准并交接执行；模型工具不能自行批准实施。
- [x] 修改使旧 ready 失效；暂停/恢复及旧文件兼容，工作区只读/计划目录写入边界保留。

Run: `go test ./internal/runtime/plan -count=1`。验证讨论→保存→继续修改→重新保存→明确执行、恢复后 ready、运行期拒绝执行。

## 切片三：真实输入路径与状态展示

Files: `internal/ui/bubble/input.go`, `command_registry.go`, `app.go`, `bubble.go`, `types.go`；对应测试和视觉夹具，按路由/控制/展示切片。

- [x] 将临时 overlay 的真实 handleSubmit 路径复现转为正式回归：控制命令不可当新需求。
- [x] 输入走控制器 Submit；拒绝接纳保留草稿；当前任务补充不能重新 Start。
- [x] 暂停/等待保留模式及恢复入口，完成/取消独立展示；Plan ready 不自动启动 Chat。
- [x] 用户明确执行时才交接；状态能查看 Goal 指标/测量和 Plan 等待/ready。
- [x] 宽窄真实 View 新图、输入保留、暂停/完成与独立看图审核。

Run: `go test ./internal/ui/bubble -count=1`；真实视图证据放 `.agent/visual/goal-plan-workflow/`。

## 切片四：工具和恢复接线

Files: `internal/app/toolset.go`, `internal/entry/interactive/run.go`, `internal/app/state_context.go`；新集成测试/用户文档。

- [x] 注册指标工具并绑定当前 Goal 控制器。
- [x] 证据源读取 `JSONLStore.LoadSnapshot(...).ActiveHistory`，包含当前 turn 已完成的工具配对；不调用会切换会话的 Host.LoadHistory。
- [x] Goal 模式注入持续探索/冻结验收口径提示，状态压缩保留指标和最新测量。
- [x] 真实 Engine + 工具注册表 + 本地合成模型 + JSONL 验证目标未达→实验改进→达标，并证明旧 TODO 不决定完成。

Run: `go test ./internal/app ./internal/entry/interactive ./internal/runtime/loop ./internal/storage/session -count=1`。

## 最终验收

- [x] 各切片生产冻结后执行相关全包/race和独立代码审核。
- [x] `go vet ./...`，`go test -p 1 ./... -count=1 -skip '^TestManagerAsyncDiscoveryDoesNotBlockOpen$' -timeout=90s`；明确保留此前已复现的 config 既有挂起限制。
- [x] `make build BINDIR=.agent/bin/goal-plan-rework`、独立二进制 `--help`、gofmt和diff检查。
- [x] 更新中文使用文档、`memory/` 进度及验证证据，明确当前源码/隔离构建/日常安装边界。

最终验证：41测试包通过（明确跳过既有config挂起单项），八相关包race及末次五包补验、vet/build/help/gofmt/diff通过。记录 `.agent/verification/goal-plan-rework/验证记录.md`；隔离构建未安装。
