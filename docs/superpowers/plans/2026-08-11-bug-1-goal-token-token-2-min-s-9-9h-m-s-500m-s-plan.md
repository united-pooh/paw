<!-- paw-plan: id=2026-08-11-bug-1-goal-token-token-2-min-s-9-9h-m-s-500m-s-plan status=approved title=Goal-token-and-long-duration-display-fixes -->
# Goal 模式 Token 渲染与长任务小时计时修复计划

> 计划编号：`2026-08-11-bug-1-goal-token-token-2-min-s-9-9h-m-s-500m-s-plan`
> 类型：缺陷修复（Bubble Tea TUI）
> 主要范围：`internal/ui/bubble`

---

## 1. 背景与目标（为什么做）

当前有两个相互独立的 TUI 显示缺陷：

1. **Goal 模式提交后丢失输入 Token 的视觉元数据**
   - 普通聊天提交会把 `submittedDraft` 中的 `inputTokens` 写入 `entryUser`，随后 `renderTokenizedTranscriptBody` 用 token label 和语义样式渲染，隐藏底层原始语法。
   - Goal 模式的 `submitGoal` 当前只把纯字符串交给 `goalController.Start`，然后写入一条 `entrySystem`：`started <id>\nobjective: <raw line>`。
   - 该 system entry 不携带 `inputTokens`，而 transcript 的 token 化渲染当前只针对用户条目，因此补全生成的命令、技能或文件引用会退化为底层真实引用文本，例如 `/help`、`@README.md` 或 `[$design](/absolute/path/SKILL.md)`，而不是输入框中看到的 token label。

2. **长任务耗时没有小时单位**
   - 工作中 header 的 `formatTurnTimer` 和完成后 assistant footer 的 `formatTurnDuration` 都只实现了秒与分钟分支。
   - 超过一小时后，分钟继续无限累加；例如 9 小时会显示成 `540m00s` 一类文本，而不是带小时单位的 `9h00m00s`。

本次目标：

- Goal 模式成功提交后，在 transcript 中继续按输入时的 token label 与样式展示命令/技能/文件 token，同时仍把底层原始语法原样交给 Goal runtime。
- 为工作中 header 计时和完成后 turn footer 增加小时单位，并统一成紧凑的 `HhMMmSSs` 格式。
- 保持一小时以内、亚秒级、失败路径、Goal 生命周期状态以及其它输入模式的既有行为不变。

---

## 2. 根因与代码证据

### 2.1 Goal Token 丢失

相关路径：

- `internal/ui/bubble/input.go`
  - `consumeSubmittedInput` 会把当前 `inputDraft{Text, Tokens}` 克隆到 `m.submittedDraft`。
  - 普通聊天的 `startChatTurn` 通过 `userTranscriptEntry("you", line)` 创建 `entryUser`；`submittedTokensForLine` 会从 `m.submittedDraft` 取回 token 元数据。
  - `submitGoal` 当前仅调用 `goalController.Start(line)`，成功后写入纯 system entry，未使用 `userTranscriptEntry`，因此 token 元数据没有进入 transcript。
- `internal/ui/bubble/input_token.go`
  - `inputDraft.Text` 是提交给运行时的精确原始语法；`Tokens` 仅控制 Bubble Tea 的视觉展示。
  - `projectInput` 在 token range 上展示 `token.Label`，跳过底层 raw range。
  - `renderTokenizedTranscriptBody` 可正确渲染带 token 的用户条目。
- `internal/ui/bubble/transcript.go`
  - 只有 `entry.kind == entryUser && len(entry.inputTokens) > 0` 时才走 token 化 transcript 渲染。

因此修复不应改写 Goal objective 的真实内容，也不需要让 Goal runtime 理解 UI token；应在 Goal 成功启动后补回一个携带 `inputTokens` 的用户型 objective 条目，并把生命周期确认保留为独立 system entry。

### 2.2 小时单位缺失

相关路径：

- `internal/ui/bubble/header.go`
  - `formatTurnTimer`：`<60s` 输出 `Ns`，否则固定输出 `%dm%02ds`，没有小时分支。
  - `collectHeaderData` 在 agent 工作时直接使用该函数生成 `<elapsed> working`。
- `internal/ui/bubble/turn_timing.go`
  - `formatTurnDuration`：`<1s` 输出 `Nms`，`<60s` 输出 `Ns`，否则固定输出 `%dm%02ds`，同样没有小时分支。
  - `formatTurnFooter` 使用该结果装饰完成后的 assistant transcript。

这两个函数表示同一种“turn 紧凑耗时”，应共用一个整秒格式化规则，避免未来再次分叉。

---

## 3. 范围

### 3.1 In-scope（本次修改）

