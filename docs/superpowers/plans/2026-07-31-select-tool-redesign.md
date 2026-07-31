# Select 工具完整迭代实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 将 Select 工具升级为返回可读结构化答案、支持固定自定义/讨论操作、完整可滚动选项列表，并在 transcript 中提供单行摘要与可读展开详情。

**架构：** 协议层以 `SelectedOption{id,label}` 完全替代 `selected_ids`；Bubble dock 使用“答案/自定义/讨论”三类焦点和独立单行输入模型管理交互；transcript 条目保留原始 Select 请求，并由 Select 专用格式化器将请求和结果组合成摘要及详情。非 Select 工具继续走现有通用渲染路径，Select 解析失败时也回退到通用原始结果。

**技术栈：** Go、Bubble Tea、Bubbles `textinput`、Lip Gloss、Go `testing`

---

## 文件结构

### 创建

- `internal/ui/bubble/select_tool_detail.go`：解析 Select 请求/结果，生成折叠目标文本与展开详情；不承担 dock 交互。
- `internal/ui/bubble/select_tool_detail_test.go`：覆盖 Select 摘要、展开详情、取消与解析失败回退。

### 修改

- `internal/tool/select/types.go`：定义保留 ID、`SelectedOption` 和新的 `Result`。
- `internal/tool/select/input.go`：拒绝调用方使用保留 ID `custom_option`。
- `internal/tool/select/input_test.go`：增加保留 ID 校验和结果克隆测试。
- `internal/tool/select/tool.go`：稳定输出非 `null` 的 `selected_options`。
- `internal/tool/select/tool_test.go`：锁定破坏性新 JSON 协议。
- `internal/tool/select/broker_test.go`：将 broker 测试迁移到 `SelectedOptions`。
- `internal/ui/bubble/selection_dock.go`：实现三类焦点、稳定结果构造、自定义输入状态、取消/讨论行为和按键处理。
- `internal/ui/bubble/selection_dock_render.go`：实现结构化卡片、答案范围、滚动余量、固定操作区与自定义输入渲染。
- `internal/ui/bubble/selection_dock_test.go`：覆盖导航、自定义答案、最大数量、讨论退出、滚动和窄终端。
- `internal/ui/bubble/layout.go`：允许 Select dock 使用独立最大高度，同时保持普通输入框原上限。
- `internal/ui/bubble/types.go`：在 transcript 工具事务中保留原始工具输入。
- `internal/ui/bubble/transcript.go`：记录/合并工具输入，Select 展开时调用专用详情格式化器。
- `internal/ui/bubble/subagent_picker.go`：历史 transcript 恢复时保留 Select 请求输入并在合并结果后更新摘要。
- `internal/ui/bubble/utils.go`：迁移旧 Select 专用结果逻辑到新格式化器，移除 `selected_ids` 依赖。
- `internal/ui/bubble/select_tool_display_test.go`：更新 Select 调用体和折叠摘要断言。
- `internal/ui/bubble/tool_track_test.go`：覆盖实时与历史 Select 事务的单行折叠/可读展开行为。

---

### 任务 1：替换 Select 结果协议

**文件：**
- 修改：`internal/tool/select/types.go:3-44`
- 修改：`internal/tool/select/input.go:34-50`
- 修改：`internal/tool/select/tool.go:31-42`
- 测试：`internal/tool/select/input_test.go`
- 测试：`internal/tool/select/tool_test.go`
- 测试：`internal/tool/select/broker_test.go`

- [ ] **步骤 1：编写结果类型、保留 ID 和克隆行为的失败测试**

在 `internal/tool/select/input_test.go` 增加：

```go
func TestResultCloneCopiesSelectedOptions(t *testing.T) {
	original := Result{SelectedOptions: []SelectedOption{{ID: "logs", Label: "Logs"}}}
	cloned := original.Clone()
	cloned.SelectedOptions[0].Label = "Changed"
	if original.SelectedOptions[0].Label != "Logs" {
		t.Fatalf("clone shares selected options: %#v", original)
	}
}

func TestDecodeInputRejectsReservedCustomOptionID(t *testing.T) {
	_, err := decodeInput(json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"custom_option","label":"Other"}]}`))
	if err == nil || err.Error() != `option id "custom_option" is reserved` {
		t.Fatalf("error=%v", err)
	}
}
```

将 `internal/tool/select/tool_test.go` 中两个 JSON 测试改为：

```go
func TestToolRunReturnsStableSubmittedJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{SelectedOptions: []SelectedOption{
			{ID: "metrics", Label: "Metrics"},
			{ID: "logs", Label: "Logs"},
		}})
	}()
	got, err := x.Run(context.Background(), json.RawMessage(`{"prompt":"Choose","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}`))
	if err != nil || got != `{"cancelled":false,"selected_options":[{"id":"metrics","label":"Metrics"},{"id":"logs","label":"Logs"}]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if strings.Contains(got, "selected_ids") {
		t.Fatalf("legacy field leaked: %s", got)
	}
}

func TestToolRunReturnsCancellationJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{Cancelled: true})
	}()
	got, err := x.Run(context.Background(), validSingleInput())
	if err != nil || got != `{"cancelled":true,"selected_options":[]}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
}
```

将 `internal/tool/select/broker_test.go` 中用于完成请求的值统一改为：

```go
Result{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}}
```

并将结果断言改为：

```go
if got := <-resultCh; !reflect.DeepEqual(got.SelectedOptions, []SelectedOption{{ID: "a", Label: "A"}}) {
	t.Fatalf("got=%#v", got)
}
```

- [ ] **步骤 2：运行协议测试验证失败**

运行：

```bash
go test ./internal/tool/select -run 'Test(ResultCloneCopiesSelectedOptions|DecodeInputRejectsReservedCustomOptionID|ToolRunReturnsStableSubmittedJSON|ToolRunReturnsCancellationJSON|BrokerPublishesAndCompletesRequest)' -v
```

预期：编译失败，提示 `SelectedOption`、`SelectedOptions` 或 `CustomOptionID` 未定义。

- [ ] **步骤 3：实现新的结果类型和保留 ID**

将 `internal/tool/select/types.go` 中结果部分改为：

```go
const CustomOptionID = "custom_option"

type SelectedOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Result struct {
	Cancelled       bool             `json:"cancelled"`
	SelectedOptions []SelectedOption `json:"selected_options"`
}

func (r Result) Clone() Result {
	if r.SelectedOptions == nil {
		r.SelectedOptions = nil
	} else {
		r.SelectedOptions = append([]SelectedOption{}, r.SelectedOptions...)
	}
	return r
}
```

在 `internal/tool/select/input.go` 的选项 ID 非空校验后加入：

```go
if options[i].ID == CustomOptionID {
	return Request{}, fmt.Errorf("option id %q is reserved", CustomOptionID)
}
```

在 `internal/tool/select/tool.go` 中将空数组归一化改为：

```go
if result.SelectedOptions == nil {
	result.SelectedOptions = []SelectedOption{}
}
```

- [ ] **步骤 4：运行 Select 包测试验证通过**

运行：

```bash
go test ./internal/tool/select -v
```

预期：PASS，且测试输出中不再出现生产结果字段 `selected_ids`。

- [ ] **步骤 5：确认生产代码没有遗留旧结果字段**

运行：

```bash
grep -R "SelectedIDs\|selected_ids" internal/tool/select internal/ui/bubble --include='*.go'
```

预期：此时只允许请求侧的 `InitialSelectedIDs` / `initial_selected_ids` 和尚未迁移的 Bubble 结果引用存在；`internal/tool/select/types.go`、`tool.go` 不得再出现结果字段 `SelectedIDs`。

- [ ] **步骤 6：Commit**

```bash
git add internal/tool/select/types.go internal/tool/select/input.go internal/tool/select/input_test.go internal/tool/select/tool.go internal/tool/select/tool_test.go internal/tool/select/broker_test.go
git commit -m "feat: replace Select result protocol"
```

---

### 任务 2：建立 dock 焦点模型和稳定结果构造

**文件：**
- 修改：`internal/ui/bubble/selection_dock.go:16-171`
- 测试：`internal/ui/bubble/selection_dock_test.go`

- [ ] **步骤 1：编写导航、预设答案和讨论取消的失败测试**

将 `internal/ui/bubble/selection_dock_test.go` 中结果断言迁移到 `SelectedOptions`，并增加：

```go
func TestSelectionDockNavigatesAnswersAndFixedActions(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatalf("initial focus=%#v", d.focus)
	}
	d.end()
	if d.focus.kind != selectionFocusChat {
		t.Fatalf("end focus=%#v, want chat action", d.focus)
	}
	d.move(-1)
	if d.focus.kind != selectionFocusCustom {
		t.Fatalf("focus=%#v, want custom action", d.focus)
	}
	d.move(-1)
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 2 {
		t.Fatalf("focus=%#v, want final answer", d.focus)
	}
	d.home()
	if d.focus.kind != selectionFocusAnswer || d.focus.answerIndex != 0 {
		t.Fatalf("home focus=%#v", d.focus)
	}
}

func TestSelectionDockBuildsStableReadableOptions(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.selected["traces"] = true
	d.selected["logs"] = true
	result, ok := d.submit()
	want := []selecttool.SelectedOption{
		{ID: "logs", Label: "Logs"},
		{ID: "traces", Label: "Traces"},
	}
	if !ok || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", result, ok)
	}
}

func TestSelectionDockChatUsesCancellationResult(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.focus = selectionFocus{kind: selectionFocusChat}
	result, complete := d.activateFocused()
	if !complete || !result.Cancelled || result.SelectedOptions == nil || len(result.SelectedOptions) != 0 {
		t.Fatalf("result=%#v complete=%v", result, complete)
	}
}
```

在测试 import 中加入：

```go
"reflect"
```

- [ ] **步骤 2：运行 dock 状态测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(NavigatesAnswersAndFixedActions|BuildsStableReadableOptions|ChatUsesCancellationResult|ToggleAndSubmit|SingleSubmit)' -v
```

预期：编译失败，提示 `selectionFocus`、`focus`、`activateFocused` 或 `SelectedOptions` 相关实现缺失。

- [ ] **步骤 3：实现三类焦点和结果构造**

在 `internal/ui/bubble/selection_dock.go` 增加：

```go
type selectionFocusKind uint8

const (
	selectionFocusAnswer selectionFocusKind = iota
	selectionFocusCustom
	selectionFocusChat
)

type selectionFocus struct {
	kind        selectionFocusKind
	answerIndex int
}
```

将 `selectionDock` 改为：

```go
type selectionDock struct {
	request      selecttool.Request
	focus        selectionFocus
	selected     map[string]bool
	customLabel  string
	firstVisible int
	errorText    string
}
```

在 `newSelectionDock` 中用初始单选 ID 设置 `focus.answerIndex`；默认焦点为第 0 个答案。

实现统一导航位置：

