# Progress

## Goal / Plan 重做（2026-10-09）

- [x] 调查历史设计与源码，真实handleSubmit六个问题复现。
- [x] 用户澄清：Goal是量化指标驱动的自主探索，Plan持续对话后明确执行。
- [x] Goal指标契约、探索循环、真实/native证据、持久化、恢复、预算和补充输入；通知交付窗口补测完成。
- [x] Plan持续对话、ready/execute分离及恢复；排队/失败恢复/通知窗口均先红后绿，包内race通过。
- [x] 输入路由、补充、状态和视觉验证；宽窄4图独立复核通过，运行中切会话/切回Chat仍受守卫。
- [x] 工具/证据源/压缩状态接线：真实Actor→Engine→Bash→JSONL低40/高90量化验收、Plan澄清/保存/显式执行集成通过；空session不混入其他目标。
- [x] 独立审核、8相关包race与末次5包复验、全仓41包（排除已知config挂起测试）、vet、隔离构建/help、gofmt/diff和中文文档通过。
- 产物 .agent/bin/goal-plan-rework/paw，SHA256 962bf055cdeb9a264b5876f402a4ea1b250b5a8d5558553a168702204ab0cd70；未替换安装、提交或推送。用法 docs/reference/goal-plan.md；证据 .agent/verification/goal-plan-rework/验证记录.md，四图 .agent/visual/goal-plan-workflow/验证记录.md。

## TODO 前置依赖（2026-10-09，已完成）

- [x] 调查现有 TODO、普通/Goal 续行、持久化、TUI 与官方依赖语义。
- [x] 数据/工具与依赖图校验先红后绿，克隆隔离与331776组合独立审核。
- [x] 普通和 Goal 续行/依赖指纹；真实 Engine+工具的6请求2续行合成流程。
- [x] 状态压缩、JSONL 重开恢复及旧快照兼容。
- [x] TUI 阻塞显示、短高度溢出修复、宽窄/解除后三图及独立审核。
- [x] 6相关包全包/race、vet、隔离 build/help、文档、gofmt/diff通过。全仓已知config测试60秒超时；其余39包通过，最终Bubble race与config排除该项通过。初次同步编入的overflow红测保留记录。
- 交付 .agent/bin/todo-dependencies/paw，SHA256 b28519d6459d6cdeb7da2f91353248498bbe084e61b020f315c9627fbd3da55a；未覆盖安装、提交、推送。用法 docs/reference/todo-dependencies.md；证据 .agent/verification/todo-dependencies/验证记录.md。

## 单次任务平均速度与工具协议自动恢复（2026-10-05）

- [x] 识别并复现 request avg 重置、工具协议直接终止；按单次前台任务实现累计/冻结/重置。
- [x] 早期 usage 后流量与独立 /task 完成计时的失败测试及修复；24行布局保留工具汇总。
- [x] typed native arguments error、正常/恢复入口后台纠正、10次有界预算、日志/trace、取消及整批拒绝；普通工具失败后模型修正继续的回归。
- [x] Bubble/Loop/Model/Token Tracer相关完整race、全仓vet、隔离构建；统计与恢复分别独立审核通过。
- [x] 原生PTY19项通过：三请求120输出、Read实际2.005秒，总7.6秒/15.7tok/s，结束冻结；失败→重试→恢复均记录，Read只执行一次。四张非空图及证据 `.agent/visual/turn-rate-tool-recovery.md`、`.agent/verification/turn-rate-tool-recovery/验证记录.md`。binary SHA76702c68…c4e57；未安装、提交、推送。

## 工具协议泄漏与流采样（2026-10-04）

- [x] 对齐实际session、运行binary和provider ledger；确定非旧版本问题。
- [x] 工具协议typed error及超长行Grep修复；半合法批次不执行，损坏正文/恢复不泄漏。
- [x] stream到达时间/三类增量的独立采样、完整请求平均速率；completed回填不造峰。
- [x] CTX真实超限与ledger有界工具汇总、文案/图和独立验收。
- [x] Model/Loop/File/Bubble/Token Tracer race、vet/build/help；41测试包串行通过（只跳过既有config阻塞单项）。原生PTY 15项全通过，2次localhost合成请求、exit0。

最终binary `.agent/bin/token-monitor/paw`，SHA256 `0b35fa5650029d7a27ec1fc6df9301cd1b9fbadd66d8b6d283a78bc5a3b51e04`。证据 `.agent/verification/token-monitor/stream-repair/调查与验证.md`、`.agent/visual/token-monitor-received-stream.md` 和原生PTY结果。未向外部模型发送用户历史、未覆盖日常安装/提交/推送。流速是实际收到片段的字符估算；服务端未传回的隐藏推理不可观测。


## 常驻 Token 侧栏（2026-10-04）

- [x] 调查输入焦点、轮询、任务/Todo更新路径；确认复用数据和采样。
- [x] 默认常驻与被动输入、轮询/会话/尺寸生命周期。
- [x] 上半真实更新自动切换，下半大面积实时图。
- [x] 回归、构建、实际渲染与PTY、独立审查；更新使用说明。


最终17项原生PTY通过，bubble全race/vet/build/help与41包串行测试通过（仅跳过已证实旧config阻塞测试）。binary 1693b387…db9d77，证据 .agent/visual/passive-token-sidebar.md 与 .agent/visual/token-monitor/pty-evidence.md。未安装、提交或推送。

## Token Monitor 与上下文条（2026-10-04，已实施）

- [x] 确认旧条/波纹仍在，用户批准侧栏摘要＋全屏 Tokens 设计并要求复用现有代码。
- [x] 上下文条恢复与主请求/子任务累计分离；完整总量未知时显示CTX —，保留显式零与历史估算。
- [x] 当前会话及子任务账本投影、可见输出采样和异步TUI接线；工具归因和provider账单分开，采样/列表有界。
- [x] Tokens 导航、全屏、终端渲染与未知/部分数据处理；修复跨tab全屏泄漏、mode/queued工具计数和窄屏内容裁切。
- [x] 6张真实View fixture新截图、Chat/Responses/工具回传本地SSE端到端、独立视觉/语义审核；相关race、vet/build及排除已确认旧config阻塞单项后的41包Go门禁通过。
- [x] 原生PTY发现的单独Esc等待下一键缺陷已修复；同Read有界续接，鼠标/UTF8/EOF/取消/fd flags回归、独立审核和Linux交叉编译通过。最终隔离PTY14项全通过、退出0，仅一次localhost合成请求。
- 交付 .agent/bin/token-monitor/paw，SHA256 e9d1381457d44f61583a017131724000d6adf6d19379a8380b50cd1a13e2a070；视觉和运行证据 .agent/visual/token-monitor.md。
- 计划 docs/codex/plans/2026-10-04-token-monitor.md；保留既有dirty工作，不提交/推送/覆盖日常安装。

## 本地误断连与安全诊断（2026-10-03）

- [x] 旧版真实92秒输出暂停触发误断连，修复版保持单请求完成；Responses/Chat/Anthropic的Read预算、零字节、旧timer、取消均经回归和独立审核。
- [x] Responses逐attempt诊断先红后绿，避免Close污染、gzip歧义及Done后退出丢记录；无正文，JSONL/查询副本隔离。最终四种真实CLI场景及逐次诊断一致，90.002秒真正空闲仍保护并恢复。
- [x] 最终model+tokentracer完整race、全仓vet、build/help、独立审查通过；全仓仅排除既有AsyncModelDiscovery阻塞单项后41包通过。
- [x] 备份ec071f4c…14c11至.agent/bin/connection-interruption/paw.before，原子安装8ab2eb4b166a1723cedb5ec2da3443cab8859e24fa706253a0cbc6474ddbb91e；安装help/hash通过。未重启用户PID90606，需重启Paw使用新版；未提交/推送。
- [x] 第2目标轮补两条602602字节公网合成输入，服务报告141449 tokens，代理/直连均完整成功；缓存差异不作速度归因。12:14续查原会话仍无安装后请求，PID未重启；源码和安装hash未变。阶段审计见 .agent/verification/connection-interruption-20261003/followup/阶段审计.md。
- [ ] 整体目标未完成：16:52恢复后补齐原journal近似复现助手，64条真实消息/44工具对/414618字节，5项安全测试与独立代码审核通过。自动审批拒绝执行，要求用户明确许可私有内容发往原网关；进程未启动、请求未发送。17:35第三轮仍未收到许可或新故障日志，62个实例/事件文件无新增写入，claim/result不存在，原journal未变，已标blocked。不得以自动续行或替代方式绕过；等明确许可或新的可分析证据。见 followup/real-context-probe/授权等待审计.md 及原安全证据。
- 证据 .agent/verification/connection-interruption-20261003/调查记录.md；最终真实四场景 .agent/verification/responses-disconnect-rootcause-20261003/final-runtime-mr2wb1kd/results.json。

