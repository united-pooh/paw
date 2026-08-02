# Paw 原生 Todo List 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为主 Agent 增加原生 `update_todo` 完整快照工具，在 Bubble Tea transcript 中展示可折叠 Todo 卡片，通过 `Ctrl+P` 提供纵向独立页面，并从现有会话工具历史恢复 Todo 状态。

**架构：** 新建 `internal/todo` 包承载状态模型、校验、结果协议、发布 Broker 和工具实现；交互模式把同一个 Broker 注入工具 Registry 与 Bubble UI，headless 工具不依赖 UI 消费者，subagent 不注册该工具。Bubble UI 将实时 Broker 事件和历史 `update_todo` tool-result 都投影为专用 transcript entry，并从最新快照派生独立页面；JSONL 存储继续只保存普通工具调用和结果，不建立第二套持久化文件。

**技术栈：** Go、Bubble Tea、Lip Gloss、现有 `tool.Tool` / `tool.Registry` / `loop.Runner`、现有 session journal、标准库 `encoding/json` / `context` / `sync`、Go testing。

---

## 实现前约束

设计规格：`docs/superpowers/specs/2026-08-02-native-todo-list-design.md`。

当前 `dev` 工作区有大量未提交修改，且本功能需要修改的共享文件已经处于修改状态。执行计划时必须：

1. 优先在 brainstorming 已创建的专用 worktree 中执行。
2. 若只能在当前工作区执行，每次修改共享文件前先运行：

```bash
git diff -- cmd/agent/main.go internal/ui/bubble/app.go internal/ui/bubble/bubble.go internal/ui/bubble/layout.go internal/ui/bubble/transcript.go internal/ui/bubble/types.go internal/ui/bubble/subagent_picker.go
```

3. 禁止回退、覆盖或整体重写无关改动。
4. 共享文件只做局部编辑；新增逻辑优先放入职责单一的新文件。
5. 每次 commit 前使用 `git diff --cached` 确认暂存区只包含本任务相关修改。
6. 本计划不实现 Phase，不增加 phase、group、dependency、parent 或 hierarchy 字段。

## 文件结构

### 新建文件

- `internal/todo/types.go`：`Status`、`Item`、`Snapshot`、`UpdateInput`、`UpdateResult`，克隆和计数辅助函数。
- `internal/todo/validate.go`：输入规范化、完整快照校验和稳定错误文本。
- `internal/todo/broker.go`：无阻塞发布、顺序事件队列、最新快照和关闭生命周期。
- `internal/todo/tool.go`：`update_todo` 工具元数据、Schema、时间戳生成、发布和结果编码。
- `internal/todo/types_test.go`：克隆、计数和完成状态测试。
- `internal/todo/validate_test.go`：合法输入和全部校验错误测试。
- `internal/todo/broker_test.go`：顺序、深拷贝、关闭、context 和非阻塞行为测试。
- `internal/todo/tool_test.go`：工具元数据、Schema、结果和失败不发布测试。
- `internal/ui/bubble/todo_state.go`：实时快照接入、当前状态、transcript 快照插入、折叠和完成联动。
- `internal/ui/bubble/todo_restore.go`：从 session records 中识别 `update_todo` 调用结果并重建卡片。
- `internal/ui/bubble/todo_render.go`：Todo 卡片、折叠摘要、状态行和宽度降级渲染。
- `internal/ui/bubble/todo_page.go`：`Ctrl+P` 页面状态、滚动、按键处理和渲染。
- `internal/ui/bubble/todo_state_test.go`：实时更新、折叠、完成联动和 smart-scroll 测试。
- `internal/ui/bubble/todo_restore_test.go`：合法、损坏、错误、清除和会话隔离恢复测试。
- `internal/ui/bubble/todo_render_test.go`：卡片、摘要、宽字符和窄终端测试。
- `internal/ui/bubble/todo_page_test.go`：快捷键、空状态、滚动、实时刷新和 modal 优先级测试。

### 修改文件

- `cmd/agent/main.go`：交互模式创建 Todo Broker；主交互 Registry 注册 `update_todo`；single-turn 注册不带 UI Broker 的工具。
- `cmd/agent/register_test.go`：主 Agent 注册范围和 subagent 隔离测试。
- `internal/subagent/manager_test.go`：基础 subagent Registry 不包含 `update_todo`。
- `internal/ui/bubble/bubble.go`：向 Bubble UI 注入 Todo Broker。
- `internal/ui/bubble/app.go`：初始化 Todo 监听 command，处理 Todo 事件、`Ctrl+P` 和最终回答折叠。
- `internal/ui/bubble/types.go`：新增 `entryTodo`、Todo entry 字段、appModel 当前 Todo 和页面状态。
- `internal/ui/bubble/layout.go`：Todo 页面打开时渲染独立全页内容并隐藏 textarea 光标锚点。
- `internal/ui/bubble/transcript.go`：专用 Todo entry 渲染、缓存键、位置高度和工具轨迹摘要接入。
- `internal/ui/bubble/tool_display.go`：把 `update_todo` 显示为简短 `Todo` 工具轨迹。
- `internal/ui/bubble/tool_track_test.go`：工具调用和结果不泄漏完整 items JSON。
- `internal/ui/bubble/new_message_notice.go`：Todo 页面打开时不覆盖页面；Todo 快照只计一次新消息。
- `internal/ui/bubble/selection.go`：鼠标点击 Todo 卡片标题或摘要时切换展开状态。
- `internal/ui/bubble/session_picker.go`：恢复命令把 records 交给 Todo 恢复投影。
- `internal/ui/bubble/subagent_picker.go`：主 session transcript 转换时插入 Todo entry；subagent preview 不把内部历史误投影为主 Todo。
- `internal/ui/bubble/styles.go`：增加 Todo 标题、完成、进行中、待处理和说明的语义样式。
- `internal/ui/bubble/bubble_test.go`：构造器和事件桥接回归。
- `internal/ui/bubble/fixed_layout_test.go`：Todo 页面极小终端固定尺寸测试。

---

### 任务 1：定义 Todo 核心协议、克隆和统计

**文件：**
- 创建：`internal/todo/types.go`
- 创建：`internal/todo/types_test.go`

- [ ] **步骤 1：编写核心类型行为的失败测试**

创建 `internal/todo/types_test.go`：

```go
package todo

import (
    "reflect"
    "testing"
    "time"
)

func TestSnapshotCloneDoesNotShareItems(t *testing.T) {
    original := Snapshot{
        Explanation: "start",
        Items: []Item{{ID: "inspect", Content: "Inspect code", Status: StatusInProgress}},
        UpdatedAt: time.Unix(10, 0).UTC(),
    }
    cloned := original.Clone()
    cloned.Items[0].Content = "changed"

    if original.Items[0].Content != "Inspect code" {
        t.Fatalf("Clone() shared Items: %#v", original.Items)
    }
}

func TestSnapshotProgress(t *testing.T) {
    snapshot := Snapshot{Items: []Item{
        {ID: "a", Content: "A", Status: StatusCompleted},
        {ID: "b", Content: "B", Status: StatusInProgress},
        {ID: "c", Content: "C", Status: StatusPending},
    }}
    if got := snapshot.CompletedCount(); got != 1 {
        t.Fatalf("CompletedCount() = %d, want 1", got)
    }
    if snapshot.AllCompleted() {
        t.Fatal("AllCompleted() = true for incomplete snapshot")
    }
}

func TestEmptySnapshotIsClearedNotCompleted(t *testing.T) {
    snapshot := Snapshot{Items: []Item{}}
    if !snapshot.Cleared() {
        t.Fatal("Cleared() = false")
    }
    if snapshot.AllCompleted() {
        t.Fatal("AllCompleted() = true for empty list")
    }
}

func TestUpdateResultClone(t *testing.T) {
    result := UpdateResult{Accepted: true, Snapshot: Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}}}
    cloned := result.Clone()
    cloned.Snapshot.Items[0].ID = "b"
    if reflect.DeepEqual(result, cloned) {
        t.Fatal("Clone() still aliases nested snapshot")
    }
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/todo -run 'TestSnapshot|TestEmptySnapshot|TestUpdateResult' -count=1
```

预期：FAIL，`internal/todo` 包或公开类型尚不存在。

- [ ] **步骤 3：实现最终核心类型**

创建 `internal/todo/types.go`：

```go
package todo

import "time"

type Status string

const (
    StatusPending    Status = "pending"
    StatusInProgress Status = "in_progress"
    StatusCompleted  Status = "completed"
)

type Item struct {
    ID      string `json:"id"`
    Content string `json:"content"`
    Status  Status `json:"status"`
}

type Snapshot struct {
    Explanation string    `json:"explanation,omitempty"`
    Items       []Item    `json:"items"`
    UpdatedAt   time.Time `json:"updated_at"`
}

type UpdateInput struct {
    Explanation string `json:"explanation,omitempty"`
    Items       []Item `json:"items"`
}

type UpdateResult struct {
    Accepted bool     `json:"accepted"`
    Snapshot Snapshot `json:"snapshot"`
}

func (s Snapshot) Clone() Snapshot {
    if s.Items == nil {
        s.Items = nil
    } else {
        s.Items = append([]Item{}, s.Items...)
    }
    return s
}

func (r UpdateResult) Clone() UpdateResult {
    r.Snapshot = r.Snapshot.Clone()
    return r
}

func (s Snapshot) CompletedCount() int {
    count := 0
    for _, item := range s.Items {
        if item.Status == StatusCompleted {
            count++
        }
    }
    return count
}

func (s Snapshot) TotalCount() int { return len(s.Items) }
func (s Snapshot) Cleared() bool   { return len(s.Items) == 0 }
func (s Snapshot) AllCompleted() bool {
    return len(s.Items) > 0 && s.CompletedCount() == len(s.Items)
}
```

保留空 slice 与 nil 的区别：工具解码成功后统一将合法 `items` 规范化为非 nil slice；`Clone` 必须保持调用方传入的 nil 语义，恢复解析时再统一。

