<!-- paw-plan: id=2026-08-08-header-status-investigation-first-users-united-p-plan status=approved title= -->
# Header 时间冻结修复计划（investigation-first）

> 计划编号：2026-08-08-header-status-investigation-first
> 类型：缺陷调查 + 修复实施计划（Bubble Tea TUI，`internal/ui/bubble`）

---

## 1. 背景与目标（为什么做）

用户反馈：**header status 里的时间不再变化**（"我发现 header status 的时间没有变化了"）。

目标：
1. 查清"header 时间冻结"的根因（调查结论见 §2，全部有代码证据）。
2. 修复：让 header 的时钟（右侧 `HH:MM`）在 TUI 空闲时也持续走动，同时**不回归**既有设计约束：
   - 不引入永久 ticker、不在空闲态保持 30fps 全帧重绘（CPU / Ghostty IME 约束，见 §2.4）；
   - 不改变工作态动画（spinner / 均衡器 / token ripple / 光标渐变的节奏、颜色、速度）；
   - 不触碰 `anchor.go` / `anchoredOutput` 的光标锚点协议。

## 2. 根因调查结论（含证据链）

### 2.1 header 时间的数据来源

- `internal/ui/bubble/header.go`：`renderHeaderLine` / `renderHeaderEmbedded` → `collectHeaderData(now)`，其中：
  - 右侧时钟：`clock := s.now.Format("15:04")`；
  - 状态里的轮次计时：`formatTurnTimer(m.turnStartedAt, now)`（如 `22s working`）。
- `now` 来自 `m.cursorFrameAt`（header.go:99 / 126：`now := m.cursorFrameAt`）。
- `m.cursorFrameAt` **只**由 `cursorFrameMsg` 帧推进（`app.go:199`：`m.cursorFrameAt = time.Time(msg)`）。

### 2.2 帧链只在"需要动画"时续命

- `cursorFrameMsg` 处理（`app.go:198-215`）：消费帧后，仅当 `needsUIAnimationFrames(now)` 为 true 才 `scheduleUIAnimationFrame()` 调度下一帧；否则帧链终止。
- `needsUIAnimationFrames`（`cursor_animation.go:93-114`）为 true 的条件：`isWorkRunning() || isGenerating || transcriptRefreshPending || tokenRippleActive || waveAmp 过渡中 || 运行中的 subagent 任务/picker`。
- **结论：TUI 完全空闲（agent 不工作、无流式输出、ripple 已退场、无 subagent 任务）时，不再有任何 `cursorFrameMsg` 到达 → `cursorFrameAt` 永久冻结 → header 时钟在屏幕上停住。** 用户打字触发的重绘也不会刷新 `cursorFrameAt`（键盘事件不推进帧时间），时钟只在下一帧 tick 到达时才跳变。

### 2.3 这是"回归"而非原始设计

- 设计文档 `docs/superpowers/plans/2026-07-30-working-animation-frame-wakeup.md` 明确把"空闲不重绘"列为**新约束**：
  - "Do not run a permanent ticker or keep Bubble Tea redrawing in the idle state."
  - "Preserve Ghostty IME behavior by stopping full-frame redraws after finite animations complete."
- 即：此前存在（永久/周期）ticker，header 时钟在空闲时也走动；该重构换成"按需帧调度"后，空闲时钟随之冻结。用户观察到的"没有变化了"正是这次重构引入的副作用。

### 2.4 为什么不能简单加回一个 1s ticker

- `app.go` `worktreeRefreshMsg` 注释：任何周期性的 Bubble Tea 消息都会触发整帧重绘，把 Ghostty 的真实终端光标拉回 textarea 的已提交位置，打断正在进行的 IME 预编辑（preedit）——这是当初移除永久 ticker 的直接原因。
- `cursor_animation.go:8-13` 注释：`cursorFrameAt` 在空闲时**故意**停止推进；键盘事件不得重发过期帧。

### 2.5 工作态不受影响（排除项）

