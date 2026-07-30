# Ghostty IME 光标与主题背景修复设计

**日期：** 2026-07-30  
**状态：** 已批准，待实现计划  
**目标环境：** macOS、Ghostty、Apple 原生中文输入法

## 1. 背景

当前 TUI 在 Ghostty 中使用 Apple 原生中文输入法时存在两个相关的输入渲染问题：

1. 当输入框中只有 `/` 并触发命令候选框，随后进入中文输入法预编辑时，输入区域可能出现终端默认黑色背景，而不是当前主题背景。黑色背景在视觉上割裂输入 dock，任何时候都不应出现。
2. 输入拼音但尚未完成选词时，预编辑字母显示在应用绘制的光标后方；应用光标不会随预编辑文本前进。期望采用原生 IME 行为：预编辑文本显示在光标前，光标位于预编辑文本末尾。
3. 曾观察到应用主题后光标颜色渐变消失，但该现象目前无法稳定复现。本项降为观察项：本次不单独重写渐变算法，但修复不得破坏现有颜色和渐变动画。

## 2. 目标与非目标

### 2.1 目标

- 输入 dock 在普通输入、`/` 补全、IME 预编辑、主题切换和窗口缩放期间始终保持当前主题背景，不出现黑色区域或背景跳变。
- 屏幕上只存在一个可见输入光标。
- 真实终端光标成为 Apple IME 的原生锚点，并由 Ghostty 随未提交的预编辑文本推进。
- 普通输入和终端输入继续使用各主题定义的光标颜色端点。
- 保留现有 3 秒周期、30 FPS、`cursorIntensityAt()` 曲线和颜色插值行为。
- 主题切换后，背景和光标动画立即使用新主题 palette。
- 补全框打开、关闭、布局变化、多行输入和已提交中文文本均不得造成双光标、残影或错误坐标。
- 程序退出或输入不再激活时，恢复终端默认光标颜色、可见性和 SGR 状态。

### 2.2 非目标

- 不修改 Apple 输入法或 Ghostty 配置。
- 不尝试获取或绘制 IME 未提交的预编辑文本。
- 不猜测 IME composition 长度，也不引入无法由 TTY 输入流可靠维护的 `imeComposing` 状态。
- 不修改 `/`、`@`、`$` 补全的触发、过滤或选择语义。
- 不修改 textarea 的文本编辑、换行、token、粘贴折叠或提交语义。
- 不修改 `cursorIntensityAt()` 的动画曲线和周期。
- 不增加“关闭光标动画”作为规避开关。
- 不进行与本问题无关的 UI 或主题重构。

## 3. 原因定位

### 3.1 黑色背景

当前帧渲染和真实光标锚定分属两个阶段：

1. `appModel.View()` 使用当前主题绘制 Bubble Tea 帧。
2. 帧尾 ANSI reset 清除当前 SGR 背景。
3. `anchoredOutput.Write()` 在帧写完后，通过 `moveTerminalCursorToAnchor()` 把真实终端光标移动至输入框逻辑位置。
4. 该定位序列只包含 `\r` 和 CSI 上移/右移，不会重新建立当前主题背景。
5. Apple 输入法的预编辑内容由 Ghostty 在真实终端光标处直接绘制，不经过 `textarea.View()`。
6. 因此预编辑内容继承 ANSI reset 后的终端默认背景，在目标环境中表现为黑色。

现有 `restoreBackgroundAfterANSIReset()` 只修复 Bubble Tea 帧内部嵌套样式 reset 后的画布背景，未覆盖帧写入完成后的真实光标锚定阶段。

### 3.2 预编辑字母位于光标后

当前同时存在两个光标概念：

- textarea 在帧内容中绘制的彩色“软件光标”；
- Ghostty 用来定位 Apple IME 的真实终端光标。

未完成选词的拼音不会作为普通 `tea.KeyMsg` 提交给应用，因此 textarea 不知道预编辑文本长度，其软件光标停留在 composition 起点。Ghostty 的真实终端光标会随预编辑文本原生前进，但软件光标仍留在原位置，于是用户看到类似：

```text
| nihao
```

