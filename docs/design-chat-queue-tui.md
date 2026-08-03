# Chat Queue TUI 交互设计

## 状态

已确认设计，待审阅后进入 implementation plan 阶段。

## 背景与目标

当前 chat queue 主要通过 transcript 中的 `you (queued)`、`queued` 和 `queued for next turn` 表示，用户难以区分普通提交与排队提交，也无法查看、编辑、删除或调整队列中的消息。

本设计将 queue 作为输入区域内独立、可操作的状态面板，同时保留现有 FIFO 串行执行机制。Transcript 只记录事实，queue 面板负责实时状态和交互。

目标：

- 明确区分普通输入、queue 选择和 queue 编辑。
- 普通输入模式下占用最少空间。
- 用户始终知道队列数量和队首摘要。
- 支持进入 queue 选择、编辑、取消恢复、保存到队尾和调整顺序。
- 不改变现有 `QueryGuard`、`turnFinishedMsg` 和串行 turn 执行流程。

## 已确认的视觉方案

采用 v3：

- 普通模式：显示单行摘要 `QUEUE · N · <队首消息摘要>  ↓ 选择`。
- 选择模式：展开完整 queue 列表。
- 编辑模式：输入框标题、边框和提示使用黄色 `EDIT` 状态。
- 选择模式使用蓝色焦点。
- Queue 为空时隐藏 queue 面板。
- 队列面板属于输入区域，不依赖 transcript 滚动。
- 视觉 mockup：`.superpowers/brainstorm/40658-1785739374/content/queue-tui-v3.html`。

## 交互状态

```go
type queueInteractionMode uint8

const (
    queueModeInactive queueInteractionMode = iota
    queueModeSelecting
    queueModeEditing
)
```

### 普通输入模式

Queue 有消息时，输入框下方显示单行摘要：

```text
QUEUE · 3 · 检查项目结构                         ↓ 选择
```

规则：

- Queue 为空时隐藏。
- 显示待处理数量，不包含当前正在运行的 turn。
- 摘要显示队首消息，超长内容单行截断。
- 当前输入为空，或是最新的普通输入，且光标位于内容末尾时，按 `↓` 进入 queue 选择模式。
- 当前输入是历史消息、光标不在末尾、处于编辑模式或 queue 为空时，`↓` 保持现有 textarea/历史导航行为。
- 现有 `Enter` 和 `Tab` 提交语义保持不变。

### Queue 选择模式

```text
QUEUE · 3 · 2/3                            SELECTING
  1 · 检查项目结构
› 2 · 执行完整测试
  3 · 总结当前修改

↑/↓ 选择 · i 编辑 · alt/command+k/j 调整 · esc 退出
```

规则：

- `↑` / `↓`：选择上一个/下一个 queue item。
- `i`：取出选中 item，进入编辑模式。
- `Alt+K` / `Alt+J`：选中 item 前移/后移。
- `Command+K` / `Command+J`：执行同样的前移/后移，作为 macOS 兼容替代。
- 到达队列边界时不移动。
- `Esc`：退出选择模式，不改变队列。
- 选择模式拦截普通文本，不把字符写入 textarea。
- 操作队列顺序不会写入 transcript，避免污染对话记录。

### Queue 编辑模式

```text
EDIT · queue item #2

执行完整测试，并报告失败原因█

Esc 取消并恢复 · Enter 保存到队尾
```

进入编辑模式时，选中 item 从 queue 中临时取出并填入输入框。输入框获得焦点，queue 列表显示剩余项目。

规则：

- 使用黄色标题、边框和提示标记编辑状态。
- `Esc`：取消修改，恢复原始内容，并恢复到原队列位置。
- `Enter`：保存当前 draft，追加到队列末尾，退出编辑模式。
- `:wq`：作为显式保存命令保留；在 Edit 模式输入后按 `Enter` 也保存到队尾。默认 `Enter` 已直接保存。
- 空 draft 不入队，并退出编辑模式。
- 编辑模式下禁止误触发普通 chat turn。

## 数据模型