- `internal/ui/bubble/input.go`
  - 调整 Goal 模式成功提交后的 transcript 记录方式。
  - 保留原始 objective 传递、输入历史、Goal 工作态和动画状态逻辑。
- `internal/ui/bubble/goal_command_test.go` 或 `internal/ui/bubble/input_token_test.go`
  - 增加 Goal 模式下命令 token、文件 token 的端到端回归测试。
- `internal/ui/bubble/turn_timing.go`
  - 增加供 header 与 footer 共用的紧凑整秒格式化助手。
  - 让完成后 footer 支持小时单位。
- `internal/ui/bubble/header.go`
  - 让工作中 header 计时复用统一格式化规则。
- `internal/ui/bubble/header_test.go`
  - 增加一小时边界与 9 小时场景测试。
- `internal/ui/bubble/turn_timing_test.go`
  - 增加 footer/Duration 的小时边界与 9 小时场景测试。

### 3.2 Out-of-scope（明确不做）

- 不改变 `goalController.Start(string)`、`goal.Runtime`、模型消息或文件引用展开协议。
- 不把 UI token 元数据持久化到 Goal store/checkpoint；token 仍是当前 Bubble transcript 的视觉元数据。
- 不改变手工键入但未通过补全创建 token 的文本语义；无 token 元数据的 `/text`、`@text` 继续按普通文本显示。
- 不修改 Plan 模式的 transcript 展示；本需求只修复 Goal 模式。
- 不修改 `/goal status|pause|resume|stop|budget` 等生命周期命令行为。
- 不泛化 system entry 的 token 渲染能力；本次使用已有、已验证的 `entryUser + inputTokens` 渲染路径。
- 不修改 running tool、subagent、task card 等其它耗时格式；它们已有独立的小时/天格式和展示约束。
- 不新增天单位。Turn 计时超过 24 小时时继续累计小时，例如 `27h05m07s`。

---

## 4. 行为与功能内容（精确行为定义）

### 4.1 Goal 模式提交后的 transcript

当 `m.goalMode == true` 且 `goalController.Start(line)` 成功时：

1. `goalController.Start` 接收的仍是 trim 后的原始 `line`，不得替换为 token label，也不得删除 `/`、`@`、Markdown skill reference 或路径。
2. transcript 新增一条用户型 objective 条目：
   - `kind = entryUser`
   - `title = "you (goal)"`
   - `body = line`（保留原始文本，供历史、复制和内部数据一致性使用）
   - `inputTokens = submittedTokensForLine(line)`
3. transcript 再新增一条独立的 Goal system 确认：
   - `kind = entrySystem`
   - `title = "goal"`
   - `body = "started " + id`
4. system 确认中不再重复拼接 `objective: <raw line>`，避免同一个 objective 一次以 token 展示、一次又以原始引用文本泄漏。
5. 现有状态更新保持不变：
   - `inputSource = inputSourceFresh`
   - `goalWorking = true`
   - 设置 `turnStartedAt`、`turnID`
   - 启动 UI animation frame。
6. `goalController.Start` 失败时保持现状：只新增 error entry，不新增“已成功提交”的 goal objective/user entry，不进入 working 状态。

预期示例：

- 底层 raw objective：`read @README.md`
- Goal controller 收到：`read @README.md`
- transcript 可见：`read README.md`，其中 `README.md` 使用 file token 样式，不显示 `@`。

- 底层 raw objective：`/help`（由命令补全创建 token）
- Goal controller 收到：`/help`
- transcript 可见：`help`，使用 command token 样式，不显示 `/`。

- 底层 raw objective：`[$design](/private/path/SKILL.md)`
- Goal controller 收到完整 Markdown reference。
- transcript 只显示 `design` token，不泄漏绝对路径。

### 4.2 Turn 紧凑耗时格式

新增/抽取一个包内共享的整秒格式化助手（建议命名 `formatTurnSeconds`，使用 `int64` 秒数），规则固定如下：

| 总耗时 | 输出 |
| --- | --- |
| 负数（防御性输入） | 按 `0s` 处理 |
| `0s`–`59s` | `Ss`，例如 `0s`、`59s` |
| `1m00s`–`59m59s` | `MmSSs`，秒固定两位，例如 `1m05s`、`59m59s` |
| `>=1h` | `HhMMmSSs`，小时累计且不补零，分钟/秒固定两位，例如 `1h00m00s`、`9h05m07s`、`27h05m07s` |

具体约束：

- `formatTurnTimer(startedAt, now)`：
  - `startedAt` 为零或 `now.Before(startedAt)` 时仍返回 `0s`。
  - 其余情况把完整秒数交给共享助手。
- `formatTurnDuration(durationMS)`：
  - 负数仍归零。
  - `<1000ms` 保持现有毫秒输出，例如 `0ms`、`950ms`。
  - `>=1000ms` 舍弃不足一秒的尾数，按完整秒交给共享助手，保持现有截断语义。
