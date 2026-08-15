# Progress

## Token Tracer high-density workspace

- [x] Inspect the existing Token Tracer implementation and data contracts.
- [x] Compare Calls Table, Token Heatmap, and Folded Flame concepts.
- [x] Confirm IDE-style docking, persistence, linked highlighting, single-instance panels, and visual direction.
- [x] Write and self-review the design specification.
- [x] Obtain final user review of the written specification.
- [x] Write the implementation plan.
- [x] Implement and verify the feature.
  - [x] Frontend: React 19 + TypeScript + Vite + Dockview 7 dashboard under `internal/tokentracer/dashboard` (33 unit/component tests green).
  - [x] Go embed: `dashboard_embed.go` serves built `dist/`; legacy `dashboard.go` and `legacyDashboardHTML` removed.
  - [x] E2E: Go fixture server + 6 Playwright browser tests green.
  - [x] Visual evidence: `.agent/visual/token-tracer-{desktop,narrow}.png` + evidence note.

## llm-metadata context windows

- [x] Generate embedded data (330 models) + generator script.
- [x] MetadataContextLimit lookup (case-insensitive, provider/ prefix stripped).
- [x] EffectiveContextLimitTokens: metadata layer; default 256k -> 128k.
- [x] Tests: llm_metadata_test.go, config_test.go, bubble_test.go assertion.
- [x] Full `go build ./...` and `go test ./...` verification green.
- [x] 刷新状态文件（压缩后恢复依据） <!-- todo:1 -->
- [x] 调研 deepseek-reasonix 与 deepseek-harness（本地 clone / GitHub） <!-- todo:2 -->
- [x] 对比 Paw 架构/功能差距，严苛审查分级 <!-- todo:3 -->
- [x] 输出审查结论（含 top actions） <!-- todo:4 -->
- [x] 新增 B 模式 snip 档测试 + 全量回归验证 <!-- todo:5 -->
- [x] 调查现有 transcript 流式渲染、动画帧、配置与 config 面板结构，以及 docs/ 设计文档和近期提交 <!-- todo:ctx -->
- [x] 逐项澄清输出节奏与两种渲染效果的行为、默认值和边界 <!-- todo:clarify -->
- [x] 提出 2-3 种实现方案及取舍 <!-- todo:approaches -->
- [x] 分段呈现设计并等待用户批准 <!-- todo:design -->
- [x] 编写并自检设计文档，提交后请用户审核 <!-- todo:spec -->
- [x] 读取 writing-plans 规范并检查已提交 spec 与当前工作树状态 <!-- todo:plan_ctx -->
- [x] 编写详细 implementation plan，按功能拆分并标注 subagent 并行边界 <!-- todo:plan_write -->
- [x] 自检 plan 的依赖、冲突、验证命令与 subagent 分工 <!-- todo:plan_review -->
- [x] 将 plan 提交并交付用户审核 <!-- todo:plan_commit -->
- [x] 实现 assistant 行级动画状态与流生命周期（Subagent B） <!-- todo:impl_stream -->
- [x] 实现 post-render 乱码/浮现变换（Subagent C） <!-- todo:impl_transform -->
- [x] 完成基础功能批次：设置/UI、流生命周期、post-render 变换 <!-- todo:impl_foundation -->
- [x] 实现设置与 General UI 功能 <!-- todo:impl_settings -->
- [x] 集成渲染、缓存失效与帧调度 <!-- todo:impl_integration -->
- [x] 跨功能验收、修复并运行 go build/go test <!-- todo:impl_verify -->
- [x] 定位引用文件选择框及隐藏文件过滤逻辑 <!-- todo:inspect -->
- [x] 实现隐藏目录可搜索修复并补充/调整测试 <!-- todo:fix -->
- [x] 运行 go build ./... 与 go test ./... 验证 <!-- todo:verify -->
- [x] 复现问题并补充最小回归测试 <!-- todo:repro -->
- [x] 检查 noise + line 动画实现及现有测试 <!-- todo:inspect-noise-line -->
- [x] 补充回归测试并在必要时修复 <!-- todo:fix-noise-line -->
- [x] 运行 go build ./...、go test ./... 与 git diff --check <!-- todo:verify-noise-line -->
- [x] 检查 noise 动画实现、帧调度与 transcript 缓存失效链路 <!-- todo:inspect-noise-freeze -->
- [x] 复现定格并补充中间帧变化/完成恢复回归测试 <!-- todo:repro-noise-freeze -->
- [x] 修复动画定格问题并保留无关工作区改动 <!-- todo:fix-noise-freeze -->
- [x] 运行 go build ./...、go test ./... 与 git diff --check <!-- todo:verify-noise-freeze -->
- [x] 检查 reveal 动画实现、帧调度与现有测试 <!-- todo:inspect-reveal-freeze -->
- [x] 必要时补充回归测试并修复 reveal 定格问题 <!-- todo:fix-reveal-freeze -->
- [x] 运行 go build ./...、go test ./... 与 git diff --check <!-- todo:verify-reveal-freeze -->
- [x] 检查 transcript 动画、设置项、配置持久化和测试涉及范围 <!-- todo:inspect-transcript-effects -->
- [x] 移除渲染效果及其帧刷新集成，清理相关测试与配置 <!-- todo:remove-transcript-effects -->
- [x] 运行 gofmt、go build ./...、go test ./... 与 git diff --check <!-- todo:verify-removal -->
- [x] 检查逐字输出配置的定义、读取和 transcript 流程 <!-- todo:inspect-char-mode -->
- [x] 定位并修复逐字输出未生效的原因 <!-- todo:fix-char-mode -->
- [x] 运行 gofmt、go build ./...、go test ./... 与 git diff --check <!-- todo:verify-char-mode -->
- [x] 检查逐字模式下 Markdown 增量追加和引用渲染链路 <!-- todo:inspect-markdown-char -->
- [x] 修复引用或其他 Markdown 结构被逐字渲染打乱的问题 <!-- todo:fix-markdown-char -->
- [x] 运行 gofmt、go build ./...、go test ./... 与 git diff --check <!-- todo:verify-markdown-char -->
- [x] 确认逐字模式的实际提交粒度与刷新链路 <!-- todo:investigate-char-render -->
- [x] 根据事实修复整行显示问题并补充回归测试 <!-- todo:fix-char-render -->
- [x] 运行 gofmt、go build ./...、go test ./... 与 git diff --check <!-- todo:verify-char-render -->
- [x] 调查 config UI 面板、命令注册、配置模型及相关设计文档 <!-- todo:task-1 -->
- [x] 实现推理强度 low/medium/high/xhigh/max 的配置与 UI 指令 <!-- todo:task-2 -->
- [x] 补充或更新测试并执行 go build ./... 与 go test ./... <!-- todo:task-3 -->
- [x] 沉淀阶段结果到项目记忆并汇报 <!-- todo:task-4 -->
- [x] 修复 config_center_test.go 中删除确认测试缺失的函数声明 <!-- todo:fix-test-syntax -->
- [x] 检查配置中心推理开关、推理强度和空格行为实现及测试 <!-- todo:review-config-center -->
- [x] 运行 gofmt、go build ./... 和 go test ./... <!-- todo:verify-go -->
- [x] 核对“模型”和“当前模型”的现有职责及刚完成的推理设置改动 <!-- todo:inspect-overlap -->
- [x] 将推理开关与推理强度移入“通用”第一页，并恢复“当前模型”原有列表行为 <!-- todo:move-reasoning-general -->
- [x] 更新回归测试并执行 go build ./...、go test ./...、git diff --check <!-- todo:verify-general-reasoning -->
- [x] 检查配置中心模型管理、当前模型切换、顶部 Tab 与相关设计文档/测试 <!-- todo:inspect-model-merge -->
- [x] 设计并实现“当前模型”能力并入“模型”页，移除“当前模型”一级 Tab <!-- todo:implement-model-merge -->
- [x] 补充合并后的导航、渲染、激活与管理回归测试 <!-- todo:test-model-merge -->
- [x] 清理孤立 Home/Compression 死路径并加强当前项置顶、推理强度循环回归 <!-- todo:review-fixes -->
- [x] 执行 gofmt、go build ./...、go test ./...、git diff --check 并完成视觉复核 <!-- todo:verify-model-merge -->
- [x] 定位输入框边框、Chat 标签、Token Usage 和 Context Process Bar 的渲染逻辑与现有测试 <!-- todo:locate -->
- [x] 新增或调整布局测试，覆盖新的上下边框位置 <!-- todo:tests -->
- [x] 实现输入框边框布局调整 <!-- todo:implement -->
- [x] 调查底部边框三段布局与队列摘要覆盖规则 <!-- todo:investigate -->
- [x] 新增/调整项目分支与 Token Usage 对调的回归测试
- [x] 将底部边框调整为模式左、Token Usage 中、项目/分支右
- [x] 完成边界/CJK/队列路径、全量 Go 测试及视觉验证

