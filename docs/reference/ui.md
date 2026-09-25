# `internal/ui`

#### 抽象层

文件: [ui.go](../../internal/ui/ui.go)

##### `type ToolCallEvent struct`

字段:
- `ID`
- `Name`
- `Input`
- `FileMutationKnown` — 是否知道文件变更目标
- `IsFileMutation` — 是否文件变更类工具
- `FileMutation` (`*FileMutationSnapshot`) — 变更前后快照

##### `type ToolResultEvent struct`

字段:
- `ToolUseID`
- `Name`
- `Content`
- `IsError`
- `FileMutationKnown`
- `IsFileMutation`
- `FileMutation` (`*FileMutationSnapshot`)

##### `type FileMutationSnapshot struct`

字段:
- `Before` / `After` — 变更前后内容
- `BeforeExists` / `AfterExists` — 变更前后文件是否存在

用途:
- 只用于 UI 展示真实修改差异，不写入 `message.ToolResult` 或 tracing payload

##### `type SystemEvent struct`

字段:
- `Title`
- `Body`
- `Color`

##### `type UI interface`

方法:
- `OnAssistantDelta(text string) error`
- `OnToolCall(event ToolCallEvent) error`
- `OnToolResult(event ToolResultEvent) error`
- `OnDone() error`

这是 loop 层唯一依赖的 UI 抽象。

##### `type ThinkingDeltaReceiver interface`

方法:
- `OnThinkingDelta(text string) error`

用途:
- 可选扩展：接收模型 thinking 流

##### `type SystemNotifier interface`

方法:
- `OnSystemMessage(event SystemEvent) error`

用途:
- 可选扩展：接收后台任务完成等系统事件

##### `type FileMutationConsumer interface`

方法:
- `ConsumesFileMutations() bool`

用途:
- 只有选择消费的 UI 才会让 runner 去检查变更目标或读取文件快照

#### Headless 实现

文件: [headless.go](../../internal/ui/headless/headless.go)

##### `type UI struct`

字段:
- `out`
- `mu`
- `wrote`

职责:
- 把 assistant delta 写到 stdout
- 把工具事件写成单独行
- 在一轮结束时补换行

##### `New(out io.Writer) *UI`

职责:
- 创建 headless UI

##### `OnAssistantDelta(text) error`

职责:
- 流式输出 assistant 文本

##### `OnToolCall(event) error`

职责:
- 输出工具调用事件

##### `OnToolResult(event) error`

职责:
- 输出工具结果摘要

##### `OnDone() error`

职责:
- 一轮结束时补一个换行

#### Bubble Tea 实现

目录: [bubble](../../internal/ui/bubble/)

职责:
- 完整 TUI：消息历史、输入框、context meter、worktree 状态行、slash 命令、补全弹窗、subagent 面板、Select dock、图片芯片、thinking 折叠
- 使用 ESC 聚合 reader 防止鼠标/键盘序列被读边界切断（`escCoalescingReader`）
- 输出带光标锚点修正的终端流

##### Tokens 监控与上下文容量条

Activity 默认被动常驻，`renderActivityPane` 上下各分配约一半高度：上半由 `activity_summary.go` 比较 Todo/Tasks 语义快照和更新时间，下半 `renderTokenHistory` 常驻60秒曲线。重复快照不推进最新时间，会话恢复按持久化时间播种；清空后回退另一类。侧栏不接收键盘焦点，65列起分栏，更窄隐藏但保留用户开关，恢复宽度后重启轮询。容量条复用 `renderTokenFrontierWith` 的缓存/已用/空余分段与工作波纹。

`token_monitor.go` 接收可选 `OnModelRequestStart` / `OnModelStreamActivity` / `OnModelRequestEnd` / `OnModelUsage` 回调。Chat、Responses、Anthropic 在解码真实正文、可见推理和工具参数增量时记录到达时间，经 loop 在展示缓冲前送给 `token_monitor_live.go`；完成快照或非流式回填不产生流活动。采样器只累计数值，不保留片段；使用固定 60 个秒桶，recent TPS 为最近 3 个桶之和除以 3。ASCII 每字符估算 0.25 token，其他 Unicode 字符估算 1 token，曲线和 recent 标 `≈`，不作为 provider 账单统计；无真实片段的请求显示 `—`。工具时间按 UI 收到的开始/结果事件计量，最多保留 4096 个去重 ID，溢出显示部分统计。

