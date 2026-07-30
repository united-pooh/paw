# 模型级额外请求体配置规格

**状态：** Draft  
**日期：** 2026-07-29  
**目标版本：** 待定  
**涉及模块：** `internal/model`、`internal/ui/bubble`

## 1. 背景

Paw 当前通过 `~/.paw/config.json` 配置模型 Profile，支持 endpoint、transport、模型列表、超时、重试和流式模式。模型请求体则由 Go 结构体固定构造。

这导致 Provider 新增或私有扩展参数时，必须修改 Paw 代码。例如 OpenAI-compatible endpoint 可能支持：

```json
{
  "service_tier": "fast"
}
```

其他 endpoint 还可能支持：

```json
{
  "reasoning_effort": "high",
  "temperature": 0.2,
  "metadata": {
    "team": "platform"
  }
}
```

Paw 不应为每个 Provider 参数增加固定 Go 字段。配置没有声明额外参数时，也不应向请求添加任何新的可选字段。

## 2. 目标

1. 允许在模型 Profile 上配置任意额外 JSON 请求体字段。
2. 支持 Profile 级公共配置和模型级覆盖。
3. 支持 OpenAI-compatible 与 Anthropic transport。
4. 未配置额外请求体时，保持现有请求行为不变。
5. 不在 Paw 中维护 Provider 参数白名单。
6. 防止配置覆盖 Paw 必须控制的结构性字段。
7. 在配置加载或应用阶段尽早发现错误，而不是请求发出后才失败。
8. `/model` 切换 Profile 或模型后，正确保留并应用对应额外请求体。

## 3. 非目标

本功能不负责：

1. 验证 Provider 是否支持某个透传字段。
2. 为 `service_tier`、`temperature`、`reasoning_effort` 等字段建立强类型 Go 配置。
3. 提供 TUI 表单编辑任意 JSON 参数。
4. 支持额外 HTTP header；本规格仅处理 JSON 请求体。
5. 允许覆盖 Paw 管理的消息、工具、模型选择或流式协议字段。
6. 将模型额外参数记录进 transcript 或发送给模型作为上下文。

Provider 不支持某个合法透传字段时，由 Provider API 返回错误，Paw 沿用现有模型接口错误处理流程展示该错误。

## 4. 用户配置格式

### 4.1 Profile 级公共配置

新增可选字段 `extraBody`，其值必须是 JSON object：

```json
{
  "id": "openai-gateway",
  "name": "OpenAI Gateway",
  "provider": "openai",
  "transport": "openai-compatible",
  "baseUrl": "https://gateway.example/v1",
  "apiPath": "/chat/completions",
  "model": "gpt-5.6-sol",
  "models": [
    "gpt-5.6-sol",
    "gpt-5.6-pro"
  ],
  "extraBody": {
    "metadata": {
      "team": "platform",
      "environment": "production"
    }
  }
}
```

`extraBody` 应用于该 Profile 中所有模型。

### 4.2 模型级配置

新增可选字段 `modelExtraBody`。它必须是 JSON object，其每个 key 是模型名，每个 value 也必须是 JSON object：

```json
{
  "modelExtraBody": {
    "gpt-5.6-sol": {
      "service_tier": "fast"
    },
    "gpt-5.6-pro": {
      "reasoning_effort": "high"
    }
  }
}
```

只有当前选中的模型对应的对象参与请求体合并。

### 4.3 完整示例

```json
{
  "schemaVersion": 1,
  "activeModelProfileId": "openai-gateway",
  "modelProfiles": [
    {
      "id": "openai-gateway",
      "name": "OpenAI Gateway",
      "provider": "openai",
      "transport": "openai-compatible",
      "baseUrl": "https://gateway.example/v1",
      "apiPath": "/chat/completions",
      "apiKeyEnvName": "OPENAI_API_KEY",
      "model": "gpt-5.6-sol",
      "models": [
        "gpt-5.6-sol",
        "gpt-5.6-pro"
      ],
      "timeoutSeconds": 180,
      "retryCount": 2,
      "stream": true,
      "extraBody": {
        "metadata": {
          "team": "platform",
          "environment": "production"
        }
      },
      "modelExtraBody": {
        "gpt-5.6-sol": {
          "service_tier": "fast",
          "metadata": {
            "environment": "development",
            "feature": "agent"
          }
        },
        "gpt-5.6-pro": {
          "reasoning_effort": "high"
        }
      }
    }
  ]
}
```

