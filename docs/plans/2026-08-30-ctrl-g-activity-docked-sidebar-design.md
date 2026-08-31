# Ctrl+G Activity Docked Sidebar 设计规格

**状态：** Approved

**日期：** 2026-08-30

**范围：** `internal/ui/bubble`、`internal/task`

## 1. 背景

当前 `Ctrl+G` 打开 Activity 面板，面板包含 `Tasks` 与 `Todo` 两个页签。它在视觉上位于右侧，但实现仍是 transcript 区域上的不透明覆盖层：

- `internal/ui/bubble/layout.go:renderTranscriptRegion` 先渲染完整 transcript，再通过 `placeOpaqueOverlay(..., overlayAlignRight)` 覆盖 Activity。
- `internal/ui/bubble/activity.go:renderActivityBox` 使用 `wizardPanelStyle` 绘制独立四边框，宽度最多为内容区一半且不超过 60 列，高度为 transcript 高度减 2 行。
- `internal/ui/bubble/task_picker.go` 用 `taskPicker != nil` 同时表达“Activity 已打开”和“Activity 独占键盘焦点”。
- Activity 关闭时，运行中的任务由 `renderTaskCard` 生成悬浮卡，并通过 `placeRightCenteredOverlay` 覆盖 transcript 右侧。
- 任务 transcript 预览会替换左侧主 transcript；`handleSubmit` 已在提交前调用 `restoreMainTranscriptFromTaskPreview`，输入目标始终是主 session。

这一实现已经具备基本功能，但存在三个布局问题：

1. Activity 和运行任务卡会遮挡 transcript，而不是参与主布局。
2. Activity 只覆盖 transcript，状态行和输入框仍保持全宽，视觉上不像完整侧栏。
3. “面板可见”和“面板获取键盘”绑定在一起，无法保持侧栏可见的同时继续编辑主输入。

## 2. 目标

本次设计把 Activity 从右侧悬浮块改成主 hairline frame 内的 docked sidebar：

1. Activity 打开时占据独立右栏，左侧 transcript、状态行、输入框和 queue 一起缩窄，任何内容都不被 Activity 遮挡。
2. 左右区域共享现有顶部和底部 hairline frame，不在右栏内部再绘制 rounded/modal 四边框。
3. Activity 可见性、左右 pane 焦点和任务 transcript 预览彼此独立。
4. 支持 Vim 风格 pane 导航和键盘调宽。
5. 终端过窄时切换为 Activity 内部全页，而不是退回 overlay。
6. 移除运行任务悬浮卡，用框线内嵌状态提示提供任务可见性。
7. 保留现有 Tasks/Todo 能力，并为未来 Activity 页面保留轻量扩展点。

最高优先级是：**Activity 不得遮挡主内容。**

## 3. 非目标

本次不做以下工作：

- 不建立通用 IDE workspace 或任意 pane 管理器。
- 不允许拖拽调整宽度，不持久化 Activity 宽度到设置文件。
- 不新增 Queue、Tools 等 Activity 页面；只让页面注册方式能容纳未来扩展。
- 不改变 task/session/todo 的业务数据模型或持久化格式。
- 不允许输入直接发送到 task session；输入始终属于主 session。
- 不重做 `/config`、`/setting`、model/theme/session picker、completion 的视觉样式。
- 第一阶段不增加 Activity 的鼠标导航；键盘路径必须完整可用。

## 4. 方案选择

评估过三个层级：

1. **最小 Dock 改造：** 只把 overlay 变成分栏，继续独占键盘。改动小，但不能保持侧栏可见并继续输入。
2. **焦点感知 Dock：** 独立建模可见性、焦点、宽度和预览，并加入 Vim pane 导航。
3. **通用多 Pane 工作区：** 把 transcript/input/Activity 全部纳入通用 pane manager。

采用方案 2。它满足不遮挡、非模态输入、Vim 导航和未来页签扩展，同时避免通用工作区重构的范围与风险。

视觉方向采用“共用主外框”：右栏没有第二层四边框，只通过一列竖向分隔线、顶部 Activity 段和内部 header/footer 建立层级。

## 5. 状态模型

Activity 状态需要从 `taskPicker != nil` 的隐式模态状态拆开。概念状态如下：

```text
activity.visible          bool
activity.focus            workspace | activity
activity.page             tasks | todo
activity.widthColumns     int
activity.selectedTaskID   string
activity.commandPrefix    idle | ctrl-w
activity.layoutMode       docked | fullscreen   // 由终端宽度派生

taskPreview               nil | preview state   // 与 activity.visible 独立
```

