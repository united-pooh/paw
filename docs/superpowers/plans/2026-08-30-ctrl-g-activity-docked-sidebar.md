# Ctrl+G Activity Docked Sidebar 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 把 `Ctrl+G` Activity 从覆盖 transcript 的右侧浮层改成主 hairline frame 内的全高 docked sidebar，并补齐非模态焦点、Vim pane 导航、task preview 与窄屏全页行为。

**架构：** 用纯函数计算 Activity 的 hidden/docked/fullscreen 几何；把 Activity 的可见性、焦点、宽度、页签和 task 身份保存为独立状态；`View` 分别渲染左 workspace 与右 Activity，再用 `│/┬/┴` 拼成单一固定矩形。现有 task/todo 数据源和 task transcript loader 保持不变，modal/completion 仍只在左 workspace 内合成。

**技术栈：** Go、Bubble Tea、Bubbles textarea/viewport、Lip Gloss、`github.com/charmbracelet/x/ansi`、标准库 `testing`。

**规格：** `docs/plans/2026-08-30-ctrl-g-activity-docked-sidebar-design.md`（Approved，commits `ce74d39`、`90db569`）

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/ui/bubble/activity_layout.go` | 创建 | Activity 宽度常量、hidden/docked/fullscreen 纯几何、workspace/activity 精确矩形横向拼接、split hairline |
| `internal/ui/bubble/activity_layout_test.go` | 创建 | 85/84 列边界、36% 默认宽度、32–52 clamp、4 列 resize、ANSI/宽字符拼接测试 |
| `internal/ui/bubble/activity_navigation_test.go` | 创建 | Ctrl+G、Ctrl+W h/l/</>、Esc、焦点与按 task ID 保持选择的交互测试 |
| `internal/ui/bubble/activity_dock_test.go` | 创建 | View 分栏、全高分隔线、无 overlay/nested border、关闭态提示、modal/completion 左栏约束测试 |
| `internal/ui/bubble/task_preview_dock_test.go` | 创建 | docked/fullscreen task preview、错误快照、提交回主 session 测试 |
| `internal/ui/bubble/types.go` | 修改 | `activityState`、pane focus、command prefix、layout mode；`taskTranscriptPreview.loadError` |
| `internal/ui/bubble/app.go` | 修改 | 初始化 Activity 状态；把 pane 全局键路由放在现有 modal/picker 之后、普通输入之前；preview load error 路由 |
| `internal/ui/bubble/task_picker.go` | 修改 | Activity 显示/隐藏、focus、resize、页签/任务导航、按 ID 刷新选择、preview 状态与错误恢复 |
| `internal/ui/bubble/activity.go` | 修改 | 把有四边框的 `renderActivityBox` 改成精确宽高、无外框的 `renderActivityPane`；Tasks/Todo 页签注册和 footer |
| `internal/ui/bubble/right_panel.go` | 修改 | Task 行改为 Activity pane 使用的两行信息布局，并读取 `activityState` 的选择与 preview 标识 |
| `internal/ui/bubble/layout.go` | 修改 | `tuiLayout` 增加 Activity 几何；workspace 独立渲染；docked/fullscreen View 分支；移除 Activity/task card overlay；cursor anchor 使用 workspace 宽度 |
| `internal/ui/bubble/status_line.go` | 修改 | 底边框拆成 workspace 段和 Activity/worktree 段，保持 mode/token/worktree 锚点 |
| `internal/ui/bubble/header.go` | 修改 | Activity 打开时的右侧标题段、关闭时 running task 边框提示 |
| `internal/ui/bubble/task_card.go` | 修改 | 删除悬浮卡与 overlay，只保留 `runningTasks`/`hasRunningTasks` 活跃任务查询 |
| `internal/ui/bubble/task_card_test.go` | 修改 | 删除卡片视觉断言，保留 active-process 语义并改测 hairline running 提示 |
| `internal/ui/bubble/activity_side_panel_test.go` | 删除 | 旧测试绑定了 overlay 位置和四边框宽度，改由 `activity_dock_test.go` 覆盖 |
| `internal/ui/bubble/fixed_layout_test.go` | 修改 | Activity 命令、frame invariant、窄屏 fullscreen 与 pane focus 回归 |
| `internal/ui/bubble/bubble_test.go` | 修改 | 更新原有 Ctrl+G/task preview/task update 测试的状态字段和新语义 |
| `internal/ui/bubble/new_message_notice.go` | 修改 | Activity docked 时允许新消息 notice 在左 workspace 内渲染 |
| `internal/ui/bubble/cursor_animation.go` | 修改 | Activity 动画条件读取新状态 |
| `README.md` | 修改 | 更新 Ctrl+G、Ctrl+W pane 导航、调宽、preview 与窄屏说明 |

不改：`internal/task`、session/todo 持久化、工具协议、主题颜色角色、`placeOpaqueOverlay` 的其他 modal/completion 使用者。

---

### 任务 1：建立 Activity 纯几何模型

**文件：**
- 创建：`internal/ui/bubble/activity_layout.go`
- 创建：`internal/ui/bubble/activity_layout_test.go`
- 修改：`internal/ui/bubble/layout.go:21-74`
- 修改：`internal/ui/bubble/types.go:656-663`

- [ ] **步骤 1：编写失败的几何测试**

创建 `internal/ui/bubble/activity_layout_test.go`：

```go
package bubble

import "testing"

func TestComputeActivityGeometryHiddenAndBreakpoint(t *testing.T) {
	hidden := computeActivityGeometry(120, false, 0)
	if hidden.mode != activityLayoutHidden || hidden.workspaceWidth != 120 || hidden.activityWidth != 0 || hidden.separatorWidth != 0 {
		t.Fatalf("hidden = %+v", hidden)
	}

	docked := computeActivityGeometry(85, true, 0)
	if docked.mode != activityLayoutDocked || docked.workspaceWidth != 52 || docked.activityWidth != 32 || docked.separatorWidth != 1 {
		t.Fatalf("85 columns = %+v, want 52+1+32 dock", docked)
	}

	fullscreen := computeActivityGeometry(84, true, 0)
	if fullscreen.mode != activityLayoutFullscreen || fullscreen.activityWidth != 84 || fullscreen.separatorWidth != 0 {
		t.Fatalf("84 columns = %+v, want fullscreen", fullscreen)
	}
}

func TestComputeActivityGeometryDefaultAndClamp(t *testing.T) {
	cases := []struct {
		width, requested, wantActivity int
	}{
		{100, 0, 36},
		{120, 0, 43},
		{200, 0, 52},
		{120, 12, 32},
		{120, 90, 52},
		{90, 52, 37}, // workspace 最少保留 52，另留 1 列 separator。
	}
	for _, tc := range cases {
		got := computeActivityGeometry(tc.width, true, tc.requested)
		if got.activityWidth != tc.wantActivity {
			t.Fatalf("width=%d requested=%d activity=%d, want %d: %+v", tc.width, tc.requested, got.activityWidth, tc.wantActivity, got)
		}
		if got.workspaceWidth+got.separatorWidth+got.activityWidth != tc.width {
			t.Fatalf("geometry does not fill width: %+v", got)
		}
	}
}

