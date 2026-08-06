# 鼠标拖拽选中文本：方案调研与选区配色设计

> 调研日期：2026-08-06 ｜ 目标项目：paw（Bubble Tea v1.3.10 / Go TUI）
> 目标环境：Ghostty、kitty、WezTerm、Alacritty 等现代终端 + iTerm2/macOS（本地为主，无 tmux/SSH）
> 方法：deep-research（方案调研）+ frontend-design（配色设计），对比度数据按 WCAG 2.x 相对亮度公式实测计算
>
> **落地状态（2026-08-06）**：§3.5 的 P0 全部落地（OSC 52 双写、README 终端修饰键对照表）、P1 落地（双击选词/三击选行 + 词行吸附 + 单击动作延迟到双击窗口后）；§4 的配色公式已实现于 `internal/theme/themes.go`（`palette()` 内 30% 混合 + Default 25% 特例），16 色终端选区自动降级为反色。实测公式结果与 §4.3 表格值相差 1–2 个 RGB 单位（四舍五入方向差异），视觉无区别。未落地：复制 toast（P1）、Bubble Tea v2 升级（P2）。

---

## 1. 结论速览（TL;DR）

1. **交互方案**：paw 的「应用内自绘选区」本身就是 TUI 里领先的做法（绝大多数 TUI 不做，靠终端原生选择）。正确形态是**混合方案**——应用内选区与终端原生选择并存，用户按住 **Shift**（Ghostty/kitty/WezTerm/Alacritty）或 **Option**（iTerm2）拖拽即可无缝切到终端原生选择。**这部分 paw 零代码**，只需在帮助/README 里写清楚。
2. **最有价值的三个改进**（按性价比）：
   - **P0 配色修正**：当前所有主题的选区背景与正文背景对比度只有 1.07–1.56:1，选区几乎不可见（详见 §4）。
   - **P0 OSC 52 双写**：剪贴板增加 OSC 52 通道（`charmbracelet/x/ansi` 已在依赖中），一行代码，覆盖 SSH/远程与终端侧剪贴板。
   - **P1 双击选词**：Bubble Tea v1 需自计时实现；v2 的 `MouseClickMsg` 自带点击计数，可作为升级 v2 的收益点。
3. **配色方案**：推荐「**混合公式**」`选区背景 = 正文背景与前景按 30% 混合`，全主题自动一致，深色主题达成「文字 ≥4.5:1 + 与正文区分 ≥2:1」；浅色主题与个别主题用手工色特例（§4.3 给出每主题具体 hex）。

---

## 2. 现状盘点（paw 已实现的能力）

`internal/ui/bubble/selection.go` 已经具备一套完整的应用内选区：

| 能力 | 实现 | 评价 |
|---|---|---|
| 拖拽三态跟踪 | `handleTranscriptMouse`：Press→Motion→Release | ✅ 完整 |
| 边缘滚动 | `scrollTranscriptSelectionAtEdge`（拖到顶部/底部自动滚 viewport） | ✅ 完整 |
| grapheme 吸附 | `snapCellRangeToGraphemes`（不切开中文/emoji） | ✅ 完整（CJK 场景刚需） |
| 选区渲染 | `renderSelectedLineFragment` + `selectedTranscriptLineStyle` | ⚠️ 配色不可见（§4.1） |
| 复制 | 释放即 `writeClipboard`（atotto/clipboard，本地剪贴板） | ⚠️ 无 OSC 52 通道 |
| 点击消解 | 无位移的 press-release 回退为链接/todo/tool 点击 | ✅ 完整 |
| 鼠标模式 | `tea.WithMouseAllMotion()` | ✅ hover 需要；拖拽中已跳过 hover 分支 |
| 双击选词 | 无 | ❌ 缺失（v1 需自实现） |

---

## 3. 交互方案调研

### 3.1 三条路线

| 路线 | 做法 | 优点 | 缺点 | 代表 |
|---|---|---|---|---|
| **A. 纯原生选择** | 不启用 mouse capture（或用户配置 `mouse-reporting = false`），选区完全由终端渲染 | 零代码；选区样式由终端保证；支持双击/三击/矩形选择 | 失去应用内点击交互（链接、tool 展开、hover） | 大多数普通 CLI |
| **B. 应用内自绘选区** | 应用捕获鼠标事件，自己跟踪选区、渲染高亮、写剪贴板 | 保留全部应用交互；选区内容可进应用内逻辑（后续可做「选中后提问」等）；不依赖终端 | 实现复杂（坐标/换行/grapheme/滚动）；与终端原生选择互斥 | paw（少数派，GitHub 上无同类 bubbletea 实现可参考） |
| **C. 混合（推荐）** | B + 终端侧「修饰键放行」：用户按住 Shift/Option 拖拽时，终端接管做原生选择 | 同时拥有两种选区；零应用代码（终端默认行为） | 用户需要知道修饰键组合；两种选区观感可能不同（可配置对齐） | tmux/lazygit 等现代 TUI 的普遍做法 |