```go
func (d *selectionDock) focusPosition() int {
	switch d.focus.kind {
	case selectionFocusCustom:
		return len(d.request.Options)
	case selectionFocusChat:
		return len(d.request.Options) + 1
	default:
		return clampInt(d.focus.answerIndex, 0, len(d.request.Options)-1)
	}
}

func (d *selectionDock) setFocusPosition(position int) {
	last := len(d.request.Options) + 1
	position = clampInt(position, 0, last)
	switch {
	case position < len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: position}
	case position == len(d.request.Options):
		d.focus = selectionFocus{kind: selectionFocusCustom}
	default:
		d.focus = selectionFocus{kind: selectionFocusChat}
	}
	d.errorText = ""
}

func (d *selectionDock) move(delta int) { d.setFocusPosition(d.focusPosition() + delta) }
func (d *selectionDock) home()          { d.setFocusPosition(0) }
func (d *selectionDock) end()           { d.setFocusPosition(len(d.request.Options) + 1) }
```

将预设答案结果构造集中为：

```go
func (d *selectionDock) selectedCount() int {
	count := len(d.selected)
	if strings.TrimSpace(d.customLabel) != "" {
		count++
	}
	return count
}

func (d *selectionDock) selectedOptions() []selecttool.SelectedOption {
	out := make([]selecttool.SelectedOption, 0, d.selectedCount())
	for _, option := range d.request.Options {
		if d.selected[option.ID] {
			out = append(out, selecttool.SelectedOption{ID: option.ID, Label: option.Label})
		}
	}
	if label := strings.TrimSpace(d.customLabel); label != "" {
		out = append(out, selecttool.SelectedOption{ID: selecttool.CustomOptionID, Label: label})
	}
	return out
}
```

单选预设答案提交返回当前 `Option` 的 `ID` 和 `Label`；多选提交使用 `selectedCount()` 校验并返回 `selectedOptions()`；取消固定返回：

```go
func (d *selectionDock) cancel() selecttool.Result {
	return selecttool.Result{Cancelled: true, SelectedOptions: []selecttool.SelectedOption{}}
}
```

`toggleHighlighted` 仅在 `focus.kind == selectionFocusAnswer` 时操作答案。

实现操作分发的骨架：

```go
func (d *selectionDock) activateFocused() (selecttool.Result, bool) {
	switch d.focus.kind {
	case selectionFocusChat:
		return d.cancel(), true
	case selectionFocusAnswer:
		if d.request.Mode == selecttool.ModeSingle {
			option := d.request.Options[d.focus.answerIndex]
			return selecttool.Result{SelectedOptions: []selecttool.SelectedOption{{ID: option.ID, Label: option.Label}}}, true
		}
		d.toggleHighlighted()
		return selecttool.Result{}, false
	default:
		return selecttool.Result{}, false
	}
}
```

- [ ] **步骤 4：运行 dock 状态测试验证通过**

运行：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(NavigatesAnswersAndFixedActions|BuildsStableReadableOptions|ChatUsesCancellationResult|ToggleAndSubmit|StableOrder|SingleSubmit)' -v
```

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go
git commit -m "feat: add Select dock action focus model"
```

---

### 任务 3：实现 Custom option 原地输入

**文件：**
- 修改：`internal/ui/bubble/selection_dock.go`
- 测试：`internal/ui/bubble/selection_dock_test.go`

- [ ] **步骤 1：编写自定义答案的失败测试**

在 `internal/ui/bubble/selection_dock_test.go` 增加：

```go
func TestSelectionDockSingleCustomOptionSubmitsImmediately(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeSingle))
	d.focus = selectionFocus{kind: selectionFocusCustom}
	if result, complete := d.activateFocused(); complete || len(result.SelectedOptions) != 0 || !d.editingCustom {
		t.Fatalf("activation result=%#v complete=%v dock=%#v", result, complete, d)
	}
	d.customInput.SetValue("  Custom answer  ")
	result, complete := d.confirmCustom()
	want := []selecttool.SelectedOption{{ID: selecttool.CustomOptionID, Label: "Custom answer"}}
	if !complete || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v complete=%v", result, complete)
	}
}

func TestSelectionDockMultipleCustomOptionAddsAndEditsOneAnswer(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("First custom")
	if result, complete := d.confirmCustom(); complete || len(result.SelectedOptions) != 0 {
		t.Fatalf("unexpected completion: %#v %v", result, complete)
	}
	if d.customLabel != "First custom" || d.selectedCount() != 2 {
		t.Fatalf("dock=%#v", d)
	}
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	if d.customInput.Value() != "First custom" {
		t.Fatalf("prefill=%q", d.customInput.Value())
	}
	d.customInput.SetValue("Edited custom")
	d.confirmCustom()
	result, ok := d.submit()
	want := []selecttool.SelectedOption{
		{ID: "logs", Label: "Logs"},
		{ID: selecttool.CustomOptionID, Label: "Edited custom"},
	}
	if !ok || !reflect.DeepEqual(result.SelectedOptions, want) {
		t.Fatalf("result=%#v ok=%v", result, ok)
	}
}

func TestSelectionDockCustomOptionValidatesEmptyAndMax(t *testing.T) {
	d := newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.request.MaxSelect = 1
	d.selected["logs"] = true
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("Extra")
	if _, complete := d.confirmCustom(); complete || d.errorText != "You can select at most 1 option." {
		t.Fatalf("dock=%#v", d)
	}

	d = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	d.focus = selectionFocus{kind: selectionFocusCustom}
	d.activateFocused()
	d.customInput.SetValue("   ")
	if _, complete := d.confirmCustom(); complete || d.errorText != "Custom option cannot be empty." {
		t.Fatalf("dock=%#v", d)
	}
}
```

- [ ] **步骤 2：运行自定义答案测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomOptionSubmitsImmediately|MultipleCustomOptionAddsAndEditsOneAnswer|CustomOptionValidatesEmptyAndMax)' -v
```

预期：编译失败，提示 `editingCustom`、`customInput` 或 `confirmCustom` 未定义。

- [ ] **步骤 3：在 dock 中加入独立 textinput 状态**

在 `internal/ui/bubble/selection_dock.go` import 中加入：

```go
"strings"

