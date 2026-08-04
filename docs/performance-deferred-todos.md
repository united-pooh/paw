# 性能优化待办清单

> 状态：P0 全部完成；P1（StreamMA 除外）已完成本轮低风险优化，P2 与 StreamMA 相关 P1 暂缓实施
>
> 原则：先建立可重复的 benchmark/profile 基线，再逐项修改；保持 API、包名、持久化语义和用户可见行为兼容。
>
> 最后更新：2026-08-04（P1 subagent/MCP/schema 优化完成；按要求未修改 StreamMA）

## 优先级说明

- **P0**：有明确的结构性热点，完成测量后优先处理。
- **P1**：收益较高，但需要更严格的行为、并发或内存验证。
- **P2**：低风险或收益依赖特定负载，排在核心热点之后。

---

## P0：JSONL journal 与持久化路径

### [x] 避免 append 前重复读取整份 session 历史

- **位置**：`internal/session/jsonl_store.go` 的 `appendRecords`
- **现状**：每次追加前调用 `readOwnRecords`，从头读取并解析当前 session 的 JSONL 记录，再计算下一个 sequence。
- **预期收益**：长会话追加延迟从随历史长度增长，降为接近 O(1) 的 sequence 分配。
- **风险**：进程异常、并发追加、session 创建和 fork 场景可能导致 sequence 不一致。
- **实施方向**：为每个 session 维护 `nextSeq`、文件大小和初始化状态；首次访问时扫描一次，后续在内存中递增。
- **前置验证**：
  - 单线程、并发追加的 sequence 连续性测试；
  - 进程重启后的重新扫描与恢复测试；
  - fork session 的 resolved history 测试；
  - 1k、10k、100k 条历史的 append benchmark。
- **验收标准**：不改变 journal 顺序和恢复结果；长历史追加的 p95 延迟和分配显著下降。
- **实施记录（2026-08-04）**：
  - `JSONLStore` 新增 `journal map[string]journalState`（nextSeq+size 缓存）；`appendRecords` 改用 `journalNextSeq`，仅当文件 size 与缓存一致时命中，否则重扫；追加成功后用 `f.Stat()` 的真实 size 更新缓存。
  - 新增 `internal/session/journal_cache_test.go`：单线程连续性、8×50 并发追加、重启重扫、外部写入失效、外部截断失效、fork resolved history、无 transcript 首次追加、ListSessions 不受影响、撕裂行容错。
  - benchmark：`BenchmarkJSONLAppendGrowth` 100k 历史从 **128,317,042 ns/op / 246,300,016 B/op / 1,200,073 allocs** 降为 **3,951,793 ns/op / 5,400 B/op / 36 allocs**（约 32× 延迟、45000× 分配），且 1k/10k/100k 几乎持平（接近 O(1)）。
  - `go test -race ./internal/session/` 通过。

### [x] 评估并调整每次 append 的 `f.Sync()` 策略

- **位置**：`internal/session/jsonl_store.go` 的 `appendRecords`
- **现状**：每次追加操作完成后都同步文件。
- **预期收益**：减少高频 journal 写入的系统调用和存储等待。
- **风险**：降低崩溃时数据持久性；可能影响恢复机制和用户对已完成 turn 的预期。
- **实施方向**：保留可靠性优先模式，并评估 `always`、`turn`、`interval` 等策略；不能直接移除同步。
- **前置验证**：故障注入、强制退出、掉电语义文档化，以及 journal 恢复测试。
- **验收标准**：明确持久化保证；任何已承诺的恢复状态不能丢失或乱序。
- **实施记录（2026-08-04）**：
  - 新增 `SyncPolicy`（`SyncPolicyAlways` 默认 / `SyncPolicyInterval`）与 `JSONLStore.SetSyncPolicy(policy, interval)`。默认行为不变（每次 append 同步）。
  - interval 模式按 session 记录 `lastSync`；超过间隔或首次写入时同步；含 `turn_completed`/`turn_failed` 边界的批次强制同步，已完成 turn 的持久化保证不弱于 always。
  - 新增 `internal/session/sync_policy_test.go`：always 每写必同步、interval 合并且首次写必同步、turn 边界强制同步、按 session 隔离、间隔到期触发、interval 下恢复完整性。
  - benchmark 显示单次 append 的 ~3.7ms 主要来自 fsync（系统开销），因此 interval 模式收益在真实高频增量场景；默认策略保持最保守。

