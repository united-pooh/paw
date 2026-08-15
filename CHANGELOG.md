# Changelog

## 2026-08-15

- 结构化提问工具 `Select` 更名为 `question`，并支持批量提问：一次调用通过
  `questions` 数组携带多个问题，UI 逐个展示（标题带 `QUESTION k/N` 进度），
  结果按输入顺序返回数组；任一题取消则整批原子取消。旧 `Select` 名称与
  单题输入形状不再兼容。

## 2026-08-08

- 修复空闲态 header 时钟冻结：动画帧链停止后由 15s 低频空闲时钟链接手，header/状态栏时间持续走动；保留 3s 键盘静默窗口，打字与 Ghostty IME 合成期间不扰动预编辑光标。

## 2026-08-03

- 加固 Responses 工具协议：为工具结果引入 provider-data 信封，隔离并规范化各 provider 的返回数据。

## 2026-08-02

- 新增原生 todo 列表：定义 todo 快照协议与校验，注册 `update_todo` 工具、接入主 agent 与 Bubble Tea 渲染。
- 会话记录中渲染可折叠的 todo 卡片，回答结束后自动折叠已完成项；新增 Ctrl-P todo 页面，可从会话历史恢复 todo 快照。
- 结构化提问默认优先使用 Select 工具；清理临时任务报告文件。

## 2026-08-01

- 文件工具链加固：新增严格 Read 基线验证，Edit 对齐严格读取、权限与安全路径契约；runner 捕获文件变更快照并隔离模型结果，在工具行展示真实修改 diff。
- Bubble UI 优化：Markdown 代码块改为圆角边框，完善工具摘要、目标与中文状态展示，保留工具行悬停状态。
- 对齐 Reasonix 的工具结果压缩：新增上下文维护配置，按上下文压力分级清理，裁剪并归档过期工具结果，保留工具分组，在会话恢复与轮次间维持上下文，并补充生命周期测试。

## 2026-07-31

- 实现阻塞式 Select 工具：新增交互 dock 工作流并注入 Bubble UI，支持多选提交、自定义内联答案与单一自定义选项替换。
- 新增 Select 活动摘要与可读的 transcript 详情渲染；加固选择渲染与结果协议，兼容序列化后的可选默认参数。

## 2026-07-30

- 新增内置 TUI 主题注册表、从用户主目录加载设置与运行时主题切换。
- 修复 Ghostty IME 光标：使用真实终端光标并加入主题化生命周期测试，稳定输入光标对齐。
- 新增按需驱动的 UI 动画调度器，保证模型工作期间动画持续运行且波纹退场完整。
- 新增模型上下文压缩；新增原生 Edit 工具（精确字符串替换 + 原子写入）。
- 为 Read/Write/Edit 增加读状态过期写保护；工具行展示 +N -M 变更摘要，内容相同时抑制 +0 -0。

## 2026-07-29

- 项目由 GoCode 更名为 Paw 并更新运行时。
- 渲染带边框对齐的 Markdown 表格、隐藏标题语法标记；支持剪贴板图片输入与多模态请求。
- 记录已完成工具耗时并匹配无 ID 工具；支持终端链接交互，优化窄空间波纹渲染。
- 统一从 Paw 全局目录加载 skills；清理仓库垃圾文件（构建产物、agent 会话、IDE 配置等）。

## 2026-07-28

- 支持多模型配置与运行时切换。
- 状态栏重设计（均衡器 fill+wave 双态）、header 两端对齐并展示 session 累计 token。
- 修复触控板滑动与短读 Alt+[ 解析；增强模型容错与终端交互体验。

## 2026-07-27

- 重构 Bubble TUI 流式渲染与固定交互布局。
- 修正工具详情背景与 Markdown diff 识别；统一终端字素宽度并恢复会话记录。
- 恢复会话输入历史并保留技能令牌渲染；过滤碎片化鼠标输入并刷新流式 Markdown。

## 2026-06-28

- 迁移 StreamMA routing artifacts 与 subagent picker。

## 2026-06-27

- 固化 Token Tracer、技能运行时并完成 StreamMA 主线集成。
- 稳定流式输出期间 TUI 输入框锚定与长块渲染；分离 transcript 滚动与输入框上下键焦点。

## 2026-06-19

- StreamMA 子代理降载与输入框多行优化。

## 2026-06-18

- 实现 StreamMA 协作运行模式：注入后台 subagent 结果到父上下文，新增 StreamMA 运行时命令与 UI 接线。
- TUI 布局重构为 70/30 分栏，增强工具调用样式与上下文展示；启用 Anthropic prompt caching 修复 cache 命中率恒为 0 的问题。

## 2026-06-17

- 添加 subagent 管理系统（persona/registry/worker）并完善相关功能。
- 工具条目展示重构：从 citation 模型迁移到独立 entry 模型；修复 diff 展示、右侧面板抖动及相关渲染问题。

## 2026-06-16

- 修改默认启动行为：`go run ./cmd/agent` 现在每次都启动一个全新的空会话；若需要恢复指定会话，请使用 `-s <session-id>` 参数。
- 新增 `/sessions` 命令：可浏览所有历史会话，列表展示 ID 前缀、创建日期、文件大小与第一条消息摘要，选中后可直接恢复该会话继续对话。
- 新增输入补全弹窗：在输入框中输入 `/` 触发斜杠命令补全，输入 `@` 触发文件路径补全；使用 ↑↓ 键导航候选项，Tab 或 Enter 确认选择，Esc 关闭弹窗。

## 2026-06-12

- Added Bubble slash commands for `/export`, `/setting`, `/subagent`, and `/tasks`, and updated `/help` to show parameter hints for commands that accept arguments.
- Added persisted runtime state under `.paw/`, including `.paw/settings.json` defaults, transcript exports in `.paw/exports/`, and subagent transcripts under `.paw/sessions/<sessionID>/`.
- Changed `/model` so it supports `status`, `custom`, and `deepseek` shortcuts in addition to the interactive provider wizard.
- Changed the interactive UI so the input area shows a context-usage meter by default instead of the old `Input`/`Waiting`/`Terminal` title labels.
- Fixed turn-scoped cache-hit display so turns without usage data fall back to `0.0%`, prevented background system notifications from splitting an active assistant response, and tightened exported transcript permissions to `0600`.