## Subagent preview transcript blank (2026-08-15)

- [x] 定位根因：session transcript 写入已迁移为统一信封 `es.Envelope`（`{"seq","type","occurred_at","schema_version","payload"}`），主会话恢复走 `store.LoadResolvedRecords`（双格式兼容），但 ctrl+g subagent preview 的 `loadSubagentTranscriptEntries` 仍直接 `json.Unmarshal` 到 `session.Record`——envelope 行不报错但字段全空，产出 0 条 entry → 预览一片空白。
- [x] 修复：session 包导出 `ParseTranscriptLine`（envelope + legacy 双格式，语义与 store 内部 readOwnRecords 一致）；UI 加载优先走 store `LoadResolvedRecords`（与 /resume 一致，含 fork 解析），失败时文件 fallback 改用 `ParseTranscriptLine`。
- [x] 测试：`ParseTranscriptLine` 单测（envelope/legacy/损坏行）+ bubble 层 3 个（store 路径、envelope 文件 fallback、Content fallback）。
- [x] 验证：`go build ./...`、`go test ./...` 全绿；真实 783KB envelope subagent transcript 修复前 0 条 → 修复后 38 条非空 entries。

## Ctrl+G subagent panel global toggle (2026-08-15)

- [x] 现状梳理：ctrl+g 打开 Activity（Subagents 选择器）面板，面板内再按 ctrl+g/esc 关闭；但 subagent preview 中按 ctrl+g 会再次弹出面板（preview 状态残留），toggle 语义不完整。
- [x] 用户选择方案：ctrl+g 升级为全局 toggle。
- [x] 实现：app.go ctrl+g 分支在 subagentPreview 非 nil 时直接退出 preview 返回主 transcript（restoreMainTranscriptFromSubagentPreview），其余状态打开面板；面板内 ctrl+g 关闭行为保持不变。
- [x] 测试：新增 TestCtrlGTogglesSubagentPreviewClosed（preview 中 ctrl+g → 恢复主 transcript、preview/picker 均 nil、输入保留）；现有 ctrl+g 打开/关闭测试全绿。
- [x] 验证：go build ./...、go test ./... 全绿；README 快捷键说明同步为「展开/收起 toggle」。

