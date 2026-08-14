# Config Center — 全屏扁平搜索键值 UI

日期: 2026-08-14
状态: 已实现并通过 UI 回归测试

## 目标
把 `/config`（与 `/setting`）从当前的嵌套小 modal 改成 Claude Code 式 `/config` TUI：
全屏、顶部 tab、近全宽搜索、固定双列键值、页面小 gutter、底部固定快捷键、
深色终端风格。复刻四要点：固定值列、反色 tab、全宽搜索、值列右侧自然留白。

## 布局（锁定）
```
屏幕宽 W。页面 gutter = clamp(W/40, 2, 6)，内容宽 = W - 2×gutter。
╭─ Paw ─(全屏)──────────────────────────────────────────────────╮
│  Settings  [ General ]  Providers  Models  Cred  Diag           │ ← 反色激活
│                                                                   │
│  ╭─────────────────────────────────────────────────────────────╮  │
│  │ ⌕ Search settings…                                         │  │ ← 近全宽搜索框
│  ╰─────────────────────────────────────────────────────────────╯  │
│                                                                   │
│  Compression mode                state                            │ ← 固定值列
│  Resume recent turns             3                                │
│  Subagent context mode           empty                            │
│  Subagent run mode               background                       │
│  Translate on double click       false                            │
│  Theme                           default                          │
│  Context limit tokens            1048576                          │
│  Context meter location          input-above                      │
│  …                                                                │
│                                                                   │
│  Type to filter · Enter edit · ↑/↓ select · Tab switch           │ ← 固定在底部
╰────────────────────────────────────────────────────────────────╯
```
- 全屏：清屏后整页渲染（替代当前小 `renderModalPanel` 居中盒）。
- 页面只保留 2–6 列自适应 gutter；搜索框、tab、footer 共用左右边界。
- 双列键值：名称贴页面左缘；值从固定列开始并左对齐，右侧自然留白。
- 选中背景只覆盖名称和值，不横贯整行。
- 深色终端：沿用现有 `m.styles`（theme），不硬编码 #282C33。

## Tab 结构（复用现有 config center 顶层区）
General / Providers / Models / Credentials / Diagnostics。
- **General**（新）：扁平键值列表，列 settings.json 所有扁平字段。
- 其余 tab：沿用各自现有页面内容（Providers 列表 / Model actions / Credentials /
  Diagnostics），统一使用全屏小 gutter 与固定双列样式。本轮不改这些页的动作流程。

## General 列表项（settings.json 扁平字段，20 项）
| key | 类型 | 编辑方式 |
|---|---|---|
| compression.mode | enum(state/summary) | 循环 |
| compression.resume_recent_turns | int | 内联 |
| compression.state_compaction_ratio | float | 内联 |
| subagent.default_context_mode | enum(empty/fork) | 循环 |
| subagent.default_run_mode | enum(sync/background) | 循环 |
| subagent.wait_timeout_ms | int | 内联 |
| ui.theme | enum | 循环 |
| ui.context_limit_tokens | int | 内联 |
| ui.context_meter_location | enum | 循环 |
| ui.translate_on_double_click | bool | 切换 |
| context_maintenance.soft_compact_ratio | float | 内联 |
| context_maintenance.tool_result_snip_ratio | float | 内联 |
| context_maintenance.compact_ratio | float | 内联 |
| context_maintenance.compact_force_ratio | float | 内联 |
| context_maintenance.compact_target_ratio | float | 内联 |
| context_maintenance.tail_tokens | int | 内联 |
| context_maintenance.min_tool_result_bytes | int | 内联 |
| context_maintenance.keep_errors | bool | 切换 |
| context_maintenance.keep_user_marked | bool | 切换 |
| context_maintenance.archive_enabled | bool | 切换 |

## 交互
- 打字 → 过滤当前 tab 的行（label/value 子串匹配，大小写不敏感）。
- `↑/↓` 选行；`Enter` 编辑：bool 切换、enum 循环、int/float 内联输入（Enter 确认 Esc 取消）。
- 编辑确认 → `SaveSettings`（持久化）+ 热应用到 runner（见下）。
- `Esc` 清搜索 / 关编辑；`Tab` 或顶行 `←/→` 切 tab。
- 底部 hint 常驻。

## 热生效
现有桥：`syncRunnerCompression(cfg)`（compression.mode）、`syncRunnerModelContextLimit(cfg)`（context_limit_tokens，经模型配置）。
需补一个统一入口 `syncRunnerSettings(cfg)`，按字段调对应 setter：
- compression.mode → `SetContextMode`（已有）
- compression.resume_recent_turns → `SetResumeRecentTurns`（已有）
- compression.state_compaction_ratio → `SetStateCompactionRatio`（已有）
- context_maintenance.* → `SetContextMaintenanceConfig`（已有）
- ui.theme / context_limit_tokens / meter_location / translate → 已有对应应用路径（theme 重渲染、context_limit 经 syncRunnerModelContextLimit）。
未实现某 setter 的 runner 静默跳过（可选接口断言，沿用现有模式）。

## 文件
- `internal/ui/bubble/config_center.go`：全屏渲染、tab 行、搜索 state+过滤、双列键值渲染、底部 hint、General 列表项 + 编辑、`syncRunnerSettings`。
- `internal/ui/bubble/setting_wizard.go`：`/setting` 改为直接打开新 General tab（向导退场，或保留为 fallback——本轮退场，去重）。
- `internal/ui/bubble/app.go` / layout：modal 全屏化（新 `renderFullscreenPanel` 或改 `renderModalPanel` 加全屏模式）。
- `internal/ui/bubble/bubble.go`：若需要新可选接口。
- 退掉之前加在向导里的 compression 步（`settingWizardCompression` 等），合并进 General 列表。

## 测试
- render：小 gutter、近全宽搜索、固定值列、完整 tab、footer 固定在底部。
- 搜索过滤：打字后只显示匹配行；Esc 复原。
- tab 切换：General/Providers/… 切换正确。
- 编辑各类型：bool 切换、enum 循环、int/float 内联；确认后 SaveSettings + 对应 setter 被调。
- 热生效：改 compression.mode → runner 收到 `SetContextMode`（复用 `fakeRunner.SetContextMode` 记录器）。
- 回归：现有 config center 测试（providers/models/credentials 流程）不破。

## 不做（out of scope）
- providers/models/credentials 页面内部交互重设计（只换外壳）。
- 图里的 Status/Usage/Stats tab（paw 无对应概念）。
- 右侧留白改"说明面板"（图描述里提到的 v2，本轮不做）。