## 已安装版真实复现（2026-10-03）

- [x] 同一合成 SSE：旧版复现 3751 字节 decode / 907 字节思考报错，已安装新版自动恢复；完整坏帧仍停止。
- [x] 实际 CLI 跑满连续十次上限、两段各十次成功清零、真实 90 秒看门狗；真实 PTY 显示 1/10 后完成并正常退出。工具副作用、结构化请求及 JSONL 一致。
- 证据 .agent/verification/responses-installed-reproduction-20261003/验证记录.md；仅新增验证脚本/证据，生产源码与真实配置未改。原始 provider 数据仍缺失，不把模拟复现当作当时原始取证。

## Responses 截断尾帧（2026-10-03）

- [x] 确认 10:20:36 的 decode 报错发生于上一轮新版实际进程；无原始 SSE，无法确认当次帧边界。
- [x] 分帧缺口先红后绿：EOF/read/idle 截断尾不再误报 decode；完整坏帧仍停止，未闭合 completed 工具不提交。
- [x] 独立审核、相关 race、全仓 vet、build/help 通过；串行全仓仅排除原有 AsyncModelDiscovery 阻塞单项后 41 包通过。
- [x] 已备份并原子更新 ~/go/bin/paw，SHA256 ec071f4c98eb8ff1cb2b9da0b072ae3b7a168874908877ad457ab0fda1d14c11；安装 help 通过，未强制重启、提交或推送。
- 旧程序备份 .agent/bin/responses-truncated-frame/paw.before；证据 .agent/verification/responses-truncated-frame-20261003/验证记录.md。用户需重启 Paw 后恢复会话。

## 断流自动续接（2026-10-03）

- [x] 输出后暂时性断流自动续接，单次连续失败最多 10 次；完整成功响应（包括空 completed）清零，后续故障从 1/10 开始。恢复期间空流仍使用剩余额度，取消/协议/provider 错误停止。
- [x] 保留已完成工具与等待期间新指令；临时恢复提示、partial、不完整工具不进入权威历史。思考流独立收尾，Bubble blockIndex 复用不拼接旧思考。
- [x] 先红后绿、独立边界审核、model/loop/bubble 定向 race、全仓 vet、构建/help、六帧实际 View 验证通过。
- [x] 最终串行全仓仅跳过 TestManagerAsyncDiscoveryDoesNotBlockOpen 后 41 包通过；原始 actor 超时及 exec 偶发空 PID 日志保留，独立复核未关联本次实现。
- [ ] 原有 AsyncModelDiscovery 测试阻塞仍未解决，不能声称无条件全仓全绿。本任务未修改 config manager 两文件。
- 交付 .agent/bin/stream-auto-recovery/paw，未安装、提交、推送；证据 .agent/verification/stream-auto-recovery-20261003/验证记录.md。

## 重复调查与 Responses 断流（2026-10-03）

- [x] 六种正文/思考断流先失败复现，修复后不重发，保留原始错误/cause/request ID；无输出重试与完成快照校验仍有效。
- [x] 沿用提纲与目标仓库范围提示；仅改写文案不算进展，接入真实成功工具结果避免误停。Goal/Plan 原判据保持。
- [x] 本地 HTTP → Engine → Journal 断流/明确继续集成回归、相关 race、全仓 vet、隔离构建/help、独立审核通过。
- [ ] 全仓无条件绿灯仍受原有未完成 AsyncModelDiscovery 测试阻塞（45 秒独立超时重现）；未触碰原两文件。仅排除此单项后 41 包全部通过。证据 .agent/verification/responses-recovery-20261003/验证记录.md。
- 独立二进制 .agent/bin/responses-recovery/paw；未安装、提交、推送。模型遵从提示和透明网络断点恢复均不在已验证保证内。

## 限制映射块公式回归（2026-09-25）

- [x] 两段原式失败复现：底层库拒绝孤立竖线后带下标；适配为可挂脚标的竖线字符。
- [x] 窄屏/行内带标签箭头另有缺失，先失败再补带方向和上下标签的单行表示；语义和 TestTeX 定向通过。
- [x] Bubble 全包、TeX race、全仓 vet、隔离 build/help、diff 通过；深浅新截图与真实 PTY 60/140/100 列通过。证据 `.agent/visual/tui-latex-restriction/`，实验程序重建，未安装/提交/推送。

## 行内上横线回归（2026-09-25）

- [x] 截图原式先失败复现：termtex 的 overline 在 Inline 下仍占两行，Paw 缺少线性转换导致整式回退源码。
- [x] 单字符组合横线与复合表达式显式分组；定向语义、Markdown 容器及流式/复制回归通过。
- [x] Bubble 全包、TeX race、全仓 vet、隔离构建/help、diff 通过；深浅截图及真实 PTY 60/140/100 列验证通过。证据 `.agent/visual/tui-latex-overline/`，未安装/提交/推送。

## Default 浅金加粗与置顶背景（2026-09-24）

- [x] 浅金 ANSI 3、置顶 ANSI 0 局部底色完成；全仓测试重跑通过，vet/build/help 通过，正式 View 截图及安装 SHA256 核验完成。首次全仓的进程池 stderr 测试失败、单测连续 10 次通过，保留日志。证据 `.agent/visual/default-emphasis/gold-sticky.md`；重启 Paw 生效。

## 加粗主题色与引用可读性（2026-09-24）

- [x] 用户确认实际渲染预览，核实当前新增的 Default 原生色和 LaTeX 实验。
- [x] 先失败回归，再落实主题主色加粗与引用可读性；另复现并修复 Catppuccin 引用前景被 RGB 编码取整破坏的问题。
- [x] 主题切换/深浅色/原生 ANSI/无色/换行和表格无底色验证；Bubble/theme 全包、定向回归、vet、diff 通过。
- [x] 3 张正式渲染截图与独立代码/视觉复核通过；make build 和 --help 通过，交付 .agent/bin/emphasis-color/paw。包含当前工作树其他实验，未覆盖日常安装、未提交推送；证据 .agent/visual/emphasis-color.md。

## TUI LaTeX / tikzcd 实验（2026-09-24）

- [x] 调查并取得用户完整方案批准，创建实验分支。
- [x] 失败回归、固定 termtex 公式适配与 Markdown/表格接入；代码/链接/金额/未知语法保护。
- [x] tikzcd 解析、直线/折线网格排版及碰撞/超宽关系降级，保留重复边与孤立节点。
- [x] 流式/取消/恢复/复制/缩放验证，Bubble 全包、定向 race、vet、隔离 build/help、diff 通过。
- [x] 6 张深浅主题 × 60/100/140 列截图、随机输入检查、三轮基准和真实隔离 PTY 验收；原始消息持久化未改变。
- [x] 交付 `.agent/bin/paw-tui-latex` 与 `.agent/visual/tui-latex/`；未安装/提交/推送。同工作区并行发生的 default 主题改动未由本任务修改。

## 会话切换提示与终端串屏（2026-09-20）

- [x] 定位 applySessionPickerRestore 成功提示与 runtime/loop/history.go 的标准日志直写 stderr；恢复测试复现同款日志。
- [x] 去掉普通切换提示，显式 relayout/refresh；旧正文/弹窗消失及空会话、Activity 宽度恢复验证通过。
- [x] 交互诊断日志文件隔离与退出恢复，后台并发写入和设置失败不污染终端通过。
- [x] Bubble/interactive/loop 完整包、定向 race、全仓 vet/build、三张新截图与独立审核通过；证据 .agent/visual/session-restore.md。
- [x] make build 安装至 ~/go/bin/paw，--help 通过；原程序保留 .agent/bin/paw-before-session-restore-fix。没有提交/推送本轮变更，保留已有研究资料。
- [x] 真实隔离 PTY 120×40：/resume 恢复故意缺失工具结果的会话；repair 日志落盘且不进入 stdout/stderr，切换提示不存在，退出 0、无模型 HTTP 请求。结构化验证与原始 ANSI 已保存 .agent/visual/session-restore/。

## TUI 表格背景纠正（2026-09-18）

