# Codex CLI、Pi 与 OpenCode 的 Agent 上下文去重与 Tool Loop 研究

> 调查主题：为什么 agent 在工具循环中会重复输出调查清单，以及成熟 coding agent 如何避免上下文重复累积。
>
> 调查版本：
> - OpenAI Codex：`openai/codex`，commit `7431f10d0d4b4ecf5df08a853b41859013b17e45`
> - Pi：`earendil-works/pi`，commit `a96fb984d8c8b065fc5d193309fc812a882adee0`
> - OpenCode：`anomalyco/opencode`，`dev` 分支，调查时 SHA `7fe993879f98aa17cecc70f70d3f40d6f0f11689`

## 摘要

三者都采用同一个基础原则：**tool loop 是同一个外层 turn/session 内的多次模型采样，而不是每次工具返回后重新创建一个全新的对话**。工具调用和工具结果作为结构化历史消息或 message part 写回 session；稳定的 system prompt、工具定义、环境信息和 skill catalog 与对话历史分离。

但三者的强度不同：

- **Pi**：采用最清晰的 `TurnState` 快照模型。同一 turn 内只生成一次 system prompt、资源和工具快照；完整 skill 内容按需作为一次 invocation 加入历史；普通请求仍可能把当前完整 messages 发给 provider。
- **OpenCode**：采用持久化 message/part、instruction claims、compaction marker、tail 保留和旧 tool output pruning。它允许每次 generation 重新组装 request-level system prompt，但不把这些内容逐轮追加进 session history。
- **Codex CLI**：实现最完整的增量上下文模型。除了 session history 和 turn/step 分层，还用 `WorldStateSnapshot` 与 `render_diff` 只注入发生变化的环境、权限、AGENTS.md、工具和 skill context，并在 compaction 时同步更新 history 与 world-state baseline。

对当前项目最重要的结论是：截图中的重复调查清单，主要不是“同一消息被 UI 重绘”，而是当前 loop 将完整 skill 文本作为每一轮 system prompt 重新提供，且没有记录“本外层 turn 已经输出调查计划”的结构化状态。成熟实现通常不会把完整 skill 正文作为每个 tool round 的新指令；它们要么只暴露 skill metadata，要么只在显式 invocation 时注入一次，要么将动态上下文按 snapshot/diff 管理。

## 1. 研究问题与方法

### RQ1：如何区分外层 turn 与内部 tool loop？

关注一次用户请求中，模型调用、工具执行、工具结果回写和后续模型调用的生命周期边界。

### RQ2：如何组织 system prompt、skills、工具结果和历史，避免重复注入和无界增长？

重点区分：

1. provider 请求中重新发送相同前缀；
2. session history 中重复追加相同内容；
3. UI 仅重复展示同一事件。

### RQ3：哪些设计可以安全迁移到当前 Go 项目？

优先考虑不改变 provider 协议、工具调用语义和已有 session 数据格式的渐进式方案。

### 方法

采用固定版本源码调查，而非仅依据项目 README 或二手文章。每个结论至少对应一个具体源码入口、数据结构或状态转换；无法由源码直接证明的地方标记为推断。

## 2. 统一概念模型

三者都可以抽象为：

```text
Session / Conversation
  └── Turn：一次用户任务
       ├── Turn/Step state snapshot
       ├── sampling request #1
       ├── assistant tool call
       ├── tool execution
       ├── structured tool result appended to history
       ├── sampling request #2
       └── final assistant response
```

正确的循环语义是：

```text
history := [prior messages]

for each sampling request in the same turn:
    context := build(request-level instructions, current history, current tools)
    output := model(context)
    history.append(output)
    if output contains tool calls:
        history.append(tool results)
        continue
    break
```

危险的实现则是：

```text
for each tool round:
    history += full system prompt
    history += full skill file
    history += full tool instructions
    history += tool result
    model(history)
```

后者会造成两个问题：

- history 真正重复膨胀；
- 模型每轮重新看到“开始调查前先输出调查清单”等初始化指令，可能把 tool result 误判为新的任务开始。