正确行为需要真实终端光标成为唯一可见光标：

```text
nihao |
```

### 3.3 动画与位置耦合风险

`cursorFrameMsg` 以约 30 FPS 驱动渐变。如果每个动画 tick 都重新锚定真实终端光标，Ghostty 在 IME 预编辑期间推进的光标会每 33ms 被拉回 textarea 的逻辑位置，造成候选框跳动、拼音位置错误或残影。

因此，位置锚定与颜色/可见性动画必须严格解耦。

## 4. 选定方案

采用“单一真实终端光标”方案：

- textarea 继续维护文本、逻辑光标、编辑状态和可见投影，但不再绘制可见的软件光标。
- 真实终端光标成为输入区域唯一的可见光标和 IME 锚点。
- `View()` 发布准确的输入逻辑坐标和主题背景。
- `applyCursorAnimation()` 发布当前动画帧的光标颜色与可见性。
- 输出控制层串行应用位置、背景、颜色和可见性。
- 纯动画更新只改变真实光标颜色或显示状态，不包含任何定位序列。

此方案同时满足原生 IME 行为、主题背景连续性以及现有光标渐变保留要求。

## 5. 架构设计

### 5.1 职责边界

#### `appModel`

负责：

- textarea 和 token 输入的逻辑状态；
- 当前 input dock 布局；
- 当前主题；
- 当前动画强度；
- 计算真实终端光标应使用的位置、背景、颜色和可见性。

不负责：

- 绘制未提交的 IME 预编辑内容；
- 直接向 `os.Stdout` 写 OSC/ANSI；
- 根据输入法或键盘布局猜测 composition 状态。

#### 终端光标控制器

负责保存并比较两类状态：

```go
type terminalCursorPosition struct {
    active       bool
    upFromBottom int
    column       int
    background   string
}

type terminalCursorVisual struct {
    color   string
    visible bool
}
```

位置状态和视觉状态必须分离。控制器需要能够识别：

- 坐标变化；
- 背景变化；
- active 状态变化；
- 仅颜色或可见性变化；
- 完全无变化。

#### `anchoredOutput`

负责所有真实终端写入的串行化：

- Bubble Tea 帧；
- 帧前光标隐藏和位置不变量恢复；
- 帧后 anchor 定位；
- 主题背景恢复；
- OSC 光标颜色更新；
- 光标 show/hide；
- Close 或退出恢复。

所有写入必须共用同一把输出锁，避免 OSC/ANSI 序列插入 UTF-8 文本或 Bubble Tea 控制序列中间。

### 5.2 单一可见光标

textarea 仍使用原始逻辑 cursor 计算位置，但用于 `View()` 的渲染副本不得输出可见光标单元格。

不能仅把软件光标颜色设置成背景色，因为 cursor renderer 可能覆盖光标下的字符，也可能在主题切换或低亮度阶段产生残影。实现应优先使用 bubbles cursor/textarea 的公开隐藏能力；若公开 API 无法安全隐藏，则在 textarea 渲染副本中禁用 cursor 渲染，同时保留原始 `m.input` 的逻辑 cursor 状态。

任何时候屏幕上只能有一个可见输入光标，即真实终端光标。

### 5.3 坐标来源

真实终端光标坐标统一由：

```go
func (m appModel) inputCursorTerminalPosition(layout tuiLayout) terminalCursorPosition
```

计算。该函数必须继续复用现有输入投影逻辑：

- 普通 textarea：`visibleTextareaCursorRow/Column`；
- token 输入：`inputTokenProjection().cursorRow/cursorColumn`；
- 粘贴折叠：现有 folded cursor row/column 逻辑；
- 多行输入：textarea 可见行偏移；
- 中文、emoji 和组合 grapheme：terminal cell 宽度，而非 UTF-8 字节数。

最终位置必须计入 frame、header、transcript、status、input dock 和 padding。`anchoredOutput` 不得再次推导或猜测布局。

### 5.4 帧后激活顺序

当 anchor 需要重新应用时，帧后顺序必须为：

1. 隐藏真实终端光标；
2. 从帧底部移动至当前输入逻辑坐标；
3. 发出当前主题背景 SGR；
4. 发出当前动画帧的终端光标颜色控制序列；
5. 根据动画状态显示或保持隐藏真实光标。

