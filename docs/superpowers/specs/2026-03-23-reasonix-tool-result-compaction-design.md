# DeepSeek-Reasonix Tool Result 上下文压缩对齐设计

## 1. 背景

本项目当前在触发上下文压缩后，会将待折叠区域中的 Tool Result 完整渲染到 compaction transcript，再调用模型生成自然语言摘要。该方案实现统一，但在工具密集会话中存在以下问题：

- 超大工具输出会再次完整进入摘要请求；
- 无法仅通过机械清理工具结果释放上下文；
- 摘要后旧 Tool Call/Result 的结构化关系退出活动上下文；
- 错误结果和用户要求长期保留的内容依赖模型摘要质量；
- 从 append-only journal 恢复会话时，完整旧工具输出会重新进入活动上下文。

本设计对齐 `esengine/DeepSeek-Reasonix` 的 Tool Result 处理方式：先按上下文压力执行确定性的 snip/prune，仍不足时才进行语义摘要压缩。

## 2. 目标

将当前流程：

```text
完整 Tool Result
→ 直接送入 summarizer
→ 整段历史替换为自然语言摘要
```

升级为分层维护管线：

```text
50%：仅提示，保持 prefix cache
60%：snip 旧的大型 Tool Result
80%：prune；仍超限才执行 summary compaction
90%：强制 summary compaction
```

同时满足以下约束：

1. append-only session journal 始终保存完整事实；
2. snip/prune 不破坏 Tool Call/Result 配对；
3. 被移除的原文先写入独立 JSONL archive；
4. cold resume 按恢复后的上下文压力执行同一管线；
5. 默认保留错误结果和用户显式标记的内容；
6. 工具可通过可选接口声明最适合自己的首尾裁剪策略；
7. 自动和手动摘要失败时均可在归档成功后机械折叠，确保释放上下文；
8. 避免连续逐轮触发摘要压缩。

## 3. 非目标

本次不包含：

- 修改 `message.Message` 或 `message.ToolResult` 的持久化 JSON 结构；
- 改写或压缩 append-only session journal；
- 修改 provider 的 Tool Call/Result 消息协议；
- 改变 session fork 的序列语义；
- 新增 CLI flag 或 TUI 配置入口；
- 为所有动态 MCP 工具强制实现裁剪接口；
- 将完整上下文管理抽象成可插拔的多策略框架。

## 4. 总体架构

采用独立的“上下文维护管线”，将压力判断、工具结果维护、归档和摘要折叠拆分为职责明确的组件：

```text
活动 history / 恢复后的 ActiveHistory
            │
            ▼
     Context Pressure
  50% notice / 60% snip
  80% prune+compact / 90% force
            │
            ▼
 Tool Result Maintenance
  ├─ 保护近期 tail
  ├─ 保留 error / [keep] 调用组
  ├─ 按 SnipHint 截取首尾
  └─ 原文写入独立 JSONL archive
            │
            ▼
   Summary Compaction
  prune 后仍超阈值才调用模型
            │
            ▼
      模型请求 history
```

### 4.1 `context_pressure.go`

负责：

- token 压力估算；
- `50/60/80/90%` 阶段选择；
- soft notice；
- prune 后重新估算；
- fold 经济性检查；
- 连续摘要与 stuck 防循环状态。

### 4.2 `tool_result_maintenance.go`

负责：

- stale Tool Result 识别；
- recent tail 与工具调用组保护；
- snip、prune 及幂等 marker 解析；
- `KeepErrors` 与 `KeepUserMarked`；
- 根据注册工具解析 `SnipHint`。

### 4.3 `compaction_archive.go`

负责：

- 按 session 写入时间戳 JSONL archive；
- 写入并同步成功后才允许改写活动 history；
- 内容哈希索引与跨 cold-resume 归档复用；
- session 路径安全。

### 4.4 `context_compaction.go`

保留并增强：

- compaction region 规划；
- pinned prefix 与固定 token tail；
- keep/fold 分区；
- 摘要调用；
- mechanical fallback；
- summary usage 统计。

### 4.5 `internal/tool/tool.go`

