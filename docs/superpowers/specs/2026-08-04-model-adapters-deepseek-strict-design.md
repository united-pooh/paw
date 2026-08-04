# GPT/DeepSeek 模型适配器与 DeepSeek Strict Tool Calling 设计

- 日期：2026-08-04
- 状态：已批准设计，待实现

## 1. 背景与目标

当前模型请求主要在 `internal/model` 中按协议分支处理。需要为 GPT 家族模型和 DeepSeek 模型建立独立适配器，同时让其他模型继续使用 OpenAI-compatible 默认行为。

本次改造目标：

- GPT 家族模型使用 `GPTAdapter`；
- DeepSeek 模型使用 `DeepSeekAdapter`；
- 其他模型使用 `OpenAICompatibleAdapter`；
- DeepSeek 工具调用始终使用 `function.strict = true`；
- 自动将工具参数 Schema 规范化为 DeepSeek strict 所需形式；
- 不改变调用方传入的 `ToolDefinition`；
- 不允许 `ExtraBody` / `ModelExtraBody` 覆盖工具定义；
- 继续复用现有 HTTP、SSE、usage、tool call 和统一 `StreamEvent` 处理逻辑。

参考：<https://api-docs.deepseek.com/zh-cn/guides/tool_calls>

## 2. 适配器接口

适配器采用小接口，只负责构造 Chat Completions 请求体，不负责 HTTP 或响应解析：

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

### 2.1 OpenAI-compatible 适配器

`OpenAICompatibleAdapter` 保留当前默认行为：

- 构造 OpenAI 风格消息；
- 构造普通 function tools；
- 不自动增加 `strict`；
- 作为未识别 provider/model 的默认实现。

### 2.2 GPT 适配器

`GPTAdapter` 当前复用 OpenAI-compatible 的请求构造逻辑，但作为独立扩展边界：

- 请求格式与 OpenAI-compatible 保持一致；
- 暂不增加额外 GPT 专属字段；
- 为后续 GPT 专属能力预留位置。

### 2.3 DeepSeek 适配器

`DeepSeekAdapter` 基于 Chat Completions 请求构造，但对工具定义执行 DeepSeek 专属处理：

- 深拷贝每个 `InputSchema`；
- 规范化 strict Schema；
- 每个 function 设置 `strict: true`；
- Schema 无法安全转换时，在发起 HTTP 请求前返回错误；
- 不修改原始 `ToolDefinition`。

最终工具结构为：

```json
{
  "type": "function",
  "function": {
    "name": "tool_name",
    "description": "...",
    "parameters": {},
    "strict": true
  }
}
```

## 3. 适配器选择

选择时对 `Provider` 和 `Model` 执行去空格并转小写。优先级如下：

```text
1. provider == deepseek
2. model 以 deepseek- 开头
3. provider == gpt 或 openai
4. model 以 gpt- 开头
5. OpenAI-compatible
```

示例：

```text
Provider=openrouter, Model=deepseek-chat
→ DeepSeekAdapter
```

```text
Provider=custom, Model=gpt-4.1
→ GPTAdapter
```

DeepSeek 模型名优先于其他普通 Provider。GPT 模型名兜底规则仅匹配 `gpt-*`，不将 `o*` 等模型归入 GPT 适配器。

## 4. 与现有协议路由的关系

适配器不接管协议生命周期：

```text
Client
 ├─ 选择 Anthropic / Responses / Chat Completions 协议
 ├─ 选择 Chat Completions 适配器
 ├─ 由适配器构造请求体
 ├─ Client 发送 HTTP 请求
 └─ 复用现有响应、SSE、usage 和 StreamEvent 解析
```

本次重点改造 Chat Completions 路径。Anthropic Messages、Responses API 以及现有统一事件解析逻辑不在适配器中复制。

## 5. ExtraBody 保护规则

`ExtraBody` 与 `ModelExtraBody` 仍可提供普通 provider 扩展字段，但不能作为工具定义入口，也不能覆盖适配器生成的工具内容。

适配器保护字段：

```text
tools
tool_choice
function
strict
parameters
```

处理规则：

- ExtraBody 中的保护字段静默忽略；
- 不报错；
- 内部根据 `ToolDefinition` 重新生成工具定义；
- DeepSeek 的 `function.strict = true` 始终由适配器控制；
- ExtraBody 中的其他普通字段继续保留。

