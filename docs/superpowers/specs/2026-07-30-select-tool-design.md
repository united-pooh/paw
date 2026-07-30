# Select 阻塞式 TUI 选项工具设计

## 概述

为 Paw 新增一个仅在主会话 TUI 中可用的模型工具 `Select`。该工具用于展示多个结构化选项，支持单选和多选。模型调用工具后，当前工具执行阻塞，Bubble Tea 界面使用底部交互 Dock 接管键盘输入；用户提交或取消后，工具返回结构化 JSON，模型继续当前回合。

本功能必须保持 TUI 优先，不引入浏览器或图形界面运行时依赖。浏览器视觉伴侣仅用于本设计阶段的布局比较，不属于产品实现。

## 目标

- 提供统一的 `Select` 模型工具，通过 `mode` 支持单选和多选。
- 在主 TUI 底部展示专用交互 Dock，临时替换普通文本输入区。
- 用户提交后向模型返回稳定选项 ID，而非展示文本。
- 支持可选说明、预选、最小选择数和最大选择数。
- 支持长列表内部滚动，不挤占全部 transcript 空间。
- 保持现有 `tool.Tool` 接口不变，通过 Broker 连接工具 goroutine 与 Bubble Tea 事件循环。
- 将 `Select` 作为独占执行屏障，避免它与同批其他工具并行。
- 用户可取消选择而不终止整个模型回合。

## 非目标

首版不包含：

- 搜索、关键词过滤或模糊匹配；
- 选项图标、自定义颜色或任意展示元数据；
- 鼠标交互；
- 循环式上下导航；
- headless 模式支持；
- 同步或后台子代理支持；
- 多个 Selection Dock 并行展示；
- 将完整选项列表重复写入 transcript；
- 对现有 Bubble Tea picker 进行无关的通用化重构。

## 已确认的产品决策

- `Select` 是模型主动调用的阻塞式问答工具，不是纯展示工具。
- 使用统一工具，通过 `mode: "single" | "multiple"` 区分模式。
- Dock 位于底部并临时替换普通输入区，上方 transcript 保持可见。
- `↑` / `↓` 移动高亮项，单选按 Enter 提交当前项。
- 多选使用 Space 勾选或取消，Enter 显式提交全部勾选项。
- Esc 和 Selection Dock 活跃时的 Ctrl+C 返回结构化取消结果，不终止模型回合。
- 多选支持可选 `min_select` 和 `max_select`。
- 选项包含稳定 `id`、展示 `label` 和可选 `description`。
- 支持单选和多选预选；未指定时高亮第一项，但不产生隐式选择。
- 工具仅注册到主会话 TUI，不向 headless 或子代理注册。
- 长列表在 Dock 内部滚动，首版不提供搜索。
- `Select` 独占执行；同批其他工具在它完成后继续，多个 `Select` 按调用顺序逐个执行。

## 架构

采用“Selection Broker + 普通同步工具”方案，分为四个职责明确的单元。

### 1. Select 工具

建议放置于 `internal/tool/select`。

职责：

- 实现现有 `tool.Tool` 接口；
- 提供名称、描述和 JSON Schema；
- 解析、标准化和验证模型输入；
- 向 `SelectionBroker` 发布合法请求；
- 阻塞等待用户提交、用户取消、Broker 关闭或 context 终止；
- 将成功结果编码为稳定 JSON。

该包不依赖 Bubble Tea，不包含样式或终端布局逻辑。

`Select` 不实现 `tool.ConcurrencySafeTool`。工具调度层必须将它视为独占执行屏障，而不是仅仅把它与其他非并发安全工具放进不可预测的并行分组。

### 2. SelectionBroker

Broker 负责工具执行 goroutine 与 Bubble Tea 消息循环之间的请求握手：

```text
Select.Run
    │ publish request
    ▼
SelectionBroker ── Bubble Tea message/command ──▶ appModel
    ▲                                                │
    └──────────── complete(requestID, result) ◀──────┘
```

职责和约束：

- 每个请求分配唯一 request ID；
- 同一时刻最多存在一个已发布到 TUI 的活动请求；
- 后续请求按到达顺序等待前一个请求释放；
- 完成、用户取消、context 取消和 Broker 关闭竞争时只接受第一个终态；
- 完成操作幂等，重复 Enter 或重复取消不会 panic、阻塞或覆盖结果；
- context 终止会解除对应 `Run` 的等待并清理请求；
- Broker 关闭会解除所有等待者并返回明确错误；
- Broker 不持有任何视觉样式、光标或滚动状态。