新增可选能力，不修改现有 `Tool` 基础接口：

```go
type SnipHint struct {
    Head      int
    Tail      int
    HeadChars int
    TailChars int
}

type SnipHinter interface {
    SnipHint() SnipHint
}

type ReadOnlyTool interface {
    ReadOnly() bool
}
```

## 5. 配置模型

新增独立上下文维护配置：

```go
type ContextMaintenanceConfig struct {
    SoftCompactRatio    float64
    ToolResultSnipRatio float64
    CompactRatio        float64
    CompactForceRatio   float64
    CompactTargetRatio  float64
    TailTokens          int
    MinToolResultBytes  int
    KeepErrors          bool
    KeepUserMarked      bool
    ArchiveEnabled      bool
}
```

默认值：

```text
soft_compact_ratio       = 0.50
tool_result_snip_ratio   = 0.60
compact_ratio            = 0.80
compact_force_ratio      = 0.90
compact_target_ratio     = 0.50
tail_tokens              = 16384
min_tool_result_bytes    = 1024
keep_errors              = true
keep_user_marked          = true
archive_enabled           = true
```

配置必须满足：

```text
0 < soft <= snip <= compact <= force < 1
0 < target < compact
tail_tokens > 0
min_tool_result_bytes > 0
```

无效配置在启动或加载阶段返回明确错误，不静默纠正。本期只支持配置文件，不新增 CLI 和 TUI 设置入口。

## 6. 压力计算与阶段行为

### 6.1 压力值来源

每次首轮模型请求前计算：

```go
estimatedTokens := estimateMessageTokens(history)
```

当前进程已有最近一次真实 prompt usage 时，在 history 尚未被维护改写前，可以取估算值与真实值中的较大者作为初始压力：

```go
pressureTokens := max(estimatedTokens, lastPromptTokens)
```

一旦 history 被 snip、prune 或 summary 改写，旧 usage 不再代表当前投影，后续判断必须重新估算改写后的 history，不能仅从旧 usage 中扣除估算节省量。

cold resume 没有可信的当前投影 usage，直接使用恢复后 `ActiveHistory` 的 token 估算值。

### 6.2 `< 50%`

- 不处理 history；
- 清除连续压缩的 stuck 状态；
- 允许下一次达到 soft threshold 时重新提示。

### 6.3 `50% ～ 60%`

仅发出一次通知：

```text
Context is getting large; preserving cache until cleanup is needed.
```

不改 history，维持 cache-first、append-only 的 prompt prefix。

### 6.4 `60% ～ 80%`

执行 snip：

- 仅处理 protected recent tail 之前的旧大型 Tool Result；
- 原文先归档；
- 按工具 `SnipHint` 保留首尾；
- 不调用 summarizer；
- 使用维护后的 history 发起正常模型请求。

### 6.5 `80% ～ 90%`

先执行 prune：

- 已 snip 的结果可升级为 prune；
- 未 snip 的旧大型结果可直接 prune；
- 原文未归档时先归档；
- 重算维护后的 history token。

若维护后已低于 80%，跳过 summarizer。若仍在 80% 以上，仅当 foldable region 估算不少于约 400 tokens 时调用 summarizer，避免为了过小收益增加模型调用与延迟。

### 6.6 `>= 90%`

先 prune，再强制 summary compaction：

- 跳过约 400-token 的经济性检查；
- 即使可折叠区域较小，也尽力创建 headroom；
- summarizer 失败时，在归档成功的前提下使用 mechanical fold。

## 7. Recent Tail 与调用组边界

recent tail 使用固定 token 预算：

```text
min(16384, contextWindow × 0.50)
```

并满足：

- 至少保留最近两条消息；
- tail 不从含 Tool Result 的 user 消息开始；
- assistant Tool Call 与其结果不能被边界拆分；
- 多工具调用组整体进入 tail 或整体留在旧区域；
- 当前正在执行的 turn 不参与维护和摘要。

固定预算用于避免大窗口保留过多旧工具输出，以及压缩后的保留 tail 本身仍高于阈值，导致下一轮立即重复压缩。

