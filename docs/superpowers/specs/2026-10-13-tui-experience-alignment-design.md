# TUI 体验对齐设计：配色、图标语义、commandcode 体验

日期：2026-10-13
状态：已批准（brainstorming 逐节确认，用户授权自主实施）
分支：dev

## 背景

2026-10-13 的黑盒调查（对照 commandcode 截图）确认四条根因，全部有 file:line 证据：

1. **默认折叠工作过程**：工作段折成一行 `▸ Thought for…`（transcript_worksegment.go:337-360）；assistant 开口即强制折叠 pending 工具卡片 `readyPendingToolSegmentsInCurrentTurn`（transcript.go:1131-1141）；工具组头 `▸ Tools N calls` 无文件名（transcript.go:2218）。这是 2026-08-24 工作段折叠 spec 的设计哲学，本次按用户要求调整取向。
2. **遥测占黄金位置**：Activity 侧栏默认开（app.go:84，约 36% 宽），无数据也渲染 8 行 `—` 占位。
3. **状态分散**：5 个活动指示器（header `⠋ Ns working`、CTX ripple、`◌` 行、`● Thoughts`、光标呼吸）、5 个时钟 4 参照系；无中断提示；底部无规范状态行。
4. **三处重复**：Activity/Tasks 双标题（activity.go:24 + 框线）、CTX 双读数（status_line.go:120-131 vs 231-237）、错误双管道（失败卡 + 裸 error 行）。

paw 的渲染底座（卡片/diff/动画/golden 帧）全部现成，本设计是**取向调整 + 组件层收口**，不是重写。

## 目标 / 非目标

**目标**：
- 任何时刻一眼看懂"正在干什么"：唯一规范状态行 + 工具过程默认可见
- 一个语义 = 一个组件 = 一个符号 = 一个颜色角色
- 观感对齐 commandcode：彩色 chip（读取/编辑/Shell）、`::` 叙述行、`* 思考了 N 秒*` 折叠行、`⚒` 状态行、全中文案

**非目标**：
- 不重写渲染底座（卡片/diff/视口/动画机制保留）
- 不新增键位绑定（esc 维持关面板语义；中断就是 ctrl+c）
- 侧栏开关状态不做持久化（不动 config 管线）
- 7 套 hex 主题不逐个策展新角色（blendHex 自动派生；只策展 default ANSI）

## §1 目标界面（信息架构）

```
──────────────── deepseek-v4-flash · 16:40 ────────────────
 启动两个 agent 辩论 Dario 是不是 AGI 支持者
 * 思考了 7 秒 [[ctrl+O 展开]]                      ← 紫、思考折叠行
 :: 范围确定：先各自检索证据，再交叉质询。            ← 蓝、助手叙述行
[任务] 白金燐子 完成 2m29s · 4.3KB
   摘要：哼，全网证据本喵亲自挖好了……
[任务] 椎名立希 失败 2m20s
   错误：模型接口 402: Insufficient Balance         ← 错误只在失败卡内
[读取] cache-report.txt 25 行
[编辑] cleanup.go └ 已更新，5 行新增 1 行删除
   13 - │ 	old := true
   14 + │ 	new := true
 :: 缓存大头是 uv（10G），建议先清它。              ← 说话不回收 diff
 * 4 个工具 · 读取 a.go · 读取 b.go · … · 全部完成 [[ctrl+O 展开]]
[Shell] du -sh ~/.cache/uv ~/.npm
   └ 10G  ~/.cache/uv … 还有 9 行 [[ctrl+O 展开]]
──────────────────────────────────────────────────────────
 ⚒ 执行 Shell 中…  ctrl+c 中断 · 0m 12s · ↓ 1.2k tok   ← 唯一状态行
 上下文 19% ▒▒▒█████░░░░░░░░ 48.9k/256k               ← 唯一 CTX 读数
 › 输入你的问题 …           /help @文件 !shell
 聊天 · paw master · ctrl+G 活动面板                   ← 底栏无 CTX 重复
```

