# Responses 工具协议完整加固实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让 OpenAI Responses API 在连续工具调用、Session 恢复和上下文压缩后完整保留 reasoning output items，并阻止三类原生协议执行非法参数，同时原子拒绝包含不兼容 schema 的 MCP 工具刷新。

**架构：** 在通用 `message.Message` 上增加可选、不透明的 provider 数据，由 Responses transport 保存并重放完整 output items；模型流在完成事件中一次性交付 provider 数据和经验证的工具调用，Runner 将其随 assistant 消息持久化。工具参数解析统一采用“验证失败即协议错误”的策略；MCP 工具 schema 在替换快照前统一验证，失败时保留旧快照。

**技术栈：** Go、OpenAI Responses API、OpenAI-compatible Chat Completions、Anthropic Messages、JSONL Session journal、MCP JSON-RPC、Go `testing`/`httptest`

---

## 文件结构

### 创建

- `internal/message/types_test.go`：验证 provider 数据的 JSON 往返兼容性及旧消息兼容性。
- `internal/model/tool_arguments.go`：集中验证、深拷贝原生工具调用参数，返回带工具标识的协议错误。
- `internal/model/tool_arguments_test.go`：覆盖合法 object、空值、数组、截断 JSON 和嵌套 JSON 字符串。
- `internal/model/anthropic_stream_test.go`：覆盖 Anthropic 非法工具参数不会产生 `ToolCalls`。
- `internal/mcp/schema.go`：验证模型侧函数工具 schema 的 Responses 兼容结构。
- `internal/mcp/schema_test.go`：覆盖合法 schema 与拒绝原因。

### 修改

- `internal/message/types.go`：为 `Message` 增加可选 `ProviderData json.RawMessage`。
- `internal/model/stream.go`：为 `StreamEvent` 增加 provider 数据；Chat Completions 在发出工具调用前验证参数，不再把非法参数替换成 `{}`。
- `internal/model/anthropic_stream.go`：Anthropic 工具输入验证失败时发送协议错误，不执行工具。
- `internal/model/responses.go`：保存/重放完整 Responses output items，显式 `strict:false`，统一流式与非流式完成语义，并拒绝截断响应。
- `internal/model/responses_test.go`：覆盖 reasoning 往返、未知 item、strict、流中断、非法参数和旧 Session 回退。
- `internal/model/stream_test.go`：覆盖 Chat Completions 非法工具参数不会产生 `ToolCalls`。
- `internal/model/anthropic_stream_test.go`：覆盖 Anthropic 非法工具参数不会产生 `ToolCalls`。
- `internal/loop/runner.go`：从最终 `StreamEvent` 收集 provider 数据，附加到 assistant 消息，并在模型投影时保留原生工具结构和 provider 数据。
- `internal/loop/runner_test.go`：验证 provider 数据被保存在 history/journal，并在恢复后传回模型。
- `internal/loop/tool_result_maintenance.go`：深拷贝 `ProviderData` 和每个 `ToolUses[i].Input`。
- `internal/loop/tool_result_maintenance_test.go`：验证 maintenance 克隆不共享 provider 数据。
- `internal/loop/context_compaction_test.go`：验证保留尾部消息的 provider 数据被保留、摘要不继承旧 provider 数据。
- `internal/mcp/manager.go`：在 `replaceServerTools` 修改状态前验证整批 schema，失败时保持工具 map 和 snapshot version 不变。
- `internal/mcp/manager_test.go`：验证启动发现和刷新均原子拒绝不兼容 schema。
- `internal/mcp/types.go`：使 `ToolSpec.ModelSchema` 只负责默认值和深拷贝，验证由 `schema.go` 明确执行。

## 约定的接口

所有任务使用以下一致接口，不在后续任务中改名：

```go
// internal/message/types.go
type Message struct {
    Role         Role            `json:"role"`
    Content      string          `json:"content,omitempty"`
    Parts        []ContentPart   `json:"parts,omitempty"`
    ToolUse      *ToolCall       `json:"tool_use,omitempty"`
    ToolUses     []ToolCall      `json:"tool_uses,omitempty"`
    ToolResult   *ToolResult     `json:"tool_result,omitempty"`
    ToolResults  []ToolResult    `json:"tool_results,omitempty"`
    ProviderData json.RawMessage `json:"provider_data,omitempty"`
}
```

```go
// internal/model/stream.go
type StreamEvent struct {
    Delta        string
    Thinking     string
    ToolCalls    []message.ToolCall
    ProviderData json.RawMessage
    Done         bool
    Err          error
    Usage        *Usage
}
```

```go
// internal/model/tool_arguments.go
func decodeToolArguments(provider, callID, name string, raw []byte) (json.RawMessage, error)
```

```go
// internal/mcp/schema.go
func validateToolSpecs(specs []ToolSpec) error
func validateModelToolSchema(toolName string, schema json.RawMessage) error
```

Responses provider 信封固定为：

```go
const (
    responsesProviderTransport = "openai-responses"
    responsesProviderVersion   = 1
)

type responsesProviderData struct {
    Transport   string            `json:"transport"`
    Version     int               `json:"version"`
    OutputItems []json.RawMessage `json:"output_items"`
}
```

---

### 任务 1：扩展消息模型并保证不透明 provider 数据可安全持久化