## 8. Tool Result 维护规则

### 8.1 数据形态

本项目的 Tool Result 可能以单个或批量形式存在于一条 `RoleUser` 消息中：

```go
ToolResult  *message.ToolResult
ToolResults []message.ToolResult
```

维护时不删除消息，也不改变：

- `ToolUseID`；
- `IsError`；
- Tool Call 所在 assistant 消息；
- 同一批次结果顺序；
- 单结果与多结果的消息形态。

仅替换符合条件的 `ToolResult.Content`。

### 8.2 可维护条件

Tool Result 必须同时满足：

- 位于 protected recent tail 之前；
- 不属于当前活动 turn；
- 原始正文至少 `MinToolResultBytes`，默认 1024 字节；
- 尚未进入目标维护状态；
- 未被 keep policy 保护。

### 8.3 Snip 格式

多行输出：

```text
[snipped tool result — Bash, 524288 bytes archived to <path>; showing first 40 lines and last 40 lines]
<头部内容>
[... 12340 lines omitted ...]
<尾部内容>
```

单行大输出：

```text
[snipped tool result — Read, 524288 bytes archived to <path>; single large line truncated]
<头部字符>
[... 500000 bytes omitted ...]
<尾部字符>
```

要求：

- UTF-8 安全，不从多字节字符中间截断；
- 已 snip 结果不重复 snip；
- marker 记录原始字节数和 archive 路径；
- 同一次维护可将多个原始结果写入同一个 archive 文件。

### 8.4 Prune 格式

```text
[elided tool result — Bash, 524288 bytes archived to <path>; re-run the tool if the data is needed again]
```

规则：

- 原始大结果可直接 prune；
- 已 snip 结果可升级为 prune；
- 升级时复用首次 snip 的原始字节数和 archive 路径；
- 已 prune 结果幂等跳过；
- archive 失败时不改写 history。

### 8.5 损坏 marker

若 marker 看似存在但无法合法解析：

- 将其作为普通文本处理；
- 不信任或复用其中的 archive 路径；
- 正文达到阈值时允许重新归档并生成合法 marker；
- 单条损坏不能导致维护过程 panic。

## 9. 工具级 Snip 策略

解析优先级：

```text
工具实现 SnipHinter
→ ReadOnlyTool 分类默认值
→ 未知工具按有副作用处理
```

默认策略：

| 工具类别 | 多行输出 | 单行输出 |
|---|---:|---:|
| 只读工具 | 前 80 行、后 12 行 | 前 10000、后 2000 字符 |
| 有副作用或未知 | 前 40 行、后 40 行 | 前 8000、后 8000 字符 |

未知工具按有副作用处理，以降低丢失末尾错误、退出状态或最终结论的风险。

`SnipHint` 的四个字段必须非负，且行策略和字符策略至少各保留一端。非法 hint 回退到类别默认值，不中断上下文维护。

## 10. Keep Policy

默认同时启用：

```text
KeepErrors
KeepUserMarked
```

### 10.1 错误结果

以下 Tool Result 确定性保留：

- `ToolResult.IsError == true`；
- 去除前导空白并忽略大小写后，正文以 `error:` 开头；
- 去除前导空白并忽略大小写后，正文以 `blocked:` 开头。

普通 Bash 非零退出的 `[exit status: ...]` 不触发永久保留；该结果仍允许 snip，并依靠 Bash 的均衡首尾策略保留末尾错误信息。

### 10.2 用户显式标记

支持用户消息以下列标记开头：

```text
[[keep]]
[keep]
<keep>
<!-- keep -->
```

匹配忽略前导空白与大小写。被标记消息逐字保留，不进入摘要。

### 10.3 工具调用组完整性

若某个 Tool Result 因 `KeepErrors` 被保护，必须同时保护：

1. 发起它的 assistant Tool Call 消息；
2. 同一 assistant 消息内的全部 Tool Calls；
3. 紧随其后的对应 Tool Results 消息；
4. 同批次兄弟 Tool Results。

这样可确保模型 API 所要求的多工具调用结果集合保持完整。

