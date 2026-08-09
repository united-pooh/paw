# 对齐修复计划：把 CodeWhale 的「工具调用配对防御」对齐到 paw

## 1. 背景

用户环境通过本地代理（`http://localhost:8317/v1`，OpenAI 兼容）调用模型时，曾遇到
`400 {"error":{"message":"No tool call found for tool output with call_id call_00_...","type":"invalid_request_error"}}`。

调研 CodeWhale 后发现其处理哲学是 **预防优于重试**：四层防御
（错误分类不重试 → 出站请求前配对修复 → 会话加载时修复 → 协议线级兜底 + 压缩级联），
真正漏网的 400 被归类为不可重试错误，脱敏后直接展示给用户。

本计划把这套防御**对齐到 paw 的架构**，补齐缺口。

## 2. paw 现状与差距（调研结论）

### 2.1 paw 的协议架构（关键背景）

- **默认路径（Chat Completions / Anthropic）**：`runner.buildModelMessages` →
  `renderMessageForModel` 把 `ToolUses`/`ToolResults` 渲染成**文本信封**
  （`toolUseEnvelope` JSON / `TOOL_RESULT:\n{json}`）放进 `Content` 字符串。
  **无原生 call_id 引用 → 不会触发 "No tool call found" 类 400**。
- **Responses API 路径（`internal/model/responses.go`）**：`buildResponsesInput`
  生成原生 `function_call`（`CallID`）与 `function_call_output`（`CallID`）项，
  并支持 `ProviderData` 权威重放原始 output items。
  **这里存在原生 call_id 引用 → 是唯一可能产生该 400 的出口**。
- 工具执行：`runToolCallsWithCheckpoint` 执行后 `buildToolResultsMessage(results)`
  追加为 user 消息；崩溃/中断时 `session.RecoveryState` 记录已完成结果与丢弃调用，
  下一轮注入 `[Paw recovery]` 用户消息。
- 会话持久化：`commitHistory` 存结构化 `message.Message`（含 `ToolUseID`），
  `LoadSession` 恢复后直接使用。

### 2.2 差距对照表

| CodeWhale 层 | paw 现状 | 差距 |
|---|---|---|
| 0. 错误分类，400 不重试 | ✅ `isRetryableHTTPStatus` 只重试 408/425/429/5xx；`ProviderHTTPError` 解析 type/code/message | ❌ 无「工具配对损坏」识别与针对性提示 |
| 1. 出站请求前配对修复 | ❌ 无 | ❌ 需新增 |
| 2. 会话加载时修复 | ❌ `LoadSession` 原样加载 | ❌ 需新增 |
| 3. 协议线级兜底 | ⚠️ `validateResponsesInputItems` 只校验 call_id **非空**，不校验引用存在 | ❌ 需升级为修复 |
| 4. 压缩/清理级联 | ✅ `keepMessageIndexes`/`assistantCallIndexForResults` 保持错误结果与其 call 成对；折叠时 results 进 summary 文本后成对删除；`tool_result_maintenance` 只改 Content 不删消息 | ✅ 已达标，不加代码 |

## 3. 修复方案（对齐 CodeWhale，适配 paw 架构）

### 3.1 新增 `internal/model/tool_pair_repair.go`（第 1 层：出站前消息层修复）

```go
type ToolPairRepairStats struct {
    RepairedToolCalls int      // 补了合成错误结果的悬空 call 数
    OrphanedResults   int      // 被隔离的孤儿 tool result 数
    OrphanedResultIDs []string // 诊断收据（对齐 CodeWhale 的 orphan_result_ids）
    RepairedCallIDs   []string
}

func RepairToolCallPairs(messages []message.Message) ([]message.Message, ToolPairRepairStats)
```

规则（对齐 CodeWhale `tool_history_repair.rs` + `chat.rs` + `anthropic.rs`）：
1. **收集 call 集合**：所有 assistant 消息的结构化字段（`ToolUses`/`ToolUse`）
   的 ID。
2. **隔离孤儿 result**：user 消息中 `ToolResult.ToolUseID` 不在 call 集合内的结果
   从消息中移除（`ToolResults` 过滤 / `ToolResult` 置空）；消息因此变空时保留
   消息本身（角色语义不变），只去掉孤儿结果。
3. **悬空 call 补合成结果**：assistant call 之后没有任何 user 消息含其结果时，
   在其**之后的下一条 user 消息**追加
   `ToolResult{ToolUseID: call.ID, Content: "[Paw repair] tool call was not executed.", IsError: true}`
   （对齐 CodeWhale 的 "Tool call interrupted by process exit" 合成错误结果）。
   末尾 call 且之后无 user 消息 → 不补（本轮可能仍在执行中）。
4. **幂等**：修复输出再次修复 stats 为零。
5. **零拷贝**：无问题时返回原切片（不复制）。

### 3.2 `StreamMessage` / `RunMessage` 入口挂接（对齐 CodeWhale `prepare_model_bound_request`）

在 `Client.StreamMessage` 与 `Client.RunMessage` 开头对 `messages` 调用
`RepairToolCallPairs`（所有协议路径统一覆盖，成本 O(n)）。返回的 stats 通过
`context` 或忽略（模型层不感知 UI；修复统计在 runner 层另行记录，见 3.4）。

### 3.3 Responses 线级兜底（第 3 层：wire 层修复）

新增 `internal/model/responses_repair.go`：

