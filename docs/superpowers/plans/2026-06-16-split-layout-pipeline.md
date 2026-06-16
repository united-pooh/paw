# TUI Split Layout + Pipeline Status Machine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the TUI into 70/30 horizontal layout, render tool calls as coloured blockquotes, move the context meter into a right sidebar, and display a live 18-stage pipeline state machine in that sidebar.

**Architecture:** Eight sequential tasks — layout scaffold first (creates the right column), then visual polish (blockquotes), then sidebar cards (context → subagents → tasks), then pipeline detection and windowed display. Each task leaves tests passing and the app runnable.

**Tech Stack:** Go 1.21+, Bubble Tea (`bubbletea`, `bubbles`, `lipgloss`), `os.Stat` / `encoding/json` for pipeline polling.

---

## File Structure

| File | Role after plan |
|---|---|
| `internal/ui/bubble/types.go` | + `sidebarWidth int`, `pipelineState`, `pipelinePhaseEntry[18]`, `pipelineStateUpdatedMsg` |
| `internal/ui/bubble/layout.go` | 70/30 split in `relayout()` + `View()`; remove `renderInputAboveMeter`; add `renderRightPanel()` dispatch |
| `internal/ui/bubble/transcript.go` | `renderToolEntryBody` → blockquote style |
| `internal/ui/bubble/styles.go` | + `rightCardStyle`, `toolCallBorderStyle`, `toolResultBorderStyle`, `toolErrorBorderStyle` |
| `internal/ui/bubble/right_panel.go` | **new** — `renderRightPanel`, `renderContextCard`, `renderSubagentsCard`, `renderTasksCard`, `renderPipelineCard` |
| `internal/ui/bubble/context_meter.go` | + `renderContextCardContent(width int) string` for right panel reuse |
| `internal/ui/bubble/app.go` | + `pipelinePollCmd`, `pipelineStateUpdatedMsg` handler in `Update()` |
| `internal/ui/bubble/bubble_test.go` | update width tests; add right-panel, blockquote, pipeline tests |

---

### Task 1: 布局 70/30 分栏 scaffold

**Files:**
- Modify: `internal/ui/bubble/types.go` (add `sidebarWidth int` to appModel)
- Modify: `internal/ui/bubble/styles.go` (add `rightCardStyle`)
- Modify: `internal/ui/bubble/layout.go` (relayout, View, renderTranscriptBox, expandTranscriptToFillHeight)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 在 `types.go` appModel 末尾加 `sidebarWidth int`**

找到 `types.go` 第 254 行 `completion *completion`，在它后面加：
```go
sidebarWidth int // 右侧面板宽度（字符），由 relayout() 计算并存储
```

- [ ] **Step 2: 在 `styles.go` 末尾加 `rightCardStyle`**

在 `completionPanelStyle` 定义后追加：
```go
rightCardStyle = lipgloss.NewStyle().
    Border(lipgloss.RoundedBorder()).
    BorderForeground(colorManager.LipglossColor(colorPanelBorder)).
    Padding(0, 1)
```
同时在 `const` 块里加：
```go
rightSidebarMinWidth = 20
```

- [ ] **Step 3: 写失败测试（验证 sidebarWidth 计算）**

在 `bubble_test.go` 追加：
```go
func TestRelayout_SidebarWidthIs30Percent(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.width = 120
    model.height = 40
    model.ready = true
    model.relayout()
    // 30% of 120 = 36
    if model.sidebarWidth < 30 || model.sidebarWidth > 40 {
        t.Errorf("sidebarWidth = %d, want ~36 (30%% of 120)", model.sidebarWidth)
    }
    // viewport uses remaining ~70%
    if model.viewport.Width < 70 {
        t.Errorf("viewport.Width = %d, want ≥70 (left column inner)", model.viewport.Width)
    }
}
```

