# `internal/capability/model`

#### 配置层

文件: [config.go](../../internal/capability/model/config.go)

持久化、Schema、凭据、模型发现与热重载统一由 `internal/platform/config` 管理；本包只保留从 `config.jsonc` 快照合成的运行时请求配置，不再读取或写入旧 `~/.paw/config.json`。完整契约见上文“配置系统 v2”。

##### `type Config struct`

模型连接参数。

字段:
- `ProfileID` / `ProfileName`
- `Provider` / `Transport`
- `APIBaseURL`
- `APIPath`
- `APIKey`
- `APIKeyEnvName`
- `Model`
- `Models`
- `ExtraBody`（`RequestBody`，合并进每个 provider 请求体的任意 JSON 对象）
- `ModelExtraBody`（`map[string]RequestBody`，按模型名附加的请求体）
- `ContextLimitTokens` / `ModelContextLimitTokens`（上下文 token 上限，可按模型覆盖）
- `Timeout`
- `RetryCount` / `RetryCountSet`（来自 v2 Provider `retries`；网络失败或遇到 408/425/429/5xx 时的重试次数，显式 `0` 有效）
- `Stream` / `StreamSet`（来自 Provider/Model/工作区分层结果；显式 `false` 有效）
- `Profiles`（全部已配置 profile）

##### `type Profile struct`

字段:
- `ID` / `Name` / `Provider` / `Transport`
- `APIBaseURL` / `APIPath` / `APIKey` / `APIKeyEnvName`
- `Model` / `Models`
- `ExtraBody` / `ModelExtraBody`
- `ContextLimitTokens` / `ModelContextLimitTokens`
- `Timeout` / `RetryCount` / `Stream` / `StreamSet`
- `CredentialID`

##### 配置来源

应用启动链只通过 `internal/platform/config.Manager` 读取全局 `~/.paw/config.jsonc`，合并同一全局目录中的 `projects/<id>/config.jsonc` 项目级活动模型与安全模型参数覆盖，再把不可变运行时快照注入 `model.Client`。设置 `PAW_CONFIG_HOME` 时，两者统一使用该根目录；不再读取工作区 `.paw/config.jsonc`。旧 `~/.paw/config.json` 不再自动迁移、读取或回写。

provider 的 `discovery.enabled` 为 `true` 时，顶层进程会在启动时执行模型发现；`/config` 中修改 provider 或切换“模型发现”后会显式刷新。普通模型切换和被动文件重载只复用已确认结果或匹配缓存。

provider 的 `discovery.autoRegister` 为 `true` 时，一次成功的发现会把上游模型列表持久化到 `config.jsonc`：新增上游模型、删除上游已消失的模型（含手写条目），并在当前模型被删除时把 `activeModel` 回退到该 provider 剩下的最小模型 ID。写回走与 `/config` 相同的 revision + 文件内容 CAS 与 JSONC 定向补丁路径；发现失败、校验失败或写盘失败都保留原配置并只产生 warning 诊断，`/config` 的“自动注册模型”开关可随时切换。

##### `type RequestBody map[string]any`

任意 JSON 对象，通过 `extraBody` / `modelExtraBody` 合并进 provider 请求体。受保护字段（如 `model`、`system`、`messages`、`tools`、`stream` 等）不允许出现在 extra body 中；`ValidateExtraRequestBodies` 会在加载配置时校验。

#### 请求/响应类型

文件: [types.go](../../internal/capability/model/types.go)

##### `type ChatCompletionsRequest struct`

字段:
- `Model`
- `Messages`
- `Stream`

##### `type ChatCompletionsResponse struct`

字段:
- `Choices[].Message`
- `Error`

这是当前最小响应投影，不是供应商完整 schema。

#### 客户端

文件: [client.go](../../internal/capability/model/client.go)

##### `type Client struct`

职责:
- 发送 HTTP 请求
- 解析同步响应
- 解析流式响应

字段:
- `httpClient`
- `cfg`

##### `NewClient(cfg Config) *Client`

职责:
- 创建模型客户端

##### `RunMessage(ctx, messages) (string, error)`

职责:
- 发起一次非流式请求

当前状态:
- CLI 主路径不依赖它
- 仍然保留作为同步调用能力

##### `StreamMessage(ctx, messages, tools) (<-chan StreamEvent, error)`

职责:
- 发起流式请求（按 `transport` 分发到 OpenAI-compatible 或 Anthropic-compatible 流式解析）
- 返回事件 channel
- `tools` 为原生工具定义（`[]model.ToolDefinition`），用于 LLM API 的原生工具调用请求

#### 流式层

文件: [stream.go](../../internal/capability/model/stream.go)

##### `type StreamEvent struct`

字段:
- `Delta`
- `Thinking`
- `Done`
- `Err`
- `Usage`

约定:
- 一次事件只表达一种状态

##### 关键内部函数

这些函数是 `model` 包内部的流式拆分点，不是外部扩展接口:

###### `newStreamScanner(body) *bufio.Scanner`

职责:
- 创建并配置 SSE 行扫描器