## 3. Codex CLI：World State Diff + History Baseline

### 3.1 Turn 与 sampling request 分离

主要入口：

- `codex-rs/core/src/session/turn.rs`
- `run_turn`
- `run_sampling_request`

`run_turn` 明确把一次用户任务包裹成 turn 内循环。模型每次返回 tool call 时，工具输出转换为 `ResponseInputItem`，写入 session history，再进行下一次 sampling；只有没有后续工具调用时 turn 才结束。

这避免了把每次 tool call 当成新的外层用户任务。

### 3.2 Session、TurnContext、StepContext 分层

Codex 将状态分为：

| 层级 | 作用 |
|---|---|
| Session | conversation history、稳定 base instructions、reference context、world-state baseline |
| TurnContext | 当前 turn 的 developer instructions、模型和协作模式设置、skill snapshot |
| StepContext | 当前 sampling request 的环境、MCP、工具路由、AGENTS.md 快照 |
| ModelClientSession | 当前 turn 的传输状态和连接复用，不跨 turn 直接复用 turn-specific sticky state |

这意味着动态上下文不是一个全局 `activeSkillContext` 字符串，而是与 turn/step 生命周期绑定的状态。

### 3.3 稳定 instructions 与 history 分离

Codex 的 Responses API 请求区分：

```rust
ResponsesApiRequest {
    instructions,
    input,
    tools,
    ...
}
```

稳定的 base/model instructions 由 session 初始化时解析；developer instructions 保存在 `TurnContext`。对话和工具结果进入 `input` history，而不会被复制为下一轮 user message 的一部分。

注意：provider 仍可能在每次请求看到 `instructions` 和当前完整 `input`。这里解决的是“history 反复追加”，不是保证网络上只传增量字节。

### 3.4 WorldStateSnapshot 与 render_diff

主要文件：

- `codex-rs/core/src/context/world_state/mod.rs`
- `codex-rs/core/src/context/world_state/agents_md.rs`
- `codex-rs/core/src/session/world_state.rs`

每个动态上下文 section 都实现 snapshot 和 render diff：

```rust
trait WorldStateSection {
    type Snapshot;

    fn snapshot(&self) -> Self::Snapshot;

    fn render_diff(
        &self,
        previous: PreviousSectionState<Self::Snapshot>,
    ) -> Option<Box<dyn ContextualUserFragment>>;
}
```

`AgentsMdState::render_diff` 的关键行为是：

```rust
if previous == current {
    return None;
}
```

内容未变化时，本 step 不再注入。内容变化时，注入 replacement notice 和新内容；内容被删除时注入 removal notice。

World State 覆盖的不是只有 AGENTS.md，还包括：

- model instructions；
- personality；
- permissions；
- environments；
- tools；
- plugins/apps instructions；
- collaboration mode；
- context window guidance；
- skills 等扩展 section。

初始 step 记录完整 baseline，后续 step 通过 `record_step_world_state_if_changed` 只记录变化。若精确 snapshot 不可用，还可以扫描 retained history 中的 fragment marker 进行保守判断。

### 3.5 Skills：metadata snapshot，显式 mention 后按需注入

Codex 的 skill 流程大致是：

```text
HostSkillsSnapshot
  -> collect_explicit_skill_mentions
  -> build_skill_injections
  -> record injection items into history once
```

不会因为发现了很多 skill 就把所有 `SKILL.md` 全文放入每个 sampling request。显式 skill mention 触发后，正文作为 ResponseItem 写入 history，后续 tool loop 复用已有 history。

此外，隐式 skill invocation 使用 set 去重，避免同一 skill 在同一生命周期反复触发副作用。

### 3.6 Compaction：替换 history，并同步 baseline

主要文件：

- `codex-rs/core/src/compact.rs`
- `run_auto_compact_task`
- `build_compacted_history`
- `replace_compacted_history`

Codex 的 compaction 不是简单地在旧 history 后面再追加一个 summary，而是构造新的 compacted history，通常保留：

```text
[部分必要 user messages]
[summary]
```

然后同步更新：

- compacted history；
- reference context item；
- world-state baseline；
- token usage。