选择 `gpt-5.6-sol` 时，额外字段深度合并为：

```json
{
  "service_tier": "fast",
  "metadata": {
    "team": "platform",
    "environment": "development",
    "feature": "agent"
  }
}
```

### 4.4 无配置时的行为

以下配置不产生任何额外请求字段：

```json
{
  "model": "gpt-5.6-sol",
  "models": ["gpt-5.6-sol"]
}
```

以下空对象配置也不产生额外请求字段：

```json
{
  "extraBody": {},
  "modelExtraBody": {
    "gpt-5.6-sol": {}
  }
}
```

## 5. 数据模型

### 5.1 JSON object 表示

运行时建议使用命名类型，而不是 `map[string]any` 裸类型散布在代码中：

```go
type RequestBody map[string]any
```

`RequestBody` 表示一个可深度复制、验证和合并的 JSON object。

建议在 `internal/model` 中提供以下集中能力：

```go
func CloneRequestBody(body RequestBody) RequestBody
func MergeRequestBodies(base, override RequestBody) RequestBody
func ValidateExtraRequestBody(profileID, modelName, transport string, body RequestBody) error
func EffectiveExtraRequestBody(cfg Config) RequestBody
```

具体命名可在实现时调整，但深度复制、合并、校验和生效配置计算不应分散在 OpenAI、Anthropic 与 UI 代码中重复实现。

### 5.2 Config 与 Profile

`Config` 和 `Profile` 增加：

```go
ExtraBody      RequestBody
ModelExtraBody map[string]RequestBody
```

`persistedModelConfig` 增加：

```go
ExtraBody      RequestBody            `json:"extraBody,omitempty"`
ModelExtraBody map[string]RequestBody `json:"modelExtraBody,omitempty"`
```

### 5.3 所有权与复制

`map[string]any` 和嵌套 map/slice 是引用类型，因此以下边界必须深度复制：

1. persisted config 转换为 `Profile`；
2. `Profile.Config()`；
3. `cloneProfiles()`；
4. `ConfiguredProfiles()` fallback；
5. `Client.CurrentModelConfig()` 如当前语义要求返回隔离副本；
6. `Client.ApplyModelConfig()` 存储配置前；
7. 合并请求体时。

目标是避免 `/model` 切换、测试代码或请求构造过程意外修改共享 Profile 配置。

## 6. 合并语义

最终请求体按以下顺序构造：

```text
Paw 基础请求体
  ← Profile.extraBody
  ← Profile.modelExtraBody[当前模型]
```

后面的值覆盖前面的值。

### 6.1 深度合并规则

对于同名字段：

1. object + object：递归合并；
2. array：覆盖方整体替换基础数组；
3. string、number、boolean：覆盖方替换基础值；
4. `null`：覆盖方显式替换为 JSON `null`，最终请求中保留该字段；
5. 类型不同：覆盖方整体替换基础值。

示例：

```json
// extraBody
{
  "metadata": {
    "team": "platform",
    "environment": "production"
  },
  "tags": ["profile"]
}
```

```json
// modelExtraBody[model]
{
  "metadata": {
    "environment": "development",
    "feature": "agent"
  },
  "tags": ["model"],
  "optional": null
}
```

合并结果：

```json
{
  "metadata": {
    "team": "platform",
    "environment": "development",
    "feature": "agent"
  },
  "tags": ["model"],
  "optional": null
}
```

### 6.2 仅顶层字段受保护

保护规则只检查额外请求体的顶层 key。

不合法：

```json
{
  "model": "another-model"
}
```

合法：

```json
{
  "metadata": {
    "model": "internal-label"
  }
}
```

原因是嵌套对象由 Provider 自行定义，Paw 不应推测其内部 schema。

## 7. 受保护字段

### 7.1 OpenAI-compatible

以下顶层字段禁止出现在 `extraBody` 或任何 `modelExtraBody[model]` 中：

- `model`
- `messages`
- `tools`
- `stream`
- `stream_options`

这些字段由 Paw 管理，以保证：

- `/model` 选择与实际请求一致；
- 对话历史不会被配置替换；
- 工具定义和工具闭环不被破坏；
- 流式与非流式代码路径一致；
- usage 流选项保持 Paw 的现有协议要求。

### 7.2 Anthropic