###### `handleStreamLine(ctx, line, events) (done bool, err error)`

职责:
- 处理单行 SSE 文本

###### `handleStreamPayload(ctx, payload, events) (done bool, err error)`

职责:
- 处理 `data:` 后的 payload

###### `decodeStreamChunk(payload) (chatCompletionsStreamResponse, error)`

职责:
- JSON 解码

###### `emitChunkEvents(ctx, chunk, events) bool`

职责:
- 把 chunk 转成 `StreamEvent`

###### `consumeStream(ctx, resp, events)`

职责:
- 后台消费整个 SSE 响应流

#### Responses 流式防御

`responses_transport.go`、`responses_sse.go`、`responses_failure.go`、`responses_completion.go` 将传输编排、分帧诊断、安全重试和完成提交分开，保持 `StreamMessage` / `StreamEvent` 接口不变。

1. **协议与诊断**：支持多行 SSE、BOM、CR/LF/CRLF；只在空行结束事件后派发，连接结束时丢弃未闭合的末尾事件，保留原始 EOF/read/idle 分类，而不把截断尾块误报为 JSON decode。即使末尾 JSON 看起来完整，缺少事件终止空行也不提交正文、工具或完成状态。此边界遵循 [SSE 解析规则](https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream)。保留帧大小上限，错误包含事件阶段、底层错误类别与 request ID，不输出原始响应体。首个完整事件的坏帧错误未被完整完成快照修复时，后续空闲、读取或 EOF 错误不能覆盖它；取消仍立即生效。
2. **生命周期**：空闲 watchdog 只累计等待底层 `Read` 的时间，默认预算为 90 秒；解析、事件交付、UI 或输出消费者暂停不计入网络空闲。读到字节后重置预算，零字节读取保留剩余预算。空闲超时与取消共用幂等关闭路径，消费暂停时取消仍有效；清理计时器和 context callback，避免旧超时回调关闭已经恢复的连接。
3. **重试预算**：HTTP 与流失败共用 `retries + 1` 次总请求预算及退避，尊重 `Retry-After`，取消立即结束；认证、协议限制等确定性错误不盲目重试。
4. **输出边界与提交**：只有尚未输出正文或思考时，流失败才可重新 POST；未完成的工具参数仍留在缓冲区，不属于已发布输出。已输出后重发会产生新的生成结果，因此直接报告首次错误，保留事件阶段、底层错误类别、request ID 及已输出正文/思考字节数，不用新的输出覆盖或拼接旧内容。工具、用量及完成状态仅在完成快照前缀校验后一起提交；坏帧恢复只信任完整完成快照，不能用残缺参数覆盖它。

这能自动恢复尚无可见输出的部分瞬断。已有输出后，适配层不做跨请求逐字重放；运行时通过 `IsRecoverableResponsesInterruption` 识别已经发布正文或思考的 `stream_idle`、`stream_read`、`unexpected_eof`，从已完成步骤发起新的模型响应。每次连续断流最多自动续接 10 次，成功完成模型响应后清零，后续再断流重新从 1/10 开始。首次无输出的失败仍仅遵守 provider 的 `retries`；已进入续接后，即使下一次未输出就再次断流，也会用剩余续接次数。每次请求内部未输出时的 provider 重试配置保持原样；取消、协议错误和 provider 错误不走运行时续接。

正文或纯思考的 partial 先收尾并保存，仅供历史展示；新响应使用已提交的步骤和工具结果，不携带未完成内容与工具参数，不拼接旧输出。退避为 1、2、4、5 秒并封顶，等待可取消；用完次数后保留故障，可输入“继续”手动接续。不自动切换模型或降级为非流式请求。

启用请求观测时，每次进入消费阶段的 Responses 流以 `attempt_stream_end` 写入现有 tracer JSONL，并关联所属 HTTP attempt。记录协议解析终态、底层读取结束类别、读等待与消费暂停的最长时间、最后收到字节距结束的时间、本地超时/取消标记，以及未闭合 SSE 尾部长度。字节数是 HTTP 自动解压后的应用层字节，`uncompressed` 标记自动解压；协议和编码仅记录固定白名单值，不记录正文、认证头或原始错误文本。

完成快照验证通过后先持久化该诊断，再交付最终完成事件；清理连接的耗时不进入统计。若最终交付被取消，传输诊断仍可为 `completed`，逻辑请求的 `request_end` 则保留取消状态。中途取消时上游读取和调用方退出是异步的，进程立即退出仍可能缺少这条诊断；缺失记录不能当成没有断流。

#### Anthropic 流式层

文件: [anthropic_stream.go](../../internal/capability/model/anthropic_stream.go)

职责:
- 针对 Anthropic Messages API 的流式解析（`message_start` / `content_block_start` / `content_block_delta` / `message_delta` 等）
- 处理 thinking、text、tool_use 内容块
- 支持 system prompt 的 `cache_control`（`type: "ephemeral"`）
- 把 provider 返回的 usage 汇总到 `StreamEvent.Usage`