## 6. DeepSeek Schema 规范化

新增 `internal/model/deepseek_schema.go`，负责 DeepSeek strict Schema 的深拷贝、递归规范化和预检。

### 6.1 规范化流程

```text
原始 InputSchema
    ↓ 深拷贝
递归规范化
    ↓
strict 约束预检
    ↓
写入 function.parameters
```

### 6.2 object 规则

对于 object Schema：

- 设置 `additionalProperties: false`；
- 将所有 `properties` 加入 `required`；
- 原本可选的字段转换为 nullable，以保留其逻辑可选语义；
- 保留描述、枚举、默认值等无冲突信息；
- 递归处理嵌套 object。

例如：

```json
{
  "type": "object",
  "properties": {
    "query": { "type": "string" },
    "limit": { "type": "integer" }
  },
  "required": ["query"]
}
```

规范化为：

```json
{
  "type": "object",
  "properties": {
    "query": { "type": "string" },
    "limit": { "type": ["integer", "null"] }
  },
  "required": ["query", "limit"],
  "additionalProperties": false
}
```

### 6.3 递归结构与引用

递归处理：

- 根 Schema；
- `properties`；
- 数组 `items`；
- 可安全转换的 `anyOf` / `oneOf` / `allOf`；
- `$defs` / `definitions` 中的定义。

`$ref` 保留原样，不直接展开，以避免 Schema 膨胀和递归展开。引用的定义本身仍在 `$defs` / `definitions` 中规范化。

检测循环引用，避免无限递归。

### 6.4 不安全 Schema

采用“安全转换，否则报错”：

- 确定不会改变语义的结构自动转换；
- 无法安全转换的结构在请求发送前返回错误；
- 错误包含工具名称、Schema 路径和具体原因。

需要报错的情况包括：

- 顶层 Schema 不是 object；
- 无法保留语义的复杂 `oneOf` / `anyOf` / `allOf`；
- 动态 `additionalProperties`；
- 不支持的 Schema 组合；
- 无法安全处理的循环引用；
- 非法 JSON；
- `$ref` 指向不存在的定义。

错误示例：

```text
DeepSeek 工具 weather 的 Schema $.properties.location:
无法将 oneOf 转换为 strict nullable Schema
```

## 7. 代码结构

建议新增：

```text
internal/model/
├── adapter.go
├── adapter_selector.go
├── openai_compatible_adapter.go
├── gpt_adapter.go
├── deepseek_adapter.go
└── deepseek_schema.go
```

现有公共逻辑应抽取并复用，例如：

```go
buildOpenAIMessages(...)
buildOpenAITools(...)
buildChatCompletionsRequest(...)
```

避免 GPT 与 DeepSeek 复制流式解析和 HTTP 处理。

## 8. 测试要求

### 8.1 适配器选择

覆盖：

- provider 为 `deepseek`；
- model 为 `deepseek-*`；
- DeepSeek 模型覆盖普通 provider；
- provider 为 `openai` / `gpt`；
- model 为 `gpt-*`；
- provider/model 无法识别时使用 OpenAI-compatible；
- 大小写和首尾空格处理。

### 8.2 DeepSeek Schema

覆盖：

- 普通 object；
- 嵌套 object；
- 可选字段转 nullable 并加入 required；
- 数组 items；
- `$defs`；
- `$ref`；
- `additionalProperties`；
- 非法 JSON；
- 循环引用；
- 不支持的组合；
- 原始 Schema 不被修改。

### 8.3 请求体

覆盖：

- DeepSeek function 包含 `strict: true`；
- GPT 不包含 DeepSeek strict 字段；
- OpenAI-compatible 不包含 strict；
- ExtraBody 中的工具字段被忽略；
- ExtraBody 中的普通字段保留；
- Schema 预检失败时不发送 HTTP 请求。

## 9. 非目标

本次不包含：

- 为每个 provider 重写 HTTP 客户端；
- 为每个 provider 复制 SSE 解析器；
- 改变 Anthropic Messages 或 Responses API 的统一事件模型；
- 通过新增用户配置开关控制 DeepSeek strict；
- 允许 ExtraBody 覆盖内部工具定义。