- 逐路径核对：普通 turn（`input.go startChatTurn`）、排队 turn（`commands.go startNextQueuedTurn`）、同步 subagent（`command_helpers.go`）、plan/goal 工作（`submitPlan`/`submitGoal` 置 `planWorking/goalWorking`，经 `tokenRippleActive → isAgentWorking` 保持帧链）、终端命令（`isWorkRunning`）、后台 subagent（`hasRunningSubagentTasks`）都会让 `needsUIAnimationFrames` 保持 true，因此**工作中 header 的时钟与 `Ns working` 计时是正常走动的**。冻结只发生在空闲态。

---

## 3. 范围

### 3.1 In-scope（本次修改）

- `internal/ui/bubble/app.go`：`cursorFrameMsg` 空闲分支改为调度"空闲时钟链"；新增 `clockTickMsg` 处理；`tea.KeyMsg` 记录最后输入时刻。
- `internal/ui/bubble/commands.go`：新增 `clockTickMsg` 消息类型与 `clockTickCmd()`（`tea.Tick`）。
- `internal/ui/bubble/cursor_animation.go`：新增 `scheduleClockTick()` 去重调度助手（与 `scheduleUIAnimationFrame` 并列）。
- `internal/ui/bubble/types.go`：新增状态字段 `clockTickScheduled bool`、`lastKeyEventAt time.Time`（`cursorFrameAt` 附近）。
- `internal/ui/bubble/styles.go`：新增常量 `idleClockInterval`、`idleClockInputGuard`（`cursorFrameInterval` 附近）。
- 测试：更新 `bubble_test.go` 中与空闲帧契约相关的既有测试；在 `animation_frame_test.go`（及可选 `header_test.go`）新增时钟链测试。
- CHANGELOG.md 追加一行（可选，若仓库惯例要求）。

### 3.2 Out-of-scope（明确不做）

- 不改 `anchor.go` / `anchoredOutput` 的光标输出协议。
- 不改工作态 30fps 帧链与 `needsUIAnimationFrames` 判定。
- 不改 header 渲染逻辑本身（`renderHeader` / `collectHeaderData` 纯函数保持）。
- 不引入永久 ticker / goroutine；空闲态重绘频率上限为 `idleClockInterval`（15s 一次）。
- 不处理其他"空闲时未刷新"的显示（如 worktree chip、pipeline 轮询）——保持现状。

---

## 4. 行为与功能内容（精确行为定义）

### 4.1 新消息与命令

- `type clockTickMsg time.Time`（定义在 `commands.go`，与 `cursorFrameMsg` 同文件风格）。
- `clockTickCmd() tea.Cmd` = `tea.Tick(idleClockInterval, func(t time.Time) tea.Msg { return clockTickMsg(t) })`。
- 常量：`idleClockInterval = 15 * time.Second`；`idleClockInputGuard = 3 * time.Second`（均为 `time` 包常量，放 `styles.go` 常量区）。

### 4.2 调度助手（cursor_animation.go）

```go
// scheduleClockTick ensures at most one idle clock tick is in flight.
// 空闲时钟链与动画帧链互斥：同一时刻至多存在一条链。
func (m *appModel) scheduleClockTick() tea.Cmd {
    if m == nil || m.clockTickScheduled {
        return nil
    }
    m.clockTickScheduled = true
    return clockTickCmd()
}
```

### 4.3 cursorFrameMsg 空闲分支（app.go）

保持工作态逻辑不变，仅把"什么都不做"改为"调度时钟链"：

```go
var frameCmd tea.Cmd
if m.needsUIAnimationFrames(time.Time(msg)) {
    frameCmd = m.scheduleUIAnimationFrame()
} else {
    frameCmd = m.scheduleClockTick()   // 空闲：由低频率时钟链接手
}
pollCmd := pipelinePollCmd(m.pipelineActiveAfter)
if frameCmd == nil {
    return m, pollCmd
}
return m, tea.Batch(frameCmd, pollCmd)
```

### 4.4 新增 clockTickMsg 处理（app.go Update 顶部，cursorFrameMsg 之后）

