# Paw 原生 Todo List 设计

**日期：** 2026-08-02
**状态：** 已完成设计，待实现计划
**范围：** Agent 原生工具、会话历史、Bubble Tea transcript 与独立 Todo 页面

## 背景

Paw 当前能够显示普通对话、thinking、工具调用和工具结果，但缺少一个稳定、醒目、可恢复的任务清单。复杂任务执行时间较长时，用户只能从连续对话和工具轨迹中推断当前进度，难以快速判断：

- Agent 已经完成了哪些工作；
- 当前正在处理哪一项；
- 后续还剩哪些事项；
- 恢复历史会话后此前任务进度是什么。

本设计为 Paw 增加原生 Todo 跟踪能力。Todo 由 Agent 通过专用 `update_todo` 工具维护，不从 Assistant Markdown 中解析，也不根据 `Read`、`Edit`、`Bash` 等工具调用自动推导。

首版只实现 Todo List。Phase、阶段时间线和 Todo 分组不在本次范围内，也不为其预先建立复杂抽象。

## 目标

- 为 Agent 提供结构化 `update_todo` 工具，以完整快照方式维护当前任务清单。
- 仅在复杂、多步骤任务中自动创建 Todo；简单任务保持当前交互节奏。
- 在对话流中插入醒目的 Todo 快照卡片。
- 仅完整展开最新 Todo 快照，自动折叠旧快照，并允许用户重新展开查看。
- 使用 `Ctrl+P` 打开独立 Todo 页面，集中查看当前清单。
- 将 Todo 更新作为普通会话历史的一部分持久化；恢复会话时重建当前 Todo 和历史快照。
- 对状态、ID 和并发进行严格校验，防止 UI 与 Agent 对进度产生不同理解。

## 非目标

首版不包含：

- Phase 或阶段时间线；
- Todo 分组、父子任务或嵌套层级；
- Todo 依赖关系或阻塞关系；
- 百分比进度、预计完成时间或实际耗时统计；
- 用户在 TUI 中新增、删除、编辑或改写 Todo；
- 拖动排序、鼠标排序或键盘重排；
- 根据普通工具调用自动推断 Todo 状态；
- 从 Assistant Markdown checklist 中解析 Todo；
- 多套并行 Todo List；
- 将 Todo 与 subagent 各自内部任务清单合并；
- 为未来 Phase 功能预留通用 workflow、graph 或 hierarchy 模型。

## 已确认的产品决策

- Todo 由 Agent 自动维护，用户首版只读。
- 简单问答、解释和单一小修改不创建 Todo。
- 多步骤、跨文件、实现加测试、调查加修复等复杂任务创建 Todo。
- Todo 更新通过原生 `update_todo` 工具提交，不解析自然语言。
- 每次调用提交完整清单快照，不提交增量 patch。
- 状态固定为 `pending`、`in_progress` 和 `completed`。
- 同一快照中最多只能有一个 `in_progress`。
- Agent 可以增加、删除、重排和改写事项，但同一事项应尽量保持稳定 ID。
- 对话流每次更新都产生一张完整 Todo 快照卡片。
- 最新快照默认完整展开，旧快照自动折叠为一行摘要。
- 用户可以重新展开旧快照；展开旧快照不改变哪个快照是当前状态。
- Todo 全部完成后先显示完整完成态；最终回答出现时再自动折叠完成快照。
- 独立页面快捷键固定为 `Ctrl+P`。
- `Ctrl+T` 保持现有 Tool Inspect 语义，不复用、不覆盖。
- Todo 按会话持久化，恢复会话时同步恢复。

## 用户体验

### 复杂任务开始

Agent 在开始实质执行前调用 `update_todo`：

```text
Todo                                                     0/3

  ● 检查现有 TUI 与会话恢复路径                 进行中
  ○ 实现 Todo 工具、状态同步与持久化             待处理
  ○ 实现 transcript 卡片和独立页面               待处理
```

状态图标：

- `○`：`pending`
- `●`：`in_progress`
- `✓`：`completed`

### 任务推进

完成第一项并开始第二项时，Agent 提交新的完整快照：

```text
▸ Todo updated · 0/3

Todo                                                     1/3

  ✓ 检查现有 TUI 与会话恢复路径                 已完成
  ● 实现 Todo 工具、状态同步与持久化             进行中
  ○ 实现 transcript 卡片和独立页面               待处理
```

