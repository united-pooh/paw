# Select 阻塞式 TUI 选项工具实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 在主 Bubble Tea 会话中新增阻塞式 `Select` 工具，以底部 Dock 提供单选、多选、预选、数量约束、取消和长列表滚动，并把稳定的选项 ID 返回模型。

**架构：** `internal/tool/select` 提供输入验证、同步工具和并发安全 Broker；Broker 通过事件流把请求交给 Bubble Tea，TUI 使用独立的 `selectionDock` 状态机渲染和处理键盘。主交互模式显式装配并注册该工具，headless 和子代理保持不注册；现有 Runner 的“连续并发安全工具成批执行、非并发安全工具串行执行”机制天然把 `Select` 作为执行屏障。

**技术栈：** Go、Bubble Tea、Lip Gloss、现有 `tool.Tool`/`tool.Registry`/`loop.Runner`、Go `context` 与同步原语、标准库 `encoding/json`、Go testing。

---

## 实现前约束

当前工作区已有与其他功能相关的未提交修改。执行计划时必须先创建或切换到专用 worktree；若无法隔离，则每次编辑共享文件前先运行 `git diff -- <file>`，不得覆盖、回退或整体格式化无关改动。尤其注意当前已修改的：

- `internal/loop/runner.go`
- `internal/loop/runner_test.go`
- `internal/ui/bubble/bubble.go`
- `internal/ui/bubble/bubble_test.go`
- `internal/ui/bubble/styles.go`
- `internal/ui/bubble/transcript.go`
- `internal/ui/bubble/types.go`
- `internal/ui/bubble/utils.go`
- `internal/ui/bubble/tool_display.go`
- `internal/ui/bubble/tool_display_test.go`

设计规格：`docs/superpowers/specs/2026-07-30-select-tool-design.md`。

## 文件结构

### 新建文件

- `internal/tool/select/types.go`：公共 `Mode`、`Option`、`Request`、`Result`、`Event` 类型及克隆辅助函数。
- `internal/tool/select/input.go`：模型输入解码、字段存在性判断、标准化和稳定错误消息。
- `internal/tool/select/broker.go`：FIFO 请求队列、活动请求、事件投递、完成、context 取消和关闭生命周期。
- `internal/tool/select/tool.go`：`Select` 工具元数据、Schema、`Run` 和稳定 JSON 结果编码。
- `internal/tool/select/input_test.go`：输入协议和 Schema 测试。
- `internal/tool/select/broker_test.go`：Broker 顺序、幂等、取消、关闭和竞态测试。
- `internal/tool/select/tool_test.go`：工具端到端提交/取消测试。
- `internal/ui/bubble/selection_dock.go`：Dock 纯状态机、按键行为、选择约束和滚动窗口。
- `internal/ui/bubble/selection_dock_render.go`：Dock 高度计算、主题化渲染和窄终端降级。
- `internal/ui/bubble/selection_dock_test.go`：状态机、消息接入、键盘和渲染测试。

### 修改文件

- `internal/ui/bubble/types.go`：为 `appModel` 增加 Selection Broker 与 Dock 状态。
- `internal/ui/bubble/app.go`：初始化 Broker 监听命令；处理请求/失效消息；在普通输入路径之前分派 Dock 按键。
- `internal/ui/bubble/layout.go`：活动 Dock 决定输入区域高度、替换输入内容并隐藏 textarea 光标锚点。
- `internal/ui/bubble/bubble.go`：为 UI 注入 Broker，并在 `Run` 中传给 `appModel`。
- `internal/ui/bubble/utils.go`：Select 工具调用的紧凑 transcript 格式与完成摘要。
- `internal/ui/bubble/transcript.go`：完成 Select 工具行时使用结构化摘要。
- `internal/ui/bubble/tool_display_test.go`：Select 调用和结果摘要回归测试。
- `cmd/agent/main.go`：仅交互模式创建、注入、注册和关闭 Broker。
- `cmd/agent/register_test.go`：交互注册包含 Select、基础/headless 注册不包含 Select。
- `internal/loop/runner_test.go`：用真实非并发安全语义验证 Select 位置形成工具批次屏障；生产 Runner 代码预计无需修改。

---

### 任务 1：定义 Select 协议并完成输入验证

**文件：**
- 创建：`internal/tool/select/types.go`
- 创建：`internal/tool/select/input.go`
- 创建：`internal/tool/select/input_test.go`

- [ ] **步骤 1：编写协议与合法输入的失败测试**

在 `internal/tool/select/input_test.go` 中建立表驱动测试，先固定最终公开类型和标准化结果：

```go
package selecttool

import (
    "encoding/json"
    "reflect"
    "testing"
)

func TestDecodeInputNormalizesSingleSelection(t *testing.T) {
    raw := json.RawMessage(`{
        "prompt":"  Choose environment  ",
        "mode":"single",
        "options":[
            {"id":"prod","label":" Production ","description":" Live traffic "},
            {"id":"stage","label":"Staging"}
        ],
        "initial_selected_id":"stage"
    }`)

    got, err := decodeInput(raw)
    if err != nil {
        t.Fatalf("decodeInput() error = %v", err)
    }
    want := Request{
        Prompt: "Choose environment",
        Mode:   ModeSingle,
        Options: []Option{
            {ID: "prod", Label: "Production", Description: "Live traffic"},
            {ID: "stage", Label: "Staging"},
        },
        InitialSelectedIDs: []string{"stage"},
        MinSelect:          1,
        MaxSelect:          1,
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("decodeInput() = %#v, want %#v", got, want)
    }
}

func TestDecodeInputDefaultsMultipleBounds(t *testing.T) {
    got, err := decodeInput(json.RawMessage(`{
        "prompt":"Choose signals",
        "mode":"multiple",
        "options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]
    }`))
    if err != nil {
        t.Fatalf("decodeInput() error = %v", err)
    }
    if got.MinSelect != 0 || got.MaxSelect != 2 {
        t.Fatalf("bounds = %d..%d, want 0..2", got.MinSelect, got.MaxSelect)
    }
    if len(got.InitialSelectedIDs) != 0 {
        t.Fatalf("initial ids = %v, want empty", got.InitialSelectedIDs)
    }
}
```

同时加入 `Request.Clone()` 测试，确保 options 和 initial IDs 不共享底层切片。

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/tool/select -run 'TestDecodeInput|TestRequestClone' -count=1
```

预期：FAIL，包或 `Request`、`decodeInput` 尚不存在。

- [ ] **步骤 3：实现公共协议类型**

在 `types.go` 中定义实际使用的类型：

```go
package selecttool

type Mode string

const (
    ModeSingle   Mode = "single"
    ModeMultiple Mode = "multiple"
)

type Option struct {
    ID          string `json:"id"`
    Label       string `json:"label"`
    Description string `json:"description,omitempty"`
}

type Request struct {
    ID                 string   `json:"id,omitempty"`
    Prompt             string   `json:"prompt"`
    Mode               Mode     `json:"mode"`
    Options            []Option `json:"options"`
    InitialSelectedIDs []string `json:"initial_selected_ids,omitempty"`
    MinSelect          int      `json:"min_select"`
    MaxSelect          int      `json:"max_select"`
}

type Result struct {
    Cancelled   bool     `json:"cancelled"`
    SelectedIDs []string `json:"selected_ids"`
}

func (r Request) Clone() Request {
    r.Options = append([]Option(nil), r.Options...)
    r.InitialSelectedIDs = append([]string(nil), r.InitialSelectedIDs...)
    return r
}

func (r Result) Clone() Result {
    r.SelectedIDs = append([]string(nil), r.SelectedIDs...)
    return r
}
```

- [ ] **步骤 4：实现字段存在性敏感的输入解码**

在 `input.go` 中不要仅依赖 `omitempty` 后的零值，因为需要区分“字段未提供”和“单选错误地提供了 `min_select: 0`”。使用指针字段：

```go
package selecttool