- [ ] **步骤 4：补充状态字符串和计数边界测试**

增加以下断言：

```go
func TestStatusValuesAreStable(t *testing.T) {
    if StatusPending != "pending" || StatusInProgress != "in_progress" || StatusCompleted != "completed" {
        t.Fatalf("status values changed: %q %q %q", StatusPending, StatusInProgress, StatusCompleted)
    }
}

func TestAllCompleted(t *testing.T) {
    snapshot := Snapshot{Items: []Item{
        {ID: "a", Content: "A", Status: StatusCompleted},
        {ID: "b", Content: "B", Status: StatusCompleted},
    }}
    if !snapshot.AllCompleted() || snapshot.CompletedCount() != 2 || snapshot.TotalCount() != 2 {
        t.Fatalf("unexpected progress: %d/%d all=%v", snapshot.CompletedCount(), snapshot.TotalCount(), snapshot.AllCompleted())
    }
}
```

- [ ] **步骤 5：格式化并运行测试**

```bash
gofmt -w internal/todo/types.go internal/todo/types_test.go
go test ./internal/todo -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/todo/types.go internal/todo/types_test.go
git commit -m "feat: define todo snapshot protocol"
```

---

### 任务 2：实现完整快照规范化与严格校验

**文件：**
- 创建：`internal/todo/validate.go`
- 创建：`internal/todo/validate_test.go`

- [ ] **步骤 1：编写合法输入和规范化失败测试**

创建 `internal/todo/validate_test.go`：

```go
package todo

import (
    "encoding/json"
    "reflect"
    "testing"
)

func TestDecodeUpdateInputNormalizesWhitespace(t *testing.T) {
    got, err := DecodeUpdateInput(json.RawMessage(`{
        "explanation":"  start implementation  ",
        "items":[
            {"id":" inspect ","content":" Inspect existing code ","status":"completed"},
            {"id":"build","content":" Build Todo page ","status":"in_progress"}
        ]
    }`))
    if err != nil {
        t.Fatalf("DecodeUpdateInput() error = %v", err)
    }
    want := UpdateInput{
        Explanation: "start implementation",
        Items: []Item{
            {ID: "inspect", Content: "Inspect existing code", Status: StatusCompleted},
            {ID: "build", Content: "Build Todo page", Status: StatusInProgress},
        },
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("DecodeUpdateInput() = %#v, want %#v", got, want)
    }
}

func TestDecodeUpdateInputAcceptsClear(t *testing.T) {
    got, err := DecodeUpdateInput(json.RawMessage(`{"items":[]}`))
    if err != nil {
        t.Fatalf("DecodeUpdateInput() error = %v", err)
    }
    if got.Items == nil || len(got.Items) != 0 {
        t.Fatalf("Items = %#v, want non-nil empty slice", got.Items)
    }
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/todo -run TestDecodeUpdateInput -count=1
```

预期：FAIL，`DecodeUpdateInput` 尚不存在。

- [ ] **步骤 3：实现拒绝未知字段的解码器**

创建 `internal/todo/validate.go`。使用 `json.Decoder` 和 `DisallowUnknownFields()`，并确认输入只包含一个 JSON 值：

```go
package todo

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "strings"
)

func DecodeUpdateInput(raw json.RawMessage) (UpdateInput, error) {
    decoder := json.NewDecoder(bytes.NewReader(raw))
    decoder.DisallowUnknownFields()

    var input UpdateInput
    if err := decoder.Decode(&input); err != nil {
        return UpdateInput{}, fmt.Errorf("decode update_todo input: %w", err)
    }
    if err := ensureJSONEOF(decoder); err != nil {
        return UpdateInput{}, err
    }
    if input.Items == nil {
        return UpdateInput{}, fmt.Errorf("items is required")
    }
    return NormalizeAndValidate(input)
}

func ensureJSONEOF(decoder *json.Decoder) error {
    var extra any
    if err := decoder.Decode(&extra); err == io.EOF {
        return nil
    } else if err != nil {
        return fmt.Errorf("decode update_todo input: %w", err)
    }
    return fmt.Errorf("decode update_todo input: multiple JSON values")
}
```

`NormalizeAndValidate` 必须可供历史恢复复用，不能只接受原始 JSON。

- [ ] **步骤 4：编写全部稳定错误文本测试**

增加表驱动测试：

```go
func TestDecodeUpdateInputRejectsInvalidSnapshots(t *testing.T) {
    tests := []struct {
        name string
        raw  string
        want string
    }{
        {"missing items", `{}`, "items is required"},
        {"empty id", `{"items":[{"id":" ","content":"A","status":"pending"}]}`, "items[0].id must not be empty"},
        {"empty content", `{"items":[{"id":"a","content":" ","status":"pending"}]}`, "items[0].content must not be empty"},
        {"duplicate id", `{"items":[{"id":"a","content":"A","status":"pending"},{"id":"a","content":"Again","status":"completed"}]}`, `items contains duplicate id "a"`},
        {"invalid status", `{"items":[{"id":"a","content":"A","status":"blocked"}]}`, "items[0].status must be pending, in_progress, or completed"},
        {"multiple active", `{"items":[{"id":"a","content":"A","status":"in_progress"},{"id":"b","content":"B","status":"in_progress"}]}`, "items may contain at most one in_progress item"},
        {"unknown top field", `{"items":[],"phase":"build"}`, `decode update_todo input: json: unknown field "phase"`},
        {"unknown item field", `{"items":[{"id":"a","content":"A","status":"pending","depends_on":[]}]}`, `decode update_todo input: json: unknown field "depends_on"`},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := DecodeUpdateInput(json.RawMessage(tt.raw))
            if err == nil || err.Error() != tt.want {
                t.Fatalf("error = %v, want %q", err, tt.want)
            }
        })
    }
}
```

另加测试确认：

- 零个 `in_progress` 合法；
- ID 大小写不合并；
- 同一 ID 仅内部空格不同不被改写；
- 可增加、删除、重排、改写事项；
- 不设置首版数量上限。

- [ ] **步骤 5：实现规范化和校验**

```go
func NormalizeAndValidate(input UpdateInput) (UpdateInput, error) {
    input.Explanation = strings.TrimSpace(input.Explanation)
    input.Items = append([]Item{}, input.Items...)

    seen := make(map[string]struct{}, len(input.Items))
    active := 0
    for i := range input.Items {
        item := &input.Items[i]
        item.ID = strings.TrimSpace(item.ID)
        item.Content = strings.TrimSpace(item.Content)
        if item.ID == "" {
            return UpdateInput{}, fmt.Errorf("items[%d].id must not be empty", i)
        }
        if item.Content == "" {
            return UpdateInput{}, fmt.Errorf("items[%d].content must not be empty", i)
        }
        if _, exists := seen[item.ID]; exists {
            return UpdateInput{}, fmt.Errorf("items contains duplicate id %q", item.ID)
        }
        seen[item.ID] = struct{}{}
        switch item.Status {
        case StatusPending, StatusCompleted:
        case StatusInProgress:
            active++
        default:
            return UpdateInput{}, fmt.Errorf("items[%d].status must be pending, in_progress, or completed", i)
        }
    }
    if active > 1 {
        return UpdateInput{}, fmt.Errorf("items may contain at most one in_progress item")
    }
    return input, nil
}
```

为恢复结果增加：

```go
func ValidateSnapshot(snapshot Snapshot) (Snapshot, error) {
    normalized, err := NormalizeAndValidate(UpdateInput{
        Explanation: snapshot.Explanation,
        Items:       snapshot.Items,
    })
    if err != nil {
        return Snapshot{}, err
    }
    snapshot.Explanation = normalized.Explanation
    snapshot.Items = normalized.Items
    return snapshot, nil
}
```

`ValidateSnapshot` 保留 `UpdatedAt`，不重新生成时间。

- [ ] **步骤 6：运行验证测试**

```bash
gofmt -w internal/todo/validate.go internal/todo/validate_test.go
go test ./internal/todo -run 'TestDecodeUpdateInput|TestNormalizeAndValidate|TestValidateSnapshot' -count=1
```

预期：PASS。

- [ ] **步骤 7：Commit**

```bash
git add internal/todo/validate.go internal/todo/validate_test.go
git commit -m "feat: validate todo snapshots"
```

---

### 任务 3：实现非阻塞 Todo Broker 与 `update_todo` 工具

**文件：**
- 创建：`internal/todo/broker.go`
- 创建：`internal/todo/broker_test.go`
- 创建：`internal/todo/tool.go`
- 创建：`internal/todo/tool_test.go`

- [ ] **步骤 1：编写 Broker 顺序和深拷贝失败测试**

创建 `broker_test.go`：

```go
package todo

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestBrokerPublishesSnapshotsInOrder(t *testing.T) {
    broker := NewBroker()
    first := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusInProgress}}}
    second := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusCompleted}}}

    if !broker.Publish(first) || !broker.Publish(second) {
        t.Fatal("Publish() rejected an open broker")
    }
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()

    gotFirst, err := broker.Next(ctx)
    if err != nil {
        t.Fatal(err)
    }
    gotSecond, err := broker.Next(ctx)
    if err != nil {
        t.Fatal(err)
    }
    if gotFirst.Items[0].Status != StatusInProgress || gotSecond.Items[0].Status != StatusCompleted {
        t.Fatalf("events out of order: %#v %#v", gotFirst, gotSecond)
    }
}

func TestBrokerCopiesPublishedSnapshot(t *testing.T) {
    broker := NewBroker()
    snapshot := Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}}
    broker.Publish(snapshot)
    snapshot.Items[0].Content = "mutated"

    got, err := broker.Next(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    got.Items[0].Content = "consumer mutation"
    latest, ok := broker.Latest()
    if !ok || latest.Items[0].Content != "A" {
        t.Fatalf("broker state mutated: %#v", latest)
    }
}
```

- [ ] **步骤 2：运行 Broker 测试验证失败**

```bash
go test ./internal/todo -run TestBroker -count=1
```