以下顶层字段禁止出现：

- `model`
- `system`
- `messages`
- `tools`
- `stream`

`max_tokens` 不受保护。

当前 Anthropic 默认发送：

```json
{
  "max_tokens": 8192
}
```

如果额外请求体配置：

```json
{
  "max_tokens": 16384
}
```

则最终值为 `16384`。未配置时继续使用 `8192`。

### 7.3 字段名匹配

受保护字段按请求 JSON 的精确字段名匹配，区分大小写。

例如 `stream_options` 被保护，而 `metadata.stream_options` 不被保护。`Stream_Options` 作为未知 Provider 字段可以透传；Provider 是否接受由其自行决定。

## 8. 配置校验

### 8.1 校验时机

必须在以下入口校验：

1. 从 `~/.paw/config.json` 加载 Profile 时；
2. `Client.ApplyModelConfig()` 应用程序内构造的配置时；
3. `SaveModelConfig()` 持久化前。

请求构造函数仍可做防御性校验，但不能把首次发现配置错误推迟到网络请求阶段。

### 8.2 object 类型要求

`extraBody` 如果出现，必须是 JSON object。

不合法：

```json
{
  "extraBody": "fast"
}
```

不合法：

```json
{
  "extraBody": null
}
```

`modelExtraBody` 如果出现，必须是 JSON object；其每个值也必须是 JSON object。

不合法：

```json
{
  "modelExtraBody": {
    "gpt-5.6-sol": null
  }
}
```

空 object `{}` 合法。

为了区分“字段缺失”和“显式 null”，不能只依赖解码到非指针 map 后检查 nil。配置解析层应保留字段是否存在及其原始 JSON 类型，或为这些字段实现严格的自定义 JSON 解码。

### 8.3 模型名严格校验

`modelExtraBody` 的每个 key 必须满足以下任一条件：

1. 存在于 Profile 的 `models` 列表；
2. 等于 Profile 的 `model` 字段。

否则配置加载失败。

示例错误：

```text
model profile "openai-gateway": modelExtraBody references unknown model "gpt-5.6-soll"
```

模型名匹配采用精确字符串匹配，与现有 `SupportsModel` 行为一致。

### 8.4 受保护字段错误

发现受保护字段时直接报错，不静默忽略。

建议错误格式：

```text
model profile "openai-gateway": extraBody contains protected field "stream"
```

模型级建议格式：

```text
model profile "openai-gateway": modelExtraBody["gpt-5.6-sol"] contains protected field "model"
```

错误必须包含：

- Profile ID；
- 配置来源是 `extraBody` 还是具体模型的 `modelExtraBody`；
- 冲突字段名。

### 8.5 transport 识别

保护字段集合由当前 Profile 的 transport 决定：

- transport 包含 `anthropic`：使用 Anthropic 保护集合；
- 其他现有 OpenAI-compatible 路径：使用 OpenAI 保护集合。

该判断应与当前请求路由逻辑集中复用，避免配置校验与请求发送对 transport 的理解不一致。

## 9. 请求构造

### 9.1 统一构造策略

不要尝试给现有请求结构体实现包含任意字段的普通 struct tag。推荐流程：

1. 使用现有强类型结构构造 Paw 基础请求；
2. `json.Marshal` 后解码为 `RequestBody`，或通过一个集中 helper 转为 object；
3. 计算 `EffectiveExtraRequestBody(cfg)`；
4. 深度合并基础请求与额外请求；
5. 对最终 object 执行 `json.Marshal`。

建议抽象：

```go
func MarshalRequestBody(base any, extra RequestBody) ([]byte, error)
```

该 helper 应：

- 保证 base 序列化结果是 JSON object；
- 不修改 `extra`；
- 深度合并；
- 保留 JSON `null`；
- 返回带上下文的错误。

### 9.2 OpenAI-compatible 路径

以下路径都必须应用额外请求体：

1. `Client.RunMessage()`；
2. `nonStreamingOpenAIMessage()`；
3. `streamOpenAIMessage()`。

这三条路径必须共享同一个请求体合并 helper，避免只有聊天主路径生效而其他调用遗漏。

无额外配置时，JSON 语义应与当前请求保持一致。

流式示例最终请求：

```json
{
  "model": "gpt-5.6-sol",
  "messages": [],
  "stream": true,
  "stream_options": {
    "include_usage": true
  },
  "tools": [],
  "service_tier": "fast"
}
```