上方旧快照自动折叠；下方最新快照完整展开。

### 全部完成

所有事项完成时，最新快照先完整显示：

```text
Todo                                                     3/3

  ✓ 检查现有 TUI 与会话恢复路径                 已完成
  ✓ 实现 Todo 工具、状态同步与持久化             已完成
  ✓ 实现 transcript 卡片和独立页面               已完成
```

当该回合最终 Assistant 回答出现后，完成快照自动折叠：

```text
✓ Todo completed · 3/3
```

这使用户在最终回答前能确认完整完成状态，同时避免完成后的清单长期占据 transcript 高度。

### 独立页面

用户随时按 `Ctrl+P` 打开独立 Todo 页面：

```text
┌ Todo ─────────────────────────────────────────── 1/3 ┐
│                                                       │
│  ✓  检查现有 TUI 与会话恢复路径             已完成   │
│  ●  实现 Todo 工具、状态同步与持久化         进行中   │
│  ○  实现 transcript 卡片和独立页面           待处理   │
│                                                       │
│  ↑↓ scroll                               esc close   │
└───────────────────────────────────────────────────────┘
```

独立页纵向展示清单，不将事项横向平铺。用户需要一眼看出顺序、状态和当前工作项。

## Agent 使用原则

`update_todo` 的工具描述和 Agent 指令必须表达以下规则。

### 应创建 Todo 的情况

- 任务需要多个明确步骤；
- 修改涉及多个文件或多个子系统；
- 任务包含调查、实现和测试等不同工作阶段；
- 用户要求完整实现、迁移、重构或系统性排查；
- 任务足够长，用户会从持续进度反馈中获益。

### 不应创建 Todo 的情况

- 普通知识问答或代码解释；
- 单一、局部且可立即完成的小修改；
- 只需执行一个简单命令或读取一个文件；
- 为了显得忙碌而把显然的一步拆成多项；
- 清单本身不能给用户提供额外可见性。

### 更新时间

- 在复杂任务开始实质执行前创建初始清单；
- 工作重心或事项状态发生实质变化时更新；
- 完成当前项后，在同一快照中将其设为 `completed`，并将下一项设为 `in_progress`；
- 新发现必要工作时可以增加事项；
- 发现事项不再必要时可以删除或改写事项；
- 最终回答前，将真实完成的事项标记为 `completed`；
- 未完成或被阻止的事项不得为了获得整齐的完成态而标记完成；
- 不逐工具调用更新，不为微小进展高频刷新。

## 数据模型

建议建立独立、无 Bubble Tea 依赖的 Todo 包，例如 `internal/todo`：

```go
type Status string

const (
    StatusPending    Status = "pending"
    StatusInProgress Status = "in_progress"
    StatusCompleted  Status = "completed"
)

type Item struct {
    ID      string `json:"id"`
    Content string `json:"content"`
    Status  Status `json:"status"`
}

type Snapshot struct {
    Explanation string    `json:"explanation,omitempty"`
    Items       []Item    `json:"items"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### 字段语义

- `id`：由 Agent 首次创建事项时提供，在后续快照中识别同一逻辑事项。
- `content`：面向用户的简短行动描述，应说明将完成什么，而非展示内部推理。
- `status`：事项当前状态，仅允许三种固定值。
- `explanation`：可选的简短更新原因，例如“开始构建独立页面”；它不是隐藏推理，也不应包含冗长过程说明。
- `updated_at`：由工具接受更新时生成，不信任模型提供的时间。

`Snapshot` 和 `Item` 在跨 goroutine、存储或 UI 边界传递时必须复制 slice，避免调用方修改已发布快照。

## `update_todo` 工具协议

### 工具名称

```text
update_todo
```

### 输入示例

```json
{
  "explanation": "开始构建独立页面",
  "items": [
    {
      "id": "inspect-structure",
      "content": "检查现有 TUI 结构",
      "status": "completed"
    },
    {
      "id": "build-page",
      "content": "构建 Todo 独立页面",
      "status": "in_progress"
    },
    {
      "id": "restore-tests",
      "content": "添加会话恢复测试",
      "status": "pending"
    }
  ]
}
```

### JSON Schema 语义