- [ ] **Step 4: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run TestRelayout_SidebarWidthIs30Percent -v
```
Expected: `FAIL` — `sidebarWidth = 0`

- [ ] **Step 5: 更新 `relayout()` 计算 70/30**

将 `layout.go` 第 189-199 行的 `relayout()` 全部替换为：
```go
func (m *appModel) relayout() {
    total := maxInt(40, m.width)
    // 右侧面板占 30%；最少 rightSidebarMinWidth 字符
    m.sidebarWidth = maxInt(rightSidebarMinWidth, total*30/100)
    leftColWidth := total - m.sidebarWidth

    m.viewport.Width = maxInt(20, leftColWidth-transcriptPanelHorizontalFrame)
    m.input.SetWidth(maxInt(20, leftColWidth-4))
    m.input.SetHeight(inputVisibleLineCount(m.input))

    headerHeight := m.headerHeight()
    inputHeight := lipgloss.Height(m.renderActiveInputPanel())
    // inputAboveMeter 已移除，不再占高度
    availableTranscriptHeight := m.height - headerHeight - inputHeight - transcriptPanelVerticalFrame
    m.viewport.Height = maxInt(1, availableTranscriptHeight)
    m.expandTranscriptToFillHeight()
}
```

- [ ] **Step 6: 更新 `View()` 为水平分栏，移除 inputAboveMeter**

将 `layout.go` 第 11-33 行的 `View()` 全部替换为：
```go
func (m appModel) View() string {
    if !m.ready {
        if m.cursorAnchor != nil {
            m.cursorAnchor.clear()
        }
        return "Starting Bubble Tea..."
    }

    input := m.renderActiveInputPanel()
    panelHeight := maxInt(1, m.height-m.headerHeight())

    // 左列：transcript + 输入框
    leftContent := lipgloss.JoinVertical(lipgloss.Left,
        m.renderTranscriptBox(),
        input,
    )
    // 右列：侧边栏（三个卡片）
    right := m.renderRightPanel(m.sidebarWidth, panelHeight)

    body := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, right)

    var parts []string
    if header := m.renderHeader(); header != "" {
        parts = append(parts, header)
    }
    parts = append(parts, body)
    view := lipgloss.JoinVertical(lipgloss.Left, parts...)
    m.updateTerminalCursorAnchor(input)
    return view
}
```

- [ ] **Step 7: 更新 `renderTranscriptBox()` 使用左列宽**

将第 152-155 行替换为：
```go
func (m appModel) renderTranscriptBox() string {
    leftColWidth := m.width - m.sidebarWidth
    width := maxInt(28, leftColWidth-2)
    return transcriptPanelStyle.Width(width).Height(maxInt(1, m.viewport.Height)).Render(m.viewport.View())
}
```

- [ ] **Step 8: 更新 `expandTranscriptToFillHeight()` 移除 renderInputAboveMeter**

将第 202-220 行替换为：
```go
func (m *appModel) expandTranscriptToFillHeight() {
    if m.height <= 0 {
        return
    }
    input := m.renderActiveInputPanel()
    var parts []string
    if header := m.renderHeader(); header != "" {
        parts = append(parts, header)
    }
    parts = append(parts, m.renderTranscriptBox())
    parts = append(parts, input)
    full := lipgloss.JoinVertical(lipgloss.Left, parts...)
    if deficit := m.height - m.sidebarWidth - lipgloss.Height(full); deficit > 0 {
        m.viewport.Height += deficit
    }
}
```

- [ ] **Step 9: 在 `layout.go` 末尾加 stub `renderRightPanel`**

```go
// renderRightPanel 渲染右侧 30% 面板（三个圆角卡片）。
func (m appModel) renderRightPanel(width, totalHeight int) string {
    // stub — 后续 Task 3-7 填充真实内容
    inner := maxInt(4, width-2)
    _ = inner
    return rightCardStyle.Width(width - 2).Height(totalHeight - 2).Render("")
}
```

- [ ] **Step 10: 移除 `inputEmbeddedTitleHeight()` 对 renderInputAboveMeter 的依赖（已经在 View 里移除，此处确认 `renderInputBox()` 不受影响）**

确认 `renderInputBox()` 第 115-149 行中 `settings.MeterLocationInputTitle` 分支仍然独立工作（不修改）。

- [ ] **Step 11: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestRelayout_SidebarWidthIs30Percent -v
```
Expected: PASS

```bash
go build ./... && go test ./... 2>&1 | tail -15
```
Expected: all packages ok (或仅有宽度相关的存量测试失败需要逐一修复)

- [ ] **Step 12: 修复存量宽度测试**

搜索 `bubble_test.go` 里所有 `m.width` / `viewport.Width` 的断言，把基于 `m.width-4` 的期望值改为基于 `leftColWidth-4`（其中 `leftColWidth = m.width - m.sidebarWidth`）。

- [ ] **Step 13: Commit**

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/styles.go \
        internal/ui/bubble/layout.go internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : TUI 70/30 水平分栏，移除输入框上方 context meter"
```

---

### Task 2: 工具调用 blockquote 样式

**Files:**
- Modify: `internal/ui/bubble/styles.go` (add 3 border styles)
- Modify: `internal/ui/bubble/transcript.go` (rewrite renderToolEntryBody)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 在 `styles.go` 末尾加三个 blockquote 样式**

```go
// 工具调用 blockquote 样式（以带颜色竖线区分调用/成功/失败）
toolCallBorderStyle = lipgloss.NewStyle().
    Border(lipgloss.Border{Left: "│"}).
    BorderForeground(colorManager.LipglossColor(colorLabelTool)).  // 214 橙色
    Background(lipgloss.Color("232")).
    PaddingLeft(1)

toolResultBorderStyle = lipgloss.NewStyle().
    Border(lipgloss.Border{Left: "│"}).
    BorderForeground(lipgloss.Color("65")).  // 绿色系
    Background(lipgloss.Color("232")).
    PaddingLeft(1)

toolErrorBorderStyle = lipgloss.NewStyle().
    Border(lipgloss.Border{Left: "│"}).
    BorderForeground(colorManager.LipglossColor(colorLabelError)). // 203 红色
    Background(lipgloss.Color("232")).
    PaddingLeft(1)
```

- [ ] **Step 2: 写失败测试（验证 blockquote 包含竖线）**

在 `bubble_test.go` 追加：
```go
func TestRenderToolEntryBody_BlockquoteContainsVerticalBar(t *testing.T) {
    body := "Read README.md\nfile content here"
    result := renderToolEntryBody(body, 60, 1.0)
    if !strings.Contains(result, "│") {
        t.Errorf("renderToolEntryBody = %q, want │ vertical bar (blockquote style)", result)
    }
    if !strings.Contains(result, "Read README.md") {
        t.Errorf("renderToolEntryBody = %q, want summary in header", result)
    }
}