func TestResizeActivityWidthUsesFourColumnStep(t *testing.T) {
	if got := resizeActivityWidth(40, -1); got != 36 {
		t.Fatalf("shrink = %d, want 36", got)
	}
	if got := resizeActivityWidth(40, 1); got != 44 {
		t.Fatalf("grow = %d, want 44", got)
	}
	if got := resizeActivityWidth(32, -1); got != 32 {
		t.Fatalf("min clamp = %d, want 32", got)
	}
	if got := resizeActivityWidth(52, 1); got != 52 {
		t.Fatalf("max clamp = %d, want 52", got)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestComputeActivityGeometry|TestResizeActivityWidth' -v
```

预期：编译失败，报 `undefined: computeActivityGeometry`、`undefined: activityLayoutHidden`。

- [ ] **步骤 3：实现常量、模式和纯几何**

在 `internal/ui/bubble/types.go` 的 `activityTab` 前加入：

```go
type activityLayoutMode uint8

const (
	activityLayoutHidden activityLayoutMode = iota
	activityLayoutDocked
	activityLayoutFullscreen
)
```

创建 `internal/ui/bubble/activity_layout.go`：

```go
package bubble

const (
	activityDefaultPercent = 36
	activityMinWidth        = 32
	activityMaxWidth        = 52
	activityWorkspaceMin    = 52
	activitySeparatorWidth  = 1
	activityResizeStep      = 4
	activityDockMinWidth    = activityWorkspaceMin + activitySeparatorWidth + activityMinWidth
)

type activityGeometry struct {
	mode             activityLayoutMode
	workspaceWidth   int
	activityWidth    int
	separatorWidth   int
}

func computeActivityGeometry(fullWidth int, visible bool, requestedWidth int) activityGeometry {
	fullWidth = maxInt(1, fullWidth)
	if !visible {
		return activityGeometry{mode: activityLayoutHidden, workspaceWidth: fullWidth}
	}
	if fullWidth < activityDockMinWidth {
		return activityGeometry{mode: activityLayoutFullscreen, activityWidth: fullWidth}
	}
	width := requestedWidth
	if width <= 0 {
		width = (fullWidth*activityDefaultPercent + 50) / 100
	}
	maxForWorkspace := fullWidth - activitySeparatorWidth - activityWorkspaceMin
	width = clampInt(width, activityMinWidth, minInt(activityMaxWidth, maxForWorkspace))
	return activityGeometry{
		mode:             activityLayoutDocked,
		workspaceWidth:   fullWidth - activitySeparatorWidth - width,
		activityWidth:    width,
		separatorWidth:   activitySeparatorWidth,
	}
}

func resizeActivityWidth(width, direction int) int {
	if width <= 0 {
		width = activityMinWidth
	}
	return clampInt(width+direction*activityResizeStep, activityMinWidth, activityMaxWidth)
}
```

扩展 `internal/ui/bubble/layout.go:tuiLayout`：

```go
	workspaceWidth        int
	activityWidth         int
	activitySeparatorWidth int
	activityMode          activityLayoutMode
```

新增：

```go
func applyActivityGeometry(layout tuiLayout, visible bool, requestedWidth int) tuiLayout {
	geometry := computeActivityGeometry(layout.contentWidth, visible, requestedWidth)
	layout.workspaceWidth = geometry.workspaceWidth
	layout.activityWidth = geometry.activityWidth
	layout.activitySeparatorWidth = geometry.separatorWidth
	layout.activityMode = geometry.mode
	return layout
}
```

`computeTUILayoutWithInputLimit` 返回前把 `workspaceWidth` 初始化为 `contentWidth`，确保尚未接入 Activity state 的调用点仍安全：

```go
		workspaceWidth:   contentWidth,
		activityMode:     activityLayoutHidden,
```

- [ ] **步骤 4：运行测试验证通过**

```bash
go test ./internal/ui/bubble -run 'TestComputeActivityGeometry|TestResizeActivityWidth|TestComputeTUILayoutKeepsOuterFrameStable' -v
```

预期：全部 PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/activity_layout.go internal/ui/bubble/activity_layout_test.go internal/ui/bubble/layout.go internal/ui/bubble/types.go
git commit -m "feat(ui): add pure Activity dock geometry"
```

---

### 任务 2：把 Activity 可见性、焦点和 task 身份改成持久状态

**文件：**
- 创建：`internal/ui/bubble/activity_navigation_test.go`
- 修改：`internal/ui/bubble/types.go:462-466, 641`
- 修改：`internal/ui/bubble/app.go:27-110, 500-560, 665-735`
- 修改：`internal/ui/bubble/task_picker.go:36-120, 568-608, 733-766`
- 修改：`internal/ui/bubble/activity.go:11-41`
- 修改：`internal/ui/bubble/layout.go:326-348, 679-690`
- 修改：`internal/ui/bubble/right_panel.go:34-112`
- 修改：`internal/ui/bubble/new_message_notice.go:82-101`
- 修改：`internal/ui/bubble/cursor_animation.go:110-122`
- 修改：`internal/ui/bubble/bubble_test.go`、`fixed_layout_test.go` 中直接访问 `taskPicker` 的断言

- [ ] **步骤 1：编写失败的状态与刷新测试**

创建 `internal/ui/bubble/activity_navigation_test.go`：

```go
package bubble

import (
	"testing"

	"paw/internal/task"
)

func TestOpenAndCloseActivityPreserveWidthTabAndSelection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 120
	model.height = 30
	model.ready = true
	model.activity.widthColumns = 44
	model.activity.tab = activityTabTodo
	model.activity.selectedTaskID = "task-2"

	model.openActivity(activityTabTasks)
	if !model.activity.visible || model.activity.focus != activityFocusPanel || model.activity.tab != activityTabTasks {
		t.Fatalf("opened activity = %+v", model.activity)
	}
	model.closeActivity()
	if model.activity.visible || model.activity.focus != activityFocusWorkspace {
		t.Fatalf("closed activity = %+v", model.activity)
	}
	if model.activity.widthColumns != 44 || model.activity.selectedTaskID != "task-2" {
		t.Fatalf("persistent fields lost: %+v", model.activity)
	}
}

func TestRefreshActivityTasksPreservesSelectionByID(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.tasks = []task.TaskSnapshot{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	model.activity.selectedIndex = 1
	model.activity.selectedTaskID = "b"
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "c"}, {ID: "b"}, {ID: "a"}}}

	model.refreshActivityTasks()
	if model.activity.selectedIndex != 1 || model.activity.selectedTaskID != "b" {
		t.Fatalf("selection after reorder = %+v", model.activity)
	}

	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "c"}, {ID: "a"}}}
	model.refreshActivityTasks()
	if model.activity.selectedIndex != 1 || model.activity.selectedTaskID != "a" {
		t.Fatalf("selection after removal = %+v, want adjacent task a", model.activity)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestOpenAndCloseActivityPreserve|TestRefreshActivityTasksPreserves' -v
```

预期：编译失败，`appModel` 没有 `activity` 字段。

- [ ] **步骤 3：定义 `activityState` 并初始化**

在 `internal/ui/bubble/types.go` 用以下类型替换 `taskPicker`：

```go
type activityPaneFocus uint8

const (
	activityFocusWorkspace activityPaneFocus = iota
	activityFocusPanel
)

type activityCommandPrefix uint8

const (
	activityCommandIdle activityCommandPrefix = iota
	activityCommandCtrlW
)

type activityState struct {
	visible            bool
	focus              activityPaneFocus
	tab                activityTab
	widthColumns       int
	tasks              []task.TaskSnapshot
	selectedIndex      int
	selectedTaskID     string
	commandPrefix      activityCommandPrefix
}
```

把 `appModel.taskPicker *taskPicker` 改为：

```go
	activity activityState
```

在 `newModel` 的 struct literal 中初始化：

```go
		activity: activityState{
			focus: activityFocusWorkspace,
			tab:   activityTabTasks,
		},
```

- [ ] **步骤 4：改写打开、关闭和按 ID 刷新**

在 `internal/ui/bubble/task_picker.go` 用以下核心实现替换 `newTaskPicker/openActivity/refreshActivityTasks`：

```go
func (m *appModel) openActivity(tab activityTab) {
	if m == nil {
		return
	}
	m.activity.visible = true
	m.activity.focus = activityFocusPanel
	m.activity.tab = tab
	m.activity.commandPrefix = activityCommandIdle
	m.sessionPicker = nil
	m.modelWizard = nil
	m.settingWizard = nil
	m.clearCompletionAndRelayout()
	m.refreshActivityTasks()
	m.input.Blur()
	m.relayout()
	m.refreshViewport()
}

func (m *appModel) closeActivity() {
	if m == nil {
		return
	}
	m.activity.visible = false
	m.activity.focus = activityFocusWorkspace
	m.activity.commandPrefix = activityCommandIdle
	m.input.Focus()
	m.relayout()
	m.refreshViewport()
}

func (m *appModel) refreshActivityTasks() {
	if m == nil || !m.activity.visible {
		return
	}
	oldIndex := m.activity.selectedIndex
	selectedID := strings.TrimSpace(m.activity.selectedTaskID)
	m.activity.tasks = append(m.activity.tasks[:0], m.taskEntries()...)
	if len(m.activity.tasks) == 0 {
		m.activity.selectedIndex = 0
		m.activity.selectedTaskID = ""
		return
	}
	if selectedID != "" {
		for index, snapshot := range m.activity.tasks {
			if strings.TrimSpace(snapshot.ID) == selectedID {
				m.activity.selectedIndex = index
				return
			}
		}
	}
	m.activity.selectedIndex = clampInt(oldIndex, 0, len(m.activity.tasks)-1)
	m.activity.selectedTaskID = strings.TrimSpace(m.activity.tasks[m.activity.selectedIndex].ID)
}

func (m *appModel) selectActivityTask(index int) {
	if len(m.activity.tasks) == 0 {
		m.activity.selectedIndex = 0
		m.activity.selectedTaskID = ""
		return
	}
	m.activity.selectedIndex = clampInt(index, 0, len(m.activity.tasks)-1)
	m.activity.selectedTaskID = strings.TrimSpace(m.activity.tasks[m.activity.selectedIndex].ID)
}

// 过渡实现保持当前独占键盘语义；任务 3 将 Ctrl+G/Esc 提升为 pane 全局命令。
func (m appModel) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+g", "esc":
		m.closeActivity()
		return m, m.input.Focus()
	case "tab", "right", "l":
		m.activity.tab = activityTabTodo
		return m, nil
	case "shift+tab", "left", "h":
		m.activity.tab = activityTabTasks
		return m, nil
	case "up", "k":
		if m.activity.tab == activityTabTasks {
			m.selectActivityTask(m.activity.selectedIndex - 1)
		}
		return m, nil
	case "down", "j":
		if m.activity.tab == activityTabTasks {
			m.selectActivityTask(m.activity.selectedIndex + 1)
		}
		return m, nil
	case "enter":
		if m.activity.tab != activityTabTasks || len(m.activity.tasks) == 0 {
			return m, nil
		}
		selected := clampInt(m.activity.selectedIndex, 0, len(m.activity.tasks)-1)
		return m.previewTaskTranscript(m.activity.tasks[selected])
	}
	return m, nil
}
```

完成状态替换后运行：

```bash
rg -n 'taskPicker|newTaskPicker|handleTaskPickerKey' internal/ui/bubble
```

逐个按以下规则清零结果：

- `activity.go`：nil 判断改为 `!m.activity.visible`，tab 改为 `m.activity.tab`。
- `right_panel.go`：选择下标改为 `m.activity.selectedIndex`，只在 visible 时画选中态。
- `layout.go`：旧 overlay 条件暂时改为 `m.activity.visible`；任务 5 会彻底删除 overlay。
- `new_message_notice.go`：本任务暂时用 `!m.activity.visible` 保持旧语义；任务 3 再允许 docked notice。
- `cursor_animation.go`：遍历 `m.activity.tasks`，且前置条件为 `m.activity.visible`。
- `app.go` 和 `task_picker.go`：函数名统一为 `handleActivityKey`，所有关闭动作调用 `closeActivity`。
- `applyTaskPreviewRestore`、`restoreMainTranscriptFromTaskPreview`：把直接给 picker 指针赋 nil 改成过渡性的 `m.closeActivity()`；任务 6 再删除这些过渡关闭动作，锁定最终 preview 语义。
- 测试：`model.taskPicker == nil` 改为 `!model.activity.visible`；tab/tasks/selectedIndex 改读 `model.activity`。

命令最终必须无输出；不要保留兼容 alias 或第二份状态。

- [ ] **步骤 5：运行状态测试和相关旧测试**

```bash
go test ./internal/ui/bubble -run 'TestOpenAndCloseActivityPreserve|TestRefreshActivityTasksPreserves|TestTaskTaskUpdateMsgRefreshesActivityAndPreview' -v
```

预期：新增测试 PASS；旧 task update 测试在本步骤同步字段名后 PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/activity_navigation_test.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/task_picker.go internal/ui/bubble/activity.go internal/ui/bubble/layout.go internal/ui/bubble/right_panel.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/cursor_animation.go internal/ui/bubble/bubble_test.go internal/ui/bubble/fixed_layout_test.go
git commit -m "refactor(ui): separate Activity state from modal presence"
```

---

### 任务 3：实现 Ctrl+G、Ctrl+W pane chord 与非模态键路由

**文件：**
- 修改：`internal/ui/bubble/activity_navigation_test.go`
- 修改：`internal/ui/bubble/task_picker.go:65-108`
- 修改：`internal/ui/bubble/app.go:665-735`
- 修改：`internal/ui/bubble/new_message_notice.go:82-101`

- [ ] **步骤 1：追加失败的键盘测试**

追加到 `activity_navigation_test.go`：

```go
func TestActivityGlobalKeysToggleFocusAndResize(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = next.(appModel)
	if !model.activity.visible || model.activity.focus != activityFocusPanel {
		t.Fatalf("ctrl+g open = %+v", model.activity)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = next.(appModel)
	if model.activity.focus != activityFocusWorkspace || !model.input.Focused() {
		t.Fatalf("ctrl+w h = %+v focused=%v", model.activity, model.input.Focused())
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = next.(appModel)
	if model.activity.focus != activityFocusPanel || model.input.Focused() {
		t.Fatalf("ctrl+w l = %+v focused=%v", model.activity, model.input.Focused())
	}

	model.activity.widthColumns = 40
	for _, key := range []rune{'<', '>'} {
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
		model = next.(appModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		model = next.(appModel)
	}
	if model.activity.widthColumns != 40 {
		t.Fatalf("shrink+grow width = %d, want 40", model.activity.widthColumns)
	}
}

func TestClosedActivityDoesNotInterceptCtrlW(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if _, handled, _ := model.handleActivityGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlW}); handled {
		t.Fatal("closed Activity intercepted Ctrl+W")
	}
}

func TestActivityInvalidCtrlWChordIsConsumed(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	model.activity.focus = activityFocusWorkspace
	model.input.Focus()
	model.input.SetValue("draft")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = next.(appModel)
	if model.input.Value() != "draft" || model.activity.commandPrefix != activityCommandIdle {
		t.Fatalf("invalid chord leaked: value=%q activity=%+v", model.input.Value(), model.activity)
	}
}
```

补 import：

```go
tea "github.com/charmbracelet/bubbletea"
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestActivityGlobalKeys|TestActivityInvalidCtrlWChord' -v
```

预期：FAIL；`Ctrl+W` 尚未设置 prefix，Activity 仍独占所有按键。

- [ ] **步骤 3：实现全局 pane key handler**

在 `task_picker.go` 新增：

```go
func (m appModel) handleActivityGlobalKey(msg tea.KeyMsg) (appModel, bool, tea.Cmd) {
	keyName := msg.String()
	if keyName == "ctrl+g" {
		if m.activity.visible {
			m.closeActivity()
		} else {
			m.openActivity(m.activity.tab)
		}
		return m, true, nil
	}
	if !m.activity.visible {
		return m, false, nil
	}
	if m.activity.commandPrefix == activityCommandCtrlW {
		m.activity.commandPrefix = activityCommandIdle
		switch keyName {
		case "h":
			m.activity.focus = activityFocusWorkspace
			return m, true, m.input.Focus()
		case "l":
			if m.currentLayout().activityMode == activityLayoutDocked {
				m.activity.focus = activityFocusPanel
				m.input.Blur()
			}
			return m, true, nil
		case "<":
			if m.currentLayout().activityMode == activityLayoutDocked {
				m.activity.widthColumns = resizeActivityWidth(m.activity.widthColumns, -1)
				m.relayout()
				m.refreshViewport()
			}
			return m, true, nil
		case ">":
			if m.currentLayout().activityMode == activityLayoutDocked {
				m.activity.widthColumns = resizeActivityWidth(m.activity.widthColumns, 1)
				m.relayout()
				m.refreshViewport()
			}
			return m, true, nil
		default:
			return m, true, nil
		}
	}
	if keyName == "ctrl+w" {
		m.activity.commandPrefix = activityCommandCtrlW
		return m, true, nil
	}
	if keyName == "esc" {
		if m.currentLayout().activityMode == activityLayoutFullscreen {
			m.closeActivity()
			return m, true, m.input.Focus()
		}
		if m.taskPreview != nil {
			m.restoreMainTranscriptFromTaskPreview()
			return m, true, nil
		}
		if m.activity.focus == activityFocusPanel {
			m.activity.focus = activityFocusWorkspace
			return m, true, m.input.Focus()
		}
	}
	return m, false, nil
}
```

用以下函数替换原 `handleTaskPickerKey`；它只处理 Activity pane 自身的页签、任务导航和 Enter，不处理 `Ctrl+G`/`Esc`：

```go
func (m appModel) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "right", "l":
		m.activity.tab = activityTabTodo
		return m, nil
	case "shift+tab", "left", "h":
		m.activity.tab = activityTabTasks
		return m, nil
	case "up", "k":
		if m.activity.tab == activityTabTasks {
			m.selectActivityTask(m.activity.selectedIndex - 1)
		}
		return m, nil
	case "down", "j":
		if m.activity.tab == activityTabTasks {
			m.selectActivityTask(m.activity.selectedIndex + 1)
		}
		return m, nil
	case "enter":
		if m.activity.tab != activityTabTasks || len(m.activity.tasks) == 0 {
			return m, nil
		}
		selected := clampInt(m.activity.selectedIndex, 0, len(m.activity.tasks)-1)
		return m.previewTaskTranscript(m.activity.tasks[selected])
	}
	return m, nil
}
```

- [ ] **步骤 4：调整 `appModel.Update` 的优先级**

在 `app.go` 中保留 selection/theme/config/setting/model/session picker 的现有优先级；在这些分支之后、tool inspect/queue/completion/普通输入之前加入：

```go
		if next, handled, cmd := m.handleActivityGlobalKey(msg); handled {
			return next, cmd
		}
		if m.activity.visible && m.activity.focus == activityFocusPanel {
			return m.handleActivityKey(msg)
		}
```

删除旧的 `if m.taskPicker != nil` 独占路由分支，以及 task preview 中按 `ctrl+g` 直接调用 `restoreMainTranscriptFromTaskPreview` 的分支；`Ctrl+G` 只能由 `handleActivityGlobalKey` 改变 Activity 可见性。

`newMessageNoticeCanRender` 不再因 Activity 可见而返回 false；notice 会在任务 5 中随 transcript 一起限制在 workspace 宽度。

- [ ] **步骤 5：运行交互测试**

```bash
go test ./internal/ui/bubble -run 'TestActivityGlobalKeys|TestActivityInvalidCtrlWChord|TestActivityCommandsAndTabs' -v
```

预期：全部 PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/activity_navigation_test.go internal/ui/bubble/task_picker.go internal/ui/bubble/app.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/fixed_layout_test.go
git commit -m "feat(ui): add Vim Activity pane navigation"
```

---

### 任务 4：把 Activity 内容改成无外框的精确 pane

**文件：**
- 创建：`internal/ui/bubble/activity_dock_test.go`
- 修改：`internal/ui/bubble/activity.go:11-123`
- 修改：`internal/ui/bubble/right_panel.go:15-180`

- [ ] **步骤 1：编写失败的 pane 渲染测试**

创建 `internal/ui/bubble/activity_dock_test.go`：

```go
package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/task"
)

func TestRenderActivityPaneHasExactSizeAndNoNestedBorder(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tab = activityTabTasks
	model.activity.tasks = []task.TaskSnapshot{{
		ID: "worker-1", Name: "layout-research", Status: task.TaskRunning,
		StartedAt: time.Now().Add(-84 * time.Second), UsedTokens: 8200,
	}}
	model.taskController = &fakeTaskController{tasks: append([]task.TaskSnapshot(nil), model.activity.tasks...)}
	model.activity.selectedTaskID = "worker-1"

	rendered := model.renderActivityPane(40, 18)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 18 {
		t.Fatalf("height=%d, want 18", len(lines))
	}
	for row, line := range lines {
		if got := terminalCellWidth(line); got != 40 {
			t.Fatalf("row=%d width=%d, want 40: %q", row, got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Activity", "Tasks 1", "Todo", "layout-research", "running", "8.2k"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pane missing %q:\n%s", want, plain)
		}
	}
	for _, banned := range []string{"╭", "╮", "╰", "╯", "┌", "┐", "└", "┘"} {
		if strings.Contains(plain, banned) {
			t.Fatalf("nested border %q found:\n%s", banned, plain)
		}
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run TestRenderActivityPaneHasExactSizeAndNoNestedBorder -v
```

预期：编译失败，`renderActivityPane` 未定义。

- [ ] **步骤 3：实现页签注册与无框 pane**

在 `activity.go` 定义轻量页签表：

```go
var activityPages = []struct {
	id    activityTab
	title string
}{
	{id: activityTabTasks, title: "Tasks"},
	{id: activityTabTodo, title: "Todo"},
}
```

新增以下 pane 入口；本任务暂时保留 `activityPanelWidth/activityPanelHeight` 供 overlay 兼容 wrapper 使用，任务 5 删除 wrapper 和这两个旧尺寸函数：

```go
func (m appModel) renderActivityPane(width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	headerHeight := minInt(2, height)
	footerHeight := 0
	if height >= 4 {
		footerHeight = 1
	}
	bodyHeight := maxInt(0, height-headerHeight-footerHeight)

	focus := ""
	if m.activity.focus == activityFocusPanel {
		focus = activityFocusStyle.Render("FOCUSED")
	}
	title := renderSidebarRow(width, "Activity", focus, wizardTitleStyle, activityFocusStyle)
	tabs := m.renderActivityTabs(width)

	body := ""
	switch m.activity.tab {
	case activityTabTodo:
		body = m.renderActivityTodo(width, bodyHeight)
	default:
		body = m.renderActivityTasks(width, bodyHeight)
	}
	parts := []string{title}
	if headerHeight > 1 {
		parts = append(parts, tabs)
	}
	if bodyHeight > 0 {
		parts = append(parts, fitStyledRect(body, width, bodyHeight))
	}
	if footerHeight > 0 {
		parts = append(parts, activityHintStyle.Render(truncateStyledCellLine(m.activityFooterHint(), width)))
	}
	return fitStyledRect(strings.Join(parts, "\n"), width, height)
}

// 任务 5 接入真实 dock 前保留兼容入口，使本任务可独立编译和测试。
func (m appModel) renderActivityBox() string {
	if !m.activity.visible {
		return ""
	}
	return m.renderActivityPane(m.activityPanelWidth(), m.activityPanelHeight())
}

func (m appModel) renderActivityTabs(width int) string {
	labels := make([]string, 0, len(activityPages))
	for _, page := range activityPages {
		label := page.title
		switch page.id {
		case activityTabTasks:
			label += " " + strconv.Itoa(len(m.activity.tasks))
		case activityTabTodo:
			if m.hasCurrentTodo && !m.currentTodo.Cleared() {
				label += fmt.Sprintf(" %d/%d", m.currentTodo.CompletedCount(), m.currentTodo.TotalCount())
			}
		}
		style := unselectedProviderStyle
		if page.id == m.activity.tab {
			style = m.styles.SelectionSelected
		}
		labels = append(labels, style.Render(" "+label+" "))
	}
	return truncateStyledCellLine(strings.Join(labels, " "), width)
}

func (m appModel) activityFooterHint() string {
	if m.activity.tab == activityTabTodo {
		return "Tab/←/→ page · Esc main"
	}
	return "↑/↓ select · Enter preview · Tab page · Esc main"
}
```

在 `activity.go` import 中加入 `strconv`。定义文件级样式：

```go
var (
	activityFocusStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorSignal)).Bold(true)
	activityHintStyle  = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
)
```

`renderActivityTasks` 改为读取 `m.activity.tasks`，并在 `right_panel.go` 加入以下两行 task renderer：

```go
var activityTaskSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m appModel) renderActivityTasks(width, height int) string {
	if m.taskController == nil {
		return fitStyledRect(labelErrorStyle.Render("Task controller is unavailable."), width, height)
	}
	tasks := m.activity.tasks
	if len(tasks) == 0 {
		return fitStyledRect(unselectedProviderStyle.Render("No tasks yet."), width, height)
	}
	visible := maxInt(1, height/2)
	selected := clampInt(m.activity.selectedIndex, 0, len(tasks)-1)
	start := clampInt(selected-visible+1, 0, maxInt(0, len(tasks)-visible))
	end := minInt(len(tasks), start+visible)
	lines := make([]string, 0, height)
	for index := start; index < end; index++ {
		first, second := m.renderActivityTaskRows(tasks[index], width, index == selected)
		lines = append(lines, first)
		if len(lines) < height {
			lines = append(lines, second)
		}
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func (m appModel) renderActivityTaskRows(snapshot task.TaskSnapshot, width int, selected bool) (string, string) {
	glyph := "✓"
	status := "done"
	right := ""
	switch snapshot.Status {
	case task.TaskRunning:
		glyph = activityTaskSpinnerFrames[m.spinnerFrameIdx%len(activityTaskSpinnerFrames)]
		status = "running"
		right = formatElapsedTime(time.Since(snapshot.StartedAt))
	case task.TaskFailed:
		glyph, status = "✗", "failed"
	case task.TaskStopped:
		glyph, status = "■", "stopped"
	case task.TaskInterrupted:
		glyph, status = "!", "interrupted"
	}
	name := taskDisplayName(snapshot)
	first := renderSidebarRow(width, glyph+" "+name, right, unselectedProviderStyle, unselectedProviderStyle)
	meta := status
	if snapshot.UsedTokens > 0 {
		meta += " · " + formatCompactTokenCount(snapshot.UsedTokens) + " tokens"
	}
	if m.taskPreview != nil && strings.TrimSpace(m.taskPreview.task.ID) == strings.TrimSpace(snapshot.ID) {
		meta += " · previewing on left"
	}
	if width < 40 {
		meta = status
		if m.taskPreview != nil && strings.TrimSpace(m.taskPreview.task.ID) == strings.TrimSpace(snapshot.ID) {
			meta += " · previewing"
		}
	}
	second := "  " + truncateStyledCellLine(meta, maxInt(1, width-2))
	if selected {
		return m.styles.SelectionSelected.Render(fitStyledCellLine(ansi.Strip(first), width)),
			m.styles.SelectionSelected.Render(fitStyledCellLine(second, width))
	}
	return fitStyledCellLine(first, width), unselectedProviderStyle.Render(fitStyledCellLine(second, width))
}
```

`right_panel.go` import 增加 `github.com/charmbracelet/x/ansi`；继续复用现有 `formatElapsedTime`、`renderSidebarRow`、`taskDisplayName` 和 token compact formatter。删除 `activity.go` 末尾旧的 `renderActivityTasks`，避免与新 renderer 重复定义。

- [ ] **步骤 4：运行 pane 测试**

```bash
go test ./internal/ui/bubble -run 'TestRenderActivityPane|TestActivityCommandsAndTabs' -v
```

预期：全部 PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/activity.go internal/ui/bubble/right_panel.go internal/ui/bubble/activity_dock_test.go
git commit -m "feat(ui): render Activity as borderless full-height pane"
```

---

### 任务 5：把 workspace 与 Activity 拼成单一固定 frame

**文件：**
- 修改：`internal/ui/bubble/activity_layout.go`
- 修改：`internal/ui/bubble/activity_layout_test.go`
- 修改：`internal/ui/bubble/activity_dock_test.go`
- 修改：`internal/ui/bubble/layout.go:21-161, 248-385, 665-708, 835-867`
- 修改：`internal/ui/bubble/status_line.go:120-175`
- 修改：`internal/ui/bubble/header.go:103-126`
- 修改：`internal/ui/bubble/fixed_layout_test.go:18-85, 194-220`

- [ ] **步骤 1：追加失败的矩形与 frame joint 测试**

追加到 `activity_layout_test.go`：

```go
func TestJoinActivityColumnsKeepsExactCellGeometry(t *testing.T) {
	left := "中文  \nleft  "
	right := "👩‍💻 \nright"
	got := joinActivityColumns(left, right, 6, 5, 2, "│")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("height=%d, want 2", len(lines))
	}
	for row, line := range lines {
		if terminalCellWidth(line) != 12 {
			t.Fatalf("row=%d width=%d: %q", row, terminalCellWidth(line), ansi.Strip(line))
		}
		if !strings.Contains(ansi.Strip(line), "│") {
			t.Fatalf("row=%d missing separator: %q", row, ansi.Strip(line))
		}
	}
}