预期：FAIL，Broker 尚不存在。

- [ ] **步骤 3：实现无阻塞事件队列**

创建 `broker.go`：

```go
package todo

import (
    "context"
    "errors"
    "sync"
)

var ErrBrokerClosed = errors.New("todo broker is closed")

type Broker struct {
    mu         sync.Mutex
    events     []Snapshot
    latest     Snapshot
    hasLatest  bool
    wake       chan struct{}
    closed     bool
}

func NewBroker() *Broker {
    return &Broker{wake: make(chan struct{}, 1)}
}
```

实现规则：

- `Publish(snapshot) bool` 在锁内复制并追加事件、更新 latest，然后向容量 1 的 wake channel 尝试发送；不得等待 TUI 消费者。
- 已关闭时返回 false；工具忽略该 false，使应用退出和 headless 场景不阻塞、不把合法更新转为错误。
- `Next(ctx)` 优先从事件队列弹出；队列空且 closed 时返回 `ErrBrokerClosed`；否则等待 `ctx.Done()` 或 wake。
- `Latest()` 返回 `(Snapshot, bool)` 并深拷贝。
- `Close()` 幂等，标记 closed 并唤醒等待者；不清空已排队事件，`Next` 先耗尽队列，再返回 closed。

- [ ] **步骤 4：补齐 Broker 生命周期测试**

增加：

```go
func TestBrokerNextHonorsContext(t *testing.T) {
    broker := NewBroker()
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, err := broker.Next(ctx)
    if !errors.Is(err, context.Canceled) {
        t.Fatalf("Next() error = %v", err)
    }
}

func TestBrokerCloseWakesNext(t *testing.T) {
    broker := NewBroker()
    errCh := make(chan error, 1)
    go func() {
        _, err := broker.Next(context.Background())
        errCh <- err
    }()
    broker.Close()
    select {
    case err := <-errCh:
        if !errors.Is(err, ErrBrokerClosed) {
            t.Fatalf("Next() error = %v", err)
        }
    case <-time.After(time.Second):
        t.Fatal("Next() remained blocked after Close")
    }
}

func TestBrokerPublishDoesNotWaitForConsumer(t *testing.T) {
    broker := NewBroker()
    done := make(chan struct{})
    go func() {
        for i := 0; i < 1000; i++ {
            broker.Publish(Snapshot{Items: []Item{{ID: "a", Content: "A", Status: StatusPending}}})
        }
        close(done)
    }()
    select {
    case <-done:
    case <-time.After(time.Second):
        t.Fatal("Publish() blocked without a consumer")
    }
}
```

- [ ] **步骤 5：编写工具元数据、Schema 和结果失败测试**

创建 `tool_test.go`：

```go
func TestToolMetadata(t *testing.T) {
    tool := NewTool(nil)
    if tool.Name() != "update_todo" {
        t.Fatalf("Name() = %q", tool.Name())
    }
    description := tool.Description()
    for _, phrase := range []string{"complex", "full", "in_progress", "simple"} {
        if !strings.Contains(description, phrase) {
            t.Fatalf("Description() missing %q: %q", phrase, description)
        }
    }
    var schema map[string]any
    if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
        t.Fatalf("schema error: %v", err)
    }
    if schema["additionalProperties"] != false {
        t.Fatalf("root additionalProperties = %#v", schema["additionalProperties"])
    }
}

func TestToolRunReturnsAuthoritativeSnapshot(t *testing.T) {
    broker := NewBroker()
    fixed := time.Date(2026, 8, 2, 12, 34, 56, 0, time.UTC)
    tool := NewTool(broker)
    tool.nowFn = func() time.Time { return fixed }

    got, err := tool.Run(context.Background(), json.RawMessage(`{
        "explanation":" start ",
        "items":[{"id":"build","content":" Build page ","status":"in_progress"}]
    }`))
    if err != nil {
        t.Fatalf("Run() error = %v", err)
    }
    want := `{"accepted":true,"snapshot":{"explanation":"start","items":[{"id":"build","content":"Build page","status":"in_progress"}],"updated_at":"2026-08-02T12:34:56Z"}}`
    if got != want {
        t.Fatalf("Run() = %s, want %s", got, want)
    }
    published, err := broker.Next(context.Background())
    if err != nil || published.UpdatedAt != fixed {
        t.Fatalf("published = %#v, error = %v", published, err)
    }
}
```

- [ ] **步骤 6：实现工具和严格 Schema**

创建 `tool.go`：

```go
package todo

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
)

type Tool struct {
    broker *Broker
    nowFn  func() time.Time
}

func NewTool(broker *Broker) *Tool {
    return &Tool{broker: broker, nowFn: time.Now}
}

func (*Tool) Name() string { return "update_todo" }
func (*Tool) Description() string {
    return "Maintain a full todo snapshot for complex multi-step work. Use it before substantial execution and when status materially changes; do not create a todo list for simple questions or one-step edits. Submit the complete ordered list every time, preserve stable item ids, use only pending/in_progress/completed, and keep at most one item in_progress."
}
func (*Tool) InputSchema() json.RawMessage { return json.RawMessage(updateTodoInputSchema) }
func (*Tool) IsConcurrencySafe(json.RawMessage) bool { return true }
```

Schema 必须包含：

```json
{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "explanation": {"type": "string"},
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "id": {"type": "string"},
          "content": {"type": "string"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
        },
        "required": ["id", "content", "status"]
      }
    }
  },
  "required": ["items"]
}
```

`Run`：

```go
func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    input, err := DecodeUpdateInput(raw)
    if err != nil {
        return "", err
    }
    nowFn := time.Now
    if t != nil && t.nowFn != nil {
        nowFn = t.nowFn
    }
    snapshot := Snapshot{
        Explanation: input.Explanation,
        Items:       append([]Item{}, input.Items...),
        UpdatedAt:   nowFn().UTC(),
    }
    if t != nil && t.broker != nil {
        t.broker.Publish(snapshot)
    }
    data, err := json.Marshal(UpdateResult{Accepted: true, Snapshot: snapshot})
    if err != nil {
        return "", fmt.Errorf("encode update_todo result: %w", err)
    }
    return string(data), nil
}
```

nil Broker 是合法 headless 行为；nil Tool receiver 也必须能执行校验和返回结果，不 panic。

- [ ] **步骤 7：补齐失败不发布和清除测试**

覆盖：

- 无效输入返回稳定错误，`broker.Next` 使用短 deadline 验证没有事件；
- `items: []` 返回非 nil 空数组并发布 cleared snapshot；
- context 已取消时不解析、不发布；
- Broker 已关闭时工具仍成功返回结果；
- `UpdatedAt` 永远覆盖模型无法提供的时间字段，因为 Schema 和解码器拒绝 `updated_at`；
- `IsConcurrencySafe` 返回 true。

- [ ] **步骤 8：运行包测试和 race detector**

```bash
gofmt -w internal/todo/broker.go internal/todo/broker_test.go internal/todo/tool.go internal/todo/tool_test.go
go test ./internal/todo -count=1
go test -race ./internal/todo -count=1
```

预期：PASS，无 race。

- [ ] **步骤 9：Commit**

```bash
git add internal/todo/broker.go internal/todo/broker_test.go internal/todo/tool.go internal/todo/tool_test.go
git commit -m "feat: add update_todo tool and broker"
```

---

### 任务 4：装配主 Agent 工具并保持 subagent 隔离

**文件：**
- 修改：`cmd/agent/main.go`
- 修改：`cmd/agent/register_test.go`
- 修改：`internal/subagent/manager_test.go`

- [ ] **步骤 1：编写主 Agent 注册失败测试**

在 `cmd/agent/register_test.go` 增加：

```go
func TestRegisterInteractiveToolsAddsUpdateTodo(t *testing.T) {
    registry := tool.NewRegistry()
    selectionBroker := selecttool.NewBroker()
    todoBroker := todo.NewBroker()
    defer selectionBroker.Close()
    defer todoBroker.Close()

    if err := registerInteractiveTools(registry, selectionBroker, todoBroker); err != nil {
        t.Fatalf("registerInteractiveTools() error = %v", err)
    }
    registered, ok := registry.Get("update_todo")
    if !ok {
        t.Fatal("interactive registry missing update_todo")
    }
    if _, ok := registered.(*todo.Tool); !ok {
        t.Fatalf("update_todo type = %T", registered)
    }
}

func TestRegisterMainToolsAddsHeadlessUpdateTodo(t *testing.T) {
    registry := tool.NewRegistry()
    registerMainAgentTools(registry, nil)
    if _, ok := registry.Get("update_todo"); !ok {
        t.Fatal("main registry missing headless-safe update_todo")
    }
}
```

这里把注册职责明确拆成：

- `registerMainAgentTools(registry, todoBroker)`：主 Agent 通用工具，single-turn 和 interactive 都调用。
- `registerInteractiveTools(registry, selectionBroker)`：只注册需要 TUI 用户交互的 Select。

不要把 Todo 与 Select 都塞进同一个“interactive only” helper，否则 `agent -p` 无法维护并持久化结构化 Todo。

- [ ] **步骤 2：编写 subagent 不注册的失败测试**

在 `internal/subagent/manager_test.go` 增加同包测试：

```go
func TestBaseToolRegistryDoesNotContainUpdateTodo(t *testing.T) {
    registry := newBaseToolRegistry(t.TempDir())
    if _, ok := registry.Get("update_todo"); ok {
        t.Fatal("subagent base registry unexpectedly contains update_todo")
    }
}
```

- [ ] **步骤 3：运行注册测试验证失败**

```bash
go test ./cmd/agent -run 'TestRegister.*UpdateTodo' -count=1
go test ./internal/subagent -run TestBaseToolRegistryDoesNotContainUpdateTodo -count=1
```

预期：cmd 测试 FAIL；subagent 测试应 PASS 或因测试 helper 签名差异编译失败。

- [ ] **步骤 4：实现主 Agent 注册 helper**

在 `cmd/agent/main.go` 引入：

