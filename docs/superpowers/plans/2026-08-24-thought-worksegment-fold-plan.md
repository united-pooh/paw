# Thought 工作段折叠实现计划

日期：2026-08-24
设计文档：`docs/superpowers/specs/2026-08-24-thought-worksegment-fold-design.md`
范围：`internal/ui/bubble`
说明：`writing-plans` 技能未安装，本计划按其方法论（小步任务、测试先行、每步可验证）手工编写。

## 架构决策（实现前必读）

渲染管线现状：`renderTranscriptEntriesFrom`（transcript.go:1433）直接遍历 `m.transcript`，span/缓存/选择坐标全部按 transcript 下标索引。为不碰工具追踪系统（`recordToolCallEntry`、`rebuildToolRuntimeIndex`、tool inspect 按 toolUseID 查原始条目），采用**派生视图**：

- `m.transcript`：原始条目，永远不变（所有现有逻辑不动）；
- `m.viewEntries`：`= foldWorkSegments(m.transcript, mode)`，transcript 每次变更后重算（条目量级小，先全量重算，不设缓存）；
- 渲染、spans、选择/点击坐标一律基于 `m.viewEntries`；
- mode 选择：回合进行中（`m.running`）→ `foldLive`，其余（含 resume）→ `foldResting`。

## Task 1：`entryWorkSegment` 类型 + `foldWorkSegments` 纯函数（resting 规则）

**文件**：
- 新建 `internal/ui/bubble/transcript_worksegment.go`
- 新建 `internal/ui/bubble/transcript_worksegment_test.go`
- 修改 `internal/ui/bubble/types.go`

**步骤**：

1. `types.go`：`entryKind` 增加 `entryWorkSegment`；`transcriptEntry` 增加 `segment *workSegmentData` 指针字段（避免膨胀主结构）：

```go
type workSegmentData struct {
    children      []transcriptEntry // 收编的原始 reasoning/tool 条目（视图引用）
    toolCalls     int               // 不含 update_todo
    failed        int
    hasReasoning  bool
    startedAt     time.Time
    finishedAt    time.Time
    expanded      bool
}
```

2. 写失败测试（表驱动，覆盖 spec 测试矩阵）：
   - reasoning+tools 连续 → 一段，统计正确（toolCalls/failed/hasReasoning/时长）；
   - `entryAssistant`/`entryUser`/`entrySystem` 切段；
   - `update_todo` 跳过且不切段（仅含它的运行不产生块）；
   - question/select 工具不收编且切段；
   - 段尾裸工具运行并入前一个含 reasoning 的段；完全无 reasoning → `Worked` 命名语义（hasReasoning=false）；
   - 空段抑制；
   - `foldLive`：仅尾部段标记 live，非尾部段原样保留。

3. 实现 `foldWorkSegments(entries []transcriptEntry, mode foldMode) []transcriptEntry`：
   - 单次扫描，收编 `entryReasoning`/`entryThinking`/`entryTool` 连续运行；
   - 簿记判定复用现有 `strings.EqualFold(name, "update_todo")`（tool_display.go 同款）；交互类名单：`question`、`select`；
   - `formatSegmentDuration(d)`：`1 s` 起步，`59 s` / `2m 15s` / `1h 3m`。

4. 验证：`go test ./internal/ui/bubble/ -run TestFoldWorkSegments -v`

## Task 2：resting 渲染（标题 + 展开）

**文件**：`internal/ui/bubble/transcript_worksegment.go`、`internal/ui/bubble/transcript.go`（separator 规则）、测试同 Task 1 文件

**步骤**：

1. 失败测试：
   - 三种标题：`Thought for 2m 15s · 15 tool calls · 1 failed` / `Thought for 12 s` / `Worked for 48 s · 5 tool calls`；
   - `M=0` 不显示计数部分；`N>0` 才显示 `· N failed`（中性色，不用 error 样式）；
   - 展开态 = 标题 + children 逐条走 `renderEntryAt`，子条目平铺（不嵌套 Tools 组折叠）。

2. 实现：
   - `renderWorkSegmentEntry(entry, width, at, showThinking) string`；
   - `transcriptEntrySeparator` 增加 `entryWorkSegment` 的分隔规则（与 reasoning 条目一致）；
   - `renderEntryAt`/`renderTranscriptEntryBody` 支持新 kind。

3. 验证：`go test ./internal/ui/bubble/ -run 'TestWorkSegment' -v`

## Task 3：管线接入 `m.viewEntries`

**文件**：`internal/ui/bubble/types.go`（appModel 字段）、`internal/ui/bubble/transcript.go`、`internal/ui/bubble/app.go`

**步骤**：