- [x] 复现表头/行内代码/高亮背景遗漏；独立复核测试盲点和语义换行尾部问题。
- [x] 统一表内无背景样式与列宽测量，保留表外 Markdown 行内样式。
- [x] 五列表格视觉验收、完整 View 背景验证和回归检查；7 主题 × 3 宽度、NoColor、深浅色截图通过。
- [x] 已提交 `a6de3e6`（时间去重）、`1b17bfd`（表格）；推送到 origin/dev 并用 ls-remote 核对完整 SHA 一致。make build 更新 ~/go/bin/paw，--help 通过；原二进制备份在 .agent/bin/paw-before-table-background-fix。研究资料与原有 progress 差异未纳入提交。

## TUI 视觉层级（2026-09-15）

- [x] 用户新增要求：当前轮输入吸顶，原输入可见时取消，跨轮切换；整行底色更清楚，鼠标命中/选择不误映射。

- [x] 截图和现有代码调查；用户批准调整方案。
- [x] 模型通知折叠、点击/键盘可达性与宽度回归。
- [x] Markdown/表格/摘要/用户消息视觉规则统一。
- [x] 输入提示与带标签的底部指标。
- [x] 同一中文 fixture 三尺寸前后截图、Go 检查和交付。

## 任务复杂度与模型路由研究（2026-09-09）

- [x] 冻结三项RQ与worker权限边界，读取deep-research及全部参考规则；启用三路只读检索，OpenAI部分按官方文档源顺序核验。
- [x] 三路完成产品/开源/评测取证；主agent核验Cursor Compass原文、Anthropic安全降级、OpenAI官方推理/子agent文档及关键源码，补充级联和模型内生推理、固定档/随机路由反证。
- [x] 完成docs/local/research/2026-09-09-任务复杂度研判与模型路由工程综述.md；24组来源含12篇论文，三路独立复核后收紧版本日期、阈值保证、推理增量、重复样本及因果归因表述。
- [x] 1–11章节、24引用定义/首次顺序/正文链接、RQ闭环及空白检查通过；产品元信息、论文元信息与11个关键源码/AWS路径再核验。未复现模型实验、未推断Paw节省比例、未改源码/配置/技能，未提交推送。

## 更新本机 brainstorming（2026-09-09）

- [x] 用户授权更新；检查本机旧目录和安装工具，限定只更新brainstorming。
- [x] 使用skill-installer临时安装obra/superpowers v6.3.0（b36e0829c6d0140e93cfef2ca599b1b07d4a7797）；旧配套文件的content/state拆分和owner PID修复已包含于新版，8文件内容与权限符合上游。
- [x] 已切换/Users/united_pooh/.codex/skills/brainstorming；旧目录完整移至/Users/united_pooh/.codex/skill-backups/brainstorming-20260909.7eZJCB/brainstorming，切换后逐文件hash/权限复核一致。Node/Bash语法、隔离HTTP鉴权/片段包装/helper注入和停止验证通过；未改其他技能或Paw源码，未提交推送。

## grill-me 与 brainstorming 提示词风格研究（2026-09-09）

- [x] 冻结三项RQ及原文/作者解释/实测证据边界；按deep-research分三路只读检索。
- [x] GitHub当前grill-me wrapper→grilling/frontier，brainstorming三路由；锁定两main SHA，本机与2026-03-24上游文件SHA256一致。
- [x] 完成docs/local/research/2026-09-09-grill-me与brainstorming提示词风格比较.md；11证据组含原文、历史、作者说明、PR2063与Workflow Arena实测边界、两篇论文旁证。
- [x] 独立只读审查后修正审批点/终态、路径覆盖权/审批权、调查/实施边界三处；引文编号、连续章节、来源元数据、本机hash和diff检查通过。未运行模型A/B，不宣称某风格获胜；未改源码或安装技能。

## 认知成本清理（2026-09-07）

- [x] simplify-it 三路只读审查；基线 Go 全量和前端46 tests通过，保留用户既有dirty改动。
- [x] 具名 usage 位与显式 JSON/merge 关联；先运行16字段零/null/wire往返 characterization，再验证重构。删除ledgerErr死字段及mergeUsageSnapshot转发包装。
- [x] global 前端7文件格式化、Map/get项目查询、requestKey复用；重复项目最后覆盖和缺失ID回退用例在改前/改后均通过。
- [x] README职责导航、启动分发、运行时链路、Task/Engine API名称校准；67个本地相对链接检查无缺失。
- [x] Go全量、4包race、vet/build/diff、前端47 tests/typecheck/build、9 E2E通过；独立Go语义、前端逐字格式化对照和两图视觉审查通过，见verify。
- [x] 保留工作期间外部删除的 .agent/visual 与旧设计文档；不重建被删目录，新视觉证据改放本轮 /tmp 目录。未提交、推送、安装覆盖或调用外部模型。
- [ ] 后续（需另立切片）：三处UI usage派生缓存去重，保留未报告nil/显式零和返回值隔离。
- [ ] 后续（需明确边界）：两个全局轮询effect与终态详情刷新策略；不贸然引入通用hook。
- [ ] 后续（需计量回归）：ledger requestUsageTotal 单遍响应identity合并；保留原始attempt、无ID独立计量及向下修正。
- [ ] 后续（需恢复契约审计）：task迁移避免无效metadata重写，不能破坏缺失投影重建。


## 通用上下文上限防御性修复

- [x] 复现通用值已保存但仍为 131072、负数误接受、切换/重启忽略通用值；恢复失败会话后沿用原计划完成。
- [x] 统一 resolver、默认 0 自动、旧正值兼容、model map/profile 克隆及 JSON/UI 负值校验。
- [x] UI 显示/状态/卡片与保存/清空/重载/各模型切换路径接线；运行时含 incomplete、子任务及 state/summary 压缩共用实时来源。
- [x] 独立审查发现 settings 保存事务排序问题，补先红后绿锁范围与并发回归；同目录原子替换避免先截断原文件。
- [x] 定向 race、串行全量、vet/build/diff 检查通过；默认并行全量先通过，最终一次仅重现既有 exec 截断用例，单独 20 次通过。全仓 race 存在独立 actor/exec 失败，不宣称无条件全绿。
- [x] 80/120 列渲染、4 张截图与独立 Playwright 核对完成；真实 PTY 保存270000、专属64000覆盖、切回/重启270k通过：`/tmp/paw-context-smoke.nXLMhz/verification.json` 全true。
- 范围边界：自动维护仍在下一 turn 首轮检查；已有独立 worker 不实时订阅通用设置；同控制器锁不提供跨进程配置事务。未覆盖安装二进制或用户配置，未提交/推送。

## 全局 Token Tracer（2026-09-07）

- [x] 完成源码/设计/采集可靠性和原子计量研究，复核当前工作树。
- [x] 用户授权实现与方案选择；采用专用遥测记录和独立全局读服务。
- [x] A1 统一模型 usage 桶、累计合并、请求级 accumulator，生产 transport 标注与请求 ID 传播；显式零、缺失字段和向下修正回归通过。
- [x] A2 修复 loop 的 cache creation/Calls；worker、StreamMA 多请求累计；压缩与失败/重试观测覆盖。
  - 主链/worker/pool/StreamMA/压缩累计与 request ID 去重通过全包和 race。独立 request_start 计 Calls，零/失败无 usage、旧 StreamMA 生命周期与取消 drain 均有回归。
  - 模型 observer 已与 runtime Recorder 接通；request_start/end、实际 HTTP attempts、Responses failed/incomplete usage 均落全局账本，失败工具仍不放行。run ID 改为随机身份。
  - 原子分类已接全局UI，并修正真实 Chat/Anthropic 的 TOOL_RESULT 文本信封；通过结构化来源与真实wire匹配，防止用户手写相似文本误归工具。参数/结果payload与封装分离，不保存正文；mixed_text_v1仅粗估，多模态未知。
  - provider 不一致缓存/推理子集不再抬高总量或被截成伪精确值；原字段保留，已知状态降级并提示 usage_inconsistent。total-only来源保留，UI不虚构输入细分。
- [x] B1 独立 Recorder/增量 LedgerReader；跨项目去重、关闭历史、半行恢复、重复记录、轮转、异常版本、截断、symlink、权限与隔离回归通过。
- [x] B2 runtime/worker 接线和独立 tracer 命令；普通CLI三进程及worker/serve/interactive真实入口smoke均通过。
- [x] C 全局概览/项目/会话/模型/工具/请求、深浅主题、部分usage展示、筛选范围一致性、JSON导出和保留Dockview。39前端测试通过；按需加载Dockview将首屏JS从约606KB降至214KB。
- [x] D 三个真实Paw进程并发、每项目两轮Read工具调用，6HTTP/3840tokens/3个Read结果原子；退出重读、无正文、无项目.paw全部通过。8个浏览器E2E通过，1440/760/390视口截图与证据已存 .agent/visual/2026-09-07-global-token-tracer.md。
  - 首两次fixture失败分别因误用已有会话选择参数、mock误把role=user文本工具结果当普通提示。已经修正为真实serializer合同并加入15秒超时/12请求上限；旧失败证据不可当成功使用。
  - 最终重跑fixture：/tmp/paw-final-smoke.gn1VHN/verification.json；独立预览服务使用此隔离state目录，URL http://127.0.0.1:19008/。旧fixture成功证据仍保留。
  - Dockview E2E修正为 /?view=debug，等待浮窗实际持久化后再刷新；恢复时不再强制激活Calls，全部布局交互回归通过。