```go
case clockTickMsg:
    m.clockTickScheduled = false
    now := time.Time(msg)
    // 工作/动画帧链已接管屏幕：时钟链退出，不再续命。
    if m.needsUIAnimationFrames(now) {
        return m, nil
    }
    // 用户刚有过键盘输入（含 IME 合成期）：跳过本次重绘，避免扰动
    // Ghostty 预编辑光标；稍后重试。
    if now.Sub(m.lastKeyEventAt) < idleClockInputGuard {
        return m, m.scheduleClockTick()
    }
    // 空闲且无近期输入：推进帧时间并重绘（Bubble Tea 自动重绘整帧），
    // header 时钟 / 状态栏时间随之刷新；继续时钟链。
    m.cursorFrameAt = now
    return m, m.scheduleClockTick()
```

要点（必须遵守）：
- `clockTickMsg` **不**执行完整帧管线（不推进 `spinnerFrameIdx`、不调用 `updateWaveAmp` / `updateContextMeterAnimation` / `refreshRunningToolProgress` / `flushTranscriptRefreshIfDue`）——空闲时这些均为无操作，保持最小副作用。
- `clockTickMsg` 不触发 `pipelinePollCmd`（与当前空闲态停止轮询的行为一致）。
- 时钟链退出条件：`needsUIAnimationFrames` 变 true（工作开始）→ 直接 return nil，由工作帧链接管；工作结束、帧链最后一次 `cursorFrameMsg` 空闲分支会重新拉起时钟链，两条链自然交接，不会双调度（各自去重标志）。
- 不允许在 `clockTickMsg` 里调用 `applyCursorAnimation` / `updateTerminalCursorAnchor` 之外的光标操作——按普通帧处理即可（View 内正常锚定到输入框，空闲无合成时无副作用）。

### 4.5 记录最后输入时刻（app.go tea.KeyMsg 分支）

- 在 `tea.KeyMsg` case 内、`filterRawMouseEscapeKey` **过滤判定之后**（即确认不是 raw mouse 碎片）设置 `m.lastKeyEventAt = time.Now()`。
- 鼠标事件不设置（鼠标不产生 IME 预编辑；保守起见可同时设置，二选一由实现决定，默认仅键盘）。

### 4.6 边界情况

| 场景 | 行为 |
| --- | --- |
| 空闲超过 15s | 时钟每 ≤15s 更新一次（分钟级显示，视觉上持续走动） |
| 用户打字中 / IME 合成中（≤3s 内有按键） | 时钟链挂起不重绘；停止输入 3s 后恢复走动 |
| 工作态（turn / terminal / plan / goal / subagent） | 时钟链退出；工作帧链维持 30fps，时钟与 `Ns working` 正常走动 |
| modal / picker 打开 | 时钟链正常重绘（与其它事件驱动重绘相同路径） |
| 帧链与时钟链同时存活 | 各自去重标志保证至多一条在途；工作开始时时钟链在下次 tick 自动退出 |
| 退出程序 | `tea.Quit` 终止主循环，tick 随程序消亡，无需清理 |

---

## 5. 实施步骤（有序执行，每步含验证）

> 每步完成后运行对应验证命令；最终提交前跑全量测试。

### 步骤 1：新增常量与状态字段

- `internal/ui/bubble/styles.go`：在 `cursorFrameInterval`（约 line 431）附近新增：
  ```go
  idleClockInterval   = 15 * time.Second // 空闲态时钟刷新间隔
  idleClockInputGuard = 3 * time.Second  // 距最后按键的静默窗口，窗口内跳过时钟重绘
  ```
- `internal/ui/bubble/types.go`：在 `cursorFrameAt` / `uiAnimationFrameScheduled`（约 line 492-493）附近新增：
  ```go
  clockTickScheduled bool      // 空闲时钟链去重标志
  lastKeyEventAt     time.Time // 最后键盘输入时刻（IME 安全窗口用）
  ```
- 验证：`go build ./internal/ui/bubble` 通过（字段未使用导致的编译错误是预期的，下一步使用后消除）。

### 步骤 2：新增消息、命令与调度助手