## Ctrl+G opens Activity as right sidebar (2026-08-15)

- [x] 澄清需求：用户说的「subagent 侧边面板」= subagent 运行时屏幕右侧的悬浮任务卡（task_card.go，placeRightCenteredOverlay 贴右垂直居中）；用户选择：ctrl+g 在右侧展开完整 subagent 面板，替代居中 modal。
- [x] 实现：placeOpaqueOverlay 增加 overlayAlignRight；renderActivityBox 改为右侧面板尺寸（activityPanelWidth = min(60, contentWidth/2)、高度 = transcriptHeight-2）；renderTranscriptRegion 在 subagentPicker 打开时右侧合成 Activity 面板、隐藏悬浮任务卡；renderActiveModalBox 移除 Activity 居中分支。
- [x] 测试：新增 activity_side_panel_test.go 3 个（右侧渲染 + 任务卡隐藏 + 面板宽度约束）；既有 Activity/ctrl+g/preview 测试全绿。
- [x] 视觉验证：真实 View() 输出 → HTML fixture → Chrome 截图（.agent/visual/activity-side-panel-{open,card}.png），独立 subagent 像素分析确认面板贴右、右缘对齐、垂直居中、内容正确；已知截图层伪影（card 图游离竖线）已记录。
- [x] 验证：go build ./...、go test ./... 全绿；README 快捷键说明更新。

## Select → question 改名 + 批量提问 (2026-08-15)