- 顶层必须是对象。
- `items` 必填，类型为数组；允许空数组表示清除当前 Todo。
- `explanation` 可选，类型为字符串。
- 每个 item 必须包含 `id`、`content` 和 `status`。
- `status` 的 Schema 枚举固定为三种合法值。
- 不接受未声明字段，避免模型拼错字段后静默丢失。

### 校验规则

1. `id` 去除首尾空白后不得为空。
2. `content` 去除首尾空白后不得为空。
3. `id` 在当前快照中必须唯一。
4. `status` 只能是 `pending`、`in_progress` 或 `completed`。
5. 同一快照中最多只能有一个 `in_progress`。
6. 首版不设置强制事项数量上限。
7. 空 `items` 表示显式清除当前 Todo。
8. `explanation`、`id` 和 `content` 可去除首尾空白，但不得改变 ID 的大小写或内部字符。
9. 更新可以改变数量和顺序，不要求新快照必须包含旧快照的全部 ID。
10. 校验失败时不发布新快照、不修改当前 Todo，也不插入 transcript 卡片。

错误文本必须稳定并指出具体字段或冲突 ID，例如：

```text
items[1].content must not be empty
items contains duplicate id "build-page"
items may contain at most one in_progress item
items[2].status must be pending, in_progress, or completed
```

### 工具结果

工具成功后返回规范化后的完整快照，而不是只返回 `ok`：

```json
{
  "accepted": true,
  "snapshot": {
    "explanation": "开始构建独立页面",
    "items": [
      {
        "id": "inspect-structure",
        "content": "检查现有 TUI 结构",
        "status": "completed"
      },
      {
        "id": "build-page",
        "content": "构建 Todo 独立页面",
        "status": "in_progress"
      },
      {
        "id": "restore-tests",
        "content": "添加会话恢复测试",
        "status": "pending"
      }
    ],
    "updated_at": "2026-08-02T12:34:56Z"
  }
}
```

空快照成功结果仍包含 `snapshot.items: []`，用于恢复时区分“明确清除”与“从未创建”。

工具结果中的快照是会话恢复的权威记录。UI 不依赖 Assistant 文本，也不依赖仅存在于内存中的回调才能恢复状态。

## 架构

采用“纯 Todo 模型 + 原生工具 + 更新 Broker + 会话历史重建 + Bubble UI 投影”的结构。

```text
Agent tool call
    │
    ▼
update_todo.Run
    ├─ parse / normalize / validate
    ├─ create authoritative Snapshot
    ├─ publish accepted snapshot to Todo Broker
    └─ return structured tool result
             │
             ├──────── ordinary tool history ───────▶ JSONL session store
             │
             ▼
Bubble appModel
    ├─ append Todo transcript snapshot
    ├─ replace current Todo state
    └─ render Ctrl+P Todo page
```

### 1. Todo 核心包

核心包负责：

- 状态、事项和快照类型；
- 输入规范化与验证；
- 深拷贝；
- 完成数、总数、是否全部完成等纯计算；
- 稳定的工具输入和结果编码。

核心包不得依赖 Bubble Tea、lipgloss、session JSONL 实现或 transcript 类型。

### 2. `update_todo` 工具

工具实现现有 `tool.Tool` 接口，负责：

- 提供工具名称、描述和输入 Schema；
- 解析并校验完整快照；
- 由 Paw 生成 `UpdatedAt`；
- 将合法快照发布给主会话 Todo Broker；
- 返回包含规范化快照的结构化结果。

`update_todo` 不执行文件操作，也不从当前工具轨迹推断进度。工具执行时间短、无外部 I/O，可声明为并发安全；但 Broker 必须串行化已接受快照，使 UI 观察顺序与工具完成顺序一致。

同一模型批次若异常产生多个 `update_todo` 调用，每个合法调用按实际完成顺序形成独立快照。Agent 提示应避免这种用法，正常情况下每次状态变化只调用一次。

### 3. Todo Broker

Broker 是工具执行路径与 Bubble Tea 事件循环之间的窄边界。它只发布已经校验和带时间戳的快照，不包含视觉状态。

要求：