func TestRenderSplitHairlineUsesJointAtWorkspaceBoundary(t *testing.T) {
	line := ansi.Strip(renderSplitHairline("main", "Activity", 52, 32, "┬", ""))
	if terminalCellWidth(line) != 85 || string([]rune(line)[52]) != "┬" {
		t.Fatalf("split line = %q width=%d", line, terminalCellWidth(line))
	}
}
```

补 imports：

```go
import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)
```

追加到 `activity_dock_test.go`：

```go
func TestViewRendersFullHeightDockWithoutCoveringWorkspace(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.transcript = []transcriptEntry{{kind: entryAssistant, body: "LEFT-SENTINEL " + strings.Repeat("x", 70)}}
	model.openActivity(activityTabTasks)
	model.relayout()
	model.refreshViewport()

	view := ansi.Strip(model.View())
	assertFixedFrame(t, view, 120, 30)
	layout := model.currentLayout()
	if layout.activityMode != activityLayoutDocked {
		t.Fatalf("layout = %+v", layout)
	}
	for row, line := range strings.Split(model.View(), "\n")[1:29] {
		joint := ansi.Strip(cutStyledCellsExact(line, layout.workspaceWidth, layout.workspaceWidth+1))
		if joint != "│" {
			t.Fatalf("row=%d joint=%q, want │: %q", row, joint, ansi.Strip(line))
		}
	}
	if !strings.Contains(view, "LEFT-SENTINEL") || !strings.Contains(view, "Activity") {
		t.Fatalf("dock lost content:\n%s", view)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestJoinActivityColumns|TestRenderSplitHairline|TestViewRendersFullHeightDock' -v
```

预期：编译失败，拼接/frame helpers 未定义。

- [ ] **步骤 3：实现精确列拼接与 split hairline**

追加到 `activity_layout.go`：

```go
func joinActivityColumns(left, right string, leftWidth, rightWidth, height int, separator string) string {
	left = fitStyledRect(left, leftWidth, height)
	right = fitStyledRect(right, rightWidth, height)
	separator = fitStyledCellLine(separator, activitySeparatorWidth)
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		lines[row] = leftLines[row] + separator + rightLines[row]
	}
	return strings.Join(lines, "\n")
}

func renderSplitHairline(leftContent, rightContent string, leftWidth, rightWidth int, joint, lineColor string) string {
	jointStyle := lipgloss.NewStyle()
	if lineColor != "" {
		jointStyle = jointStyle.Foreground(lipgloss.Color(lineColor))
	}
	return embedHairlineContent(leftContent, leftWidth, lineColor) +
		jointStyle.Render(joint) +
		embedHairlineContent(rightContent, rightWidth, lineColor)
}
```

添加 imports：`strings`、`github.com/charmbracelet/lipgloss`。

- [ ] **步骤 4：让 `currentLayout` 和 `relayout` 使用 Activity geometry**

把 `currentLayout` 改成所有路径都经过同一几何收尾：

```go
func (m appModel) currentLayout() tuiLayout {
	if m.selectionDock != nil {
		base := applyActivityGeometry(computeTUILayout(m.width, m.height, inputMinVisibleLines), m.activity.visible, m.activity.widthColumns)
		selectionWidth := base.workspaceWidth
		if base.activityMode == activityLayoutFullscreen {
			selectionWidth = base.contentWidth
		}
		inputHeight := m.selectionDock.preferredHeight(inputDockContentWidth(selectionWidth))
		layout := computeTUILayoutWithInputLimit(m.width, m.height, inputHeight, selectionDockMaxVisibleLines)
		return applyActivityGeometry(layout, m.activity.visible, m.activity.widthColumns)
	}
	queueHeight := m.queuePanelHeight()
	textareaHeight := m.input.Height()
	if textareaHeight <= 0 {
		textareaHeight = inputMinVisibleLines
	}
	requestedInputHeight := textareaHeight + queueHeight
	inputLimit := inputMaxVisibleLines
	if queueHeight > 0 {
		inputLimit += queuePanelMaxHeight
	}
	layout := computeTUILayoutWithInputLimit(m.width, m.height, requestedInputHeight, inputLimit)
	layout.queueHeight = minInt(queueHeight, maxInt(0, layout.inputHeight-1))
	layout.queueInlineHeight = m.queueInlineSummaryHeight()
	return applyActivityGeometry(layout, m.activity.visible, m.activity.widthColumns)
}
```

`relayout` 的 input/viewport 宽度使用 `layout.workspaceWidth`；fullscreen 时为了保留隐藏输入的折行模型，使用完整 content width：

```go
workspaceModelWidth := layout.workspaceWidth
if layout.activityMode == activityLayoutFullscreen {
	workspaceModelWidth = layout.contentWidth
}
inputWidth := inputDockContentWidth(workspaceModelWidth)
transcriptWidth := maxInt(1, workspaceModelWidth-transcriptContentStyle.GetHorizontalPadding())
```

- [ ] **步骤 5：拆出 workspace body 并重写 `View` 分支**

新增以下函数：`renderWorkspaceBody` 放在 `layout.go`，`renderActivityHeader` 放在 `header.go`，`renderActivityFullscreenBottomContent` 放在 `activity.go`：

```go
func (m appModel) renderWorkspaceBody(layout tuiLayout) string {
	workspaceLayout := layout
	workspaceLayout.contentWidth = layout.workspaceWidth
	parts := make([]string, 0, 4)
	if workspaceLayout.transcriptHeight > 0 {
		parts = append(parts, m.renderTranscriptRegion(workspaceLayout))
	}
	if workspaceLayout.statusHeight > 0 {
		parts = append(parts, m.renderDockStatusLine(workspaceLayout.contentWidth))
	}
	parts = append(parts, m.renderInputBoxForLayout(workspaceLayout))
	if workspaceLayout.queueHeight > 0 {
		parts = append(parts, m.renderQueuePanel(workspaceLayout.contentWidth, workspaceLayout.queueHeight))
	}
	return fitStyledRect(strings.Join(parts, "\n"), workspaceLayout.contentWidth, layout.contentHeight)
}

func (m appModel) renderActivityHeader(width int) string {
	page := "Tasks"
	if m.activity.tab == activityTabTodo {
		page = "Todo"
	}
	return truncateStyledCellLine("Activity / "+page, width)
}

func (m appModel) renderActivityFullscreenBottomContent(width int) string {
	return truncateStyledCellLine("ACTIVITY · Esc/Ctrl+G back", width)
}
```

从 `renderTranscriptRegion` 删除 `renderTaskCard/placeRightCenteredOverlay` 和 `renderActivityBox/placeOpaqueOverlay` 两段；保留其他 modal/completion/notice overlay。随后删除任务 4 的 `renderActivityBox` 兼容 wrapper 以及 `activityPanelWidth/activityPanelHeight`。

把 `View` 的普通帧构建替换为三分支：

```go
layout := m.currentLayout()
var view string
switch layout.activityMode {
case activityLayoutFullscreen:
	inner := m.renderActivityPane(layout.activityWidth, layout.contentHeight)
	view = renderDockedFrame(
		inner,
		m.renderActivityHeader(layout.activityWidth),
		m.renderActivityFullscreenBottomContent(layout.activityWidth),
		layout.frameWidth,
		layout.frameHeight,
	)
case activityLayoutDocked:
	workspace := m.renderWorkspaceBody(layout)
	activity := m.renderActivityPane(layout.activityWidth, layout.contentHeight)
	separatorColor := colorManager.Hex(colorMarkdownQuoteBorder)
	if m.activity.focus == activityFocusPanel {
		separatorColor = m.currentModeHex()
		if separatorColor == "" {
			separatorColor = colorManager.Hex(colorSignal)
		}
	}
	separatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(separatorColor))
	inner := joinActivityColumns(
		workspace,
		activity,
		layout.workspaceWidth,
		layout.activityWidth,
		layout.contentHeight,
		separatorStyle.Render("│"),
	)
	top := renderSplitHairline(
		m.renderHeaderEmbedded(layout.workspaceWidth),
		m.renderActivityHeader(layout.activityWidth),
		layout.workspaceWidth,
		layout.activityWidth,
		"┬",
		separatorColor,
	)
	bottom := renderSplitHairline(
		m.renderBottomWorkspaceLine(layout.workspaceWidth),
		m.renderActivityBottomLine(layout.activityWidth),
		layout.workspaceWidth,
		layout.activityWidth,
		"┴",
		m.currentModeHex(),
	)
	view = top + "\n" + inner + "\n" + bottom
default:
	inner := m.renderWorkspaceBody(layout)
	view = renderDockedFrame(inner, m.renderHeaderEmbedded(layout.contentWidth), m.renderBottomDockLine(layout.contentWidth), layout.frameWidth, layout.frameHeight)
}
```

背景喷涂和 cursor anchor 沿用现有收尾。queue-inline 只在 hidden/docked workspace 可见时运行，fullscreen 必须跳过；docked 时把 Activity 整段计入 `rightInset`：

```go
if layout.queueInlineHeight > 0 && layout.activityMode != activityLayoutFullscreen {
	rightInset := terminalCellWidth(m.renderBottomDockWorktree(layout.frameWidth)) + 2
	if layout.activityMode == activityLayoutDocked {
		rightInset += layout.activitySeparatorWidth + layout.activityWidth
	}
	view = renderQueueInlineBottomBorder(
		view,
		layout.frameWidth,
		m.queuePanelContent(layout.workspaceWidth),
		m.currentModeHex(),
		terminalCellWidth(m.renderModeIndicator())+2,
		rightInset,
	)
}
```

- [ ] **步骤 6：拆分底边框段和光标宽度**

在 `status_line.go` 把现有 `renderBottomDockLine` 重命名为 `renderBottomDockLineWithRight(width int, right string)`，保留其完整函数体，只删除函数体内原来的 `right := m.renderBottomDockWorktree(width)` 赋值，让参数成为唯一 right segment。然后添加 wrapper：

```go
func (m appModel) renderBottomDockLine(width int) string {
	return m.renderBottomDockLineWithRight(width, m.renderBottomDockWorktree(width))
}
```

再新增：

```go
func (m appModel) renderBottomWorkspaceLine(width int) string {
	return m.renderBottomDockLineWithRight(width, "")
}