实现可以沿用现有 `taskPicker` 结构，也可以重命名为 `activityState`；关键约束是：

- `visible` 不再等价于“所有按键都路由给 Activity”。
- `taskPreview` 不因 Activity 关闭而自动清除。
- 任务选择以 task ID 为身份，不只依赖数组下标。
- `layoutMode` 是终端宽度和当前可见性的纯派生结果，不单独持久化。
- 宽度只在当前 TUI 运行期间保留；重启恢复默认值。

## 6. 键盘与焦点行为

### 6.1 全局快捷键

这里的“全局”指正常 workspace 帧内，不越过现有全屏页和阻塞式 picker/modal 的事件优先级；这些状态打开时继续由其自身处理按键，Activity 状态只保存、不响应 pane chord。

| 输入 | 条件 | 行为 |
|---|---|---|
| `Ctrl+G` | Activity 关闭 | 打开 Activity；宽屏进入 docked，窄屏进入 fullscreen；焦点交给 Activity |
| `Ctrl+G` | Activity 打开 | 关闭 Activity；焦点交回 workspace；若左侧是 task preview，preview 保持 |
| `Ctrl+W`, `h` | Activity 可见且为 docked | 聚焦左侧 workspace |
| `Ctrl+W`, `l` | Activity 可见且为 docked | 聚焦右侧 Activity |
| `Ctrl+W`, `<` | Activity 可见且为 docked | 右栏缩窄 4 列 |
| `Ctrl+W`, `>` | Activity 可见且为 docked | 右栏加宽 4 列 |

Activity 可见时，`Ctrl+W` 直接成为 pane command 前缀，不保留输入框“删除前一个词”的语义。前缀只消费紧随其后的一个按键：

- `h/l/</>` 执行对应命令。
- `Esc` 或其他按键取消前缀，并忽略该组合。
- 不引入计时器，不让前缀跨越两个普通按键。

Activity 关闭时不拦截 `Ctrl+W`，保持当前 textarea 行为。

### 6.2 Activity 焦点

| 输入 | Tasks 页 | Todo 页 |
|---|---|---|
| `Tab` / `Right` / `l` | 切到下一 Activity 页 | 切到下一 Activity 页 |
| `Shift+Tab` / `Left` / `h` | 切到上一 Activity 页 | 切到上一 Activity 页 |
| `Up` / `k` | 选择上一 task | 无操作；第一阶段不增加 Todo 滚动或 item 操作 |
| `Down` / `j` | 选择下一 task | 无操作；内容继续按可用高度裁剪 |
| `Enter` | 在左侧预览所选 task transcript | 无操作 |
| `Esc` | 见下方优先级 | 见下方优先级 |

Activity 内的裸 `h/l` 继续用于切页；pane 切换必须使用 `Ctrl+W h/l`，避免冲突。

### 6.3 Esc 优先级

按下 `Esc` 时依次判断：

1. 若 `Ctrl+W` 前缀待处理，只取消前缀。
2. 若窄屏 Activity fullscreen 正在显示，关闭 Activity 并返回原左侧内容；底层若是 task preview，则保留该 preview。
3. 若左侧正在 task preview，恢复主 session transcript；Activity 的开关状态和当前焦点保持不变。
4. 若焦点在宽屏 Activity，焦点交回 workspace，Activity 保持可见。
5. 否则沿用 workspace 当前的 Esc 行为。

### 6.4 任务预览与提交

- 宽屏 docked 的 Tasks 页按 `Enter` 后，左侧显示所选 task transcript。
- 宽屏下 Activity 保持打开，焦点仍留在 Activity，便于用 `Up/Down + Enter` 连续切换 task；窄屏行为按 §7.3 关闭 Activity fullscreen 后进入 preview。
- 顶部左段和输入区必须明确显示 preview 身份及 `input → main session`，防止把 task transcript 误认为输入目标。
- `Ctrl+W h` 可进入左侧阅读和编辑。
- 在 preview 中提交输入时，沿用现有 `handleSubmit` 行为：先恢复主 transcript，再把消息提交给主 session，使新输出立即可见。
- `Ctrl+G` 关闭 Activity 时不退出 preview；用户可用 `Esc` 单独返回主 transcript。

## 7. 布局算法

### 7.1 尺寸常量

设计采用以下初始值：

