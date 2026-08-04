# GPT/DeepSeek 模型适配器实现计划

> 设计规格：`docs/superpowers/specs/2026-08-04-model-adapters-deepseek-strict-design.md`
> 本计划只描述实现步骤，不在计划阶段修改业务代码。

## 目标

在现有 `internal/model` 请求链路中引入小型 Chat Completions 适配器层：

- `DeepSeekAdapter`：为工具 function 写入 `strict: true`，并规范化 strict JSON Schema；
- `GPTAdapter`：独立适配器边界，暂时复用 OpenAI-compatible 请求行为；
- `OpenAICompatibleAdapter`：保留默认行为；
- 继续复用现有 HTTP、重试、SSE、usage 和 `StreamEvent` 处理；
- 不允许 `ExtraBody` / `ModelExtraBody` 覆盖适配器生成的工具字段。

## 实现步骤

### 1. 扩展 Chat Completions 工具类型

**文件：** `internal/model/types.go`

- 为 `openAIToolFunction` 增加可选字段：

  ```go
  Strict bool `json:"strict,omitempty"`
  ```

- 保持非 DeepSeek 请求的 JSON 兼容性：`omitempty` 确保普通工具定义不出现 `strict`。
- 保持 `ToolDefinition` 作为 provider 无关的输入类型，不增加 DeepSeek 专用字段。

### 2. 增加适配器接口和共享请求构造函数

**新增文件：** `internal/model/adapter.go`

定义：

```go
type ModelAdapter interface {
    Name() string
    BuildChatCompletionsRequest(
        cfg Config,
        messages []message.Message,
        tools []ToolDefinition,
        stream bool,
    ) (ChatCompletionsRequest, error)
}
```

在同一层提供共享构造逻辑，或将其放入现有 `stream.go` 的非方法函数中：

- `buildOpenAICompatibleChatCompletionsRequest`；
- 复用 `buildOpenAIMessages`；
- 将工具定义映射为 `openAITool`；
- 空 `InputSchema` 使用当前项目已有的 object 空 Schema 约定；
- 设置 `StreamOptions.IncludeUsage` 与当前流式行为一致。

共享构造逻辑不得执行 DeepSeek Schema 变换。

### 3. 实现三个适配器

**新增文件：**

- `internal/model/openai_compatible_adapter.go`
- `internal/model/gpt_adapter.go`
- `internal/model/deepseek_adapter.go`

实现细节：

- `OpenAICompatibleAdapter` 调用共享构造函数，工具 function 不设置 `Strict`；
- `GPTAdapter` 复用 `OpenAICompatibleAdapter` 或共享构造函数，保持与当前 OpenAI-compatible 请求完全一致；
- `DeepSeekAdapter`：
  - 先构造消息和基础请求；
  - 对每个工具的 `InputSchema` 调用 DeepSeek Schema 规范化函数；
  - 设置 `Function.Strict = true`；
  - 不修改输入的 `json.RawMessage`；
  - 将工具名加入错误上下文。

适配器只构造请求，不执行 HTTP，不处理 SSE 或响应。

### 4. 实现适配器选择器

**新增文件：** `internal/model/adapter_selector.go`

提供稳定、可测试的选择函数，例如：

```go
func SelectModelAdapter(cfg Config) ModelAdapter
```

规范化 `Provider` 和 `Model` 后按以下顺序选择：

1. `provider == "deepseek"`；
2. `model` 以 `deepseek-` 开头；
3. `provider == "gpt" || provider == "openai"`；
4. `model` 以 `gpt-` 开头；
5. `OpenAICompatibleAdapter`。

要求：

- 大小写不敏感；
- 忽略首尾空格；
- DeepSeek 模型名优先于普通第三方 Provider；
- GPT 兜底只匹配 `gpt-`，不匹配 `o*`；
- 选择器返回的适配器不依赖网络或全局状态。

### 5. 抽取 Chat Completions 请求体构造，接入流式和非流式路径

**文件：** `internal/model/stream.go`

重构 `nonStreamingOpenAIMessage` 和 `streamOpenAIMessage`：

- 使用 `SelectModelAdapter(cfg)`；
- 调用适配器构造 `ChatCompletionsRequest`；
- 保留现有的 `MarshalRequestBody`、`doRequestWithRetry`、HTTP 状态检查、非流式响应解析和 `consumeStream`；
- 保持错误语义和事件顺序；
- DeepSeek Schema 预检错误必须在调用 `doRequestWithRetry` 前返回，确保不会发出 HTTP 请求；
- 保持 Anthropic stream fallback 和 Responses API 路由不变。

建议将原来两个方法中的重复请求体构造删除，但不要在本次改动中重写响应解析。

### 6. 保护 ExtraBody 中的工具字段

**文件：** `internal/model/request_body.go`

在合并 Chat Completions 请求体前，为适配器路径增加过滤逻辑，例如：

```go
func FilterProtectedToolFields(body RequestBody) RequestBody
```

保护字段：

```text
tools
 tool_choice
function
strict
parameters
```

实现要求：

- 深拷贝后过滤，不能改变 `cfg.ExtraBody` 或 `cfg.ModelExtraBody`；
- 对 `ExtraBody` 和当前模型的 `ModelExtraBody` 都过滤；
- 保护字段静默忽略；
- 其他字段继续按现有递归合并规则保留；
- 不改变 Anthropic / Responses 现有保护字段行为，除非共享合并入口会影响它们；如会影响，应只在 Chat Completions 适配器路径使用过滤后的 extra body。

同时评估现有 `openAIProtectedRequestFields`，将新增保护字段与已有 `model/messages/tools/stream/stream_options` 规则统一，避免允许 extra body 覆盖适配器生成的字段。

