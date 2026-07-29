# Transcript New Message Notification Implementation Plan

> For Codex workers: Implement task-by-task. Use update_plan to track progress, keep only one step in progress at a time, edit files with the repo's established tools and apply_patch for manual changes, and run the exact verification commands listed below. Steps use checkbox syntax for tracking.

**Goal:** 在 Paw TUI 中实现非底部 transcript 的中文新消息提示、计数、点击回到底部和滚动清零，同时保持现有 smart scroll 的手动偏移。

**Architecture:** 在 appModel 中维护未读数量、悬停/点击状态和 detached scroll 周期；在 transcript 逻辑事件入口计数，而不是在渲染帧或每个 token 上计数。提示作为不改变布局高度的 opaque overlay 渲染在 transcript 最后一行，鼠标命中在现有 transcript 选择处理之前处理。

**Tech Stack:** Go、Bubble Tea、Bubbles viewport、Lip Gloss、现有 terminal cell/ANSI overlay 工具、internal/ui/bubble 单元测试。

---

## 当前代码地图

- internal/ui/bubble/types.go:292 定义 appModel，transcriptEntry 位于同文件前部。
- internal/ui/bubble/transcript.go:50 负责 assistant/thinking 流、addEntry、tool result 更新和 viewport 刷新。
- internal/ui/bubble/app.go:110 是 Bubble Tea Update；窗口、消息、键盘和鼠标事件都在这里汇合。
- internal/ui/bubble/layout.go:91 组装 header、transcript、dock status 和 input；renderTranscriptRegion 使用 placeOpaqueOverlay 渲染 transcript 浮层。
- internal/ui/bubble/selection.go:23 处理 transcript 鼠标选择、工具 hover 和边缘滚动。
- internal/ui/bubble/status_line.go:77 渲染 context usage/token frontier 所在的 dock status line。
- internal/ui/bubble/styles.go:140 已有 context style；colorSignal、colorTerminalBackground 和 colorWorktreeBackground 可直接用于新提示，不需要新增调色板角色。
- 现有 smart-scroll 测试在 internal/ui/bubble/bubble_test.go:1020 附近；cell overlay 测试在 internal/ui/bubble/terminal_cells_test.go。

## Task 1: 建立通知状态、事件 marker 和纯渲染基础

Files:
- Create: internal/ui/bubble/new_message_notice.go
- Modify: internal/ui/bubble/types.go:20-75,292-365
- Create: internal/ui/bubble/new_message_notice_test.go

- [ ] Step 1: 写通知渲染与状态边界的失败测试

在 new_message_notice_test.go 中加入：

~~~go
func TestNewMessageNoticeText(t *testing.T) {
    if got := newMessageNoticeText(3, false, 80); got != "↓ 3 条新消息" {
        t.Fatalf("default text = %q", got)
    }
    if got := newMessageNoticeText(3, true, 80); got != "↓ 3 条新消息 · 回到底部" {
        t.Fatalf("hover text = %q", got)
    }
}

func TestNewMessageNoticeRenderUsesBackgroundOnly(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.newMessageNoticeCount = 3
    rendered := model.renderNewMessageNotice(80)
    if rendered == "" || !strings.Contains(ansi.Strip(rendered), "↓ 3 条新消息") {
        t.Fatalf("rendered notice = %q", rendered)
    }
    if _, ok := newMessageNoticeStyle.GetBackground().(lipgloss.NoColor); ok {
        t.Fatalf("notice style has no background")
    }
    if strings.Contains(ansi.Strip(rendered), "┌") || strings.Contains(ansi.Strip(rendered), "─") {
        t.Fatalf("rendered notice contains a border: %q", ansi.Strip(rendered))
    }
}

func TestNewMessageNoticeBoundsAreCentered(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.ready = true
    model.width = 80
    model.height = 10
    model.relayout()
    model.newMessageNoticeCount = 2
    bounds := model.transcriptNoticeBounds()
    if bounds.width <= 0 || bounds.height != 1 {
        t.Fatalf("notice bounds = %#v", bounds)
    }
    want := (model.currentLayout().contentWidth - bounds.width) / 2
    if bounds.x != want {
        t.Fatalf("notice x = %d, want %d", bounds.x, want)
    }
}

func TestNewMessageNoticeHiddenWhenCountIsZero(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    if got := model.renderNewMessageNotice(80); got != "" {
        t.Fatalf("zero-count notice = %q, want hidden", got)
    }
}
~~~

- [ ] Step 2: 运行失败测试，确认缺少实现