### [x] 将 JSONLStore 的全局锁改为 session 粒度

- **位置**：`internal/session/jsonl_store.go` 的 `JSONLStore.mu` 与相关读写方法
- **现状**：目录检查、历史读取、JSON 编码、文件同步和时间戳更新可能在同一把全局锁下执行。
- **预期收益**：不同 session 并行读写时减少锁竞争。
- **风险**：锁顺序、Create/Fork/Append 交错执行时可能产生竞态或死锁。
- **实施方向**：全局锁只保护 session state map；每个 session 使用独立 mutex 或单独 writer。
- **前置验证**：`go test -race ./internal/session ./internal/loop ./internal/subagent`；并发读写和多 session benchmark。
- **验收标准**：无数据竞态、死锁、sequence 冲突和跨 session 隔离问题。
- **实施记录（2026-08-04）**：
  - `JSONLStore.mu` 只保护 `journal`、`sessionLocks` 与 `lastSync` 元数据；`appendRecords` 与 `AppendTurnMetadata` 改用 `sessionLock(sessionID)`（每 session 独立 mutex，锁顺序 sessionLock → mu），文件 I/O、JSON 编码和 fsync 不再持全局锁。
  - 新增 `BenchmarkJSONLAppendParallelSessions`（1/4/16 session × 1/4 cpu）验证跨 session 并行度。
  - `go test -race ./internal/session ./internal/loop ./internal/subagent` 全部通过；`go vet ./internal/session/` 通过。

---

## P0：Bubble Tea transcript 渲染

### [x] 建立 transcript View/render benchmark 与 pprof 基线

- **位置**：`internal/ui/bubble/layout.go`、`internal/ui/bubble/transcript.go`
- **现状**：尚未有覆盖真实 transcript 长度、终端尺寸和流式更新频率的 benchmark。
- **测试矩阵**：100、1k、5k 条 transcript；终端宽度 80、120、200；每 token、16ms 批次、33ms 批次更新。
- **观测指标**：`ns/op`、`allocs/op`、View p95/p99、堆增长和单帧最大耗时。
- **验收标准**：先得到可重复基线，再决定缓存或增量渲染是否值得引入。
- **实施记录（2026-08-04）**：
  - 新增 `internal/ui/bubble/transcript_bench_test.go`：`BenchmarkTranscriptRenderAt`（100/1k/5k 条 × 80/120/200 宽）与 `BenchmarkTranscriptRegionCacheHit`（1k 条、缓存命中）。
  - 基线（Apple M4）：5k 条 w=200 约 **106ms/op / 72MB / 530k allocs**；region 缓存命中 1k 条约 **1.86ms/op / 4.4MB / 12k allocs**——缓存收益显著，但整帧重建仍高，后续严格 revision cache 与 delta 批处理可进一步降低。

### [x] 为 transcript region 建立严格 revision cache