- [x] E 对照已授权目标审计和验证完成；保留既有Bash并行测试抖动限定，不宣称仓库无条件全绿。
  - 最终串行全量go test -p 1 ./... -count=1、6核心包race、vet/build/diff、46前端测试/typecheck/build、11浏览器E2E通过。默认并行全量先通过、末轮触发既有Bash截断用例；单独20次及串行全量通过，相关代码未改。
  - [x] 大历史查询切片：增量扫描与完整快照分离；v2 Query/单请求详情/全匹配导出接HTTP。601请求分页无重无漏、稳定同时间排序、期间/工具/会话/模型/搜索组合、总量/趋势不随页缩小、详情克隆隔离、过滤导出无其他项目metadata回归通过。
  - [x] 前端server查询、50/100/250分页、按需详情及失败重试，取消过期查询/丢弃迟到响应，导出显式读取全部匹配记录；旧客户端不再把新概览误读为0（分页API版本2，账本/导出版本1）。移除不再使用的前端全量筛选/分组投影，保留单请求明细计量。
  - [x] 10万请求预载历史基准（M4，5次/视图）：概览64.46ms/3349B，50请求页68.73ms/21608B，工具52.98ms/2545B；原全量快照245.98ms/186600945B。新路径每次分配约0.81–0.88MB，原全量约641.75MB；不含首次读盘/浏览器渲染/最大实例数压力。
  - [x] 44前端测试/typecheck/build通过；10/10 E2E通过（原三项目真实账本、按筛选完整下载、601行合成分页/末页/刷新保留选择、Dockview回归）；8张截图已采集并查看，其中pagination截图是明确标注的601行合成展示，不是601次真实模型请求。
  - [x] 旧StreamMA生命周期：Engine独立request_start经DisplayBus→streamingUI/worker wire透传，正常消费与取消后drain按requestID计Calls，不生成假Usage。零/失败/重复start及累计usage回归通过；进程内worker/Stream补TaskID、ParentSessionID、Purpose归属。定向race通过。
  - [x] worker/serve/interactive真实入口：/private/tmp/paw-entry-smoke.sVTkVI/verification.json，3HTTP/36tokens，worker start与usage身份相同；parent ownership、无遥测正文、无项目.paw均true。首次失败是fixture /tmp与子进程/private/tmp存储身份不同；修夹具后重跑，未改生产存储合同。自建交互进程已退出。
  - [x] 最后可靠性切片：Responses同logical同transport同响应ID累计207→105；不同ID/缺ID/不同logical不猜测合并，原始attempt/input exposure不删。partial/final独立于字段presence，未知attempt不回填；真实HTTP→observer→Recorder→Query/详情组合验证4HTTP/2logical/210tokens。
  - [x] Anthropic正常message_delta同时带stop_reason/usage的丢output修复：end_turn/max_tokens/tool_use三种真实流100/0→100/5均先红后绿，finishreason/工具flush/EOF行为未改。
  - [x] reader九项回归：64MiB扫描预算包含半行，小预算可续读；EOF释放payload，未增长不重读，增长后预算内恢复；全reader最多一个≤2MiB pending。80MiB坏尾积压可消退；64字节边界哨兵发现已测同inode长回，非任意篡改检测。
  - [x] 最终入口重跑：/private/tmp/paw-entry-final.DLPWNT/verification.json valid=true；3HTTP/36tokens，3条final usage metadata。自建TUI/worker/serve均退出，仅保留独立只读合成预览。
  - 已授权多项目/美化/优化/口径目标验收完成。未提交、未推送、未安装覆盖用户二进制。组件仍粗估、费用未知、有限留存、同目录ControllerLease未解除；这些边界见README，不冒充token-monitor全部功能复刻。

- [x] 确认 ANSI 控制序列泄漏的具体渲染路径与根因 <!-- todo:investigate -->
- [x] 调整终端命令结果渲染，避免 shell 输出被重新包装为 OSC 8 超链接，并补回归测试 <!-- todo:fix -->
- [x] 运行 gofmt、相关测试及 go test ./... <!-- todo:verify -->
- [x] 确认旧 config.json 兼容路径、discover 触发条件及迁移方案 <!-- todo:design -->
- [x] 实现 /config 通用设置中的 YOLO 配置，并停止旧 config.json 支持 <!-- todo:implement -->
- [x] 补充/更新测试与文档，运行 go test ./... <!-- todo:tests -->
- [x] 检查 provider 配置架构与相关设计文档 <!-- todo:inspect -->

## Actor 运行时重构（docs/spec-actor-runtime-refactor.md）

- [x] P0 清场：删 eventing/ipynb/py/hello 死包，仓库异物清零 <!-- todo:p0 -->
- [x] P1 装配收敛：buildRunner 9 元组 → appContext；plan/goal_controller 迁入 internal/ <!-- todo:p1 -->
- [x] P2 hook 收编：Hook 链 + 12 内聚协作者，Runner 字段 51→23 <!-- todo:p2 -->
- [x] P3 actor 内核：internal/actor 全量落地（分片单写者/Journal-First+Outbox/监督隔离/持久化定时器/虚拟时钟），覆盖 92.4%，-race 全绿，I1-I6 + 崩溃矩阵全过 <!-- todo:p3 -->
- [x] P4 TaskActor 换壳：task/manager→actor；StopOwnedTasks→Tell(Stop)；task 流事件化；旧内存态机删除（ADR-11）；行为等价 <!-- todo:p4 -->
  - [x] P4-1 勘察：worker/pool/persona/launcher 与 Manager 消费方清单（bootstrap/tool_registration/bubble/loop Runner 四处） <!-- todo:p4-1 -->
  - [x] P4-2 TaskActor（事件化 task 流，meta.json 兼容双写）+ RegistryActor（索引/WaitAny/订阅）+ Host 装配 <!-- todo:p4-2 -->
  - [x] P4-3 Manager 换壳为 facade，删除旧内存态机 <!-- todo:p4-3 -->
  - [x] P4-4 行为等价验证：manager_test 全绿 + golden 事件流 + 崩溃恢复用例；spec 更新 <!-- todo:p4-4 -->
- [x] P5 SessionActor：loop 引擎入住；session JSONL 与 es 合流；权限门→Suspend+Decision；goal/plan 会话恢复（fold 重建 activeGoalID/activePlanID） <!-- todo:p5 -->
- [x] P5 订阅总线：领域事件显示流（Ephemeral 通道）+ 合规落库 <!-- todo:p5-bus -->
  - [x] P5-1 Engine：Runner 终名、字段收敛、Ephemeral display bus 与行为 golden <!-- todo:p5-1 -->
  - [x] P5-2 SessionActor：Durable turn、Host、transcript 单流 adapter 与 activation-aware resume <!-- todo:p5-2 -->
  - [x] P5-3 恢复矩阵：inbox/partial/tool-started/tool-result/pending-permission fold 与幂等处理 <!-- todo:p5-3 -->
  - [x] P5-4 权限门：工作区外 Read allow-once/deny、批次预检与 Bubble selection dock <!-- todo:p5-4 -->
  - [x] P5-5 Goal/Plan：会话绑定、Plan snapshot、控制器重绑定、恢复提示与 `/plan resume` <!-- todo:p5-5 -->
  - [x] P5-6 原子切换与验收：生产调用方只经 SessionActor，旧执行路径删除，全量门禁通过 <!-- todo:p5-6 -->