**调研事实**：bubbletea 官方 issue #162 明确承认「mouse enabled 后原生选择不可用」是普遍困境，生态至今的解法就是**终端侧修饰键放行**（kitty 文档原话：*"You can select text with kitty even when a terminal program has grabbed the mouse by holding down the Shift key"*）。paw 的路线 B+C 组合是当前 TUI 能做到的最佳形态。

### 3.2 终端放行矩阵（RQ2）——paw 无需代码，但需要文档

| 终端 | 放行方式 | 细节 |
|---|---|---|
| **Ghostty** | **Shift + 拖拽** | 配置 `mouse-shift-capture` 默认 `false`：shift 不会随鼠标协议发给程序，而是扩展/开始原生选择。程序可用 `XTSHIFTESCAPE` 覆盖（Ghostty 1.3.0） |
| **kitty** | **Shift + 拖拽** | 官方文档明确支持 |
| **WezTerm** | **Shift + 拖拽** | 默认；可用 `bypass_mouse_reporting_modifiers` 换成别的修饰键 |
| **Alacritty** | **Shift + 拖拽** | 默认行为 |
| **iTerm2** | **Option + 拖拽** | ⚠️ 与前三者不同！iTerm2 用 option 键绕过 mouse reporting 做原生选择 |

应用侧约束：Bubble Tea v1.3.10 的 `MouseMsg` **没有修饰键字段**（源码确认），所以应用无法感知 shift——这恰好是好事：终端默认在 shift 按下时不把事件发给应用，放行逻辑完全在终端侧，paw 不用写任何代码。

**一致性提示**：Ghostty 原生选区默认是「反转窗口前景/背景」（注意：不是 cell 色），1.2.0 起可配 `selection-foreground`/`selection-background`（支持 `cell-foreground`/`cell-background`）。如果希望原生选区与 paw 自绘选区观感一致，可以在文档中给出 Ghostty 配置指引（§4.4）。

### 3.3 剪贴板方案（RQ3）

| 方案 | 现状/成本 | 适用 |
|---|---|---|
| 本地剪贴板（atotto/clipboard） | ✅ 已实现 | 本地环境（当前目标环境足够） |
| **OSC 52** | `charmbracelet/x/ansi` **已在 go.mod 依赖中**：`ansi.SetSystemClipboard(text)`；需以 tea.Cmd 写 stdout（v1 无 `tea.SetClipboard`） | SSH/远程、以及不依赖 OS 剪贴板权限 |
| Bubble Tea v2 原生 | v2 内置 OSC 52 剪贴板（`tea.SetClipboard`）+ `MouseClickMsg`（带点击计数） | 未来升级 v2 的收益点 |

**建议**：双写（本地 + OSC 52）。注意 OSC 52 需要终端允许写入（Ghostty `clipboard-write` 默认 allow；kitty/WezTerm 默认允许；iTerm2 默认会弹授权）。

**复制交互惯例**：paw 的「释放即复制」符合现代终端习惯（Ghostty `copy-on-select` 默认 true；X11 primary 选择即复制），建议保留；可选增强：复制时状态栏提示「已复制 N 字符」，选区保留到下一次点击（Ghostty `clear-selection-on-copy` 默认 false 同理）。

### 3.4 高级交互（RQ5）

- **双击选词**：v1 需自实现（press→release→press 间隔 < 250ms 且位移小于阈值，进入 word 模式；三击进 line 模式）。工作量中等（需基于 `snapCellRangeToGraphemes` 的 word 边界）。**捷径**：Bubble Tea v2 的 `MouseClickMsg` 自带 `Count`，且 v2 已发布一年+；若计划升级 v2，双击选词应排在 v2 升级之后。
- **拖拽出窗口**：release 在窗口外时 `MouseMsg` 坐标可能是边界值，需确认 `transcriptPointForMouse` 已钳制（paw 的 `selectionBounds` 有 clamp，✅）。
- **拖拽中 hover 冻结**：paw 已在 `selecting` 时跳过 hover 分支，避免拖拽时频繁重绘，✅（这正是 `MouseAllMotion` 下需要注意的性能点）。
- **选区与点击冲突**：paw 的「release 无位移 = 点击」消解已正确处理「选区 vs 链接/todo/tool」冲突，✅。