"github.com/charmbracelet/bubbles/textinput"
```

向 `selectionDock` 增加：

```go
customInput   textinput.Model
editingCustom bool
```

在 `newSelectionDock` 中初始化：

```go
customInput := textinput.New()
customInput.Prompt = ""
customInput.Placeholder = "Type a custom answer"
customInput.CharLimit = 0
customInput.Width = 40

d := &selectionDock{
	request:     request.Clone(),
	selected:    selected,
	customInput: customInput,
}
```

实现：

```go
func (d *selectionDock) beginCustomEdit() {
	if d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return
	}
	d.customInput.SetValue(d.customLabel)
	d.customInput.CursorEnd()
	d.customInput.Focus()
	d.editingCustom = true
	d.errorText = ""
}

func (d *selectionDock) cancelCustomEdit() {
	d.editingCustom = false
	d.customInput.Blur()
	d.errorText = ""
}

func (d *selectionDock) confirmCustom() (selecttool.Result, bool) {
	label := strings.TrimSpace(d.customInput.Value())
	if label == "" {
		d.errorText = "Custom option cannot be empty."
		return selecttool.Result{}, false
	}
	if d.customLabel == "" && d.selectedCount() >= d.request.MaxSelect {
		d.errorText = fmt.Sprintf("You can select at most %d %s.", d.request.MaxSelect, optionNoun(d.request.MaxSelect))
		return selecttool.Result{}, false
	}
	d.customLabel = label
	d.cancelCustomEdit()
	if d.request.Mode == selecttool.ModeSingle {
		return selecttool.Result{SelectedOptions: []selecttool.SelectedOption{{ID: selecttool.CustomOptionID, Label: label}}}, true
	}
	return selecttool.Result{}, false
}

func optionNoun(count int) string {
	if count == 1 {
		return "option"
	}
	return "options"
}
```

将 `activateFocused` 的 custom 分支改为调用 `beginCustomEdit()`。

- [ ] **步骤 4：将自定义输入接入 Bubble Tea 按键处理**

在 `handleSelectionDockKey` 开头增加：

```go
if m.selectionDock.editingCustom {
	switch msg.String() {
	case "enter":
		if result, complete := m.selectionDock.confirmCustom(); complete {
			cmd := m.completeSelection(result)
			m.relayout()
			return m, cmd
		}
	case "esc":
		m.selectionDock.cancelCustomEdit()
	case "ctrl+c":
		cmd := m.completeSelection(m.selectionDock.cancel())
		m.relayout()
		return m, cmd
	default:
		var inputCmd tea.Cmd
		m.selectionDock.customInput, inputCmd = m.selectionDock.customInput.Update(msg)
		m.selectionDock.errorText = ""
		m.relayout()
		return m, inputCmd
	}
	m.relayout()
	return m, nil
}
```

普通状态下：

- `Space` 在答案焦点时切换多选，在操作焦点时调用 `activateFocused()`。
- `Enter` 在多选答案焦点时调用 `submit()`；在单选答案或两个操作焦点时调用 `activateFocused()`。
- `Chat about this` 的完成路径与 `Esc` 共用 `completeSelection(cancel())`。

- [ ] **步骤 5：运行自定义答案和按键测试**

运行：

```bash
go test ./internal/ui/bubble -run 'TestSelectionDock(SingleCustomOptionSubmitsImmediately|MultipleCustomOptionAddsAndEditsOneAnswer|CustomOptionValidatesEmptyAndMax|BrokerRequestAndKeys|ConsumesCtrlCAsCancellation)' -v
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go
git commit -m "feat: add inline custom Select answers"
```

---

### 任务 4：重做 Select dock 渲染、滚动说明和独立高度

**文件：**
- 修改：`internal/ui/bubble/selection_dock_render.go`
- 修改：`internal/ui/bubble/layout.go:34-84`
- 测试：`internal/ui/bubble/selection_dock_test.go`

- [ ] **步骤 1：编写结构化 UI 和滚动范围的失败测试**

更新 `TestRenderSelectionDock`：

```go
func TestRenderSelectionDock(t *testing.T) {
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	plain := ansi.Strip(m.renderSelectionDock(60, 14))
	for _, want := range []string{
		"SELECT · MULTIPLE",
		"Choose signals",
		"3 answers",
		"Custom option",
		"Chat about this",
		"space toggle",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in %q", want, plain)
		}
	}
}
```

将长列表测试改为：

```go
func TestSelectionDockLongListShowsExactRangeAndRemainingCounts(t *testing.T) {
	r := selectionRequest("x", selecttool.ModeMultiple)
	r.Options = nil
	for i := 0; i < 12; i++ {
		r.Options = append(r.Options, selecttool.Option{ID: fmt.Sprintf("id-%d", i), Label: fmt.Sprintf("Option %d", i)})
	}
	r.MaxSelect = len(r.Options)
	m := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	m.selectionDock = newSelectionDock(r)
	first := ansi.Strip(m.renderSelectionDock(48, 14))
	if !strings.Contains(first, "12 answers") || !strings.Contains(first, "showing 1-") || !strings.Contains(first, "answers below") {
		t.Fatalf("first page=%q", first)
	}
	if !strings.Contains(first, "Custom option") || !strings.Contains(first, "Chat about this") {
		t.Fatalf("fixed actions missing=%q", first)
	}
	m.selectionDock.focus = selectionFocus{kind: selectionFocusAnswer, answerIndex: 11}
	last := ansi.Strip(m.renderSelectionDock(48, 14))
	if !strings.Contains(last, "answers above") || !strings.Contains(last, "Option 11") {
		t.Fatalf("last page=%q", last)
	}
	if strings.Contains(last, "more") {
		t.Fatalf("ambiguous more indicator remains=%q", last)
	}
}
```

增加高度隔离测试：

```go
func TestCurrentLayoutAllowsTallerSelectionDockWithoutChangingInputLimit(t *testing.T) {
	selection := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	selection.ready = true
	selection.width = 100
	selection.height = 32
	selection.selectionDock = newSelectionDock(selectionRequest("x", selecttool.ModeMultiple))
	selectionLayout := selection.currentLayout()
	if selectionLayout.inputHeight <= inputMaxVisibleLines {
		t.Fatalf("selection input height=%d, want above normal max=%d", selectionLayout.inputHeight, inputMaxVisibleLines)
	}

	normal := newModel(context.Background(), &fakeRunner{}, "", nil, nil, nil, nil, nil)
	normal.ready = true
	normal.width = 100
	normal.height = 32
	normal.input.SetHeight(inputMaxVisibleLines + 8)
	normalLayout := normal.currentLayout()
	if normalLayout.inputHeight != inputMaxVisibleLines {
		t.Fatalf("normal input height=%d, want %d", normalLayout.inputHeight, inputMaxVisibleLines)
	}
}
```

- [ ] **步骤 2：运行渲染和布局测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(RenderSelectionDock|SelectionDockLongListShowsExactRangeAndRemainingCounts|CurrentLayoutAllowsTallerSelectionDockWithoutChangingInputLimit|SelectionDockTinyTerminalDoesNotOverflow)' -v
```