```go
import todo "paw/internal/todo"
```

实现：

```go
func registerMainAgentTools(registry *tool.Registry, broker *todo.Broker) error {
    if registry == nil {
        return fmt.Errorf("tool registry is nil")
    }
    registry.Register(todo.NewTool(broker))
    return nil
}
```

在 `buildRunnerWithSubagentContext` 完成 `registerTools(...)` 后、执行可选 configurator 前调用 `registerMainAgentTools`。为避免 worker/subagent 获得该工具，给 `subagentRuntimeContext` 增加明确布尔字段：

```go
type subagentRuntimeContext struct {
    // 现有字段保持不变
    disableMainTodo bool
}
```

主 single-turn 和 interactive 默认 false；`runSubagentWorkerMode` 构造 context 时设置 `disableMainTodo: true`。注册代码：

```go
if !subCtx.disableMainTodo {
    if err := registerMainAgentTools(registry, nil); err != nil {
        // 使用现有 mcpManager 清理路径返回错误
    }
}
```

interactive 需要带 Broker，因此不要先注册 nil 后覆盖。给 `runnerToolConfigurator` 继续用于 interactive，在 configurator 中重复注册同名工具会替换 map 值，但计划要求避免隐式覆盖。更清晰的最终签名是为构建函数增加可选装配结构：

```go
type runnerToolOptions struct {
    todoBroker    *todo.Broker
    configurators []runnerToolConfigurator
}
```

为控制变更面，实际实现可保留 variadic configurator，并让 interactive configurator只执行：

```go
registry.Register(todo.NewTool(todoBroker))
return registerInteractiveTools(registry, selectionBroker)
```

同时基础构建先注册 `todo.NewTool(nil)`。Registry 的同名覆盖是现有、确定的行为。测试必须断言 interactive 最终工具持有非 nil Broker：执行工具并从 Broker 读取事件，而不是读取未导出字段。

- [ ] **步骤 5：在 interactive 模式创建 Todo Broker**

在 `runInteractiveMode` 紧邻 Selection Broker：

```go
output := bubbleui.New()
selectionBroker := selecttool.NewBroker()
todoBroker := todo.NewBroker()
defer selectionBroker.Close()
defer todoBroker.Close()
output.SetSelectionBroker(selectionBroker)
output.SetTodoBroker(todoBroker)
```

传入 configurator：

```go
func(registry *tool.Registry) error {
    registry.Register(todo.NewTool(todoBroker))
    return registerInteractiveTools(registry, selectionBroker)
}
```

`registerInteractiveTools` 继续只负责 Select。更新其测试签名，不改变既有 Select 注册语义。

- [ ] **步骤 6：确认 worker 和 Manager Registry 均不包含 Todo**

增加测试或扩展现有构建夹具，断言：

- 普通 main registry 有 `update_todo`；
- interactive main registry 有带发布能力的 `update_todo`；
- `newBaseToolRegistry` 没有 `update_todo`；
- `runSubagentWorkerMode` 的构建 context 设置 `disableMainTodo`；
- 现有 Subagent、SubagentStatus、SubagentStop、Select 和 MCP 注册不回归。

- [ ] **步骤 7：运行注册测试**

```bash
gofmt -w cmd/agent/main.go cmd/agent/register_test.go internal/subagent/manager_test.go
go test ./cmd/agent -count=1
go test ./internal/subagent -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add cmd/agent/main.go cmd/agent/register_test.go internal/subagent/manager_test.go
git commit -m "feat: register update_todo for main agents"
```

---

### 任务 5：接入 Bubble Todo 事件与当前状态

**文件：**
- 创建：`internal/ui/bubble/todo_state.go`
- 创建：`internal/ui/bubble/todo_state_test.go`
- 修改：`internal/ui/bubble/bubble.go`
- 修改：`internal/ui/bubble/app.go`
- 修改：`internal/ui/bubble/types.go`
- 修改：`internal/ui/bubble/bubble_test.go`

- [ ] **步骤 1：编写 UI Broker 注入和事件失败测试**

在 `todo_state_test.go`：

```go
func TestTodoBrokerEventCreatesCurrentSnapshot(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    snapshot := todo.Snapshot{
        Explanation: "start",
        Items: []todo.Item{{ID: "build", Content: "Build page", Status: todo.StatusInProgress}},
        UpdatedAt: time.Unix(100, 0).UTC(),
    }

    next, cmd := model.Update(todoBrokerEventMsg{snapshot: snapshot})
    got := next.(appModel)
    if !got.hasCurrentTodo {
        t.Fatal("hasCurrentTodo = false")
    }
    if got.currentTodo.Items[0].ID != "build" {
        t.Fatalf("currentTodo = %#v", got.currentTodo)
    }
    if cmd == nil {
        t.Fatal("Todo event did not schedule the next broker wait")
    }
}

func TestTodoBrokerClearResetsCurrentSnapshot(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "A", Status: todo.StatusPending}}}, true)
    model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{}}, true)
    if model.hasCurrentTodo || !model.todoWasCleared {
        t.Fatalf("state = has:%v cleared:%v", model.hasCurrentTodo, model.todoWasCleared)
    }
}
```

- [ ] **步骤 2：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run TestTodoBroker -count=1
```

预期：FAIL，Todo UI 状态不存在。

- [ ] **步骤 3：扩展 UI 和 appModel 状态**

在 `bubble.UI` 增加：

```go
todoBroker *todo.Broker

func (u *UI) SetTodoBroker(broker *todo.Broker) {
    u.mu.Lock()
    defer u.mu.Unlock()
    u.todoBroker = broker
}
```

在 `appModel` 增加：

```go
todoBroker       *todo.Broker
currentTodo      todo.Snapshot
hasCurrentTodo   bool
todoWasCleared   bool
latestTodoIndex  int
todoPage         *todoPage
```

`newModel` 初始化 `latestTodoIndex: -1`。扩展 `newModel` 签名的最后一个参数为 `todoBroker *todo.Broker`，在 `bubble.UI.Run` 取锁快照后传入。更新所有直接调用 `newModel` 的测试统一传 `nil`；不要在测试 helper 中偷偷创建 Broker。

- [ ] **步骤 4：实现等待 command 和消息类型**

在 `todo_state.go`：

```go
type todoBrokerEventMsg struct {
    snapshot todo.Snapshot
    err      error
}

func waitTodoBrokerEventCmd(ctx context.Context, broker *todo.Broker) tea.Cmd {
    if broker == nil {
        return nil
    }
    return func() tea.Msg {
        snapshot, err := broker.Next(ctx)
        return todoBrokerEventMsg{snapshot: snapshot, err: err}
    }
}
```

在 `Init()` 中加入等待命令。`Update` 处理规则：

- `ErrBrokerClosed` 或 `context.Canceled`：不新增 entry，不继续监听；
- 其他错误：不修改当前 Todo，继续监听；
- 合法快照：调用 `applyTodoSnapshot(snapshot, true)`，然后继续监听；
- 对收到的快照再次调用 `todo.ValidateSnapshot`，防止测试或错误发布者绕过工具校验；失败时忽略且不插卡。

- [ ] **步骤 5：实现当前状态和专用 entry 插入**

先在 `entryKind` 新增 `entryTodo`，在 `transcriptEntry` 增加：

```go
todoSnapshot       *todo.Snapshot
todoExpanded       bool
todoLatest          bool
todoCompletedFold   bool
todoCleared         bool
```

`applyTodoSnapshot(snapshot, live)`：

1. 深拷贝并校验 snapshot。
2. 若 `latestTodoIndex` 有效，将旧 entry 的 `todoLatest=false`、`todoExpanded=false` 并 `touchTranscriptEntry`。
3. 非空快照：设置 `currentTodo`、`hasCurrentTodo=true`、`todoWasCleared=false`；追加 `entryTodo{todoSnapshot:&copy,todoExpanded:true,todoLatest:true}`。
4. 空快照：清空 current，设置 `todoWasCleared=true`；追加 `entryTodo{todoSnapshot:&copy,todoCleared:true,todoLatest:true,todoExpanded:false}`。
5. 新 entry index 写入 `latestTodoIndex`。
6. Todo 页面若已打开，调用 `todoPage.resetForSnapshot()` 把滚动回到顶部。
7. live=true 时使用 `addEntry`，从而复用 smart-scroll 和新消息计数；恢复时直接 append 并在全部恢复后统一 refresh，避免产生未读计数。

- [ ] **步骤 6：测试旧快照折叠和深拷贝**

加入：

```go
func TestApplyingNewTodoCollapsesPreviousSnapshot(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
    model.transcript[model.latestTodoIndex].todoExpanded = true
    model.applyTodoSnapshot(testTodoSnapshot(todo.StatusCompleted), true)

    if model.transcript[0].todoExpanded || model.transcript[0].todoLatest {
        t.Fatalf("old entry not collapsed: %#v", model.transcript[0])
    }
    latest := model.transcript[model.latestTodoIndex]
    if !latest.todoExpanded || !latest.todoLatest {
        t.Fatalf("latest entry not expanded: %#v", latest)
    }
}
```

另测修改源 snapshot 后不影响 currentTodo 和 entry。

- [ ] **步骤 7：运行 Bubble 状态测试**

```bash
gofmt -w internal/ui/bubble/todo_state.go internal/ui/bubble/todo_state_test.go internal/ui/bubble/bubble.go internal/ui/bubble/app.go internal/ui/bubble/types.go internal/ui/bubble/bubble_test.go
go test ./internal/ui/bubble -run 'TestTodoBroker|TestApplyingNewTodo|TestTodoSnapshot' -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/ui/bubble/todo_state.go internal/ui/bubble/todo_state_test.go internal/ui/bubble/bubble.go internal/ui/bubble/app.go internal/ui/bubble/types.go internal/ui/bubble/bubble_test.go
git commit -m "feat: connect todo snapshots to Bubble Tea"
```

---

### 任务 6：实现 transcript Todo 卡片、摘要和展开交互

**文件：**
- 创建：`internal/ui/bubble/todo_render.go`
- 创建：`internal/ui/bubble/todo_render_test.go`
- 修改：`internal/ui/bubble/transcript.go`
- 修改：`internal/ui/bubble/tool_inspect.go`
- 修改：`internal/ui/bubble/selection.go`
- 修改：`internal/ui/bubble/styles.go`
- 修改：`internal/ui/bubble/types.go`

- [ ] **步骤 1：编写折叠摘要和完整卡片失败测试**

创建 `todo_render_test.go`：

```go
func TestRenderCollapsedTodoUpdated(t *testing.T) {
    entry := transcriptEntry{
        kind: entryTodo,
        todoSnapshot: &todo.Snapshot{Items: []todo.Item{
            {ID: "a", Content: "A", Status: todo.StatusCompleted},
            {ID: "b", Content: "B", Status: todo.StatusInProgress},
            {ID: "c", Content: "C", Status: todo.StatusPending},
        }},
        todoExpanded: false,
    }
    got := ansi.Strip(renderEntry(entry, 80))
    if !strings.Contains(got, "▸ Todo updated · 1/3") {
        t.Fatalf("render = %q", got)
    }
}

