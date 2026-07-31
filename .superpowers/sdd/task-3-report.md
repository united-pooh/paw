# Task 3 Report: Custom option inline input

## 实现

- 在 `selectionDock` 中加入独立的 Bubble Tea `textinput.Model` 与 `editingCustom` 状态。
- Custom action 支持 Enter/Space 激活，输入框使用简报指定的 Prompt、Placeholder、CharLimit 与 Width。
- Custom 确认时 trim 文本并拒绝空白；新建 custom 计入 min/max，错误文案按简报精确取值。
- 单选 custom 确认后立即返回 `SelectedOptions` 并完成 Select。
- 多选 custom 确认后保存一个 custom answer 并继续；再次打开会预填现有值并允许编辑，不会新增第二个 custom。
- 编辑态 Enter 确认，Esc 只退出编辑并保留 Select，Ctrl+C 取消整个 Select。
- 保持单选 preset Enter 立即提交、多选 answer Space 切换/Enter 提交、Chat 等同取消。
- 未修改 renderer、transcript 或 `highlighted` 兼容桥。

## 修改文件

- `internal/ui/bubble/selection_dock.go`
- `internal/ui/bubble/selection_dock_test.go`

另按任务要求生成本报告：`.superpowers/sdd/task-3-report.md`。

## TDD 证据

### RED

命令：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomOptionSubmitsImmediately|MultipleCustomOptionAddsAndEditsOneAnswer|CustomOptionValidatesEmptyAndMax)' -v
```

结果：编译失败，明确报告 `editingCustom`、`customInput`、`confirmCustom` 未定义，符合简报预期。

### GREEN

命令：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomOptionSubmitsImmediately|MultipleCustomOptionAddsAndEditsOneAnswer|CustomOptionValidatesEmptyAndMax|CustomKeysActivateWithEnterAndSpace|CustomEditEscPreservesSelectAndCtrlCCancels|BrokerRequestAndKeys|ConsumesCtrlCAsCancellation)' -v
```

结果：PASS，`ok paw/internal/ui/bubble`。

同时执行 `git diff --check`：PASS。

### 包级测试

命令：

```bash
go test ./internal/ui/bubble
```

结果：任务相关测试通过，但包级测试存在 2 个与本任务修改文件无关的既有失败：

- `TestCompleteSelectToolCallBodySummarizesResult`
- `TestSelectToolDisplayTargetAndResultSummary`

两者期望旧 `SelectedIDs` 构造的摘要，但当前任务 1 已将结果协议迁移到 `SelectedOptions`；失败位于未获准修改的 `select_tool_display_test.go` 测试数据/期望范围。本任务未越界修改。

## 自审

- 仅触及指定实现与测试文件；未改 renderer。
- Custom label 输出顺序稳定：preset 按 request option 顺序，custom 最后。
- 编辑已有 custom 时不会因其自身占用 max 而阻止重开或修改。
- 输入错误后普通字符更新会清除错误，空白 Enter 则保留编辑态并显示精确错误。
- Chat 的 Enter/Space 激活和普通 Esc/Ctrl+C 均走 Select cancellation result。
- `highlighted` 仅维持任务 4 前 renderer 兼容行为。

## 疑虑

- 包级测试中的上述 2 个失败属于任务 1 协议迁移后的测试夹具不一致，且不在本任务允许修改的两个文件内；已保留并如实记录。
- 按要求未进行任务 4 renderer 重设计，因此 textinput 的实际专用渲染将在后续 renderer 工作中接入；本任务完成状态与按键状态机集成。

---

## 审查修复记录

### 修复内容

- 将 `beginCustomEdit` 与 `confirmCustom` 的 max 新增检查限定为 `ModeMultiple`；单选已有初始 preset 且 `MaxSelect=1` 时，Custom 作为替换项可正常进入、确认并只返回 `custom_option`。
- 新增真实按键回归，覆盖单选场景下 Enter/Space 激活 Custom，并经 Enter 确认完成。
- 新增多选满额边界回归：preset 与 existing custom 恰好占满 max 时，仍可重开并编辑 existing custom，最终结果中只有一个 `custom_option`。

### RED

命令：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomReplacesInitialPresetAtMax|MultipleEditsExistingCustomWhenMaxIsFull)' -v
```

结果：`TestSelectionDockSingleCustomReplacesInitialPresetAtMax` 的 Enter 与 Space 子测试均失败，错误为 Custom 未进入编辑态并显示 `You can select at most 1 option.`；多选 existing custom 边界测试已通过。

### GREEN

聚焦修复测试及任务 3 相关回归全部 PASS：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomOptionSubmitsImmediately|SingleCustomReplacesInitialPresetAtMax|MultipleCustomOptionAddsAndEditsOneAnswer|MultipleEditsExistingCustomWhenMaxIsFull|CustomOptionValidatesEmptyAndMax|CustomKeysActivateWithEnterAndSpace|CustomEditEscPreservesSelectAndCtrlCCancels|MultipleKeysSpaceTogglesAndEnterSubmits|MultipleEnterValidatesMinimumWithoutToggling|MultipleSpaceEnforcesMaximumWithoutCompleting|BrokerRequestAndKeys|ConsumesCtrlCAsCancellation)' -v
```

`git diff --check`：PASS。

包级 `go test ./internal/ui/bubble` 仍仅有原报告记录的两个既有 `select_tool_display_test.go` 协议夹具失败，本次未修改 renderer/transcript 或该测试文件。

### 修改文件

- `internal/ui/bubble/selection_dock.go`
- `internal/ui/bubble/selection_dock_test.go`
- `.superpowers/sdd/task-3-report.md`（按要求追加修复记录）

### 自审

- 行为条件明确限定为 multiple + 新增 custom；multiple 满额时仍阻止新增，但已有 custom 的编辑不受影响。
- single 的 custom 完成结果由原有单选返回路径构造，不会混入初始 preset。
- 未修改 renderer/transcript；实现与测试代码仅触及任务允许的两个源码文件。