### 10.4 保留范围

Keep policy 只作用于最近一次 compaction summary 之后的消息。更早的已保留消息在后续 pass 中允许进入 fold，防止保留区无限增长。既有 compaction summary 本身始终原样保留，不再次摘要。

## 11. JSONL Archive

### 11.1 目录布局

按 session 隔离：

```text
.paw/sessions/<safe-session-id>/compactions/
  20260323-142530.123-snip.jsonl
  20260323-142601.456-prune.jsonl
  20260323-142700.789-fold.jsonl
  index.jsonl
```

### 11.2 记录结构

每行保存一个被处理的完整原始消息及维护元数据：

```json
{
  "operation": "snip",
  "session_id": "session-123",
  "message_index": 8,
  "tool_result_index": 1,
  "tool_use_id": "call-456",
  "tool_name": "Bash",
  "original_bytes": 524288,
  "message": {
    "role": "user",
    "tool_results": []
  },
  "created_at": "2026-03-23T14:25:30.123Z"
}
```

`message_index` 和 `tool_result_index` 仅用于排查当时活动投影中的位置；archive 保存完整原始 message，以保留调用组相关上下文。

### 11.3 原子性约束

- 必须先成功创建目录、写入记录、关闭并 `Sync` 文件；
- 只有 archive 成功后才能改写内存 history；
- 任一步骤失败时，本次待维护内容保持原样；
- 多条结果作为一次 pass 归档时，要么完整成功后统一改写，要么不改写。

### 11.4 去重与复用

归档键：

```text
session ID + tool use ID + SHA-256(original content)
```

`index.jsonl` 采用 append-only 记录键到 archive 路径的映射。规则：

- 原结果第一次 snip：归档；
- 原结果直接 prune：归档；
- snip 升级 prune：复用 marker 中的 archive，不重复归档；
- cold resume 再次遇到 journal 原文：查索引并复用已有 archive；
- 索引缺失或损坏：允许重新归档，不影响正确性。

### 11.5 路径安全

- session ID 必须转换为安全目录名；
- 归档目标必须校验仍位于当前 session 的 `compactions` 目录内；
- `../`、绝对路径和路径分隔符不能造成目录逃逸；
- marker 中解析出的历史路径仅在合法、受控目录内时可复用。

## 12. Cold Resume

恢复流程：

```text
从 append-only journal 恢复完整 ActiveHistory
→ 估算当前上下文压力
→ 执行同一套 50/60/80/90 管线
→ 得到当前 Runner 的内存投影
→ 加入本轮用户消息并发起模型请求
```

行为约束：

- 不按时间间隔判断 cold resume；
- 压力低于 60% 时保持完整恢复历史；
- 达到 60% 时可 snip；
- 达到 80% 时可 prune，并在必要时摘要；
- journal、导出和审计始终保留原始 Tool Result；
- 新进程可通过 archive 哈希索引复用归档；
- 未完成 turn 的 recovery 数据不被维护管线破坏；
- orphaned tool-call group 继续由现有 `LoadSnapshot` 安全投影规则处理。

cold-resume 维护与运行中维护必须复用同一实现，不维护两套阈值或裁剪逻辑。

## 13. Summary Compaction

### 13.1 Fold 分区

`partitionCompactionRegion` 扩展后，以下内容原样保留：

- 小型普通 user 消息；
- 既有 `<compaction-summary>`；
- `[keep]` 等用户标记消息；
- `KeepErrors` 保护的完整工具调用组；
- local-only 或其他不可发送给 summarizer 的本地内容。

以下内容可进入 fold：

- 其他 assistant 工作；
- 已 snip/prune 的旧工具调用组；
- 未受保护的普通工具结果；
- 超过 pinned user budget 的大型用户粘贴内容。

### 13.2 摘要输入

继续使用结构化 compaction prompt，并保持：

- 工具输入只暴露字段摘要，不传完整 JSON；
- Tool Result 使用当前活动投影中的内容，因此可能是原文、snipped 内容或 elided marker；
- `/compact <focus>` 和 PreCompact 等附加关注点继续传入摘要 prompt。