import (
    "encoding/json"
    "fmt"
    "strings"
)

type toolInput struct {
    Prompt             string    `json:"prompt"`
    Mode               Mode      `json:"mode"`
    Options            []Option  `json:"options"`
    InitialSelectedID  *string   `json:"initial_selected_id"`
    InitialSelectedIDs *[]string `json:"initial_selected_ids"`
    MinSelect          *int      `json:"min_select"`
    MaxSelect          *int      `json:"max_select"`
}

func decodeInput(raw json.RawMessage) (Request, error) {
    var in toolInput
    if err := json.Unmarshal(raw, &in); err != nil {
        return Request{}, fmt.Errorf("decode Select input: %w", err)
    }
    // 依次验证 prompt、mode、options、模式专属字段、预选和边界。
}
```

实现时使用以下稳定错误文本，测试必须逐字断言：

```text
prompt is required
mode must be "single" or "multiple"
options must contain at least one option
options[0].id is required
options[0].label is required
duplicate option id: prod
initial_selected_id is only valid in single mode
initial_selected_ids is only valid in multiple mode
min_select is only valid in multiple mode
max_select is only valid in multiple mode
initial_selected_id references unknown option id: missing
duplicate initial selected id: logs
initial_selected_ids references unknown option id: missing
min_select must be between 0 and 2
max_select must be between 0 and 2
min_select must not exceed max_select
initial selection count 2 exceeds max_select 1
```

单选标准化为 `MinSelect=1`、`MaxSelect=1`；若存在 `initial_selected_id`，转为单元素 `InitialSelectedIDs`。多选结果保留原 options 顺序。

- [ ] **步骤 5：补齐所有验证错误的表驱动测试**

在 `input_test.go` 中加入：

```go
func TestDecodeInputRejectsInvalidRequests(t *testing.T) {
    tests := []struct {
        name string
        raw  string
        want string
    }{
        {"empty prompt", `{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}`, "prompt is required"},
        {"bad mode", `{"prompt":"Pick","mode":"other","options":[{"id":"a","label":"A"}]}`, `mode must be "single" or "multiple"`},
        {"no options", `{"prompt":"Pick","mode":"single","options":[]}`, "options must contain at least one option"},
        {"duplicate id", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"},{"id":"a","label":"Again"}]}`, "duplicate option id: a"},
        {"single min field", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"min_select":0}`, "min_select is only valid in multiple mode"},
        {"multiple single initial", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_id":"a"}`, "initial_selected_id is only valid in single mode"},
        {"unknown initial", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_ids":["missing"]}`, "initial_selected_ids references unknown option id: missing"},
        {"bad bounds", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"min_select":1,"max_select":0}`, "min_select must not exceed max_select"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := decodeInput(json.RawMessage(tt.raw))
            if err == nil || err.Error() != tt.want {
                t.Fatalf("error = %v, want %q", err, tt.want)
            }
        })
    }
}
```

增加 option ID 大小写保持、首尾空白清理、重复初始 ID、初始数量超上限测试。

- [ ] **步骤 6：运行协议测试并格式化**

运行：

```bash
gofmt -w internal/tool/select/types.go internal/tool/select/input.go internal/tool/select/input_test.go
go test ./internal/tool/select -run 'TestDecodeInput|TestRequestClone' -count=1
```

预期：PASS。

- [ ] **步骤 7：Commit**

```bash
git add internal/tool/select/types.go internal/tool/select/input.go internal/tool/select/input_test.go
git commit -m "feat: define Select tool request protocol"
```

---

### 任务 2：实现 FIFO Selection Broker 和生命周期

**文件：**
- 创建：`internal/tool/select/broker.go`
- 创建：`internal/tool/select/broker_test.go`

- [ ] **步骤 1：编写 Broker 正常握手的失败测试**

在 `broker_test.go` 中先固定 API：

```go
func TestBrokerPublishesAndCompletesRequest(t *testing.T) {
    broker := NewBroker()
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()

    resultCh := make(chan Result, 1)
    errCh := make(chan error, 1)
    go func() {
        result, err := broker.Ask(ctx, Request{
            Prompt: "Pick",
            Mode: ModeSingle,
            Options: []Option{{ID: "a", Label: "A"}},
            MinSelect: 1,
            MaxSelect: 1,
        })
        resultCh <- result
        errCh <- err
    }()

    event, err := broker.NextEvent(ctx)
    if err != nil {
        t.Fatalf("NextEvent() error = %v", err)
    }
    if event.Kind != EventRequest || event.Request.ID == "" {
        t.Fatalf("event = %#v, want request with id", event)
    }
    if !broker.Complete(event.Request.ID, Result{SelectedIDs: []string{"a"}}) {
        t.Fatal("Complete() = false, want true")
    }
    if err := <-errCh; err != nil {
        t.Fatalf("Ask() error = %v", err)
    }
    if got := <-resultCh; !reflect.DeepEqual(got.SelectedIDs, []string{"a"}) {
        t.Fatalf("result = %#v", got)
    }
}
```

在 `types.go` 补充事件类型：

```go
type EventKind uint8

const (
    EventRequest EventKind = iota + 1
    EventInvalidated
    EventClosed
)

type Event struct {
    Kind      EventKind
    Request   Request
    RequestID string
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：

```bash
go test ./internal/tool/select -run TestBrokerPublishesAndCompletesRequest -count=1
```

预期：FAIL，`Broker` API 尚不存在。

- [ ] **步骤 3：实现 Broker 的最小队列模型**

在 `broker.go` 中使用互斥锁保护 `queue`、`active`、`events` 和关闭状态。推荐结构：

```go
var ErrBrokerClosed = errors.New("selection broker is closed")

type pendingRequest struct {
    request Request
    done    chan completion
}

type completion struct {
    result Result
    err    error
}

type Broker struct {
    mu      sync.Mutex
    nextID  uint64
    queue   []*pendingRequest
    active  *pendingRequest
    events  chan Event
    closed  bool
}

func NewBroker() *Broker {
    return &Broker{events: make(chan Event, 16)}
}
```

关键规则：

1. `Ask` 克隆请求、生成 `select-<n>` ID、入队；若没有活动请求，立即提升队首并投递 `EventRequest`。
2. `NextEvent(ctx)` 从 `events` 读取；context 结束返回 `ctx.Err()`；channel 关闭返回 `ErrBrokerClosed`。
3. `Complete(id,result)` 仅完成 ID 匹配的活动请求；先清空 active，再非阻塞写入 completion，然后提升下一个请求。
4. 不允许在持有 `mu` 时阻塞发送到用户控制的 channel。
5. 返回给调用方和事件消费者的 request/result 均克隆切片。

将事件 channel 设为足以容纳“活动请求失效 + 下一请求发布”的小缓冲，但所有发送仍通过一个 `emitLocked` 辅助函数；若缓冲满，启动短生命周期 goroutine 发送会导致关闭竞态，因此不要这样做。更稳妥的实现是把 `events` 作为容量 1 的唤醒 channel，实际事件存放在 `eventQueue []Event`，`NextEvent` 在锁下弹出队首；状态变化只尝试向 `wake chan struct{}` 写一个信号。

推荐最终字段：

```go
type Broker struct {
    mu         sync.Mutex
    nextID     uint64
    queue      []*pendingRequest
    active     *pendingRequest
    eventQueue []Event
    wake       chan struct{}
    closed     bool
}
```

- [ ] **步骤 4：编写 FIFO、重复完成和错误 ID 测试**

加入测试：

```go
func TestBrokerCompleteIsIdempotent(t *testing.T) {
    broker := NewBroker()
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()

    done := make(chan error, 1)
    go func() {
        _, err := broker.Ask(ctx, Request{
            Prompt: "Pick", Mode: ModeSingle,
            Options: []Option{{ID: "a", Label: "A"}}, MinSelect: 1, MaxSelect: 1,
        })
        done <- err
    }()
    event, err := broker.NextEvent(ctx)
    if err != nil { t.Fatal(err) }
    if !broker.Complete(event.Request.ID, Result{SelectedIDs: []string{"a"}}) {
        t.Fatal("first Complete() = false")
    }
    if broker.Complete(event.Request.ID, Result{SelectedIDs: []string{"a"}}) {
        t.Fatal("second Complete() = true, want false")
    }
    if err := <-done; err != nil { t.Fatalf("Ask() error = %v", err) }
}

func TestBrokerRejectsWrongRequestID(t *testing.T) {
    broker := NewBroker()
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    done := make(chan error, 1)
    go func() {
        _, err := broker.Ask(ctx, Request{
            Prompt: "Pick", Mode: ModeSingle,
            Options: []Option{{ID: "a", Label: "A"}}, MinSelect: 1, MaxSelect: 1,
        })
        done <- err
    }()
    event, err := broker.NextEvent(ctx)
    if err != nil { t.Fatal(err) }
    if broker.Complete("wrong-id", Result{SelectedIDs: []string{"a"}}) {
        t.Fatal("Complete(wrong-id) = true")
    }
    if !broker.Complete(event.Request.ID, Result{SelectedIDs: []string{"a"}}) {
        t.Fatal("active request could not be completed after wrong id")
    }
    if err := <-done; err != nil { t.Fatalf("Ask() error = %v", err) }
}
```

另写 `TestBrokerQueuesRequestsFIFO`：启动两个 `Ask` goroutine；读取第一个 `EventRequest` 后，用 30ms timeout 调用 `NextEvent` 并断言 `context.DeadlineExceeded`；完成第一个请求后读取第二个事件，断言两个 request ID 不同且第二个 prompt 为 `Second`，最后完成第二个请求并等待两个 goroutine 无错误结束。

FIFO 测试必须断言第二个请求在第一个完成之前不会产生 `EventRequest`，可使用 30ms context timeout 的 `NextEvent` 验证。

- [ ] **步骤 5：实现 context 取消与失效事件**

`Ask` 等待 completion 时：

```go
select {
case completed := <-pending.done:
    return completed.result.Clone(), completed.err
case <-ctx.Done():
    broker.cancelPending(pending, ctx.Err())
    return Result{}, ctx.Err()
}
```

`cancelPending`：

- 若 pending 是 active：清空 active，排入 `EventInvalidated{RequestID:id}`，完成 pending，并提升下一请求。
- 若 pending 尚在 queue：从 queue 删除并完成，不需要给 TUI 发失效事件。
- 若已由 Complete 决定终态：不覆盖成功结果。

- [ ] **步骤 6：编写 context 和关闭测试**

加入以下完整行为测试，每个测试都启动 `Ask` goroutine、用 `NextEvent` 获得活动 request ID，并通过带 1 秒 deadline 的 channel 等待断言不悬挂：

- `TestBrokerContextCancellationInvalidatesActiveRequest`：收到请求事件后取消 Ask context；断言 Ask 返回 `context.Canceled`；下一事件为 `EventInvalidated` 且 ID 相同。
- `TestBrokerCancelledQueuedRequestDoesNotBlockNext`：A 为 active，B/C 在 queue；取消 B，完成 A；断言下一请求直接是 C，且 C 可完成。
- `TestBrokerCloseReleasesActiveAndQueuedRequests`：A active、B queued 后调用 `Close`；断言两个 Ask 都 `errors.Is(err, ErrBrokerClosed)`，第二次 Close 不 panic。
- `TestBrokerNextEventReturnsClosed`：关闭空 Broker 后调用 `NextEvent`，断言 `errors.Is(err, ErrBrokerClosed)`。

关闭测试中的 Ask goroutine使用：

```go
errCh := make(chan error, 1)
go func() {
    _, err := broker.Ask(context.Background(), request)
    errCh <- err
}()
select {
case err := <-errCh:
    if !errors.Is(err, ErrBrokerClosed) { t.Fatalf("error = %v", err) }
case <-time.After(time.Second):
    t.Fatal("Ask remained blocked after Close")
}
```

`Close()` 必须幂等：

```go
func (b *Broker) Close() {
    // 标记 closed；完成 active 和所有 queue；排入 EventInvalidated（若需要）和 EventClosed；唤醒 NextEvent。
}
```

- [ ] **步骤 7：增加提交与 context 竞态测试并运行 race detector**

竞态测试循环至少 100 次，同时调用 `Complete` 和 `cancel()`，断言：

- `Ask` 总能结束；
- 结果要么成功要么 `context.Canceled`；
- 不 panic；
- 后续请求仍能工作。

运行：

```bash
gofmt -w internal/tool/select/types.go internal/tool/select/broker.go internal/tool/select/broker_test.go
go test ./internal/tool/select -run TestBroker -count=1
go test -race ./internal/tool/select -run TestBroker -count=1
```

预期：全部 PASS，无 race 报告。

- [ ] **步骤 8：Commit**

```bash
git add internal/tool/select/types.go internal/tool/select/broker.go internal/tool/select/broker_test.go
git commit -m "feat: add FIFO selection broker"
```

---

### 任务 3：实现 Select 工具、Schema 与稳定结果

**文件：**
- 创建：`internal/tool/select/tool.go`
- 创建：`internal/tool/select/tool_test.go`

- [ ] **步骤 1：编写工具元数据和 Schema 的失败测试**

```go
func TestToolMetadata(t *testing.T) {
    tool := New(NewBroker())
    if got := tool.Name(); got != "Select" {
        t.Fatalf("Name() = %q", got)
    }
    if !strings.Contains(tool.Description(), "wait") || !strings.Contains(tool.Description(), "TUI") {
        t.Fatalf("Description() = %q", tool.Description())
    }
    var schema map[string]any
    if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
        t.Fatalf("schema is invalid JSON: %v", err)
    }
    required := schema["required"].([]any)
    // 断言 prompt、mode、options 都存在。
}
```

Schema 必须声明：

- `mode.enum = ["single", "multiple"]`
- `options.minItems = 1`
- option required 为 `id`、`label`
- `min_select`/`max_select` minimum 为 0
- 根 required 为 `prompt`、`mode`、`options`

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/tool/select -run TestToolMetadata -count=1
```

预期：FAIL，`Tool`/`New` 尚不存在。

- [ ] **步骤 3：实现 Tool 元数据和 Run**

在 `tool.go`：

```go
type Tool struct {
    broker *Broker
}

func New(broker *Broker) *Tool { return &Tool{broker: broker} }
func (t *Tool) Name() string { return "Select" }
func (t *Tool) Description() string {
    return "Render a blocking single- or multiple-choice prompt in the main TUI and wait for the user to submit or cancel."
}
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(selectInputSchema) }

func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    if t == nil || t.broker == nil {
        return "", errors.New("selection broker is unavailable")
    }
    request, err := decodeInput(raw)
    if err != nil {
        return "", err
    }
    result, err := t.broker.Ask(ctx, request)
    if err != nil {
        return "", err
    }
    data, err := json.Marshal(Result{
        Cancelled: result.Cancelled,
        SelectedIDs: append([]string(nil), result.SelectedIDs...),
    })
    if err != nil {
        return "", fmt.Errorf("encode Select result: %w", err)
    }
    return string(data), nil
}
```

不要实现 `IsConcurrencySafe`。

- [ ] **步骤 4：编写工具提交、取消和验证失败测试**

```go
func TestToolRunReturnsStableSubmittedJSON(t *testing.T) {
    broker := NewBroker()
    tool := New(broker)
    go func() {
        event, _ := broker.NextEvent(context.Background())
        broker.Complete(event.Request.ID, Result{SelectedIDs: []string{"metrics", "logs"}})
    }()
    got, err := tool.Run(context.Background(), json.RawMessage(`{
        "prompt":"Choose",
        "mode":"multiple",
        "options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]
    }`))
    if err != nil {
        t.Fatalf("Run() error = %v", err)
    }
    if got != `{"cancelled":false,"selected_ids":["metrics","logs"]}` {
        t.Fatalf("Run() = %s", got)
    }
}

补充以下三个测试的完整断言逻辑：

```go
func TestToolRunReturnsCancellationJSON(t *testing.T) {
    broker := NewBroker()
    tool := New(broker)
    go func() {
        event, _ := broker.NextEvent(context.Background())
        broker.Complete(event.Request.ID, Result{Cancelled: true, SelectedIDs: []string{}})
    }()
    got, err := tool.Run(context.Background(), validSingleInput())
    if err != nil { t.Fatalf("Run() error = %v", err) }
    if got != `{"cancelled":true,"selected_ids":[]}` {
        t.Fatalf("Run() = %s", got)
    }
}

func TestToolRunDoesNotPublishInvalidInput(t *testing.T) {
    broker := NewBroker()
    tool := New(broker)
    _, err := tool.Run(context.Background(), json.RawMessage(`{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}`))
    if err == nil || err.Error() != "prompt is required" {
        t.Fatalf("error = %v", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
    defer cancel()
    if _, err := broker.NextEvent(ctx); !errors.Is(err, context.DeadlineExceeded) {
        t.Fatalf("NextEvent() error = %v", err)
    }
}

func TestToolRunReturnsContextCancellation(t *testing.T) {
    broker := NewBroker()
    tool := New(broker)
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() {
        _, err := tool.Run(ctx, validSingleInput())
        done <- err
    }()
    if _, err := broker.NextEvent(context.Background()); err != nil { t.Fatal(err) }
    cancel()
    if err := <-done; !errors.Is(err, context.Canceled) {
        t.Fatalf("Run() error = %v", err)
    }
}
```

其中 `validSingleInput()` 返回一个包含 prompt、single mode 和一个 option 的 `json.RawMessage`。
```

注意：最终“按 options 顺序输出”的责任放在 Dock 提交函数中；Tool 只稳定编码 Broker 返回值。测试可保留 Broker 给定顺序，Dock 测试另行断言排序。

- [ ] **步骤 5：运行工具包完整测试**

```bash
gofmt -w internal/tool/select/tool.go internal/tool/select/tool_test.go
go test ./internal/tool/select/... -count=1
go test -race ./internal/tool/select/... -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/tool/select/tool.go internal/tool/select/tool_test.go
git commit -m "feat: implement blocking Select tool"
```

---

### 任务 4：实现 Selection Dock 纯状态机

**文件：**
- 创建：`internal/ui/bubble/selection_dock.go`
- 创建：`internal/ui/bubble/selection_dock_test.go`
- 修改：`internal/ui/bubble/types.go`

- [ ] **步骤 1：编写初始状态和导航失败测试**

在 `selection_dock_test.go` 使用 `selecttool.Request` 构建状态：

```go
func TestNewSelectionDockUsesInitialSelectionAndFirstHighlight(t *testing.T) {
    dock := newSelectionDock(selecttool.Request{
        ID: "select-1",
        Prompt: "Choose",
        Mode: selecttool.ModeMultiple,
        Options: []selecttool.Option{
            {ID: "a", Label: "A"},
            {ID: "b", Label: "B"},
        },
        InitialSelectedIDs: []string{"b"},
        MinSelect: 1,
        MaxSelect: 2,
    })
    if dock.highlighted != 0 {
        t.Fatalf("highlighted = %d", dock.highlighted)
    }
    if !dock.selected["b"] || dock.selected["a"] {
        t.Fatalf("selected = %#v", dock.selected)
    }
}

func TestSelectionDockNavigationStopsAtBounds(t *testing.T) {
    dock := testSelectionDock(3)
    dock.move(-1)
    if dock.highlighted != 0 { t.Fatalf("moved above first") }
    dock.move(99)
    if dock.highlighted != 2 { t.Fatalf("highlighted = %d", dock.highlighted) }
    dock.move(1)
    if dock.highlighted != 2 { t.Fatalf("wrapped past last") }
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestNewSelectionDock|TestSelectionDockNavigation' -count=1
```

预期：FAIL，Dock 类型不存在。

- [ ] **步骤 3：实现状态结构和纯导航函数**

在 `selection_dock.go`：

```go
type selectionDock struct {
    request     selecttool.Request
    highlighted int
    selected    map[string]bool
    firstVisible int
    errorText   string
}

func newSelectionDock(request selecttool.Request) *selectionDock {
    selected := make(map[string]bool, len(request.InitialSelectedIDs))
    for _, id := range request.InitialSelectedIDs {
        selected[id] = true
    }
    return &selectionDock{request: request.Clone(), selected: selected}
}

func (d *selectionDock) move(delta int) {
    if d == nil || len(d.request.Options) == 0 { return }
    d.highlighted = clampInt(d.highlighted+delta, 0, len(d.request.Options)-1)
}

func (d *selectionDock) home() { d.highlighted = 0 }
func (d *selectionDock) end() { d.highlighted = maxInt(0, len(d.request.Options)-1) }
```

在 `types.go` 的 `appModel` 中加入：

```go
selectionBroker *selecttool.Broker
selectionDock   *selectionDock
```

导入别名统一使用：

```go
selecttool "paw/internal/tool/select"
```

- [ ] **步骤 4：编写多选切换与约束失败测试**

```go
func TestSelectionDockToggleHonorsMaxSelect(t *testing.T) {
    dock := newSelectionDock(selecttool.Request{
        Mode: selecttool.ModeMultiple,
        Options: []selecttool.Option{{ID:"a",Label:"A"},{ID:"b",Label:"B"}},
        MaxSelect: 1,
    })
    if !dock.toggleHighlighted() { t.Fatal("first toggle rejected") }
    dock.move(1)
    if dock.toggleHighlighted() { t.Fatal("toggle above max succeeded") }
    if dock.errorText != "You can select at most 1 options." {
        t.Fatalf("error = %q", dock.errorText)
    }
    dock.move(-1)
    if !dock.toggleHighlighted() { t.Fatal("unselect failed") }
    if dock.errorText != "" { t.Fatalf("error not cleared: %q", dock.errorText) }
}
```

单选 `toggleHighlighted()` 返回 false 且不改变状态。

- [ ] **步骤 5：实现 toggle、提交结果和稳定顺序**

```go
func (d *selectionDock) toggleHighlighted() bool {
    if d == nil || d.request.Mode != selecttool.ModeMultiple || len(d.request.Options) == 0 {
        return false
    }
    option := d.request.Options[d.highlighted]
    if d.selected[option.ID] {
        delete(d.selected, option.ID)
        d.errorText = ""
        return true
    }
    if len(d.selected) >= d.request.MaxSelect {
        d.errorText = fmt.Sprintf("You can select at most %d options.", d.request.MaxSelect)
        return false
    }
    d.selected[option.ID] = true
    d.errorText = ""
    return true
}

func (d *selectionDock) submit() (selecttool.Result, bool) {
    if d.request.Mode == selecttool.ModeSingle {
        return selecttool.Result{SelectedIDs: []string{d.request.Options[d.highlighted].ID}}, true
    }
    count := len(d.selected)
    if count < d.request.MinSelect {
        d.errorText = fmt.Sprintf("Select at least %d options.", d.request.MinSelect)
        return selecttool.Result{}, false
    }
    if count > d.request.MaxSelect {
        d.errorText = fmt.Sprintf("You can select at most %d options.", d.request.MaxSelect)
        return selecttool.Result{}, false
    }
    ids := make([]string, 0, count)
    for _, option := range d.request.Options {
        if d.selected[option.ID] { ids = append(ids, option.ID) }
    }
    return selecttool.Result{SelectedIDs: ids}, true
}

func (d *selectionDock) cancel() selecttool.Result {
    return selecttool.Result{Cancelled: true, SelectedIDs: []string{}}
}
```

- [ ] **步骤 6：补齐状态机测试**

覆盖：

- single submit 当前高亮；
- multiple 少于 min 时不提交；
- multiple 按 options 顺序返回；
- Space 成功切换后清除错误；
- Home/End；
- `j`/`k` 将在键盘集成测试中覆盖；
- 零值/空 options 防御性不 panic。

- [ ] **步骤 7：实现基于视觉行预算的窗口计算测试**

在状态中保留 `firstVisible`，添加纯函数：

```go
type selectionOptionLayout struct {
    index int
    lines []string
}

func (d *selectionDock) visibleRange(optionHeights []int, lineBudget int) (start, end int)
```

测试多行 description：高度 `[1,3,1,2]`，预算 4，高亮从 0 移到 2 后，返回窗口必须包含 index 2 且总高度不超过预算；高亮到 3 时窗口下移。不要假设每个选项固定一行。

- [ ] **步骤 8：运行状态机测试并提交**

```bash
gofmt -w internal/ui/bubble/types.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go
go test ./internal/ui/bubble -run 'TestSelectionDock|TestNewSelectionDock' -count=1
```

预期：PASS。

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go
git commit -m "feat: add Selection Dock state machine"
```

---

### 任务 5：连接 Broker 事件与 Bubble Tea 键盘处理

**文件：**
- 修改：`internal/ui/bubble/app.go`
- 修改：`internal/ui/bubble/bubble.go`
- 修改：`internal/ui/bubble/types.go`
- 修改：`internal/ui/bubble/selection_dock.go`
- 修改：`internal/ui/bubble/selection_dock_test.go`

- [ ] **步骤 1：编写请求消息打开 Dock 的失败测试**

先定义消息：

```go
type selectionBrokerEventMsg struct {
    event selecttool.Event
    err   error
}
```

测试：

```go
func TestSelectionBrokerRequestOpensDock(t *testing.T) {
    broker := selecttool.NewBroker()
    model := newTestModel(&fakeRunner{})
    model.selectionBroker = broker
    next, cmd := model.Update(selectionBrokerEventMsg{event: selecttool.Event{
        Kind: selecttool.EventRequest,
        Request: selecttool.Request{
            ID:"select-1", Prompt:"Choose", Mode:selecttool.ModeSingle,
            Options:[]selecttool.Option{{ID:"a",Label:"A"}}, MinSelect:1, MaxSelect:1,
        },
    }})
    got := next.(appModel)
    if got.selectionDock == nil || got.selectionDock.request.ID != "select-1" {
        t.Fatalf("dock = %#v", got.selectionDock)
    }
    if cmd == nil { t.Fatal("request must schedule next broker event listener") }
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run TestSelectionBrokerRequestOpensDock -count=1
```

预期：FAIL，消息处理不存在。

- [ ] **步骤 3：实现 Broker 等待命令和消息处理**

在 `selection_dock.go`：

```go
func waitSelectionBrokerEventCmd(ctx context.Context, broker *selecttool.Broker) tea.Cmd {
    if broker == nil { return nil }
    return func() tea.Msg {
        event, err := broker.NextEvent(ctx)
        return selectionBrokerEventMsg{event: event, err: err}
    }
}
```

在 `appModel.Init()` 中，当 broker 非 nil 时 append 该命令。

在 `Update` 的非键盘消息 switch 中处理：

- `EventRequest`：若当前 Dock nil，创建 Dock；若已有不同 ID，保留当前并重新监听（正常 Broker 不应发生）。
- `EventInvalidated`：仅当 ID 匹配时清理 Dock、`relayout()`、恢复 input focus。
- `EventClosed` 或 `ErrBrokerClosed`：清理 Dock且不再监听。
- 正常事件处理完返回 `waitSelectionBrokerEventCmd(...)` 继续监听。

- [ ] **步骤 4：编写键盘优先级与提交失败测试**

使用 `tea.KeyMsg`：

```go
func TestSelectionDockConsumesKeysBeforeTextarea(t *testing.T) {
    broker := selecttool.NewBroker()
    model := newTestModel(&fakeRunner{})
    model.selectionBroker = broker
    model.selectionDock = newSelectionDock(validMultipleRequest("select-1"))
    before := model.input.Value()

    next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
    got := next.(appModel)
    if got.selectionDock.highlighted != 1 { t.Fatalf("highlighted = %d", got.selectionDock.highlighted) }
    if got.input.Value() != before { t.Fatalf("textarea changed to %q", got.input.Value()) }
}
```

另写真实 Broker goroutine 测试 Enter：启动 `broker.Ask`，把返回的 request 事件交给 model，发送 Enter，断言 Ask 返回选中结果且 Dock 清空。

- [ ] **步骤 5：实现 handleSelectionDockKey**

在 `Update` 的 `case tea.KeyMsg:` 中，完成 raw mouse escape 过滤后、theme picker/wizard/tool inspect/全局 Ctrl+C 之前加入：

```go
if m.selectionDock != nil {
    return m.handleSelectionDockKey(msg)
}
```

实现：

```go
func (m appModel) handleSelectionDockKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.String() {
    case "up", "k":
        m.selectionDock.move(-1)
    case "down", "j":
        m.selectionDock.move(1)
    case "home":
        m.selectionDock.home()
    case "end":
        m.selectionDock.end()
    case " ", "space":
        m.selectionDock.toggleHighlighted()
    case "enter":
        if result, ok := m.selectionDock.submit(); ok {
            m.completeSelection(result)
        }
    case "esc", "ctrl+c":
        m.completeSelection(m.selectionDock.cancel())
    }
    m.relayout()
    return m, nil
}
```

`completeSelection` 必须先保存 request ID，调用 `broker.Complete`，仅在返回 true 时清理 Dock；调用不阻塞。清理后返回 `m.input.Focus()` 命令。为了让 handler 返回 focus cmd，可让 `completeSelection` 返回 `tea.Cmd`。

Space 的 `msg.String()` 在 Bubble Tea 中通常为单个空格；测试同时覆盖实际构造的 rune 空格。

- [ ] **步骤 6：编写取消和失效测试**

覆盖：

- Esc 返回 `{Cancelled:true, SelectedIDs:[]}`；
- Ctrl+C 不触发 `cancelModelWork`，不修改 `lastCtrlCAt`；
- 不满足 min 的 Enter 保持 Dock；
- 达 max 的 Space 保持 Dock并显示错误；
- `EventInvalidated` 错误 ID 不关闭当前 Dock；
- 匹配 ID 关闭 Dock并保留 textarea 原草稿；
- Dock 期间 `/`、Tab、普通字符不进入 completion/input。

- [ ] **步骤 7：为 Bubble UI 注入 Broker**

在 `bubble.UI` 增加：

```go
selectionBroker *selecttool.Broker

func (u *UI) SetSelectionBroker(broker *selecttool.Broker) {
    u.mu.Lock()
    defer u.mu.Unlock()
    u.selectionBroker = broker
}
```

`Run` 取出 broker，并将 `newModel` 签名扩展为最后一个参数：

```go
func newModel(..., anchor *terminalCursorAnchor, selectionBroker *selecttool.Broker) appModel
```

更新 `newTestModel` 和所有直接调用 `newModel` 的测试，统一传 `nil`；不要在调用点临时创建 Broker。

- [ ] **步骤 8：运行 Bubble 集成测试并提交**

```bash
gofmt -w internal/ui/bubble/app.go internal/ui/bubble/bubble.go internal/ui/bubble/types.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go internal/ui/bubble/bubble_test.go internal/ui/bubble/fixed_layout_test.go
go test ./internal/ui/bubble -run 'TestSelection' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

```bash
git add internal/ui/bubble/app.go internal/ui/bubble/bubble.go internal/ui/bubble/types.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go internal/ui/bubble/bubble_test.go internal/ui/bubble/fixed_layout_test.go
git commit -m "feat: connect Selection Broker to Bubble Tea"
```

---

### 任务 6：实现底部 Dock 布局、滚动和主题化渲染

**文件：**
- 创建：`internal/ui/bubble/selection_dock_render.go`
- 修改：`internal/ui/bubble/layout.go`
- 修改：`internal/ui/bubble/selection_dock.go`
- 修改：`internal/ui/bubble/selection_dock_test.go`

- [ ] **步骤 1：编写推荐高度和输入替换的失败测试**

```go
func TestCurrentLayoutUsesSelectionDockHeight(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.ready = true
    model.width = 80
    model.height = 24
    model.selectionDock = newSelectionDock(longSelectionRequest("select-1", 8))
    model.relayout()

    layout := model.currentLayout()
    if layout.inputHeight <= inputMinVisibleLines {
        t.Fatalf("selection inputHeight = %d, want expanded dock", layout.inputHeight)
    }
    if layout.transcriptHeight < 1 {
        t.Fatalf("transcriptHeight = %d, want at least 1", layout.transcriptHeight)
    }
}

func TestRenderInputBoxUsesSelectionDock(t *testing.T) {
    model := preparedSelectionModel(80, 20)
    rendered := stripANSI(model.renderInputBoxForLayout(model.currentLayout()))
    if !strings.Contains(rendered, "Select · multiple") || !strings.Contains(rendered, "space toggle") {
        t.Fatalf("dock render = %q", rendered)
    }
    if strings.Contains(rendered, "Ask anything") {
        t.Fatalf("normal input leaked into selection dock: %q", rendered)
    }
}
```

使用项目已有 ANSI 去除辅助函数；若没有公共 helper，则测试中使用 `ansi.Strip`。

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestCurrentLayoutUsesSelectionDockHeight|TestRenderInputBoxUsesSelectionDock' -count=1
```

预期：FAIL，布局仍使用 textarea 高度。

- [ ] **步骤 3：实现 Selection Dock 请求高度**

在 `selection_dock_render.go` 定义：

```go
const selectionDockMaxVisibleLines = inputMaxVisibleLines

func (d *selectionDock) preferredHeight(width int) int {
    // title 1 + prompt 至少 1 + options + 可选 error + hints 1；
    // clamp 到 1..inputMaxVisibleLines。
}
```

`currentLayout()` 改为：

```go
func (m appModel) currentLayout() tuiLayout {
    inputHeight := m.input.Height()
    if m.selectionDock != nil {
        base := computeTUILayout(m.width, m.height, inputMinVisibleLines)
        inputHeight = m.selectionDock.preferredHeight(inputDockContentWidth(base.contentWidth))
    }
    if inputHeight <= 0 { inputHeight = inputMinVisibleLines }
    return computeTUILayout(m.width, m.height, inputHeight)
}
```

`relayout()` 使用同一请求高度计算 viewport，但 Dock 活跃时不要把 textarea 永久设置成 Dock 高度：

```go
normalInputHeight := m.tokenAwareInputVisibleLineCount()
requestedInputHeight := normalInputHeight
if m.selectionDock != nil {
    requestedInputHeight = m.selectionDock.preferredHeight(inputWidth)
}
layout := computeTUILayout(m.width, m.height, requestedInputHeight)
if m.selectionDock == nil {
    m.input.SetHeight(layout.inputHeight)
} else {
    m.input.SetHeight(clampInt(normalInputHeight, 1, inputMaxVisibleLines))
}
```

Dock 关闭后 `relayout()` 会恢复正常输入高度。

- [ ] **步骤 4：实现内容渲染和主题复用**

`renderInputBoxForLayout` 最前面分支：

```go
if m.selectionDock != nil {
    return renderFixedStyledPanel(
        inputDockStyle,
        layout.contentWidth,
        layout.inputHeight,
        m.renderSelectionDock(inputDockContentWidth(layout.contentWidth), layout.inputHeight),
    )
}
```

`renderSelectionDock` 构建：

- 第一行：`Select · <mode>`，右侧 `current/total`；宽度不足时优先保留标题。
- prompt：使用 body/heading 风格，按 display width 换行。
- option：高亮前缀 `›`，非高亮两个空格；multiple 加 `[x]`/`[ ]`。
- 高亮整行使用 `m.styles.Selected`；普通项使用 `m.styles.Unselected` 或 body/input hint 风格，背景必须与 input dock 一致。
- description 缩进到 label 文本起点，使用 `m.styles.StatusMuted`。
- error 使用 `m.styles.StatusError`。
- hints：single 为 `↑↓ move  enter submit  esc cancel`；multiple 增加 `space toggle`。

所有传入文字先 `sanitizeTerminalText`，再使用现有 `wrapDisplayWidth`/`truncateDisplayWidth`/`fitStyledRect` 辅助函数。不要按 rune 长度做布局。

- [ ] **步骤 5：实现可见选项滚动和 more 指示**

渲染前为每个 option 生成视觉行切片，得到高度数组。根据固定行占用计算 `optionBudget`：

```text
总高度
- title
- prompt 行数
- hints
- 可选 error
- 可选 more 指示
= options 可用行
```

调用状态机的 `visibleRange`，必要时为 `↑ more` / `↓ more` 各预留一行并重新计算一次，确保最终不超预算。高亮移动后调用 `ensureHighlightedVisible(optionHeights,budget)` 更新 `firstVisible`。

- [ ] **步骤 6：实现极小终端降级测试**

增加表驱动测试尺寸：`80x24`、`40x10`、`20x6`、`8x3`。每个测试：

```go
view := model.View()
plain := ansi.Strip(view)
lines := strings.Split(plain, "\n")
if len(lines) != height { ... }
for _, line := range lines {
    if terminalCellWidth(line) != width { ... }
}
```

内容断言：

- 正常尺寸有 description；
- 矮终端可隐藏 description，但保留至少一个 label 和操作提示（当内容高度允许）；
- 长列表滚动时出现 `↓ more`；移动到底部后出现 `↑ more`；
- 不 panic、无负尺寸。

- [ ] **步骤 7：隐藏 Selection Dock 期间的 textarea 终端光标**

在 `updateTerminalCursorAnchor` 或其活动条件中加入 `m.selectionDock == nil`。Dock 活跃时调用 `cursorAnchor.clear()`，避免 Ghostty/IME 光标显示在被隐藏的 textarea 位置。

加入测试：Dock 活跃调用 `View()` 后 anchor 不 active；关闭 Dock后重新 active。

- [ ] **步骤 8：运行布局和完整 Bubble 测试**

```bash
gofmt -w internal/ui/bubble/selection_dock_render.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go internal/ui/bubble/layout.go
go test ./internal/ui/bubble -run 'Test.*Selection.*(Render|Layout|Height|Scroll|Terminal|Cursor)' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/ui/bubble/selection_dock_render.go internal/ui/bubble/selection_dock.go internal/ui/bubble/selection_dock_test.go internal/ui/bubble/layout.go
git commit -m "feat: render scrolling Select dock"
```

---

### 任务 7：主交互模式装配与注册隔离

**文件：**
- 修改：`cmd/agent/main.go`
- 修改：`cmd/agent/register_test.go`

- [ ] **步骤 1：编写注册隔离的失败测试**

不要让通用 `registerTools` 自动注册 Select。新增专用 helper 并测试：

```go
func TestRegisterInteractiveToolsAddsSelect(t *testing.T) {
    registry := tool.NewRegistry()
    broker := selecttool.NewBroker()
    registerInteractiveTools(registry, broker)
    if _, ok := registry.Get("Select"); !ok {
        t.Fatal("interactive registry missing Select")
    }
}

func TestRegisterToolsDoesNotAddSelect(t *testing.T) {
    registry := tool.NewRegistry()
    // 使用现有 registerTools 测试夹具。
    if _, ok := registry.Get("Select"); ok {
        t.Fatal("base/headless registry unexpectedly contains Select")
    }
}
```

再断言 `subagent.newBaseToolRegistry` 的既有测试或公开行为不包含 Select；若该 helper 不可从 cmd 包访问，不修改子代理包，仅通过其现有 manager 工具 definitions 测试。

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./cmd/agent -run 'TestRegisterInteractiveToolsAddsSelect|TestRegisterToolsDoesNotAddSelect' -count=1
```

预期：FAIL，交互 helper 尚不存在。

- [ ] **步骤 3：实现仅交互模式注册 helper**

在 `cmd/agent/main.go`：

```go
func registerInteractiveTools(registry *tool.Registry, broker *selecttool.Broker) error {
    if registry == nil {
        return fmt.Errorf("tool registry is nil")
    }
    if broker == nil {
        return fmt.Errorf("selection broker is nil")
    }
    registry.Register(selecttool.New(broker))
    return nil
}
```

为了从 `runInteractiveMode` 访问 registry，采用最小侵入方式扩展 `buildRunner` 返回值并不可取。推荐给 `loop.Runner` 增加窄方法会污染 Runner API；更好的装配方式是把可选交互注册函数传入构建路径：

```go
type runnerToolConfigurator func(*tool.Registry) error

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI, configure ...runnerToolConfigurator) (...)
```

在 `buildRunnerWithSubagentContext` 创建 registry 并完成 `registerTools` 后，依次执行 configurator：

```go
for _, configure := range configurators {
    if configure != nil {
        if err := configure(registry); err != nil {
            if mcpManager != nil {
                _ = mcpManager.Close(context.Background())
            }
            return nil, "", nil, nil, nil, nil, nil, err
        }
    }
}
```

但 `buildRunnerWithSubagentContext` 同时用于 worker，不能让 variadic 隐式向子代理传播。最终签名建议明确：

```go
func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI, configure ...runnerToolConfigurator) (...)
func buildRunnerWithSubagentContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, subCtx subagentRuntimeContext, configure ...runnerToolConfigurator) (...)
```

现有 worker 调用不传 configurator；single-turn 不传；interactive 传一个注册 Select 的闭包。

- [ ] **步骤 4：在 runInteractiveMode 创建并注入 Broker**

```go
func runInteractiveMode(ctx context.Context, opts options) error {
    clearTerminalWindow(os.Stdout)

    output := bubbleui.New()
    selectionBroker := selecttool.NewBroker()
    defer selectionBroker.Close()
    output.SetSelectionBroker(selectionBroker)

    runner, sessionID, client, settingsController, subagentManager, store, mcpManager, err := buildRunner(
        ctx,
        opts.sessionID,
        output,
        func(registry *tool.Registry) error {
            return registerInteractiveTools(registry, selectionBroker)
        },
    )
    // 保留现有错误处理和 controller 注入。
}
```

`runSingleTurnMode` 继续调用 `buildRunner(ctx,...,output)`，因此 definitions 中没有 Select。子代理 worker 同样不传 configurator。

- [ ] **步骤 5：测试关闭生命周期**

若直接测试 `runInteractiveMode` 需要真实终端，不做脆弱集成。改为测试：

- `registerInteractiveTools(nil, broker)` 返回稳定错误；
- `registerInteractiveTools(registry,nil)` 返回稳定错误；
- Bubble UI 注入的 broker 在 `Close()` 后等待中的 `Select.Run` 返回 `ErrBrokerClosed`（工具包已有覆盖）；
- 在 `main.go` 代码审查中确认 `defer selectionBroker.Close()` 位于创建后立即注册。

- [ ] **步骤 6：运行 cmd 与全注册测试**

```bash
gofmt -w cmd/agent/main.go cmd/agent/register_test.go
go test ./cmd/agent -count=1
go test ./internal/subagent -count=1
```

预期：PASS，且既有 MCP/文件工具注册测试不变。

- [ ] **步骤 7：Commit**

```bash
git add cmd/agent/main.go cmd/agent/register_test.go
git commit -m "feat: register Select only in interactive TUI"
```

---

### 任务 8：验证工具执行屏障与顺序

**文件：**
- 修改：`internal/loop/runner_test.go`

生产代码 `runToolCallsWithCheckpoint` 已按原始顺序把连续 `ConcurrencySafeTool` 成批并行执行，并把未实现该接口的工具串行执行。`Select` 不实现 `ConcurrencySafeTool`，所以无需修改 `internal/loop/runner.go`；本任务用测试锁定该行为。

- [ ] **步骤 1：编写安全工具 → Select 屏障 → 安全工具的失败/回归测试**

在 `runner_test.go` 新增可观察工具：

```go
type orderedTool struct {
    name    string
    safe    bool
    started chan<- string
    release <-chan struct{}
}

func (t *orderedTool) Name() string { return t.name }
func (t *orderedTool) Description() string { return t.name }
func (t *orderedTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *orderedTool) IsConcurrencySafe(json.RawMessage) bool { return t.safe }
func (t *orderedTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
    t.started <- t.name
    if t.release != nil {
        select { case <-ctx.Done(): return "", ctx.Err(); case <-t.release: }
    }
    return t.name + " done", nil
}
```

由于“没有 `IsConcurrencySafe` 方法”无法由同一个类型条件表达，为屏障工具单独定义 `serialOrderedTool`，其 API 相同但不实现 `IsConcurrencySafe`。

测试流程：

1. calls 顺序为 `safe-before`、`Select`、`safe-after`。
2. `safe-before` 启动并阻塞；确认 Select 尚未启动。
3. 释放 before；确认 Select 启动并阻塞；确认 after 尚未启动。
4. 释放 Select；确认 after 才启动。
5. 最终结果顺序仍与 calls 一致。

- [ ] **步骤 2：运行测试确认当前实现是否已通过**

```bash
go test ./internal/loop -run TestToolCallsPreserveBarrierAroundSerialTool -count=1
```

预期：PASS。如果 FAIL，先检查是否当前未提交的 Runner 改动改变了批处理语义；仅在确认真实缺陷后对 `runToolCallsWithCheckpoint` 做最小修复，保持如下算法：连续 safe 批次 → 单个 unsafe → 下一批次。

- [ ] **步骤 3：增加多个 Select/串行工具顺序测试**

calls：`Select-1`、`Select-2`、`safe-after`。逐个 release，断言第二个在第一个结束前不启动，safe-after 在第二个结束前不启动。

- [ ] **步骤 4：运行 loop 完整测试和 race 测试**

```bash
gofmt -w internal/loop/runner_test.go
go test ./internal/loop -count=1
go test -race ./internal/loop -run 'TestToolCallsPreserveBarrier|TestToolCallsSerialize' -count=1
```

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/loop/runner_test.go
git commit -m "test: lock Select tool execution barrier"
```

如果生产 `runner.go` 确实需要修复，将它与测试一起加入 commit，并在 commit message 使用 `fix:`。

---

### 任务 9：为 transcript 工具轨迹增加 Select 专用摘要

**文件：**
- 修改：`internal/ui/bubble/utils.go`
- 修改：`internal/ui/bubble/transcript.go`
- 修改：`internal/ui/bubble/tool_display_test.go`

- [ ] **步骤 1：编写 Select 调用显示的失败测试**

在 `tool_display_test.go`：

```go
func TestFormatSelectToolCallBodyHidesOptionPayload(t *testing.T) {
    body := formatToolCallBody("Select", json.RawMessage(`{
        "prompt":"Choose environment",
        "mode":"single",
        "options":[
            {"id":"prod","label":"Production","description":"Live"},
            {"id":"stage","label":"Staging"}
        ]
    }`), "")
    if body != "Select\nmode  single\nprompt  Choose environment" {
        t.Fatalf("body = %q", body)
    }
    if strings.Contains(body, "Production") || strings.Contains(body, "prod") {
        t.Fatalf("option payload leaked: %q", body)
    }
}
```

- [ ] **步骤 2：编写完成摘要的失败测试**

```go
func TestCompleteSelectToolCallBodySummarizesResult(t *testing.T) {
    running := formatRunningToolCallBody("Select", json.RawMessage(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}]}`), "")
    got := completeToolCallBody("Select", running, "ok", `{"cancelled":false,"selected_ids":["a"]}`)
    if firstToolEntryLine(got) != "Select · selected 1 option" {
        t.Fatalf("summary = %q", firstToolEntryLine(got))
    }

    cancelled := completeToolCallBody("Select", running, "ok", `{"cancelled":true,"selected_ids":[]}`)
    if firstToolEntryLine(cancelled) != "Select · cancelled" {
        t.Fatalf("summary = %q", firstToolEntryLine(cancelled))
    }
}
```

同时测试 2 项时使用 `selected 2 options`；错误结果仍显示 `Select · error`，不要尝试解析错误文本。

- [ ] **步骤 3：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestFormatSelect|TestCompleteSelect' -count=1
```

预期：FAIL。

- [ ] **步骤 4：实现紧凑调用体**

在 `formatToolCallBody` 的 Subagent 分支之前加入 Select 分支：

```go
if strings.EqualFold(name, "Select") {
    return formatSelectToolCallBody(name, fields)
}
```

实现：

```go
func formatSelectToolCallBody(name string, fields []toolDisplayField) string {
    lines := []string{name}
    if mode := fieldValue(fields, "mode"); mode != "" {
        lines = append(lines, "mode  "+mode)
    }
    if prompt := fieldValue(fields, "prompt"); prompt != "" {
        lines = append(lines, "prompt  "+summarizeToolContent(prompt))
    }
    return strings.Join(lines, "\n")
}
```

不要显示 options、initial IDs 或 min/max；完整 JSON 已保留在 tool inspect 数据中。

- [ ] **步骤 5：实现完成摘要 helper 并接入 transcript**

在 `utils.go`：

```go
func completeToolCallBody(name, body, status, content string) string {
    if !strings.EqualFold(strings.TrimSpace(name), "Select") || status != "ok" {
        return completeRunningToolCallBody(body, status)
    }
    var result selecttool.Result
    if json.Unmarshal([]byte(content), &result) != nil {
        return completeRunningToolCallBody(body, status)
    }
    summary := "Select · cancelled"
    if !result.Cancelled {
        noun := "options"
        if len(result.SelectedIDs) == 1 { noun = "option" }
        summary = fmt.Sprintf("Select · selected %d %s", len(result.SelectedIDs), noun)
    }
    lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
    if len(lines) == 0 { return summary }
    lines[0] = summary
    return strings.Join(lines, "\n")
}
```

在 `recordToolResultEntry` 将：

```go
entry.body = completeRunningToolCallBody(entry.body, status)
```

替换为：

```go
entry.body = completeToolCallBody(name, entry.body, status, content)
```

错误结果和无效 JSON 保持现有状态行为。

- [ ] **步骤 6：运行显示与 transcript 回归测试**

```bash
gofmt -w internal/ui/bubble/utils.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_display_test.go
go test ./internal/ui/bubble -run 'TestFormatSelect|TestCompleteSelect|Test.*Tool.*Display|Test.*Tool.*Track' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 7：Commit**

```bash
git add internal/ui/bubble/utils.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_display_test.go
git commit -m "feat: summarize Select tool activity"
```

---

### 任务 10：端到端回归、静态检查与文档验收

**文件：**
- 可能修改：仅测试发现的 Select 相关文件
- 验证：`docs/superpowers/specs/2026-07-30-select-tool-design.md`

- [ ] **步骤 1：运行新增包测试和 race detector**

```bash
go test ./internal/tool/select/... -count=1
go test -race ./internal/tool/select/... -count=1
```

预期：PASS，无 race。

- [ ] **步骤 2：运行 Bubble、Loop、Cmd 和子代理测试**

```bash
go test ./internal/ui/bubble/... -count=1
go test ./internal/loop/... -count=1
go test ./cmd/agent/... -count=1
go test ./internal/subagent/... -count=1
```

预期：PASS。

- [ ] **步骤 3：运行完整测试套件**

```bash
go test ./... -count=1
```

预期：PASS。若失败来自工作区既有未提交改动，记录准确失败命令和错误，不得通过回退无关代码来“修复”。

- [ ] **步骤 4：运行格式和差异检查**

```bash
gofmt -w internal/tool/select/*.go internal/ui/bubble/selection_dock*.go
git diff --check
git status --short
```

预期：`git diff --check` 无输出；status 仅包含本功能预期文件及进入 worktree 前已知的其他改动。

- [ ] **步骤 5：按规格逐项人工验收**

启动 TUI 后用可控模型或测试入口触发以下调用：

```json
{
  "prompt": "Choose environment",
  "mode": "single",
  "options": [
    {"id": "prod", "label": "Production", "description": "Live traffic"},
    {"id": "stage", "label": "Staging", "description": "Pre-release"}
  ],
  "initial_selected_id": "stage"
}
```

确认：

- Dock 替换输入区；
- ↑↓ 移动，Enter 提交；
- transcript 工具行先 running，后 `selected 1 option`；
- 普通输入草稿未丢失。

再触发多选：

```json
{
  "prompt": "Choose signals",
  "mode": "multiple",
  "options": [
    {"id": "logs", "label": "Logs"},
    {"id": "metrics", "label": "Metrics"},
    {"id": "traces", "label": "Traces"}
  ],
  "min_select": 1,
  "max_select": 2,
  "initial_selected_ids": ["logs"]
}
```

确认 Space、min/max 错误、Esc/Ctrl+C 取消、长列表 more 指示和滚动。

- [ ] **步骤 6：检查注册隔离**

在测试或调试输出中确认：

- 主交互 registry definitions 包含 `Select`；
- `agent -p` 的 headless definitions 不包含 `Select`；
- 子代理基础 registry 不包含 `Select`；
- 多个 Select 调用按顺序出现，没有并行 Dock。

- [ ] **步骤 7：最终 Commit**

若步骤 1-6 产生修正：

```bash
git add internal/tool/select internal/ui/bubble cmd/agent internal/loop/runner_test.go
git commit -m "test: verify Select TUI workflow"
```

若没有额外改动，不创建空 commit。

- [ ] **步骤 8：记录最终验证摘要**

在交付消息中列出：

- 实现的工具输入/结果协议；
- 交互键位；
- Broker 关闭和 context 取消行为；
- 注册隔离；
- 运行过的测试命令及结果；
- 任何因原工作区改动导致但未处理的非本功能失败。
