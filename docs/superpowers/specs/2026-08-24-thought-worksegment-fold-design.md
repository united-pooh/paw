# Thought 工作段折叠设计（Work Segment Folding）

日期：2026-08-24
状态：已评审（设计确认于对话内）
范围：`internal/ui/bubble`（TUI transcript 呈现层）

## 背景与问题

paw 的 transcript 目前有两套并存的过程呈现：

1. **Reasoning**：流式时实时显示思考文本，结束后折叠为一行 `Thought for N s`（`renderReasoningBody`，transcript.go:2647）；
2. **工具调用**：call+result 合并为事务条目，连续工具条目组成可折叠的 Tools 组（`mergeTranscriptToolEntries`）。

问题：

- 长任务中 transcript 被大量工具行占据，"用户 ↔ 结论"的对话叙事被过程噪音淹没；
- `Thought for N s` 只计思考时长，不反映工具执行的墙钟时间；
- reasoning 与 tools 是两套独立的折叠/展开交互，语义不一致；
- 与 auto-continue 无感化方向矛盾：续行回合的过程行让"无感"破功。

## 目标

- 回合结束后，transcript 还原为干净对话：可见正文 + 每段一行的工作摘要块；
- 回合进行中，有一个始终可见的 live 工作块（12 行滚动窗口）承载全部过程输出；
- 失败呈现克制：单个工具失败不整行变红，仅在标题追加中性计数；
- 实时与 resume 历史渲染形态一致。

## 非目标

- 不改事件流（`toolCallMsg`/`toolResultMsg`/reasoning/assistant delta）与数据模型；
- 不改工具权限/交互工具（question、select、permission）的呈现与交互；
- 不做 live 块的吸底悬浮（翻历史时块随内容滚走，不钉在底部）；
- 不做窗口内手动滚动（事后展开看全量）；
- 不改 `update_todo` 工具本身的 compact 显示规则。

## 核心模型：WorkSegment 的三态生命周期

**WorkSegment（工作段）** = 回合内"可见正文（`entryAssistant`）之间"的 reasoning + 工具事务集合。数据源始终是原始 transcript entries；折叠只发生在"entries → 视图"一步（视图层分组，方案 A）。

### 形态 1 · Live（回合进行中）

- 回合开始后 transcript 尾部出现**唯一一个** live 块，标题实时更新：

  ```text
  ● Thoughts · 1m 23s · 7 tool calls
  ```

- 标题下方为 **12 行滚动窗口**：reasoning 流（暗色）与工具事务行（compact 单行）按时间序灌入，超过 12 行自动滚动，始终显示最新 12 行；
- 窗口内条目只读（不可 inspect、不走打字机动画）；
- 可见正文照常作为 transcript 条目渲染在原位；live 块始终位于最新位置；
- 工具失败行在窗口内照常显示红色 `!`（实时信号不隐藏），块标题不整行变红；
- 它是 transcript 的实时尾部：跟随模式下始终可见；用户上翻历史时随内容滚走（非吸底）。

### 形态 2 · Resting（回合结束）

`doneMsg` 到达时 live 块与窗口消失，按"正文切段"规则落成若干折叠一行块，每段各自统计，标题自适应命名：

```text
Thought for 2m 15s · 15 tool calls · 1 failed     ← 含 reasoning 与工具的段
Thought for 12 s                                   ← 纯 reasoning 段
Worked for 48 s · 5 tool calls                     ← 纯工具段
```

规则：

- `update_todo`（簿记）不计入统计；
- 失败仅追加中性 `· N failed`，标题不变色；
- ESC 中断的回合同样折叠，中断工具（error 态）计入 failed。

### 形态 3 · Expanded（事后展开）

单击 resting 块（或 Ctrl+O 全局展开）→ 内联展开全部子条目（reasoning 正文 + tool 行，复用现有渲染 / inspect / diff / 选择），**无** 12 行窗口。再次单击折回。

## 组件

### 1. `foldWorkSegments` 纯函数（唯一分组规则来源）

新文件 `internal/ui/bubble/transcript_worksegment.go`（transcript.go 已超 2700 行，按单一职责拆分）：

```go
type foldMode int

const (
    foldLive    foldMode = iota // 回合进行中：仅尾部段渲染为 live 块；非尾部段保持原始条目不折叠（回合中不折旧段）
    foldResting                 // 回合结束 / 历史渲染：全部段折叠为一行块
)

func foldWorkSegments(entries []transcriptEntry, mode foldMode) []transcriptEntry
```

分组规则：

- 扫描 entries，将 `entryReasoning` / `entryThinking` / `entryTool` 的连续运行收编为段；
- `entryAssistant` / `entryUser` / `entrySystem` 立即切段（原位可见）；
- 两类工具不收编，且处理不对称：
  - `update_todo`（簿记）：不进段、**不切段**（频率高，切段会把所有段切碎）；
  - 交互/权限类（question、select、permission）：不进段、**且切段**（需用户输入，前后工具不并入同段）；
