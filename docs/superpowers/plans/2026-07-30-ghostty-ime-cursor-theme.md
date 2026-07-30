# Ghostty IME 光标与主题背景修复实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法跟踪进度。

**目标：** 在 Ghostty + Apple 原生中文输入法下，以唯一真实终端光标提供准确 IME 锚点，保持主题背景与现有光标颜色渐变，避免动画重绘重置预编辑位置。

**架构：** textarea 仅维护逻辑光标并以隐藏模式渲染；线程安全的 terminal cursor controller 分别发布位置状态与视觉状态。anchoredOutput 串行写 Bubble Tea 帧、主题背景与真实光标控制；空闲光标渐变由输出层仅发送 OSC 12/show-hide 序列，不触发 Bubble Tea 整帧重绘。需要其他 TUI 动画时才继续调度 Bubble Tea frame tick。

**技术栈：** Go 1.25、Bubble Tea、Bubbles textarea/cursor、Lipgloss、ANSI SGR、OSC 12/112。

---

## 文件结构

- 创建：`internal/ui/bubble/anchor_test.go` — 终端 cursor controller、ANSI/OSC 编码、输出顺序与去重测试。
- 修改：`internal/ui/bubble/anchor.go` — 位置/视觉状态、真实光标输出、背景恢复、颜色动画和幂等 cleanup。
- 修改：`internal/ui/bubble/layout.go` — 发布主题背景和精确 anchor；隐藏 textarea 软件光标。
- 修改：`internal/ui/bubble/cursor_animation.go` — 将动画状态发布给真实光标控制器，保留曲线与主题端点。
- 修改：`internal/ui/bubble/app.go` — 初始化隐藏软件光标，限制空闲整帧动画 tick。
- 修改：`internal/ui/bubble/bubble.go` — 输出包装器生命周期和退出恢复。
- 修改：`internal/ui/bubble/bubble_test.go` — 更新现有 anchor 断言并增加 completion/主题/单一光标回归测试。

## Phase 1：状态模型与 ANSI/OSC 编码

- [ ] **步骤 1：编写失败测试**
  - 测试合法 `#rrggbb` 背景生成 `CSI 48;2;r;g;bm`。
  - 测试光标颜色生成 `OSC 12;#rrggbb ST`，恢复生成 OSC 112。
  - 测试 visual-only 序列不包含 `\r` 或 CSI A/B/C/D。
  - 测试非法颜色不生成黑色。

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/ui/bubble -run 'TestTerminal(Cursor|Background|Visual)' -count=1
```

预期：FAIL，缺少新编码函数和状态类型。

- [ ] **步骤 3：实现最少状态和编码函数**
  - 拆分 `terminalCursorPosition` 与 `terminalCursorVisual`。
  - 增加颜色规范化、背景 SGR、OSC 12/112、show/hide、激活和恢复函数。
  - 增加线程安全 controller 的 position/visual 发布与快照消费。

- [ ] **步骤 4：运行测试确认通过**

```bash
go test ./internal/ui/bubble -run 'TestTerminal(Cursor|Background|Visual)' -count=1
```

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/anchor.go internal/ui/bubble/anchor_test.go
git commit -m "fix: add terminal cursor visual state (phase 1)"
```

## Phase 2：单一真实光标、IME 位置和主题背景

- [ ] **步骤 1：编写失败测试**
  - textarea 渲染副本使用 `cursor.CursorHide`，文本不被反转或覆盖。
  - anchor 包含当前主题背景和动画颜色。
  - `/` completion 打开前后 anchor 列不变。
  - anchoredOutput 帧后按“定位 → 背景 → 颜色 → 可见性”输出。
  - 仅视觉更新不输出定位序列。

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/ui/bubble -run 'Test(InputUsesSingleRealCursor|ViewPublishesCursorTheme|SlashCompletionKeepsCursorAnchor|AnchoredOutput)' -count=1
```

预期：FAIL，生产路径仍绘制软件光标且 anchor 无视觉状态。

- [ ] **步骤 3：实现最少生产切换**
  - `renderInputContent` 的 textarea 副本设置 `cursor.CursorHide`。
  - folded fallback 同样隐藏 cursor。
  - `inputCursorTerminalPosition` 携带当前主题背景。
  - `applyCursorAnimation` 不再修改 textarea 可见光标，改为发布真实光标颜色和可见性。
  - anchoredOutput 使用共享锁应用完整 anchor 和 visual-only 更新。
  - Close/Run defer 恢复 OSC 112、显示光标和 SGR reset。

- [ ] **步骤 4：避免空闲 30 FPS 整帧重绘**
  - 输出层独立定时计算现有 `cursorIntensityAt()` 并仅写 OSC 12/show-hide。
  - Bubble Tea frame tick 只在 spinner、wave、meter、tool progress、modal/activity 等确实需要整帧动画时继续调度。
  - 普通空闲输入和 completion 打开时不得周期性重定位真实光标。

- [ ] **步骤 5：运行测试确认通过**

```bash
go test ./internal/ui/bubble -run 'Test(InputUsesSingleRealCursor|ViewPublishesCursorTheme|SlashCompletionKeepsCursorAnchor|AnchoredOutput|Cursor)' -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/anchor.go internal/ui/bubble/layout.go internal/ui/bubble/cursor_animation.go internal/ui/bubble/app.go internal/ui/bubble/bubble.go internal/ui/bubble/anchor_test.go internal/ui/bubble/bubble_test.go
git commit -m "fix: use real terminal cursor for IME (phase 2)"
```

## Phase 3：主题与生命周期回归加固

- [ ] **步骤 1：增加主题与恢复测试**
  - `applyTheme()` 后下一输出使用新背景、新普通/终端光标端点。
  - 一个动画周期产生多个 OSC 颜色且位置不变。
  - inactive、Close 和重复 Close 恢复幂等。
  - wizard/terminal work 状态关闭真实输入光标。

- [ ] **步骤 2：运行目标测试**

```bash
go test ./internal/ui/bubble ./internal/theme -count=1
```

预期：PASS。

- [ ] **步骤 3：运行全仓验证**

```bash
go test ./... -count=1
go vet ./...
```

预期：全部通过，无 vet 错误。

- [ ] **步骤 4：人工验收记录**
  - Ghostty 中输入 `/` 打开 completion。
  - Apple 中文输入法输入 `nihao`，等待两个 3 秒周期。
  - 确认无黑色背景、单一光标位于拼音末尾、渐变持续且位置不回跳。
  - 选词、移动逻辑光标、resize、切换内置主题、正常退出和双 Ctrl+C。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble internal/theme
git commit -m "test: harden themed IME cursor lifecycle (phase 3)"
```