func TestRenderExpandedTodoCard(t *testing.T) {
    entry := transcriptEntry{
        kind: entryTodo,
        todoSnapshot: &todo.Snapshot{
            Explanation: "Start implementation",
            Items: []todo.Item{
                {ID: "a", Content: "Inspect history", Status: todo.StatusCompleted},
                {ID: "b", Content: "Build page", Status: todo.StatusInProgress},
                {ID: "c", Content: "Add tests", Status: todo.StatusPending},
            },
        },
        todoExpanded: true,
        todoLatest: true,
    }
    got := ansi.Strip(renderEntry(entry, 80))
    for _, want := range []string{"Todo", "1/3", "✓", "Inspect history", "●", "Build page", "○", "Add tests", "Start implementation"} {
        if !strings.Contains(got, want) {
            t.Fatalf("render missing %q: %q", want, got)
        }
    }
}
```

- [ ] **步骤 2：运行渲染测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestRenderCollapsedTodo|TestRenderExpandedTodo' -count=1
```

预期：FAIL，专用渲染不存在。

- [ ] **步骤 3：新增 Todo 语义样式**

在 `StyleSet` 增加：

```go
TodoTitle       lipgloss.Style
TodoCount       lipgloss.Style
TodoCompleted   lipgloss.Style
TodoInProgress  lipgloss.Style
TodoPending     lipgloss.Style
TodoExplanation lipgloss.Style
TodoSummary     lipgloss.Style
```

在 `NewStyleSet` 中只使用当前主题已有的 success、signal/active、muted、foreground、background 颜色。不要新增固定 RGB 常量。`NO_COLOR` 由现有 color manager 处理，文字图标仍保留。

- [ ] **步骤 4：实现纯 Todo 渲染函数**

在 `todo_render.go` 实现：

```go
func renderTodoEntry(entry transcriptEntry, width int) string
func renderTodoCollapsed(snapshot todo.Snapshot, completedFold, cleared bool, width int) string
func renderTodoExpanded(snapshot todo.Snapshot, width int) string
func renderTodoItem(item todo.Item, width int, showStatusLabel bool) []string
func todoStatusDisplay(status todo.Status) (icon, label string)
```

稳定文案：

- 普通旧快照：`▸ Todo updated · N/M`
- 完成折叠：`✓ Todo completed · M/M`
- 清除：`─ Todo cleared`
- 状态标签：`已完成`、`进行中`、`待处理`

布局规则：

1. 标题行左侧 `Todo`，右侧 `N/M`。
2. 宽度至少 24 时显示右侧状态标签；更窄时隐藏标签，保留图标和内容。
3. 内容宽度使用终端 cell 辅助函数，不使用 `len`。
4. 长内容用现有 `wrapDisplayWidth` 或同语义 helper 换行，续行与正文起点对齐。
5. Explanation 与事项之间空一行，使用次要样式。
6. `width <= 0` 返回空字符串；宽度 1–8 仍不 panic。

- [ ] **步骤 5：接入 transcript 渲染和缓存**

在 `renderEntryAt` 最前面处理：

```go
if entry.kind == entryTodo {
    return indentLines(renderTodoEntry(entry, transcriptBodyWidth(width)), transcriptEntryGutter)
}
```

在 `renderEntryBodyAt` 不再处理 Todo，避免显示普通 label。

扩展 `transcriptRenderCacheKey`：

```go
todoSnapshotJSON string
todoExpanded     bool
todoLatest       bool
todoCompletedFold bool
todoCleared      bool
```

使用 `json.Marshal(entry.todoSnapshot)` 生成稳定 cache key；仅在构建 key 时执行，不把 JSON 显示给用户。

`assistantEntryIsRenderable` 对 Todo 不做特殊过滤。`transcriptEntrySeparator` 让 Todo 与相邻 tool/assistant 之间使用一个空行以上，避免卡片粘连；同类 Todo 摘要之间只需一行。

- [ ] **步骤 6：确保 transcript 位置计算包含 Todo 高度**

`transcriptEntryLocationsWith` 已对非工具 entry 调用 `renderEntryAt`，新增测试锁定：

```go
func TestTodoTranscriptLocationMatchesRenderedHeight(t *testing.T) {
    entries := []transcriptEntry{expandedTodoEntryWithLongContent()}
    locations := transcriptEntryLocations(entries, 40, true, time.Time{})
    rendered := renderEntry(entries[0], 40)
    if len(locations) != 1 || locations[0].height != lipgloss.Height(rendered) {
        t.Fatalf("locations = %#v, rendered height = %d", locations, lipgloss.Height(rendered))
    }
}
```

- [ ] **步骤 7：实现鼠标切换展开状态**

在 `selection.go` 的 transcript 左键点击路径中，在工具组命中处理之前加入：

```go
func (m *appModel) toggleTodoAtTranscriptRow(row int) bool {
    index := m.transcriptIndexAtRow(row)
    if index < 0 || index >= len(m.transcript) || m.transcript[index].kind != entryTodo {
        return false
    }
    entry := &m.transcript[index]
    if entry.todoCleared {
        return true
    }
    entry.todoExpanded = !entry.todoExpanded
    touchTranscriptEntry(entry)
    m.invalidateTranscriptLocations()
    m.refreshViewportPreservingOffset()
    return true
}
```

复用 `transcriptEntryLocationsAt()` 做 row → entry 映射；不要建立第二套行高算法。鼠标 press/release 的最终位置仍必须在同一 Todo entry 内才切换，避免文本拖选误触。点击展开不调用 `recordTranscriptEntryActivity`。

若当前 transcript 没有普通 entry 的键盘焦点模型，不新增一套仅 Todo 使用的全局焦点；规格中的 Enter 复用是条件要求，首版以鼠标切换为准。

- [ ] **步骤 8：补齐宽度、状态和清除测试**

表驱动宽度 `120 / 80 / 40 / 20 / 8 / 1`：

```go
for _, width := range []int{120, 80, 40, 20, 8, 1} {
    rendered := renderTodoEntry(entry, width)
    for _, line := range strings.Split(rendered, "\n") {
        if terminalCellWidth(line) > width {
            t.Fatalf("width %d overflow: %q", width, line)
        }
    }
}
```

覆盖中文、英文、emoji、全角字符、空 explanation、全部完成、零项清除、未知 status 防御性降级。未知 status 显示 `?` 和原内容，不 panic。

- [ ] **步骤 9：运行渲染和鼠标测试**

```bash
gofmt -w internal/ui/bubble/todo_render.go internal/ui/bubble/todo_render_test.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_inspect.go internal/ui/bubble/selection.go internal/ui/bubble/styles.go internal/ui/bubble/types.go
go test ./internal/ui/bubble -run 'Test.*Todo.*(Render|Transcript|Mouse|Location|Width)' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 10：Commit**

```bash
git add internal/ui/bubble/todo_render.go internal/ui/bubble/todo_render_test.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_inspect.go internal/ui/bubble/selection.go internal/ui/bubble/styles.go internal/ui/bubble/types.go
git commit -m "feat: render collapsible todo transcript cards"
```

---

### 任务 7：实现最终回答后的完成折叠

**文件：**
- 修改：`internal/ui/bubble/todo_state.go`
- 修改：`internal/ui/bubble/todo_state_test.go`
- 修改：`internal/ui/bubble/app.go`

- [ ] **步骤 1：编写完成折叠失败测试**

在 `todo_state_test.go`：

```go
func TestCompletedTodoStaysExpandedBeforeFinalAnswer(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{
        {ID: "a", Content: "A", Status: todo.StatusCompleted},
        {ID: "b", Content: "B", Status: todo.StatusCompleted},
    }}, true)
    entry := model.transcript[model.latestTodoIndex]
    if !entry.todoExpanded || entry.todoCompletedFold {
        t.Fatalf("completed snapshot folded too early: %#v", entry)
    }
}