预期：旧 UI 缺少固定操作和精确范围；布局高度仍被 `inputMaxVisibleLines` 限制。

- [ ] **步骤 3：为布局增加 Select 专用高度上限**

在 `internal/ui/bubble/layout.go` 将纯布局函数拆为：

```go
func computeTUILayout(width, height, requestedInputHeight int) tuiLayout {
	return computeTUILayoutWithInputLimit(width, height, requestedInputHeight, inputMaxVisibleLines)
}

func computeTUILayoutWithInputLimit(width, height, requestedInputHeight, inputHeightLimit int) tuiLayout {
	frameWidth := maxInt(1, width)
	frameHeight := maxInt(1, height)
	contentWidth := maxInt(1, frameWidth-mainFrameHorizontalFrame)
	contentHeight := maxInt(1, frameHeight-mainFrameVerticalFrame)

	headerHeight := 0
	if contentHeight >= 4 {
		headerHeight = dockStatusHeight
	}
	statusHeight := 0
	if contentHeight-headerHeight >= 2 {
		statusHeight = dockStatusHeight
	}
	worktreeHeight := 0
	inputHeightLimit = maxInt(1, inputHeightLimit)
	inputHeight := clampInt(requestedInputHeight, 1, inputHeightLimit)
	maxInputHeight := maxInt(1, contentHeight-headerHeight-statusHeight-worktreeHeight-1)
	inputHeight = minInt(inputHeight, maxInputHeight)
	if inputHeight+headerHeight+statusHeight+worktreeHeight > contentHeight {
		inputHeight = maxInt(1, contentHeight-headerHeight-statusHeight-worktreeHeight)
	}
	transcriptHeight := maxInt(0, contentHeight-headerHeight-statusHeight-worktreeHeight-inputHeight)
	return tuiLayout{
		frameWidth: frameWidth, frameHeight: frameHeight,
		contentWidth: contentWidth, contentHeight: contentHeight,
		headerHeight: headerHeight, transcriptHeight: transcriptHeight,
		statusHeight: statusHeight, worktreeHeight: worktreeHeight,
		inputHeight: inputHeight,
	}
}
```

将 `selectionDockMaxVisibleLines` 设为 `16`，并在 `currentLayout` 的 Select 分支调用：

```go
base := computeTUILayout(m.width, m.height, inputMinVisibleLines)
inputHeight := m.selectionDock.preferredHeight(inputDockContentWidth(base.contentWidth))
return computeTUILayoutWithInputLimit(m.width, m.height, inputHeight, selectionDockMaxVisibleLines)
```

普通输入仍调用 `computeTUILayout`。

- [ ] **步骤 4：实现结构化 dock 渲染**

在 `internal/ui/bubble/selection_dock_render.go` 按固定区块计算预算：

```go
const selectionDockMaxVisibleLines = 16
```

渲染规则必须精确为：

```text
SELECT · MULTIPLE                         selected 2 / max 3
Choose signals
3 answers · showing 1-3 · choose at least 1
[answer rows with descriptions]
↑ 2 answers above                         ↓ 4 answers below
────────────────────────────────────────────────────────────
  + Custom option
  ◌ Chat about this
↑↓ move  space toggle  enter submit  esc cancel
```

实现时：

- 标题使用 `strings.ToUpper(string(d.request.Mode))`。
- `answerStatusLine(total,start,end,min,max,selected)` 只统计 `request.Options`。
- `visibleRange` 只接收答案行高度；当焦点位于系统操作时，用最近一次答案索引维持当前滚动页。
- 操作区始终预留 3 行：分隔线、Custom、Chat。
- 自定义编辑状态将 Custom 行替换为 `Custom option  ` 加 `d.customInput.View()`，Chat 行仍保留。
- 上下滚动提示使用精确数量：`↑ N answers above`、`↓ N answers below`。
- 不再渲染 `↑ more` 或 `↓ more`。
- 高亮答案使用 `m.styles.Selected`；已选但未高亮答案以 `[x]` 标记并使用 `m.styles.Unselected`，从而不只依赖颜色。
- Custom 和 Chat 根据 `d.focus.kind` 分别使用 `Selected` 或 `Unselected`。
- 错误行优先替换快捷键行，避免面板高度跳动。