- [x] P4–P5 第三轮审查修复轮（spec §15）：H2/H3/H4/M1/M2 修复、Kind 空值归一、system.go/cell.go 拆分（<250 行）、内核管理端口单测、P5 事件流级 golden 三路径、flaky 测试受控时钟化、bench 基准入库 <!-- todo:review-round -->
- [ ] P6 loopRunner 接入换壳（前置债务：loop 包扇出 11→≤8；Durable 跨消息 group commit；流式端到端与常驻内存基准，见 spec §10/§15） <!-- todo:p6 -->
- [x] 读取项目说明与持久化记忆，确认调研范围和验证方式 <!-- todo:survey-context -->
- [x] 分析多 worker/agent 的核心架构与任务传递流程 <!-- todo:survey-workers -->
- [x] 分析插件、MCP、工具注册与扩展机制 <!-- todo:survey-plugins -->
- [x] 分析项目目录、入口、运行流程和关键文档 <!-- todo:survey-structure -->
- [x] 汇总 worker 结果，给出当前项目快速概览与结论 <!-- todo:survey-summary -->
- [x] wheel_coalescer：删 60fps 合并，改为每滚轮事件立即滚动 <!-- todo:c-coalescer -->
- [x] app.go：delta 1→3，简化 transcriptWheelBatchMsg 分支 <!-- todo:c-app -->
- [x] bubble.go：简化 programEventFilter 构造 <!-- todo:c-bubble -->
- [x] 重写/修正 wheel_coalescer 相关测试与 delta 断言 <!-- todo:c-tests -->
- [x] go test ./internal/ui/bubble 验证 <!-- todo:c-verify -->
- [x] 阅读项目结构、项目文档与持久化记忆，确认相关模块和约束 <!-- todo:investigate-context -->
- [x] 定位剪贴板粘贴、图片附件创建、会话初始化/消息发送代码路径 <!-- todo:investigate-flow -->
- [x] 检查新会话与已有会话的状态差异，并用现有测试确认图片数据丢失边界 <!-- todo:investigate-state-tests -->
- [x] 基于调查结论实施最小修复 <!-- todo:implement-fix -->
- [x] 运行 go test ./... 及相关回归验证 <!-- todo:verify-fix -->
- [x] 图片粘贴生产路径修复：在 SessionActor Durable 消息序列化前保存图片附件引用，补充 SessionActor 回归测试；`go test ./... -count=1`、相关 race/build/vet/gofmt/diff 检查全通过 <!-- todo:clipboard-image-fix -->
- [x] 沉淀阶段结果与教训到项目记忆 <!-- todo:archive-results -->
- [x] 调查并修复 worker fork/exec EAGAIN（a622bad，已验证） <!-- todo:bug-worker-eagain -->
- [x] go test ./... 全量验证修复并提交（a622bad） <!-- todo:bug-verify -->
- [x] 任务 1：<model> 块格式与解析（9b0a367） <!-- todo:task-1-recover -->
- [x] 任务 2：renderModelSwitchCard 圆角状态卡（0ce7940） <!-- todo:task-2 -->
- [x] 任务 3：transcript 集成分支（03df929） <!-- todo:task-3 -->
- [x] 任务 4：四处调用点统一切换（611e68f） <!-- todo:task-4 -->
- [x] 移除 /model Confirm 步骤：选择模型后直接应用（ebd3ae8） <!-- todo:remove-confirm -->
- [x] 合入 worker fork/exec EAGAIN 修复（e5a2471） <!-- todo:merge-worker-fix -->
- [x] 全量 go test ./... + go vet ./... 回归 <!-- todo:full-verify -->
- [x] 定位 ox-alpha 相关配置、模型适配器与调用入口 <!-- todo:scope -->
- [x] 检查流式事件到可见消息的完整处理链路及空响应判定 <!-- todo:pipeline -->
- [x] 检查当前会话日志/持久化记录，提取连续空响应的一手证据 <!-- todo:evidence -->
- [x] 整理事实、关键约束与基于证据的结论 <!-- todo:conclusion -->
- [x] 修复 duplicate finish_reason 问题（internal/model/stream.go） <!-- todo:fix-duplicate-finish-reason -->
- [x] 调查 /clear 指令无法创建新 session、携带脏上下文的原因 <!-- todo:investigate-clear-session -->
- [x] 确认并测试 ox-alpha duplicate finish_reason 兼容修复 <!-- todo:verify-stream-fix -->
- [x] 调查现有 session 创建/切换流程与 /clear、/new 命令约束 <!-- todo:map-new-session-flow -->
- [x] 实现 /clear 与 /new 共享的新 session 创建行为 <!-- todo:implement-new-session-command -->
- [x] duplicate finish_reason 与新 session 相关包测试、10 次重复回归、定向 race、`go vet ./...`、diff 检查通过；`go test ./...` 仍有无关失败：actor 用例单跑通过、UI reasoning 用例定向运行仍失败 <!-- todo:verify-all -->
- [x] 定位 task 完成事件、taskController 状态与侧边块渲染链路 <!-- todo:investigate-task-sidebar -->
- [x] 添加或定位可复现侧边块未消失的失败测试 <!-- todo:reproduce-task-sidebar -->
- [x] 实施最小修复，确保 task 结束后侧边块消失 <!-- todo:fix-task-sidebar -->
- [x] taskController 悬浮卡改用本地进程存活视图；task/UI 定向测试、race、`go vet ./...` 与 diff 检查通过；`go test ./...` 仅剩未触及的 reasoning UI 用例失败 <!-- todo:verify-task-sidebar -->
- [x] First impression & paper-type positioning <!-- todo:1 -->
- [x] Fatal-flaws audit (early gate) <!-- todo:2 -->
- [x] Lifecycle & capability matching <!-- todo:3 -->
- [x] Five-dimension scoring <!-- todo:4 -->
- [x] Paradigm-shift probe <!-- todo:5 -->
- [x] Feasibility check <!-- todo:6 -->
- [x] Run integrity gate silently & confirm verdict consistency <!-- todo:7 -->
- [x] 定位状态栏/顶栏 chip 的组装与样式边界（谁在给空格上背景） <!-- todo:inv-1 -->
- [x] 写临时试验：ASCII/中文/宽字符分支名的 chip 与 markdown 输出原始 ANSI 取证（chip 部分已完成） <!-- todo:inv-2 -->
- [x] 分析证据，给出结论与修复建议 <!-- todo:inv-3 -->
- [x] 定位 FinishReason 生产消费点与 tool call 执行入口 <!-- todo:t1 -->
- [x] 增量1：JSON 修复层（repairJson + 挂入 decodeToolArguments）+ 测试 <!-- todo:t2 -->
- [x] 增量2：Schema 类型强转（coerce + 挂入 restoreToolArguments）+ 测试 <!-- todo:t3 -->
- [x] 增量3：截断守卫（Anthropic message_delta stop_reason=max_tokens 映射 FinishReasonLength）+ 测试 <!-- todo:t4 -->

## 工具参数错误自愈三防线（借鉴 pi-mono，2026-08）

- [x] 调研 Paw 现状与 pi-mono 处理策略（InputError 透传 / IsError 回喂已同构） <!-- todo:investigate -->
- [x] 增量1 JSON 修复层：decodeToolArguments 修复字符串内裸控制符/非法转义/尾反斜杠+闭引号；结构性损坏仍拒绝（internal/model/tool_arguments.go） <!-- todo:implement -->
- [x] 增量2 Schema 类型强转：coerceValueToSchema（数字串→number、bool 串→boolean、number→string，anyOf 分支选择），挂入 restoreToolArguments 校验前执行（internal/model/schema_coerce.go） <!-- todo:implement -->
- [x] 增量3 截断守卫：Anthropic message_delta stop_reason 映射 FinishReason（max_tokens→length/tool_use→tool_calls/end_turn→stop），复用 runner 既有「length 截断含工具调用即拒执行」守卫（internal/model/anthropic_stream.go） <!-- todo:implement -->
- [x] go test ./internal/model ./internal/loop 全绿；ui/bubble 失败经 HEAD worktree 验证为既有问题与本改动无关 <!-- todo:verify -->

教训：Anthropic 流此前丢弃 stop_reason，截断消息里的 tool call 会带合法参数直接执行——修复层可能把截断参数补成「合法但残缺」，宁可拒执行回喂错误让模型重发。
- [x] go test ./... 全量验证 <!-- todo:t5 -->
- [x] 确认 GLM-5.3-Flash 的官方型号与发布信息 <!-- todo:i1 -->
- [x] 核对官方 API/模型卡声明的输入输出模态 <!-- todo:i2 -->
- [x] 区分基础模型能力与平台侧图片/视频工具能力 <!-- todo:i3 -->
- [x] 整理适用于 Paw 的结论与限制 <!-- todo:i4 -->
- [x] 定位 snapshot seq=0 根因（Ephemeral-only actor 从未产生流事件，快照仍尝试写） <!-- todo:root_cause_snapshot -->
- [x] 定位 8999 端口占用根因（旧 agent 进程持端口，tracer 绑定失败即致命） <!-- todo:root_cause_port -->
- [x] 修复：零事件 cell 跳过快照写（internal/actor/pump.go snapshot） <!-- todo:fix_snapshot_skip -->
- [x] 修复：tracer 端口被占时回退空闲端口并大声提示（cmd/agent/interactive.go） <!-- todo:fix_port_fallback -->
- [x] 回归测试：两处各加一个用例；go test 全部相关包通过 <!-- todo:fix_and_test -->