如果只替换 history、不更新 baseline，下一轮会错误地认为所有环境上下文都没有注入过，反而导致整份 context 再次注入。Codex 明确避免了这个一致性问题。

### 3.7 Codex 的核心判断

Codex 解决重复问题的核心不是普通字符串缓存，而是：

```text
stable instructions
+ structured history
+ turn/step snapshots
+ world-state diff
+ compaction baseline replacement
```

## 4. Pi：Turn Snapshot + 结构化 AgentMessage

### 4.1 内外两层 loop

主要文件：

- `packages/agent/src/agent-loop.ts`
- `runAgentLoop`
- `runLoop`
- `streamAssistantResponse`

Pi 的 inner loop 处理：

```text
assistant response
  -> tool calls
  -> tool results
  -> next assistant response
```

outer loop 处理模型本来要结束后才到达的 follow-up messages。

工具结果通过 `createToolResultMessage` 构造为结构化 `ToolResultMessage`，随后：

```ts
currentContext.messages.push(result)
newMessages.push(result)
```

下一次请求从同一个 `currentContext.messages` 转换，而不是重新构造“刚才工具做了什么”的长字符串。

### 4.2 systemPrompt、messages、tools 三者分离

Pi 在模型调用边界才执行：

```ts
const llmMessages = await config.convertToLlm(messages)

const llmContext = {
    systemPrompt: context.systemPrompt,
    messages: llmMessages,
    tools: context.tools,
}
```

因此 system prompt 没有作为普通 history message 追加。

但必须准确说明：普通 provider API 仍会在每次模型请求中收到当前 `systemPrompt + messages + tools`。Pi 主要避免的是本地 history 的重复追加，以及 system prompt provider 在一个 turn 中被反复重新计算。

### 4.3 AgentHarness 的 TurnState 快照

主要文件：

- `packages/agent/src/harness/agent-harness.ts`
- `createTurnState`
- `createContext`
- `prepareNextTurn`

`createTurnState` 一次性解析：

- session context；
- resources；
- system prompt；
- model；
- thinking level；
- tool context；
- active tools；
- stream options；
- session id。

同一 turn 的多个 tool loop 使用同一份 system prompt 和资源快照。`prepareNextTurn` 在 save point 后才重新构造下一份 turn state。

这直接避免了当前项目中的一种风险：每次 tool round 都重新读取并拼接完整 `SKILL.md`。

### 4.4 Skills：系统只提供索引，全文按需 invocation

Pi 的 `formatSkillsForSystemPrompt` 只输出：

```xml
<available_skills>
  <skill>
    <name>...</name>
    <description>...</description>
    <location>...</location>
  </skill>
</available_skills>
```

完整 skill 文件通过 `formatSkillInvocation` 在显式 `AgentHarness.skill(name)` 时注入一次，并进入正常 history。

这会带来一个重要行为差异：模型通常不会在每个 tool round 再次看到完整 skill 正文，也不会因 skill 的初始化指令每轮重新触发调查计划。

### 4.5 Compaction 与 provider cache

Pi 提供：

- `shouldCompact`；
- 安全 message boundary 的 `findCutPoint`；
- compaction summary；
- `firstKeptEntryId` / `retainedTail`；
- summary + retained tail 的 context projection；
- provider `cacheRetention` hint。

需要注意，调查版本的 AgentHarness 文档明确指出自动 compaction 和 retry decision point 可能由上层 coding-agent 负责，而不是 AgentHarness 核心自动完成。因此不能把所有 Pi 组件都描述成默认自动压缩。

### 4.6 Pi 的核心判断

Pi 的主要优势是 API 边界清楚：

```text
TurnState snapshot
  + AgentContext { systemPrompt, messages, tools }
  + structured ToolResultMessage
  + save-point refresh
  + compaction projection
```

它没有 Codex 那么完整的 world-state diff，但非常适合当前项目优先落地。

## 5. OpenCode：持久化 Parts + Instruction Claims + Pruning

### 5.1 Session history 由结构化 message/part 组成

主要文件：