背景必须来自当前 `appModel.theme.Colors.TerminalBackground`，不得硬编码、读取旧缓存或临时依赖包级 `colorManager`。

### 5.5 帧前恢复顺序

若上一帧已把真实光标移至输入区，下一次 Bubble Tea 帧写入前必须：

1. 隐藏真实终端光标，避免移动和重绘闪烁；
2. 恢复到 Bubble Tea 预期的帧底部行首不变量；
3. 写入完整新帧；
4. 根据最新状态重新激活 anchor。

### 5.6 颜色动画通道

现有动画逻辑保持：

```go
intensity := cursorIntensityAt(cursorCycleOffset(m.cursorFrameAt))
color := m.styles.Colors.CursorColor(
    intensity,
    m.isTerminalInputActive(),
)
visible := intensity > cursorHiddenThreshold
```

普通输入继续使用 `CursorNormalBright`，终端输入继续使用 `CursorTerminalBright`。低亮度端继续插值至当前主题 `TerminalBackground`。

颜色更新使用 Ghostty 支持的终端光标颜色控制协议：

```text
OSC 12 ; #rrggbb ST
```

恢复终端默认光标颜色使用 OSC 112。

纯视觉更新允许改变：

- OSC 12 光标颜色；
- CSI show/hide cursor。

纯视觉更新禁止包含：

- `\r`；
- CSI cursor up/down/forward/back；
- erase line/screen；
- SGR reset；
- 重新设置 textarea 逻辑坐标；
- 重新激活主题背景。

这使 Ghostty 在 IME 预编辑时可以继续原生推进真实光标，而 30 FPS 动画不会把位置拉回 composition 起点。

### 5.7 状态转移规则

| 变化类型 | 输出行为 |
|---|---|
| 只有颜色变化 | 只输出 OSC 12；不定位 |
| 只有可见性变化 | 只输出 show/hide；不定位 |
| 坐标变化 | 隐藏、定位、恢复背景、设置颜色、按状态显示 |
| 背景变化 | 在当前逻辑 anchor 上执行完整重新激活 |
| inactive → active | 完整定位并激活 |
| active → inactive | 显示光标、OSC 112、SGR reset |
| 完全无变化 | 不输出控制序列 |

相同颜色应去重，避免无意义的高频 OSC 写入。可以对最终十六进制颜色去重，但不得改变逻辑动画周期、曲线或帧率。

### 5.8 `/` 补全不变量

completion 当前渲染在 transcript 浮层，input dock 固定在底部。`/` completion 打开或关闭时：

- 输入逻辑列仍位于 `/` 后；
- 正常尺寸下 input dock 的 anchor 不应因浮层出现而漂移；
- 小窗口允许布局重新分配，但重新计算后的 anchor 必须位于当前 input dock 内；
- completion 的 selected/unselected ANSI reset 不得泄漏并清除 input dock 背景；
- completion 开关后应发布最新 anchor，不能复用旧布局快照。

### 5.9 终端能力降级

本设计不查询终端响应，避免异步查询污染输入流。

若其他终端忽略 OSC 12：

- 文本输入和真实光标位置仍必须正确；
- IME 锚点和主题背景仍必须正确；
- 光标可降级为终端默认颜色；
- 控制序列不得作为可见文本泄漏。

在目标环境 Ghostty 中，颜色和渐变属于强制验收项，不允许以“终端可能不支持”为理由接受渐变丢失。

## 6. 生命周期与错误恢复

以下状态不显示真实输入光标：

- 应用尚未 ready；
- terminal command 正在独占运行；
- tool inspect 打开；
- theme/model/settings/session/subagent picker 打开；
- 输入区不应接受焦点；
- 程序正在退出。

正常退出、双击 `Ctrl+C`、Bubble Tea `Run()` 返回错误、`anchoredOutput.Close()` 和上层 deferred cleanup 均必须恢复：

1. 显示真实终端光标；
2. OSC 112 恢复终端默认光标颜色；
3. SGR reset；
4. 不保留错误坐标或主题背景状态。