func (m appModel) renderActivityBottomLine(width int) string {
	return embedHairlineContent(m.renderBottomDockWorktree(maxInt(width, worktreeInlineMinimumWidth)), width, m.currentModeHex())
}
```

`inputCursorTerminalPosition` 的 column clamp 改为：

```go
column = minInt(column, maxInt(0, layout.workspaceWidth-1))
```

`shouldAnchorTextInputCursor` 把任务 2 的过渡条件 `!m.activity.visible` 改为：

```go
(!m.activity.visible || (m.activity.focus == activityFocusWorkspace && m.currentLayout().activityMode != activityLayoutFullscreen))
```

- [ ] **步骤 7：运行布局和固定矩形测试**

```bash
go test ./internal/ui/bubble -run 'TestJoinActivityColumns|TestRenderSplitHairline|TestViewRendersFullHeightDock|TestViewFrameInvariant|TestVisualGeometry|TestInputGrowth' -v
```

预期：全部 PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/ui/bubble/activity_layout.go internal/ui/bubble/activity_layout_test.go internal/ui/bubble/activity_dock_test.go internal/ui/bubble/layout.go internal/ui/bubble/status_line.go internal/ui/bubble/header.go internal/ui/bubble/fixed_layout_test.go
git commit -m "feat(ui): compose Activity into the main docked frame"
```