Run:

~~~bash
go test ./internal/ui/bubble -run 'TestNewMessageNotice' -count=1
~~~

Expected: FAIL，报告 newMessageNoticeCount、newMessageNoticeText 或 renderNewMessageNotice 尚未定义。

- [ ] Step 3: 添加通知字段、entry marker 和纯渲染方法

在 types.go 中为 transcriptEntry 增加 UI-only 字段：

~~~go
newMessageNoticeCycle uint64 // UI-only marker; 不参与 transcript 文本渲染。
~~~

在 appModel 中增加：

~~~go
newMessageNoticeCycle   uint64
newMessageNoticeCount   int
newMessageNoticeHovered bool
newMessageNoticePressed bool
~~~

在 new_message_notice.go 中实现类型、样式和方法：

~~~go
type transcriptNoticeBounds struct {
    x      int
    y      int
    width  int
    height int
}

var newMessageNoticeStyle = lipgloss.NewStyle().
    Foreground(colorManager.LipglossColor(colorSignal)).
    Background(colorManager.LipglossColor(colorWorktreeBackground)).
    Bold(true).
    Padding(0, 1)

var newMessageNoticeHoverStyle = lipgloss.NewStyle().
    Foreground(colorManager.LipglossColor(colorTerminalBackground)).
    Background(colorManager.LipglossColor(colorSignal)).
    Bold(true).
    Padding(0, 1)

func newMessageNoticeText(count int, hovered bool, width int) string {
    if count <= 0 || width <= 0 {
        return ""
    }
    full := fmt.Sprintf("↓ %d 条新消息", count)
    if hovered {
        full += " · 回到底部"
    }
    if terminalCellWidth(full) <= width {
        return full
    }
    compact := fmt.Sprintf("↓ %d 条消息", count)
    return truncateDisplayWidth(compact, width)
}

func (m appModel) renderNewMessageNotice(width int) string {
    if m.newMessageNoticeCount <= 0 || width <= 0 || !m.newMessageNoticeCanRender() {
        return ""
    }
    textWidth := maxInt(1, width-newMessageNoticeStyle.GetHorizontalPadding())
    text := newMessageNoticeText(m.newMessageNoticeCount, m.newMessageNoticeHovered, textWidth)
    if text == "" {
        return ""
    }
    style := newMessageNoticeStyle
    if m.newMessageNoticeHovered {
        style = newMessageNoticeHoverStyle
    }
    return style.Render(text)
}
~~~

transcriptNoticeBounds 使用 currentLayout、transcriptScreenTop 和 transcriptHeight-1 计算提示行；newMessageNoticeCanRender 在 modal 或 completion overlay 活跃时返回 false。命中区域宽度取默认文案和悬停文案两者中较宽者，避免 hover 后提示变宽造成边缘点击抖动；渲染文本宽度必须扣除样式水平 padding。

- [ ] Step 4: 运行基础测试并格式化

Run:

~~~bash
gofmt -w internal/ui/bubble/new_message_notice.go internal/ui/bubble/new_message_notice_test.go internal/ui/bubble/types.go
go test ./internal/ui/bubble -run 'TestNewMessageNotice' -count=1
~~~

Expected: 4 个通知基础测试 PASS。

## Task 2: 接入 transcript 逻辑事件计数

Files:
- Modify: internal/ui/bubble/app.go:45-78
- Modify: internal/ui/bubble/transcript.go:50-90,145-215,230-330
- Modify: internal/ui/bubble/new_message_notice.go
- Modify: internal/ui/bubble/new_message_notice_test.go

- [ ] Step 1: 写计数和去重失败测试

~~~go
func TestAssistantActivityCountsOnceWhileAwayFromBottom(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)

    next, _ := model.Update(assistantDeltaMsg("first line\nsecond line\n"))
    model = next.(appModel)
    next, _ = model.Update(assistantDeltaMsg("third line\n"))
    model = next.(appModel)

    if model.newMessageNoticeCount != 1 {
        t.Fatalf("assistant notice count = %d, want 1", model.newMessageNoticeCount)
    }
}

func TestThinkingToolAndSystemActivitiesCount(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)

    next, _ := model.Update(thinkingDeltaMsg("private thought\n"))
    model = next.(appModel)
    next, _ = model.Update(assistantDeltaMsg("answer\n"))
    model = next.(appModel)
    next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Read"}))
    model = next.(appModel)
    next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: "ok"}))
    model = next.(appModel)
    next, _ = model.Update(systemEventMsg(ui.SystemEvent{Title: "后台任务", Body: "完成"}))
    model = next.(appModel)

    if model.newMessageNoticeCount != 4 {
        t.Fatalf("activity notice count = %d, want 4", model.newMessageNoticeCount)
    }
}