恢复操作必须幂等。重复恢复不得隐藏光标、重新应用旧主题色或移动到错误位置。

颜色验证必须拒绝非法值。非法或空颜色不得通过 `parseHexColor()` 静默变为 `0,0,0` 并生成黑色；应跳过对应颜色控制或使用明确的安全恢复行为。

## 7. 三阶段交付

### Phase 1：终端光标状态模型和 ANSI/OSC 编码

#### 目标

建立可独立测试的位置状态、视觉状态和控制序列编码，不立即切换生产渲染路径。

#### 主要工作

- 在 `internal/ui/bubble/anchor.go` 中拆分 position 与 visual 状态。
- 增加背景 SGR、OSC 12、OSC 112、show/hide、激活和恢复编码函数。
- 增加状态差异分类与去重逻辑。
- 对颜色输入做严格验证。
- 保持当前 TUI 生产行为不变。

#### 完成条件

- 所有编码函数有确定性单元测试；
- visual-only 序列可机械证明不包含定位指令；
- 非法颜色不会生成隐式黑色；
- 单独 commit，便于审查和回滚。

### Phase 2：切换单一真实光标并修复 IME 背景与位置

#### 目标

让真实终端光标成为唯一可见输入光标，并保证 Ghostty 原生推进 Apple IME 预编辑光标。

#### 主要工作

- 使 textarea 渲染副本不输出可见软件光标；
- 从原始 textarea/token/folded projection 读取逻辑位置；
- 将当前主题背景发布到 anchor；
- 将动画颜色和可见性发布到 visual 通道；
- 在 `anchoredOutput` 中串行应用帧、位置、背景、颜色和显示状态；
- 确保纯动画更新不调用位置恢复或定位函数；
- 保持 `/`、`@`、`$` 补全语义不变。

#### 完成条件

- 自动化位置与输出状态测试通过；
- Ghostty + Apple 原生中文输入法人工测试中无黑色背景、双光标、位置回跳和残影；
- 光标颜色与渐变保持生效。

### Phase 3：主题、生命周期与全量回归加固

#### 目标

覆盖主题切换、modal、终端模式、窗口缩放和退出恢复，防止修复只在静态场景有效。

#### 主要工作

- 验证 `applyTheme()` 后下一安全输出使用新背景和新光标颜色；
- 为 active/inactive、Close、正常退出和错误退出增加幂等恢复；
- 增加动画观察项回归测试；
- 验证全部内置主题；
- 运行 bubble、theme 和全仓测试与 vet；
- 完成目标环境人工验收矩阵。

#### 完成条件

- 全量自动化验证通过；
- 全部内置主题至少验证一次背景和渐变；
- 退出后 Ghostty shell 光标颜色与可见性恢复；
- 无需用户修改 Ghostty 配置。

## 8. 测试策略

### 8.1 Phase 1 单元测试

建议新增 `internal/ui/bubble/anchor_test.go`，避免继续扩大 `bubble_test.go`：

- 主题背景正确编码为 SGR true color；
- 光标颜色正确编码为 OSC 12；
- 恢复序列包含 OSC 112、显示光标和 SGR reset；
- visual-only 序列不包含 `\r`、cursor movement 或 erase；
- 非法颜色不生成黑色 SGR/OSC；
- 普通和终端输入可以输出不同颜色；
- 相同 anchor、颜色变化被分类为 visual-only；
- 背景变化被分类为完整 anchor 激活。

### 8.2 坐标测试

覆盖：

- 空输入；
- 单行 ASCII；
- 已提交中文；
- emoji 和宽 grapheme；
- 光标位于文本中间；
- 多行输入；
- textarea 可见区域滚动；
- token 输入；
- 粘贴折叠；
- `/` completion 打开与关闭；
- window resize；
- modal 打开时 anchor inactive。

### 8.3 输出状态测试

使用可记录输出的 writer 或可注入输出接口模拟：

```text
写 Bubble Tea 帧
→ 应用 anchor
→ 仅更新颜色
→ 写下一帧
→ 关闭或恢复
```

断言：

