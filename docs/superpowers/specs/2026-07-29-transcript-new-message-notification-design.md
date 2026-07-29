# Paw TUI 非底部新消息通知设计

## 状态

- 日期：2026-07-29
- 状态：已获用户确认，待实施
- 关联能力：transcript smart scroll、context usage status line、TUI 鼠标交互

## 背景

Paw 当前的 transcript 已支持 smart scroll：用户手动离开底部后，assistant 流式输出、thinking 更新、工具结果和窗口 resize 不会强行把 viewport 拉回底部。

但用户离开底部后无法知道下方是否产生了新内容，也无法快速回到最新消息。本设计增加一个只存在于 TUI 的新消息提示，不改变已经修复的滚动跟随规则。

## 目标

当用户位于 transcript 非底部位置且产生新的可见内容时：

1. 保持当前 viewport 偏移。
2. 在现有 context usage 进度条上方居中显示一行浮空提示。
3. 使用中文文案和背景色强调，不绘制边框或独立面板。
4. 点击提示后跳到最新消息并清除计数。
5. 用户通过滚轮、`End` 或方向键回到底部时自动清除计数。

## 非目标

- 不新增操作系统级通知或桌面 toast。
- 不替换现有 `Notifier` 的普通 UI 通知职责。
- 不改变 smart scroll 的自动跟随和手动滚动语义。
- 不增加新的键盘快捷键。
- 不逐 token 或逐 cursor frame 产生通知计数。

## 已确认的用户体验

### 默认状态

未读数量大于 0、提示未被鼠标悬停时显示：

```text
↓ N 条新消息
```

### 悬停状态

鼠标位于提示文字的可点击区域时显示：

```text
↓ N 条新消息 · 回到底部
```

### 视觉

- 提示文字使用项目现有 Signal Cyan 视觉语言。
- 背景色只覆盖提示文字本身，可带少量水平内边距。
- 不绘制边框、圆角框、分隔线或独立背景面板。
- 普通和悬停状态使用两档背景/前景对比度，确保可识别。
- 终端过窄时按 cell 宽度安全截断，至少保留数量和“新消息”语义。

## 状态与数据模型

在 `appModel` 中维护以下状态：

- `newMessageCount int`：当前离底部后累积的可见新消息数量。
- `newMessageNoticeHovered bool`：提示是否处于鼠标悬停状态。
- 提示命中区域：由当前布局、终端宽度和提示文字动态计算，用于鼠标 hover/click。
- 当前 detached scroll 周期内的事件去重标记：避免同一条 assistant 流、thinking 过程或 tool result 重复计数。

提供集中式的行为方法，负责事件判定和清零：

- `recordTranscriptActivity(...)`：在逻辑上的可见消息/事件产生时调用。
- `clearNewMessageNotice()`：将数量、悬停状态和去重标记一起清空。
- `syncNewMessageNoticeAfterScroll()`：滚动处理完成后，如果 viewport 已在底部则清空提示。
- `handleNewMessageNoticeClick()`：跳到底部、刷新 viewport 并清空提示。

通知状态只属于当前 TUI 会话。初次加载历史、切换 session、`/clear` 和恢复 session 时都应清空，不把历史内容误判为新消息。

## 计数语义与事件入口

计数判断必须发生在 transcript 内容变更之前，以当前 viewport 是否在底部作为依据：

- 在底部：不增加计数，沿用现有自动跟随。
- 不在底部：增加对应的逻辑事件数量，并保留手动滚动位置。

### Assistant 流

一条 assistant 流视为一条逻辑消息。第一次在非底部状态下提交该流的新可见内容时计数一次；后续稳定行、流式 delta、Markdown 重渲染和 cursor frame 不重复计数。

如果 assistant 流在用户仍位于底部时开始，用户随后向上滚动，则在离开底部后的第一次新内容提交时计数一次。这样不会因为用户已经看过的首段内容而产生通知遗漏。

### Thinking

thinking 的增量更新不逐次计数；thinking 内容完成并被 finalize 时计数一次。无内容的空 thinking 不计数。

### Tool

- tool call 的 `running` 预览不计数。
- tool result 完成并更新/创建可见结果时计数一次。
- 同一个 tool use ID 的重复刷新不重复计数。
- tool error 结果仍属于一条 tool result。