func TestRunningToolDoesNotCountUntilResult(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)

    next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Read"}))
    model = next.(appModel)
    if model.newMessageNoticeCount != 0 {
        t.Fatalf("running tool notice count = %d, want 0", model.newMessageNoticeCount)
    }

    next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: "ok"}))
    model = next.(appModel)
    if model.newMessageNoticeCount != 1 {
        t.Fatalf("tool result notice count = %d, want 1", model.newMessageNoticeCount)
    }
}

func TestBottomActivityDoesNotCount(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.GotoBottom()
    next, _ := model.Update(assistantDeltaMsg("visible at bottom\n"))
    model = next.(appModel)

    if model.newMessageNoticeCount != 0 || !model.viewport.AtBottom() {
        t.Fatalf("bottom state = count %d atBottom %v", model.newMessageNoticeCount, model.viewport.AtBottom())
    }
}
~~~

- [ ] Step 2: 运行失败测试，确认事件入口尚未接入

Run:

~~~bash
go test ./internal/ui/bubble -run 'Test(AssistantActivity|ThinkingToolAndSystem|RunningTool|BottomActivity)' -count=1
~~~

Expected: transcript 仍会更新，但新增计数断言失败。

- [ ] Step 3: 实现周期清零和 transcript marker

在 new_message_notice.go 中实现：

~~~go
func (m *appModel) recordTranscriptEntryActivity(index int, countAtBottom bool) {
    if m == nil || index < 0 || index >= len(m.transcript) {
        return
    }
    if m.newMessageNoticeCycle == 0 {
        m.newMessageNoticeCycle = 1
    }
    entry := &m.transcript[index]
    if entry.newMessageNoticeCycle == m.newMessageNoticeCycle {
        return
    }
    atBottom := m.viewport.AtBottom()
    if atBottom && !countAtBottom {
        return
    }
    entry.newMessageNoticeCycle = m.newMessageNoticeCycle
    if !atBottom {
        m.newMessageNoticeCount++
    }
}

func (m *appModel) recordAssistantActivity(index int) {
    if m == nil || m.viewport.AtBottom() {
        return
    }
    m.recordTranscriptEntryActivity(index, false)
}

func (m *appModel) clearNewMessageNotice() {
    if m == nil {
        return
    }
    m.newMessageNoticeCount = 0
    m.newMessageNoticeHovered = false
    m.newMessageNoticePressed = false
    m.newMessageNoticeCycle++
    if m.newMessageNoticeCycle == 0 {
        m.newMessageNoticeCycle = 1
    }
}

func (m *appModel) syncNewMessageNoticeAfterScroll() {
    if m != nil && m.newMessageNoticeCount > 0 && m.viewport.AtBottom() {
        m.clearNewMessageNotice()
    }
}
~~~

在 newModel 初始化 newMessageNoticeCycle 为 1。

- [ ] Step 4: 把 marker 接到实际消息路径

在 appendAssistantDelta 每次稳定行写入并 touchTranscriptEntry 后调用 recordAssistantActivity。

在 finalizeThinkingStream flush 前保存 activeThinking；hadContent 为 true 时调用 recordTranscriptEntryActivity(thinkingIndex, true)。

在 addEntry append 后按以下规则计数：

~~~go
index := len(m.transcript) - 1
if entry.kind != entryUser && (entry.kind != entryTool || entry.toolStatus != "running") {
    m.recordTranscriptEntryActivity(index, true)
}
m.refreshViewport()
~~~

在 recordToolResultEntry 匹配并更新已有事务后、refreshViewport 前调用 recordTranscriptEntryActivity(idx, true)。

- [ ] Step 5: 运行计数和 smart-scroll 测试

Run:

~~~bash
gofmt -w internal/ui/bubble/app.go internal/ui/bubble/transcript.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/new_message_notice_test.go
go test ./internal/ui/bubble -run 'Test(AssistantActivity|ThinkingToolAndSystem|RunningTool|BottomActivity|AssistantStreamingPreservesManualTranscriptScroll|ThinkingStreamingPreservesManualTranscriptScroll|CompletedTurnPreservesManualTranscriptScroll)' -count=1
~~~

Expected: 所有计数与既有 smart-scroll 测试 PASS，非底部测试中的 YOffset 保持原值。