- [x] /grilling 澄清设计：纯 `questions` 数组（breaking）、结果按顺序数组返回、整批原子取消、旧 `Select` 不兼容、行为模式写入全局 `~/.paw/agent.md`。
- [x] selecttool 包：`decodeInput` 解码 questions 数组并逐题校验（错误带 `questions[i]:` 前缀）；`Tool.Run` 逐题 `Broker.Ask` 串行展示，任一题取消即丢弃已作答结果整批置 cancelled；返回 `BatchResult`（`{"results":[...]}`）；`Request` 增加 `BatchIndex/BatchSize` 元数据（UI 进度用）。
- [x] 工具改名 `question`：UI 渲染（utils.go/transcript.go/subagent_picker.go）、plan/filter、loop/prompt_builder（新增批量策略文案）、plan/prompt、docs/plan-mode-runtime-design.md 全部切换；transcript 摘要改「answered N questions」，展开详情按 Q1/Q2 分块。
- [x] dock 标题 `QUESTION k/N` 批量进度指示（selection_dock_render.go）。
- [x] 测试：select 包（批量顺序/原子取消/元数据）、UI（批量详情/摘要/进度指示）、register/prompt_builder/filter/tool_track 更新；新增 docs/design-question-tool.md。
- [x] 验证：`go build ./...`、`go test ./...` 全绿。
- [x] docs 设计记录 + CHANGELOG 条目 <!-- todo:6 -->
- [x] 全局 ~/.paw/agent.md 写入 question 工具行为模式 <!-- todo:7 -->
- [x] 调查 question 批量协议、Broker 与当前串行流程 <!-- todo:investigate-protocol -->
- [x] 调查 selection dock 状态机、键盘事件与渲染布局 <!-- todo:investigate-ui -->
- [x] 调查选中态样式定义与现有测试约束 <!-- todo:investigate-style-tests -->
- [x] 阅读设计文档并运行相关测试建立基线 <!-- todo:investigate-baseline -->
- [x] 通过 /grill-me 收敛确认页、取消、导航和样式细节 <!-- todo:grill-design -->
- [x] 输出调查结论与待确认实现方案，等待执行批准 <!-- todo:propose-plan -->
- [x] 重构 Broker/Tool 批量请求为单次批量事件与完整结果返回 <!-- todo:implement-protocol -->
- [x] 实现 question dock 的问题页、确认页、左右导航与选择状态保存 <!-- todo:implement-dock -->
- [x] 统一选中态主题令牌并补充协议/UI 回归测试 <!-- todo:implement-style-tests -->
- [x] 更新设计文档与运行 go build ./...、go test ./... <!-- todo:verify-document -->
- [x] 阶段一：引入 conc，保护 in-process subagent 与池管理 goroutine <!-- todo:phase1 -->
- [x] 检查当前工作区、设计文档和已有 subagent 改动，恢复准确实现边界 <!-- todo:stage0-state -->
- [x] 阶段一：引入 conc，保护 in-process subagent 与池管理 goroutine <!-- todo:stage1-conc -->
- [x] 阶段一：引入 conc，保护 in-process subagent 与池管理 goroutine <!-- todo:phase-1-conc -->
- [x] 阶段一：引入 conc，保护 in-process subagent 与池管理 goroutine <!-- todo:phase1-conc -->
- [x] model 包：ProxyConfig 类型 + Config.Proxy 字段 + client transport 构建（auto/direct/custom） <!-- todo:t1 -->
- [x] config 包：Document.Proxy / Provider.Proxy / SetProxy Operation / clone / mergePreset / schema <!-- todo:t2 -->
- [x] config 包：runtimeConfig 组装 effective proxy + discovery 应用 proxy <!-- todo:t3 -->
- [x] UI：新增「连接」tab（全局代理）+ Provider 动作页代理选项 + 文本编辑 <!-- todo:t4 -->
- [x] 测试：新增 proxy 相关单测 + go build ./... && go test ./... 全绿 <!-- todo:t5 -->
- [x] Write 对已存在文件强制先 Read（VerifyRequired，新建文件不受影响） <!-- todo:w1 -->
- [x] Edit 拦截报错强化：两步指引 + 禁止 Bash/Write 绕过 <!-- todo:w2 -->
- [x] Edit/Write/Read 工具 Description 强化先 Read 契约 <!-- todo:w3 -->
- [x] 更新 write/edit/read_state 相关测试对齐新行为 <!-- todo:w4 -->
- [x] go build ./... && go test ./internal/tool/... 全绿 <!-- todo:w5 -->