- [ ] **步骤 5：运行渲染、布局和固定帧测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(RenderSelectionDock|SelectionDockLongListShowsExactRangeAndRemainingCounts|CurrentLayoutAllowsTallerSelectionDockWithoutChangingInputLimit|CurrentLayoutUsesSelectionDock|SelectionDockTinyTerminalDoesNotOverflow)' -v
```

预期：PASS；每个终端尺寸仍严格满足固定宽高。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/selection_dock_render.go internal/ui/bubble/layout.go internal/ui/bubble/selection_dock_test.go
git commit -m "feat: redesign Select dock rendering"
```

---

### 任务 5：实现 Select 专用 transcript 摘要与展开详情

**文件：**
- 创建：`internal/ui/bubble/select_tool_detail.go`
- 创建：`internal/ui/bubble/select_tool_detail_test.go`
- 修改：`internal/ui/bubble/types.go:48-73`
- 修改：`internal/ui/bubble/transcript.go:25-54,252-332,589-635,913-948`
- 修改：`internal/ui/bubble/utils.go:54-138`
- 修改：`internal/ui/bubble/select_tool_display_test.go`

- [ ] **步骤 1：编写 Select 详情格式化器的失败测试**

创建 `internal/ui/bubble/select_tool_detail_test.go`：

```go
package bubble

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelectToolPresentationSubmitted(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Which animals are mammals?","mode":"multiple","options":[{"id":"whale","label":"Whale","description":"Breathes with lungs"},{"id":"shark","label":"Shark","description":"A fish"}]}`)
	result := `{"cancelled":false,"selected_options":[{"id":"whale","label":"Whale"},{"id":"custom_option","label":"Platypus"}]}`
	presentation, ok := parseSelectToolPresentation(input, result)
	if !ok {
		t.Fatal("presentation did not parse")
	}
	if presentation.target != "selected 2 options" {
		t.Fatalf("target=%q", presentation.target)
	}
	want := "Which animals are mammals?\n\nWhale\n  Breathes with lungs\n\nPlatypus\n  Custom option"
	if presentation.detail != want {
		t.Fatalf("detail=%q want=%q", presentation.detail, want)
	}
	if strings.Contains(presentation.detail, "Shark") || strings.Contains(presentation.detail, "selected_options") {
		t.Fatalf("unselected or raw JSON leaked: %q", presentation.detail)
	}
}

func TestSelectToolPresentationCancelled(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	presentation, ok := parseSelectToolPresentation(input, `{"cancelled":true,"selected_options":[]}`)
	if !ok || presentation.target != "cancelled" || presentation.detail != "Pick\n\nSelection cancelled." {
		t.Fatalf("presentation=%#v ok=%v", presentation, ok)
	}
}

func TestSelectToolPresentationRejectsMalformedInputOrResult(t *testing.T) {
	if _, ok := parseSelectToolPresentation(json.RawMessage(`{"prompt":`), `{}`); ok {
		t.Fatal("malformed input accepted")
	}
	if _, ok := parseSelectToolPresentation(json.RawMessage(`{"prompt":"Pick","options":[]}`), `{"selected_options":`); ok {
		t.Fatal("malformed result accepted")
	}
}
```

- [ ] **步骤 2：运行格式化器测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'TestSelectToolPresentation' -v
```

预期：编译失败，提示 `parseSelectToolPresentation` 未定义。

- [ ] **步骤 3：实现 Select 专用解析和详情生成**

创建 `internal/ui/bubble/select_tool_detail.go`：

```go
package bubble

import (
	"encoding/json"
	"fmt"
	"strings"

	selecttool "paw/internal/tool/select"
)

type selectToolPresentation struct {
	target string
	detail string
}

func parseSelectToolPresentation(input json.RawMessage, content string) (selectToolPresentation, bool) {
	var request struct {
		Prompt  string              `json:"prompt"`
		Options []selecttool.Option `json:"options"`
	}
	if err := json.Unmarshal(input, &request); err != nil || strings.TrimSpace(request.Prompt) == "" {
		return selectToolPresentation{}, false
	}
	var result selecttool.Result
	if err := json.Unmarshal([]byte(content), &result); err != nil || result.SelectedOptions == nil {
		return selectToolPresentation{}, false
	}
	prompt := strings.TrimSpace(request.Prompt)
	if result.Cancelled {
		return selectToolPresentation{target: "cancelled", detail: prompt + "\n\nSelection cancelled."}, true
	}

	descriptions := make(map[string]string, len(request.Options))
	for _, option := range request.Options {
		descriptions[option.ID] = strings.TrimSpace(option.Description)
	}
	parts := []string{prompt}
	for _, selected := range result.SelectedOptions {
		label := strings.TrimSpace(selected.Label)
		if label == "" {
			continue
		}
		block := label
		description := descriptions[selected.ID]
		if selected.ID == selecttool.CustomOptionID {
			description = "Custom option"
		}
		if description != "" {
			block += "\n  " + description
		}
		parts = append(parts, block)
	}
	count := len(result.SelectedOptions)
	return selectToolPresentation{
		target: fmt.Sprintf("selected %d %s", count, optionNoun(count)),
		detail: strings.Join(parts, "\n\n"),
	}, true
}
```

- [ ] **步骤 4：让 transcript 条目保留原始工具输入**

在 `internal/ui/bubble/types.go` 的 `transcriptEntry` 增加：

```go
toolInput json.RawMessage
```

在 `transcriptRenderCacheKey` 增加：

```go
toolInput string
```

在 `recordToolCallEntry` 创建事务时写入：

```go
toolInput: append(json.RawMessage(nil), input...),
```

在 `transcriptRenderKey` 写入：

```go
toolInput: string(entry.toolInput),
```