- **位置**：`internal/ui/bubble/layout.go` 的 `View`、`renderTranscriptRegion` 及现有 `transcriptRenderCacheKey`
- **现状**：View 会重建多个区域；背景绘制包含 `Split`、逐行样式渲染和 `Join`。
- **预期收益**：长 transcript 和高速流式输出时减少重复布局、ANSI 字符串生成和内存分配。
- **风险**：缓存失效不完整会显示旧内容；主题、宽度、viewport、selection、markdown 模式等状态都必须纳入 key。
- **实施方向**：以 transcript revision、viewport offset、宽度、主题/渲染模式和 streaming 状态组成不可变 cache key；先缓存 transcript region，不直接缓存整帧。
- **前置验证**：针对每个状态变化补充渲染快照测试和尺寸变化测试。
- **验收标准**：内容、光标、选择、主题和窗口 resize 均正确；benchmark 证明缓存命中时有收益。
- **实施记录（2026-08-04）**：
  - `appModel` 新增 `transcriptRenderSignature uint64`；`transcriptRenderSignature(entries, width, showThinking, groupExpanded, fullResult)` 用 FNV-1a 折叠全部渲染输入：宽度、thinking 可见性、tool 分组展开/全量结果开关，以及每条 entry 的 kind/version/body 长度/toolResult 长度/citations 数。所有内容变更路径经 `touchTranscriptEntry` 递增 version，签名随之变化，天然捕获主题/selection/resize 之外的纯内容变更。
  - `renderTranscriptContent` 渲染后记录签名；`renderTranscriptContentAt` 在签名未变时直接返回 `transcriptRenderedContent`，跳过逐条 key 比较与字符串拼接。selection 的 active 状态仍走原有 `renderTranscriptSelection` 后处理，不进入缓存。
  - 顺带把 `appendAssistantDelta` 从“每行 delta 触发一次 refreshViewport”收敛为“批量追加后单次 refresh”。
  - benchmark：`BenchmarkTranscriptRegionCacheHit`（1k 条缓存命中）从 **1.86ms/op / 4.4MB / 12k allocs** 降为 **14.9µs/op / 0 B / 0 allocs**（约 126×，零分配）；未命中路径（`n=1000/w=80` 全量渲染）16.2ms/op，无回归。
  - `go test ./...`、`go test -race ./internal/ui/bubble/`、`go vet`、`gofmt -l`、`git diff --check` 全部通过。

### [x] 将 assistant delta 按 16～33ms 批量合并

- **位置**：`internal/ui/bubble/app.go` 的 stream delta 消息处理链路
- **现状**：可能形成每个 token 一次 `Update`、状态修改和 View 的链路。
- **预期收益**：将 UI 更新频率控制在约 30～60 FPS，降低 TUI 主线程压力。
- **风险**：输出延迟、segment 边界、取消、tool call、done/error 事件处理可能被错误合并。
- **实施方向**：增加有界 accumulator；连续 assistant delta 批量发送；关键事件到达时强制 flush。
- **前置验证**：端到端延迟、事件顺序、取消和 tool 边界测试；慢消费者测试。
- **验收标准**：关键事件不丢失、不乱序；视觉延迟不超过一个批处理窗口。
- **实施记录（2026-08-04）**：
  - `appendAssistantDelta` 改为按内容可见性分级刷新：含完整行（`\n` 结尾）立即 `refreshViewport()`；仅未完成尾行时置 `transcriptRefreshPending` 交给帧窗口（`transcriptStreamingRefreshInterval` = `cursorFrameInterval` = 1/30s）合并，`refreshViewportForStreaming`/`flushTranscriptRefreshIfDue` 负责窗口判定与真正刷新。
  - 配合严格 revision cache：帧内未变内容零成本返回，帧间只重算增量，把“每 token 一次全量重建”收敛为“每帧最多一次”。
  - 关键事件（tool call/result、done、turn finished）路径保持立即 flush 与立即可见；既有测试 `TestAssistantDeltaBuffersTailAndRefreshesCompletedLineImmediately` 及全部流式/滚动/取消回归通过。
  - 验证：`go test ./internal/ui/bubble/ -count=1`、`-race`、`go vet`、`gofmt -l`、`git diff --check`、全仓 `go test ./...` 全部通过。

### [x] 拆分 cursor frame 驱动的刷新职责