### 7. 实现 DeepSeek strict Schema 规范化

**新增文件：** `internal/model/deepseek_schema.go`

建议暴露内部函数：

```go
func normalizeDeepSeekToolSchema(toolName string, raw json.RawMessage) (json.RawMessage, error)
```

处理流程：

1. 空 Schema 使用合法的 object 空 Schema；
2. `json.Unmarshal` 为有序无关的 `map[string]any`；
3. 深拷贝并递归遍历；
4. 根节点必须为 object；
5. object：
   - 递归处理所有 `properties`；
   - 将全部 properties 放入 `required`；
   - 设置 `additionalProperties: false`；
   - 原本不在 required 的属性转换为 nullable；
6. 递归处理 `items`；
7. 递归处理安全的 `anyOf` / `oneOf` / `allOf`；
8. 递归处理 `$defs` 和 `definitions`；
9. 保留 `$ref`，不展开引用；
10. 使用访问栈检测循环引用或无法安全处理的引用；
11. 对动态 `additionalProperties`、顶层非 object、无法安全表达语义的组合返回错误；
12. 错误包含工具名和 JSON 路径；
13. 返回重新序列化的深拷贝，不复用原始字节切片。

实现时要明确 nullable 表达方式并保持一致：优先保留现有 type，通过 `anyOf` 增加 `{"type":"null"}`；只有在 DeepSeek strict 支持且不会丢失关键约束时才使用 type 数组。对不能安全转换的 union 结构返回错误，不做 best-effort 静默降级。

### 8. 调整请求体合并入口并验证兼容性

**文件：** `internal/model/request_body.go` 及相关调用点

- 让 Chat Completions 请求使用过滤后的 extra body；
- 保证 `model`、`messages`、`tools`、`stream`、`stream_options` 和 DeepSeek function strict 字段不能被覆盖；
- 不影响普通扩展字段，如 `temperature`、`max_tokens` 或 provider-specific 参数；
- 运行现有 ExtraBody 相关测试，确认 Anthropic 和 Responses API 行为没有回归；
- 如果现有 `ValidateExtraRequestBodies` 会拒绝工具保护字段，需要调整为与“静默忽略”规则一致，而不是在配置加载阶段报错。

### 9. 增加单元测试

**新增/修改文件：**

- `internal/model/adapter_test.go`
- `internal/model/deepseek_schema_test.go`
- `internal/model/stream_test.go`
- `internal/model/request_body_test.go`

#### 适配器选择测试

覆盖：

- `Provider=deepseek`；
- `Model=deepseek-chat` 覆盖普通 provider；
- `Provider=openai` / `gpt`；
- `Model=gpt-4.1`；
- 未识别 provider/model 使用 OpenAI-compatible；
- 大小写和首尾空格；
- `o3` 不被选择为 GPT 适配器。

#### Schema 测试

覆盖：

- 普通 object；
- 嵌套 object；
- 可选属性被加入 required 并变成 nullable；
- `additionalProperties` 被设置为 false；
- 数组 items；
- `$defs` 中的 object；
- `$ref` 保留；
- 缺失 `$ref` 定义；
- 非法 JSON；
- 顶层非 object；
- 动态 additionalProperties；
- 不安全 union；
- 循环引用；
- 输入 Schema 未被修改。

#### 请求体/HTTP 测试

复用 `httptest.Server` 和现有 `StreamMessage` 测试风格：

- DeepSeek 流式请求包含 `function.strict=true`；
- DeepSeek 非流式请求同样包含 strict；
- GPT 请求不包含 strict；
- OpenAI-compatible 请求不包含 strict；
- ExtraBody 中的 `tools`、`tool_choice`、`function`、`strict`、`parameters` 被忽略；
- ExtraBody 中的普通字段保留；
- Schema 规范化错误时 server 没有收到请求；
- 现有 tool call SSE 解析和 usage 事件仍然通过。

### 10. 格式化、测试和回归检查

执行：

```bash
gofmt -w internal/model/*.go
go test ./internal/model/...
go test ./...
```

重点检查：

- 旧的 Chat Completions 工具请求测试仍通过；
- Anthropic stream fallback 测试仍通过；
- Responses API 测试仍通过；
- ExtraBody 合并测试仍通过；
- 没有因适配器接口引入 import cycle；
- DeepSeek 预检错误不会触发重试或 HTTP 请求；
- 非 DeepSeek provider 的工具 Schema 保持原行为。

## 预计变更文件

### 新增

- `internal/model/adapter.go`
- `internal/model/adapter_selector.go`
- `internal/model/openai_compatible_adapter.go`
- `internal/model/gpt_adapter.go`
- `internal/model/deepseek_adapter.go`
- `internal/model/deepseek_schema.go`
- `internal/model/adapter_test.go`
- `internal/model/deepseek_schema_test.go`

### 修改

- `internal/model/types.go`
- `internal/model/stream.go`
- `internal/model/request_body.go`
- `internal/model/request_body_test.go`
- `internal/model/stream_test.go`

## 风险与控制

- **Schema 语义变化：** 只对 DeepSeek 适配器转换，其他 provider 不变；无法安全转换时提前报错。
- **ExtraBody 回归：** 过滤仅作用于 Chat Completions 适配器路径，并保留普通扩展字段测试。
- **流式行为变化：** 只替换请求体构造，继续使用原有 SSE 消费器和事件模型。
- **Responses API 误路由：** `StreamMessage` 的 Responses/Anthropic/Chat Completions 协议判断保持原顺序，不让适配器选择器替代协议判断。
- **循环 `$ref`：** 使用访问栈和路径错误，禁止无限递归或无限膨胀。