键位事实（已核实）：中断 = ctrl+c 两段式（app.go:864-879，第一次停工具、第二次停回合）；**不新增 esc 中断绑定**，状态行诚实显示 `ctrl+c 中断`。折叠展开 = ctrl+o；逐条检查 = ctrl+t（app.go:768，检查器内 enter 展开单条、ctrl+o 完整输出）。

## §2 组件层架构

```
theme.Palette ──Values()──▶ styles.go rebuildStyles ──▶ 组件层 ──▶ 各渲染点
 (+status.*, +chip.*, +accent, +narration)               chip.go / statusline.go
                                                          glyphs.go / copy.go
```

1. **theme.go 新增语义角色**：`StatusSuccess / StatusRunning / StatusWarning / StatusError`（顶替 styles.go:144-155 借 worktree 三色的做法）、`Accent`（⚒/思考行，ANSI=5）、`Narration`（`::` 行，ANSI=4）、chip 类别色（见 §5）。
2. **glyphs.go（新）图标注册表**：语义→字形唯一映射。成功 `✓`、失败 `✗`（收编 ×/✕）、取消 `■`、警告 `!`、活动 `●`、折叠 `▸`、展开 `▾`、状态行 `⚒`、输入 `›`、叙述 `::`、思考行 `*`。运行中不用字形，用文字。单测：注册表无重复字形；grep 渲染源码断言无裸字形字面量（glyphs.go 自身豁免）。
3. **chip.go（新，吸收 tool_display.go）**：`renderToolChip()` → `[读取] cache-report.txt`；`renderStatusText()` → `完成/运行中/失败` 彩色文字（实底药丸退役）。工具→(中文标签, 颜色角色) 映射表；MCP 工具兜底 `[插件]`。
4. **statusline.go（新）**：唯一工作状态源 `⚒ {动作}…  ctrl+c 中断 · {m:ss} · ↓{tok/s}`。
5. **copy.go（新）文案表**：用户可见文案集中一处。映射：CTX→上下文、Ask anything→输入你的问题、@file→@文件、chat/plan/goal→聊天/计划/目标、TOKEN HISTORY→令牌历史、Workspace→工作区、Activity/Tasks→活动/任务、Thought for→思考了、Worked for→工作了、出错→失败。

新文件全部平铺在 `internal/ui/bubble/`，不新开包。迁移方式：组件先落地，渲染点逐片切换，旧样式随迁移从 rebuildLegacyStyles 删除。

## §3 P0 折叠与可见性规则

回合结束后工作段 dissolve 为三类静态内容：

1. **思考**：完成后折成一行 `* 思考了 N 秒 [[ctrl+O 展开]]`；流式 live window（12 行）机制保留。
2. **工具卡片分两档**：
   - **永不折**：编辑类（Edit/Write）、Shell、失败卡、权限卡。Edit 卡 = chip 头 + `└ 已更新，5 行新增 1 行删除` + diff 本体，超 12 行截断 `… 还有 N 行 [[ctrl+O 展开]]`。
   - **可折**：只读成功卡（读取/搜索/网页）。连续 ≥6 个时中间折成组头 `* N 个工具 · 读取 a.go · … · 全部完成 [[ctrl+O 展开]]`，**组头自带文件清单**（最多 3 个 + `等 N 个`）；最近 2 个保持展开；组内出现编辑/Shell/失败立即断组，坏卡独立显示。
3. **说话即折叠规则废止**：`readyPendingToolSegmentsInCurrentTurn` 不再回收 Edit diff 与任何工具卡；助手 `::` 叙述与卡片共存。

**键位**：ctrl+o 从"只切 thinking"扩为**全局折叠总开关**（思考 + 工具组 + 截断输出一并展开/收起）；ctrl+t 逐条检查不变。

## §4 P1 状态行与侧栏