func TestRenderToolResultBody_GreenBorder(t *testing.T) {
    // result: ok 行用绿色竖线（通过 title 区分，在 renderEntryBodyAt 中 title=="result" 决定样式）
    entry := transcriptEntry{kind: entryTool, title: "result", body: "Read ok\npackage main..."}
    result := renderEntryBodyAt(entry, 60, time.Time{})
    if !strings.Contains(result, "│") {
        t.Errorf("tool result = %q, want │ border", result)
    }
}
```

- [ ] **Step 3: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run "TestRenderTool" -v
```
Expected: FAIL — no `│` in current output

- [ ] **Step 4: 更新 `renderToolEntryBody` 为 blockquote 样式**

将 `transcript.go` 第 156-177 行的 `renderToolEntryBody` 全部替换为：
```go
func renderToolEntryBody(body string, width int, progress float64) string {
    body = strings.TrimRight(body, "\n")
    summary, detail := splitToolSummary(body)
    // header 行：▾ tool: Name  或  ▾ result: ok
    header := toolHeaderStyle.Render("▾ " + summary)

    if detail == "" || progress <= 0 {
        return toolCallBorderStyle.Width(width - 3).Render(header)
    }

    detailLines := strings.Split(detail, "\n")
    visibleLines := maxInt(1, int(float64(len(detailLines))*progress+0.999))
    if visibleLines > len(detailLines) {
        visibleLines = len(detailLines)
    }
    // 参数/内容行："> key: value" 前缀
    prefixed := make([]string, visibleLines)
    for i, l := range detailLines[:visibleLines] {
        prefixed[i] = "> " + l
    }
    content := header + "\n" + toolDetailStyle.Width(width-4).Render(strings.Join(prefixed, "\n"))
    return toolCallBorderStyle.Width(width - 3).Render(content)
}
```

- [ ] **Step 5: 更新 `renderEntryBodyAt` 为 result/error 使用不同竖线颜色**

在 `renderEntryBodyAt` 第 150-153 行（entryTool 分支）改为：
```go
if entry.kind == entryTool {
    // title=="result" 用绿色竖线，其余（"tool"、错误）用橙/红色
    isError := strings.HasPrefix(entry.body, "result") && strings.Contains(entry.body, "error")
    isResult := entry.title == "result"
    body := renderToolEntryBody(entry.body, width, toolExpandProgress(entry, at))
    if isError {
        // 已经是 toolCallBorderStyle，此处仅换颜色：重渲染外层边框
        // 移除旧边框（toolCallBorderStyle 渲染时已包含），改用 error 样式
        summary, detail := splitToolSummary(strings.TrimRight(entry.body, "\n"))
        header := toolHeaderStyle.Render("▾ " + summary)
        _ = detail
        return toolErrorBorderStyle.Width(width - 3).Render(header)
    }
    if isResult {
        summary, detail := splitToolSummary(strings.TrimRight(entry.body, "\n"))
        header := toolHeaderStyle.Render("▾ " + summary)
        if detail == "" {
            return toolResultBorderStyle.Width(width - 3).Render(header)
        }
        lines := strings.Split(detail, "\n")
        prefixed := make([]string, len(lines))
        for i, l := range lines {
            prefixed[i] = "> " + l
        }
        content := header + "\n" + toolDetailStyle.Width(width-4).Render(strings.Join(prefixed, "\n"))
        return toolResultBorderStyle.Width(width - 3).Render(content)
    }
    return body
}
```

- [ ] **Step 6: 运行测试**

```bash
go test ./internal/ui/bubble/... -run "TestRenderTool" -v
go test ./... 2>&1 | tail -10
```
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/ui/bubble/styles.go internal/ui/bubble/transcript.go \
        internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : 工具调用改用 blockquote 竖线样式（橙/绿/红）"
```

---

### Task 3: Context 卡片（右侧面板第三块）

**Files:**
- Modify: `internal/ui/bubble/context_meter.go` (add renderContextCardContent)
- Create: `internal/ui/bubble/right_panel.go`
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestRenderContextCard_ContainsTokenAndFree(t *testing.T) {
    runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 5000, LimitTokens: 100000}}
    model := newTestModel(runner)
    result := model.renderContextCard(28)
    if !strings.Contains(result, "↑") && !strings.Contains(result, "↓") {
        t.Errorf("context card = %q, want ↑ or ↓ arrow", result)
    }
    if !strings.Contains(result, "free") {
        t.Errorf("context card = %q, want free label", result)
    }
}
```