```text
activityDefaultRatio = 36%
activityMinWidth     = 32 cells
activityMaxWidth     = 52 cells
workspaceMinWidth    = 52 cells
activitySeparator    = 1 cell
activityResizeStep   = 4 cells
activityDockMinWidth = 85 cells // 52 + 1 + 32
```

`activityDockMinWidth` 针对 hairline frame 内可用宽度计算，而不是包含终端外部 padding 的窗口宽度。

### 7.2 宽屏 docked 模式

当 Activity 可见且可用宽度不少于 85 列：

```text
activityWidth = clamp(savedWidthOr36Percent, 32, min(52, fullWidth - 1 - 52))
workspaceWidth = fullWidth - 1 - activityWidth
separatorWidth = 1
```

右栏宽度调整后继续满足：

- `activityWidth >= 32`
- `activityWidth <= 52`
- `workspaceWidth >= 52`
- 左右宽度加分隔线严格等于 frame 内宽度

Activity 关闭后 workspace 恢复完整可用宽度；再次打开时恢复本次运行中的上次宽度。

### 7.3 窄屏 fullscreen 模式

当 Activity 可见且可用宽度小于 85 列：

- Activity 占据顶部和底部 hairline 之间的全部内部区域。
- 不渲染 transcript、状态行、输入框和 queue，但它们的状态与草稿保持不变。
- 不使用 overlay，不同时挤出不可读的双栏。
- `Ctrl+G` 或 `Esc` 关闭 Activity 并返回原 workspace。
- Tasks 页 `Enter` 关闭 Activity fullscreen，并进入所选 task preview 全页；Activity 的页签、宽度和选中 task ID 保留，之后按 `Ctrl+G` 可重新打开 Activity。

### 7.4 垂直布局

宽屏 Activity 贯穿完整内部高度，与左侧 workspace 等高：

```text
左侧 workspace                 右侧 Activity
┌ transcript                  ┌ Activity title
├ status                      ├ tabs
├ input                       ├ task/todo content
└ queue（存在时）             └ key hint footer
```

左侧继续使用现有 transcript/status/input/queue 的垂直计算，只把宽度输入改成 `workspaceWidth`。Activity 自己按固定 header、footer 和剩余 body 高度计算，不参与输入高度分配。

## 8. 框线与视觉层级

### 8.1 共用 hairline frame

当前主界面只有顶部和底部 hairline，`mainFrameHorizontalFrame = 0`。新设计不强制改成完整 box border，而是在现有语言中增加 dock 分隔：

```text
──── paw / model ───────────────┬─ Activity / Tasks · ● 1 ─────
workspace content               │ activity content
workspace content               │ activity content
──── mode / token / worktree ───┴───────────────────────────────
```

- 顶部交点使用 `┬`，底部交点使用 `┴`，内部使用 `│`。
- Activity 聚焦时，分隔线和 Activity 顶部段使用当前 mode 强调色。
- workspace 聚焦时，分隔线降为 `colorMarkdownQuoteBorder` 一类的次级颜色；Activity 选中行仍保持可辨识。
- 不使用 rounded border、阴影模拟或 overlay 合成。

### 8.2 Activity 内部结构

Activity pane 从上到下分四段：

1. 标题行：`Activity`，右侧显示 `FOCUSED` 或运行任务数量。
2. 页签行：`Tasks N`、`Todo completed/total`；选中页签使用现有 `SelectionSelected` 或同等级高对比样式。
3. 内容区：task 列表或 todo 内容，占据所有剩余高度。
4. 提示行：上下选择、Enter preview、切页和 Esc 行为；宽度不足时按优先级截断。

任务行：

- 每个未选中 task 只占一行：左侧为状态 glyph/spinner 与 worker 角色名，右侧为明确的 worker status；running 同时显示持续时间。
- worker 名使用 `TaskSnapshot.Color` 对应的角色应援色；选中态保留 `SelectionSelected` 背景，但不再抹掉角色名前景色。
- 只有当前选中 task 展开详情：优先显示 `Description`，缺失时回退为 `Prompt` 单行摘要，再按侧栏宽度进行单词/宽字符安全换行。
- token 数与 `previewing on left` 作为选中项末尾的次级元数据行；不再让所有 task 固定占两行。
- 列表按注意力优先级排序：`running` → `failed/interrupted` → `stopped` → `completed`；同组内最新启动任务在前。刷新后仍按 task ID 保持选择。
- 滚动窗口按实际渲染行高计算，确保多行描述不会把选中项挤出可视区域。

### 8.3 Activity 关闭态