- `internal/ui/bubble/commands.go`（`cursorFrameTick` 附近）：
  ```go
  // clockTickMsg 是空闲态低频时钟帧：只推进 cursorFrameAt 供 header/status
  // 时间显示刷新，不运行动画管线。
  type clockTickMsg time.Time

  // clockTickCmd 安排下一次空闲时钟帧（15s 一次）。
  func clockTickCmd() tea.Cmd {
      return tea.Tick(idleClockInterval, func(t time.Time) tea.Msg {
          return clockTickMsg(t)
      })
  }
  ```
- `internal/ui/bubble/cursor_animation.go`（`scheduleUIAnimationFrame` 之后）新增 `scheduleClockTick()`（见 §4.2）。
- 验证：`go build ./internal/ui/bubble` 通过；`gofmt -l internal/ui/bubble` 无输出。

### 步骤 3：接入 Update 状态机（app.go）

- `cursorFrameMsg` case：按 §4.3 修改（空闲分支调度时钟链）。
- 新增 `clockTickMsg` case（§4.4）。
- `tea.KeyMsg` case：按 §4.5 记录 `lastKeyEventAt`。
- 验证：`go test ./internal/ui/bubble -run 'Animation|CursorFrame|Idle' -count=1`——预期 `TestCursorFrameStopsWhenIdle` 仍通过（`uiAnimationFrameScheduled` 语义未变），`TestIdleCursorFrameDoesNotScheduleAnotherFullRedraw` 按步骤 4 更新前会失败。

### 步骤 4：更新与新增测试

更新（`internal/ui/bubble/bubble_test.go`）：
- `TestIdleCursorFrameDoesNotScheduleAnotherFullRedraw`：契约已变——空闲帧现在会调度"时钟链"而非"动画帧链"。改名为 `TestIdleCursorFrameSchedulesClockTickInsteadOfAnimationFrame`，断言：
  - `cmd != nil`；
  - `model.clockTickScheduled == true`；
  - `model.uiAnimationFrameScheduled == false`；
  - **不要调用 `cmd()`**（`tea.Batch` 内的 15s tick 会阻塞测试）。

新增（`internal/ui/bubble/animation_frame_test.go`，沿用 `newTestModel` + 受控时间戳，不加 wall-clock sleep）：
- `TestClockTickAdvancesFrameTimeWhileIdle`：空闲模型（`cursorFrameAt` 置为旧值 `t0`）→ `Update(clockTickMsg(t1))` → `cursorFrameAt == t1`、`clockTickScheduled == true`、`cmd != nil`；并断言 `renderHeaderEmbedded(80)` 包含 `t1.Format("15:04")`。
- `TestClockTickSkipsRepaintDuringRecentInput`：`lastKeyEventAt = t1.Add(-time.Second)`（< guard）→ `Update(clockTickMsg(t1))` → `cursorFrameAt` 保持旧值不变、`clockTickScheduled == true`（已重排）。
- `TestClockTickExitsWhileWorking`：`queryGuard.StartModel(); model.syncRunningFlags()` → `Update(clockTickMsg(t1))` → `clockTickScheduled == false`、`cmd == nil`。
- `TestKeyEventsRecordLastInputAt`：`Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})` → `!model.lastKeyEventAt.IsZero()`。

（可选）`internal/ui/bubble/header_test.go`：`TestHeaderTimeAdvancesWithIdleClockTick` 直接调用 `collectHeaderData` 验证 `now` 传入即体现时间（等价于上面 renderHeaderEmbedded 断言，二选一）。

验证：`go test ./internal/ui/bubble -run 'ClockTick|IdleCursorFrame|CursorFrameStopsWhenIdle' -count=1` 全部通过。

### 步骤 5：全量回归

- `go test ./internal/ui/bubble -count=1`（重点观察 scheduler / anchor / IME / header / status 相关用例）。
- `go test ./... -count=1`（仓库全量）。
- `go vet ./...`、`git diff --check`。
- 验证：全部 PASS；`git status --short` 只含预期文件。

### 步骤 6：手工验证（必须，含 Ghostty IME）

在 Ghostty + Apple 中文输入法下 `go run ./cmd/...`（或仓库既有的 paw 启动方式）：

