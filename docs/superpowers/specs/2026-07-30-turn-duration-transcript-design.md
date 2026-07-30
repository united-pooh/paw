# Transcript 回答耗时与 response_at 设计

日期：2026-07-30

## 1. 背景与目标

Bubble Tea 当前只在运行中的顶部 header 保存 turnStartedAt。一轮回答结束后，这个值被清空，历史 transcript 只剩消息正文，因此用户无法回看上一轮模型回答运行了多久，也无法知道模型何时完成回答。

本功能为同一个 session 的模型回答增加持久化的时间元数据，并在回答正文下方渲染一行灰色 footer：

~~~text
1m35s · 07:47:47 AM
~~~

其中左侧为完整模型回合耗时，右侧为回答完成时间。时间信息只属于 transcript 展示，不进入模型上下文。

## 2. 范围

### 2.1 首版包含

- 记录主会话每个模型回合的开始时间、完成时间和耗时；
- 耗时从用户提交该回合开始，到最终 assistant 回答提交结束；
- 计时包含 thinking、工具调用和重试；
- 持久化所有已完成回合的时间元数据；
- 在每个已完成 assistant transcript entry 下显示灰色 footer；
- 恢复 session 时恢复 footer；
- 最近三个模型回答可以直接在同一 transcript 中看到各自的耗时；
- 保持现有顶部 working 实时计时；
- 保持模型上下文、token 统计和原始消息内容不变。

### 2.2 首版不包含

- 不改 Activity、Subagents 卡片、/tasks 或后台 subagent TaskSnapshot 展示；
- 不为每个 API round 单独展示耗时；
- 不增加网页式卡片、侧栏或新的 transcript 布局；
- 不把 footer 文本拼入 assistant message body；
- 不把时间元数据加入模型 prompt、上下文统计或模型请求；
- 不为失败/取消且没有最终回答的回合显示成功回答 footer；
- 不改变现有模型消息 JSON 格式。

## 3. 用户可见行为

### 3.1 正常回答

模型流式输出期间，顶部 header 继续显示当前实时状态，例如：

~~~text
⠋ 1m35s working
~~~

模型回合完整结束后，最终 assistant entry 的 Markdown 正文下面追加一行：

~~~text
agent >

回答正文……

1m35s · 07:47:47 AM
~~~

footer 只包含两个格式化值，不显示 duration、response_at 或其他标签。整行使用现有低对比度灰色语义样式（colorContextFree 或等价主题角色），不使用新的颜色字面量。

### 3.2 时间格式

- 持久化时间使用 UTC RFC3339Nano；
- 展示时间转换为本地时区；
- response_at 使用 12 小时制 03:04:05 PM；
- duration 内部保存毫秒；
- 展示规则沿用现有计时风格：小于 1 秒显示毫秒，短于 1 分钟显示秒，超过 1 分钟显示 XmYYs，例如 950ms、12s、1m35s。

### 3.3 异常和兼容行为

- 回合失败或取消且没有最终 assistant 回答：显示已有错误信息，不显示成功 footer；
- 旧 session 没有时间元数据：照常恢复消息，不显示 footer；
- 单条元数据损坏：跳过该条记录，不阻塞消息历史加载；
- 时间元数据写入失败：完成事件发送前进行有限重试；仍失败时不让模型回答失败，当前 UI 可暂时显示内存中的 footer，并记录明确的持久化错误，禁止静默吞掉失败；
- 窄终端沿用现有 transcript cell-width 裁剪和缓存规则。

## 4. 持久化模型

### 4.1 文件位置

每个 session 增加独立的 sidecar：

~~~text
.paw/sessions/<session-id>/turns.jsonl
~~~

transcript.jsonl 继续只保存原始 message.Message 记录。turns.jsonl 是 session transcript 的展示元数据，不是模型历史。

### 4.2 数据结构

~~~go
type TurnStatus string

const (
    TurnCompleted TurnStatus = "completed"
    TurnFailed    TurnStatus = "failed"
    TurnStopped   TurnStatus = "stopped"
)

type TurnMetadata struct {
    TurnID        string
    AssistantSeq *int64
    StartedAt     time.Time
    ResponseAt    *time.Time
    DurationMS    int64
    Status        TurnStatus
}
~~~

字段序列化名称固定为 turn_id、assistant_seq、started_at、response_at、duration_ms 和 status。

正常完成记录至少包含：

~~~json
{
  "turn_id": "turn-42",
  "assistant_seq": 18,
  "started_at": "2026-07-30T23:46:12.000Z",
  "response_at": "2026-07-30T23:47:47.000Z",
  "duration_ms": 95000,
  "status": "completed"
}
~~~

assistant_seq 指向该回合最终 assistant message 的 session record 序号。它是 UI 关联键，不能改变消息内容。失败/停止记录可以保留开始和结束耗时，但没有 assistant_seq 或 response_at 时不渲染成功 footer。

### 4.3 存储边界

internal/session 提供独立的 turn metadata 存储能力，至少包含：