func TestSuccessfulFinalAnswerCollapsesCompletedTodo(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.applyTodoSnapshot(allCompletedTodo(), true)
    model.turnHasModelOutput = true

    next, _ := model.Update(turnFinishedMsg{})
    got := next.(appModel)
    entry := got.transcript[got.latestTodoIndex]
    if entry.todoExpanded || !entry.todoCompletedFold {
        t.Fatalf("completed snapshot not folded: %#v", entry)
    }
}
```

- [ ] **步骤 2：编写不得折叠的失败测试**

覆盖：

```go
func TestFinalAnswerDoesNotFoldIncompleteTodo(t *testing.T)
func TestCancelledTurnDoesNotFoldCompletedTodo(t *testing.T)
func TestFailedTurnDoesNotFoldCompletedTodo(t *testing.T)
func TestEmptyAssistantDoesNotFoldCompletedTodo(t *testing.T)
```

使用当前 `turnFinishedMsg` 字段构造 `context.Canceled`、普通 error 和无模型输出状态。

- [ ] **步骤 3：实现单一折叠入口**

在 `todo_state.go`：

```go
func (m *appModel) foldCompletedTodoAfterFinalAnswer() {
    if m == nil || !m.hasCurrentTodo || !m.currentTodo.AllCompleted() {
        return
    }
    index := m.latestTodoIndex
    if index < 0 || index >= len(m.transcript) {
        return
    }
    entry := &m.transcript[index]
    if entry.kind != entryTodo || entry.todoSnapshot == nil || !entry.todoSnapshot.AllCompleted() {
        return
    }
    entry.todoExpanded = false
    entry.todoCompletedFold = true
    touchTranscriptEntry(entry)
    m.invalidateTranscriptLocations()
}
```

在 `turnFinishedMsg` 路径中，仅当：

- `msg.err == nil`；
- 当前回合确实产生可见 Assistant 输出；
- Assistant 流已 finalize；

才调用该方法。调用后按当前 viewport 底部状态刷新，折叠不增加新消息计数。

不要在 `doneMsg` 中提前折叠，因为 `doneMsg` 只是流结束信号，真正回合错误仍可能随后到达。

- [ ] **步骤 4：处理新快照覆盖完成折叠**

`applyTodoSnapshot` 插入新快照时：

- 旧完成快照保留 `todoCompletedFold=true`；
- 新快照始终 `todoCompletedFold=false`；
- 新快照即使全部完成也先完整展开。

增加测试：完成折叠后新建下一轮 pending 快照，旧 entry 保持 `✓ Todo completed`，新 entry 展开。

- [ ] **步骤 5：运行完成联动测试**

```bash
gofmt -w internal/ui/bubble/todo_state.go internal/ui/bubble/todo_state_test.go internal/ui/bubble/app.go
go test ./internal/ui/bubble -run 'Test.*Todo.*(Final|Fold|Completed|Cancelled|Failed)' -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/todo_state.go internal/ui/bubble/todo_state_test.go internal/ui/bubble/app.go
git commit -m "feat: fold completed todo after final answer"
```

---

### 任务 8：从 session records 恢复 Todo 历史和当前状态

**文件：**
- 创建：`internal/ui/bubble/todo_restore.go`
- 创建：`internal/ui/bubble/todo_restore_test.go`
- 修改：`internal/ui/bubble/session_picker.go`
- 修改：`internal/ui/bubble/subagent_picker.go`
- 修改：`internal/ui/bubble/types.go`

- [ ] **步骤 1：编写历史投影失败测试**

创建 `todo_restore_test.go`。使用真实 `session.Record`、`message.ToolCall` 和 `message.ToolResult` 构造历史：

```go
func TestRestoreTodoEntriesFromSuccessfulToolResults(t *testing.T) {
    records := []session.Record{
        {
            Seq: 1,
            TurnID: "turn-1",
            Message: message.Message{Role: message.RoleAssistant, ToolUse: &message.ToolCall{
                ID: "call-1", Name: "update_todo", Input: json.RawMessage(`{"items":[{"id":"a","content":"A","status":"in_progress"}]}`),
            }},
        },
        {
            Seq: 2,
            TurnID: "turn-1",
            Message: message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
                ToolUseID: "call-1",
                Content: `{"accepted":true,"snapshot":{"items":[{"id":"a","content":"A","status":"in_progress"}],"updated_at":"2026-08-02T10:00:00Z"}}`,
            }},
        },
    }

    restored := restoreTodoProjection(records)
    if len(restored.Entries) != 1 || !restored.HasCurrent || restored.Current.Items[0].ID != "a" {
        t.Fatalf("projection = %#v", restored)
    }
}
```

- [ ] **步骤 2：运行恢复测试验证失败**

```bash
go test ./internal/ui/bubble -run TestRestoreTodo -count=1
```

预期：FAIL，恢复投影不存在。

- [ ] **步骤 3：实现 tool call/result 配对解析**

在 `todo_restore.go` 定义：

```go
type todoRestoreProjection struct {
    Entries       []transcriptEntry
    Current       todo.Snapshot
    HasCurrent    bool
    WasCleared    bool
    LatestIndex   int
}

func restoreTodoProjection(records []session.Record) todoRestoreProjection
func decodeTodoResult(content string) (todo.Snapshot, bool)
```

扫描规则：

1. 维护 `toolUseID -> {name, turnID}` map；从 `ToolUse` 和 `ToolUses` 收集。
2. 从 `ToolResult` 和 `ToolResults` 收集结果。
3. 只有匹配到名称严格为 `update_todo`、`IsError=false` 的结果才解析。
4. 结果必须 `accepted=true`，并通过 `todo.ValidateSnapshot`。
5. 无法解析、未知 toolUse ID、失败结果、未完成 tool-use 全部忽略。
6. 每次合法结果创建专用 `entryTodo`；新结果到来时折叠此前最新 entry。
7. 空 items 创建 cleared entry，并把 Current 清空。
8. 使用 Snapshot 自带 `UpdatedAt` 作为 entry `createdAt`；时间为零时回退到 result record `CreatedAt`。

`decodeTodoResult` 使用 `json.Decoder.DisallowUnknownFields` 解析 `todo.UpdateResult`。历史若含未来新增字段，为保持前向兼容，根级 unknown field 可以选择普通 `json.Unmarshal`；但 `snapshot` 必须通过当前结构校验。最终实现选择普通 `json.Unmarshal`，避免升级后旧 UI 丢掉整个合法 Todo。

- [ ] **步骤 4：实现“同回合最终 Assistant 已存在”恢复折叠**

记录每个合法 Todo entry 的 `TurnID`。扫描完成后，为每个全部完成的 Todo entry 检查：在该结果之后、下一次 Todo 更新之前，是否存在同一 `TurnID` 的非空最终 Assistant message。

定义非空 Assistant：

```go
func restoredAssistantIsVisible(msg message.Message) bool {
    return msg.Role == message.RoleAssistant && strings.TrimSpace(sanitizeAssistantVisibleBody(msg.Content)) != ""
}
```

如果存在：

```go
entry.todoExpanded = false
entry.todoCompletedFold = true
```

如果没有，保持展开。错误/失败 turn 的 journal projection不会凭空产生正常最终 Assistant，因此不需要从文本猜测成功。

- [ ] **步骤 5：把 Todo entries 插入普通 transcript 的正确位置**

现有 `transcriptEntriesFromRecords` 按 record 顺序生成普通 transcript。不要在恢复完成后把全部 Todo 卡片追加到末尾。重构为单次 record 扫描：

```go
func transcriptEntriesFromRecords(records []session.Record, metadata []session.TurnMetadata, workspaceRoot string) []transcriptEntry
```

在处理每条 tool-result record 后：

1. 保留现有 tool result entry。
2. 若该 result 是合法 `update_todo`，紧接着 append 对应 Todo entry。

为避免重复实现，`todoRestoreTracker` 提供：

```go
type todoRestoreTracker struct { /* call map and latest index */ }
func (t *todoRestoreTracker) ObserveRecord(record session.Record) []transcriptEntry
func (t *todoRestoreTracker) Finalize(entries []transcriptEntry) todoRestoreProjection
```

subagent preview 使用同一个普通转换函数时必须传 `includeMainTodo=false`，防止子代理历史中的同名第三方工具或未来兼容记录覆盖主 Todo。主 session restore 传 true。

- [ ] **步骤 6：在 session restore 应用当前状态**

扩展 `sessionRestoredMsg`：

```go
currentTodo     todo.Snapshot
hasCurrentTodo  bool
todoWasCleared  bool
latestTodoIndex int
```

恢复 command 在后台从 records 构建 entries 和 Todo projection。`applySessionPickerRestore` 在替换 transcript 后同步设置上述 appModel 字段。若无 Todo：

```go
m.currentTodo = todo.Snapshot{}
m.hasCurrentTodo = false
m.todoWasCleared = false
m.latestTodoIndex = -1
m.todoPage = nil
```

session switch 必须关闭当前 Todo 页面，避免页面短暂显示上一会话数据。

- [ ] **步骤 7：补齐损坏、错误和隔离测试**

覆盖：

- 两个合法快照：旧折叠、最新展开；
- 最新为空快照：当前为空、`todoWasCleared=true`；
- 错误 tool result 不覆盖旧合法状态；
- malformed JSON 不覆盖旧状态；
- `accepted:false` 忽略；
- 未知 toolUse ID 忽略；
- 未完成 tool-use 忽略；
- 完成快照后同 turn 有 Assistant final，恢复为 completed 折叠；
- 没有 final，保持完整完成态；
- session A 切到无 Todo 的 session B 后清空 current；
- subagent preview 不修改主 current Todo；
- forked resolved records 按已有顺序自然恢复。

- [ ] **步骤 8：运行恢复和 session 测试**

```bash
gofmt -w internal/ui/bubble/todo_restore.go internal/ui/bubble/todo_restore_test.go internal/ui/bubble/session_picker.go internal/ui/bubble/subagent_picker.go internal/ui/bubble/types.go
go test ./internal/ui/bubble -run 'TestRestoreTodo|Test.*Session.*Todo|Test.*Subagent.*Todo' -count=1
go test ./internal/session -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。正常 JSONL Store 不需要新增 Todo 专用代码；现有 journal 测试证明工具调用和结果会持久化。

- [ ] **步骤 9：Commit**

```bash
git add internal/ui/bubble/todo_restore.go internal/ui/bubble/todo_restore_test.go internal/ui/bubble/session_picker.go internal/ui/bubble/subagent_picker.go internal/ui/bubble/types.go
git commit -m "feat: restore todo snapshots from session history"
```

---

### 任务 9：实现 `Ctrl+P` 独立 Todo 页面

**文件：**
- 创建：`internal/ui/bubble/todo_page.go`
- 创建：`internal/ui/bubble/todo_page_test.go`
- 修改：`internal/ui/bubble/app.go`
- 修改：`internal/ui/bubble/layout.go`
- 修改：`internal/ui/bubble/new_message_notice.go`
- 修改：`internal/ui/bubble/anchor.go`
- 修改：`internal/ui/bubble/fixed_layout_test.go`