## tool_use JSON 泄漏到 transcript 修复（fence 包裹 + 非法转义，2026-08）

- [x] 根因：模型在 tool_use 信封外包 ``` fence，且 pattern 含非法 JSON 转义（如 `\.`），json.Valid 失败 → loop 信封解析与 bubble UI 清洗双双放弃，原始 JSON 明文上屏 <!-- todo:leak-root-cause -->
- [x] 修复：model.RepairJSONStringLiterals 导出；loop decodeToolUseEnvelope(s) 解析失败先修字面量重试（internal/loop/tool_parser.go）<!-- todo:leak-loop-fix -->
- [x] 修复：bubble isToolUseJSONPayload 同样接入宽松修复，保证泄漏内容仍被识别并从视图剥离（internal/ui/bubble/utils.go）<!-- todo:leak-bubble-fix -->
- [x] 测试：tool_parser_test.go 2 例 + utils_leak_test.go 2 例全绿；loop/model 包 -count=1 通过；go vet 干净；bubble 全量 FAIL 为既有问题（HEAD worktree 基线同样失败，与本改动无关）<!-- todo:leak-verify -->
- [x] 读全部 selectedProviderStyle 调用点上下文（completion/right_panel/activity 等）确认 receiver 可用性 <!-- todo:u1 -->
- [x] 改 7 处调用点到 SelectionSelected + translate 换 MarkdownHighlight <!-- todo:u2 -->
- [x] 删除 Selected（style_set）+ selectedProviderStyle（styles.go）+ color 角色（color_manager/theme）+ 测试表条目 <!-- todo:u3 -->
- [x] go build ./... + 定向测试 + go test ./... 全量并对比既有失败基线（IDENTICAL_TO_BASELINE 已证实） <!-- todo:u4 -->
- [x] 调查 selection dock 单选/多选状态迁移、样式和现有测试 <!-- todo:q1 -->
- [x] 补失败测试：反色选中、单选导航即选中、多选焦点与选中保持独立 <!-- todo:q2 -->
- [x] 实现 SelectionSelected 反色和单选焦点即选中 <!-- todo:q3 -->
- [x] selection/question 定向测试、P5 golden、go vet、go build 通过；bubble 全量失败集合与 HEAD 基线一致；全量额外 exec 限流用例单次波动，独立重复 5 次通过 <!-- todo:q4 -->
- [x] 运行定向测试、go test ./... 并核对既有基线失败 <!-- todo:q4 -->

## Responses 历史跨供应商兼容（2026-08-31）

- [x] 定位根因：Responses `ProviderData` 在模型/供应商切换后仍原样重放，`web_search_call.action` 被严格端点拒绝 <!-- todo:responses-origin-investigate -->
- [x] 扩展 `MessageOrigin`，持久化 provider/profile/transport/adapter/model <!-- todo:responses-origin-metadata -->
- [x] 同源请求保留 ProviderData；跨源请求回退通用消息/工具投影 <!-- todo:responses-origin-fix -->
- [x] 覆盖跨模型、同模型跨 profile、同源 status 清洗、旧 transcript 保守降级与工具配对 <!-- todo:responses-origin-tests -->
- [x] `go test ./... -count=1`、定向 race、go vet、diff check 通过；首次全量 actor 时序用例抖动，单测 10 次及第二次全量均通过 <!-- todo:responses-origin-verify -->
- [x] 更新跨会话进度/验证记录 <!-- todo:archive -->
- [x] 探索 Paw 当前运行时、UI 边界与浏览器服务基础 <!-- todo:context -->
- [x] 确认是否启用视觉伴侣展示浏览器原型与架构图 <!-- todo:visual -->
- [x] 逐项澄清浏览器前端的目标、范围、部署与成功标准 <!-- todo:questions -->
- [x] 提出 2–3 种架构方案并分析权衡 <!-- todo:approaches -->
- [x] 编写并提交浏览器前端设计规格文档 <!-- todo:spec -->
- [x] 执行规格占位符、一致性、范围和模糊性自检 <!-- todo:selfcheck -->
- [x] 等待用户审查书面规格并处理修改 <!-- todo:review -->
- [x] 还原故障前后的持久化事件与时间线 <!-- todo:timeline -->
- [x] 核对 Paw 内所有结束或取消 TUI 的入口 <!-- todo:app-exits -->
- [x] 检查 Bubble Tea Program.Run 的非命令退出条件 <!-- todo:tea-exits -->
- [x] 查找故障时的进程、信号和终端日志证据 <!-- todo:runtime-evidence -->
- [x] 梳理实现涉及的现有文件、构造器、测试和前端工具链 <!-- todo:plan-context -->
- [x] 编写分阶段 TDD 实现计划与精确文件清单 <!-- todo:plan-write -->
- [x] 检查浏览器工作台计划的规格覆盖、占位符、精确文件路径与仓库可执行性 <!-- todo:plan-check -->
- [x] 使用 Gitmoji 提交浏览器工作台实现计划，并保持既有 model/message 改动不入提交 <!-- todo:plan-commit -->
- [x] 完成 writing-plans 交接，计划位于 docs/superpowers/plans/2026-08-31-browser-workbench.md <!-- todo:plan-handoff -->
- [x] 检查规格覆盖、占位符和类型一致性 <!-- todo:plan-check -->
- [x] 使用 Gitmoji 提交实现计划 <!-- todo:plan-commit -->
- [x] 提供计划执行方式并完成 writing-plans 交接 <!-- todo:handoff -->
- [x] 任务 1：建立 workspace canonical path 契约 <!-- todo:impl-1 -->
- [x] 任务 2：实现顶层 ControllerLease <!-- todo:impl-2 -->
- [x] 任务 3：为 session store 增加显式 root 构造器 <!-- todo:impl-3 -->
- [x] 调查 Task worker 中断异常、失败链路与无效 token 消耗根因 <!-- todo:task-worker-investigation -->
- [x] 修复 Task worker 生命周期：后台任务脱离父 turn、显式取消带 cause、失败保存部分文本/usage、worker stderr 入错、池容量计数与失败后复用；TaskWait 默认恢复 90 分钟 <!-- todo:task-worker-remediation -->
- [x] 修复 Responses SSE 解析：按事件边界解析多行 data，坏帧后等待 completed 权威快照恢复，跨 provider/model 历史保守投影 <!-- todo:responses-stream-recovery -->
- [x] 任务 4：提取 WorkspaceRuntime 组合根与 runtime-owned Toolset，禁用 worker config watcher，关闭顺序与跨 runtime 隔离测试通过 <!-- todo:impl-4 -->
- [x] 脱敏审查、Gitmoji 提交、推送 dev 并更新本地二进制 <!-- todo:release-current -->
- [x] 任务 5：实现共享 ResourceGovernor；各 runtime 的进程池创建常驻 worker 前获取共享 slot，worker 退出时幂等释放 <!-- todo:impl-5 -->
- [x] 任务 6：实现 Supervisor 两 runtime 上限、busy 保护、LRU 空闲淘汰与最近工作区原子存储；ForgetRecent 不隐式关闭 runtime <!-- todo:impl-6 -->
- [x] 任务 7：实现 WorkspaceCoordinator 单写者状态机，固定 active session/turn、队列、interaction、session version 与 Activity 快照 <!-- todo:impl-7 -->
- [x] 任务 8：实现 store-only SessionService，支持 session/turn 游标分页、只读快照以及不激活 Host 的 Create/Fork <!-- todo:impl-8 -->
- [x] 任务 9：定义 schema_version=1 的 AppEvent 信封与 typed payload JSON 契约 <!-- todo:impl-9 -->
- [x] 任务 10：实现 EventHub 原子 replay/live 切换、ring 淘汰、游标 reset 与慢消费者 reset <!-- todo:impl-10 -->
- [x] 任务 11：实现 coordinator/EventHub 一致快照、流式 part 投影与 25ms/16KiB UTF-8 byte offset batcher <!-- todo:impl-11 -->
- [x] 任务 12：新增 session.command_receipt journal 记录；Create/Fork 跨重启与并发重试返回同一资源且只持久化一次 receipt <!-- todo:impl-12 -->
- [x] 任务 13：实现 UI Adapter，将 reasoning/assistant/tool/system 回调投影为稳定 ID、offset、摘要和 detail 事件 <!-- todo:impl-13 -->
- [x] 任务 14：增加独立 serve FlagSet 和 loopback listen 校验，保持 legacy/worker flags 解析兼容 <!-- todo:impl-14 -->
- [x] 任务 15：实现 256-bit 一次性 bootstrap exchange、HttpOnly SameSite cookie、Host/Auth/Origin/body-limit/security-header middleware <!-- todo:impl-15 -->
- [x] 任务 16：实现 bootstrap/workspace/session/export API、serve fragment URL、context shutdown 与 Supervisor 强制收敛 <!-- todo:impl-16 -->
- [x] 任务 17：实现 cookie-authenticated SSE、after/Last-Event-ID 游标、wire frame、fake ticker heartbeat、reset frame 与 client cancel 释放 <!-- todo:impl-17 -->
- [x] 任务 18：建立 React/Vite/Vitest/ESLint 工具链、静态 SPA handler、hashed immutable assets 与 Go embed 构建脚本 <!-- todo:impl-18 -->
- [x] 任务 19：实现前端 typed API、snapshot store、SSE EventSource 连接器与 stream/sequence/UTF-8 offset reducer <!-- todo:impl-19 -->
- [x] 任务 20：实现只读现代工作台：工作区/会话侧栏、对话/轨迹切换、详情抽屉、安全 Markdown 与 DSH 风格设计 token <!-- todo:impl-20 -->
- [x] 任务 21：接通 Session Create/Fork、Submit、active turn/receipt 生命周期、busy/version 错误和带本地草稿/稳定 command ID 的 Composer <!-- todo:impl-21 -->
- [x] 任务 22：接通 active_turn 校验下的 steer、queue、cancel，持久化 command input/receipt，并同步 Composer 运行状态与队列事件 <!-- todo:impl-22 -->
- [x] 调查“复述目标 + 宣布下一步”消息的实际来源与关闭方式：用户端 harness 自动注入的续行提示，非 agent.md 要求 <!-- todo:investigate-progress-restatement -->
- [x] 任务 23：实现 InteractionHub question/permission 请求-应答、coordinator pending 状态、SSE 事件、HTTP answer/decision 幂等端点与前端 InteractionBanner <!-- todo:impl-23 -->
- [x] 任务 25：实现 bounded TraceDetailStore、scoped /trace/{event_id} 详情 API、2MiB 截断与前端详情栏 loading/error/copy 状态 <!-- todo:impl-25 -->
- [x] 任务 1–23：canonical path、lease、session root、runtime、governor、supervisor、coordinator、session service、事件/快照、receipt、UI adapter、serve、auth、API、SSE、前端工具链、store/reducer、只读工作台、submit、steer/queue/cancel、question/permission <!-- todo:impl-1-to-23 -->
- [x] 任务 24/25：trace detail store、详情 API 与前端详情栏 <!-- todo:impl-24-25 -->
- [x] 任务 26：SSE reset 后重取快照并按新 stream/sequence 重连，reconnectingSnapshot 防并发重载 <!-- todo:impl-26 -->
- [x] 任务 1–26：后端运行时/事件/命令/SSE/交互/详情与前端工作台全部核心闭环 <!-- todo:impl-1-to-26 -->
- [x] 任务 1–27：后端运行时/事件/命令/SSE/交互/详情/多工作区切换与前端工作台 <!-- todo:impl-1-to-27 -->
- [x] 任务 28：重启时 restoreUnfinishedTurns 投影 turn.interrupted、清理 coordinator active/pending，恢复排队输入时显式传入 event 上下文 <!-- todo:impl-28 -->
- [x] 任务 1–28：后端运行时/事件/命令/SSE/交互/详情/多工作区切换/重启投影与前端工作台 <!-- todo:impl-1-to-28 -->
- [x] 任务 29：真实 E2E fixture 与 Playwright 测试；修复 projection 把 receipt 误归 legacy turn、竞态丢弃落伍快照、refreshNow 闭包过期 <!-- todo:impl-29 -->
- [x] 读取配置加载与供应商/模型初始化代码，确认全局与项目配置的优先级 <!-- todo:config-flow -->
- [x] 检查实际配置文件位置与启动环境，对比 ~/ 和仓库目录的行为（不输出密钥） <!-- todo:config-environment -->
- [x] 用现有测试或最小复现验证原因，汇总证据和处理建议 <!-- todo:config-verify -->
- [x] 梳理所有工作区 .paw 写入、全局路径和隔离规则，确定统一存储改动范围 <!-- todo:global-audit -->
- [x] 先补失败测试，再统一配置及工作区运行数据的全局路径 <!-- todo:global-paths -->
- [x] 修正其余工作区 .paw 写入与文档，保留按工作区隔离的数据 <!-- todo:global-consumers -->
- [x] 从当前会话失败日志追踪 Responses 流式读取、超时、重试和历史恢复边界 <!-- todo:responses-investigate -->
- [x] 设计并实现 3–4 层解耦防御及故障注入回归测试 <!-- todo:responses-defenses -->
- [x] 运行完整测试与独立审查，验证主目录启动和工作区零 .paw 写入 <!-- todo:global-verify -->
- [x] 验证中断、截断、重试和重复输出防护，运行完整测试与独立审查 <!-- todo:responses-verify -->

## Responses completion_mismatch 误报（2026-09-05 晚）

- [x] 对照截图 task/parent 错误、当前流式源码和旧格式化函数，复现多段/空白导致的正常完成误拒绝。
- [x] 在已发布 reasoning 的单次与跨重试路径使用原始快照投影；保留完成展示格式和严格冲突拒绝。
- [x] 新增 28 个回归子场景；模型/loop race、独立 review、build/vet、5 个真实 CLI 场景和串行全量 `go test -p 1 ./... -count=1` 通过。默认并行测试有下述独立失败，未声称所有门禁全绿。
- [ ] 独立后续项：调查未修改的 `TestBashStreamOutputLimitRemainsBounded` 在全量执行中缺少截断标记的问题；两次并行全量失败，单独 20 次及串行全量通过。源码发现 Wait/Close 早于 reader 完成的时序风险；本轮没有修改 Bash 执行器。

教训：协议字节对齐不能复用会 trim/插入换行的展示文本；是否已有 reasoning 是跨 attempt 的已发布状态，不能只检查当前 attempt 的 delta。
- [x] 追踪 /config 通用上下文长度的保存及运行时应用路径 <!-- todo:trace_config_context -->
- [x] 核对模型上下文优先级、显示与压缩行为，解释失效原因 <!-- todo:verify_context_precedence -->

## 目录组织迁移（2026-09-08）

- [x] dirty源码备份、完整包/测试/embed迁移；原internal的808个非依赖/非dist文件均在新位置存在。
- [x] cmd/paw薄入口、六类entry包、app共享装配、SessionHost/Engine命名；旧入口不保留。
- [x] Makefile/脚本迁新路径；长期docs与本地资料分流；101个文档链接通过。
- [x] Plan兼容迁移/恢复/symlink保护；产物隔离/空白身份/文档评分/普通模式写失败告警回归；三路独立复核。
- [x] 1223个历史产物文件可恢复归档；重跑后源码无.paw/.pipeline-workspace/旧summary。
- [x] Go全量、定向race、vet/build、五类新CLI模式、本地HTTP/PTY/E2E通过；前端44+47 tests与两端build通过，限制见verify。
- [ ] 独立既有项：工作台ConversationView.tsx:435的react-hooks/set-state-in-effect失败。与迁移前逐字相同，未改用户滚动逻辑或关规则；make web-build带lint所以同受阻挡。
- [ ] 独立既有项：worker parser返回的WorkerContext在生产入口丢弃后重建；本轮未改变，不扩大重构范围。

- [x] 2026-09-08后续：make build默认输出~/go/bin/paw，增加BINDIR覆盖并同步三处活跃说明；临时含空格目录实编/help、Go全量和diff检查通过，未覆盖已安装二进制。

## 单行输入区视觉预览（2026-09-08）

- [x] 用户要求先预览；已建视觉伴侣http://localhost:52964（.superpowers/brainstorm/98386-1788843729），浏览器已查看当前/提案对照，空态总占位146→86px。
- [x] 用户明确选择B「默认一行，多行时展开」。保留此页面，不推waiting覆盖。
- [x] 设计写入docs/local/specs/2026-09-08-compact-composer-design.md；暂无生产源码修改，Composer/workbench CSS的SHA256与预览前相同。
- [x] 用户最终确认保留200px源码上限，授权实施；已按writing-plans进入实现，初版原型150px不作为正式上限。

## 单行输入区实施（2026-09-08）

- [x] 默认rows=1、内容变化与宽度变化自动测量，删除/发送后收起；CSS独占36px最小值与200px最大值，无新增高度state。
- [x] 删除composer-hint及其样式；紧凑卡54px，readonly输入区样式不变。390px空态占位文字不再折行撑高，真实文本仍自然折行。
- [x] 先红后绿：6项新增单测与390px真实浏览器边界。最终前端50 tests、typecheck/build、改动文件lint、4E2E、Go全量、make check、临时构建/help与diff检查通过。
- [x] 五张实际组件截图和结构证据在.agent/visual/compact-composer.md；本地fixture无外部模型调用，端口18777已退出。原视觉伴侣未覆盖；未安装覆盖、提交或推送。
- [ ] 独立既有后续项：初次E2E发送后snapshot含turn.messages=null，ConversationView.tsx:106直接forEach导致白屏。随后三次完整E2E通过，但不代表竞态已修复；该文件与迁移前tar备份SHA256完全相同。保留失败trace，未扩大本轮修复范围。

## 会话导航与新消息浮钮（2026-09-08，已批准范围完成）

- [x] 用户先批准交互原型中的刻度位置和预览卡大小；当时仅完成预览，后续完整规格批准及实施记录如下。
- [x] investigation-first确认新消息浮钮引用未定义的--surface-card；当前服务CSS与旧HEAD均存在。原CSS隔离浏览器复现背景rgba(0,0,0,0)、opacity1、z-index6，正文透出；12项ConversationView测试通过但不覆盖CSS绘制。证据与限制见docs/local/plans/2026-09-08-new-message-notice-investigation.md。
- [x] 用户回复“要”，批准把新消息按钮不透明背景修复纳入导航改动；完整规格已写入docs/local/specs/2026-09-08-conversation-navigation-design.md并自审。
- [x] 用户回复“可以”，批准完整书面规格及首版已加载轮次范围；按writing-plans分切片TDD实施。
- [x] 满宽单一滚动视口、768px居中正文、边缘滚动条与实际dock净空；不透明背景和高于dock渐变的浮钮层级分别回归。
- [x] 一轮一项的正文摘录、stable turn_id锚点、悬停/聚焦预览、键盘定位、当前阅读高亮；窄屏菜单、100轮可达和宽窄往返焦点通过。
- [x] 上翻/导航保持历史位置，新增流式内容与完成快照不抢底；有未读显示新消息数，无未读提供回到最新；切会话清理。
- [x] 68前端单测、typecheck/build、8完整浏览器测试、隔离Go全量、make check、临时BINDIR构建/help与diff检查通过。九张生产截图及结构化说明.agent/visual/conversation-navigation.md；没有安装覆盖、提交或推送。
- [ ] 独立既有项保持：ConversationView发送effect lint现位于451（原435，逻辑未改）；null-messages快照竞态仍未修复。默认导航只覆盖已加载轮次，不含更早历史分页。

## 导航密度反馈（2026-09-08）

- [x] 用户认为25px太稀疏，提供密集参考图；已做25/14/10px三档视觉对照，推荐10px、2px线厚，触屏菜单保持大点击区。
- [x] 原型localhost60497已打开，浏览器实测与hover/click检查通过，截图.agent/visual/navigation-density-preview.png；探索清单docs/local/plans/2026-09-08-navigation-density-exploration.md。不修改真实会话或生产样式。
- [x] 用户明确选择密集刻度；同步替换旧规格的桌面最小24px约束，CSS仅25→10px行高、3→2px线厚，移动菜单44px不变。
- [x] 密度回归先红后绿；68单测/typecheck/build/改动测试lint/Go全量/make check/临时构建help通过。5项导航E2E两轮均通过；完整套件首轮7/9、全新实例复跑9/9，首轮null-messages白屏及后续bootstrap认证失败均保留，不声称竞态已修。
- [x] 生产截图和限定.agent/visual/navigation-density.md，日志/tmp/paw-density.E9PaJe；未覆盖安装程序、用户配置或会话，未提交/推送。
- [x] 最终校验：git status 干净、提交历史可读 <!-- todo:t6 -->

## 前端产物新鲜度门禁（2026-09-09）

- [x] 用户批准B；发现工作区已有未提交指纹脚本与Makefile/npm/pre-push接入，保留并补齐，不重复覆盖。
- [x] 隔离回归先红后绿：source/public/config/lockfile、缺失输入、dist篡改/删除、hash失败、mtime独立与Make入口阻断；换目录/还原源码同内容通过。10个测试组通过，npm失败不写stamp，pre-push旧产物不调用Go或覆盖binary。
- [x] 工作台69、看板47项npm测试及两套typecheck/Vite build通过；串行Go全量、make check、临时含空格BINDIR构建/help和diff检查通过。证据/tmp/paw-web-dist.TMr2HJ；新binary含pill-face/search-face，不宣称UI视觉验收。
- [x] 保留并加固已有B实现；两份v2 stamp由真实npm build生成。不改历史统计源码、安装程序、前端src或全局lint；不提交/推送。已有跳过变量仍明确告警；raw go build不受此门禁保护。
- [ ] 独立既有项：pre-push当前HEAD=dev时使用dirty工作树，且依据本地ref而非stdin实际推送SHA；本轮只接入/验证新鲜度，不声称修复完整发布快照一致性。

## 历史回合统计调查与修复（2026-09-08，已完成）

- [x] investigation-first：现场会话9319dd的六轮中前五轮仅时间、最后16:45完整统计；磁盘仅最后一条sidecar，浏览器DOM与之对应，当前服务JS与dist逐字节一致。
- [x] 定位runTask丢弃TurnTiming、TaskOrchestrator传nil、completeTurnExecution跳过统计写盘；隔离Go overlay测试gate关闭通过、开启漏存失败且无persist error。既有11项Go与15项ConversationView测试通过，不能否定新增复现。
- [x] 结论与源码/现场/测试边界记于docs/local/plans/2026-09-08-history-turn-metadata-investigation.md；临时证据/tmp/paw-history-investigation.s9RaML。未改生产代码、真实会话、安装程序或配置，保留既有dirty内容。
- [x] 用户回复“要”，授权修复保存链路与历史恢复测试；旧token回填不纳入本次实施。
- [x] 先补gate首轮/自动续行/ExecuteTurn计量失败测试，再修最小执行链。生产仅3文件，Task保留首轮timing、续行重置、单轮入口初始化ID/start/usage。
- [x] 覆盖中间轮写盘错误不丢失，以及真实Host执行后重新加载历史统计。历史集成测试覆盖ConsistentSnapshot、actor assistant_seq和读取前后文件字节不变；旧源码overlay复测缺sidecar失败。
- [x] 最终串行Go全量、3包race、历史集成race重复5次、vet/临时构建/help、15项前端页脚测试和diff检查通过。真实隔离Web API页面刷新后两轮统计仍在，截图.agent/visual/history-turn-metadata.png；fixture已退出。未覆盖安装程序、真实会话或配置，未提交推送；旧缺失统计未回填。
- [x] 运行 go test ./... 验证通过 <!-- todo:test -->
- [x] 梳理各组变更的差异，确定原子提交分组 <!-- todo:group -->
- [x] 创建原子提交：fix(loop) 与 feat(build) 两个 commit（memory/progress.md 保留本地） <!-- todo:commit -->
- [x] 推送到 origin/dev <!-- todo:push -->
- [x] 盘点 ~/.codex 中可清理的配置类文件与条目 <!-- todo:inventory -->
- [x] 按确认范围执行清理（备份删除走废纸篓，config.toml 改动先留检查点） <!-- todo:execute -->

## Default 跟随终端（2026-09-24）

- [x] 用户批准完整方案；已定位全帧补底色、局部背景与 RGB 插值路径。
- [x] 失败测试与调色板/样式修复。
- [x] 主题切换、终端光标及动画兼容。
- [x] 全仓测试、定向 race、vet、独立构建/help 通过；视觉证据 `.agent/visual/default-terminal.md`。未覆盖安装程序，保留并行 LaTeX 改动，未提交推送。

## 透明主题对比度修正（2026-09-24）

- [x] 用户实机截图显示 ANSI 8 与亮桌面合成背景近乎同色；对比度测试和真实 View 测试复现。
- [x] default 辅助文字改终端默认前景，边框 ANSI 7、注释 ANSI 15；theme/bubble 测试、vet、亮背景预览与构建通过。已更新 ~/go/bin/paw，旧文件备份和哈希见 .agent/visual/default-terminal/contrast-install.json；需重启现有进程。