- `packages/opencode/src/session/message-v2.ts`
- `packages/opencode/src/session/session.ts`
- `packages/opencode/src/session/prompt.ts`

历史不是字符串数组，而是：

```text
Message
  ├── user
  │   └── TextPart / FilePart / CompactionPart / SubtaskPart
  └── assistant
      └── TextPart / ReasoningPart / ToolPart / StepPart
```

`MessageV2.toModelMessagesEffect` 把这些结构化 parts 投影成 provider message。tool call/result 通过 ToolPart 重放；旧 tool output 被标记后，可以投影成占位文本。

### 5.2 system prompt 是请求级数据，不写进 history

`SessionPrompt.prompt` 每次 generation 组装：

```text
SystemPrompt.provider(model)
SystemPrompt.environment(model)
SystemPrompt.skills(agent)
Instruction.system()
SystemPrompt.mcp(...)
MessageV2.toModelMessagesEffect(history)
```

system prompt 可能每次请求重新构造，但它没有变成 session history 中重复出现的 user message。这与当前项目“把完整 skill context 作为每轮 system prompt 重新提供”相似，但 OpenCode 的完整 skill 正文通常不在 system prompt 中。

### 5.3 Skills：清单进入 system，正文通过 skill tool 加载

`SystemPrompt.skills(agent)` 注入可用 skill 的名称、描述和位置，并指导模型在任务匹配时调用 skill tool。

skill 正文进入 tool result / tool part，具有明确调用 ID 和持久化生命周期。这样未匹配的 skill 不消耗全文 token，也不会在每个 tool round 重复塞入所有 skill 文件。

### 5.4 Project instructions 的两层去重

主要文件：

- `packages/opencode/src/session/instruction.ts`

第一层是路径发现：

```ts
// The first project-level match wins so we don't stack AGENTS.md/CLAUDE.md from every ancestor.
```

第二层是 message-scoped claims：

```ts
claims: Map<MessageID, Set<string>>
```

当 Read 工具读取多个文件时，同一个 assistant message 内，相同 instruction 文件只会附加一次。assistant message 生命周期结束时通过 `clear(messageID)` 清理 claims，避免全局状态永久污染。

这比简单的全局 `injected = true` 更准确：同一文件在不同 assistant generation 可能重新需要，但同一 generation 内不应重复。

### 5.5 Compaction 与旧工具输出裁剪

OpenCode 使用：

- compaction user marker；
- summary assistant message；
- `tail_start_id`；
- `filterCompacted()`；
- `PRUNE_PROTECT`；
- `PRUNE_MINIMUM`；
- `part.state.time.compacted`。

典型模型可见上下文是：

```text
[compaction marker]
[summary]
[retained recent tail]
[新消息]
```

而不是：

```text
[完整旧历史]
[summary]
[retained tail]
[新消息]
```

对于旧 tool output，OpenCode 保留 tool call 关系但清除大型 output，模型只看到：

```text
[Old tool result content cleared]
```

`skill` tool 被列为 protected tool，避免刚加载的 skill 正文过早被清除。

### 5.6 OpenCode 的核心判断

OpenCode 对当前项目最有参考价值的两个机制是：

1. **instruction claim key 包含 messageID 和 filepath**；
2. **保留完整可审计 session，但通过 compaction/tail/pruning 控制模型实际看到的 projection**。

## 6. 三方横向比较

| 维度 | Codex CLI | Pi | OpenCode |
|---|---|---|---|
| 外层 turn / 内层 tool loop | 明确区分 | 明确区分 inner/outer loop | SessionPrompt loop + processor |
| tool result 存储 | ResponseInputItem / history | ToolResultMessage | Assistant ToolPart |
| 稳定 system instructions | session / Responses instructions | AgentContext.systemPrompt | request-level system array |
| 同一 turn 的 system prompt 计算 | turn/step 状态 | TurnState snapshot | 每次 request 组装，但不写 history |
| skill 默认注入 | explicit mention 后 injection | metadata，全文显式 invocation | metadata，正文 skill tool |
| 动态环境上下文 | WorldState snapshot/diff | 主要依赖 turn snapshot | request-level environment |
| instruction 去重 | world-state snapshot + fragment matcher | 由 snapshot/应用层负责 | MessageID → filepath claims |
| compaction | history replacement + baseline update | summary + retained tail | marker + summary + tail pointer |
| 旧 tool output 裁剪 | compaction history 处理 | compaction projection | 显式 prune，保留 tool 关系 |
| provider 请求是否仍含完整历史 | 通常是当前 history projection | 通常是 | 通常是 projection |