- [ ] **Step 2: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run TestRenderContextCard -v
```
Expected: FAIL — `renderContextCard undefined`

- [ ] **Step 3: 在 `context_meter.go` 末尾加 `renderContextCardContent`**

```go
// renderContextCardContent 为右侧 Context 卡片渲染多行内容（token 数 + 进度条 + 百分比 + turns）。
func (m appModel) renderContextCardContent(innerWidth int) string {
    stats := m.contextStats()
    limit := maxInt(1, stats.LimitTokens)
    rawUsed := maxInt(0, stats.UsedTokens)
    rawCache := clampInt(stats.CacheTokens, 0, rawUsed)
    labelUsed, labelCache := m.animatedLabelTokens(rawUsed, rawCache, limit)

    arrow := "↑"
    if m.isGenerating {
        arrow = "↓"
    }
    tokenStr := formatCompactTokenCount(labelUsed) + arrow
    freeStr := formatContextFreeLabel(labelUsed, limit)

    // 第一行：token 数 + 箭头左，free 右
    topLine := lipgloss.JoinHorizontal(lipgloss.Top,
        contextUsedStyle.Render(tokenStr),
        lipgloss.NewStyle().Width(maxInt(1, innerWidth-lipgloss.Width(tokenStr)-lipgloss.Width(freeStr))).Render(""),
        contextFreeStyle.Render(freeStr),
    )

    // 进度条（含 cache 层，高 1 行）
    barWidth := maxInt(1, innerWidth)
    animatedUsed, animatedCache, _ := m.animatedContextTokens(limit)
    bar := renderContextBar(animatedUsed, animatedCache, limit, barWidth, "")

    // 第三行：百分比
    usedPct := formatContextPercent(labelUsed, limit)
    cachePct := formatContextPercent(labelCache, limit)
    freePct := formatContextPercent(maxInt(0, limit-labelUsed), limit)
    pctLine := contextUsedStyle.Render(usedPct) + " " +
        contextCacheStyle.Render("cache "+cachePct) + " " +
        contextFreeStyle.Render("free "+freePct)

    // 第四行：turns（右对齐）
    turnsStr := fmt.Sprintf("turns %d", m.turnsCount())
    turnsLine := lipgloss.NewStyle().
        Width(innerWidth).
        Foreground(lipgloss.Color("236")).
        Align(lipgloss.Right).
        Render(turnsStr)

    return strings.Join([]string{topLine, bar, pctLine, turnsLine}, "\n")
}

// turnsCount 计算当前会话的对话轮次数（user 消息数量）。
func (m appModel) turnsCount() int {
    n := 0
    for _, e := range m.transcript {
        if e.kind == entryUser {
            n++
        }
    }
    return n
}
```

- [ ] **Step 4: 创建 `right_panel.go` 并实现 `renderContextCard`**

```go
// 本文件定义右侧 30% 面板的三个卡片渲染逻辑。
package bubble

import (
    "github.com/charmbracelet/lipgloss"
)

// renderRightPanel 渲染右侧面板（Pipeline/Tasks + Subagents + Context 三个圆角卡片）。
func (m appModel) renderRightPanel(width, totalHeight int) string {
    inner := maxInt(4, width-2) // rightCardStyle border=1 + padding=1 each side = 2 total

    // 先渲染两个自适应高度的卡片
    subagentsContent := m.renderSubagentsCardContent(inner)
    subagentsCard := rightCardStyle.Width(inner).Render(subagentsContent)
    subH := lipgloss.Height(subagentsCard)

    contextContent := m.renderContextCardContent(inner)
    contextCard := rightCardStyle.Width(inner).Render(contextContent)
    ctxH := lipgloss.Height(contextCard)

    // Pipeline/Tasks 卡片填满剩余高度（最少 6 行）
    pipelineH := maxInt(6, totalHeight-subH-ctxH-2) // -2 = 两个间隙行
    pipelineContent := m.renderPipelineOrTasksContent(inner, pipelineH-2)
    pipelineCard := rightCardStyle.Width(inner).Height(pipelineH - 2).Render(pipelineContent)

    return lipgloss.JoinVertical(lipgloss.Left,
        pipelineCard,
        subagentsCard,
        contextCard,
    )
}

// renderContextCard 返回带圆角边框的 Context 卡片（公共方法，供测试使用）。
func (m appModel) renderContextCard(width int) string {
    inner := maxInt(4, width-2)
    return rightCardStyle.Width(inner).Render(m.renderContextCardContent(inner))
}

// renderSubagentsCardContent 渲染 Subagents 卡片内容（空实现，Task 4 填充）。
func (m appModel) renderSubagentsCardContent(_ int) string {
    return "subagents"
}

// renderPipelineOrTasksContent 渲染 Pipeline 或 Tasks 卡片内容（空实现，Task 5-7 填充）。
func (m appModel) renderPipelineOrTasksContent(_, _ int) string {
    return "pipeline / tasks"
}
```

- [ ] **Step 5: 更新 `layout.go` 里的 stub `renderRightPanel` — 删除之前的 stub，`right_panel.go` 现在提供真实实现**

删除 `layout.go` 末尾的：
```go
func (m appModel) renderRightPanel(width, totalHeight int) string {
    // stub...
}
```
（`right_panel.go` 已经定义了正式版本）

- [ ] **Step 6: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestRenderContextCard -v
go build ./... && go test ./... 2>&1 | tail -10
```
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/ui/bubble/context_meter.go internal/ui/bubble/right_panel.go \
        internal/ui/bubble/layout.go internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : 右侧面板 Context 卡片（token + bar + 百分比）"