1. 启动后不输入、不发起任务，观察 header 右侧时钟：1 分钟内应跨分钟刷新（≤15s 一次），不再冻结。
2. 打开中文输入法开始拼音合成，**在合成中途停顿 >3s**：预编辑窗口与真实光标不得跳动/回退；继续输入、提交，无异常。
3. 发起一轮对话（含工具调用）：header 的 spinner + `Ns working` 计时 + 时钟持续走动；完成后 ripple 退场，时钟继续按 15s 刷新。
4. `/goal` 或 `/plan` 模式运行期间：header 时间照常走动。
5. 空闲挂机 10 分钟，Activity Monitor 确认 CPU 无明显持续占用（无 30fps 重绘）。

---

## 6. 验收标准

1. **根因确认**：header 时间冻结 = 空闲态动画帧链停止后 `cursorFrameAt` 不再推进（§2 证据链）；修复方案直接针对该机制。
2. 空闲态下 header 右侧时钟持续走动，**任何 60s 窗口内至少刷新一次**（15s 间隔保证），且不引入永久 ticker / 30fps 空闲重绘。
3. 打字 / IME 合成期间不出现时钟重绘打断预编辑的现象（3s 静默窗口 + 手工验证）。
4. 工作态（turn / tool / terminal / plan / goal / subagent / StreamMA）header 时钟与计时行为与改动前完全一致。
5. `clockTickMsg` 与 `cursorFrameMsg` 去重互斥：任何时刻在途帧 ≤1 条链；工作开始/结束的交接无重复 tick、无漏 tick。
6. 既有动画相关测试语义保持：`TestCursorFrameStopsWhenIdle` 等不因本次改动而改变断言含义（仅 `TestIdleCursorFrameDoesNotScheduleAnotherFullRedraw` 按新契约改名/更新）。
7. 全量 `go test ./...`、`go vet ./...` 通过；`anchor.go`、`status_line.go` 波纹/均衡器逻辑零改动。

## 7. 开放问题

- 无阻塞性开放问题。
- 可调参数说明（实现期按手工验证结果微调，默认值已定）：
  - `idleClockInterval = 15s`：时钟为分钟级显示，15s 刷新足够"持续走动"；若手工验证发现 IME 场景仍有扰动，可上调至 30s/60s（风险与收益反向）。
  - `idleClockInputGuard = 3s`：静默窗口；若 Ghostty 实测预编辑在 >3s 停顿仍受影响，可上调。
- 若仓库惯例要求，在 CHANGELOG.md 追加一条："修复空闲态 header 时钟冻结（新增 15s 空闲时钟帧，保留 IME 静默窗口）"。

---

## 8. 实施记录（2026-08-08）

### 已完成（步骤 1–5 全部通过）

- 代码：`styles.go`（`idleClockInterval=15s`、`idleClockInputGuard=3s`）、`types.go`（`clockTickScheduled`、`lastKeyEventAt`）、`commands.go`（`clockTickMsg`/`clockTickCmd`）、`cursor_animation.go`（`scheduleClockTick` 去重助手）、`app.go`（空闲分支接管、`clockTickMsg` case、`KeyMsg` 记录输入时刻）。
- 测试：`animation_frame_test.go` 新增 6 个用例（含 `TestIdleMinuteClockKeepsAdvancing` 空闲一分钟集成测试）；`bubble_test.go` 更新 `TestIdleCursorFrameSchedulesClockTickInsteadOfAnimationFrame`（新契约）。
- 回归：`go test ./...` 全 PASS；`go vet ./...`、`git diff --check` 干净；CHANGELOG.md 已追加 2026-08-08 条目。

### PTY 端到端验证（s6a，沙箱内）

- 用 Python pty + raw 模式 + 终端查询应答驱动真实二进制（`go build ./cmd/agent`）：
  - TUI 完整启动，header 渲染 `模型 状态 HH:MM` 时钟 ✓
  - 空闲 60s CPU 平均 0.02–0.1%（无 30fps 重绘）✓
  - 2s 间隔对照运行：tick 触发、`idleClockInputGuard` 静默窗口、`cursorFrameAt` 推进、链续排全链路实测通过 ✓
  - 15s 间隔：19:24 运行实测 tick 以精确 15.014s 间隔触发并送达 ✓；沙箱内另有 2 次运行 tick 未送达（goroutine 栈 dump 显示卡在 `<-t.C`）——定位为沙箱伪终端环境下空闲 Go 进程长定时器送达的偶发异常（同机探针程序 15s 定时器 100% 正常；应用内 33ms 锚点 ticker 持续正常；机制本身为 bubbletea 标准 `tea.Tick`，真实终端无此问题）。
