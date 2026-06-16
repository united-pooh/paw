# TUI 布局重设计 + 工具调用美化

**Date:** 2026-06-16  
**Status:** Approved

---

## 目标

将当前单列 TUI 改为 70/30 水平分栏，左侧保持对话流，右侧新增信息面板；同时：
- 工具调用改用 blockquote `>` 样式渲染
- Context meter 从输入框上方移入右侧 Context 卡片
- 右侧顶部卡片根据是否运行 pipeline 自动切换显示 Pipeline 阶段状态机 或 Tasks 列表

---

## 布局结构

```
┌──────────────────────────────────────────────────────┐
│   left (70%)               │   right (30%)           │
│  ┌──────────────────────┐  │  ┌───────────────────┐  │
│  │  Transcript          │  │  │  Pipeline / Tasks  │  │  ← fill
│  │  (viewport)          │  │  └───────────────────┘  │
│  │                      │  │  ┌───────────────────┐  │
│  │                      │  │  │  Subagents         │  │  ← auto
│  └──────────────────────┘  │  └───────────────────┘  │
│  ┌──────────────────────┐  │  ┌───────────────────┐  │
│  │  Input box           │  │  │  Context           │  │  ← auto
│  └──────────────────────┘  │  └───────────────────┘  │
└──────────────────────────────────────────────────────┘
```

- 左右之间**无分割线**，靠各自卡片的 border + 背景色自然区分
- 右侧三个卡片各有独立圆角 + 卡片间 `gap`
- Pipeline/Tasks 卡片 `flex:1` 填满剩余高度，内容 `justify-content: space-evenly`
- Subagents + Context 卡片自适应内容高度

---

## Left Panel（70%）

### Transcript

当前 `viewport.Model` 保持，仅宽度按 70% 计算。

### 工具调用 blockquote 样式

`transcript.go` 中 `entryTool` 的渲染从当前 `▾ tool: name ...` 改为 blockquote 样式：

**工具调用**（`title == "tool"`）：
```
│ ▾ tool: Bash           ← 橙色 border-left + 橙色 header
│   > command: go test   ← 灰色参数行，以 > 开头缩进
```
- 竖线颜色：`colorLabelTool`（214，橙色）
- 参数展开：保留现有 650ms 动画，改 `│` → `>` 前缀
- 背景：比 transcript 略深的色块

**工具结果**（`title == "result"`，无错误）：
```
│ ▾ result: ok · 2.1KB   ← 绿色 border-left
│   > package main...    ← 灰绿色内容行
```
- 竖线颜色：`colorLabelSystem` 或新增 `colorToolResult`（绿色系，接近 `#4a9a68`）

**工具结果**（`title == "result"`，有错误）：
```
│ ▾ result: error         ← 红色 border-left
│   > FAIL: ...           ← 暗红色内容行
```
- 竖线颜色：`colorLabelError`（203，红色）

### 输入框上方

**完全移除** `renderInputAboveMeter()` 函数及其在 `View()` 中的调用；`relayout()` 里 `inputAboveHeight` 固定为 0（不再调用该函数计算高度）。输入框直接贴近 transcript 底部，上方无任何内容。

---

## Right Panel（30%）

### 卡片 1：Pipeline 状态机 / Tasks（二选一，fill height）

#### 切换条件

通过检测 `.pipeline-workspace/` 目录是否存在且含有 `spec.json`（表示 pipeline 已初始化）来决定显示哪个卡片。检测在每个 `cursorFrameMsg` 时异步进行（轮询间隔约 500ms，避免频繁 stat）。

#### Pipeline 状态机