- [ ] **步骤 5：改造实时结果摘要和展开详情**

在 `recordToolResultEntry` 完成匹配后，用专用解析器更新 target：

```go
if strings.EqualFold(name, "Select") {
	if presentation, ok := parseSelectToolPresentation(entry.toolInput, content); ok {
		entry.toolTarget = presentation.target
	}
}
```

将 `renderToolTransactionEntry` 的详情来源改为：

```go
result := entry.toolResult
if strings.EqualFold(toolEntryDisplayName(entry), "Select") {
	if presentation, ok := parseSelectToolPresentation(entry.toolInput, entry.toolResult); ok {
		result = presentation.detail
	}
}
result = renderTerminalLinks(sanitizeTerminalText(result))
```

保持解析失败时使用 `entry.toolResult`，从而安全回退到通用原始结果。

- [ ] **步骤 6：更新 Select 折叠摘要工具函数**

在 `internal/ui/bubble/utils.go`：

- 删除对 `selecttool.Result.SelectedIDs` 的使用。
- `completeToolCallBody` 对 Select 只替换第一行为：

```go
summary := "Select · cancelled"
if !result.Cancelled {
	count := len(result.SelectedOptions)
	summary = fmt.Sprintf("Select · selected %d %s", count, optionNoun(count))
}
```

- `selectToolResultTarget` 使用 `len(result.SelectedOptions)`。
- 保持 `formatSelectToolCallBody` 在运行阶段隐藏完整 options payload。

更新 `internal/ui/bubble/select_tool_display_test.go` 中的 JSON 为：

```json
{"cancelled":false,"selected_options":[{"id":"a","label":"A"}]}
```

并保持折叠摘要断言只检查 `selected N option(s)`，不包含答案标签。

- [ ] **步骤 7：运行 Select 详情和摘要测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(SelectToolPresentation|FormatSelectToolCallBodyHidesOptionPayload|CompleteSelectToolCallBodySummarizesResult|SelectToolDisplayTargetAndResultSummary)' -v
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/ui/bubble/select_tool_detail.go internal/ui/bubble/select_tool_detail_test.go internal/ui/bubble/types.go internal/ui/bubble/transcript.go internal/ui/bubble/utils.go internal/ui/bubble/select_tool_display_test.go
git commit -m "feat: render readable Select transcript details"
```

---

### 任务 6：支持历史 Select 事务恢复并锁定单行折叠行为

**文件：**
- 修改：`internal/ui/bubble/subagent_picker.go:195-304`
- 修改：`internal/ui/bubble/tool_track_test.go`

- [ ] **步骤 1：编写实时和历史 Select transcript 的失败测试**

在 `internal/ui/bubble/tool_track_test.go` 增加：

```go
func TestSelectToolTrackCollapsesToOneLineAndExpandsReadableDetail(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"prompt":"Which animals are mammals?","mode":"multiple","options":[{"id":"whale","label":"Whale","description":"Breathes with lungs"},{"id":"shark","label":"Shark","description":"A fish"}]}`)
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "select-1", Name: "Select", Input: input}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "select-1",
		Name:      "Select",
		Content:   `{"cancelled":false,"selected_options":[{"id":"whale","label":"Whale"},{"id":"custom_option","label":"Platypus"}]}`,
	}))
	model = next.(appModel)

	collapsed := ansi.Strip(renderTranscript(model.transcript, 100, true))
	if len(strings.Split(strings.TrimSpace(collapsed), "\n")) != 1 {
		t.Fatalf("collapsed Select uses multiple rows:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "Select: selected 2 options") || strings.Contains(collapsed, "Whale") || strings.Contains(collapsed, "Platypus") {
		t.Fatalf("collapsed Select=%q", collapsed)
	}

	if !model.toggleToolExpansion(0) {
		t.Fatal("Select transaction did not expand")
	}
	expanded := ansi.Strip(renderTranscript(model.transcript, 100, true))
	for _, want := range []string{"Which animals are mammals?", "Whale", "Breathes with lungs", "Platypus", "Custom option"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded Select missing %q:\n%s", want, expanded)
		}
	}
	for _, unwanted := range []string{"Shark", "selected_options", `"cancelled"`} {
		if strings.Contains(expanded, unwanted) {
			t.Fatalf("expanded Select leaked %q:\n%s", unwanted, expanded)
		}
	}
}

func TestHistoricalSelectTransactionPreservesInputForReadableExpansion(t *testing.T) {
	callEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID: "select-1", Name: "Select",
			Input: json.RawMessage(`{"prompt":"Pick a signal","mode":"single","options":[{"id":"logs","label":"Logs","description":"Application logs"}]}`),
		}},
	}, time.Now(), "")
	resultEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleUser,
		ToolResults: []message.ToolResult{{
			ToolUseID: "select-1",
			Content:   `{"cancelled":false,"selected_options":[{"id":"logs","label":"Logs"}]}`,
		}},
	}, time.Now(), "")
	merged := mergeTranscriptToolEntries(append(callEntries, resultEntries...))
	if len(merged) != 1 || string(merged[0].toolInput) == "" || merged[0].toolTarget != "selected 1 option" {
		t.Fatalf("merged=%#v", merged)
	}
	merged[0].toolExpanded = true
	rendered := ansi.Strip(renderTranscript(merged, 100, true))
	if !strings.Contains(rendered, "Pick a signal") || !strings.Contains(rendered, "Application logs") {
		t.Fatalf("historical Select detail missing:\n%s", rendered)
	}
}
```