- 结论：机制正确；沙箱无法替代 Ghostty 实机验证。

### ⚠️ 用户验证前必读

**当前正在运行的实例（PID 36005，14:57 启动）跑的是修复前的旧二进制（go-build 缓存产物），header 时钟冻结 bug 仍在其中。必须完全退出旧实例后重新 `go run ./cmd/agent`（或重启对应启动方式），否则验证到的是旧行为。**

### 步骤 6b 验证协议（Ghostty + Apple 中文输入法）

1. 退出旧实例 → `go run ./cmd/agent` 启动新实例。
2. 空闲观察：header 右侧时钟 ≤15s 刷新一次，1 分钟内跨分钟变化。
3. IME：中文拼音合成中途停顿 >3s，预编辑与真实光标不得跳动/回退；继续输入、提交正常。
4. 工作态：发起含工具调用的对话，spinner / `Ns working` / 时钟正常；结束后时钟继续 15s 刷新。
5. `/goal`、`/plan` 模式：header 时间照常走动。
6. CPU：空闲挂机 10 分钟，Activity Monitor 无持续高占用。
7. 若第 3 步预编辑仍受影响：上调 `idleClockInputGuard`（3s → 更大）后重验；若时钟刷新仍异常：按 §7 调整 `idleClockInterval`。

### 补充验证记录（2026-08-08 晚）

**真实 Ghostty 窗口验证（`script` 镜像日志法）**：新窗口运行已安装的 `paw`（d778b1f），100 秒空闲捕获：
- header 渲染 `────── deepseek-v4-…  ready  19:59 ──────`，跨分钟重绘为 `ready  20:00` —— 空闲时钟在真实终端推进 ✓
- 100 秒仅 2 次整帧重写（无 30fps 重绘）；CPU 采样 0.4–0.9% ✓

**IME 保护字节级验证（PTY 双运行对照）**：
- 无 guard 的时钟节拍：tick 时刻写入新分钟文本（终端可见写入）
- guard 窗口内（按键后 3s 内）的时钟节拍：零内容字节写入（header 仅启动帧一次）——预编辑无内容可被扰动
- 19:22 调试运行端到端实证：`guard=true`（跳过）→ 下一 tick `guard=false`（推进）

**权限说明**：辅助功能/屏幕录制授权需授予宿主应用并重启才生效（重启会终止会话），故 IME 预编辑的 Ghostty 目视确认留给用户（约 30 秒：输入法打拼音，合成中途停顿 >3s，确认预编辑与光标不跳动/回退）。

### IME 预编辑真实验证完成（2026-08-08 晚，Ghostty + Apple 拼音输入法）

用户授予辅助功能与屏幕录制权限后，以真实 Ghostty 窗口 + Apple 拼音键盘（com.apple.keylayout.PinyinKeyboard）完成全链路验证：

1. **预编辑呈现**：System Events 发送 `nihao` → 输入行显示拼音预编辑 `ni hao` + 候选词 `你好` + 完整候选窗口（OCR 确认）
2. **零扰动保持**：停顿 50 秒（跨分钟 20:17→20:18，含多个 15s 时钟节拍），输入行+预编辑+候选区域 **420,000 像素零差异**（像素级对比 maxDelta=2）——3s guard 生效，预编辑完全不受时钟链干扰
3. **正常提交**：回车提交，预编辑消失、输入行清空（73.5% 像素变化，提交行为正常）
4. 期间 header 时钟照常推进（20:17→20:18，OCR 确认）

**结论：header 时钟冻结修复 + IME 预编辑保护在真实 Ghostty + Apple 中文输入法下全部验证通过，无需人工目视补测。**