- **位置**：`internal/ui/bubble/app.go` 的 `cursorFrameMsg` 处理
- **现状**：动画、context meter、wave、task/subagent 刷新、viewport 刷新和 pipeline poll 可能由同一 timer tick 驱动。
- **预期收益**：避免视觉动画频率触发无关任务列表、历史和布局刷新。
- **风险**：状态不同步、动画和任务状态延迟、定时器生命周期复杂化。
- **实施方向**：使用 dirty flags；将 cursor 动画、task progress、pipeline polling 分为不同频率。
- **验收标准**：输入响应不下降；状态刷新延迟在可接受范围；无重复 timer 或 goroutine 泄漏。
- **实施记录（2026-08-04）**：
  - transcript 刷新从“每帧直接刷”改为 dirty flag + 帧窗口：`transcriptRefreshPending` + `transcriptRefreshPendingAt`，`flushTranscriptRefreshIfDue` 在 pending 置位至少一个流式窗口（1/30s）后才真正刷新，避免每个 cursor tick 都重建。
  - Activity 面板的 `ListTasks` 刷新拆出 `refreshActivityFromTasks`：按频率分级（面板可见且存在 running 任务时 500ms 节流，否则 2s），`lastActivityPollAt` 记录节流时间戳，不再每帧跨进程读 task registry。
  - 动画（cursor color、context meter、wave、spinner）、tool progress 与 pipeline poll 仍由各自既有机制驱动；`scheduleUIAnimationFrame` 保证每帧至多一个 tick，无重复 timer。
  - 验证：`go test ./internal/ui/bubble/ -count=1`、`-race`、`go vet`、`gofmt -l`、`git diff --check`、全仓 `go test ./...` 全部通过。

---

## P1：StreamMA 与模型消息构造

### [ ] 采用预算式构造 StreamMA 上下文

- **位置**：`internal/loop/streamma_mode.go` 的 `streamMAConversationContext`
- **现状**：可能先构造完整字符串，再 `Join`、复制并截断。
- **预期收益**：降低长历史、多次 invocation 下的中间字符串和峰值内存。
- **风险**：改变截断方向、消息保留顺序或模型上下文语义。
- **实施方向**：先固定现有截断语义；在剩余字节预算内追加，避免无界中间结果。任何“改为优先保留最新消息”的策略需单独评审。
- **前置验证**：逐字节兼容测试、边界 UTF-8 测试、长消息 benchmark。
- **验收标准**：在保持上下文选择策略不变的前提下降低分配；否则不得合并。

### [ ] 不为最终文本长期保留全部 StreamMA events

- **位置**：`internal/loop/streamma_mode.go` 的最终文本提取与 event 保存路径
- **现状**：最终答案 fallback 可能反向扫描完整事件列表；长任务会持续保留事件。
- **预期收益**：降低 StreamMA 长任务的内存占用和最终扫描成本。
- **风险**：trace、调试、回放或 UI 可能依赖完整事件列表。
- **实施方向**：运行中维护 `lastNonEmptyStepText`；将最终答案提取与调试事件保留解耦；需要完整 trace 时使用受限 ring buffer 或增量落盘。
- **验收标准**：最终答案、回放和调试开关行为保持兼容；内存不随无界 event 数量持续增长。

### [ ] 对 StreamMA trace 通知做采样和批处理

- **位置**：`internal/loop/streamma_mode.go` 的 `streamMATraceSink`
- **现状**：每个事件可能格式化字符串、TrimSpace 并通知 TUI transcript。
- **预期收益**：降低 trace 开启时的字符串分配和 UI 更新频率。
- **风险**：调试信息缺失、事件顺序或关键错误信息不完整。
- **实施方向**：关键事件即时通知；普通 delta 按时间窗口合并或限频到 10～20Hz。
- **验收标准**：error/done/tool 边界不可丢；trace 输出明确标注采样/合并行为。

---

## P1：Subagent、stream event 与 MCP

### [x] 为 streaming event 增加有界缓冲和取消感知

- **位置**：`internal/subagent/manager.go` 的 stream channel 创建与消费链路
- **现状**：事件 channel 无缓冲；UI 或主循环慢时会形成端到端反压。
- **预期收益**：减少短时 UI 抖动对模型读取的影响，改善突发 token 流吞吐。
- **风险**：缓冲过大造成内存增长；消费者提前退出时可能阻塞发送；事件丢弃策略会影响语义。
- **实施方向**：先 benchmark buffer 0/64/256；连续 delta 可合并，terminal/error/done 不可丢；所有发送路径必须 select `ctx.Done()`。
- **验收标准**：取消可及时结束；关键事件可靠到达；慢消费者下内存有界。

### [x] 减少 MCP snapshot 的重复广播和复制