### 关键相同点

1. 不把 system prompt 作为普通对话历史追加；
2. tool call/result 结构化持久化；
3. 外层 turn 内复用上下文状态；
4. skill 采用索引/metadata 与正文按需加载；
5. context 超限时做 projection/compaction，而不是无限追加 summary；
6. 允许 UI 展示完整历史，但给模型发送压缩后的子集。

### 关键差异

- Codex 强调**状态变化 diff**；
- Pi 强调**turn snapshot 与 API 边界**；
- OpenCode 强调**持久化 parts、message-scoped claims 和可裁剪 projection**。

## 7. 对当前 Go 项目的事实映射

根据当前项目的源码调查，相关链路大致是：

```text
runTurnWithTiming
  -> activateSkillContext(userInput.Content)
  -> runModelTurn
  -> buildModelMessages
  -> buildSystemPrompt
  -> model.StreamMessage
  -> tool execution
  -> append tool result to history
  -> next tool round
```

当前明显问题是：

1. `activateSkillContext` 将完整 skill 文件内容保存到 Runner 状态；
2. `buildSystemPrompt` 在每次模型请求中把完整 `activeSkillContext` 重新加入 system prompt；
3. skill 文本中“先输出调查提纲”的初始化要求没有 turn-level `planEmitted` 状态；
4. 工具结果作为历史消息累积，长 `Read/Grep/Bash` 输出会继续占用后续上下文；
5. 当前 history maintenance 有 compaction 能力，但尚未形成像 Codex/OpenCode 那样清晰的 history projection、tail pointer 和 baseline 一致性模型。

因此要区分两种成本：

### 7.1 真实历史重复

当前若 `buildSystemPrompt` 只将 skill 放在每次请求的 system prompt，而没有把它追加到 `history`，则它不一定在 session history 中重复存储；但每次 provider request 仍会重复发送同样的 system prompt token。

### 7.2 模型行为重复

即使本地 history 没有复制 skill 文本，模型每一轮都重新看到“开始调查时要输出调查清单”的规则，也可能重复生成调查清单。这正是截图最符合的解释。

### 7.3 UI 展示重复

现有证据不支持 `assistantDeltaMsg` 把同一 delta 无限追加到多个 UI 条目。UI 主要把每次 assistant generation 和 tool result 分别展示；因此多段调查清单很可能是多次真实 assistant generation 的输出。

## 8. 推荐迁移方案

按风险和收益排序，建议分四层实现。

### P0：建立 turn/step 生命周期状态

新增类似：

```go
type TurnState struct {
    ID                 string
    Skill              *SkillSnapshot
    PlanEmitted        bool
    InitialContextSent bool
    ToolRounds         int
}
```

语义：

- `TurnState` 在一次外层用户请求开始时创建；
- tool loop 只复用该 state；
- 外层 turn 结束时销毁；
- 新用户请求创建新 state。

至少需要：

```go
PlanEmitted bool
```

第一次模型请求可以收到完整 skill 约束；后续请求只收到短提示：

```text
The investigation plan has already been emitted for this turn. Continue from the latest tool result; do not restart the plan.
```

这比简单全局布尔值安全，因为不同用户 turn 允许重新开始新的调查计划。

### P1：skill metadata 与正文分离

推荐模型：

```text
system prompt：
  available skills: name / description / location

显式 skill invocation：
  一次性加载完整 SKILL.md

后续 tool loop：
  通过已有 history 或短状态引用，不重复附加全文
```

若当前用户输入显式引用了 skill，可以兼容现有行为，但将完整正文只注入首个 sampling request，后续轮次改为引用或不再发送。