Broker API 的具体命名可在实现计划中确定，但应表达以下能力：提交并等待请求、订阅/获取下一请求、按 ID 完成请求、按 ID 取消失效请求、关闭 Broker。

### 3. TUI Selection Dock

`appModel` 持有可空的临时 Dock 状态，建议将纯状态机和渲染拆到专用文件，避免继续扩大通用输入处理职责。

Dock 状态至少包含：

- 活动 request ID；
- 已验证的 prompt、mode 和 options；
- 当前高亮索引；
- 多选勾选 ID 集合；
- 可见窗口或滚动偏移；
- 当前校验提示；
- 完成请求所需的 Broker 引用或窄接口。

Dock 活跃时：

- 临时替换普通输入区；
- 优先处理选择相关键盘事件；
- 普通文本输入、历史导航、补全和聊天提交暂停；
- transcript 继续渲染，并保留现有滚动能力；
- 完成或取消后清理 Dock、重新布局并恢复普通输入焦点。

### 4. 主程序装配

主会话启动时创建唯一 Broker，并同时注入主工具 Registry 和 Bubble TUI：

```text
main
 ├─ SelectionBroker
 ├─ SelectTool(Broker)  → main Registry
 └─ BubbleTUI(Broker)
```

只有实际运行 Bubble TUI 且能够消费选择请求时才注册 `Select`。headless UI、`sinkUI`、同步子代理和后台子代理均不注册该工具，避免无人响应的永久阻塞和多个执行流争抢唯一 Dock。

应用关闭时必须关闭 Broker，以释放仍在等待的工具 goroutine。

## 工具输入协议

### 单选示例

```json
{
  "prompt": "选择目标部署环境",
  "mode": "single",
  "options": [
    {
      "id": "production",
      "label": "Production",
      "description": "部署到正式环境"
    },
    {
      "id": "staging",
      "label": "Staging",
      "description": "部署到预发布环境"
    }
  ],
  "initial_selected_id": "staging"
}
```

### 多选示例

```json
{
  "prompt": "选择需要启用的功能",
  "mode": "multiple",
  "options": [
    {"id": "logs", "label": "日志"},
    {"id": "metrics", "label": "指标"},
    {"id": "traces", "label": "链路追踪"}
  ],
  "min_select": 1,
  "max_select": 2,
  "initial_selected_ids": ["logs"]
}
```

### 字段定义

- `prompt`：必填非空字符串，显示在 Dock 顶部。
- `mode`：必填枚举，只允许 `single` 或 `multiple`。
- `options`：必填数组，至少包含一个选项。
- `options[].id`：必填非空字符串，在一次请求中唯一；作为结果值返回。
- `options[].label`：必填非空字符串，作为主要展示文本。
- `options[].description`：可选字符串，作为次要说明展示。
- `initial_selected_id`：仅单选可用，若提供必须引用现有选项 ID。
- `initial_selected_ids`：仅多选可用，若提供不得重复且都必须引用现有选项 ID。
- `min_select`：仅多选可用；省略时默认为 `0`。
- `max_select`：仅多选可用；省略时默认为 `len(options)`。

### 验证规则

- `prompt`、选项 ID 和 label 经去除首尾空白后不得为空。
- 不自动修改稳定 ID 的大小写或内部字符。
- 必须满足 `0 ≤ min_select ≤ max_select ≤ len(options)`。
- 初始多选数量不得超过 `max_select`。
- 单选输入中出现 `initial_selected_ids`、`min_select` 或 `max_select` 属于输入错误。
- 多选输入中出现 `initial_selected_id` 属于输入错误。
- 未提供预选时，高亮第一项，但单选不视为已提交，多选勾选集合为空。
- JSON 解析或字段验证失败时直接返回工具错误，不发布 Broker 请求，也不打开 Dock。

## 结果协议

提交和取消均使用统一结构：

```json
{
  "cancelled": false,
  "selected_ids": ["production"]
}
```

用户取消：

```json
{
  "cancelled": true,
  "selected_ids": []
}
```

规则：