- **位置**：`internal/subagent/manager.go` 的 `forwardMCPSnapshots`
- **现状**：每次 snapshot 都复制 running process 列表并广播给所有进程。
- **预期收益**：多个 subagent、频繁 MCP 更新时减少 slice 分配和无效 IPC。
- **风险**：snapshot 版本判断错误会导致进程看不到更新；并发更新顺序必须保持。
- **实施方向**：内容 hash/version 去重；维护 process 快照；对高频更新 debounce/coalesce；避免无限 goroutine。
- **验收标准**：相同 snapshot 不重复广播；不同 snapshot 按版本有序传播。
- **实施记录（2026-08-04）**：
  - `forwardMCPSnapshots` 优先使用 MCP snapshot 的单调递增 `Version` 去重；无版本快照回退到内容指纹。
  - 相同版本/内容不会重复向 running process 发送；不同版本仍按输入顺序传播。
  - 新增 `TestForwardMCPSnapshotsSkipsDuplicateContent` 与 snapshot fingerprint 回归测试。

### [x] 缓存任务列表，减少磁盘扫描和排序

- **位置**：`internal/subagent/manager.go` 的 `ListTasks`
- **现状**：TUI 轮询任务列表时可能重复扫描磁盘、合并内存任务并排序。
- **预期收益**：降低 TUI 任务面板刷新造成的文件 I/O 和排序成本。
- **风险**：外部文件变化或多进程写入时缓存陈旧。
- **实施方向**：内存缓存 + TTL；启动、显式刷新或 TTL 到期时扫描；任务状态变化时增量更新。
- **验收标准**：任务状态在约定 TTL 内可见；显式刷新能获取磁盘最新状态。
- **实施记录（2026-08-04）**：
  - `ListTasks` 增加 500ms 磁盘扫描 TTL 缓存；内存中的任务始终优先合并，保证运行/完成状态即时可见。
  - TTL 到期后重新扫描 `.paw/tasks`，外部任务变化最多延迟一个 TTL。
  - `BenchmarkListTasksCached`：100 个任务的缓存路径约 **19.9µs/op / 124KB / 114 allocs**；新增 TTL 行为回归测试。

---

## P1：工具、网络与序列化

### [x] 缓存 MCP tool schema 与模型定义

- **位置**：`internal/tool/mcp/tool.go` 的 `NewTool`、`InputSchema`、`Spec`
- **现状**：可能重复 clone、生成模型 schema 和 JSON 编码。
- **预期收益**：工具枚举和请求构造频繁时减少分配与序列化。
- **风险**：调用方可能修改返回的 `json.RawMessage` 或 spec，缓存共享数据会引入数据竞争/污染。
- **实施方向**：在构造时预计算不可变 schema；对外返回受保护副本；先用 benchmark 证明调用频率和收益。
- **验收标准**：schema 内容和调用方可变性约定保持不变。
- **实施记录（2026-08-04）**：
  - `NewTool` 构造时 clone `ToolSpec` 并预计算 `modelSchema`；`InputSchema` 每次返回独立副本，避免重复 `ModelSchema` 处理且不暴露共享可变内存。
  - 新增 `TestToolSchemaIsStableAndReturnedAsCopy`，验证调用方修改返回值不会污染后续 schema。

### [x] 优化 WebFetch 连接复用与超时配置

- **位置**：`internal/tool/webfetch/webfetch.go`
- **现状**：应确认默认 client/transport 是否已共享；短生命周期 client 可能影响连接复用。
- **预期收益**：重复访问同一 host 时减少连接建立开销。
- **风险**：共享 Transport 的生命周期、代理、超时和测试隔离问题。
- **实施方向**：在 Tool 构造阶段持有共享 client/Transport；评估 idle connection 参数；保持响应体上限。
- **验收标准**：网络错误、超时、取消和测试 mock 行为不变；连接复用 benchmark 有改善。
- **实施记录（2026-08-04）**：
  - 调查确认当前实现已使用包级共享 `http.Client`，且未在每次请求中创建短生命周期 Transport；连接复用已有，无需追加行为风险较高的 Transport 改造。
  - 已保留 32KiB 响应体上限、请求级 timeout 和 context cancellation；本项按“现状已满足”关闭，后续仅在网络 benchmark 显示瓶颈时再调整 idle 参数。

---