## Task 3: 添加浮空 overlay 和鼠标命中

Files:
- Modify: internal/ui/bubble/layout.go:119-153
- Modify: internal/ui/bubble/app.go:450-478
- Modify: internal/ui/bubble/new_message_notice.go
- Modify: internal/ui/bubble/new_message_notice_test.go

- [ ] Step 1: 写 overlay 和点击失败测试

测试至少覆盖：

~~~go
func TestViewRendersNewMessageNoticeAboveStatusLine(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.newMessageNoticeCount = 3
    view := ansi.Strip(model.View())
    lines := strings.Split(view, "\n")
    noticeRow, statusRow := -1, -1
    for i, line := range lines {
        if strings.Contains(line, "↓ 3 条新消息") {
            noticeRow = i
        }
        if strings.Contains(line, "ready") && strings.Contains(line, "chat") {
            statusRow = i
        }
    }
    if noticeRow < 0 || statusRow < 0 || noticeRow >= statusRow {
        t.Fatalf("notice row=%d status row=%d\n%s", noticeRow, statusRow, view)
    }
}

func TestClickingNewMessageNoticeGoesToBottomAndClears(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)
    model.newMessageNoticeCount = 2
    bounds := model.transcriptNoticeBounds()
    x, y := bounds.x+bounds.width/2, bounds.y

    next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
    model = next.(appModel)
    next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
    model = next.(appModel)

    if model.newMessageNoticeCount != 0 || !model.viewport.AtBottom() {
        t.Fatalf("after click: count=%d atBottom=%v", model.newMessageNoticeCount, model.viewport.AtBottom())
    }
}
~~~

- [ ] Step 2: 运行失败测试

Run:

~~~bash
go test ./internal/ui/bubble -run 'Test(ViewRendersNewMessageNotice|ClickingNewMessageNotice)' -count=1
~~~

Expected: 提示行不存在，点击不会清零或跳到底部。

- [ ] Step 3: 在 transcript region 添加不改变高度的 overlay

在 renderTranscriptRegion 的 modal/completion 提前返回之后、最终 return 之前加入：

~~~go
if notice := m.renderNewMessageNotice(layout.contentWidth); notice != "" {
    base = placeOpaqueOverlay(
        base,
        notice,
        layout.contentWidth,
        layout.transcriptHeight,
        overlayAlignBottom,
    )
}
return fitStyledRect(base, layout.contentWidth, layout.transcriptHeight)
~~~

不要修改 dockStatusHeight、statusHeight 或 relayout，确保提示出现时 viewport 高度和偏移不变。

- [ ] Step 4: 在 transcript selection 之前处理提示 hover/click

实现 bounds.contains 和 handleNewMessageNoticeMouse。行为必须满足：

~~~go
if m.selecting || m.newMessageNoticeCount <= 0 {
    return m, false, nil
}
~~~

motion 只更新 hovered；左键 press 只在提示区域设置 pressed；左键 release 只有 press/release 都在提示区域时才执行：

~~~go
m.selectionActive = false
m.selecting = false
m.viewport.GotoBottom()
m.clearNewMessageNotice()
m.refreshViewport()
~~~

在 app.go 的 tea.MouseMsg 分支中先调用提示 handler，再调用 handleTranscriptMouse。普通 transcript mouse handler 返回后调用 next.syncNewMessageNoticeAfterScroll()。拖拽从 transcript 其他位置开始时，提示 handler 必须让 selecting 流程继续。

- [ ] Step 5: 运行 overlay、命中和既有鼠标测试

Run:

~~~bash
gofmt -w internal/ui/bubble/app.go internal/ui/bubble/layout.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/new_message_notice_test.go
go test ./internal/ui/bubble -run 'Test(ViewRendersNewMessageNotice|ClickingNewMessageNotice|MouseWheel|HorizontalMouseWheel|ArrowKeysAfterTranscriptWheel)' -count=1
~~~

Expected: 新提示测试和既有滚轮/选择测试 PASS；提示不改变 transcript/status 行相对顺序。

## Task 4: 接入所有回到底部路径和会话清理

Files:
- Modify: internal/ui/bubble/app.go:390-478
- Modify: internal/ui/bubble/command_registry.go:167-185
- Modify: internal/ui/bubble/subagent_picker.go:423-465
- Modify: internal/ui/bubble/new_message_notice.go
- Modify: internal/ui/bubble/new_message_notice_test.go