- `formatTurnFooter` 的响应时间格式与两个空格分隔保持不变，例如：
  - `9h05m07s  07:47:47 AM`
- 不改变 header 的 strict-width/truncation 机制；长 elapsed 文本仍由既有 header 渲染预算处理。

### 4.3 边界场景

| 场景 | 预期 |
| --- | --- |
| Goal objective 没有 token | 仍显示为普通 `you (goal)` 文本，Goal 正常启动 |
| Goal objective 有多个 token | 全部 token range 与 label 保留，普通文本片段顺序不变 |
| Token 前后有 Unicode/多行文本 | 继续由 `projectInput` 的 rune range 与 cell-width 逻辑渲染 |
| Goal 启动失败 | 不显示 `started`，不新增成功 objective 条目 |
| `59m59s` | `59m59s` |
| `60m00s` | `1h00m00s`，不得显示 `60m00s` |
| `9h00m00s` | `9h00m00s`，不得显示 `540m00s` |
| `9h05m07s` | `9h05m07s` |
| `27h05m07s` | `27h05m07s`，不切换成天单位 |

---

## 5. 具体执行步骤（有序，逐步验证）

### 步骤 1：先增加 Goal Token 回归测试

在 `internal/ui/bubble/goal_command_test.go`（可复用 `fakeGoalController`）新增 Goal 直接输入模式的表驱动测试，至少覆盖：

1. command token：raw `/help`，label `help`；
2. file token：raw `read @README.md`，label `README.md`。

测试通过 `handleSubmit` 或 `Update(tea.KeyEnter)` 走完整路径，并断言：

- `controller.started` 收到精确 raw objective；
- 输入框和 `inputTokens` 在提交后被清空；
- transcript 中存在 `entryUser`、title 为 `you (goal)`、body 为 raw objective、token 元数据完整；
- `renderEntry`/`ansi.Strip` 后可见 token label，但不包含 `/help` 或 `@README.md`；
- command/file token 仍使用各自的 token style；
- 独立 system entry 包含 `started goal-1`，但不再包含 `objective:` 或 raw objective；
- `goalWorking`、`turnStartedAt`、`turnID` 仍正确设置。

再增加失败分支断言：当 fake controller 返回错误时，不新增 `you (goal)` 成功 objective 条目，且 `goalWorking == false`。

验证：

```bash
go test ./internal/ui/bubble -run 'Goal.*Token|Goal.*Submit' -count=1
```

在实现前，新 token 测试应因 transcript 丢失 token 元数据而失败；步骤 2 后通过。

### 步骤 2：修复 `submitGoal` 的 transcript 记录

修改 `internal/ui/bubble/input.go` 的 `submitGoal`：

- 保持 trim、controller nil 检查、history、`Start(line)` 调用和错误处理顺序。
- 仅在 `Start` 成功后：
  1. 调用已有 `m.userTranscriptEntry("you (goal)", line)` 并 `addEntry`；
  2. 写入独立的 `entrySystem{title: "goal", body: "started " + id}`。
- 删除 system body 中的 `\nobjective: ` 拼接。
- 不修改 `submittedDraft`/token 的生成方式，也不修改 transcript token renderer。

验证：

```bash
go test ./internal/ui/bubble -run 'Goal.*Token|GoalCommand|SubmittedSkillToken' -count=1
```

重点确认普通聊天的 `TestSubmittedSkillTokenRendersInTranscriptAndKeepsRawRunnerInput` 仍通过，证明 Goal 修复没有改变 raw-input/token-visual 双轨契约。

### 步骤 3：为小时格式先补齐边界测试

修改 `internal/ui/bubble/header_test.go`：

- 保留现有 `22s`、`1m33s` 断言；
- 增加表驱动边界：
  - `59s -> 59s`
  - `1m05s -> 1m05s`
  - `59m59s -> 59m59s`
  - `1h -> 1h00m00s`
  - `9h05m07s -> 9h05m07s`
  - `27h05m07s -> 27h05m07s`
- 通过 `collectHeaderData` 验证 9 小时工作态 status 包含 `9h00m00s working`。

修改 `internal/ui/bubble/turn_timing_test.go`：

- 保留 `<1s`、秒、分钟和 footer 时间戳测试；
- 增加：
  - `3599000ms -> 59m59s`
  - `3600000ms -> 1h00m00s`
  - `9h05m07s -> 9h05m07s`
  - 精确 9 小时 footer 以 `9h00m00s  ` 开头。

验证：

```bash
go test ./internal/ui/bubble -run 'FormatTurn|HeaderShowsTurnElapsed' -count=1
```

实现前，小时用例应失败；步骤 4 后通过。