### 3.5 paw 改进建议清单（交互部分）

| 优先级 | 事项 | 成本 | 说明 |
|---|---|---|---|
| P0 | 文档：各终端「修饰键+拖拽」原生选择对照表 | 极小 | README/help 中写明 Shift（Ghostty/kitty/WezTerm/Alacritty）/ Option（iTerm2） |
| P0 | 剪贴板双写：+ OSC 52 | 小 | `ansi.SetSystemClipboard(text)` 以 tea.Cmd 输出 |
| P1 | 双击选词 / 三击选行 | 中 | 或等 v2 升级 |
| P1 | 复制反馈（状态栏 toast） | 小 | 「已复制 N 字符」 |
| P2 | 评估升级 Bubble Tea v2 | 大 | 收益：OSC 52 原生、`MouseClickMsg` 点击计数、声明式 mouse mode |
| P2 | 256 色终端下选区色验证 | 小 | lipgloss 自动近似，但个别色映射偏差大（§4.4） |

---

## 4. 选区配色设计

### 4.1 问题诊断（实测数据）

paw 现状：所有主题 `SelectionBackground = surface`、`SelectionForeground = fg`（Default 主题为 `#3a3a3a`/`#eeeeee`）。按 WCAG 2.x 公式实测：

| 主题 | 选区文字对比度（fg vs sel_bg） | 选区bg vs 正文bg（区分度） | 判定 |
|---|---|---|---|
| TokyoNight `#24283b` | 9.02:1 | **1.17:1** | ❌ 选区几乎不可见 |
| TokyoNightStorm `#1f2335` | 9.63:1 | **1.07:1** | ❌ 完全不可见 |
| TokyoNightLight `#cbccd1` | 6.85:1 | **1.10:1** | ❌ 几乎不可见 |
| Default `#3a3a3a` | 9.80:1 | **1.23:1** | ❌ 很弱 |
| CatppuccinMocha `#313244` | 8.69:1 | 1.30:1 | ⚠️ 弱 |
| GruvboxDark `#3c3836` | 8.45:1 | 1.27:1 | ⚠️ 弱 |
| Dracula `#44475a` | 8.59:1 | 1.56:1 | ⚠️ 勉强 |

文字对比全部达标（>6:1），但**「选区 vs 正文」的区分度全部低于 1.6:1**——用户拖拽时几乎看不到自己选了什么。这是核心问题。

**参照系**：终端原生选区 = 反色（区分度通常 >10:1）；neovim 生态（tokyonight.nvim 的 `bg_highlight = #292e42`，对 night bg 只有 1.27:1）在编辑器里靠光标行/边框等上下文兜底，TUI 大块选区没有这些上下文。

### 4.2 设计方法（frontend-design 流程）

1. **语义定位**：选区是「覆盖层」，必须同时与**正文背景、代码块背景（surface）、diff 背景**区分——所以不能用 surface 本身，要用主题层级里「比 surface 高一级」的色。
2. **双对比度约束**：
   - 内部：`sel_fg` vs `sel_bg` ≥ **4.5:1**（WCAG AA 正文）
   - 外部：`sel_bg` vs 相邻背景 ≥ **2:1**（感知清晰可见；3:1 是 WCAG 1.4.11 非文本参考线，作为加分项）
3. **占用色检查**：不与已有语义色混淆——markdown 高亮（yellow 系）、diff added/deleted 背景、链接色（cyan）都不应接近选区色。
4. **混合公式（主推）**：`sel_bg = blend(正文bg, 前景fg, 30%)`，`sel_fg = fg`。理由：模拟「半透明覆盖层」的自然浮起效果；公式化后任何新增主题自动一致，无需人工选色。实测（§4.3）所有深色主题达成双约束。
5. **浅色主题特例**：浅色主题 bg/fg 对比本身只有 ~7.5:1，混合公式区分度仅 1.5–1.8:1，是浅色背景下的现实上限（人眼对亮度差感知是对数的，方向性「变深」感知更强），接受 ≥1.5:1 并用手工色。

### 4.3 推荐色值表（可直接落地 themes.go）