**文件：**
- 修改：`internal/message/types.go:13-24`
- 创建：`internal/message/types_test.go`
- 修改：`internal/loop/tool_result_maintenance.go:339-362`
- 测试：`internal/loop/tool_result_maintenance_test.go`

- [ ] **步骤 1：编写 Message JSON 往返失败测试**

在 `internal/message/types_test.go` 添加：

```go
package message

import (
    "bytes"
    "encoding/json"
    "testing"
)

func TestMessageProviderDataJSONRoundTrip(t *testing.T) {
    original := Message{
        Role:    RoleAssistant,
        Content: "checking",
        ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}]}`),
    }
    data, err := json.Marshal(original)
    if err != nil {
        t.Fatalf("Marshal() error = %v", err)
    }
    var restored Message
    if err := json.Unmarshal(data, &restored); err != nil {
        t.Fatalf("Unmarshal() error = %v", err)
    }
    if !bytes.Equal(restored.ProviderData, original.ProviderData) {
        t.Fatalf("ProviderData = %s, want %s", restored.ProviderData, original.ProviderData)
    }
}

func TestMessageWithoutProviderDataRemainsCompatible(t *testing.T) {
    var restored Message
    if err := json.Unmarshal([]byte(`{"role":"assistant","content":"done"}`), &restored); err != nil {
        t.Fatalf("Unmarshal() error = %v", err)
    }
    if len(restored.ProviderData) != 0 {
        t.Fatalf("ProviderData = %s, want empty", restored.ProviderData)
    }
}
```

- [ ] **步骤 2：运行测试并确认字段尚不存在**

运行：

```bash
go test ./internal/message -run 'TestMessageProviderData' -v
```

预期：编译失败，错误包含 `unknown field ProviderData` 或 `restored.ProviderData undefined`。

- [ ] **步骤 3：为 Message 增加 provider 数据字段**

在 `internal/message/types.go` 的 `Message` 中加入：

```go
ProviderData json.RawMessage `json:"provider_data,omitempty"`
```

该字段只承载 provider 不透明 JSON；`message` 包不解析 transport、version 或 output item。

- [ ] **步骤 4：运行消息测试确认通过**

运行：

```bash
go test ./internal/message -v
```

预期：PASS。

- [ ] **步骤 5：编写 cloneMessage 深拷贝失败测试**

在 `internal/loop/tool_result_maintenance_test.go` 添加：

```go
func TestCloneMessageDeepCopiesProviderDataAndToolInputs(t *testing.T) {
    original := message.Message{
        Role:         message.RoleAssistant,
        ProviderData: json.RawMessage(`{"transport":"openai-responses"}`),
        ToolUses: []message.ToolCall{{
            ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`),
        }},
    }
    cloned := cloneMessage(original)
    cloned.ProviderData[2] = 'X'
    cloned.ToolUses[0].Input[2] = 'X'

    if string(original.ProviderData) != `{"transport":"openai-responses"}` {
        t.Fatalf("original ProviderData mutated: %s", original.ProviderData)
    }
    if string(original.ToolUses[0].Input) != `{"file_path":"a.go"}` {
        t.Fatalf("original ToolUses input mutated: %s", original.ToolUses[0].Input)
    }
}
```

- [ ] **步骤 6：运行 clone 测试验证失败**

运行：

```bash
go test ./internal/loop -run TestCloneMessageDeepCopiesProviderDataAndToolInputs -v
```

预期：FAIL，原消息的 `ProviderData` 或 `ToolUses[0].Input` 被克隆值修改。

- [ ] **步骤 7：实现完整深拷贝**

将 `cloneMessage` 的核心复制逻辑调整为：

```go
func cloneMessage(msg message.Message) message.Message {
    copyMessage := msg
    copyMessage.ProviderData = append(json.RawMessage(nil), msg.ProviderData...)
    copyMessage.Parts = append([]message.ContentPart(nil), msg.Parts...)
    copyMessage.ToolUses = make([]message.ToolCall, len(msg.ToolUses))
    for i, call := range msg.ToolUses {
        copyMessage.ToolUses[i] = call
        copyMessage.ToolUses[i].Input = append(json.RawMessage(nil), call.Input...)
    }
    copyMessage.ToolResults = append([]message.ToolResult(nil), msg.ToolResults...)
    if msg.ToolUse != nil {
        call := *msg.ToolUse
        call.Input = append(json.RawMessage(nil), msg.ToolUse.Input...)
        copyMessage.ToolUse = &call
    }
    if msg.ToolResult != nil {
        result := *msg.ToolResult
        copyMessage.ToolResult = &result
    }
    return copyMessage
}
```

- [ ] **步骤 8：运行相关测试确认通过**

运行：

```bash
go test ./internal/message ./internal/loop -run 'TestMessageProviderData|TestMessageWithoutProviderData|TestCloneMessageDeepCopies' -v
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/message/types.go internal/message/types_test.go internal/loop/tool_result_maintenance.go internal/loop/tool_result_maintenance_test.go
git commit -m "feat: persist provider response metadata"
```

---

### 任务 2：集中拒绝三类原生协议的非法工具参数

**文件：**
- 创建：`internal/model/tool_arguments.go`
- 创建：`internal/model/tool_arguments_test.go`
- 修改：`internal/model/stream.go:339-464`
- 修改：`internal/model/anthropic_stream.go:192-296`
- 修改：`internal/model/responses.go:347-356`
- 测试：`internal/model/stream_test.go`
- 测试：`internal/model/anthropic_stream_test.go`
- 测试：`internal/model/responses_test.go`

- [ ] **步骤 1：编写统一参数解码失败测试**

创建 `internal/model/tool_arguments_test.go`：

```go
package model

import "testing"

func TestDecodeToolArgumentsRequiresJSONObject(t *testing.T) {
    tests := []struct {
        name string
        raw  string
        ok   bool
    }{
        {name: "object", raw: `{"file_path":"README.md"}`, ok: true},
        {name: "empty object", raw: `{}`, ok: true},
        {name: "empty", raw: ``, ok: false},
        {name: "null", raw: `null`, ok: false},
        {name: "array", raw: `[]`, ok: false},
        {name: "truncated", raw: `{"file_path":`, ok: false},
        {name: "nested string", raw: `"{\"file_path\":\"README.md\"}"`, ok: false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := decodeToolArguments("responses", "call_1", "Read", []byte(tt.raw))
            if tt.ok && err != nil {
                t.Fatalf("decodeToolArguments() error = %v", err)
            }
            if !tt.ok && err == nil {
                t.Fatalf("decodeToolArguments() = %s, want error", got)
            }
        })
    }
}
```

- [ ] **步骤 2：运行 helper 测试验证失败**

运行：

```bash
go test ./internal/model -run TestDecodeToolArgumentsRequiresJSONObject -v
```

预期：编译失败，`decodeToolArguments` 未定义。

- [ ] **步骤 3：实现统一参数解码器**

创建 `internal/model/tool_arguments.go`：

```go
package model

import (
    "bytes"
    "encoding/json"
    "fmt"
    "strings"
)

func decodeToolArguments(provider, callID, name string, raw []byte) (json.RawMessage, error) {
    trimmed := bytes.TrimSpace(raw)
    label := strings.TrimSpace(name)
    if label == "" {
        label = strings.TrimSpace(callID)
    }
    if len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
        return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
    }
    var object map[string]json.RawMessage
    if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
        return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
    }
    return append(json.RawMessage(nil), trimmed...), nil
}
```

- [ ] **步骤 4：运行 helper 测试确认通过**

运行：

```bash
go test ./internal/model -run TestDecodeToolArgumentsRequiresJSONObject -v
```

预期：PASS。

- [ ] **步骤 5：为 Chat Completions 编写非法参数流测试**

在 `internal/model/stream_test.go` 添加一个 `httptest.Server`，发送：

```text
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_bad","type":"function","function":{"name":"Read","arguments":"{\"file_path\":"}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}
```

测试遍历事件并断言：

```go
var sawCalls bool
var gotErr error
for event := range events {
    sawCalls = sawCalls || len(event.ToolCalls) != 0
    if event.Err != nil {
        gotErr = event.Err
    }
}
if sawCalls {
    t.Fatal("invalid Chat Completions arguments emitted ToolCalls")
}
if gotErr == nil || !strings.Contains(gotErr.Error(), "invalid JSON object arguments") {
    t.Fatalf("error = %v, want invalid arguments error", gotErr)
}
```

- [ ] **步骤 6：修改 Chat Completions 累积器返回错误**

将签名改为：

```go
func openAIToolCallsFromAccumulated(accumulated map[int]*activeOpenAIToolCall) ([]message.ToolCall, error)
```

每个 call 使用：

```go
input, err := decodeToolArguments("Chat Completions", call.id, call.name, []byte(call.args.String()))
if err != nil {
    return nil, err
}
```

`consumeStream` 在 `finish_reason` 时先验证；失败则发送 `StreamEvent{Err: err}` 并返回，不发送 `ToolCalls` 或 `Done`。若 EOF 时存在未完成的 accumulated tool call，也执行同一验证，非法时发送错误。

- [ ] **步骤 7：为 Anthropic 编写非法 partial_json 测试并修改实现**

在 `internal/model/anthropic_stream_test.go` 模拟：

```text
data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_bad","name":"Read","input":{}}}
data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}
data: {"type":"content_block_stop"}
```

断言没有 `ToolCalls` 且收到 `Err`。将 `activeAnthropicToolCall.input()` 改为：

```go
func (t *activeAnthropicToolCall) input() (json.RawMessage, error)
```

选取 delta 或 initial input 后调用：

```go
return decodeToolArguments("Anthropic", t.id, t.name, raw)
```

在 `content_block_stop` 处理错误时发送 `StreamEvent{Err: err}` 并终止。

- [ ] **步骤 8：为 Responses 非法 arguments 添加测试并修改转换函数**

在 `internal/model/responses_test.go` 添加非流式响应：

```json
{
  "status": "completed",
  "output": [
    {"type":"function_call","id":"fc_bad","call_id":"call_bad","name":"Read","arguments":"{\"file_path\":"}
  ]
}
```

断言 `StreamMessage` 返回错误或事件流只产生 `Err`，且不产生 `ToolCalls`。将转换函数改为：

```go
func responseToolCall(item responsesOutputItem) (message.ToolCall, error) {
    id := item.CallID
    if id == "" {
        id = item.ID
    }
    input, err := decodeToolArguments("Responses API", id, item.Name, []byte(item.Arguments))
    if err != nil {
        return message.ToolCall{}, err
    }
    return message.ToolCall{ID: id, Name: item.Name, Input: input}, nil
}
```

所有调用点必须在任一错误时放弃整批工具调用。

- [ ] **步骤 9：运行三协议参数测试**

运行：

```bash
go test ./internal/model -run 'TestDecodeToolArguments|Invalid.*Arguments|Truncated.*Arguments' -v
```

预期：PASS；所有非法参数场景均没有 `ToolCalls`。

- [ ] **步骤 10：Commit**

```bash
git add internal/model/tool_arguments.go internal/model/tool_arguments_test.go internal/model/stream.go internal/model/stream_test.go internal/model/anthropic_stream.go internal/model/anthropic_stream_test.go internal/model/responses.go internal/model/responses_test.go
git commit -m "fix: reject invalid native tool arguments"
```

---

### 任务 3：定义 Responses provider 信封并支持权威原始 item 重放

**文件：**
- 修改：`internal/model/responses.go:19-192`
- 测试：`internal/model/responses_test.go`

- [ ] **步骤 1：编写 provider 信封编码/解码测试**

在 `internal/model/responses_test.go` 添加：

```go
func TestResponsesProviderDataPreservesUnknownOutputItems(t *testing.T) {
    items := []json.RawMessage{
        json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}`),
        json.RawMessage(`{"type":"future_item","id":"future_1","payload":{"x":1}}`),
    }
    encoded, err := encodeResponsesProviderData(items)
    if err != nil {
        t.Fatalf("encodeResponsesProviderData() error = %v", err)
    }
    decoded, ok := decodeResponsesProviderData(encoded)
    if !ok {
        t.Fatal("decodeResponsesProviderData() rejected valid envelope")
    }
    if len(decoded) != 2 || !bytes.Contains(decoded[1], []byte(`"future_item"`)) {
        t.Fatalf("decoded items = %s", decoded)
    }
}

func TestDecodeResponsesProviderDataRejectsWrongTransportAndVersion(t *testing.T) {
    tests := []json.RawMessage{
        json.RawMessage(`{"transport":"openai-compatible","version":1,"output_items":[]}`),
        json.RawMessage(`{"transport":"openai-responses","version":2,"output_items":[]}`),
        json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[[]]}`),
        json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{}]}`),
    }
    for _, raw := range tests {
        if _, ok := decodeResponsesProviderData(raw); ok {
            t.Fatalf("accepted invalid provider data: %s", raw)
        }
    }
}
```

- [ ] **步骤 2：运行测试验证 helper 未定义**

运行：

```bash
go test ./internal/model -run 'TestResponsesProviderData|TestDecodeResponsesProviderData' -v
```

预期：编译失败，编码/解码 helper 未定义。

- [ ] **步骤 3：实现信封和 raw item 验证**

在 `internal/model/responses.go` 添加约定接口中的常量、`responsesProviderData`，以及：

```go
func encodeResponsesProviderData(items []json.RawMessage) (json.RawMessage, error)
func decodeResponsesProviderData(raw json.RawMessage) ([]json.RawMessage, bool)
func validateResponsesOutputItems(items []json.RawMessage) error
```

验证规则必须精确为：

1. 信封 transport 必须为 `openai-responses`；
2. version 必须为 `1`；
3. 每个 item 必须是有效 JSON object；
4. 每个 item 的 `type` 必须是非空字符串；
5. 返回值必须深拷贝所有 raw bytes；
6. 未知 item type 不拒绝。

- [ ] **步骤 4：编写 buildResponsesInput 权威重放测试**

构造历史：

```go
assistant := message.Message{
    Role:    message.RoleAssistant,
    Content: "visible fallback",
    ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)},
    ProviderData: json.RawMessage(`{
      "transport":"openai-responses",
      "version":1,
      "output_items":[
        {"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},
        {"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}
      ]
    }`),
}
result := message.Message{
    Role: message.RoleUser,
    ToolResult: &message.ToolResult{ToolUseID: "call_1", Content: "contents"},
}
```

断言构建后的 input 顺序严格是：

```text
reasoning
function_call
function_call_output
```

并断言不存在由 `Content` 生成的重复 assistant message，也不存在由 `ToolUse` 生成的重复 function call。

- [ ] **步骤 5：将 Responses request input 改为 raw item 投影**

把：

```go
type responsesRequest struct {
    Input []responsesItem `json:"input"`
}
```

改成：

```go
type responsesRequest struct {
    Model  string            `json:"model"`
    Input  []json.RawMessage `json:"input"`
    Stream bool              `json:"stream"`
    Tools  []responsesTool   `json:"tools,omitempty"`
}
```

将 `buildResponsesInput` 改为 `([]json.RawMessage, error)`。普通 message、fallback function call 和 function output 使用 `json.Marshal(responsesItem{...})` 生成 raw item；assistant 消息若存在有效 ProviderData，则直接追加解码后的原始 items 并跳过该消息的通用投影。损坏、未知版本或错误 transport 的 ProviderData 必须回退到通用投影。

- [ ] **步骤 6：增加旧 Session 与损坏信封回退测试**

测试无 `ProviderData` 和无效 `ProviderData` 的 assistant 消息仍生成：

```text
assistant message
function_call
```

并确保不会因旧 Session 无法发请求。

- [ ] **步骤 7：运行 Responses 投影测试**

运行：

```bash
go test ./internal/model -run 'TestResponsesProviderData|TestDecodeResponsesProviderData|TestBuildResponsesInput' -v
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/model/responses.go internal/model/responses_test.go
git commit -m "feat: replay responses output items"
```

---

### 任务 4：统一流式与非流式 Responses 完成语义并显式关闭 strict

**文件：**
- 修改：`internal/model/stream.go:21-28`
- 修改：`internal/model/responses.go:43-466`
- 测试：`internal/model/responses_test.go`

- [ ] **步骤 1：编写 `strict:false` 请求测试**

扩展现有 Responses 请求捕获测试，对每个工具断言：

```go
var tool map[string]any
if err := json.Unmarshal(body.Tools[0], &tool); err != nil {
    t.Fatal(err)
}
strict, ok := tool["strict"]
if !ok || strict != false {
    t.Fatalf("tool strict = %#v, want explicit false", strict)
}
```

- [ ] **步骤 2：运行 strict 测试验证失败**

运行：

```bash
go test ./internal/model -run TestResponsesRequest -v
```

预期：FAIL，`strict` 字段缺失。

- [ ] **步骤 3：显式序列化 strict false**

修改工具定义：

```go
type responsesTool struct {
    Type        string          `json:"type"`
    Name        string          `json:"name"`
    Description string          `json:"description,omitempty"`
    Parameters  json.RawMessage `json:"parameters"`
    Strict      bool            `json:"strict"`
}
```

构造时写入 `Strict: false`，不能使用 `omitempty`。

- [ ] **步骤 4：编写流式 reasoning + function_call 完成测试**

测试 SSE 至少包含：

```text
response.output_item.done reasoning(output_index=0)
response.output_item.done function_call(output_index=1)
response.completed response.output=[reasoning,function_call] usage=...
```

最终断言最后一个事件同时满足：

```go
if !last.Done || len(last.ToolCalls) != 1 || len(last.ProviderData) == 0 || last.Usage == nil {
    t.Fatalf("final event = %#v", last)
}
```

解码 `ProviderData` 后断言 reasoning 在 function call 前，且 `encrypted_content` 保留。

- [ ] **步骤 5：编写截断 Responses SSE 测试**

SSE 只发送 `response.output_item.added` 和部分 `response.function_call_arguments.delta`，随后 EOF，不发送 `response.completed`。断言：

```go
if sawDone || sawToolCalls {
    t.Fatal("truncated Responses stream completed or emitted tools")
}
if gotErr == nil || !strings.Contains(gotErr.Error(), "before response.completed") {
    t.Fatalf("error = %v", gotErr)
}
```

- [ ] **步骤 6：重构 Responses output 为 raw 完成快照**

将 API response 增加：

```go
type responsesAPIResponse struct {
    Status string            `json:"status,omitempty"`
    Output []json.RawMessage `json:"output"`
    Usage  *Usage            `json:"usage,omitempty"`
    Error  *responsesError   `json:"error,omitempty"`
}
```

流式事件中的 `Item` 和 `Response.Output` 同样保留为 `json.RawMessage`。为已知字段单独解码轻量 view：

```go
type responsesOutputItemView struct {
    Type      string `json:"type"`
    ID        string `json:"id,omitempty"`
    CallID    string `json:"call_id,omitempty"`
    Name      string `json:"name,omitempty"`
    Arguments string `json:"arguments,omitempty"`
    Content   []struct {
        Type string `json:"type"`
        Text string `json:"text,omitempty"`
    } `json:"content,omitempty"`
}
```

- [ ] **步骤 7：实现单一完成事件构造器**

新增：

```go
func completedResponsesEvent(output []json.RawMessage, usage *Usage) (StreamEvent, error)
```

该 helper 必须：

1. 验证 raw output items；
2. 提取所有 message item 文本；
3. 原子验证所有 function_call arguments；
4. 任一调用非法时返回 error，不返回部分 calls；
5. 编码 ProviderData；
6. 返回 `StreamEvent{Delta: text, ToolCalls: calls, ProviderData: data, Usage: usage, Done: true}`。

`nonStreamingResponsesMessage` 要求 `Status` 为空或 `completed`；`incomplete`/`failed` 返回错误。流式 `consumeResponsesStream` 只有收到 `response.completed` 才调用该 helper；优先采用 `event.Response.Output`，若兼容网关未携带 output，才使用按 `output_index` 收集的 `response.output_item.done` raw items。EOF 前未 completed 必须发送 `Err`，不得 flush 工具调用。

- [ ] **步骤 8：扩展 StreamEvent**

在 `internal/model/stream.go` 增加：

```go
ProviderData json.RawMessage
```

所有 provider 数据在事件发送前深拷贝。Chat Completions 和 Anthropic 不设置该字段。

- [ ] **步骤 9：运行完整 Responses 测试**

运行：

```bash
go test ./internal/model -run 'Responses' -v
```

预期：PASS；流式和非流式最终事件语义一致。

- [ ] **步骤 10：Commit**

```bash
git add internal/model/stream.go internal/model/responses.go internal/model/responses_test.go
git commit -m "fix: complete responses streams atomically"
```

---

### 任务 5：Runner 持久化 provider 数据并在恢复后重放

**文件：**
- 修改：`internal/loop/runner.go:132-142, 1534-1600`
- 测试：`internal/loop/runner_test.go`

- [ ] **步骤 1：编写 Runner provider 数据持久化失败测试**

在 `internal/loop/runner_test.go` 添加一个 fake `ModelStreamer`，第一次返回：

```go
model.StreamEvent{
    ToolCalls: []message.ToolCall{{
        ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`),
    }},
    ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"reasoning","id":"rs_1"},{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}]}`),
    Done: true,
}
```

fake registry 注册一个返回 `contents` 的工具；第二次模型调用返回普通完成文本。断言：

1. 第二次传给模型的历史 assistant 消息带有相同 ProviderData；
2. Session `LoadResolvedHistory` 中对应 assistant 消息带有 ProviderData；
3. provider 数据字节与 fake 事件不共享底层 slice。

- [ ] **步骤 2：运行 Runner 测试验证失败**

运行：

```bash
go test ./internal/loop -run TestRunnerPersistsProviderDataAcrossToolRoundTrip -v
```

预期：FAIL，provider 数据未出现在 assistant 历史中。

- [ ] **步骤 3：让 turnState 收集 provider 数据**

在 `turnState` 增加：

```go
providerData json.RawMessage
```

处理每个 `StreamEvent` 时：

```go
if len(event.ProviderData) != 0 {
    state.providerData = append(json.RawMessage(nil), event.ProviderData...)
}
```

若同一响应收到多个非空 ProviderData，后一个只能覆盖前一个完整快照，不做字节拼接。

- [ ] **步骤 4：让最终 assistant 消息携带 content、tools 和 provider 数据**

把 helper 改为：

```go
func buildAssistantToolCallMessage(content string, calls []message.ToolCall, providerData json.RawMessage) message.Message
```

实现：

```go
msg := message.Message{
    Role:         message.RoleAssistant,
    Content:      content,
    ProviderData: append(json.RawMessage(nil), providerData...),
}
```

然后设置 `ToolUse`/`ToolUses`。`finalizeAssistantMessage` 的 tool-call 分支调用：

```go
return buildAssistantToolCallMessage(state.content.String(), state.toolCalls, state.providerData), nil
```

普通 assistant 分支在 `parseAssistantMessage` 后也设置：

```go
msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
```

更新所有旧 helper 调用，文本 JSON tool-envelope 路径传 `nil` provider data。

- [ ] **步骤 5：让 renderMessageForModel 保留原生协议字段**

当前 `renderMessageForModel` 会把 tool calls/results 改写成纯文本并丢弃 `ToolUse`、`ToolUses`、`ToolResult`、`ToolResults` 和 ProviderData，这会让 Responses 无法读取恢复后的原始 items。保留现有兼容文本，同时深拷贝原生字段：

```go
func renderMessageForModel(msg message.Message) message.Message {
    rendered := cloneMessage(msg)
    switch {
    case len(toolCallsFromMessage(msg)) > 0:
        calls := toolCallsFromMessage(msg)
        parts := make([]string, 0, len(calls))
        for _, call := range calls {
            parts = append(parts, marshalJSON(toolUseEnvelope{
                Type: toolUseResponseType, ID: call.ID, Name: call.Name, Input: call.Input,
            }))
        }
        if strings.TrimSpace(rendered.Content) == "" {
            rendered.Content = strings.Join(parts, "\n")
        }
        return rendered
    case len(toolResultsFromMessage(msg)) > 0:
        results := toolResultsFromMessage(msg)
        parts := make([]string, 0, len(results))
        for _, result := range results {
            parts = append(parts, marshalJSON(result))
        }
        label := "TOOL_RESULT:\n"
        if len(results) > 1 {
            label = "TOOL_RESULTS:\n"
        }
        rendered.Role = message.RoleUser
        rendered.Content = label + strings.Join(parts, "\n")
        return rendered
    default:
        return rendered
    }
}
```

Responses 使用 ProviderData/原生字段作为权威投影；Chat Completions 和 Anthropic 继续由各自 request builder 解释通用字段。不得把 `provider_data` 本身序列化进 provider 请求 JSON。

- [ ] **步骤 6：修复 buildModelMessages 的空 assistant 过滤条件**

当前空 content assistant 会被跳过。修改为仅当以下三者均为空时跳过：

```go
rendered.Content == "" &&
len(toolCallsFromMessage(rendered)) == 0 &&
len(rendered.ProviderData) == 0
```

这样纯 reasoning/provider item 消息不会在恢复后被丢弃。

- [ ] **步骤 7：运行 Runner 定向测试**

运行：

```bash
go test ./internal/loop -run 'TestRunnerPersistsProviderData|TestLoadSession' -v
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/loop/runner.go internal/loop/runner_test.go
git commit -m "feat: persist responses metadata in runner history"
```

---

### 任务 6：确保 context maintenance 和 compaction 正确处理 provider 数据

**文件：**
- 修改：`internal/loop/tool_result_maintenance.go`
- 测试：`internal/loop/tool_result_maintenance_test.go`
- 测试：`internal/loop/context_compaction_test.go`

- [ ] **步骤 1：编写 maintenance 保留 provider 数据测试**

在 `internal/loop/tool_result_maintenance_test.go` 构造：

```go
assistant := message.Message{
    Role: message.RoleAssistant,
    ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"large.txt"}`)},
    ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"large.txt\"}"}]}`),
}
result := message.Message{
    Role: message.RoleUser,
    ToolResult: &message.ToolResult{ToolUseID: "call_1", Content: strings.Repeat("x", 20000)},
}
```

执行 snip maintenance 后断言 assistant 的 ProviderData 字节不变，tool result 被缩短，且修改输出 ProviderData 不影响输入。

- [ ] **步骤 2：运行 maintenance 测试并确认通过或暴露遗漏**

运行：

```bash
go test ./internal/loop -run TestMaintainToolResultsPreservesProviderData -v
```

预期：在任务 1 的深拷贝实现后 PASS；若失败，只修复深拷贝路径，不修改 provider 内容。

- [ ] **步骤 3：编写 compaction 边界测试**

在 `internal/loop/context_compaction_test.go` 添加两个断言：

1. 被压缩掉的旧 assistant provider 数据不出现在 compacted history；
2. 压缩边界后保留的 assistant 消息仍带有原 ProviderData；
3. 新生成的 summary assistant/user 消息 `ProviderData` 为空。

测试使用两个不同 marker：`rs_old` 与 `rs_tail`，最终只允许 `rs_tail` 出现在 compacted history 的 ProviderData 中。

- [ ] **步骤 4：运行 compaction 测试**

运行：

```bash
go test ./internal/loop -run 'TestCompactContext.*ProviderData' -v
```

预期：PASS；若摘要构造复制了旧消息结构，则显式构造 `message.Message{Role: ..., Content: ...}`，不得复制 ProviderData。

- [ ] **步骤 5：运行 loop 全包测试**

运行：

```bash
go test ./internal/loop -v
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/loop/tool_result_maintenance.go internal/loop/tool_result_maintenance_test.go internal/loop/context_compaction_test.go
git commit -m "test: preserve responses metadata through context maintenance"
```

---

### 任务 7：原子拒绝不兼容 MCP 工具 schema

**文件：**
- 创建：`internal/mcp/schema.go`
- 创建：`internal/mcp/schema_test.go`
- 修改：`internal/mcp/manager.go:425-443, 497-526`
- 修改：`internal/mcp/types.go:57-66`
- 测试：`internal/mcp/manager_test.go`

- [ ] **步骤 1：编写 schema 验证表驱动测试**

创建 `internal/mcp/schema_test.go`：

```go
package mcp