**状态行**（transcript 与上下文条之间）：
- 格式：`⚒ {动作}…  ctrl+c 中断 · {m:ss} · ↓{tok/s}`；`⚒` 静态字符（耗时/tok/s 数字跳动即活动指示）。
- 动作状态机：思考中 → 执行 [chip] 中 → 等待权限 → 恢复中；回合结束短暂 `完成 · {m:ss}`（吞掉 turn_timing.go 裸计时行）；**空闲整行不渲染**。
- tok/s 复用 token 面板速率数据源，取最新值。
- 退役清单：header `⠋ Ns working`（header.go:77-80）、CTX ripple、工具行 `◌`（改"运行中"文字）、死代码 renderEqualizer（连测试删除）。
- 时钟收敛：全屏两个钟——状态行回合计时 + header 挂钟。

**侧栏**：app.go:84 `visible: true → false` 默认关闭；ctrl+G 唤出不变；打开但无数据时 8 行 `—` 与"暂无流片段"不渲染，改单行 `暂无数据`；不做持久化。

## §5 P2 消重复 + 配色图标收口

**三处重复**：
1. 双标题：框线题只留 `活动`，内容区只留 `任务 N`（activity.go:24 改；header.go:129-140 dock 头同步）。
2. CTX 双读数：保上下文条（▒ 段=缓存，可视信息保留），删 footer `上下文 %d%% · 缓存占比 %d%%`（status_line.go:231-237）；**缓存占比数字退役**。
3. 错误双管道：失败错误只渲染在失败卡内，裸 error 行并入卡片。⚠️ 第二管道确切位置调查时存疑，实施切片 5 第一步先定位再合并。

**chip 五色系（同类同色）**：读取=blue(4)、编辑/写入=green(2)、Shell=magenta(5)、搜索/网页=cyan(6)、任务=yellow(3)、插件=gray(15)。

**Palette 新角色 ANSI 值**：StatusSuccess=2、StatusRunning=6、StatusWarning=3、StatusError=1、Accent=5、Narration=4。default 主题对比度策展：次要文字/注释从 15（亮白，亮底不可见）统一换 ANSI 8；状态词实底药丸改彩色文字。

**文案**：`出错` 统一改 `失败`；全量映射进 copy.go。

## §6 测试与切片

**测试四道闸**：golden 帧回归（复用 testdata 机制，每切片更新）；唯一性单测（glyphs 无重复 + 无裸字形 + copy 无英文残留，白名单 ctrl/help 等键位词）；主题兼容（7 套主题派生新角色非空）；行为测试（K=6/最近 2 展开/断组/ctrl+o 全局/侧栏默认关）+ 真实会话手动验收对照 §1。

**切片**（顺序即依赖关系，2→3→4 同碰 transcript.go 串行）：

| # | 切片 | 内容 |
|---|------|------|
| 1 | 组件层地基 | theme 新角色 + glyphs.go + copy.go + chip.go + statusline.go（纯新增） |
| 2 | 状态行上线 | statusline 接入布局；删 header working/ripple/◌/equalizer 死代码；时钟收敛 |
| 3 | 工具行 chip 化 | transcript 工具行/任务卡/状态词走 chip.go 与注册表 |
| 4 | 折叠新规 | 说话即折叠废止、K=6 组折+文件清单头、编辑/Shell/失败永不折、ctrl+o 全局化、工作段 dissolve 文案 |
| 5 | 消重复+文案 | 双标题、CTX footer 删、错误双管道合并、copy.go 全量替换 |
| 6 | 侧栏默认关 | app.go:84 + 空态单行 |

## 决策记录

- 配色方向：**A 策展 Default**（终端 ANSI 原生 + status 角色独立 + 对比度策展 + chip 减重）
- 对齐深度：**B commandcode 像素靠拢**（chip/`::`/`*思考了*`/⚒/全中文案）
- 范围：一个 spec 六章节，分 6 片实施；实施路径：组件层重构
- 中断键：**不新增 esc 绑定**，状态行诚实显示 `ctrl+c 中断`（用户拍板"ctrl+C 才是终端"）
- 阈值：K=6 组折 + 最近 2 个只读卡保持展开；Shell 归动作类永不折
- ctrl+o 扩为全局折叠总开关
- 空闲状态行不渲染；⚒ 静态；侧栏不持久化
- 缓存占比数字退役（▒ 段保留）；chip 五色系；出错→失败