- [ ] **步骤 2：运行 transcript 测试验证失败**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(SelectToolTrackCollapsesToOneLineAndExpandsReadableDetail|HistoricalSelectTransactionPreservesInputForReadableExpansion)' -v
```

预期：实时测试可能缺少输入保留或摘要仍含多行；历史测试缺少 `toolInput` / 更新后的 target。

- [ ] **步骤 3：在历史事务中保留 Select 输入并完成结果格式化**

在 `transcriptEntriesFromMessage` 创建调用条目时增加：

```go
toolInput: append(json.RawMessage(nil), call.Input...),
```

并对 Select 使用与实时路径一致的运行 target：

```go
target := displayToolTarget(name, call.Input, workspaceRoot)
if selectTarget, ok := selectToolCallTarget(name, call.Input); ok {
	target = selectTarget
}
```

在 `mergeTranscriptToolEntries` 将结果并入调用条目后增加：

```go
call.body = completeToolCallBody(call.toolName, call.body, entry.toolStatus, entry.toolResult)
if strings.EqualFold(call.toolName, "Select") {
	if presentation, ok := parseSelectToolPresentation(call.toolInput, call.toolResult); ok {
		call.toolTarget = presentation.target
	}
}
```

不要用 `completeRunningToolCallBody` 覆盖 Select 的选择数量摘要。

- [ ] **步骤 4：运行工具轨道测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(SelectToolTrack|HistoricalSelectTransaction|HistoricalToolCallAndResultMergeByID|ToolTrackLifecycleAndResultVisibility)' -v
```

预期：PASS；普通 Read/Bash 工具渲染测试保持不变。

- [ ] **步骤 5：Commit**

```bash
git add internal/ui/bubble/subagent_picker.go internal/ui/bubble/tool_track_test.go
git commit -m "feat: restore readable historical Select results"
```

---

### 任务 7：完成全量迁移与回归验证

**文件：**
- 修改：所有仍引用旧 Select 结果字段的测试文件
- 验证：`internal/tool/select/**`
- 验证：`internal/ui/bubble/**`
- 验证：`internal/loop/**`

- [ ] **步骤 1：扫描并移除旧结果协议引用**

运行：

```bash
grep -R "SelectedIDs\|selected_ids" internal --include='*.go'
```

预期：只允许请求初始化字段 `InitialSelectedIDs` 和 JSON 请求字段 `initial_selected_ids`。任何 `Result.SelectedIDs`、结果 JSON `selected_ids` 或相关断言都必须迁移到 `SelectedOptions` / `selected_options`。

- [ ] **步骤 2：运行格式化和静态检查**

运行：

```bash
gofmt -w \
  internal/tool/select/types.go \
  internal/tool/select/input.go \
  internal/tool/select/input_test.go \
  internal/tool/select/tool.go \
  internal/tool/select/tool_test.go \
  internal/tool/select/broker_test.go \
  internal/ui/bubble/selection_dock.go \
  internal/ui/bubble/selection_dock_render.go \
  internal/ui/bubble/selection_dock_test.go \
  internal/ui/bubble/layout.go \
  internal/ui/bubble/types.go \
  internal/ui/bubble/transcript.go \
  internal/ui/bubble/subagent_picker.go \
  internal/ui/bubble/utils.go \
  internal/ui/bubble/select_tool_display_test.go \
  internal/ui/bubble/select_tool_detail.go \
  internal/ui/bubble/select_tool_detail_test.go \
  internal/ui/bubble/tool_track_test.go

git diff --check
```

预期：无输出，退出码为 0。

- [ ] **步骤 3：运行目标包测试**

运行：

```bash
go test ./internal/tool/select ./internal/ui/bubble ./internal/loop
```

预期：PASS。

- [ ] **步骤 4：运行 race 检查保护 broker 和阻塞交互**

运行：

```bash
go test -race ./internal/tool/select ./internal/ui/bubble
```

预期：PASS，无 data race 报告。

- [ ] **步骤 5：运行全仓测试**

运行：

```bash
go test ./...
```

预期：PASS。

- [ ] **步骤 6：执行窄宽终端和长内容回归测试**

运行：

```bash
go test ./internal/ui/bubble -run 'Test(SelectionDockTinyTerminalDoesNotOverflow|SelectionDockLongListShowsExactRangeAndRemainingCounts|CompactToolSummaryFitsNarrowWidths|CompactToolSummaryKeepsStatusAtNarrowWidths)' -count=1 -v
```

预期：PASS；所有渲染行不超过指定终端宽度，长列表可以导航到最后一个答案，固定操作始终渲染。

- [ ] **步骤 7：Commit**

```bash
git add internal/tool/select internal/ui/bubble internal/loop
git commit -m "test: complete Select redesign regression coverage"
```

---

## 最终验收清单

- [ ] 工具成功结果只包含 `cancelled` 和 `selected_options`。
- [ ] 每个选择结果只有 `id` 和 `label`。
- [ ] `custom_option` 为保留 ID，调用方无法定义同名预设答案。
- [ ] 每次最多保留一个自定义答案；再次进入会编辑已有值。
- [ ] 单选自定义答案按 Enter 立即提交。
- [ ] 多选自定义答案加入当前选择后可继续勾选。
- [ ] `Custom option` 与 `Chat about this` 始终显示且不计入业务答案总数。
- [ ] `Chat about this` 与 `Esc` 返回相同取消协议。
- [ ] 长列表显示答案总数、当前范围和上下剩余数量，不出现含糊的 `more`。
- [ ] 用户可以通过键盘访问请求中的每一个答案。
- [ ] 折叠 Select 事务只占一行，不显示问题或答案标签。
- [ ] 展开 Select 事务只显示原问题、已选答案和可用描述。
- [ ] 展开详情不显示未选答案或原始 JSON 字段。
- [ ] 历史恢复后的 Select 事务与实时事务呈现一致。
- [ ] 非 Select 工具的摘要、展开详情、错误自动展开和历史恢复不回归。