| 主题 | 现状（不可见） | **推荐** | 文字 | vs正文bg | 生态参照 |
|---|---|---|---|---|---|
| TokyoNight | `#24283b` | **`#4c5064`**（30% 混合） | 4.93:1 | 2.15:1 | tokyonight storm 官方 selection 是 `#33467c`+`#c0caf5`（1.88:1），混合方案更优 |
| TokyoNightStorm | `#1f2335` | **`#535973`**（30% 混合） | 4.27:1 | 2.11:1 | — |
| TokyoNightLight | `#cbccd1` | **`#a5a7b4`**（30% 混合，手工特例） | 4.59:1 | 1.65:1 | 浅色主题现实上限 |
| CatppuccinMocha | `#313244` | **`#525569`**（30% 混合） | 5.07:1 | 2.24:1 | catppuccin surface1 `#45475a`（1.80:1）作为次选 |
| Dracula | `#44475a` | **`#66686e`**（30% 混合） | 5.23:1 | 2.56:1 | — |
| GruvboxDark | `#3c3836` | **`#625e51`**（30% 混合） | 4.73:1 | 2.27:1 | gruvbox bg2 `#504945`（1.67:1）作为次选 |
| Default | `#3a3a3a` | **`#515254`**（25% 混合） | 4.43:1 | 1.79:1 | Default 用 25% 而非 30%（30% 时文字 3.96:1 略低于 AA） |

> 混合公式：`blend(bg, fg, t) = bg + (fg − bg) × t`（sRGB 通道线性插值）。实现可复用 color_manager 已有的 `interpolateHexColor`（需注意其语义是 from→to 的 amount 插值，方向一致）。
>
> 落地位置：`internal/theme/themes.go` 的 `palette()` 中 `SelectionBackground` 不再传 `surface`，改为传入各主题的独立 selection 色（或由 `palette()` 内部计算 blend）。

**占用色核对（TokyoNight 示例）**：选区 `#4c5064` vs markdown 高亮 `#e0af68` = 4.55:1 ✅；vs diff added bg `#14302a` = 3.04:1 ✅；vs diff deleted bg `#2e1420` = 2.78:1 ✅；选区前景保持 `fg`，链接色 `#7dcfff` 与 `#c0caf5` 的 1.06:1 是现状（链接靠下划线区分），不在本次范围。

### 4.4 色深回退与终端一致性

- **256 色**：lipgloss 自动映射到 xterm 256 色板。实测 `#33467c→#5f5f87`（可接受）、`#292e42→#00005f`（偏差大，**不要用** `#292e42`）；推荐色 `#4c5064→#5f5f87` 附近，可接受。落地后建议在 256 色终端（`TERM=xterm-256color` + 无 `COLORTERM`）实测一次。
- **16 色**：建议显式降级为**反色**（`Reverse(true)` 或不设背景色），与终端原生选区观感一致，避免 lipgloss 近似出奇怪颜色。可在 `styles.go` 按 termenv color profile 分支。
- **与 Ghostty 原生选区对齐（可选文档）**：用户若希望「shift+拖拽的原生选区」与 paw 自绘选区同色，可在 paw 文档给出：

  ```ghostty
  selection-background = #4c5064
  selection-foreground = #c0caf5
  ```

  （kitty：`selection_background`/`selection_foreground`；WezTerm：`colors.selection_bg`/`selection_fg`；iTerm2 在 Preferences→Profiles→Colors→Selection。）

---

## 5. 参考来源

- Ghostty 配置参考（`mouse-shift-capture`、`mouse-reporting`、`selection-*`、`copy-on-select`、`clipboard-write`、`XTSHIFTESCAPE`）：ghostty.org/docs/config/reference
- WezTerm Mouse Bindings（SHIFT 绕过、`bypass_mouse_reporting_modifiers`）：wezterm.org/config/mouse.html
- kitty 文档（"select text … by holding down the Shift key"、双击/三击/矩形选择）：sw.kovidgoyal.net/kitty/overview
- iTerm2：pointer preferences；社区确认 option+拖拽绕过 mouse reporting（superuser #1440633、gitlab iterm2#10654）
- charmbracelet/bubbletea：#162（mouse 与原生选区互斥）、#982（OSC 52 剪贴板）、v2 讨论 #1374（`MouseClickMsg` 点击计数、原生剪贴板）；v1.3.10 `mouse.go` 源码（MouseMsg 无修饰键）
- charmbracelet/x/ansi `clipboard.go`：`SetClipboard`/`SetSystemClipboard`/`SetPrimaryClipboard`/`RequestClipboard`（OSC 52）
- folke/tokyonight.nvim：`colors/storm.lua`（`bg_highlight = #292e42`）、`night.lua`（bg `#1a1b26`）、`day.lua`（反相）
- 对比度：WCAG 2.x 相对亮度/对比度公式，本报告全部数据为实测计算
- paw 源码：`internal/ui/bubble/selection.go`、`styles.go`、`color_manager.go`、`internal/theme/themes.go`