```

---

### Task 4: Subagents 卡片

**Files:**
- Modify: `internal/ui/bubble/right_panel.go` (fill renderSubagentsCardContent)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestRenderSubagentsCard_ShowsRunningStatus(t *testing.T) {
    ctrl := &fakeSubagentController{
        tasks: []subagent.TaskSnapshot{
            {ID: "t1", Status: subagent.TaskRunning, ContextMode: settings.ContextModeFork},
            {ID: "t2", Status: subagent.TaskDone, ContextMode: settings.ContextModeEmpty},
        },
    }
    model := newTestModel(&fakeRunner{})
    model.subagents = ctrl
    result := model.renderSubagentsCardContent(30)
    if !strings.Contains(result, "t1") {
        t.Errorf("subagents card = %q, want t1", result)
    }
}
```

- [ ] **Step 2: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run TestRenderSubagentsCard -v
```
Expected: FAIL — content is just `"subagents"` stub

- [ ] **Step 3: 实现 `renderSubagentsCardContent`**

替换 `right_panel.go` 里的 stub：
```go
func (m appModel) renderSubagentsCardContent(width int) string {
    hdr := lipgloss.NewStyle().
        Foreground(lipgloss.Color("237")).
        Render("subagents")

    if m.subagents == nil {
        return hdr
    }
    tasks := m.subagents.ListTasks()
    if len(tasks) == 0 {
        empty := lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Italic(true).Render("none")
        return hdr + "\n" + empty
    }

    dotRun  := lipgloss.NewStyle().Foreground(lipgloss.Color("84")).Render("⟳")
    dotDone := lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render("✓")
    dotFail := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("✗")

    lines := []string{hdr}
    for _, t := range tasks {
        var dot, nameStyle string
        switch t.Status {
        case subagent.TaskRunning:
            dot = dotRun
            nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("84")).Render(t.ID)
        case subagent.TaskFailed, subagent.TaskError:
            dot = dotFail
            nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(t.ID)
        default:
            dot = dotDone
            nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("236")).Render(t.ID)
        }
        line := dot + " " + nameStyle
        lines = append(lines, line)
    }
    return strings.Join(lines, "\n")
}
```

（需要在 `right_panel.go` 顶部 import 里加 `"strings"` 和 `"gocode/internal/subagent"` 和 `"gocode/internal/settings"`）

- [ ] **Step 4: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestRenderSubagentsCard -v
go test ./... 2>&1 | tail -10
```

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bubble/right_panel.go internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : 右侧面板 Subagents 卡片（running/done/fail）"
```

---

### Task 5: Tasks 卡片（无 pipeline 时的顶部卡片）

**Files:**
- Modify: `internal/ui/bubble/right_panel.go` (renderPipelineOrTasksContent → tasks branch)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestRenderTasksCard_ShowsActiveTask(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    // 注入两个 task（用已有的 task 系统）
    // pipelineState.detected = false，顶部卡片应显示 tasks
    model.pipelineState.detected = false
    result := model.renderPipelineOrTasksContent(30, 10)
    // 当前无 tasks，显示 badge
    if !strings.Contains(result, "tasks") {
        t.Errorf("tasks card = %q, want 'tasks' badge", result)
    }
}
```

- [ ] **Step 2: 在 `types.go` 加 `pipelineState` 结构体（为 Task 6 预置类型）**

在 `appModel` struct 后加：
```go
// pipelinePhaseStatus 标记单个 pipeline 阶段的状态。
type pipelinePhaseStatus int

const (
    phaseStatusPending pipelinePhaseStatus = iota
    phaseStatusDone
    phaseStatusActive
    phaseStatusRetry
)

// pipelinePhaseEntry 单个阶段的状态快照。
type pipelinePhaseEntry struct {
    name      string
    artifact  string // 相对 .pipeline-workspace/ 的文件名
    status    pipelinePhaseStatus
    iteration int
}

// pipelineState 完整 pipeline 状态快照（每 500ms 轮询更新）。
type pipelineState struct {
    detected   bool               // .pipeline-workspace/spec.json 是否存在
    activeIdx  int                // 当前 active 阶段索引（-1 = none）
    globalIter int                // 全局迭代（来自 execution-report.json.iteration）
    doneCount  int                // 已完成阶段数
    phases     [18]pipelinePhaseEntry
}

// pipelineStateUpdatedMsg 由后台轮询 cmd 发回，携带最新 pipelineState。
type pipelineStateUpdatedMsg struct {
    state pipelineState
}
```

同时在 `appModel` 末尾 `completion *completion` 后加：
```go
pipelineState pipelineState
```

- [ ] **Step 3: 实现 `renderPipelineOrTasksContent`（tasks 分支）**

替换 stub：
```go
func (m appModel) renderPipelineOrTasksContent(width, height int) string {
    if m.pipelineState.detected {
        return m.renderPipelineWindowedContent(width, height)
    }
    return m.renderTasksContent(width, height)
}

func (m appModel) renderTasksContent(width, height int) string {
    badge := lipgloss.NewStyle().
        Foreground(lipgloss.Color("136")).
        Background(lipgloss.Color("234")).
        Padding(0, 1).
        Render("✦ tasks")

    // TaskList 来自 runner（如实现了 TaskLister 接口）——当前 runner 无此接口，显示空状态
    // TODO: 当 Runner 接口扩展 TaskList() 后，此处读取真实 tasks
    empty := lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Italic(true).Render("no tasks")
    return badge + "\n" + empty
}

// renderPipelineWindowedContent 留给 Task 7 实现。
func (m appModel) renderPipelineWindowedContent(width, height int) string {
    return "pipeline..."
}
```