- 单选和多选均返回 `selected_ids` 数组，避免两套结果类型。
- 单选成功时数组长度固定为 1。
- 多选结果按原始 options 顺序输出，不按用户勾选时间输出，以保证确定性。
- 用户取消是成功工具结果，不设置工具错误。
- context 取消、Broker 关闭和内部失败才通过现有工具错误路径返回。
- JSON 输出应紧凑且字段顺序稳定，便于测试和模型消费。

## Selection Dock 视觉设计

### 推荐布局

```text
┌ Select · multiple ───────────────────────── 2/8 ┐
│ 选择需要启用的功能                              │
│                                                  │
│ › [x] 日志                                       │
│       收集应用运行日志                           │
│   [ ] 指标                                       │
│       收集性能与资源指标                         │
│   [x] 链路追踪                                   │
│       记录跨服务请求链路                         │
│                                                  │
│ ↑↓ move  space toggle  enter submit  esc cancel │
└──────────────────────────────────────────────────┘
```

### 视觉规则

- 标题为 `Select · single` 或 `Select · multiple`。
- 右上角显示当前高亮位置，例如 `2/8`。
- `›` 表示当前高亮项。
- 多选使用 `[ ]` 和 `[x]` 表示勾选状态。
- 单选不显示复选框，只显示高亮状态。
- description 在 label 下方缩进显示，并使用现有次要文本颜色。
- prompt、label 和 description 按终端显示宽度换行，不提供横向滚动。
- 样式应复用当前主题和输入 Dock 的边框、背景、焦点色、次要色及错误色，不硬编码与主题体系冲突的颜色。
- 工具正在等待用户时，现有 transcript 工具轨迹继续显示运行状态。

## 键盘交互

| 按键 | 单选 | 多选 |
|---|---|---|
| `↑` / `k` | 上移高亮 | 上移高亮 |
| `↓` / `j` | 下移高亮 | 下移高亮 |
| `Home` | 跳到第一项 | 跳到第一项 |
| `End` | 跳到最后一项 | 跳到最后一项 |
| `Space` | 无操作 | 切换当前项勾选状态 |
| `Enter` | 提交当前高亮项 | 提交全部已勾选项 |
| `Esc` | 取消选择 | 取消选择 |
| `Ctrl+C` | 取消选择 | 取消选择 |

导航到边界后停止，不循环跳到另一端。Selection Dock 的按键处理优先级必须高于现有 theme picker、wizard 以外的一般输入路径，并在 Dock 活跃时阻止按键落入 textarea、补全和聊天队列逻辑。

## 滚动与尺寸适配

- Dock 使用现有布局系统计算出的内容宽度和输入区域位置。
- Dock 高度可根据内容增长，但必须设置上限，保留可用 transcript 区域。
- 选项区域维护独立滚动偏移，高亮项移动后必须保持可见。
- 上方仍有隐藏内容时显示 `↑ more`，下方仍有隐藏内容时显示 `↓ more`。
- 位置指示使用选项索引，而非视觉行索引。
- description 换行可能让一个选项占多行；滚动计算必须以渲染行高为准，不能假设每项固定一行。
- 极小终端下至少优先保留 prompt、一个选项 label 和快捷键行。
- 空间不足时先截断或隐藏 description，再压缩空白；不得 panic 或生成负尺寸。

## 多选约束与就地反馈

按 Enter 提交多选时：

- 少于 `min_select`：保持 Dock 开启并显示 `Select at least N options.`。
- 多于 `max_select` 理论上不应发生；若状态异常则保持 Dock 开启并显示上限错误。
- 满足约束：完成 Broker 请求并关闭 Dock。

按 Space 尝试新增勾选且已经达到 `max_select` 时：

- 不替换或自动取消旧选项；
- 保持当前勾选集合；
- 显示 `You can select at most N options.`。

任何成功的勾选或取消操作都会清除旧校验错误。纯导航不必清除错误。

## 执行流程

```text
模型产生 Select tool call
  → Runner 发出 OnToolCall
  → Select.Run 解析并验证输入
  → Broker 排队并发布 SelectionRequest
  → Bubble Tea 收到 selectionRequestedMsg
  → appModel 创建并展示 Selection Dock
  → 用户提交或取消
  → TUI 调用 Broker.Complete(requestID, result)
  → Select.Run 解除阻塞并返回 JSON
  → Runner 发出 OnToolResult
  → 模型消费结果并继续当前回合
```

