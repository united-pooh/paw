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

状态机阶段（按顺序）：

| 阶段 | artifact 文件 | 说明 |
|---|---|---|
| Brainstorming | `design.md` | 设计稿已生成 |
| Spec | `spec.json` | Spec 已通过 |
| Plan | `plan.json` | Plan 已生成 |
| Architecture | `architecture.json` | 架构已确定 |
| Execution | `execution-report.json` | 最新一次执行报告 |
| Validation | `validation-report.json` | 最新一次验证报告 |
| Review | `review_feedback.json` | 最新一次评审结果 |
| Doc | `doc-report.json` | 文档已更新 |

**阶段状态判断**：
- **done**（绿点）：对应 artifact 文件存在，且若为 execution/review 则状态字段为通过
- **active**（蓝色发光点）：当前最新一步，artifact 文件刚刚写入（mtime 最近）
- **retry**（橙点）：execution-report 的 `iteration` > 1 且 validation/review 状态为 fail
- **pending**（暗点）：artifact 尚未生成

**迭代计数**：读取 `execution-report.json` 中的 `iteration` 字段，在 Execution/Validation/Review 阶段后显示 `×N`（retry 时附加 `↻`）。

**全局迭代轮次**：badge 上显示 `pipeline · iter N`，来自 `execution-report.json.iteration`。

状态数据存储在 `appModel.pipelineState`（新增结构体），由后台 `tea.Cmd` 定期轮询 `.pipeline-workspace/` 更新。

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
type pipelinePhase int
const (
    phaseNone pipelinePhase = iota  // 未检测到 pipeline
    phaseBrainstorming
    phaseSpec
    phasePlan
    phaseArchitecture
    phaseExecution
    phaseValidation
    phaseReview
    phaseDoc
    phaseDone
)

type phaseStatus int
const (
    statusPending phaseStatus = iota
    statusActive
    statusDone
    statusRetry
)

type pipelinePhaseInfo struct {
    phase     pipelinePhase
    status    phaseStatus
    iteration int
}

type pipelineState struct {
    detected    bool
    iteration   int  // global, from execution-report.json
    phases      []pipelinePhaseInfo
    lastChecked time.Time
}

// appModel 新增字段（右侧面板无独立 viewport，每帧直接渲染）
pipelineState  pipelineState
```

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
# 6. 无 pipeline 时显示 tasks
```