---

### 任务 6：修正 task preview、Esc、提交和窄屏全页生命周期

**文件：**
- 创建：`internal/ui/bubble/task_preview_dock_test.go`
- 修改：`internal/ui/bubble/types.go:468-475`
- 修改：`internal/ui/bubble/task_picker.go:163-203, 494-508, 568-608, 733-766`
- 修改：`internal/ui/bubble/app.go:510-560`
- 修改：`internal/ui/bubble/input.go:12-24`
- 修改：`internal/ui/bubble/bubble_test.go:5440-5615, 7141-7175`

- [ ] **步骤 1：编写失败的 preview 生命周期测试**

创建 `task_preview_dock_test.go`：

```go
package bubble

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/task"
)

func TestDockedTaskPreviewKeepsActivityOpen(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tasks = []task.TaskSnapshot{{ID: "worker", SessionID: "worker", Name: "worker"}}
	model.activity.selectedTaskID = "worker"

	preview := &taskTranscriptPreview{task: model.activity.tasks[0], parentSessionID: model.sessionID, parentTranscript: copyTranscriptEntries(model.transcript)}
	model.applyTaskPreviewRestore(sessionRestoredMsg{source: sessionRestoreTaskEnter, taskPreview: preview, entries: []transcriptEntry{{kind: entryAssistant, body: "task output"}}})
	if !model.activity.visible || model.taskPreview == nil || !strings.Contains(renderTranscript(model.transcript, 80, false), "task output") {
		t.Fatalf("preview state: visible=%v preview=%#v transcript=%#v", model.activity.visible, model.taskPreview, model.transcript)
	}
}

func TestCtrlGClosesDockButKeepsTaskPreview(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.activity.visible = true
	model.taskPreview = &taskTranscriptPreview{parentSessionID: "main", parentTranscript: []transcriptEntry{{kind: entryAssistant, body: "main"}}}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = next.(appModel)
	if model.activity.visible || model.taskPreview == nil {
		t.Fatalf("ctrl+g state: visible=%v preview=%#v", model.activity.visible, model.taskPreview)
	}
}

func TestTaskPreviewLoadErrorKeepsLastSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.activity.visible = true
	preview := &taskTranscriptPreview{task: task.TaskSnapshot{ID: "worker", Name: "worker"}, parentSessionID: "main"}
	model.applyTaskPreviewError(sessionRestoredMsg{source: sessionRestoreTaskEnter, taskPreview: preview, err: errors.New("transcript unavailable")})
	if model.taskPreview == nil || model.taskPreview.loadError != "transcript unavailable" || !model.activity.visible {
		t.Fatalf("error preview = %#v activity=%+v", model.taskPreview, model.activity)
	}
}

func TestNarrowActivityEnterClosesPanelAndStartsPreview(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tab = activityTabTasks
	model.activity.tasks = []task.TaskSnapshot{{ID: "worker", SessionID: "worker"}}

	next, cmd := model.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.activity.visible || cmd == nil {
		t.Fatalf("narrow Enter visible=%v cmd=%v", model.activity.visible, cmd)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestDockedTaskPreview|TestCtrlGClosesDock|TestTaskPreviewLoadError|TestNarrowActivityEnter' -v
```