当前 `CommandQueue` 使用 `items []string` 和 `drafts []inputDraft` 两个并行数组。改为保存完整队列项：

```go
type queuedChatItem struct {
    ID        string
    Draft     inputDraft
    CreatedAt time.Time
}

type CommandQueue struct {
    items []queuedChatItem
}
```

推荐 API：

```go
func (q *CommandQueue) Items() []queuedChatItem
func (q *CommandQueue) Len() int
func (q *CommandQueue) DequeueDraft() (queuedChatItem, bool)
func (q *CommandQueue) RemoveAt(index int) (queuedChatItem, bool)
func (q *CommandQueue) Move(id string, delta int) bool
func (q *CommandQueue) InsertAt(index int, item queuedChatItem) bool
func (q *CommandQueue) EnqueueDraft(draft inputDraft) string
func (q *CommandQueue) Clear()
```

编辑状态保存原 item 和原位置：

```go
type queueEditState struct {
    item       queuedChatItem
    originalAt int
}
```

UI 可以使用 selected index 显示位置，实际移动、删除和恢复使用稳定 ID 定位。

## 事件处理优先级

`appModel.Update()` 的按键处理顺序应为：

```text
其他弹窗/选择器
→ Queue Edit
→ Queue Selecting
→ 补全
→ transcript 滚动
→ 历史导航
→ textarea
```

这样 queue 模式不会被既有历史导航或 textarea 处理吞掉。

## 布局与终端空间

- Queue 面板放在输入区域下方。
- 普通模式只占一行。
- 选择模式才展开完整列表和一行快捷键提示。
- 编辑模式显示编辑提示，queue 面板保持紧凑。
- 终端高度不足时，限制可见 queue item 数量，优先保留输入框。
- 长消息只显示单行摘要，进入编辑后恢复完整 draft。
- 带图片的 draft 显示图片数量和文本摘要，例如 `[image ×2] 分析截图`。

## Transcript 变化

建议将多条低辨识度的 queue 状态记录收敛为一条事实记录：

```text
you > [queued #2]
执行完整测试
```

Queue 当前数量、选择、编辑和顺序由 queue 面板展示。编辑、移动等操作不追加 transcript 操作日志。

## 执行流程边界

保留现有执行流程：

```text
turnFinishedMsg
  → FinishModel()
  → DequeueDraft()
  → StartModel()
  → runTurnCmd()
```

不在本设计中改动：

- `QueryGuard` 并发保护。
- `startNextQueuedTurn()` 的 FIFO 消费机制。
- 当前 turn 的 supplement 语义。
- 图片输入和 rich draft 的传递。
- 单次 `Ctrl+C` 取消当前模型工作的行为。

顺序调整只改变待执行 queue 的数组顺序，不引入中断当前 turn 的新 priority policy。

## 错误与边界行为

- Queue 为空时不能进入选择模式。
- 前移/后移到边界时保持原位置。
- 编辑项被取出后，如果模型 turn 状态变化，不影响当前 turn；编辑项仍由 UI 状态持有。
- 保存失败或 draft 为空时不得产生空 queue item。
- `Esc` 编辑取消必须恢复原 item，避免数据丢失。
- Command 快捷键是否能到达应用取决于终端映射；应用同时注册 Alt 和 Command 变体，无法到达的变体由终端配置负责。

## 验证计划

需要增加或更新测试：

1. CommandQueue item ID、快照、移动、按位置移除和按原位置恢复。
2. 普通模式满足条件时 `↓` 进入选择模式；历史输入或非末尾光标不触发。
3. 选择模式的上下选择、Esc 退出和 Alt/Command J/K 移动。
4. `i` 进入编辑并从 queue 移除选中项。
5. Edit 模式 Esc 恢复原内容和原位置。
6. Edit 模式 Enter 与 `:wq` 保存到队尾。
7. 空 draft 不入队。
8. queue panel 普通/选择/编辑三态渲染、数量、摘要和截断。
9. 现有 FIFO 执行、rich input、QueryGuard 和完整回归测试。