### System、Error、Shell、Subagent

新的可见 system、error、shell、subagent 结果各计数一次。用户主动触发命令后产生的可见系统反馈也遵循同一规则；用户自己的 `entryUser` 不计数。

### 不产生 transcript 新消息的刷新

窗口 resize、viewport 内容重设、selection 重绘、tool elapsed 更新、光标动画和 context meter 动画都不计数。

## 渲染与布局

提示不新增固定布局行，不修改 `statusHeight`，避免提示出现/消失时改变 transcript 高度并造成滚动跳动。

渲染顺序如下：

1. 按现有逻辑渲染 transcript 内容和 selection。
2. 在 transcript 区域的最底部叠加一行居中的提示。
3. 下方继续渲染现有 context usage/status line 和输入区。

提示作为浮空层放在 transcript 最后一行，视觉上紧贴 context usage 进度条上方。应复用现有的 styled-cell overlay 和 cell 宽度计算工具，保证宽字符、ANSI 样式和窄终端下布局稳定。

当 modal 或 completion overlay 正在占用 transcript 区域时，不覆盖其内容；未读数量继续保留，覆盖层消失后恢复显示。文本 selection 不属于 modal，提示仍然显示并继续计数。

提示没有独立布局高度，因此显示提示不会调用 relayout，也不会改变 viewport 的 `YOffset` 或 `Height`。

## 鼠标交互

提示命中区域只覆盖带背景色的提示文字及其水平内边距，不占用整行。

- 鼠标 motion 进入命中区域：设置悬停状态并刷新 View。
- 鼠标 motion 离开命中区域：恢复默认文案和背景色。
- 鼠标 press/release 形成一次点击且最终位置仍在命中区域：执行回到底部。
- 跨行拖拽仍优先作为文本 selection；不会因为经过提示区域而误触发跳转。
- 点击提示时清除当前 selection 状态，随后将 viewport 定位到底部。

提示点击处理应优先于 transcript 普通行选择处理，但不改变其他 transcript 鼠标行为。

## 滚动状态转换

```text
底部 + 新事件
  -> 保持底部跟随，计数仍为 0

非底部 + 新事件
  -> 保持 YOffset，计数加 1，显示提示

非底部 + 滚动到最后
  -> 清零计数，隐藏提示

非底部 + 点击提示
  -> GotoBottom，清零计数，隐藏提示
```

用户在非底部期间持续选择历史文本时，仍按上述“非底部 + 新事件”规则计数；选择不会暂停通知。

## 测试设计

### 渲染

- `newMessageCount == 0` 时不渲染提示。
- 默认文案为 `↓ N 条新消息`。
- 悬停文案为 `↓ N 条新消息 · 回到底部`。
- 提示居中、背景色存在、无边框 ANSI 绘制。
- 窄终端下输出不超过可用 cell 宽度。
- modal/completion overlay 不被提示覆盖。

### 计数与 smart scroll

- 非底部收到 assistant 流时计数为 1，并保留原始 `YOffset`。
- 同一 assistant 流的多个 delta/stable line 仍只计数 1。
- thinking finalize、tool result、system/error/shell/subagent 分别正确增加 1。
- tool call running 不增加计数；同一 tool result 重复刷新不重复增加。
- 底部收到上述事件时不增加计数且继续跟随底部。
- selection active 时仍增加并显示计数。
- cursor frame、resize、selection 重绘和 context meter 动画不增加计数。

### 交互与清理

- 滚轮、方向键或 `End` 回到底部后清零。
- 点击提示后跳到底部、清零并退出 selection。
- 离开底部后再次产生事件，计数从 1 重新累积。
- session restore、session switch 和 `/clear` 清除残留状态。
- resize 不清零、不重复计数，提示命中区域跟随新宽度。

## 验收

实施完成后至少运行：

```text
go test ./internal/ui/bubble -count=1
go test ./... -count=1
git diff --check
```

并在真实 TUI 中验证：手动向上滚动、等待 assistant/tool/system 内容出现、点击中文提示、用滚轮或 `End` 回到底部，以及选择历史文本时继续接收新消息。