### 13.3 Fold 归档

summary fold 删除的原消息在改写 history 前写入 `operation=fold` archive。归档失败时不删除原消息。

### 13.4 Summarizer 失败

自动与手动压缩统一：

```text
fold archive 成功
→ summarizer 失败
→ 插入 mechanical fold digest
→ 压缩仍成功并释放上下文
```

mechanical digest 明确说明早期消息已折叠、摘要不可用，并在有 archive 时指出归档位置。

若 fold archive 失败：

- 自动压缩：保持 history 原样，发出通知并跳过本轮摘要；
- 手动 `/compact`：保持 history 原样并返回错误。

## 14. 连续压缩保护

Runner 记录：

- soft notice 是否已发出；
- 连续自动 summary compaction 次数；
- `compactStuck` 状态。

规则：

1. 一次健康压缩后，下一轮压力低于 80% 时清零；
2. 连续两轮仍触发 summary compaction，设置 `compactStuck=true`；
3. stuck 时暂停自动 summary compaction；
4. stuck 时 snip/prune 仍允许执行；
5. 压力降到 80% 以下后解除 stuck；
6. 发出一次明确通知，提示 context window 可能小于 system prompt 加近期 tail 的最低需求。

## 15. 可观测性

通过现有 `runner.notifySystem` 发出不会进入模型 history 的通知：

```text
context reached 50% of window; preserving cache-first prefix
snipped 3 stale tool results (~4200 tokens estimated)
pruned 2 stale tool results (~8100 tokens estimated)
compacted 14 messages: 31 → 12
context cleanup paused because the configured window is too small
```

扩展 `ContextCompactionResult`：

```go
type ContextCompactionResult struct {
    BeforeMessages       int
    AfterMessages        int
    FoldedMessages       int
    SnippedResults       int
    PrunedResults        int
    EstimatedTokensSaved int
    Summary              string
    ArchivePaths         []string
    Mechanical           bool
}
```

手动 `/compact` 可展示摘要、归档位置以及是否使用 mechanical fallback。

## 16. 错误处理

### 16.1 Snip/Prune archive 失败

- 不改写对应 Tool Result；
- history 保持原样；
- 发出 warning；
- 若压力已达到 summary 阈值，允许继续尝试使用完整结果执行 summary compaction。

### 16.2 Fold archive 失败

- 不删除 fold 区域；
- 自动压缩跳过；
- 手动压缩返回错误。

### 16.3 Marker 解析失败

- 不 panic；
- 不信任 marker 中的路径和字节数；
- 必要时重新归档并生成合法 marker。

### 16.4 非法 SnipHint

- 回退至工具类别默认值；
- 可发出调试级通知；
- 不阻断模型请求。

### 16.5 Summarizer 超时或失败

- 保持 90 秒超时；
- 可对非超时瞬时错误重试一次；
- archive 成功后使用 mechanical fold；
- summary usage 继续计入 session usage。

## 17. 测试策略

### 17.1 Tool Result 维护单元测试

覆盖：

- 旧的大结果可以 snip；
- 小于 1024 字节的结果不处理；
- recent tail 结果保持原样；
- 多结果消息只改写符合条件的结果；
- 保持 `ToolUseID`、`IsError`、消息顺序和单/多结果结构；
- 多行结果按 Head/Tail 保留；
- 单行结果按 HeadChars/TailChars 保留；
- UTF-8 边界安全；
- snip 幂等；
- prune 幂等；
- snip 可升级为 prune；
- 升级时复用原始字节数和 archive；
- 损坏 marker 不导致 panic；
- archive 失败时 history 完全不变。

### 17.2 SnipHinter 测试

覆盖：

1. 注册工具实现 `SnipHinter` 时严格采用声明；
2. 未声明时只读工具采用 `80/12`；
3. 未声明且有副作用或类型未知时采用 `40/40`；
4. 非法 hint 回退到类别默认值。

### 17.3 Keep Policy 测试

覆盖：