预期：编译失败，`loadError`/`applyTaskPreviewError` 未定义；现有 restore 会关闭 picker。

- [ ] **步骤 3：保存 preview 错误并保留 Activity**

在 `taskTranscriptPreview` 加：

```go
	loadError string
```

`renderTaskPreviewTranscript` 在 live content 后追加：

```go
	if message := strings.TrimSpace(preview.loadError); message != "" {
		entries = append(entries, transcriptEntry{
			kind: entryError, title: "task", body: message, createdAt: createdAt,
		})
	}
```

`applyTaskPreviewRestore` 删除任务 2 为保持旧行为加入的 `m.closeActivity()` 过渡调用，不改变 `m.activity.visible/focus`；成功时清空 `preview.loadError`。

新增：

```go
func (m *appModel) applyTaskPreviewError(msg sessionRestoredMsg) {
	if msg.taskPreview == nil {
		return
	}
	preview := *msg.taskPreview
	preview.parentTranscript = copyTranscriptEntries(msg.taskPreview.parentTranscript)
	preview.loadError = msg.err.Error()
	m.taskPreview = &preview
	m.resetToolInspect()
	m.clearNewMessageNotice()
	m.replaceTranscript(renderTaskPreviewTranscript(m.taskPreview, m.animationNow()))
	m.relayout()
	m.refreshViewport()
}
```