### P1：将稳定 system prompt、动态 context、conversation history 分层

建议不要继续把所有内容都放在 `buildSystemPrompt` 的一个字符串中，至少抽象成：

```go
type ModelContext struct {
    StableInstructions string
    DynamicFragments  []ContextFragment
    History           []message.Message
    Tools             []model.ToolDefinition
}
```

其中：

- `StableInstructions`：session/model 级；
- `DynamicFragments`：turn/step 级，可按 hash 或 snapshot diff；
- `History`：user/assistant/tool/tool-result；
- `Tools`：当前 step 的工具定义。

### P1：工具结果做上下文 projection

保留 UI 和审计所需的完整结果，但给模型提供 projection：

```go
type ToolResultProjection struct {
    FullText       string // session/UI/archive
    ModelText      string // model-visible bounded text
    Summary        string
    Truncated      bool
    ArtifactRef    string
}
```

例如：

- `go test ./...`：保留状态、失败摘要和关键尾部；
- 大型 `Read`：保留文件路径、行范围、摘要和必要片段；
- 大型 `Grep`：保留匹配计数、前 N 条和 artifact 引用；
- 大型 `Bash`：保留 exit code、stderr、尾部输出。

### P2：引入 snapshot/diff 去重

可以先只覆盖最容易重复的 fragment：

```go
type ContextSnapshot struct {
    SkillID       string
    SkillHash     string
    ProjectRoot   string
    WorkingDir    string
    ToolsHash     string
    Permissions   string
}
```

每个 tool round 计算当前 snapshot，与上一个已发送 snapshot 比较；不变时不生成 fragment。后续再扩展为 Codex 式 section map：

```go
map[string]json.RawMessage
```

不要只用一个全局 `injected bool`，因为：

- compaction 后需要重置 baseline；
- cwd、工具、权限可能变化；
- 不同 turn 的相同 skill 可能需要重新注入。

### P2：compaction 后同步更新 baseline

任何 history compaction 都必须同时更新：

```text
compacted history
reference context / skill baseline
usage estimate
```

否则下一轮会把已存在的 context 错误地再次当成新内容注入。

## 9. 建议的验证测试

### 9.1 skill 不重复

给模型 loop 注入一个包含唯一标记的 skill：

```text
SKILL_MARKER_123
```

执行 3 个 tool round，断言：

- 首轮完整 skill 文本最多出现一次；
- 后续请求不再出现完整正文；
- 后续只出现短状态提示或无额外正文；
- 新外层 turn 可以重新出现一次。

### 9.2 调查计划不重复

模拟模型在首轮输出计划并调用工具，后续返回 tool result；断言下一轮输入包含“继续当前计划”语义，但不再次要求输出新的调查清单。

### 9.3 tool result 结构完整

断言每个 tool call 只生成一个结构化 tool result，下一轮不会额外拼接同一结果的字符串副本。

### 9.4 context projection

构造超过阈值的 Bash/Read/Grep 结果，断言：

- UI/session 保留完整结果；
- model-visible result 被限制；
- 截断标记和 artifact ref 可用。

### 9.5 compaction baseline 一致性

执行：

```text
首轮完整 context
-> tool loop
-> compaction
-> 下一轮
```

断言 compaction 后不重复注入未变化的 skill/environment fragment。

### 9.6 三类生命周期隔离

测试：

- 同一 turn 的多个 tool round 共享 `TurnState`；
- 新 turn 不复用 `PlanEmitted`；
- session history 可以跨 turn 保留；
- turn-specific transport/tool state 不泄漏到下一 turn。

## 10. 结论：逐条回答研究问题

### RQ1：三者如何区分 turn 与 tool loop？

三者都把 tool loop 放在外层 turn/session 内部。Pi 的 `runLoop`、Codex 的 `run_turn`、OpenCode 的 `SessionPrompt.loop` 都表现为：assistant tool call → tool result → 下一次 sampling，而不是创建新的初始对话。外层 turn 负责生命周期、状态快照和结束条件；内部 loop 只推进结构化历史。

### RQ2：如何组织历史、工具结果和 skill/context？