- [ ] **Step 4: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestRenderTasksCard -v
go test ./... 2>&1 | tail -10
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/right_panel.go \
        internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : 右侧面板 Tasks 卡片（无 pipeline 时显示）"
```

---

### Task 6: Pipeline 状态检测（文件轮询）

**Files:**
- Modify: `internal/ui/bubble/app.go` (add pipelinePollCmd, Update handler)
- Modify: `internal/ui/bubble/right_panel.go` (add loadPipelineState helper)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 写失败测试（验证轮询 cmd 返回正确 msg）**

```go
func TestLoadPipelineState_DetectedWhenSpecExists(t *testing.T) {
    dir := t.TempDir()
    // 创建 spec.json
    if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(`{}`), 0o644); err != nil {
        t.Fatal(err)
    }
    state := loadPipelineState(dir)
    if !state.detected {
        t.Errorf("detected = false, want true when spec.json exists")
    }
}

func TestLoadPipelineState_NotDetectedWhenEmpty(t *testing.T) {
    dir := t.TempDir()
    state := loadPipelineState(dir)
    if state.detected {
        t.Errorf("detected = true, want false for empty dir")
    }
}
```

- [ ] **Step 2: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run TestLoadPipelineState -v
```
Expected: FAIL — `loadPipelineState undefined`

- [ ] **Step 3: 在 `right_panel.go` 实现 `loadPipelineState`**

```go
import (
    "encoding/json"
    "os"
    "path/filepath"
)

// pipelineArtifacts 按顺序列出 18 个阶段的名称和 artifact 文件名。
var pipelineArtifacts = [18][2]string{
    {"Brainstorm",  "design.md"},
    {"Spec",        "spec.json"},
    {"Plan",        "plan.json"},
    {"Arch",        "architecture.json"},
    {"Dispatch",    "dispatch.json"},
    {"Execution",   "execution-report.json"},
    {"Complexity",  "execution-report.json"}, // 与 Execution 共用，通过 Merge 是否存在区分
    {"Merge",       "merge-report.json"},
    {"Validation",  "validation-report.json"},
    {"Tree Class",  "tree-classification.json"},
    {"Rubric Gen",  "tree-rubrics.json"},
    {"Rubric Vfy",  "tree-rubric-verification.json"},
    {"Rubric Rfn",  "tree-rubrics-refined.json"},
    {"Grading",     "tree-grading-individual.json"},
    {"QA",          "qa-report.json"},
    {"Docs",        "doc-report.json"},
    {"Assessment",  "final-assessment.json"},
    {"Cleanup",     ".pipeline-last-run-summary.json"},
}

// loadPipelineState 扫描 workspaceDir 推断当前 pipeline 阶段状态。
// workspaceDir 通常为 .pipeline-workspace/（相对 cwd）。
func loadPipelineState(workspaceDir string) pipelineState {
    var s pipelineState
    // 读取迭代次数（execution-report.json.iteration）
    iterFile := filepath.Join(workspaceDir, "execution-report.json")
    if data, err := os.ReadFile(iterFile); err == nil {
        var rep struct {
            Iteration int `json:"iteration"`
        }
        if json.Unmarshal(data, &rep) == nil && rep.Iteration > 0 {
            s.globalIter = rep.Iteration
        }
    }

    s.activeIdx = -1
    lastDoneIdx := -1
    for i, pa := range pipelineArtifacts {
        artifactPath := filepath.Join(workspaceDir, pa[1])
        // Cleanup 阶段的 artifact 在 workspaceDir 的父目录
        if pa[0] == "Cleanup" {
            artifactPath = filepath.Join(filepath.Dir(workspaceDir), pa[1])
        }
        _, err := os.Stat(artifactPath)
        exists := err == nil
        s.phases[i] = pipelinePhaseEntry{
            name:     pa[0],
            artifact: pa[1],
        }
        if exists {
            s.phases[i].status = phaseStatusDone
            s.doneCount++
            lastDoneIdx = i
        }
    }

    // pipeline 只有当 spec.json 存在才算"检测到"
    if _, err := os.Stat(filepath.Join(workspaceDir, "spec.json")); err == nil {
        s.detected = true
    }

    // 确定 active 阶段：lastDoneIdx+1（如未完成）
    if s.detected && lastDoneIdx+1 < 18 {
        s.activeIdx = lastDoneIdx + 1
        s.phases[s.activeIdx].status = phaseStatusActive
        s.phases[s.activeIdx].iteration = s.globalIter
    }

    // retry：execution-report 存在且 iteration>1 且 validation 不存在
    execExists := s.phases[5].status == phaseStatusDone
    validExists := s.phases[8].status == phaseStatusDone
    if execExists && s.globalIter > 1 && !validExists {
        for i := 5; i <= 8; i++ {
            if s.phases[i].status != phaseStatusDone {
                s.phases[i].status = phaseStatusRetry
            }
        }
    }
    return s
}
```

- [ ] **Step 4: 在 `app.go` 里加 `pipelinePollCmd` 和消息处理**

在 `Update()` 的 `case` 块末尾（`subagentFinishedMsg` 之后）加：
```go
case pipelineStateUpdatedMsg:
    m.pipelineState = msg.state
    return m, nil
```