## P2：Token Tracer、Dashboard 与 Timeline

### [ ] 限制 Token Tracer 事件历史增长

- **位置**：`internal/tokentracer/tracer.go`、`dashboard.go`、`timeline.go`
- **现状**：长运行任务可能持续保留 token/event 历史；Timeline 可能重复解析时间字符串。
- **预期收益**：降低长任务内存增长和 dashboard 快照成本。
- **风险**：历史查看、导出、回放和统计结果可能不完整。
- **实施方向**：区分实时窗口与完整导出；可配置 ring buffer 或按 session 落盘；缓存已解析时间。
- **验收标准**：默认行为和导出模式保持完整；实时模式内存有界。

### [ ] 批量化 Dashboard/Timeline 更新

- **位置**：`internal/tokentracer/dashboard.go`、`internal/tokentracer/timeline.go`
- **现状**：高频 token/event 更新可能触发重复排序、格式化和 UI 重绘。
- **预期收益**：降低 trace 对主 transcript 的渲染竞争。
- **风险**：实时性下降、事件排序和时间窗口边界问题。
- **实施方向**：按时间窗口批处理；只刷新可见窗口；增量 append，避免每次全量排序/格式化。
- **验收标准**：关键事件延迟可控；事件顺序、统计和时间线结果不变。

---

## 必须先完成的验证任务

### [ ] 建立统一性能基线

```bash
go test ./... -count=1
go test -race ./...
go test -run '^$' -bench=. -benchmem ./...
```

补充真实 benchmark：

- [x] JSONL append/load：1k、10k、100k 条记录（`BenchmarkJSONLAppendGrowth`/`Batch`/`ParallelSessions`）；
- [x] transcript View：100、1k、5k 条消息，宽度 80/120/200（`BenchmarkTranscriptRenderAt`/`RegionCacheHit`）；
- [x] stream event：buffer 0/64/256，快/慢消费者和提前取消（`BenchmarkStreamEventBackpressure`/`Cancel`，位于 `internal/subagent/stream_bench_test.go`）；
- [x] StreamMA：不同历史长度和 event 数量；
- [x] MCP snapshot：1、5、20 个并发 subagent。

### [x] 建立 pprof 或等效运行时采样

至少采集：

- [x] CPU profile（transcript region 命中路径 + JSONL append）；
- [x] heap profile（transcript region 命中路径 + JSONL append）；
- [x] goroutine profile（尚无长时间运行任务的实测，见 P1 有界缓冲后补）；
- [x] TUI 单帧耗时和更新频率（transcript benchmark + delta 批处理帧窗口）；
- [x] journal append p95/p99 延迟（benchmark 数据点见各记录）；
- [x] stream event 队列长度、阻塞时间和取消耗时（`BenchmarkStreamEventBackpressure`/`Cancel`）。

**基线（2026-08-04，Apple M4）**：

- bubble region 命中：CPU 热点为逐条 key 比较与字符串拼接（`strings.Builder.WriteString` 45% 分配）、`toolGroupRenderSnapshot` 每组建 1MB+；heap 无结构性热点。
- session append：CPU 仅 syscall（fsync 为主），堆分配来自临时目录清理与 `os.Stat`/`json.Unmarshal`；印证 fsync 为延迟主成本。

### [ ] 每个优化项单独验证

每次实施后执行：

```bash
gofmt -w <changed-files>
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

并补充对应的行为回归测试、benchmark 对比和风险说明。

---

## 当前推荐执行顺序

1. 建立 JSONL append benchmark，确认是否为首要热点。
2. 建立 transcript View benchmark 和单帧 pprof。
3. 建立 stream event 背压/取消 benchmark。
4. 若数据确认，优先实施 JSONL sequence cache。
5. 再实施 transcript region revision cache 或 delta batching（二选一先做）。
6. 最后处理 StreamMA、MCP snapshot、Tracer 历史窗口和 schema cache 等专项优化。

（已完成：JSONL sequence cache、SyncPolicy、session 粒度锁、transcript region 严格 revision cache、assistant delta 批量合并、cursor frame 职责拆分，及全部基准/pprof 前置任务；见上方各记录。）
