# `internal/runtime/loop`

文件: [runner.go](../../internal/runtime/loop/engine.go)

#### `type ModelStreamer interface`

方法:
- `StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error)`

作用:
- 抽象模型流式能力

这让 `Engine` 不直接依赖具体 provider 类型。

#### `type HistoryStore interface`

方法:
- `LoadResolvedHistory(ctx, sessionID) ([]message.Message, error)`
- `Append(ctx, sessionID, msgs ...message.Message) error`

作用:
- 抽象历史加载/追加

#### `type Engine struct`

核心依赖是 `ModelStreamer`、`tool.Registry`、`HistoryStore` 与 UI / display bus；历史、usage、上下文维护、工具执行和 StreamMA 各由内部协作者管理。字段以 [Engine 定义](../../internal/runtime/loop/engine.go) 为准，不在这里维护另一份易过期的私有字段清单。

职责:
- 驱动单次 agent turn
- 在成功 turn 后维护内存中的多轮历史
- 用模型服务端 usage 字段更新 context 计量，不做本地 token 估算
- 通过 Turn Journal 增量持久化每轮消息

#### `NewEngine(model, output, registry, store, sessionID) *Engine`

职责:
- 创建调度器（空 instruction root）

#### `NewEngineWithInstructionRoot(model, output, registry, store, sessionID, instructionRoot) *Engine`

职责:
- 创建带项目指令根目录的调度器
- 构造 `PromptBuilder(NewInstructionManager(root))` 与 skill registry
- 默认开启 StreamMA

#### `RunTurn(ctx, input) (message.Message, error)`

职责:
- 执行一次完整 turn

当前逻辑:
1. 验证与初始化：检查 runner 初始化状态；持久化输入中的图片附件；按输入中的 `$skill` 或 `[$skill](.../SKILL.md)` 解析并加载当前 turn 的 skill 指令；首次运行时从 store 加载历史（含 Turn Journal snapshot 与 recovery 状态）
2. 构建本轮历史副本：复制已提交历史，插入未注入的 supplements，再追加当前用户输入（失败时不污染已提交历史）
3. 多轮工具循环（最多 500 轮）：
   - 每轮开始时检查是否有新注入的 supplements 并追加
   - 首轮前检查是否需要自动上下文压缩（`maybeCompactHistory`），压缩时保留最近消息与用户约束原文
   - 调用 `runModelTurn`：构造 system prompt + 历史消息，通过 `model.StreamMessage` 发送给 LLM 并消费流式事件
   - Responses 已输出后发生暂时性断流时，保存 display-only partial 并自动续接，单次连续故障最多 10 次；成功完成响应后清零，下一次故障从 1/10 开始。续接不消耗工具轮次额度，等待可取消；新请求沿用已完成工具历史及计划，并只在请求副本中附加临时恢复指令
   - 若返回消息不含 ToolUse，调用 `commitHistory` 持久化并返回该 assistant 消息
   - 若含 ToolUse，调用 `runToolCall` 执行工具（连续并发安全的调用会并行批处理），将 tool_result 追加到历史副本，继续下一轮
4. 超限保护：超过 500 轮返回错误

#### 任务续行与进展

`TaskOrchestrator` 在单轮完成后，把完整 `TurnExecution` 交给 `CompletionEvaluator`。普通对话的完成门依据未完成 todo 决定是否续行，并保留原计划、已读技能和调查证据；工具轮次不代表新任务，也不需要重新输出调查清单。

无进展计数以 todo 项变化或本轮成功的非 `update_todo` 工具结果为依据。仅改写回答措辞、重复提交相同 todo、失败的工具调用都不会清零计数。成功工具只证明发生了执行，不保证任务完成；连续无进展会暂停，所有自动续行仍受总预算约束。Goal 和 Plan 保持各自的完成判据。

启动目录可能不是任务目标仓库。系统指令要求先通过明确路径和小范围查找确认目标，再读取目标仓库约定和记忆，并使用绝对路径或显式命令工作目录；不会自动推断或切换工作目录。这些提示约束不保证模型完全遵从。

#### `ResetHistory()`

职责:
- 清空内存历史
- 清空最近一次 usage 计量

当前用途:
- REPL 的 `/clear`

#### `runModelTurn(ctx, history) (message.Message, error)`

职责:
- 从 registry 取原生工具定义，构造模型消息并消费流式事件

#### `buildModelMessages(history) []message.Message`

职责:
- 把内存中的 `history` 转成喂给模型的消息列表