主读数 `Turn avg` 以一次前台任务为范围，跨请求累计 output，并用任务开始至结束的墙钟时间（含工具与等待）作分母。请求切换时将上次 output 折入固定大小的累计值；usage 按 request ID 校验，快照替换当前请求用量而非累加。优先使用已知服务端 output，早期 usage 后的新片段暂估，最终 usage 校正；缺失用量的失败请求仍计入已接收估算，并标 `≈`。单次模型 `doneMsg` 只结束流，chat/task/goal/plan 的实际结束事件冻结任务读数；新任务重置，切会话清空。该读数不含子任务，区别于下方 `Σ session + tasks` 账本总量。

`LedgerReader.Monitor` 复用 Token Tracer 增量扫描、请求去重和输入 atom 归因。查询限定规范化工作区路径及当前 session 的有证据后代，不包含父会话或无关 session。返回全量汇总、前 12 个工具与最近 12 条请求；每个 attempt 在明细裁剪前保存固定大小的工具汇总及覆盖状态，不保留正文或无限工具名映射。汇总包含被列表截去的工具，原子保留上限仅影响详细归因。该上限提示限定到所选 attempt；其他无法判定会话归属的读取问题仍显示工作区范围提示。字段缺失、扫描预算未完成和读取错误均保留状态。

账本查询在 `tea.Cmd` 中异步运行，`Update` / `View` 不执行磁盘扫描。首个 WindowSize 在 Update 中启动查询；侧栏可见时单次查询后约 1 秒再刷新，最多一个查询在途；切会话丢弃旧结果，关闭侧栏停止后续轮询。空闲连续输入时复用3秒输入法静默保护，token tick同时节流检查任务变化。主上下文 `ContextStats.UsedTokens` 不再叠加 task 总量，累计任务消耗仅归入 session 总量。只有已知 total 或同时已知 input/output 才显示容量百分比；条形几何限定在窗口宽度内，数字保留原始超限值。账本中的 unknown 与显式零分开处理。

##### 实验：LaTeX 公式与 tikzcd

TUI 默认将 assistant 正文中的 `$…$`、`\(...\)`、`$$…$$` 和 `\[…\]` 转成 Unicode 公式。行内公式保持单行；块公式支持分式、根式、上下标、求和积分、矩阵、对齐及分段函数。超宽时使用带分组的线性表示，不截断二维结构。设置 `PAW_TUI_TEX=0` 可关闭本实验并使用原渲染。

行内上横线 `\overline{x}` 显示为 `x̅`；带下标或复合表达式使用明确分组，例如 `\overline{a_{ji}}` 显示为 `overline(aⱼᵢ)`，避免丢失横线的作用范围。块公式仍优先显示二维横线。

支持限制映射 `A|_{V}`。带标签箭头 `\xrightarrow{…}`、`\xleftarrow{…}` 在块公式中保留二维标签；行内或超宽降级使用 `→[标签]` / `←[标签]`。同时指定上下标签时显式标注 `above` / `below`。

普通代码、行内代码、链接地址和常见金额文本保留原样。转换仅发生在显示层；会话存储、导出与模型上下文仍保留 LaTeX，鼠标选区复制屏幕上看到的文本。流式输出未闭合时显示源码，闭合后转换，取消后保留残式。

数学图支持独立、块公式内或 `tikzcd` 代码块里的 `\begin{tikzcd}…\end{tikzcd}`；专用代码块也可直接写网格内容：

```tikzcd
\mathbb{R}^n \arrow[r,"A"] \arrow[d,"f"'] & \mathbb{R}^m \arrow[d,"g"] \\
V \arrow[r,"B"] & W
```

`&` 分列、`\\` 分行，方向由 `r/l/u/d` 组合指定，带引号的标签支持尾随 `'` 或 `swap` 换侧。节点和标签支持公式及中文。直线与折线连线发生碰撞、重复边或超宽时，整图转换为节点清单和“源 —标签→ 目标”，保留孤立节点、重复边及同名节点身份。

这是受限的字符渲染实验，不执行 TeX。通用 `tikzpicture`、曲线、图形布局选项、自定义宏和其他未支持语法保留源码；图形额外显示简短原因。当前限制为每个公式/图形 8 KiB、公式括号深度 32、环境深度 16、512 个命令、128 行及预估画布预算；图形最多 8×8 个格位、128 条边。与终端字宽模型不兼容的字素保留源码。

实现分为 [Markdown 识别](../../internal/ui/bubble/markdown_tex.go)、[公式适配](../../internal/ui/bubble/tex_math.go) 和 [tikzcd 排版](../../internal/ui/bubble/tikzcd.go)。公式依赖固定版本 `github.com/doug/termtex v0.0.0-20260918165034-c407d5cf5251`；颜色由 Paw 主题提供。Web 与 headless 输出不受此实验影响。