import (
    "encoding/json"
    "strings"
    "testing"
)

func TestValidateModelToolSchema(t *testing.T) {
    tests := []struct {
        name   string
        schema string
        ok     bool
        want   string
    }{
        {name: "object", schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`, ok: true},
        {name: "empty defaults", schema: ``, ok: true},
        {name: "invalid JSON", schema: `{`, want: "valid JSON"},
        {name: "top level array", schema: `[]`, want: "JSON object"},
        {name: "wrong type", schema: `{"type":"array"}`, want: `type must be "object"`},
        {name: "properties array", schema: `{"type":"object","properties":[]}`, want: "properties must be an object"},
        {name: "required string", schema: `{"type":"object","properties":{},"required":"query"}`, want: "required must be an array"},
        {name: "missing required property", schema: `{"type":"object","properties":{},"required":["query"]}`, want: `required property "query"`},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateModelToolSchema("server__tool", json.RawMessage(tt.schema))
            if tt.ok && err != nil {
                t.Fatalf("validateModelToolSchema() error = %v", err)
            }
            if !tt.ok && (err == nil || !strings.Contains(err.Error(), tt.want)) {
                t.Fatalf("error = %v, want substring %q", err, tt.want)
            }
        })
    }
}
```

- [ ] **步骤 2：运行 schema 测试验证失败**

运行：

```bash
go test ./internal/mcp -run TestValidateModelToolSchema -v
```

预期：编译失败，validator 未定义。

- [ ] **步骤 3：实现 Responses 兼容的结构验证器**

创建 `internal/mcp/schema.go`。精确规则：

1. 空 schema 使用 `{"type":"object","properties":{}}`；
2. 必须是有效 JSON；
3. 顶层必须是 object；
4. 若存在 `type`，必须是字符串 `object`；缺少 `type` 可接受；
5. 若存在 `properties`，必须是 JSON object；缺少时按空 properties 处理；
6. 若存在 `required`，必须是字符串数组；
7. `required` 中每个名称必须存在于 properties；
8. 不因未知 JSON Schema keyword 拒绝，因为 Responses 工具显式 `strict:false`；
9. 错误必须包含工具名和精确原因。

实现批量验证：

```go
func validateToolSpecs(specs []ToolSpec) error {
    for _, spec := range specs {
        if err := validateModelToolSchema(spec.Name, spec.ModelSchema()); err != nil {
            return err
        }
    }
    return nil
}
```

- [ ] **步骤 4：运行 validator 测试确认通过**

运行：

```bash
go test ./internal/mcp -run TestValidateModelToolSchema -v
```

预期：PASS。

- [ ] **步骤 5：编写 replaceServerTools 原子失败测试**

在 `internal/mcp/manager_test.go` 直接构造带旧工具的 Manager，记录：

```go
before := manager.Snapshot()
```

调用：

```go
err := manager.replaceServerTools("codegraph", []ToolSpec{{
    Name: "codegraph__bad", Server: "codegraph", MCPName: "bad", Kind: KindTool,
    InputSchema: json.RawMessage(`{"type":"array"}`),
}})
```

断言：

```go
if err == nil {
    t.Fatal("replaceServerTools accepted incompatible schema")
}
after := manager.Snapshot()
if !reflect.DeepEqual(after, before) {
    t.Fatalf("snapshot changed on rejected refresh: before=%#v after=%#v", before, after)
}
```

必须覆盖 snapshot version 和旧工具列表均不变。

- [ ] **步骤 6：在状态修改前验证整批工具**

在 `replaceServerTools` 获取锁或构造 `next` 之前执行：

```go
if err := validateToolSpecs(tools); err != nil {
    return fmt.Errorf("validate MCP tools for server %q: %w", serverName, err)
}
```

只有整批通过后才能替换 `m.tools`、增加 `snapshot.Version` 和发布 snapshot。`refreshServer` 现有错误路径继续调用 `setServerError`，但不得清空旧工具。

- [ ] **步骤 7：增加启动发现不兼容 schema 测试**

扩展 `TestMCPManagerHelper`，通过环境变量让 `tools/list` 返回一个 `inputSchema: {"type":"array"}`。新增启动测试断言 `Start` 返回包含工具名和 `type must be "object"` 的错误；不能创建一个部分可用的 Manager。

- [ ] **步骤 8：运行 MCP 测试**

运行：

```bash
go test ./internal/mcp -run 'TestValidateModelToolSchema|TestReplaceServerTools|IncompatibleSchema' -v
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/mcp/schema.go internal/mcp/schema_test.go internal/mcp/types.go internal/mcp/manager.go internal/mcp/manager_test.go
git commit -m "fix: reject incompatible mcp tool schemas atomically"
```

---

### 任务 8：端到端回归 Responses reasoning 工具链和协议切换

**文件：**
- 测试：`internal/model/responses_test.go`
- 测试：`internal/loop/runner_test.go`
- 测试：`internal/session/jsonl_store_test.go`

- [ ] **步骤 1：编写两轮 Responses reasoning 工具调用端到端测试**

在 `internal/model/responses_test.go` 使用记录请求体的 `httptest.Server`：

第一响应：

```json
{
  "status":"completed",
  "output":[
    {"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},
    {"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}
  ]
}
```

第二次请求必须包含顺序：

```text
system/user message items
reasoning rs_1
function_call call_1
function_call_output call_1
```

断言 reasoning item 的 `encrypted_content` 字节未变化，并断言没有重复 assistant message/function call。

- [ ] **步骤 2：编写 Session JSONL 恢复测试**

在 `internal/session/jsonl_store_test.go` 写入带 ProviderData 的 assistant journal record，再调用 `LoadResolvedHistory`，断言恢复的 provider 数据完整。随后修改恢复值中的字节，重新加载并断言磁盘数据未因 slice 共享而改变。

- [ ] **步骤 3：编写 transport 切换隔离测试**

在 model 测试中构造带 Responses ProviderData 的通用历史：

- Chat Completions 请求只使用 `Content`/`ToolUses`，请求 JSON 不出现 `provider_data`、`reasoning`、`encrypted_content`；
- Anthropic 请求只使用通用字段，同样不出现 Responses 原始 item；
- 切回 Responses 时重新使用 ProviderData 权威投影。

- [ ] **步骤 4：运行端到端定向测试**

运行：

```bash
go test ./internal/model ./internal/loop ./internal/session -run 'ResponsesReasoning|ProviderData|TransportSwitch' -v
```

预期：PASS。

- [ ] **步骤 5：运行工具和 MCP 回归测试**

运行：

```bash
go test ./internal/tool/... ./internal/mcp ./internal/subagent ./internal/todo -v
```

预期：PASS。

- [ ] **步骤 6：运行全量测试和静态检查**

运行：

```bash
gofmt -w internal/message/types.go internal/message/types_test.go internal/model/tool_arguments.go internal/model/tool_arguments_test.go internal/model/stream.go internal/model/stream_test.go internal/model/anthropic_stream.go internal/model/anthropic_stream_test.go internal/model/responses.go internal/model/responses_test.go internal/loop/runner.go internal/loop/runner_test.go internal/loop/tool_result_maintenance.go internal/loop/tool_result_maintenance_test.go internal/loop/context_compaction_test.go internal/mcp/schema.go internal/mcp/schema_test.go internal/mcp/types.go internal/mcp/manager.go internal/mcp/manager_test.go internal/session/jsonl_store_test.go
go test ./...
git diff --check
```

预期：

```text
所有 Go package PASS
git diff --check 无输出且退出码为 0
```

- [ ] **步骤 7：检查最终 diff 中的安全不变量**

运行：

```bash
git diff -- internal/message internal/model internal/loop internal/mcp internal/session
```

人工确认：

1. Responses 只在 `response.completed` 后发出工具调用；
2. reasoning/未知 output items 原样保存；
3. 有效 ProviderData 下不重复通用 assistant/function_call 投影；
4. 三类原生协议都不会把非法参数替换成 `{}`；
5. MCP schema 失败发生在 snapshot mutation 前；
6. Chat/Anthropic 请求不序列化 ProviderData；
7. compaction summary 不继承 ProviderData。

- [ ] **步骤 8：Commit**

```bash
git add internal/message internal/model internal/loop internal/mcp internal/session
git commit -m "test: cover responses tool protocol hardening"
```