### 步骤 4：统一 header 与 footer 的小时格式

修改 `internal/ui/bubble/turn_timing.go`：

- 新增 `formatTurnSeconds(seconds int64) string`（或等价包内私有名），实现 §4.2 的三段规则。
- `formatTurnDuration` 保留 `<1000ms` 分支；整秒部分改为调用共享助手。

修改 `internal/ui/bubble/header.go`：

- `formatTurnTimer` 保留零时间与时间倒退保护。
- 将 `now.Sub(startedAt)` 转成完整 `int64` 秒并调用共享助手。
- 若 `fmt` 不再使用，清理 import。
- 更新注释，明确超过一小时显示 `HhMMmSSs`，不再描述为仅 `mm:ss`。

验证：

```bash
gofmt -w internal/ui/bubble/input.go \
  internal/ui/bubble/goal_command_test.go \
  internal/ui/bubble/header.go \
  internal/ui/bubble/header_test.go \
  internal/ui/bubble/turn_timing.go \
  internal/ui/bubble/turn_timing_test.go

go test ./internal/ui/bubble -run 'Goal.*Token|GoalCommand|FormatTurn|HeaderShowsTurnElapsed' -count=1
```

### 步骤 5：Bubble TUI 全量回归

运行：

```bash
go test ./internal/ui/bubble -count=1
```

重点观察：

- token completion/projection/history/session restore；
- Goal lifecycle working-state；
- header exact width 与 compact header；
- assistant footer、transcript cache、session restore sidecar；
- queued chat、supplement、普通聊天提交。

### 步骤 6：仓库级验证

运行：

```bash
go test ./... -count=1
go vet ./...
git diff --check
```

确认：

- 无编译、测试或 vet 回归；
- 没有修改 Goal runtime 消息协议；
- diff 只包含计划内 Bubble TUI 文件（以及仓库惯例要求时的 CHANGELOG）。

### 步骤 7：手工验收

1. 启动 TUI，切换到 Goal 输入模式。
2. 通过补全选择一个命令 token，提交：
   - 输入框提交前显示 token label；
   - transcript 的 `you (goal)` 仍显示同样的 token label/样式；
   - 不显示底层 `/` 语法；
   - Goal 正常开始运行。
3. 再用文件补全选择 `@README.md` 一类文件并提交：
   - transcript 显示 file token；
   - 不显示 `@` 前缀；
   - 模型仍能收到并处理真实文件引用。
4. 若有 skill 补全，确认绝对 `SKILL.md` 路径不会出现在 transcript，只显示 skill label。
5. 小时格式无需真实等待 9 小时；以单元测试中的受控时间覆盖为主要验收。若可注入/构造 9 小时 `turnStartedAt` 或 `TurnMetadata.DurationMS`，确认 header 与 footer 分别显示 `9h00m00s working` 和 `9h00m00s  <timestamp>`。

---

## 6. 验收标准

1. Goal 模式中由补全创建的 command、skill、file token 在成功提交后仍以 token label 和对应样式出现在 transcript，不退化为 raw 引用文本。
2. Goal controller/runtime 收到的 objective 与提交前 `inputDraft.Text` 完全一致；token 修复只改变 UI 展示，不改变模型输入。
3. Goal start 成功时 transcript 包含一条 `you (goal)` objective 和一条独立 `goal: started <id>` 确认；system entry 不重复 raw objective。
4. Goal start 失败时不出现误导性的成功 objective/start 确认，且不进入 working 状态。
5. 手工输入、未由补全创建 token 的文本继续按普通文本渲染。
6. 工作中 header：
   - `<1m` 仍为 `Ss`；
   - `<1h` 仍为 `MmSSs`；
   - `>=1h` 为 `HhMMmSSs`；
   - 9 小时精确显示 `9h00m00s working`，不显示 `540m00s working`。
7. 完成后 assistant footer 使用相同小时规则；9 小时精确显示 `9h00m00s  <local time>`。
8. `<1000ms` 的 footer duration、响应时间格式、header strict-width、transcript cache/session restore 行为无回归。
9. `go test ./internal/ui/bubble -count=1`、`go test ./... -count=1`、`go vet ./...`、`git diff --check` 全部通过。

---

## 7. 开放问题

无阻塞性开放问题。

本计划采用以下明确默认值，实施时不再二次询问：

- 小时格式为紧凑的 `HhMMmSSs`，例如 `9h05m07s`；分钟和秒在小时分支固定两位。
- 超过 24 小时继续累计小时，不引入天单位。
- 只修复 Goal 直接输入模式；Plan 模式与其它 system entry 不在本次范围。
- Goal objective 使用 `you (goal)` 用户型 transcript 条目承载 token，生命周期确认继续使用独立 system entry。