- 帧末 anchor 激活后背景为当前主题色；
- visual-only 更新不包含定位；
- 相同 anchor 不重复移动；
- theme switch 后输出新背景和新光标颜色；
- inactive 后显示光标并恢复默认颜色；
- input view 不包含第二个软件光标颜色片段；
- 所有终端写入保持完整、有序且不可交错。

### 8.4 补全回归测试

构造输入值 `/` 并激活 command completion：

- 输入 dock 始终使用当前主题背景；
- 打开 completion 不生成终端默认黑色背景；
- completion 开关前后逻辑列保持在 `/` 后；
- 正常尺寸下 anchor 坐标稳定；
- 小窗口重新布局后 anchor 仍位于 input dock 内。

### 8.5 渐变观察项测试

- `cursorIntensityAt()` 保持现有曲线结果；
- 一个周期内生成多个不同颜色；
- 低亮度端趋近当前主题背景；
- 高亮度端达到 `CursorNormalBright` 或 `CursorTerminalBright`；
- theme switch 后下一动画帧使用新 palette；
- 连续 visual-only 更新包含颜色变化但不含定位；
- 颜色更新不会 reset 或清除输入背景。

### 8.6 自动化命令

至少运行：

```bash
go test ./internal/ui/bubble -count=1
go test ./internal/theme -count=1
go test ./... -count=1
go vet ./...
```

## 9. Ghostty 人工验收矩阵

目标环境：macOS、Ghostty、Apple 原生中文输入法。

1. 启动默认主题。
2. 输入 `/`，保持 command completion 打开。
3. 切换中文输入法。
4. 输入 `nihao`，不选词，等待至少两个完整的 3 秒动画周期。
5. 验证：
   - 输入 dock 无黑色背景；
   - 屏幕只有一个输入光标；
   - 光标位于 `nihao` 后；
   - 光标渐变持续；
   - 动画 tick 不把光标拉回起点；
   - Apple 候选框位置稳定；
   - 无预编辑残影。
6. 选词为“你好”，验证中文提交后真实光标与 textarea 逻辑位置一致。
7. 将逻辑光标移至文本中间，再次输入拼音并验证。
8. 打开和关闭 `/` completion 并验证背景和坐标。
9. resize Ghostty 窗口并验证重新布局。
10. 依次切换所有内置主题并重复背景、位置和渐变检查。
11. 测试普通输入与终端输入颜色端点。
12. 正常退出和双击 `Ctrl+C`，确认 shell 光标可见且恢复终端默认颜色。

## 10. 验收标准

以下要求全部满足才可完成：

- 输入区域在任何候选框、IME 和主题状态下都不出现黑色背景。
- 真实终端光标是唯一可见输入光标。
- IME 预编辑字母位于光标前，光标位于预编辑文本末尾。
- 30 FPS 动画不会周期性重置真实光标位置。
- 普通输入与终端输入保留各自主题光标颜色。
- 现有渐变周期、曲线和视觉效果不丢失。
- 主题切换后背景和光标颜色立即使用新 palette，不闪现旧主题或默认黑色。
- `/` completion、多行、token、折叠、中文、emoji 和 resize 后坐标准确。
- 无双光标、错误位置、候选框跳动和残影。
- 正常或异常退出后终端光标状态恢复。
- 自动化测试、`go vet` 和 Ghostty 人工验收矩阵通过。

以下任一现象均视为验收失败：

- 出现第二个软件光标；
- 拼音显示在可见光标后；
- 动画 tick 将光标拉回 composition 起点；
- 输入区域出现黑色背景或主题断层；
- 应用主题后渐变消失；
- 退出后 Ghostty 光标仍隐藏、保留自定义颜色或保留输入背景。

## 11. 设计结论

本次修复以如下不变量为核心：

> textarea 维护逻辑光标；真实终端光标承担唯一可见光标和 IME 锚点；位置更新与颜色动画严格解耦；输出层在真实光标位置恢复当前主题背景，并通过 Ghostty 支持的终端光标颜色控制序列保留现有渐变。

该设计在不修改输入法、补全语义和动画曲线的前提下，从光标所有权和终端状态边界上同时解决背景割裂与 IME 光标位置错误。
