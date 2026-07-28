# GoCode TUI 工具运行态、点击命中与 Token Ripple 修复设计

## 背景

当前 TUI 有三个相互独立但都发生在 transcript/status 渲染路径中的问题：

1. 工具执行时间较长时，用户看不到稳定的 `running` 状态，无法判断工具是在推进还是卡住。
2. 模型回到 ready 后，点击工具预览行会命中其上方一行，无法展开工具详情。
3. Token Ripple 的移动周期过短；亮色头部抵达最右端后被限制在最后一格，尾部没有继续向右离场。

本设计只修复上述三个问题，保留已经确认的紧凑 transcript、工具结果背景、工作树 chip 和底部状态栏布局。

## 已确认的交互结果

### 工具运行态：方案 A

工具事务保留为一条预览行，不额外插入运行事件行。工具调用期间显示：

```text
◌ Read README.md · running · 12s
```

结果到达后，同一行切换为 `ok` 或 `error`。运行时间按秒更新，运行行始终可见；工具运行期间仍不可展开，完成后恢复折叠/展开能力。

### 工具点击范围：方案 A

一个工具事务的完整可见区域都是同一个命中目标：

- 折叠时的预览行；
- 展开后的工具结果背景行；
- 展开后的详情行。

单击任意事务行切换展开/折叠；拖拽仍优先执行文本选择，不触发展开。

### Token Ripple：方案 A

Ripple 使用约 3.6 秒的完整周期：

- 约 3.0 秒从已消耗 token 进度右端向右移动；
- 约 0.6 秒继续越过进度条边界并淡出；
- 尾部由约 10 格增加到约 14 格；
- 头部和尾部均允许暂时位于进度条可见范围之外；
- 周期完成后从已消耗进度的右端重新开始；
- 只有 `generating` / `working` 状态运行，`ready` 状态保持静态。

## 设计方案

### 1. 工具运行态数据流

沿用现有 `transcriptEntry` 工具事务模型，不引入独立 transcript 事件行。

新增工具开始时间字段 `toolStartedAt time.Time`：

1. `toolCallMsg` 到达时，`recordToolCallEntry` 创建工具事务，设置 `toolStatus="running"` 与 `toolStartedAt`。
2. 渲染层将运行时间格式化为秒级文本；超过一分钟时采用紧凑的分钟/秒格式。
3. `toolResultMsg` 通过现有 tool use ID 匹配逻辑更新同一条事务，设置最终状态、结果内容和可展开状态。
4. 渲染缓存加入运行秒数变化因素，并由现有 cursor frame 驱动按秒刷新，避免每个 30fps 帧都重建 transcript。
5. 若整轮以错误结束，仍处于 running 的工具事务统一收敛为 error，避免出现永久运行态。

渲染函数链路保持单向：`appModel` 提供工具快照，`renderEntryAt` 将时间传递到工具摘要渲染，渲染函数不执行外部命令或读取运行器。

### 2. Transcript 鼠标命中模型

鼠标坐标转换不再假定 transcript 从固定的第一行开始。命中计算统一读取 `currentLayout()`：

```text
屏幕 y
  - 顶部外框行
  - headerHeight
  = transcript viewport 内行
```

之后再叠加 `viewport.YOffset`，得到 transcript 的全局行号。横坐标继续使用 transcript 内容内边距和 viewport 宽度进行 cell 宽度换算。

工具位置计算使用与实际 transcript 相同的：

- viewport 宽度；
- `showThinking` 状态；
- 工具展开状态；
- 工具结果详情高度。

`toolIndexAtTranscriptRow` 将工具位置的 `[startRow, startRow+height)` 全部映射到同一 transcript index。因此折叠预览行和展开后的结果/详情行都能触发展开状态切换。

拖拽判定优先级不变：只有 press/release 位于同一点时才尝试切换工具；只要形成跨 cell 的选区，就执行剪贴板选择而不切换工具。

### 3. Token Ripple 生命周期

Ripple 采用两个连续阶段而不是把头部 clamp 在进度条末端：

```text
travel: usedCells -> width-1       3.0s
exit:   width-1 -> width-1+tail    0.6s
```

渲染时使用虚拟 head 位置：

- 可见 cell 只绘制落在 `[0,width)` 的尾部片段；
- head 超过 `width-1` 后不再绘制亮色头部，但尾部仍会向右移动；
- exit 阶段将整体 alpha 乘以从 1 到 0 的淡出比例；
- head 到达 `width-1+tail` 时，所有 Ripple cell 都离场。

静态的已消耗部分与未消耗部分仍按现有 `━` / `─` 渲染；Ripple 只覆盖未消耗区域。背景到 signal 青色的渐变逻辑保留。

## 错误处理与边界

- 工具结果缺失时，事务保持 `running` 并持续显示 elapsed；不会静默显示 `ok`。
- 结果匹配优先使用 tool use ID；无 ID 时保留现有名称匹配回退逻辑。
- transcript 高度、滚动偏移和鼠标命中都以 cell 行为单位计算，避免日文或宽字符改变命中结果。
- Ripple 宽度为 1、已消耗 0%、中间进度和 100% 进度时都必须保持严格的状态栏宽度。
- `ready` 状态不推进 Ripple，也不改变静态 token frontier。

## 测试与验收

### 工具运行态

- tool call 事件后立即包含 `running`；
- elapsed 跨秒后刷新且不改变事务数量；
- ok/error 结果替换 running 状态并保留同一事务；
- 整轮错误会收敛遗留 running 工具。

### 鼠标命中

- 有 header 时，点击工具预览行不再命中上一行；
- 向下滚动后点击仍映射到正确工具；
- 点击展开后的结果和详情行都能折叠；
- 拖拽选择跨行时不会触发展开。

### Ripple

- 0%、中间进度和 100% 进度的起点正确；
- 3.0 秒时头部抵达右端；
- 3.0~3.6 秒期间尾部继续向右移动且 alpha 递减；
- 周期结束时尾部完全离场并从起点重置；
- idle 状态保持静态；
- `go test ./internal/ui/bubble`、`go test ./...` 与 `git diff --check` 通过。

## 范围外

- 不改变工具预览/结果的既有颜色和折叠视觉层级；
- 不改变工作树状态刷新和状态栏字段顺序；
- 不新增工具取消、重试或后台任务管理功能；
- 不重写 transcript 缓存，只增加运行态必要的失效条件。