- [ ] **步骤 1：编写打开、关闭和快捷键隔离失败测试**

创建 `todo_page_test.go`：

```go
func TestCtrlPOpensTodoPage(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.setInputDraft("keep this draft")

    next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
    got := next.(appModel)
    if got.todoPage == nil {
        t.Fatal("Ctrl+P did not open Todo page")
    }
    if got.input.Value() != "keep this draft" {
        t.Fatalf("draft changed to %q", got.input.Value())
    }
}

func TestTodoPageClosesWithEscapeAndCtrlP(t *testing.T) {
    for _, keyMsg := range []tea.KeyMsg{
        {Type: tea.KeyEsc},
        {Type: tea.KeyCtrlP},
    } {
        model := newTestModel(&fakeRunner{})
        model.todoPage = newTodoPage()
        next, cmd := model.Update(keyMsg)
        got := next.(appModel)
        if got.todoPage != nil {
            t.Fatalf("page remained open for %s", keyMsg.String())
        }
        if cmd == nil {
            t.Fatalf("closing %s did not restore input focus", keyMsg.String())
        }
    }
}

func TestCtrlTStillOpensToolInspect(t *testing.T) {
    // 使用现有可检查工具 entry 夹具，断言 Ctrl+T 仍走 tool inspect，而不是 Todo page。
}
```

- [ ] **步骤 2：运行页面测试验证失败**

```bash
go test ./internal/ui/bubble -run 'TestCtrlP|TestTodoPage|TestCtrlTStill' -count=1
```

预期：FAIL，Todo page 尚不存在。

- [ ] **步骤 3：实现页面状态和按键处理**

在 `todo_page.go`：

```go
type todoPage struct {
    offset int
}

func newTodoPage() *todoPage { return &todoPage{} }

func (p *todoPage) resetForSnapshot() {
    if p != nil {
        p.offset = 0
    }
}
```

按键：

- `esc` / `ctrl+p`：关闭并恢复 `m.input.Focus()`；
- `up` / `k` / `ctrl+p` 的 ctrl+p 已用于关闭；
- `down` / `j`；
- `pgup` / `pgdown`；
- `home` / `g`：顶部；
- `end` / `G`：底部；
- 其他按键全部消费，不进入 textarea、completion、聊天提交或 transcript。

滚动以渲染行 offset 为单位，使用当前内容总高度和页面可见高度 clamp。

- [ ] **步骤 4：按 modal 优先级接入 `Update`**

在 `case tea.KeyMsg` 中保持现有优先级：

1. raw mouse escape 过滤；
2. Selection Dock；
3. 已打开的 theme/settings/model/session/subagent modal；
4. 已打开的 Todo page；
5. Tool Inspect；
6. 普通界面快捷键。

普通界面：

```go
if msg.String() == "ctrl+p" {
    m.todoPage = newTodoPage()
    m.clearCompletionAndRelayout()
    m.input.Blur()
    return m, nil
}
```

Selection Dock 活跃时的 Ctrl+P 由 Dock 消费，不打开 Todo page。已有其他 modal 活跃时由对应 handler 消费。

- [ ] **步骤 5：实现纵向页面渲染**

`renderTodoPage(width, height int)`：

- 页面占应用 frame 内全部 content area，不与 transcript、status line、textarea 并排。
- 顶行：`Todo` 和 `N/M`。
- 第二区域：可选 explanation。
- 事项逐项纵向排列，复用 `renderTodoItem`，不横向摆放多个事项。
- footer：`↑↓ scroll  esc close`；高度不足时优先保留标题、至少一条 item 或空状态、footer。
- 空状态固定文案：

```text
No active todo list

The agent creates one automatically for complex tasks.

esc close
```

- page 内容超过高度时切片 `[offset:offset+visibleRows]`。
- 使用 `fitStyledRect` 保证结果严格等于 width × height。

在 `View()`：

```go
if m.todoPage != nil {
    inner := fitStyledRect(m.renderTodoPage(layout.contentWidth, layout.contentHeight), layout.contentWidth, layout.contentHeight)
    view := renderHairlineFrame(inner, layout.frameWidth, layout.frameHeight)
    view = paintStyledBackground(view, layout.frameWidth, layout.frameHeight, m.styles.Frame, m.theme.Colors.TerminalBackground)
    if m.cursorAnchor != nil { m.cursorAnchor.clear() }
    return view
}
```

页面打开时不渲染 header/transcript/status/input，避免用户误以为仍在编辑聊天。

- [ ] **步骤 6：处理实时更新和空状态**

`applyTodoSnapshot` 已在页面打开时 reset offset。页面每次 View 都读取 `m.currentTodo`，不复制第二份清单。

测试：

- 页面打开后收到新 snapshot，内容立即替换且 offset=0；
- clear snapshot 后显示空状态；
- 从未创建 Todo 时也能打开空状态；
- 页面关闭后 draft 未变且 textarea focus 恢复。

- [ ] **步骤 7：隐藏新消息提示和终端光标**

`newMessageNoticeCanRender()` 增加 `m.todoPage == nil`。未读计数继续保留，页面关闭后恢复显示。

终端 cursor anchor 活动条件增加 `m.todoPage == nil`；页面打开后主动 clear。不要清除 textarea 内容或 paste fold 状态。

- [ ] **步骤 8：编写极小终端和滚动测试**

尺寸：`120x30`、`80x24`、`40x10`、`20x6`、`8x3`。

```go
view := model.View()
plain := ansi.Strip(view)
lines := strings.Split(plain, "\n")
if len(lines) != height {
    t.Fatalf("height = %d, want %d", len(lines), height)
}
for _, line := range lines {
    if terminalCellWidth(line) != width {
        t.Fatalf("line width = %d, want %d: %q", terminalCellWidth(line), width, line)
    }
}
```

长清单测试发送 down/end/up，断言 offset clamp 且页面中可见事项变化。高亮不是选择，不改变任何 Todo status。

- [ ] **步骤 9：运行页面和完整布局测试**