移除 `renderTaskCard` 的悬浮渲染路径。顶部 hairline 的右侧段承担 Activity 可发现性：

- 有 running task：`● N running · Ctrl+G`
- 无 running task 且宽度足够：`Activity · Ctrl+G`
- 宽度不足：优先保留现有模型/状态信息，Activity 提示可省略；`Ctrl+G` 快捷键仍有效。

Activity 打开后，顶部右段改为 `Activity / {page} · ● N`，不重复显示关闭态提示。

### 8.4 Worker 身份与派发

- ProcessPool 内的长期 worker 保持稳定的角色名与应援色搭配；同一个 worker 执行不同 task 时身份不变化。
- worker 创建时从 40 人角色池的随机排列中取角色，一轮内不重复；不再总是从角色池头部按固定顺序出现。
- 当多个健康 worker 同时空闲时，调度器随机选择执行者，而不是固定使用 LIFO 尾部 worker。
- 非进程池或尚未取得 worker 身份的路径继续使用现有随机 persona 兜底，持久化字段和工具协议不变。

## 9. 渲染架构

### 9.1 纯布局结果

扩展 `tuiLayout` 或引入嵌套纯值，明确提供：

```text
fullContentWidth
workspaceWidth
activityWidth
activitySeparatorWidth
activityLayoutMode
contentHeight
```

现有 transcript/input/queue 渲染函数只接收 workspace 的宽高，避免它们知道 Activity 业务状态。Activity 渲染接收右栏的精确宽高。

### 9.2 帧构建顺序

宽屏帧按以下顺序构建：

1. 计算完整 frame 与 Activity 分栏尺寸。
2. 使用 `workspaceWidth` 渲染左侧 transcript/status/input/queue，得到精确矩形。
3. 使用 `activityWidth` 渲染 Activity，得到同高精确矩形。
4. 逐行以 `workspace + separator + activity` 横向拼接。
5. 生成带 `┬/┴` 交点的顶部/底部 hairline。
6. 最后执行一次背景喷涂和终端光标锚定。

Activity 不再调用 `placeOpaqueOverlay`。`placeOpaqueOverlay` 仍保留给 theme/model/session picker、translate panel、completion 和 notice 等现有用途。

### 9.3 其他浮层的范围

- `/config` 与 `/setting` 继续替换整个 frame；Activity 状态保存但暂不渲染。
- theme/model/session picker、translate panel、completion、新消息 notice 等非全屏浮层只在左侧 workspace 内合成，不覆盖 Activity。
- 打开 Activity 时沿用现有行为清理当前 completion，防止焦点突然从候选框跳到右栏。
- selection dock、queue 和 terminal 输入都属于左侧 workspace。

### 9.4 光标

- workspace 聚焦且输入可编辑时，真实终端光标继续锚定到缩窄后的输入框位置。
- Activity 聚焦、Activity fullscreen、modal 或 task 列表导航时隐藏真实终端光标。
- pane 宽度变化后必须重新执行 `relayout`，更新 textarea 宽度、viewport 宽度和光标位置。

## 10. 事件与数据流

### 10.1 Task 更新

继续使用现有 `TaskController`、task update channel 和 `refreshActivityTasks` 流程：

1. 收到 task update。
2. 读取最新 task snapshots。
3. 优先按 `selectedTaskID` 恢复选择；任务消失时选择原位置附近的相邻项。
4. 若当前 preview task 仍存在，刷新 live 内容和状态。
5. 若 preview task 消失或 transcript 加载失败，保留最后成功快照并在左侧显示明确错误，不关闭 Activity、不清空输入。

运行任务数量必须来自当前 active task 视图，不能使用可能滞后的完成投影；这一点沿用当前 task card 的修复约束。

### 10.2 Todo 更新

继续使用 `todo.Broker` 与 `currentTodo` snapshot。Activity Todo 页只消费已有快照，不建立第二份 todo 状态。Todo 清空后显示 `No active todo list`。

### 10.3 页面扩展边界

Activity 页面使用轻量页面表或 switch，至少定义：

```text
id
title
badge/summary
render(width, height)
handleKey(msg)
```

本次只注册 Tasks 和 Todo。不得为了未来页面引入通用 pane 生命周期、动态插件或持久化 schema。

## 11. 错误和空状态