实际没有工具时，继续遵守现有 `omitempty` 行为，不要求发送空 `tools`。

### 9.3 Anthropic 路径

`buildAnthropicMessagesRequest()` 继续负责构造 Paw 基础请求，包括：

- model；
- system prompt 与 cache control；
- messages；
- tools；
- stream；
- 默认 `max_tokens: 8192`。

`streamAnthropicMessage()` 在序列化网络请求前，将有效额外请求体合并进基础请求。

示例：

```json
{
  "model": "claude-sonnet",
  "system": [],
  "messages": [],
  "max_tokens": 16384,
  "stream": true,
  "metadata": {
    "user_id": "paw"
  }
}
```

Anthropic 当前只有流式路径；本规格不要求新增非流式 Anthropic 实现。

### 9.4 Anthropic 失败后的 OpenAI fallback

当前 `StreamMessage()` 在 Anthropic 流式调用建立失败后会尝试 OpenAI-compatible 流式路径。额外请求体已经按 Profile transport 通过 Anthropic 保护字段校验，因此 fallback 时可能包含 OpenAI 结构性冲突字段，例如 Anthropic 允许但 OpenAI 保护的 `stream_options`。

为避免同一配置在 fallback 路径破坏 Paw 请求，采用以下规则：

- transport 为 Anthropic 时，额外请求体必须同时不能覆盖 OpenAI fallback 的结构性字段；
- Anthropic 实际保护集合取两者并集：
  - `model`
  - `system`
  - `messages`
  - `tools`
  - `stream`
  - `stream_options`

`max_tokens` 仍允许覆盖。

这样保持现有 fallback 行为安全。若未来移除 Anthropic 到 OpenAI 的自动 fallback，可重新收窄保护集合。

## 10. `/model` 切换与持久化

### 10.1 Profile 切换

`Profile.Config()` 必须复制：

- `extraBody`；
- 完整的 `modelExtraBody` map。

切换 Profile 后，只使用新 Profile 的额外请求体。

### 10.2 同 Profile 模型切换

`/model` 修改 `cfg.Model` 时，不修改 `extraBody` 或 `modelExtraBody`。下一次请求通过 `cfg.Model` 动态选择：

```go
cfg.ModelExtraBody[cfg.Model]
```

因此同一 Profile 内切换模型后，无需重写或展开配置。

### 10.3 保存行为

`SaveModelConfig()` 当前以通用 document map 更新选中的 Profile，并保留未显式处理的未知字段。实现后应显式保存 `extraBody` 与 `modelExtraBody`，并保证：

1. `/model` 只改变 active profile/model 时不会丢失两类字段；
2. map/slice 深度复制后再写入 document；
3. 空配置的持久化策略一致：
   - 空 `extraBody` 可省略或保存 `{}`；
   - 空 `modelExtraBody` 可省略；
   - 不得写成 `null`。

推荐使用 `omitempty` 语义：空配置从 JSON 中省略。

## 11. 向后兼容

1. 现有 `schemaVersion: 1` 配置继续有效。
2. 本次新增字段是可选字段，不要求立即提升 schemaVersion。
3. 未配置新字段时，请求体不增加新的可选 Provider 参数。
4. 现有 `/model` UI 不增加步骤或表单。
5. 现有 timeout、retry、stream、API key 行为保持不变。
6. 配置文件中的其他未知顶层/Profile 字段继续由现有 document-preserving 保存机制保留。

如果实现过程中发现严格 object/null 校验无法在当前 typed unmarshal 下可靠完成，可以为 `persistedModelConfig` 实现自定义解码，但不应因此破坏未知字段保留。

## 12. 安全与可观测性

### 12.1 敏感信息

额外请求体可能包含 Provider metadata，但不应推荐把 API key、token 或密码放入其中。

Paw 不应：

- 在普通日志中打印完整额外请求体；
- 在 transcript 中展示完整额外请求体；
- 在错误信息中回显字段值。

校验错误只报告字段路径，不报告值。

### 12.2 请求追踪

本规格不要求 token tracer 记录完整参数。若需要可观测性，只允许记录：

- 是否存在 Profile 级 extra body；
- 是否命中模型级 extra body；
- 顶层字段名列表，并应评估字段名本身是否敏感。

默认实现可不增加任何追踪字段。

## 13. 测试规格