- [ ] Step 1: 写滚动清零和 selection 失败测试

~~~go
func TestScrollingToBottomClearsNewMessageNotice(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)
    model.newMessageNoticeCount = 2
    model.transcriptKeyScrollActive = true

    for !model.viewport.AtBottom() {
        next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
        model = next.(appModel)
    }
    if model.newMessageNoticeCount != 0 {
        t.Fatalf("notice count after down-to-bottom = %d, want 0", model.newMessageNoticeCount)
    }
}

func TestSelectionDoesNotPauseNewMessageNotice(t *testing.T) {
    model := newTranscriptScrollTestModel()
    model.viewport.SetYOffset(3)
    model.selectionActive = true
    next, _ := model.Update(systemEventMsg(ui.SystemEvent{Title: "后台任务", Body: "完成"}))
    model = next.(appModel)
    if model.newMessageNoticeCount != 1 {
        t.Fatalf("selection notice count = %d, want 1", model.newMessageNoticeCount)
    }
}
~~~

- [ ] Step 2: 运行失败测试

Run:

~~~bash
go test ./internal/ui/bubble -run 'Test(ScrollingToBottom|SelectionDoesNotPause)' -count=1
~~~

Expected: 滚到底部后计数仍存在，selection 场景未覆盖通知清零/继续计数语义，新增测试失败或暴露未接入路径。

- [ ] Step 3: 在所有滚动入口同步底部清零

在 app.go 中调用 syncNewMessageNoticeAfterScroll：

1. transcriptKeyScrollActive 的 up/down 分支，尤其是 down 后。
2. transcript mouse wheel 更新 viewport 后。
3. 普通 handleTranscriptMouse 返回后。
4. 通用 m.viewport.Update(msg) 完成后，以覆盖 Bubble viewport 的 End 等内建按键。

不要在 WindowSizeMsg 或 cursorFrameMsg 中调用清零，resize 和动画不能隐藏未读提示。

- [ ] Step 4: 在清理、恢复和切换 session 时重置通知状态

在 /clear handler 清空 transcript 后调用 clearNewMessageNotice，并在 history cleared 系统消息写入后再次调用 clearNewMessageNotice，保证清理反馈不继承旧计数。

在 applySessionPickerRestore、applySubagentPreviewRestore 和 restoreMainTranscriptFromSubagentPreview 替换 transcript 前调用 clearNewMessageNotice，保证恢复的历史和 preview 内容不被当成新消息。

- [ ] Step 5: 运行清理、滚动和全量 Bubble 测试

Run:

~~~bash
gofmt -w internal/ui/bubble/app.go internal/ui/bubble/command_registry.go internal/ui/bubble/subagent_picker.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/new_message_notice_test.go
go test ./internal/ui/bubble -count=1
~~~

Expected: internal/ui/bubble 全部 PASS。

## Task 5: 完成回归验证和真实 TUI 验收

- [ ] Step 1: 运行项目级测试和 diff 检查

Run:

~~~bash
go test ./... -count=1
git diff --check
~~~

Expected: 所有 Go 包测试 PASS，git diff --check 无输出。

- [ ] Step 2: 运行真实 Paw TUI

使用项目实际入口：

~~~bash
go run cmd/agent/main.go
~~~

手工验证：

1. 产生足够长的 transcript，滚轮向上离开底部。
2. 等待 assistant 继续输出，确认 viewport 不跳动，底部出现居中的“↓ N 条新消息”。
3. 将鼠标移到提示，确认背景色增强并显示“回到底部”。
4. 点击提示，确认跳到最新消息且提示消失。
5. 使用 End、方向键和滚轮分别回到底部，确认每条路径都清零。
6. 在文本选择/复制历史消息时等待 tool result 或 system event，确认计数继续增加。
7. 调整终端窗口尺寸，确认提示不改变 viewport 高度、不重复计数。

- [ ] Step 3: 复核范围和用户改动

Run:

~~~bash
git status --short
git diff --stat
~~~

确认新增/修改内容只涉及本功能及其测试；保留工作区中原有的 Paw 重命名、MCP、smart-scroll 和其他用户改动，不使用 reset、checkout 或清理命令覆盖它们。

## 实施顺序摘要

1. 先建立可测试的文案、背景样式、居中和命中区域。
2. 再接入 assistant/thinking/tool/system 等逻辑事件计数。
3. 然后把提示作为 transcript overlay 接入 View，并处理鼠标 hover/click。
4. 最后接入所有回到底部和 session/history 清理路径，运行全量验证。