```go
func repairResponsesInputItems(items []json.RawMessage) ([]json.RawMessage, ToolPairRepairStats)
```

在 `buildResponsesRequest` 中 `buildResponsesInput` 之后、`validateResponsesInputItems`
之前调用：
1. 收集输入中所有 `function_call` 的 `call_id`（含 ProviderData 重放的 items）。
2. **隔离孤儿 `function_call_output`**（`call_id` 无对应 function_call）。
3. **悬空 `function_call` 补合成 `function_call_output`**：
   `{"type":"function_call_output","call_id":...,"output":"[Paw repair] tool call was not executed."}`
   （补在紧随该 call 之后，保持顺序；对齐 CodeWhale anthropic wire 修复的"按 tool_use 顺序前置占位"精神）。
4. 覆盖 ProviderData 重放与结构化字段不一致的崩溃场景（3.1 只修结构化，这里兜底最终 wire 形态）。

### 3.4 会话加载修复（第 2 层：对齐 CodeWhale `session_manager.rs`）

`internal/loop/history.go` 的 `LoadSession`：加载 `activeHistory` 后调用
`model.RepairToolCallPairs`，stats 非空时：
- 记录日志（`log.Printf("tool history repair: repaired=%d orphaned=%d ids=%v", ...)`）
- 用修复后的 history 设置 `runner.history`（setHistory 之前替换）
- 不影响 `result.Messages`（展示层保留原始？—— 不，为一致也返回修复后的；
  对齐 CodeWhale 在加载时即持久化修复：可调用 `commitHistory` 变体写回，
  但保持简单：先修内存 + 记录日志，写回留待下次 commitHistory 自然发生）

### 3.5 错误分类增强（第 0 层补齐）

`internal/model/http_error.go` 新增：

```go
// IsToolPairingInvalidRequest 判断是否为工具调用配对损坏导致的 400。
func (e *ProviderHTTPError) IsToolPairingInvalidRequest() bool
```

判定：`StatusCode == 400` && `Type == "invalid_request_error"` && Message 含
`"No tool call found"` / `"tool call"` / `"tool_call_id"` / `"tool output"` 特征。

`internal/loop` 新增 `error_hints.go`：

```go
func decorateToolPairError(err error) error
```

在 `runSingleTurnWithTiming` / `runModelTurn` 错误返回路径包装：
`%w（会话中的工具调用记录不完整，可能由中断导致；已自动修复，若仍复现建议 /compact 或 /clear 后继续）`。
提示仅附加在 `IsToolPairingInvalidRequest()` 命中时；`errors.Is/As` 不受影响。

### 3.6 测试

1. `internal/model/tool_pair_repair_test.go`：
   - 正常配对 → 零改动、stats 为零
   - 孤儿 result（无对应 call）→ 隔离、stats.OrphanedResults=1、ID 收据正确
   - 悬空 call（有 call 无结果，后跟 user 消息）→ 补合成错误结果
   - 末尾悬空 call（无后续 user）→ 不补
   - 幂等：修复两次 stats 为零
2. `internal/model/responses_repair_test.go`：
   - 孤儿 function_call_output 被隔离
   - 悬空 function_call 补合成 output（顺序正确）
   - ProviderData 重放含孤儿 output 的场景（覆盖 3.3 兜底）
3. `internal/model/http_error_test.go` 扩展：`IsToolPairingInvalidRequest` 对
   样例错误体（含 "No tool call found for tool output with call_id ..."）返回 true，
   对普通 400 返回 false。
4. loop 层：`LoadSession` 后 history 中孤儿 result 被移除（复用现有测试基建）。

## 4. 文件清单

| 文件 | 操作 |
|---|---|
| `internal/model/tool_pair_repair.go` | 新增（第 1 层核心） |
| `internal/model/responses_repair.go` | 新增（第 3 层 wire 兜底） |
| `internal/model/stream.go` | 修改：`StreamMessage` 入口挂 repair |
| `internal/model/client.go` | 修改：`RunMessage` 入口挂 repair |
| `internal/model/responses.go` | 修改：`buildResponsesRequest` 挂 wire 修复 |
| `internal/model/http_error.go` | 修改：`IsToolPairingInvalidRequest` |
| `internal/loop/history.go` | 修改：`LoadSession` 加载后修复 |
| `internal/loop/error_hints.go` | 新增：`decorateToolPairError` |
| `internal/loop/model_turn.go` 或 `turn.go` | 修改：错误路径挂提示装饰 |
| 测试文件 × 4 | 新增/扩展 |

## 5. 验证

1. `go build ./...`
2. `go test ./internal/model/ ./internal/loop/`
3. 新增测试先红后绿（先在未修复代码上验证测试能抓住问题）
4. `go vet ./...`
5. 不触碰既有 WIP（`internal/ui/bubble/*`、`internal/tool/exec/*` 等未提交改动）与
   上一轮选区修复的提交内容

## 6. 不做的事（明确边界）

- 不改 `isRetryableHTTPStatus`（400 不重试已正确，对齐 CodeWhale）
- 不改文本信封协议路径（无原生引用，无需修复）
- 不改 `context_compaction.go` / `tool_result_maintenance.go`（第 4 层已达标）
- 不引入可见 `[tool history repair]` 回执消息（paw 已有独立 `[Paw recovery]` 机制，
  避免双轨噪音；仅日志 + 统计）