- 每个订阅者按发布顺序观察快照；
- 发布和订阅支持 context 取消；
- 关闭 Broker 能解除等待中的 Bubble Tea command；
- 发布前后都使用深拷贝，调用方不能修改 Broker 内部状态；
- Broker 可保存当前最新快照，供 UI 初始化或遗漏事件后同步；
- 空快照同样需要发布；
- Broker 不持有 transcript 展开状态、页面滚动状态或主题样式。

### 4. Bubble Tea 状态

`appModel` 至少维护：

- 当前最新 Todo 快照；
- 当前 Todo 是否存在，以及是否被明确清除；
- 独立 Todo 页面状态；
- transcript 中每个 Todo 快照自己的展开状态；
- 最新 Todo 快照对应的 transcript entry；
- 等待 Broker 下一事件的 command 生命周期。

Todo 页面和 transcript 卡片消费同一份快照数据，不分别维护两套可变清单。

### 5. 会话持久化与恢复

Todo 更新作为正常 `update_todo` tool-use/tool-result 消息进入现有会话 JSONL 历史，不建立第二套独立 Todo 文件。

恢复会话时：

1. 按历史顺序扫描已完成的 `update_todo` 工具结果；
2. 只接受能够成功解析且通过当前结构校验的成功结果；
3. 为每个合法结果恢复一条 Todo transcript 快照；
4. 后一快照成为当前 Todo，前一快照按旧快照规则折叠；
5. 最后一个合法结果为空清单时，当前 Todo 为空；
6. 未完成的 tool-use、错误结果或损坏结果不得覆盖此前合法快照；
7. 恢复过程不重新执行 `update_todo`，也不向模型新增消息。

这样 JSONL 中的工具结果既是模型所见记录，也是 UI 恢复记录，避免双写事务和状态漂移。

## 工具注册范围

首版 `update_todo` 表达的是主会话的用户可见计划，因此：

- 注册到主 Agent 的工具 Registry；
- 不注册到同步或后台 subagent Registry；
- subagent 的内部步骤不直接覆盖主会话 Todo；
- 主 Agent 可以根据 subagent 返回结果更新自己的 Todo；
- 只有存在主会话 Todo 状态接收器时才装配 Broker。

如果 headless 主 Agent 与 Bubble TUI 共用注册路径，工具仍可正常返回和持久化结构化结果，但不要求提供 Todo 页面；不得因为没有 TUI 消费者而阻塞工具执行。

## Transcript Todo 卡片

### 专用 entry 类型

Todo 卡片应成为 transcript 的专用结构化 entry，而不是伪装成 `entrySystem` 的预格式化字符串。entry 保存快照和 UI 展开状态，渲染时根据当前宽度生成内容。

这样可以保证：

- resize 后重新按 cell 宽度布局；
- 旧快照可以独立展开和折叠；
- 不需要从已渲染文本反向解析状态；
- 点击、键盘操作和位置计算使用稳定 entry 身份；
- 会话恢复后与实时更新使用同一路径。

### 插入与折叠规则

收到合法非空快照时：

1. 将此前最新 Todo 快照标记为旧快照并自动折叠；
2. 在 transcript 尾部插入新的完整快照卡片；
3. 更新当前 Todo；
4. 遵循现有 smart-scroll 和新消息通知语义，不强制把手动离开底部的用户拉到底部。

收到合法空快照时：

1. 折叠此前最新快照；
2. 清空当前 Todo；
3. 插入简短记录 `Todo cleared`，用于历史可见性；
4. `Ctrl+P` 页面进入空状态。

最终 Assistant 回答完成时，如果当前快照全部完成，则自动折叠该快照。若仍有 `pending` 或 `in_progress`，不得自动折叠为 completed 摘要。

### 折叠摘要

进行中的旧快照：

```text
▸ Todo updated · 1/4
```

全部完成并因最终回答折叠：

```text
✓ Todo completed · 4/4
```

空快照：

```text
─ Todo cleared
```

摘要中的完成数和总数来自该 entry 自己的快照，而不是当前最新快照。

### 完整卡片布局

```text
Todo                                                     1/4

  ✓  检查现有会话持久化路径                    已完成
  ●  添加 update_todo 工具                     进行中
  ○  渲染 transcript Todo 卡片                 待处理
  ○  添加 Ctrl+P 独立页面                      待处理

  开始实现工具与状态同步
```

视觉规则：