共同答案是：

1. tool result 作为结构化 history entry；
2. system prompt/tools/history 分离；
3. skill 默认只暴露 metadata，正文按需加载或显式注入；
4. instruction 使用 snapshot 或 message-scoped claims 去重；
5. context 超限时用 summary + retained tail/pruning 替换模型可见 projection；
6. provider 请求可能仍然携带当前完整 projection，但本地 history 不应重复追加稳定指令。

Codex 在动态 context diff 上最强；Pi 在 turn snapshot 上最直接；OpenCode 在 instruction claims、tool output pruning 和可审计 history projection 上最完整。

### RQ3：当前项目应迁移什么？

推荐顺序是：

1. 先实现外层 `TurnState` 和 `PlanEmitted`，停止同一 tool loop 反复执行 skill 初始化要求；
2. 将完整 skill 正文从“每次 build system prompt”改为“首轮或显式 invocation 一次”；
3. 把 skill metadata 与 skill 正文分离；
4. 将工具结果拆分为完整审计内容与模型可见 projection；
5. 再引入 context snapshot/diff；
6. 最后完善 compaction 与 baseline 的一致性。

不建议第一步就复制 Codex 的完整 WorldState 架构。当前项目应先采用 Pi 的 TurnState 边界和 OpenCode 的 message-scoped dedup，再逐步增加 Codex 式 section diff。

## 11. 限制与未确认事项

- 三个项目均在快速迭代，源码 API 和分支结构会变化；本文结论绑定调查时的 commit/branch。
- “provider 是否利用 prompt cache”取决于 provider 和 transport，不能仅凭客户端发送结构推断实际计费或缓存命中。
- 当前项目没有在这次调查中修改业务代码，也没有基于真实模型 token usage 做 A/B benchmark；迁移收益应通过请求 payload、input token usage 和重复 marker 测试验证。
- OpenCode 的 system prompt 每次 generation 重组是源码事实；它是否被 provider 侧缓存是独立问题。

## 12. 参考源码

### Codex CLI

- [turn.rs](https://raw.githubusercontent.com/openai/codex/7431f10d0d4b4ecf5df08a853b41859013b17e45/codex-rs/core/src/session/turn.rs)
- [world_state/mod.rs](https://raw.githubusercontent.com/openai/codex/7431f10d0d4b4ecf5df08a853b41859013b17e45/codex-rs/core/src/context/world_state/mod.rs)
- [world_state/agents_md.rs](https://raw.githubusercontent.com/openai/codex/7431f10d0d4b4ecf5df08a853b41859013b17e45/codex-rs/core/src/context/world_state/agents_md.rs)
- [Codex repository](https://github.com/openai/codex/tree/7431f10d0d4b4ecf5df08a853b41859013b17e45)

### Pi

- [agent-loop.ts](https://raw.githubusercontent.com/earendil-works/pi/a96fb984d8c8b065fc5d193309fc812a882adee0/packages/agent/src/agent-loop.ts)
- [agent-harness.ts](https://raw.githubusercontent.com/earendil-works/pi/a96fb984d8c8b065fc5d193309fc812a882adee0/packages/agent/src/harness/agent-harness.ts)
- [Pi repository](https://github.com/earendil-works/pi/tree/a96fb984d8c8b065fc5d193309fc812a882adee0)

### OpenCode

- [prompt.ts](https://raw.githubusercontent.com/anomalyco/opencode/7fe993879f98aa17cecc70f70d3f40d6f0f11689/packages/opencode/src/session/prompt.ts)
- [instruction.ts](https://raw.githubusercontent.com/anomalyco/opencode/7fe993879f98aa17cecc70f70d3f40d6f0f11689/packages/opencode/src/session/instruction.ts)
- [compaction.ts](https://raw.githubusercontent.com/anomalyco/opencode/7fe993879f98aa17cecc70f70d3f40d6f0f11689/packages/opencode/src/session/compaction.ts)
- [OpenCode repository](https://github.com/anomalyco/opencode/tree/7fe993879f98aa17cecc70f70d3f40d6f0f11689)