- 裸工具段归并：段尾没有新 reasoning 的连续工具运行并入前一个含 reasoning 的段；仅当段内完全无 reasoning 时命名为 `Worked for …`；
- 空段抑制：无任何条目、或仅含 `update_todo` 的段不产生块；
- 段统计：工具事务数（不含簿记）、failed 数、段墙钟时长（段首 `createdAt` → 段尾；live 用 `animationNow`）、是否含 reasoning。

### 2. `entryWorkSegment` 视图条目（新 entry kind）

持有：子条目切片（原始 reasoning/tool entries 的视图引用）、聚合统计、自适应命名、展开状态（`segmentExpanded`）。

- Resting 折叠态：一行标题（时长 + 计数 + 中性 failed）；
- Expanded 态：标题 + 子条目依次走现有 `renderTranscriptEntryBody`；**子条目平铺渲染，不再嵌套 Tools 组折叠**（避免两层折叠）；
- Live 态（仅 foldLive 且位于 transcript 尾部）：标题行（`● Thoughts · 1m 23s · 7 tool calls`，随现有 tick 动画刷新）+ 12 行窗口（子条目 compact 渲染取尾 12 行）。

### 3. 渲染管线接线

- viewport 构建入口处统一调用 `foldWorkSegments`：回合中 `foldLive`，`doneMsg` 后 `foldResting`；
- resume 路径（`transcriptEntriesFromMessage/Records` → `replaceTranscript`）：先过现有 `mergeTranscriptToolEntries`（事务化、孤儿 running 收尾 error），再过 `foldWorkSegments(foldResting)`；孤儿 error 事务计入 failed；
- 行数变化引发的 viewport / 选择坐标重建，复用现有 Tools 组展开的机制（`rebuildToolRuntimeIndex` / `refreshViewport`）；
- Ctrl+O（showThinking）联动展开所有 resting 段；单击切换单段。

### 一回合数据流时序

```text
turn start
  │  reasoning/tool/text 事件 → 原始 entries（不变）
  │  每次变更 → foldWorkSegments(foldLive)
  │  → viewport: [正文条目…] + [● Thoughts 块(12行窗口)]
doneMsg
  → foldWorkSegments(foldResting)
  → viewport: [正文] [Thought for 12 s] [正文] [Thought for 2m 15s · 15 tool calls · 1 failed]
```

## 标题格式与口径

- 时长：段墙钟时间，最小 `1 s`，格式 `12 s` / `2m 15s` / `1h 3m`；
- 计数：`· M tool calls`（不含 `update_todo`；M=0 时不显示该部分）；
- 失败：`· N failed`（N>0 时追加，中性色）；
- 命名：含 reasoning → `Thought for …`；纯工具 → `Worked for …`；
- live 标题：`● Thoughts · <elapsed> · <M> tool calls`（含 reasoning 与否统一用 Thoughts；失败行只在窗口内呈现，不进 live 标题）。

## 边界情况

| 情况 | 规则 |
| --- | --- |
| 直接回答（无 reasoning 无工具） | 不产生块 |
| 仅含 `update_todo` 的运行 | 不产生块 |
| 权限/交互工具 | 不收编且切段，原位渲染，保持现有交互 |
| ESC 中断 | 走 resting 折叠，中断工具计 failed |
| resume 孤儿工具（running→error） | 计入 failed |
| StreamMA / goal / plan | entries 类型相同，规则一致适用；其系统条目正常切段 |
| 未收编条目（簿记/交互类） | 维持现有渲染行为不变 |

## 测试策略

- **纯函数表驱动**（`foldWorkSegments`）：基本收编与统计；正文/系统条目切段；`update_todo` 跳过不切段；交互类切段；裸工具段归并与 `Worked` 命名；failed 计数与中性文案；时长格式化；空段抑制；foldLive 仅尾部段为 live。
- **渲染**：三种自适应标题；12 行窗口截尾（>12 行只显示最后 12 行）；窗口内工具行单行化；live 标题实时计数。
- **交互**：单击展开/折叠；Ctrl+O 全局展开；展开后 tool inspect/diff 可用；折叠切换后 viewport 与选择坐标正确重建。
- **resume**：历史 → resting 折叠（沿用现有 session restore 测试模式）。
- **回归**：现有 reasoning / Tools 组 / restore 测试保持绿（未收编条目行为不变）。

## 已知取舍

- live 窗口内 reasoning 即时渲染，不走流式打字机效果（`releaseStreamCharacters` 仅作用于正文条目），换滚动流畅；
- live 块不吸底：跟随模式下可见，上翻历史时随内容滚走；
- 被收编 tool 条目展开时平铺，不再嵌套 Tools 组折叠。