#### `buildSystemPrompt() string`

职责:
- 生成 system prompt
- 注入工具说明、输入 schema、当前 turn 的 skill 指令和额外 system supplement

#### `renderMessageForModel(msg) message.Message`

职责:
- 把内部消息编码成模型输入消息

#### `consumeStream(ctx, events) (message.Message, error)`

职责:
- 消费模型流式事件

#### `parseAssistantMessage(content) message.Message`

职责:
- 判断模型输出是普通文本还是 `tool_use` JSON

#### `runToolCall(ctx, call) (message.ToolResult, error)`

模型调用结束后、工具执行前，损坏的文本工具协议和原生工具 JSON 参数分别以 `ToolProtocolError` / `model.ToolArgumentsError` 分类；整批拒绝执行，避免合法与损坏调用混在一起时出现部分副作用。先采用参数解码器已有的确定性修复；仍不合法时由正常及恢复 turn 路径追加一次性纠正消息，要求模型重新生成尚未执行的原生调用，保留历史中的已完成工具结果。每次连续故障最多自动重试 10 次，成功模型响应后清零。纠正消息不写持久历史，失败/重试/恢复/耗尽写入结构化 `tool_protocol_recovery` trace 和日志，交互模式落入 `tui.log`，不记录原始参数，不插入错误正文或终止显示事件。用户取消立即结束；权限拒绝不绕过，执行状态不明的生命周期错误不自动重放。

普通工具执行失败沿用 `ToolResult.IsError` 返回并持久化，下一次模型请求可根据具体错误修正参数或换工具；不在执行器内盲目重复可能已经产生副作用的调用。

职责:
- 向 UI 发工具调用事件
- 执行工具
- 向 UI 发工具结果事件
- 返回 `ToolResult` 消息

#### `executeToolCall(ctx, call) message.Message`

职责:
- 只执行工具，不做 UI 输出

#### `prepareFileMutation(call) *fileMutationCapture`

职责:
- 若 UI 消费文件变更快照且工具实现 `FileMutationTool`，在工具执行前捕获目标路径与磁盘内容

#### `maybeCompactHistory(ctx, history) ([]message.Message, *ContextCompactionResult, error)`

职责:
- 上下文接近上限时自动压缩历史（非阻塞、失败可跳过）
- 详情见 `internal/runtime/loop/context_compaction.go`

#### 指令管理

文件: [instruction_manager.go](../../internal/runtime/loop/instruction_manager.go)

##### `type InstructionManager struct`

职责:
- 从工作区向上查找 `AGENTS.md`，缓存其内容作为“项目指令”
- 内容作为 inert text 注入 system prompt，不执行

方法:
- `NewInstructionManager(root)`
- `ProjectInstructions() string`

#### 提示词构建

文件: [prompt_builder.go](../../internal/runtime/loop/prompt_builder.go)

##### `type PromptBuilder struct`

职责:
- 以稳定顺序组装 system prompt：默认指令 → AGENTS.md 项目指令 → 工具说明 → 工具调用格式约定

方法:
- `Build(toolDescriptions []string) string`

#### 上下文压缩

文件: [context_compaction.go](../../internal/runtime/loop/context_compaction.go)

##### `type ContextCompactionResult struct`

字段:
- `BeforeMessages`
- `AfterMessages`
- `FoldedMessages`
- `Summary`

职责:
- 描述一次压缩前后的消息数量与摘要

##### 行为

- 压缩由模型生成摘要，保留路径、标识符、版本、数字、用户约束、编辑、命令结果与未完成工作原文
- 压缩只影响模型上下文，完整 journal 始终保留
- 超时 90 秒，失败时跳过并在 UI 提示

#### Turn Journal

文件: [journal.go](../../internal/storage/session/journal.go)

##### `type TurnJournal interface`

方法:
- `BeginTurn(ctx, sessionID, turnID, messages ...) error`
- `AppendAssistant(ctx, sessionID, turnID, msg) error`
- `AppendToolResult(ctx, sessionID, turnID, callIndex, result) error`
- `CompleteTurn(ctx, sessionID, turnID) error`
- `FailTurn(ctx, sessionID, turnID, err) error`
- `LoadSnapshot(ctx, sessionID) (SessionSnapshot, error)`

职责:
- 增量持久化一轮的每条消息与工具结果
- `SessionSnapshot` 区分 UI 展示消息与喂给模型的 safe history
- `RecoveryState` 记录未完成 turn 的已完成工具结果与丢弃的工具调用，重启后可恢复