- `IsError=true` 的结果原样保留；
- `error:`、`blocked:` 结果原样保留；
- 普通 Bash `[exit status: ...]` 仍可 snip；
- 四种用户 keep marker 均有效；
- marker 匹配忽略大小写和前导空白；
- 被保护结果对应 assistant call 与兄弟 results 一起保留；
- 多工具调用组不被 recent-tail 或 compaction 边界拆开；
- 保留策略仅作用于最近一次 summary 之后。

### 17.4 Archive 测试

覆盖：

- JSONL 包含完整原始消息和维护元数据；
- 成功写入并同步后才改写 history；
- 写入或 Sync 失败时不改 history；
- 相同归档键复用已有 archive；
- snip 升级 prune 不重复写入；
- index 缺失或损坏时安全重新归档；
- archive 路径不能逃出 session 目录。

### 17.5 压力管线测试

以 1000-token 测试窗口覆盖：

| 压力 | 预期行为 |
|---:|---|
| 49% | 无操作 |
| 50% | 仅一次 soft notice |
| 59% | 不改 history |
| 60% | snip，不调用 summarizer |
| 79% | 仍只 snip |
| 80% | 先 prune |
| prune 后降到 79% | 跳过 summarizer |
| prune 后仍为 80%+ | 调用 summarizer |
| 90% | 强制摘要，跳过经济性检查 |

额外覆盖：

- foldable region 小于约 400 tokens 时，非强制区间跳过摘要；
- 连续两轮摘要后进入 stuck；
- 压力回落后解除 stuck；
- stuck 状态仍可 snip/prune；
- summary 后 recent tail 不超过固定预算；
- 超大单条旧消息仍可 fold；
- system prompt 和可固定的首条用户消息保持原样。

### 17.6 Cold-resume 集成测试

使用真实临时 JSONL session store：

```text
写入完整 journal
→ 创建新 Runner
→ 恢复 ActiveHistory
→ 进入首轮请求
```

验证：

- 压力低于 60% 时不生成 archive、不改投影；
- 达到 60% 时在模型请求前 snip；
- 达到 80% 时先 prune，必要时摘要；
- journal 导出的历史仍含完整原始 Tool Result；
- 模型收到维护后的活动投影；
- 同一 Runner 后续轮次基于维护后的投影继续工作；
- 新 Runner 可通过 archive 索引复用已有归档；
- recovery 与 orphaned tool-call 安全规则保持有效。

### 17.7 Compaction 回归测试

覆盖：

- 小型普通用户消息逐字保留；
- 既有 `<compaction-summary>` 不被再次摘要；
- 工具输入不向 summarizer 暴露完整 JSON；
- 自动和手动摘要失败均采用 mechanical fold；
- mechanical fold 前必须归档成功；
- compaction 后 journal 保持完整；
- summary usage 继续计入 session usage；
- `/compact <focus>` 继续生效。

## 18. 兼容性约束

本次实现不得改变：

- `message.Message` 和 `ToolResult` 的持久化结构；
- session journal 的 append-only 语义；
- provider 消息转换格式；
- session fork 的序列语义；
- UI transcript 展示完整历史的事实来源；
- 现有 `tool.Tool` 基础接口。

`ReadOnlyTool` 与 `SnipHinter` 均为可选能力，因此现有内置工具、MCP 工具和测试替身无需一次性全部修改。

## 19. 验收标准

实现完成后必须满足：

1. 工具密集会话在 60% 压力后优先通过确定性 snip 减少上下文；
2. 达到 80% 后先 prune，释放足够空间时不调用 summarizer；
3. snip/prune 后 Tool Call/Result 配对仍有效；
4. 错误结果和用户 keep 标记内容默认确定性保留；
5. 所有被移除原文均在改写前成功写入独立 JSONL archive；
6. cold resume 按恢复后的上下文压力执行同一维护管线；
7. append-only journal 始终保存完整事实；
8. 自动和手动摘要失败均能在归档成功后机械释放上下文；
9. 不出现连续逐轮重复摘要的压缩循环；
10. 新增单元测试、集成测试及 `go test ./...` 全部通过。