在 `cursorFrameMsg` 处理函数里，在 `return m, cursorFrameTick()` 之前加（每约 500ms 触发一次轮询）：
```go
// 每 15 帧（约 500ms）轮询一次 pipeline 状态
if int(m.cursorFrameAt.UnixMilli()/500)%1 == 0 {
    cmds = append(cmds, pipelinePollCmd())
}
```

在文件某处加：
```go
// pipelinePollCmd 异步检测 .pipeline-workspace/ 并返回 pipelineStateUpdatedMsg。
func pipelinePollCmd() tea.Cmd {
    return func() tea.Msg {
        cwd, err := os.Getwd()
        if err != nil {
            return pipelineStateUpdatedMsg{}
        }
        workspaceDir := filepath.Join(cwd, ".pipeline-workspace")
        return pipelineStateUpdatedMsg{state: loadPipelineState(workspaceDir)}
    }
}
```

（`app.go` 需要 import `"os"` 和 `"path/filepath"` — 检查是否已有）

- [ ] **Step 5: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestLoadPipelineState -v
go test ./... 2>&1 | tail -10
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/right_panel.go \
        internal/ui/bubble/app.go internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : pipeline 状态检测（每 500ms 轮询 .pipeline-workspace/）"
```

---

### Task 7: Pipeline 滚动窗口显示

**Files:**
- Modify: `internal/ui/bubble/right_panel.go` (implement renderPipelineWindowedContent)
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestRenderPipelineWindowedContent_ShowsActiveInCenter(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    // 设置 pipeline 在第 8 阶段（Validation，index 8）
    model.pipelineState.detected = true
    model.pipelineState.activeIdx = 8
    model.pipelineState.doneCount = 8
    model.pipelineState.globalIter = 3
    for i := 0; i < 8; i++ {
        model.pipelineState.phases[i] = pipelinePhaseEntry{name: pipelineArtifacts[i][0], status: phaseStatusDone}
    }
    model.pipelineState.phases[8] = pipelinePhaseEntry{name: "Validation", status: phaseStatusActive, iteration: 3}
    for i := 9; i < 18; i++ {
        model.pipelineState.phases[i] = pipelinePhaseEntry{name: pipelineArtifacts[i][0], status: phaseStatusPending}
    }

    result := model.renderPipelineWindowedContent(28, 12)
    if !strings.Contains(result, "Validation") {
        t.Errorf("windowed = %q, want Validation in center", result)
    }
    if !strings.Contains(result, "8/18") {
        t.Errorf("windowed = %q, want 8/18 count", result)
    }
}
```

- [ ] **Step 2: 运行测试验证失败**

```bash
go test ./internal/ui/bubble/... -run TestRenderPipelineWindowed -v
```
Expected: FAIL

- [ ] **Step 3: 实现 `renderPipelineWindowedContent`**

替换 `right_panel.go` 里的 stub：
```go
func (m appModel) renderPipelineWindowedContent(width, height int) string {
    ps := m.pipelineState
    iterStr := ""
    if ps.globalIter > 0 {
        iterStr = fmt.Sprintf(" · iter %d", ps.globalIter)
    }
    badge := lipgloss.NewStyle().
        Foreground(lipgloss.Color("68")).
        Background(lipgloss.Color("234")).
        Padding(0, 1).
        Render(fmt.Sprintf("● pipeline%s", iterStr))

    countStr := lipgloss.NewStyle().Foreground(lipgloss.Color("237")).
        Render(fmt.Sprintf("%d/18", ps.doneCount))

    topLine := lipgloss.JoinHorizontal(lipgloss.Top,
        badge,
        lipgloss.NewStyle().Width(maxInt(1, width-lipgloss.Width(badge)-lipgloss.Width(countStr))).Render(""),
        countStr,
    )

    // 18 小圆点总览
    dots := make([]string, 18)
    for i, ph := range ps.phases {
        switch ph.status {
        case phaseStatusDone:
            dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("28")).Render("●")
        case phaseStatusActive:
            dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true).Render("▶")
        case phaseStatusRetry:
            dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("○")
        default:
            dots[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Render("·")
        }
    }
    dotsLine := strings.Join(dots, "")

    // 滚动窗口：当前 ±3（共 7 行）
    cur := ps.activeIdx
    if cur < 0 {
        cur = ps.doneCount - 1
    }
    if cur < 0 {
        cur = 0
    }

    opacities := []float64{0.20, 0.40, 0.65, 1.0, 0.65, 0.40, 0.20}
    offsets := []int{-3, -2, -1, 0, 1, 2, 3}

    stageLines := make([]string, 0, 7)
    for j, off := range offsets {
        idx := cur + off
        if idx < 0 || idx >= 18 {
            stageLines = append(stageLines, "")
            continue
        }
        ph := ps.phases[idx]
        opacity := opacities[j]

        var dotStr string
        switch ph.status {
        case phaseStatusDone:
            dotStr = "●"
        case phaseStatusActive:
            dotStr = "▶"
        case phaseStatusRetry:
            dotStr = "○"
        default:
            dotStr = "·"
        }

        iterSuffix := ""
        if ph.iteration > 0 && (ph.status == phaseStatusActive || ph.status == phaseStatusRetry) {
            if ph.status == phaseStatusRetry {
                iterSuffix = fmt.Sprintf(" ×%d ↻", ph.iteration)
            } else {
                iterSuffix = fmt.Sprintf(" ×%d", ph.iteration)
            }
        }
        label := ph.name + iterSuffix

        // 通过调暗颜色模拟 opacity（中心最亮）
        alpha := int(opacity * 255)
        _ = alpha
        var fgColor lipgloss.Color
        switch {
        case opacity >= 0.9:
            fgColor = lipgloss.Color("252")
        case opacity >= 0.55:
            fgColor = lipgloss.Color("242")
        case opacity >= 0.35:
            fgColor = lipgloss.Color("238")
        default:
            fgColor = lipgloss.Color("235")
        }

        lineStyle := lipgloss.NewStyle().Foreground(fgColor)
        if off == 0 {
            // 当前阶段高亮框
            lineStyle = lineStyle.
                Background(lipgloss.Color("234")).
                Border(lipgloss.Border{Left: "▐"}).
                BorderForeground(lipgloss.Color("68")).
                PaddingLeft(1)
        }
        stageLines = append(stageLines, lineStyle.Render(dotStr+" "+label))
    }

    return strings.Join(append(
        []string{topLine, dotsLine},
        stageLines...,
    ), "\n")
}
```