- Task controller 不可用：Tasks 页显示 `Task controller is unavailable.`，Activity 保持可操作，可切到 Todo 或关闭。
- 无 tasks：显示 `No tasks yet.`，Enter 无操作。
- 无 todo：显示 `No active todo list`。
- Task transcript 加载失败：左侧 preview 区显示 task 身份和错误原因；Esc 可回主 transcript。
- task 在刷新中消失：选择相邻项；preview 保留最后内容并标注 unavailable。
- 极小高度：优先保留 Activity 标题/页签和至少一行内容；提示行可裁剪。
- Ctrl+W 非法组合：取消前缀且不把字符写入输入框，避免半条 pane 命令泄漏。

## 12. 测试与验证

### 12.1 纯布局测试

覆盖：

- Activity 关闭时 workspace 等于完整内容宽度。
- 85 列及以上进入 docked；84 列及以下进入 fullscreen。
- 默认 36% 宽度受 32/52 和 workspace 52 列约束。
- `Ctrl+W </>` 每次改变 4 列并正确 clamp。
- 任意支持尺寸下 `workspace + separator + activity == full width`。
- Activity 打开后 transcript/status/input/queue 都使用 workspace 宽度。

### 12.2 交互测试

覆盖：

- `Ctrl+G` 打开、关闭和焦点转移。
- `Ctrl+W h/l` 切 pane；非法 chord 被吞掉；Activity 关闭时不拦截现有输入。
- Activity 可见时 input 仍可编辑和提交。
- Tasks 页 Enter 在左侧预览并保持 Activity 打开。
- preview 中 `Ctrl+G` 只关闭 Activity，preview 保持。
- preview 中 `Esc` 恢复主 transcript，Activity 状态不变。
- preview 中提交先恢复主 transcript，再发送给主 session。
- task 更新按 ID 保持选择；删除选中 task 后选择稳定。
- 窄屏 fullscreen 的 Ctrl+G/Esc/Enter 行为。

### 12.3 渲染测试

覆盖：

- 最终 View 仍严格等于终端 `width × height`。
- Activity 不经过 `placeOpaqueOverlay`，左侧文本在分隔线前完整可见。
- 分隔线贯穿完整内部高度，顶部 `┬`、底部 `┴` 位置正确。
- Activity 没有 nested rounded border。
- Activity 关闭时不渲染运行任务卡，顶部 hairline 显示运行数量。
- Activity 打开时关闭态提示不重复。
- task selection、preview 标识、输入目标提示和极窄截断正确。
- modal/completion 只覆盖左侧 workspace；全屏页仍覆盖整个 frame。
- 宽字符、emoji 和 ANSI 样式不会破坏分栏宽度。

### 12.4 验证命令与视觉证据

实现完成后至少执行：

```bash
go test ./internal/ui/bubble -run 'Activity|CtrlG|TaskPreview|FixedLayout' -v
go test ./...
```

视觉证据至少包含：

1. 120×30 默认打开态。
2. 调宽后的 Activity。
3. 左侧 task transcript preview，输入目标明确显示为 main session。
4. Activity 关闭后的顶部运行任务提示。
5. 小于 85 列的 Activity fullscreen。
6. Activity 与 completion/modal 共存。

每张截图必须是实现后的新鲜产物，并在 `.agent/visual/` 写明 changed files、viewport、artifact 和观察结果。

## 13. 迁移与兼容

- `Ctrl+G` 仍是 Activity 的主入口。
- Tasks/Todo 内容和 task transcript 加载方式不变。
- 输入草稿、输入模式、主 session 提交目标不变。
- 宽度不写入配置，因此不存在配置迁移。
- `README.md` 中“右侧边栏形态”需要更新为“docked 全高侧栏、窄屏全页、Vim pane 快捷键”。
- 删除或改写现有 `activity_side_panel_test.go` 中关于“右侧 overlay 位置”的断言。
- `task_card.go` 中只服务悬浮卡的渲染代码在确认无其他引用后删除；active task 查询可复用于顶部状态提示。

## 14. 完成标准

同时满足以下条件才视为完成：

- Activity 打开时没有任何 transcript/input/queue 内容被右栏覆盖。
- 宽屏为全高 docked 分栏，窄屏为内部全页，不存在 Activity overlay 回退。
- Activity 可保持打开且主输入可继续编辑、提交。
- Vim pane 导航和宽度调整可用，宽度边界稳定。
- task preview、Ctrl+G、Esc、提交目标的组合行为与本规格一致。
- 运行任务悬浮卡已由 hairline 状态提示替代。
- View 尺寸、宽字符、modal/completion、cursor anchor 无回归。
- `go test ./...` 通过，并具备完整视觉证据。