基于 [multi-agent-pipeline skill](https://github.com/united-pooh/MultiAgent-Pipeline-skills/tree/main/skills/multi-agent-pipeline) 的完整 18 阶段流程：

| # | 阶段 | artifact 文件（`.pipeline-workspace/`） |
|---|---|---|
| 0 | Brainstorming | `design.md` |
| 1 | Spec | `spec.json` |
| 2 | Plan | `plan.json` |
| 3 | Architecture | `architecture.json` |
| 4 | Dispatch | `dispatch.json` |
| 5 | Execution | `execution-report.json` |
| 6 | Complexity Hook | `execution-report.json`（同 Execution，由 mtime 区分） |
| 7 | Merge | `merge-report.json` |
| 8 | Validation | `validation-report.json` |
| 9 | Tree Classification | `tree-classification.json` |
| 10 | Rubric Gen | `tree-rubrics.json` |
| 11 | Rubric Verify | `tree-rubric-verification.json` |
| 12 | Rubric Refine | `tree-rubrics-refined.json` |
| 13 | Tree Grading | `tree-grading-individual.json` |
| 14 | QA | `qa-report.json` |
| 15 | Documentation | `doc-report.json` |
| 16 | Final Assessment | `final-assessment.json` |
| 17 | Cleanup | `.pipeline-last-run-summary.json`（run root） |

**阶段状态判断**（每 500ms 轮询 `.pipeline-workspace/`）：
- **done**（绿点）：artifact 文件存在
- **active**（蓝色发光点）：最新写入的 artifact（mtime 最近且 > 上次检测时刻）
- **retry**（橙点）：`execution-report.json` 的 `iteration` > 1，且其后续 artifact 不存在
- **pending**（暗点）：artifact 尚未生成

**Complexity Hook** 无独立 artifact，通过 `execution-report.json` 的 mtime 与 `merge-report.json` 是否存在来推断。

#### 滚动窗口显示（方案 C）

```
● pipeline · iter 3     9/18  ← badge + 全局计数
● ● ● ● ● ○ ○ ○ ▶ ○ ○ ○ ○ ○ ○ ○ ○ ○   ← 18 小圆点总览（done/retry/active/pending）

  Execution            ← 前 3 邻居（渐淡 opacity）
  Complexity Hook
  Merge
▶ Validation ×3        ← 当前阶段（高亮框，蓝色边框）
  Tree Classification  ← 后 3 邻居（渐淡 opacity）
  Rubric Gen
  Rubric Verify
```

- 顶部 18 点作为紧凑总览，颜色同状态圆点
- 滚动窗口展示当前 ±3 相邻阶段（前 3 渐淡，后 3 渐淡）
- 当前阶段用独立高亮框（`#101520` 背景 + 蓝色边框）
- 迭代计数显示在当前阶段名右侧（`×N`，retry 时橙色 `×N ↻`）
- `space-evenly` 垂直分布 7 行（3 前 + 当前 + 3 后）；头尾阶段时自动截断

**全局计数**：badge 右侧显示 `N/18`，来自已存在 artifact 数量。

#### Tasks 列表

无 pipeline 时，从 `appModel` 内部的 `TaskList`（现有 task 系统）读取任务：
- `in_progress` → active 状态（蓝方块，文字白色）
- `completed` → done 状态（绿方块，删除线）
- `pending` → pending 状态（暗方块，灰色）

### 卡片 2：Subagents（auto height）

从现有 `m.subagents.ListTasks()` 读取，显示每个 subagent 的状态：
- 绿点发光 = running
- 暗点 = done
- 红点 = fail/error

### 卡片 3：Context（auto height）

原 context meter 内容重新排列：

```
14.2k ↓                    free 72%    ← token 数 + 箭头左，free 右
████████░░░░░░░░░░░░░░░           ← 进度条（含 cache 层，高 5px）
28%        cache 6%       free 72%    ← 百分比行
                                turns 7    ← turns 右对齐
```

**移除**：session、limit 字段  
**保留**：token 数（`formatCompactTokenCount`）、方向箭头（`isGenerating` 控制 ↑↓）、进度条、used%、cache%、free%、turns

`ContextStats` 中 `UsedTokens`、`CacheTokens`、`LimitTokens` 直接用，计算同原 `contextMeterLine`。

---

## appModel 变更

```go
// types.go 新增

// pipelinePhaseStatus 每个阶段的显示状态
type pipelinePhaseStatus int
const (
    phaseStatusPending pipelinePhaseStatus = iota
    phaseStatusDone
    phaseStatusActive
    phaseStatusRetry
)

// pipelinePhaseEntry 单个阶段的状态快照
type pipelinePhaseEntry struct {
    name      string              // 显示名称
    artifact  string              // 对应的 artifact 文件名（相对 .pipeline-workspace/）
    status    pipelinePhaseStatus
    iteration int                 // 仅对 Execution/Validation 有意义
}

// pipelineState 完整的 pipeline 状态快照
type pipelineState struct {
    detected    bool               // .pipeline-workspace/spec.json 是否存在
    activeIdx   int                // 当前 active 阶段在 phases 中的索引（-1 = none）
    globalIter  int                // 全局迭代轮次（来自 execution-report.json.iteration）
    doneCount   int                // 已完成阶段数（用于 N/18 显示）
    phases      [18]pipelinePhaseEntry
    lastChecked time.Time
}

// appModel 新增字段（右侧面板无独立 viewport，每帧直接渲染）
pipelineState  pipelineState
```

18 个阶段的 `name` / `artifact` 映射见 Pipeline 状态机阶段表。

---

## 文件变更清单

| 文件 | 变更类型 | 内容 |
|---|---|---|
| `internal/ui/bubble/types.go` | 修改 | 新增 pipelineState 相关类型；appModel 加 pipelineState 字段 |
| `internal/ui/bubble/layout.go` | 修改 | View() 改为 JoinHorizontal；relayout() 计算 70/30 宽度；移除 renderInputAboveMeter 对 context meter 的渲染；新增 renderRightPanel() |
| `internal/ui/bubble/right_panel.go` | 新建 | renderRightPanel、renderPipelineCard、renderTasksCard、renderSubagentsCard、renderContextCard |
| `internal/ui/bubble/transcript.go` | 修改 | entryTool 渲染改为 blockquote 样式（renderToolBlockquote）；保留展开动画 |
| `internal/ui/bubble/styles.go` | 修改 | 新增 toolBlockquoteStyle、toolResultStyle、toolErrorStyle、rightCardStyle |
| `internal/ui/bubble/app.go` | 修改 | 新增 pipelinePollCmd（定期检测 .pipeline-workspace/）；处理 pipelineStateUpdatedMsg |
| `internal/ui/bubble/context_meter.go` | 修改 | 新增 renderContextCardContent(width int) string 供右侧面板调用；移除 contextMeterTitle 对外暴露的旧接口 |
| `internal/ui/bubble/bubble_test.go` | 修改+新增 | 更新宽度计算相关测试；新增右侧面板渲染测试 |

---

## 验证

```bash
# 编译
go build ./...

# 测试
go test -count=1 ./internal/ui/bubble/...

# 手动验证
go run ./cmd/agent
# 1. 验证左右分栏出现，无分割线
# 2. 工具调用显示 blockquote 样式（橙/绿/红竖线）
# 3. 输入框上方无 context meter
# 4. 右侧 Context 卡片显示 token 数 + bar + free%
# 5. 创建 .pipeline-workspace/spec.json 后，右侧顶部切换为 pipeline 视图
# 6. 依次写入 plan.json、architecture.json、dispatch.json 等，验证圆点随之更新
# 7. 写入 execution-report.json（iteration>1），验证 retry 橙点 + ↻
# 8. 无 pipeline 时（删除 .pipeline-workspace/）显示 tasks
```