- 标题和 `completed/total` 位于首行两端；终端过窄时允许计数紧跟标题。
- 每个事项纵向占一行或多行，不横向排列多个事项。
- 图标、内容和状态标签形成稳定列；窄终端优先保留图标与内容，状态标签可以降级隐藏。
- 长内容按终端 cell 宽度换行，续行与内容起点对齐。
- `explanation` 若非空，显示为卡片底部的次要文本。
- 使用现有主题的 success、active/signal 和 muted 语义色，不硬编码只适用于某一主题的颜色。
- `NO_COLOR=1` 时仍通过图标和状态文字表达差异。
- 不使用 Web 风格圆角、阴影、渐变或终端无法稳定实现的装饰。
- 卡片必须在极窄终端下保持非负尺寸且不 panic。

### 展开交互

Todo 快照沿用 transcript 现有可聚焦/可点击条目模式：

- 点击折叠摘要切换该快照展开状态；
- 点击完整卡片标题区域可折叠；
- 若现有 transcript 已提供统一 Enter 展开行为，Todo 应复用该键位；
- 展开旧快照只影响该 entry 的 UI 状态，不修改当前 Todo；
- 插入新快照时，无论旧快照此前是否被用户展开，都将它自动折叠；用户之后仍可再次展开。

## `Ctrl+P` 独立 Todo 页面

### 打开与关闭

- 在普通主界面按 `Ctrl+P` 打开 Todo 页面。
- 页面打开后按 `Esc` 或再次按 `Ctrl+P` 关闭。
- `Ctrl+T` 继续打开 Tool Inspect。
- Todo 页面活跃时，其键盘处理优先于 textarea、completion、聊天提交和 transcript 普通导航。
- 页面关闭后恢复原输入草稿和 textarea focus。

### 内容

独立页只显示当前最新 Todo，不显示历史快照列表。

- 标题显示 `Todo` 和 `completed/total`。
- 事项按快照顺序纵向展示。
- 当前 `in_progress` 项使用最醒目的 active/signal 样式。
- 已完成项使用 success 样式。
- 待处理项使用普通或 muted 样式。
- `explanation` 可作为标题下的次要说明显示。
- 内容超过可见高度时页面内部纵向滚动。
- 高亮或滚动位置不代表选择，用户不能编辑状态。

### 空状态

当前会话从未创建 Todo，或最新更新明确清空时显示：

```text
No active todo list

The agent creates one automatically for complex tasks.

esc close
```

空状态下仍正常打开页面，以便用户确认当前确实没有 Todo，而不是让快捷键看起来失效。

### 实时更新

Todo 页面打开期间收到新快照时：

- 页面立即替换为最新完整快照；
- 滚动位置重置到顶部，确保用户看到新的当前工作项和计数；
- 不自动关闭页面；
- transcript 同时按正常规则插入新快照。

## 与现有 UI 模式的优先级

现有 Bubble Tea 已包含 Selection Dock、主题选择器、设置向导、模型向导、session picker、subagent picker 和 Tool Inspect。Todo 页面是独占页面，不应与其他 modal 同时响应按键。

按键处理遵循以下原则：

1. 已经打开的高优先级交互 Dock 或 modal 先处理自己的按键；
2. Todo 页面打开时优先处理 Todo 页面按键；
3. 普通界面下 `Ctrl+P` 打开 Todo 页面；
4. `Ctrl+T` 保持 Tool Inspect；
5. 未被消费的按键才进入 completion、textarea 和聊天逻辑。

当 Selection Dock 等阻塞交互正在等待用户输入时，`Ctrl+P` 不应抢占它。Todo 页面可在该交互结束后打开。

## 最终回答联动

“全部完成后最终回答出现时折叠”必须以明确的回合完成事件驱动，而不是搜索 Assistant 文本。

规则：

- `update_todo` 将所有事项设为 `completed` 时，卡片仍保持完整展开；
- 当前模型回合产生最终 Assistant 内容并完成后，若当前 Todo 仍全部完成，则折叠最新快照；
- 如果最终回答前又收到非全完成快照，则不执行完成折叠；
- 模型回合出错或被用户取消且未产生正常最终回答时，不伪装成成功完成；
- 恢复历史时，如果已完成快照后存在该回合的最终 Assistant 消息，应恢复为折叠完成态；否则保持完整态；
- 新一轮复杂任务创建新快照时，旧的完成快照保持折叠，新快照成为当前清单。