工具调用的完整输入继续保留在现有工具调用数据和工具检查视图中。常规 transcript 不重复展示完整选项列表。完成后的工具轨迹只显示简短摘要，例如：

```text
◆ Select  selected 2 options
```

取消时：

```text
◆ Select  cancelled
```

摘要渲染应接入现有工具展示格式化机制，而不是为 transcript 建立第二套工具行系统。

## 独占执行与工具批次排序

`Select` 必须形成明确的执行屏障：

```text
并发安全工具组
  → 等待组完成
  → Select
  → 等待用户结果
  → 后续并发安全工具组
```

要求：

- 保留模型产生的工具调用原始顺序。
- 遇到 `Select` 前，等待之前已经启动的并发安全工具完成。
- `Select` 完成前，不启动后续工具。
- 多个 `Select` 按原始顺序逐个展示。
- 非并发安全工具继续遵循现有串行语义；实现时应通过通用的执行屏障分类支持 `Select`，避免在 Runner 中硬编码具体 UI 渲染细节。
- Broker 已有活动请求时，新的合法请求排队等待，不覆盖活动请求，也不返回普通 busy 错误。

## 取消与生命周期

### 用户取消

Selection Dock 活跃时按 Esc 或 Ctrl+C：

- 完成当前请求，结果为 `cancelled: true`；
- 关闭 Dock并恢复普通输入区；
- 不触发模型工作 context 的取消；
- 不进入应用双击 Ctrl+C 退出逻辑；
- 当前模型回合继续。

### context 终止

应用退出、Runner 中止、外部 context 取消或 deadline 到期时：

- Broker 将请求转为失效状态并解除 `Select.Run` 阻塞；
- 工具返回 `context.Canceled` 或 `context.DeadlineExceeded`；
- TUI 收到失效通知后，仅在 request ID 匹配时关闭对应 Dock；
- 通过现有工具错误和回合终止流程处理。

### 竞态规则

- 用户提交、用户取消、context 取消和 Broker 关闭中，首个完成者决定终态。
- 后续完成调用返回未完成/已失效状态，但不得阻塞或修改结果。
- TUI 完成错误 request ID 时不得影响当前活动请求。
- TUI 收到已失效请求时忽略并清理任何匹配的陈旧 Dock。
- 关闭 TUI 前主动关闭 Broker，确保无遗留 goroutine。

## 错误处理

以下错误在打开 Dock 前返回：

- JSON 无法解析；
- `prompt` 为空；
- `mode` 无效；
- `options` 为空；
- 选项 ID 重复；
- 选项 ID 或 label 为空；
- 预选引用不存在；
- 预选 ID 重复；
- 单选和多选专用字段混用；
- 数量约束越界或矛盾；
- 初始多选数量超过 `max_select`。

错误文本需稳定并包含相关字段名，便于模型修正工具调用。不要在验证失败后向 Broker 发布半有效请求。

Broker 关闭、无法发布请求或内部协议错误应返回明确工具错误。用户取消不得标记为错误。

## 与现有系统的集成

### Tool Registry

- 在主 Bubble TUI 装配路径注册 `Select`。
- 不在子代理 Manager 创建的 Registry 中注册。
- 不在 headless 启动路径注册。
- 工具描述需明确它会暂停当前执行并等待用户在 TUI 中选择。

### Bubble Tea

- 新增专用请求和失效消息，通过 `tea.Cmd` 将 Broker 事件送入 `appModel.Update`。
- Selection Dock 活跃检查应置于普通文本输入、补全、tool inspect 快捷键和 Ctrl+C 应用级处理之前。
- Dock 打开、滚动、错误变化、完成和取消都需要触发布局或视图刷新。
- Dock 关闭后恢复 textarea focus，但不得篡改用户在工具调用前已经存在的输入草稿。

### Transcript 与工具轨迹

- `OnToolCall` 和 `OnToolResult` 继续使用现有事件路径。
- 等待期间保持工具运行状态和动画。
- 完成摘要使用结构化 Select 结果生成，避免把 JSON 原文直接显示给用户。
- 工具检查视图仍可查看完整输入和结果。

### 会话历史

`Select` 调用和结果按照普通 tool-use/tool-result 消息进入模型历史及持久化历史。Selection Dock 本身的瞬态光标、滚动和校验错误不持久化。恢复历史会话时只展示已完成的工具记录，不重新打开旧选择请求。