在 `app.go` 的 `sessionRestoredMsg` error 分支中，`sessionRestoreTaskEnter` 调用 `applyTaskPreviewError(msg)`，不关闭 Activity、不把错误追加到父 transcript。

- [ ] **步骤 4：落实宽屏/窄屏 Enter 和 Esc 规则**

`handleActivityKey` 的 Enter 分支：

```go
	case "enter":
		if m.activity.tab != activityTabTasks || len(m.activity.tasks) == 0 {
			return m, nil
		}
		task := m.activity.tasks[clampInt(m.activity.selectedIndex, 0, len(m.activity.tasks)-1)]
		if m.currentLayout().activityMode == activityLayoutFullscreen {
			m.closeActivity()
		}
		return m.previewTaskTranscript(task)
```

`restoreMainTranscriptFromTaskPreview` 不再修改 Activity visible/focus/tab/selection。

把 `refreshTaskPreviewFromTasks` 的查找分支改成：

```go
task, ok := m.findTaskPreviewTask()
if !ok {
	const unavailable = "task is no longer available"
	if m.taskPreview.loadError == unavailable {
		return false
	}
	m.taskPreview.loadError = unavailable
	m.resetToolInspect()
	m.replaceTranscript(renderTaskPreviewTranscript(m.taskPreview, m.animationNow()))
	return true
}
if m.taskPreview.loadError != "" {
	m.taskPreview.loadError = ""
}
```

然后继续执行现有 task/live/usage 比较与刷新。`handleSubmit` 保留现有顺序：先 `consumeSubmittedInput`，成功后调用 `restoreMainTranscriptFromTaskPreview`，再处理 command/chat；新增测试断言 Activity 仍保持可见。

- [ ] **步骤 5：运行 preview 和提交测试**

```bash
go test ./internal/ui/bubble -run 'TestDockedTaskPreview|TestCtrlGClosesDock|TestTaskPreviewLoadError|TestNarrowActivityEnter|TestCtrlG|TestTaskPreview' -v
```

预期：全部 PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/task_preview_dock_test.go internal/ui/bubble/types.go internal/ui/bubble/task_picker.go internal/ui/bubble/app.go internal/ui/bubble/input.go internal/ui/bubble/bubble_test.go
git commit -m "feat(ui): keep Activity and task preview lifecycle independent"
```

---

### 任务 7：用 hairline running 提示替代悬浮 task 卡

**文件：**
- 修改：`internal/ui/bubble/header.go`
- 修改：`internal/ui/bubble/task_card.go`
- 修改：`internal/ui/bubble/task_card_test.go`
- 修改：`internal/ui/bubble/activity_dock_test.go`
- 修改：`internal/ui/bubble/layout.go:326-348`
- 删除：`internal/ui/bubble/activity_side_panel_test.go`

- [ ] **步骤 1：编写失败的关闭态提示测试**

追加到 `activity_dock_test.go`：

```go
func TestClosedActivityUsesHeaderHintInsteadOfFloatingTaskCard(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.taskController = newFakeActiveTaskController([]task.TaskSnapshot{{ID: "worker", Name: "worker", Status: task.TaskRunning}})
	model.activity.visible = false
	model.relayout()

	plain := ansi.Strip(model.View())
	if !strings.Contains(strings.Split(plain, "\n")[0], "1 running") || !strings.Contains(strings.Split(plain, "\n")[0], "Ctrl+G") {
		t.Fatalf("top border missing running hint:\n%s", plain)
	}
	if strings.Contains(plain, "taskController ·") || strings.Contains(plain, "╭") {
		t.Fatalf("legacy floating card still rendered:\n%s", plain)
	}
}

func TestOpenActivityDoesNotDuplicateClosedHint(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.taskController = newFakeActiveTaskController([]task.TaskSnapshot{{ID: "worker", Status: task.TaskRunning}})
	model.openActivity(activityTabTasks)
	plain := ansi.Strip(model.View())
	if strings.Count(plain, "1 running") > 1 {
		t.Fatalf("running hint duplicated:\n%s", plain)
	}
}
```

在测试文件定义一个嵌入现有 `fakeTaskController` 的 active fake；嵌入字段直接提供 `Run/Launch/ListTasks`，这里只新增进程存活视图：

```go
type fakeActiveTaskController struct {
	*fakeTaskController
	active []task.TaskSnapshot
}

func newFakeActiveTaskController(tasks []task.TaskSnapshot) *fakeActiveTaskController {
	copied := append([]task.TaskSnapshot(nil), tasks...)
	return &fakeActiveTaskController{
		fakeTaskController: &fakeTaskController{tasks: copied},
		active:             append([]task.TaskSnapshot(nil), copied...),
	}
}

func (f *fakeActiveTaskController) ActiveTasks() []task.TaskSnapshot {
	return append([]task.TaskSnapshot(nil), f.active...)
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestClosedActivityUsesHeaderHint|TestOpenActivityDoesNotDuplicate' -v
```

预期：FAIL；顶部无提示，legacy card 仍存在。

- [ ] **步骤 3：实现 Activity header 文本**

修改任务 5 已创建的 `renderActivityHeader`，并在 `header.go` import 加入 `strconv`：

```go
func (m appModel) renderActivityHeader(width int) string {
	page := "Tasks"
	if m.activity.tab == activityTabTodo {
		page = "Todo"
	}
	text := "Activity / " + page
	if count := len(m.runningTasks()); count > 0 {
		text += " · ● " + strconv.Itoa(count)
	}
	return truncateStyledCellLine(text, width)
}

func (m appModel) renderHeaderActivityHint() string {
	count := len(m.runningTasks())
	if count > 0 {
		return "● " + strconv.Itoa(count) + " running · Ctrl+G"
	}
	return "Activity · Ctrl+G"
}
```

在 `activity_layout.go` 新增返回完整 hairline 的 helper：

```go
func renderHairlineWithRightHint(mainContent, hint string, width int, lineColor string) string {
	width = maxInt(1, width)
	mainBudget := maxInt(1, width/2)
	mainContent = truncateStyledCellLine(mainContent, mainBudget)
	hintBudget := maxInt(0, width-terminalCellWidth(mainContent)-3)
	if hintBudget <= 0 {
		return embedHairlineContent(mainContent, width, lineColor)
	}
	hint = truncateStyledCellLine(hint, hintBudget)
	lineStyle := lipgloss.NewStyle()
	if lineColor != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(lineColor))
	}
	dash := func(n int) string {
		if n <= 0 {
			return ""
		}
		return lineStyle.Render(strings.Repeat("─", n))
	}
	left := dash(1) + " " + mainContent + " "
	right := " " + hint + " " + dash(1)
	middle := maxInt(0, width-terminalCellWidth(left)-terminalCellWidth(right))
	return fitStyledCellLine(left+dash(middle)+right, width)
}
```

把 `View` hidden/default 分支改成直接使用完整 top line：

```go
inner := m.renderWorkspaceBody(layout)
top := renderHairlineWithRightHint(m.renderHeaderEmbedded(layout.contentWidth), m.renderHeaderActivityHint(), layout.frameWidth, "")
bottom := m.renderBottomDockLine(layout.contentWidth)
view = top + "\n" + inner + "\n" + bottom
```

空间不足时 helper 优先缩短或省略 hint，主 header 仍保留至少一半宽度。

- [ ] **步骤 4：删除悬浮卡渲染代码**

把 `task_card.go` 改成只保留以下完整实现：

```go
package bubble

import taskpkg "paw/internal/task"

func (m appModel) runningTasks() []taskpkg.TaskSnapshot {
	if m.taskController == nil {
		return nil
	}
	if active, ok := m.taskController.(ActiveTaskController); ok {
		return active.ActiveTasks()
	}
	tasks := m.taskController.ListTasks()
	running := make([]taskpkg.TaskSnapshot, 0, len(tasks))
	for _, snapshot := range tasks {
		if snapshot.Status == taskpkg.TaskRunning {
			running = append(running, snapshot)
		}
	}
	return running
}

func (m appModel) hasRunningTasks() bool {
	return len(m.runningTasks()) > 0
}
```

这会删除 `taskSpinnerFrames`、`taskCardMaxWidth`、`renderTaskCard`、`renderTaskCardRow`、`spinnerFrameIndex`、`itoa`、`placeRightCenteredOverlay` 及其专属 imports/styles。

删除 `activity_side_panel_test.go`；把 `task_card_test.go` 中卡片布局/位置测试删掉，只保留 ActiveTasks 优先于 stale projection 的测试，并改成检查 `runningTasks` 与 header hint。

- [ ] **步骤 5：运行 running 状态与动画测试**

```bash
go test ./internal/ui/bubble -run 'TestClosedActivityUsesHeaderHint|TestOpenActivityDoesNotDuplicate|TestRenderTaskCardUsesActiveTasks|TestAnimation|TestNeedsUIAnimation' -v
```

预期：全部 PASS；如旧测试名仍含 `RenderTaskCard`，在本步骤重命名为 `TestRunningTasksUsesActiveTasksInsteadOfStaleProjection`。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/header.go internal/ui/bubble/activity_layout.go internal/ui/bubble/task_card.go internal/ui/bubble/task_card_test.go internal/ui/bubble/activity_dock_test.go internal/ui/bubble/layout.go
git rm internal/ui/bubble/activity_side_panel_test.go
git commit -m "refactor(ui): replace floating task card with frame status"
```