## 普通工具轨迹显示

`update_todo` 仍通过现有 `OnToolCall` / `OnToolResult` 路径进入工具历史和 Tool Inspect，但常规 transcript 不应同时展示冗长原始 JSON 和 Todo 卡片。

工具轨迹使用简短摘要：

```text
✓ Todo: updated 1/4
```

清除时：

```text
✓ Todo: cleared
```

完整输入和结构化结果继续可在 Tool Inspect 中查看。专用 Todo 卡片是面向用户的主展示，工具轨迹不得重复打印所有事项。

## Smart scroll 与新消息通知

Todo 快照是新的可见 transcript 内容，应遵循现有滚动约束：

- 用户位于底部时，插入快照后继续跟随底部；
- 用户不在底部时，保持 `YOffset`，不得强制跳转；
- 非底部插入一张 Todo 快照计为一条逻辑新消息；
- 同一次快照的 relayout、动画或展开状态变化不重复计数；
- 用户主动展开旧快照不计为新消息；
- 恢复会话和初次加载历史不产生未读计数。

## 错误与异常处理

### 工具输入错误

- 返回普通工具错误；
- 保留此前当前 Todo；
- 不插入 Todo 卡片；
- 不发布 Broker 事件；
- Tool Inspect 中仍可查看失败输入和错误。

### Broker 或 UI 不可用

- headless 情况下工具仍应成功返回结构化结果并进入会话历史；
- TUI Broker 已关闭时，不应使已经合法的 Todo 更新永久阻塞；
- 应用关闭期间未消费的 UI 通知可以丢弃，权威快照仍存在于工具结果历史中；
- 恢复会话时从持久化结果重建。

### 损坏的历史记录

- 无法解析的 `update_todo` 结果被忽略；
- 忽略损坏记录时保留此前最后一个合法快照；
- 恢复操作不得修改原始 JSONL；
- 可以通过现有日志记录诊断信息，但不向 transcript 插入重复错误卡片。

### 空清单

空清单是合法的显式清除操作，不是校验错误。它必须持久化、可恢复，并更新独立页空状态。

## 测试策略

### Todo 核心模型与工具单元测试

覆盖：

- 工具名称、描述和 JSON Schema；
- 合法的 pending、in_progress、completed 快照；
- `id` 和 `content` 首尾空白规范化；
- 空 ID、空 content；
- 重复 ID；
- 无效 status；
- 两个或更多 `in_progress`；
- 零个 `in_progress` 合法；
- 空 items 清除；
- 顺序、增加、删除和改写事项合法；
- 更新时间由 Paw 生成；
- 工具结果包含完整规范化快照；
- 校验失败不发布事件；
- 深拷贝防止调用方修改已发布快照；
- context 取消和 Broker 关闭不遗留 goroutine。

### 会话持久化与恢复测试

覆盖：

- `update_todo` 调用和结果写入现有 JSONL 历史；
- 多个快照按顺序恢复；
- 最新合法快照成为当前 Todo；
- 旧快照恢复为折叠态；
- 空快照恢复为 cleared 状态；
- 错误工具结果不覆盖旧状态；
- 损坏结果被忽略；
- 未完成 tool-use 不产生快照；
- 完成快照后存在最终 Assistant 回答时恢复为完成折叠态；
- session switch 不串用另一会话的 Todo；
- 新会话初始 Todo 为空。

### Transcript 卡片测试

覆盖：

- 三种状态的图标、文字和主题样式；
- 标题计数和完成数计算；
- explanation 展示；
- 最新快照完整展开；
- 新快照插入后旧快照自动折叠；
- 点击或 Enter 可重新展开旧快照；
- 展开旧快照不改变 current Todo；
- 全完成快照在最终回答前保持展开；
- 最终回答后折叠为 `Todo completed`；
- 非全完成快照不触发完成折叠；
- 空快照显示 `Todo cleared`；
- 长中文、英文、emoji 和宽字符安全换行；
- 宽度 `120 / 80 / 40 / 20` 下不溢出、不 panic；
- transcript location 与点击命中高度匹配；
- `NO_COLOR=1` 仍能辨认状态。

### 独立页面测试

覆盖：