1. `appModel` 增加 `viewEntries []transcriptEntry`；新增 `m.recomputeViewEntries()`：按 `m.running` 选 mode 调 `foldWorkSegments`。
2. 在 transcript 变更收口处调用：`addEntry`、`replaceTranscript`、`batchTranscriptRefresh` 回调、`doneMsg` 分支（mode 随 m.running 翻转，foldLive→foldResting 在此切换）。
3. `renderTranscriptEntriesFrom` 及 `m.transcriptEntrySpans` 改用 `m.viewEntries`；`transcriptEntryRenderable` 支持新 kind。
4. 失败测试：构造含 reasoning+tools 的 model，`doneMsg` 后 viewport 只渲染一行 `Thought for …`；span 行区间正确。
5. 验证：`go test ./internal/ui/bubble/ -run 'TestWorkSegment|TestViewport' -v`；随后全量 `go test ./internal/ui/bubble/` 检查选择/坐标类回归。

## Task 4：交互（单击展开/折叠、Ctrl+O 联动）

**文件**：`internal/ui/bubble/transcript_interaction.go`、`internal/ui/bubble/app.go`、`internal/ui/bubble/commands.go`

**步骤**：

1. 点击映射：span → viewEntries 下标 → 若为 `entryWorkSegment`，切换 `segment.expanded` 并重算视图（复用 Tools 组展开的 viewport 重建路径）。
2. Ctrl+O（showThinking）联动：全局展开时所有段 expanded=true（等价于 `reasoningExpansionSet` 语义）。
3. 展开段内 tool 行的 inspect/diff：children 复用现有渲染；行级点击在 v1 仅支持段标题切换，段内 tool 行点击走既有 toolUseID 查找（若映射复杂则退化为不可点击，记录在测试注释中）。
4. 失败测试：单击标题展开/折回；Ctrl+O 全展开；展开后行数与坐标重建正确。
5. 验证：`go test ./internal/ui/bubble/ -run 'TestWorkSegmentExpand|TestCtrlO' -v`

## Task 5：live 块（12 行滚动窗口 + 实时标题）

**文件**：`internal/ui/bubble/transcript_worksegment.go`、`internal/ui/bubble/transcript.go`、`internal/ui/bubble/app.go`（tick 刷新）

**步骤**：

1. 失败测试：
   - live 标题：`● Thoughts · 1m 23s · 7 tool calls`（计时用 `animationNow`，随 tick 更新）；
   - 窗口截尾：段内容 >12 行时只渲染最后 12 行；窗口内工具行强制单行 compact；
   - live 窗口内失败行保持 error 样式；
   - `doneMsg` 后 live 块消失、resting 块落位。
2. 实现：
   - `renderWorkSegmentLive(entry, width, at) string`：标题 + 尾 12 行（children → 现有 compact 渲染 → 行切片取尾）；
   - 窗口内 reasoning 即时渲染（不经 `releaseStreamCharacters`）；
   - 确认 tick 链（`tickMsg`/`animationNow`）在 live 期间驱动标题重渲染。
3. 验证：`go test ./internal/ui/bubble/ -run 'TestWorkSegmentLive' -v`

## Task 6：resume 接入 + 全量回归 + smoke

**文件**：`internal/ui/bubble/session_picker.go`（视接线情况）、测试

**步骤**：

1. resume 路径（`applySessionPickerRestore`、task 预览）的 transcript 同样经 `recomputeViewEntries`（foldResting）；孤儿 running→error 事务计入 failed（在 `finalizeRestoredRunningTools` 之后重算）。
2. 失败测试：含 reasoning+tools+中断工具的历史 → restore 后渲染折叠块，`· 1 failed`。
3. 全量回归：`go test ./... -timeout 300s`，`gofmt -l internal/` 干净。
4. 手工 smoke：`go run ./cmd/agent` 跑一个含多次工具调用的会话，观察：回合中 live 块 12 行窗口滚动、结束自动折叠、单击展开、`/resume` 后历史一致。

## 风险与回退

- **span/选择坐标回归**（最高风险）：Task 3 后必须全量跑 bubble 包；若出现不可控回归，回退策略为保留 Task 1-2（纯函数+渲染），管线切换用 feature flag（环境变量 `PAW_WORKSEGMENT=0` 时 `recomputeViewEntries` 直接返回 `m.transcript`）。
- **live 窗口性能**：12 行截尾本身限定了渲染成本；tick 刷新只重渲染 live 块（render cache key 含 at 的秒级截断）。
- **与 Tools 组并存**：被收编条目不再经 Tools 组渲染（foldWorkSegments 收编后 transcript 中不存在连续 tool 运行），未收编条目行为不变。