```bash
gofmt -w internal/ui/bubble/todo_page.go internal/ui/bubble/todo_page_test.go internal/ui/bubble/app.go internal/ui/bubble/layout.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/anchor.go internal/ui/bubble/fixed_layout_test.go
go test ./internal/ui/bubble -run 'Test.*TodoPage|TestCtrlP|TestCtrlTStill|Test.*Todo.*Terminal' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 10：Commit**

```bash
git add internal/ui/bubble/todo_page.go internal/ui/bubble/todo_page_test.go internal/ui/bubble/app.go internal/ui/bubble/layout.go internal/ui/bubble/new_message_notice.go internal/ui/bubble/anchor.go internal/ui/bubble/fixed_layout_test.go
git commit -m "feat: add Ctrl-P todo page"
```

---

### 任务 10：压缩 `update_todo` 普通工具轨迹并避免重复 JSON

**文件：**
- 修改：`internal/ui/bubble/tool_display.go`
- 修改：`internal/ui/bubble/transcript.go`
- 修改：`internal/ui/bubble/tool_track_test.go`

- [ ] **步骤 1：编写工具调用不泄漏 items 的失败测试**

在 `tool_track_test.go`：

```go
func TestUpdateTodoToolCallUsesCompactDisplay(t *testing.T) {
    entry := transcriptEntry{
        kind:       entryTool,
        title:      "tool",
        toolName:   "update_todo",
        toolStatus: "running",
        toolInput: json.RawMessage(`{
            "explanation":"start build",
            "items":[
                {"id":"secret-internal-id","content":"Build page","status":"in_progress"},
                {"id":"tests","content":"Add tests","status":"pending"}
            ]
        }`),
    }
    rendered := ansi.Strip(renderEntry(entry, 100))
    if !strings.Contains(rendered, "Todo") {
        t.Fatalf("render = %q", rendered)
    }
    for _, forbidden := range []string{"secret-internal-id", "Build page", "pending", `"items"`} {
        if strings.Contains(rendered, forbidden) {
            t.Fatalf("render leaked %q: %q", forbidden, rendered)
        }
    }
}
```

- [ ] **步骤 2：编写成功和清除摘要失败测试**

```go
func TestUpdateTodoToolResultSummary(t *testing.T) {
    updated := compactUpdateTodoResult(`{"accepted":true,"snapshot":{"items":[{"id":"a","content":"A","status":"completed"},{"id":"b","content":"B","status":"in_progress"}],"updated_at":"2026-08-02T10:00:00Z"}}`)
    if updated != "updated 1/2" {
        t.Fatalf("summary = %q", updated)
    }
    cleared := compactUpdateTodoResult(`{"accepted":true,"snapshot":{"items":[],"updated_at":"2026-08-02T10:00:00Z"}}`)
    if cleared != "cleared" {
        t.Fatalf("summary = %q", cleared)
    }
}
```

- [ ] **步骤 3：运行测试验证失败**

```bash
go test ./internal/ui/bubble -run TestUpdateTodoTool -count=1
```

预期：FAIL。

- [ ] **步骤 4：增加显示目录规则**

在 `tool_display.go` 对规范化工具名 `update_todo` 返回：

```go
toolDisplay{
    Name:   "Todo",
    Action: "update",
}
```

running 主行只显示：

```text
◌ Todo: update  运行中 · 1s
```

不要从 input 提取 target，不显示 explanation 或 items。Tool Inspect 仍使用 `toolInput` 和 `toolResult` 原值，因此完整结构可检查。

- [ ] **步骤 5：实现结果摘要**

```go
func compactUpdateTodoResult(content string) string {
    var result todo.UpdateResult
    if err := json.Unmarshal([]byte(content), &result); err != nil || !result.Accepted {
        return "updated"
    }
    snapshot, err := todo.ValidateSnapshot(result.Snapshot)
    if err != nil {
        return "updated"
    }
    if snapshot.Cleared() {
        return "cleared"
    }
    return fmt.Sprintf("updated %d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
}
```

在工具结果 entry 完成路径中，若 `toolName == "update_todo"` 且 status ok，把 `toolTarget` 或专用摘要字段设为该字符串，使主行显示：

```text
✓ Todo: updated 1/2  完成 · 2ms
✓ Todo: cleared  完成 · 1ms
```

错误结果继续显示 error 状态，不解析 content。

专用 Todo 卡片已经由 Broker 事件插入；`recordToolResultEntry` 不得再次从结果创建卡片。恢复才从 tool result 重建卡片。

- [ ] **步骤 6：测试 Tool Inspect 仍保留完整数据**

加入测试：

- compact transcript 不含 items；
- entry.toolInput 仍与原始 JSON 相同；
- entry.toolResult 仍与完整结果相同；
- Tool Inspect 展开能看到 `Build page`；
- 无效 JSON 结果显示通用 `updated`，不 panic；
- `Ctrl+T` 行为不变。

- [ ] **步骤 7：运行工具轨迹回归**

```bash
gofmt -w internal/ui/bubble/tool_display.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_track_test.go
go test ./internal/ui/bubble -run 'TestUpdateTodoTool|Test.*Tool.*Track|Test.*Tool.*Inspect' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/ui/bubble/tool_display.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_track_test.go
git commit -m "feat: summarize update_todo tool activity"
```

---

### 任务 11：锁定 smart-scroll、新消息计数和会话一致性

**文件：**
- 修改：`internal/ui/bubble/todo_state_test.go`
- 修改：`internal/ui/bubble/new_message_notice_test.go`
- 修改：`internal/ui/bubble/todo_restore_test.go`
- 可能修改：`internal/ui/bubble/todo_state.go`
- 可能修改：`internal/ui/bubble/new_message_notice.go`

- [ ] **步骤 1：编写非底部更新保持 offset 的测试**

```go
func TestTodoUpdatePreservesDetachedScrollAndCountsOnce(t *testing.T) {
    model := preparedScrollableModel()
    model.viewport.SetYOffset(0)
    if model.viewport.AtBottom() {
        t.Fatal("test model unexpectedly at bottom")
    }
    before := model.viewport.YOffset

    model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
    if model.viewport.YOffset != before {
        t.Fatalf("YOffset = %d, want %d", model.viewport.YOffset, before)
    }
    if model.newMessageNoticeCount != 1 {
        t.Fatalf("newMessageNoticeCount = %d, want 1", model.newMessageNoticeCount)
    }

    model.refreshViewportPreservingOffset()
    if model.newMessageNoticeCount != 1 {
        t.Fatalf("refresh recounted Todo: %d", model.newMessageNoticeCount)
    }
}
```

- [ ] **步骤 2：编写底部跟随和展开不计数测试**

覆盖：

- 底部插入 Todo 后仍在底部且未读为 0；
- 用户展开旧 Todo entry 不增加计数；
- 最终回答触发折叠不增加计数；
- 页面打开期间更新仍只产生一条逻辑消息；
- 页面隐藏 notice 但不清空 count；关闭页面后 notice 恢复；
- session restore 初始加载不产生计数。

- [ ] **步骤 3：修正 Todo `addEntry` 接入点**

如果测试发现 `addEntry` 在底部也错误计数，保持其现有语义，只确保 Todo entry 走：

```go
m.addEntry(entry)
```

而不是先 append 后手动调用两次 `recordTranscriptEntryActivity`。Todo 更新不得调用 `recordAssistantActivity`。恢复路径不得调用 `addEntry`。

- [ ] **步骤 4：编写会话切换一致性测试**

构造：

1. Session A transcript 含 Todo 1/2。
2. 打开 Todo 页面。
3. 应用 Session B restore，B 无 Todo。
4. 断言页面关闭、current 为空、A 的 snapshot 不残留。
5. 再恢复 A，卡片和 current 都回到 1/2。

另测 B 的 cleared snapshot 与“从未创建”都显示相同页面空内容，但内部 `todoWasCleared` 值不同，便于历史摘要保持 `Todo cleared`。

- [ ] **步骤 5：运行滚动和恢复测试**

```bash
gofmt -w internal/ui/bubble/todo_state_test.go internal/ui/bubble/new_message_notice_test.go internal/ui/bubble/todo_restore_test.go internal/ui/bubble/todo_state.go internal/ui/bubble/new_message_notice.go
go test ./internal/ui/bubble -run 'Test.*Todo.*(Scroll|Notice|Session|Restore|Count|Bottom)' -count=1
go test ./internal/ui/bubble -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/ui/bubble/todo_state_test.go internal/ui/bubble/new_message_notice_test.go internal/ui/bubble/todo_restore_test.go internal/ui/bubble/todo_state.go internal/ui/bubble/new_message_notice.go
git commit -m "test: lock todo scroll and session behavior"
```

---

### 任务 12：端到端验证、静态检查和人工验收

**文件：**
- 可能修改：仅测试发现的 Todo 相关文件
- 验证规格：`docs/superpowers/specs/2026-08-02-native-todo-list-design.md`

- [ ] **步骤 1：运行 Todo 核心包和 race detector**

```bash
go test ./internal/todo/... -count=1
go test -race ./internal/todo/... -count=1
```

预期：PASS，无 race。

- [ ] **步骤 2：运行 Bubble、Session、Cmd 和 Subagent 测试**

```bash
go test ./internal/ui/bubble/... -count=1
go test ./internal/session/... -count=1
go test ./cmd/agent/... -count=1
go test ./internal/subagent/... -count=1
```

预期：PASS。

- [ ] **步骤 3：运行 Bubble race 测试**

```bash
go test -race ./internal/ui/bubble/... -run 'Test.*Todo|TestTodo.*' -count=1
```

预期：PASS，无 Broker/UI 事件竞态。

- [ ] **步骤 4：运行完整仓库检查**

```bash
go test ./... -count=1
go vet ./...
git diff --check
```

预期：全部 PASS，`git diff --check` 无输出。若完整测试存在进入本功能前已有失败，记录准确命令、包和错误，不通过回退无关工作区改动来规避。

- [ ] **步骤 5：检查计划范围没有引入 Phase 模型**

```bash
git diff --name-only
git diff | grep -E 'phase|depends_on|parent_id|todo_group|hierarchy' || true
```

预期：除规格或测试中的“禁止字段”断言外，不出现新的 Phase、依赖、分组或层级实现。

- [ ] **步骤 6：人工验证复杂任务流程**

启动交互 TUI，触发：

```json
{
  "explanation": "开始实现 Todo 页面",
  "items": [
    {"id": "inspect", "content": "检查现有 TUI 结构", "status": "completed"},
    {"id": "build", "content": "构建 Todo 独立页面", "status": "in_progress"},
    {"id": "restore", "content": "添加会话恢复测试", "status": "pending"}
  ]
}
```

确认：

- transcript 出现完整纵向 Todo 卡片；
- 普通工具轨迹只显示 `Todo: updated 1/3`，不显示完整 JSON；
- `Ctrl+T` 可在 Tool Inspect 查看完整 input/result；
- `Ctrl+P` 打开独立纵向页面；
- `Esc` 返回聊天且 draft 未丢失。

- [ ] **步骤 7：人工验证更新、旧快照和完成折叠**

依次触发第二个快照和全完成快照：

```json
{
  "explanation": "开始恢复测试",
  "items": [
    {"id": "inspect", "content": "检查现有 TUI 结构", "status": "completed"},
    {"id": "build", "content": "构建 Todo 独立页面", "status": "completed"},
    {"id": "restore", "content": "添加会话恢复测试", "status": "in_progress"}
  ]
}
```

```json
{
  "explanation": "实现与验证完成",
  "items": [
    {"id": "inspect", "content": "检查现有 TUI 结构", "status": "completed"},
    {"id": "build", "content": "构建 Todo 独立页面", "status": "completed"},
    {"id": "restore", "content": "添加会话恢复测试", "status": "completed"}
  ]
}
```

确认：

- 每次更新后只有最新卡片默认展开；
- 点击旧摘要可重新展开；
- 全完成快照在最终回答前保持完整；
- 最终回答成功结束后折叠为 `✓ Todo completed · 3/3`。

- [ ] **步骤 8：人工验证恢复和清除**

1. 退出并恢复同一 session，确认 Todo 卡片顺序、折叠状态和 `Ctrl+P` 当前清单一致。
2. 切换到无 Todo 的另一 session，确认没有跨会话残留。
3. 调用：

```json
{"items":[]}
```

4. 确认 transcript 出现 `Todo cleared`，`Ctrl+P` 显示空状态。
5. 再次恢复 session，确认 cleared 状态仍存在。

- [ ] **步骤 9：人工验证 modal 和滚动兼容性**

确认：

- Selection Dock 活跃时 `Ctrl+P` 不抢占选择；
- Todo page 活跃时普通字符、Enter、Tab 不进入 textarea；
- `Ctrl+T` 保持 Tool Inspect；
- 非底部收到 Todo 更新不跳到底部，并增加一条新消息；
- 点击 Todo 卡片展开不增加新消息；
- 20 列及更窄终端不 panic、不横向溢出。

- [ ] **步骤 10：最终差异和提交检查**

```bash
git status --short
git diff --stat
git diff --check
git log --oneline -12
```

确认提交粒度与任务对应，没有混入无关工作区修改。

若步骤 1–9 产生修正：

```bash
git add internal/todo internal/ui/bubble cmd/agent internal/subagent/manager_test.go
git commit -m "test: verify native todo workflow"
```

若没有额外修改，不创建空 commit。

- [ ] **步骤 11：记录交付摘要**

交付消息必须列出：

- `update_todo` 完整快照协议和三种状态；
- 主 Agent / headless / subagent 注册范围；
- transcript 最新展开、旧快照折叠和完成折叠行为；
- `Ctrl+P` 页面键位；
- JSONL 恢复策略；
- 运行过的测试命令和结果；
- 任何由原工作区已有修改造成、未由本功能处理的失败。
