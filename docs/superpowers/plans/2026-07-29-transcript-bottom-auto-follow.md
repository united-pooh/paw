# Transcript 底部自动跟随实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 当 transcript viewport 已位于底部时，即使存在文字选区，也要在模型流式输出和窗口缩放后继续自动跟随最新内容。

**架构：** 保留现有“刷新前记录是否在底部，刷新后决定 `GotoBottom` 或恢复旧偏移”的机制，只移除 `selectionActive` 对底部跟随判定的干预。选区仍照常渲染和复制；只有 viewport 的真实滚动位置决定是否自动跟随，离开底部时仍保留手动阅读位置。

**技术栈：** Go、Bubble Tea、Bubbles viewport、Go `testing`

---

## 文件结构

- 修改：`internal/ui/bubble/transcript.go` — 让普通 transcript 刷新仅根据 `viewport.AtBottom()` 决定是否跟随。
- 修改：`internal/ui/bubble/app.go` — 让窗口尺寸变化前的底部状态捕获遵循相同规则。
- 修改：`internal/ui/bubble/bubble_test.go` — 增加选区激活时的流式输出与窗口缩放回归测试，并保留手动滚动不跟随的既有行为覆盖。

### 任务 1：选区激活时的流式输出仍跟随底部

**文件：**
- 修改：`internal/ui/bubble/transcript.go:416-420`
- 测试：`internal/ui/bubble/bubble_test.go:1093-1110`

- [ ] **步骤 1：编写失败的流式输出回归测试**

在 `TestAssistantStreamingPreservesManualTranscriptScroll` 后新增：

```go
func TestAssistantStreamingFollowsBottomWithActiveSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	model.selectionActive = true
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: 0, col: 1}

	next, _ := model.Update(assistantDeltaMsg("followed with selection\n"))
	model = next.(appModel)

	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom with active selection, offset=%d", model.viewport.YOffset)
	}
}
```

该测试明确建立有效选区，但断言只要更新前 viewport 在底部，新增 assistant 内容后仍位于底部。

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run TestAssistantStreamingFollowsBottomWithActiveSelection -count=1
```

预期：FAIL，错误包含：

```text
viewport left bottom with active selection
```

- [ ] **步骤 3：修改 transcript 刷新的底部跟随判定**

将 `internal/ui/bubble/transcript.go` 中：

```go
// refreshViewport 将 transcript 重新渲染到 viewport。
// 如果刷新前用户位于底部，则继续跟随新内容；否则保留手动滚动位置。
func (m *appModel) refreshViewport() {
	m.refreshViewportWithBottomState(!m.selectionActive && m.viewport.AtBottom())
}
```

替换为：

```go
// refreshViewport 将 transcript 重新渲染到 viewport。
// 如果刷新前 viewport 位于底部，则继续跟随新内容；否则保留手动滚动位置。
// 文字选区只影响渲染，不影响用户已经回到底部后的自动跟随。
func (m *appModel) refreshViewport() {
	m.refreshViewportWithBottomState(m.viewport.AtBottom())
}
```

不要修改 `refreshViewportWithBottomState`：它仍负责在跟随状态下调用 `GotoBottom()`，在非跟随状态下恢复旧 `YOffset`。

- [ ] **步骤 4：运行新增测试验证通过**

运行：

```bash
go test ./internal/ui/bubble -run TestAssistantStreamingFollowsBottomWithActiveSelection -count=1
```

预期：PASS。

- [ ] **步骤 5：运行手动滚动保护测试**

运行：

```bash
go test ./internal/ui/bubble -run 'TestAssistantStreaming(PreservesManualTranscriptScroll|FollowsBottomWithActiveSelection)$' -count=1
```

预期：两个测试均 PASS，证明离开底部时仍保留偏移，位于底部且有选区时则继续跟随。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/transcript.go internal/ui/bubble/bubble_test.go
git commit -m "fix: 选区激活时保持 transcript 底部跟随"
```

### 任务 2：选区激活时窗口缩放仍保持底部跟随

**文件：**
- 修改：`internal/ui/bubble/app.go:114-123`
- 测试：`internal/ui/bubble/bubble_test.go:1139-1148`

- [ ] **步骤 1：编写失败的窗口缩放回归测试**

在 `TestResizeKeepsBottomFollowAfterViewportShrinks` 后新增：

```go
func TestResizeKeepsBottomFollowWithActiveSelection(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	model.selectionActive = true
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: 0, col: 1}

	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	model = next.(appModel)

	if !model.viewport.AtBottom() {
		t.Fatalf("viewport left bottom after resize with active selection, offset=%d", model.viewport.YOffset)
	}
}
```

该测试覆盖 `WindowSizeMsg` 的独立底部状态捕获路径，避免普通刷新修复后缩放仍复现问题。

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run TestResizeKeepsBottomFollowWithActiveSelection -count=1
```

预期：FAIL，错误包含：

```text
viewport left bottom after resize with active selection
```

- [ ] **步骤 3：统一窗口缩放时的底部状态判定**

将 `internal/ui/bubble/app.go` 的 `tea.WindowSizeMsg` 分支开头从：

```go
case tea.WindowSizeMsg:
	wasAtBottom := !m.selectionActive && m.viewport.AtBottom()
	m.ready = true
	m.width = msg.Width
	m.height = msg.Height
	m.relayout()
	m.resizeStreamingBuffers()
	m.refreshViewportWithBottomState(wasAtBottom)
	return m, nil
```

替换为：

```go
case tea.WindowSizeMsg:
	wasAtBottom := m.viewport.AtBottom()
	m.ready = true
	m.width = msg.Width
	m.height = msg.Height
	m.relayout()
	m.resizeStreamingBuffers()
	m.refreshViewportWithBottomState(wasAtBottom)
	return m, nil
```

这样窗口缩放和普通 transcript 刷新都只根据 viewport 是否位于底部决定跟随状态。

- [ ] **步骤 4：运行窗口缩放测试验证通过**

运行：

```bash
go test ./internal/ui/bubble -run 'TestResizeKeepsBottomFollow(AfterViewportShrinks|WithActiveSelection)$' -count=1
```

预期：两个测试均 PASS。

- [ ] **步骤 5：运行 transcript 滚动相关回归测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(AssistantStreaming|ThinkingStreaming|CompletedTurn|ResizeKeepsBottomFollow)' -count=1
```

预期：全部 PASS。重点确认：

- assistant 手动滚动位置仍保留；
- thinking 手动滚动位置仍保留；
- turn 完成时手动滚动位置仍保留；
- 普通底部状态在缩放后仍跟随；
- 激活选区的底部状态在流式输出和缩放后仍跟随。

- [ ] **步骤 6：运行 bubble 包完整测试**

运行：

```bash
go test ./internal/ui/bubble -count=1
```

预期：PASS，无失败测试。

- [ ] **步骤 7：运行全项目测试**

运行：

```bash
go test ./... -count=1
```

预期：PASS，无编译错误或测试失败。

- [ ] **步骤 8：Commit**

```bash
git add internal/ui/bubble/app.go internal/ui/bubble/bubble_test.go
git commit -m "fix: 缩放时按 viewport 底部状态保持跟随"
```