- 追加一条已完成的 metadata；
- 读取当前 session 的 metadata；
- 首版只读取当前 session 自己的 sidecar；fork 继承的父会话消息若没有当前 session metadata 则不显示 footer，但不得污染消息历史；
- 忽略不存在的 sidecar，兼容旧 session；
- 跳过损坏行并保留其他有效记录。

该能力不改变 LoadResolvedHistory 的返回类型，模型仍只获得 []message.Message。

## 5. 运行时数据流

### 5.1 开始回合

appModel.startChatTurn 在接受用户输入时创建本轮 turn_id 和 started_at，继续用同一时间驱动顶部实时计时，并通过回合执行上下文传给 loop.Runner。排队输入只有真正开始执行时才创建开始时间。

### 5.2 完成回合

由 loop.Runner 的回合最终化边界统一确定 response_at：

1. 执行完整模型回合，包括工具闭环、thinking 和重试；
2. 得到最终 assistant message；
3. 将本轮新增消息提交到 transcript.jsonl，取得最终 assistant record 的 seq；
4. 计算 duration_ms = response_at - started_at；
5. 将 TurnMetadata 写入 turns.jsonl；
6. 向 Bubble UI 发送包含该 metadata 的完成事件；
7. Bubble 将 metadata 绑定到最终 assistant entry 并刷新 viewport。

doneMsg 只表示模型流结束，不负责固化 footer；只有 turn 完整提交后的完成事件才可以显示持久化时间。

### 5.3 恢复回合

恢复 session 时：

1. 按现有逻辑读取 resolved message history；
2. 读取当前 session 可用的 turns.jsonl；
3. 通过 assistant_seq 将 metadata 绑定到对应最终 assistant entry；
4. 渲染 Markdown 正文和灰色 footer；
5. 不将 metadata 写回 message.Message，也不重新提交任何模型上下文。

## 6. Bubble transcript 设计

transcriptEntry 增加 UI-only 的可选 timing 字段，不能复用 body 保存 footer 文本。渲染职责如下：

- renderEntryBodyAt 只渲染正文和现有 citations；
- renderEntryAt 在 assistant 正文之后按需追加 footer；
- footer 的格式化由独立纯函数完成，输入为 duration_ms、response_at 和当前时区；
- transcript render cache key 必须包含 timing 字段，避免元数据更新后继续使用旧渲染结果；
- footer 使用 cell-width 安全的现有截断逻辑；
- footer 不参与 Markdown 解析，避免模型正文中的 Markdown 影响元信息样式。

恢复主 session 时由 session picker 的主 transcript 重建路径应用 metadata。subagent transcript preview 不在首版范围内，因此不依赖该 metadata。

## 7. 组件职责

- internal/session：保存和读取 TurnMetadata sidecar；保证旧 session 与损坏行兼容；
- internal/loop：拥有完整回合的开始/结束生命周期，生成最终时间和 assistant record 序号；
- internal/ui/bubble：保存当前回合的 UI 状态、接收完成 metadata、绑定 transcript entry；
- internal/ui/bubble/transcript.go：渲染 footer、维护渲染缓存和终端宽度约束；
- internal/ui/bubble/session_picker.go：恢复主 session 时加载并应用 metadata。

不修改 internal/subagent.TaskSnapshot、Activity 面板或 /tasks 输出。

## 8. 测试与验收

### 8.1 Session 存储

- round-trip 写入和读取 TurnMetadata；
- 缺少 turns.jsonl 时正常返回空 metadata；
- 单条 JSON 损坏时保留其余有效记录；
- assistant_seq、duration_ms、response_at 精确保留；
- 与现有 session fork/history 逻辑不发生消息污染。

### 8.2 Runner 生命周期

- 从回合开始到最终回答完成计算完整 duration；
- 工具调用和重试时间被包含；
- 最终 assistant record 的 seq 正确写入 metadata；
- doneMsg 之前和消息提交失败时不会错误固化成功 footer；
- metadata 写入失败不会伪装成模型回答失败。

### 8.3 Bubble 渲染

- 精确渲染 1m35s · 07:47:47 AM；
- footer 为灰色且不包含 duration、response_at 标签；
- 三个连续模型回合分别显示各自 footer；
- 恢复 session 后 footer 与正确 assistant entry 重新关联；
- footer 不出现在 Runner 发送的 message body 或 prompt 中；
- 窄宽度下所有渲染行仍符合终端 cell width。

### 8.4 回归验证

至少运行：

~~~text
go test ./internal/session -count=1
go test ./internal/loop -count=1
go test ./internal/ui/bubble -count=1
go test ./... -count=1
git diff --check
~~~

必要时使用 NO_COLOR=1 验证纯文本内容，并通过真实 PTY/Ghostty 检查灰色 footer、滚动跟随和窄终端布局。

## 9. 完成标准

当用户在同一个 session 连续完成三轮模型回答后，重新打开该 session，最近三条 assistant 回答下方都能看到类似：

~~~text
42s · 07:40:53 AM
2m18s · 07:44:37 AM
1m35s · 07:47:47 AM
~~~

这些值来自持久化 sidecar，正文和模型上下文保持原样，且不出现新的卡片式 UI 或额外标签。