### 13.1 配置解析

至少覆盖：

1. 没有新字段时加载成功；
2. `extraBody` object 加载成功；
3. `modelExtraBody` object 加载成功；
4. 嵌套 object、array、number、boolean、string、null 叶子值保持类型；
5. `extraBody: null` 报错；
6. `extraBody` 为 string/array/number/boolean 报错；
7. `modelExtraBody: null` 报错；
8. `modelExtraBody[model]: null` 报错；
9. `modelExtraBody[model]` 为非 object 报错；
10. 未知模型名报错；
11. 当前 `model` 未出现在 `models` 中，但作为 key 时合法；
12. OpenAI 受保护字段报错；
13. Anthropic/兼容 fallback 受保护字段报错；
14. 嵌套同名字段不报错，例如 `metadata.model`；
15. 错误信息包含 Profile ID、配置来源和字段名。

### 13.2 深度合并

至少覆盖：

1. 无 extra 时 base 不变；
2. Profile 字段加入 base；
3. 模型字段覆盖 Profile 标量；
4. object 递归合并；
5. array 整体替换；
6. 类型不同时整体替换；
7. null 显式覆盖并保留；
8. 合并不修改输入 map/slice；
9. 返回值与输入无共享嵌套引用。

### 13.3 OpenAI 请求

通过 `httptest.Server` 捕获请求体，至少覆盖：

1. `RunMessage()` 应用 Profile 和模型 extra；
2. 非流式 `StreamMessage()` 应用 extra；
3. 流式 `StreamMessage()` 应用 extra；
4. `service_tier: "fast"` 原样发送；
5. 基础字段 `model/messages/tools/stream/stream_options` 保持 Paw 构造值；
6. 没有 extra 时不出现额外字段；
7. 切换模型后只应用对应的 `modelExtraBody`。

### 13.4 Anthropic 请求

通过 `httptest.Server` 捕获请求体，至少覆盖：

1. Profile 和模型 extra 被深度合并；
2. 未配置 `max_tokens` 时为 `8192`；
3. 配置 `max_tokens` 时覆盖为指定值；
4. system/messages/tools/stream 仍由 Paw 构造；
5. 没有 extra 时请求行为保持不变。

### 13.5 Profile 与 UI 切换

至少覆盖：

1. `Profile.Config()` 深度复制额外请求体；
2. `cloneProfiles()` 不共享嵌套引用；
3. `/model` 切换 Profile 后应用新 Profile 参数；
4. `/model` 切换模型后保留配置 map，并在请求时选择正确模型参数；
5. 保存 active model/profile 后 `extraBody` 和 `modelExtraBody` 不丢失。

## 14. 验收标准

以下配置：

```json
{
  "model": "gpt-5.6-sol",
  "models": ["gpt-5.6-sol"]
}
```

不得产生任何额外 Provider 参数。

加入：

```json
{
  "modelExtraBody": {
    "gpt-5.6-sol": {
      "service_tier": "fast"
    }
  }
}
```

后，OpenAI-compatible 请求必须包含：

```json
{
  "service_tier": "fast"
}
```

且无需在 Go 代码中定义 `ServiceTier` 字段。

同时必须满足：

1. Profile 与模型参数按定义深度合并；
2. OpenAI-compatible 和 Anthropic 均支持；
3. Anthropic `max_tokens` 可覆盖，默认仍为 8192；
4. 结构性受保护字段在配置加载或应用时明确报错；
5. 未知模型名明确报错；
6. 非 object 配置明确报错；
7. 所有现有测试及新增测试通过。

## 15. 建议实现分层

建议新增独立文件，例如：

```text
internal/model/request_body.go
internal/model/request_body_test.go
```

职责包括：

- JSON object 类型；
- 深度复制；
- 深度合并；
- 有效模型参数计算；
- transport 保护字段；
- 请求体 marshal helper。

现有文件职责保持：

- `config.go`：加载、保存、Profile 转换和配置级校验；
- `stream.go`：OpenAI-compatible 请求与响应流程；
- `client.go`：客户端状态和简单非流式调用；
- `anthropic_stream.go`：Anthropic 协议转换与流解析；
- `model_wizard.go`：选择 Profile/模型，不解释任意参数内容。

这种拆分避免把通用 JSON 合并逻辑复制到每个 transport 中，也避免让 UI 了解 Provider 请求体细节。