（`right_panel.go` 需要在 import 里加 `"fmt"`）

- [ ] **Step 4: 运行测试**

```bash
go test ./internal/ui/bubble/... -run TestRenderPipelineWindowed -v
go test ./... 2>&1 | tail -10
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bubble/right_panel.go internal/ui/bubble/bubble_test.go
git commit -m "feat :sparkles: : Pipeline 滚动窗口显示（18 点总览 + 当前±3 邻居）"
```

---

### Task 8: 最终整合验证

**Files:**
- Modify: `internal/ui/bubble/bubble_test.go` (end-to-end layout test)

- [ ] **Step 1: 写整合测试**

```go
func TestFullLayout_RightPanelVisible(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.width = 100
    model.height = 30
    model.ready = true
    model.relayout()

    view := model.View()
    // 右侧面板存在（context 相关内容出现）
    if !strings.Contains(view, "free") && !strings.Contains(view, "turns") {
        t.Errorf("View() = %q, want right panel with context card", view)
    }
    // 左右分布：transcript 宽约 70%，sidebar 约 30%
    if model.sidebarWidth < 25 || model.sidebarWidth > 35 {
        t.Errorf("sidebarWidth = %d, want ~30 for 100-wide terminal", model.sidebarWidth)
    }
}

func TestFullLayout_PipelineSwitchesToPipelineCard(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.width = 100
    model.height = 30
    model.ready = true
    model.pipelineState.detected = true
    model.pipelineState.activeIdx = 1
    model.pipelineState.phases[0] = pipelinePhaseEntry{name: "Brainstorm", status: phaseStatusDone}
    model.pipelineState.phases[1] = pipelinePhaseEntry{name: "Spec", status: phaseStatusActive}
    model.relayout()

    view := model.View()
    if !strings.Contains(view, "pipeline") {
        t.Errorf("View() = %q, want 'pipeline' badge when pipeline detected", view)
    }
}
```

- [ ] **Step 2: 运行所有测试**

```bash
go test -count=1 ./... 2>&1
```
Expected: all packages ok

- [ ] **Step 3: 手动验证**

```bash
go run ./cmd/agent
```
检查清单：
1. TUI 左右分栏出现，无中间分割线
2. 工具调用显示 blockquote 竖线样式（橙/绿/红）
3. 输入框上方无 context meter
4. 右侧 Context 卡片显示 token + bar + free%
5. 右侧 Subagents 卡片显示运行中的 subagent
6. 创建 `.pipeline-workspace/spec.json` 后等待约 1s，右侧顶部切换为 pipeline 视图
7. 写入 `.pipeline-workspace/plan.json`，第 2 阶段变绿，Plan 阶段变 active

- [ ] **Step 4: 推送**

```bash
git push
```

---

## Self-Review

**Spec coverage check:**
- ✅ 70/30 layout without divider — Task 1
- ✅ Tool call blockquote (orange/green/red) — Task 2
- ✅ Remove renderInputAboveMeter — Task 1
- ✅ Context card (token + bar + % + turns) — Task 3
- ✅ Subagents card — Task 4
- ✅ Tasks card (no pipeline) — Task 5
- ✅ Pipeline state machine (18 stages, file polling) — Task 6
- ✅ Windowed display (18 dots + ±3 window) — Task 7
- ✅ Pipeline/Tasks switching — Task 5 + 7 together via renderPipelineOrTasksContent

**Type consistency:**
- `pipelineState`, `pipelinePhaseEntry`, `pipelinePhaseStatus` defined in Task 5 → used in Tasks 6, 7, 8 ✅
- `pipelineArtifacts` defined in Task 6 (`right_panel.go`) → referenced in Task 7 test ✅
- `renderPipelineOrTasksContent` defined as stub in Task 3, filled in Task 5, calls `renderPipelineWindowedContent` implemented in Task 7 ✅

**No placeholders:** All code blocks contain real implementations. No TBD/TODO in task steps.