## 测试策略

### `internal/tool/select` 单元测试

覆盖：

- 工具名称、描述和 Schema；
- 合法单选和多选输入；
- 所有字段验证错误；
- 重复 ID、无效预选和模式字段混用；
- `min_select` / `max_select` 边界；
- 提交和取消 JSON；
- 多选结果按原始选项顺序输出；
- context 取消和 deadline；
- Broker 关闭；
- Complete 幂等性；
- 提交、取消和 context 竞态；
- 多个请求按 FIFO 顺序发布和完成；
- 等待者退出后队列不会永久堵塞。

### Selection Dock 状态机测试

将导航、勾选、验证和滚动尽量实现为可独立测试的纯状态变换。覆盖：

- 初始高亮和预选；
- `↑` / `↓` / `j` / `k` / Home / End；
- 边界不循环；
- Space 勾选和取消；
- 达到 `max_select` 后拒绝新增；
- Enter 的最小数量验证；
- 单选 Enter 返回当前高亮项；
- Esc 和 Ctrl+C 返回取消；
- 勾选变化清除旧错误；
- 导航不意外修改勾选；
- 滚动偏移始终让高亮项可见；
- 多行 description 下的可见窗口计算。

### 渲染测试

覆盖：

- single 和 multiple 标题；
- `[ ]`、`[x]` 和 `›`；
- prompt、label 和 description 的换行；
- 位置指示；
- `↑ more` / `↓ more`；
- 校验错误；
- 极窄和极矮终端；
- ANSI 样式后的显示宽度不越界；
- description 优先降级而 label 和操作提示保留；
- Dock 替换普通输入区但 transcript 仍存在。

### 集成测试

覆盖：

1. Broker 请求消息打开 Dock。
2. Dock 打开后普通 textarea、补全和聊天提交不消费选择按键。
3. transcript 保持可渲染和可滚动。
4. 提交后 Dock 关闭，输入草稿与焦点恢复。
5. 用户取消返回成功工具结果且模型回合继续。
6. 外部 context 取消关闭匹配 Dock并走工具错误路径。
7. 同批工具中 `Select` 形成执行屏障。
8. 多个 `Select` 按调用顺序出现。
9. 工具等待期间工具轨迹保持运行状态。
10. 完成和取消摘要正确渲染。
11. 主 TUI Registry 包含 `Select`。
12. headless 和子代理 Registry 不包含 `Select`。
13. TUI 退出会关闭 Broker并释放等待调用。

## 验证命令

实现完成后至少运行：

```bash
go test ./internal/tool/select/...
go test ./internal/ui/bubble/...
go test ./internal/loop/...
go test ./cmd/agent/...
go test ./...
```

如果代码库使用格式、静态分析或 race 测试任务，还应对新增包执行对应检查；Broker 并发测试建议额外运行：

```bash
go test -race ./internal/tool/select/...
```

## 兼容性与迁移

- 不修改现有 `tool.Tool` 接口。
- 不要求现有 UI 实现新增同步交互方法。
- 未运行 Bubble TUI 的环境不会看到该工具，因此不存在行为变化。
- 现有 transcript、tool inspect 和工具事件协议继续工作，仅增加 Select 专用显示摘要。
- 不迁移或恢复进行中的选择；应用重启后模型可根据普通工具错误或未完成回合重新询问。

## 成功标准

功能完成需同时满足：

- 模型可在主 TUI 调用 `Select` 并收到结构化选择结果。
- 单选和多选键盘交互符合本规格。
- 长列表在底部 Dock 内稳定滚动。
- 用户取消不终止模型回合。
- context 终止不会遗留阻塞 goroutine 或陈旧 Dock。
- 同批工具严格遵守 Select 执行屏障。
- headless 和子代理不会暴露该工具。
- 极端终端尺寸不会 panic 或破坏整体布局。
- 新增测试通过，且不回归现有完整测试套件。

## 实现约束

当前工作区存在与其他功能相关的未提交修改。实现本功能时必须：

- 不覆盖、回退或重新格式化无关修改；
- 在修改共享文件前检查当前内容和差异；
- 将 Select 相关改动保持在边界清晰的小文件中；
- 对共享 Runner、主程序装配和 Bubble Tea 状态机只做本功能所需的最小变更。