- 普通界面 `Ctrl+P` 打开；
- `Esc` 和再次 `Ctrl+P` 关闭；
- `Ctrl+T` 仍打开 Tool Inspect；
- 页面打开时按键不落入 textarea 或聊天提交；
- Selection Dock 活跃时不被 `Ctrl+P` 抢占；
- 页面显示当前最新快照而非旧快照；
- 空状态正确；
- 长清单可以上下滚动；
- 页面打开期间收到新快照立即刷新并回到顶部；
- 关闭后输入草稿和 focus 恢复；
- resize 后页面保持尺寸安全。

### 集成测试

覆盖：

1. 主 Agent Registry 包含 `update_todo`。
2. subagent Registry 不包含 `update_todo`。
3. 合法工具调用同时产生结构化结果和 Todo UI 事件。
4. 无 TUI 消费者时工具不会阻塞。
5. transcript 卡片与普通工具轨迹不会重复展示完整 JSON。
6. 非底部收到 Todo 更新时保持 scroll offset，并只增加一次新消息计数。
7. session restore 后 transcript 卡片和 `Ctrl+P` 页面状态一致。
8. 会话 A 与会话 B 的 Todo 状态隔离。
9. 应用退出关闭 Broker，不遗留等待 command。
10. 完整测试套件不回归现有 Tool Inspect、Selection Dock、session picker 和输入行为。

## 验证命令

实现完成后至少运行：

```bash
go test ./internal/todo/... -count=1
go test ./internal/session/... -count=1
go test ./internal/ui/bubble/... -count=1
go test ./cmd/agent/... -count=1
go test ./internal/subagent/... -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

若 Broker 使用 goroutine 或 channel，额外运行：

```bash
go test -race ./internal/todo/... ./internal/ui/bubble/... -count=1
```

并在真实 TUI 中手动验证：

1. 复杂任务开始时出现初始 Todo 卡片；
2. 多次更新后只展开最新快照；
3. 可以展开旧快照；
4. `Ctrl+P` 显示纵向完整清单；
5. 全部完成后先看到完整完成态；
6. 最终回答出现后完成卡片折叠；
7. 退出并恢复会话后状态一致；
8. 简单任务不滥用 Todo；
9. `Ctrl+T` Tool Inspect 和 Selection Dock 不回归。

## 兼容性与迁移

- 不修改现有 `tool.Tool` 接口。
- 不要求修改既有历史记录；没有 `update_todo` 结果的旧会话自然显示为空状态。
- Todo 通过普通工具消息持久化，不新增独立数据库或迁移步骤。
- 现有 Tool Inspect、工具调用记录和模型历史继续保留原始输入与结果。
- Todo UI 状态仅投影结构化工具结果，不影响模型消息的普通加载。
- Phase 后续若需要实现，应单独设计；不得在本次实现中提前引入阶段字段或多层模型。

## 成功标准

功能完成必须同时满足：

- Agent 能通过原生 `update_todo` 工具可靠维护完整 Todo 快照。
- 简单任务不创建 Todo，复杂任务在执行前创建并在实质进展时更新。
- 同一快照最多一个事项处于 `in_progress`。
- 每次合法更新在 transcript 中产生醒目的完整快照卡片。
- 仅最新快照默认展开，旧快照可重新展开。
- 全部完成状态在最终回答前完整可见，最终回答后自动折叠。
- `Ctrl+P` 独立页面以纵向布局展示当前清单。
- Todo 随会话持久化、切换和恢复，不跨会话污染。
- `Ctrl+T`、Selection Dock、输入草稿、smart scroll 和 Tool Inspect 不回归。
- 无 TUI 消费者时工具不会阻塞。
- 新增测试和完整 Go 测试套件通过。

## 实现约束

当前工作区存在大量与其他功能相关的未提交修改。实现本功能时必须：

- 不覆盖、回退或重新格式化无关修改；
- 修改共享文件前检查当前内容和差异；
- 优先创建职责单一的新文件，避免继续扩大 `app.go`、`transcript.go` 和 `types.go`；
- 对主程序装配、session restore 和 Bubble Tea 状态机只做本功能所需的最小改动；
- 任何 commit 只包含 Todo 功能相关文件或可明确归属的共享文件片段；
- 在编写实现计划前重新核对当前代码，因为工作区中的共享文件可能继续变化。