---

### 任务 8：集成回归、README 与视觉证据

**文件：**
- 修改：`internal/ui/bubble/fixed_layout_test.go`
- 修改：`internal/ui/bubble/bubble_test.go`
- 修改：`internal/ui/bubble/terminal_cells_test.go`（仅在 split helper 发现新的宽字符边界时）
- 修改：`README.md:310-315`
- 创建：`.agent/visual/activity-docked-sidebar.md`
- 创建：`.agent/visual/activity-docked-default.png`
- 创建：`.agent/visual/activity-docked-resized.png`
- 创建：`.agent/visual/activity-docked-preview.png`
- 创建：`.agent/visual/activity-docked-closed.png`
- 创建：`.agent/visual/activity-fullscreen-narrow.png`
- 创建：`.agent/visual/activity-docked-overlay-priority.png`

- [ ] **步骤 1：更新旧 Ctrl+G 与 frame invariant 测试**

把 `fixed_layout_test.go:TestActivityCommandsAndTabs` 的 Esc 断言改成：

```go
next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
model = next.(appModel)
if !model.activity.visible || model.activity.focus != activityFocusWorkspace {
	t.Fatalf("Esc should keep dock visible and focus workspace: %+v", model.activity)
}
```

在 `TestViewFrameInvariantAcrossContentAndOverlays` 增加 120×30 docked 和 80×24 fullscreen 两个子场景；每次调用 `assertFixedFrame`。

更新 `bubble_test.go` 原有测试：

- Ctrl+G 打开后断言 `activity.visible`。
- Ctrl+G 关闭时不再断言 task preview 被清除。
- Enter preview 后宽屏断言 Activity 仍打开。
- task update 后断言 `activity.tasks` 和 `selectedTaskID`。

- [ ] **步骤 2：增加 overlay 优先级回归**

在 `activity_dock_test.go` 追加：

```go
func TestCompletionAndModalStayInsideWorkspaceWhenActivityDocked(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	model.activity.focus = activityFocusWorkspace
	model.completion = &completion{kind: completionKindCommand, items: []string{"/help", "/tasks"}}
	model.relayout()
	view := ansi.Strip(model.View())
	layout := model.currentLayout()
	for _, line := range strings.Split(view, "\n")[1:29] {
		if index := strings.Index(line, "/help"); index >= layout.workspaceWidth {
			t.Fatalf("completion leaked into Activity pane at column %d:\n%s", index, view)
		}
	}

	model.completion = nil
	model.modelWizard = newModelWizard(model.currentModelConfig())
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "Activity") {
		t.Fatalf("docked Activity disappeared behind workspace modal:\n%s", view)
	}
}
```

- [ ] **步骤 3：运行局部和全量测试**

```bash
gofmt -w internal/ui/bubble/activity_layout.go internal/ui/bubble/activity_layout_test.go internal/ui/bubble/activity_navigation_test.go internal/ui/bubble/activity_dock_test.go internal/ui/bubble/task_preview_dock_test.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/task_picker.go internal/ui/bubble/activity.go internal/ui/bubble/right_panel.go internal/ui/bubble/layout.go internal/ui/bubble/status_line.go internal/ui/bubble/header.go internal/ui/bubble/task_card.go internal/ui/bubble/task_card_test.go internal/ui/bubble/fixed_layout_test.go internal/ui/bubble/bubble_test.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/cursor_animation.go

go test ./internal/ui/bubble -run 'Activity|CtrlG|TaskPreview|FixedLayout|VisualGeometry|Cursor|Completion' -v
go test ./...
```

预期：全部 PASS。

- [ ] **步骤 4：更新 README 快捷键文档**

把 `README.md` 的 Ctrl+G 条目替换为：

```markdown
- `ctrl+g`: 展开/收起主外框内的全高 Activity 右侧栏；打开后 transcript、状态行、输入框和 queue 会共同缩窄，不遮挡主内容。右栏包含 Tasks/Todo，使用 ↑↓ 选择、Enter 在左侧预览 task transcript。`ctrl+w` 后按 `h/l` 切换 workspace/Activity 焦点，按 `</>` 以 4 列步长调节右栏宽度；Esc 从 task preview 返回主 transcript，或把焦点交回 workspace。终端窄于 85 列时 Activity 使用内部全页模式。输入始终提交到主 session。
```

- [ ] **步骤 5：运行 TUI 并采集六组视觉证据**

先构建并启动可交互 TUI，使用仓库现有测试 harness 或手动 fixture 构造 running/completed/pending tasks。对以下状态分别捕获新鲜截图：

```text
120×30  Activity 默认 36% 打开态
120×30  Ctrl+W > 调宽后的状态
120×30  task transcript preview + input → main session
120×30  Activity 关闭 + 顶部 ● N running · Ctrl+G
80×24   Activity fullscreen
120×30  Activity docked + completion/model modal 仅覆盖左栏
```

若 `.agent-md/bin/playwright-capture.sh` 无法捕获终端，使用项目现有 Bubble Tea screenshot helper；不得用浏览器 mockup 代替实现证据。

在 `.agent/visual/activity-docked-sidebar.md` 写入：

```markdown
# Activity docked sidebar visual evidence

- Changed files: internal/ui/bubble/activity_layout.go, activity.go, layout.go, task_picker.go, app.go, header.go, status_line.go
- Route/workflow: interactive Paw TUI, Ctrl+G Activity
- Viewports: 120×30 and 80×24 terminal cells
- Artifacts:
  - activity-docked-default.png
  - activity-docked-resized.png
  - activity-docked-preview.png
  - activity-docked-closed.png
  - activity-fullscreen-narrow.png
  - activity-docked-overlay-priority.png
- Observed result: Activity participates in layout, separator spans the full inner height, workspace content is never covered, preview input remains bound to main session, and narrow terminals use a full internal page.
```

确认每张图片非空且修改时间晚于最终代码提交前的验证开始时间。

- [ ] **步骤 6：最终验证和独立审查**

```bash
go test ./...
git diff --check
test -s .agent/visual/activity-docked-default.png
test -s .agent/visual/activity-docked-resized.png
test -s .agent/visual/activity-docked-preview.png
test -s .agent/visual/activity-docked-closed.png
test -s .agent/visual/activity-fullscreen-narrow.png
test -s .agent/visual/activity-docked-overlay-priority.png
```

让新的 reviewer/subagent 对照设计规格检查：Ctrl+G、Ctrl+W、Esc、preview、84/85 边界、running task 数据源、modal/completion 范围和固定矩形。阻塞 finding 必须修复并重新运行 `go test ./...`。

- [ ] **步骤 7：Commit**

```bash
git add README.md internal/ui/bubble .agent/visual/activity-docked-sidebar.md .agent/visual/activity-docked-default.png .agent/visual/activity-docked-resized.png .agent/visual/activity-docked-preview.png .agent/visual/activity-docked-closed.png .agent/visual/activity-fullscreen-narrow.png .agent/visual/activity-docked-overlay-priority.png
git commit -m "feat(ui): dock Ctrl+G Activity into the main frame"
```

---

## 执行顺序与检查点

1. 任务 1–3 完成后，Activity 状态和键盘行为必须可测试，但视觉允许仍未接入最终 frame。
2. 任务 4–5 完成后，必须先跑固定矩形与宽字符测试，再继续 preview 生命周期。
3. 任务 6 完成后，Ctrl+G/Esc/Enter/submit 组合必须全部绿色。
4. 任务 7 删除 legacy task card 后，必须确认 `hasRunningTasks` 仍驱动动画刷新。
5. 任务 8 才更新 README、采集实现截图并运行全仓 `go test ./...`。

## 风险控制

- 当前工作区已有与本功能无关的 `internal/task/manager_test.go`、`internal/task/task_actor.go`、`memory/progress.md` 等改动；执行时不得修改、暂存或提交这些文件。
- 每个任务只暂存上方列出的文件；commit 前运行 `git diff --cached --name-only` 检查范围。
- `Edit` 前重新读取目标文件；删除 `activity_side_panel_test.go` 和 task card helper 前分别用 `rg` 验证引用。
- 不把 `.superpowers/brainstorm/` mockup 当作视觉验证证据；实现证据必须来自真实 TUI。
