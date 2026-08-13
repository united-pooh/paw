# 状态压缩与 Ariadne 方向记忆设计

**日期：** 2026-08-13
**状态：** 设计定稿（brainstorming 收敛 + idea-evaluator 评审修订：D10–D13、切片 0 验证实验）
**关联：** `docs/superpowers/research/2026-08-13-compaction-checkpoint-strategies.md`（调研）、`docs/superpowers/research/2026-08-13-event-stream-comparison.md`

## 1. 背景与动机

恢复历史远大于窗口的会话时，现有「对话摘要式压缩」把被折叠历史全量发给 summarizer——缓存消失时按全价计费，消耗与历史规模成正比。调研（Pi / Reasonix / dsh）结论：模型继续工作需要的是**可行动状态**（目标/进度/决策/下一步）而非对话记录；细节可以按需取回。

**决策：引入「状态压缩」模式**——用结构化状态（plan/todo/全局记忆/Ariadne）承载长期信息，对话只保留最近 3 轮，窗口外对话进事件流归档并按需取回。**不替代**现有摘要压缩，由 settings 切换。

## 2. 现状

- plan 聚合（目标）+ todo 快照事件（进度）+ transcript 事件流（对话）均已持久化
- 压缩：`maintainContextProjection` 多级阈值（soft/snip/compact/force）→ 对话概述摘要 + 归档
- 会话存储：`<cwd>/.paw/sessions/<sessionID>/`（工作区级）

## 3. 目标与非目标

### 目标
- 恢复大会话零摘要成本（不送长对话）
- 运行时缓存稳定（对话不重排不裁剪，字节前缀稳定）
- 压缩 = 状态刷新（快、便宜），90% 阈值低频触发
- 结构化的方向记忆（Ariadne）供恢复/继续工作

### 非目标
- 不替代模式 A（摘要压缩保留，settings 切换）
- 不做 RAG/向量检索（取回靠关键字检索 transcript）
- 不迁移 goal 聚合（plan/todo/memory/ariadne 之外的领域状态维持现状）

## 4. 存储布局（新）

```
~/.paw/
├── memory.md                       ← 全局记忆（长期使用习惯）
└── projects/
    └── <项目名>/
        └── sessions/
            └── <sessionID>/
                ├── transcript.jsonl  ← 会话迁移至此
                ├── meta.json
                ├── turns.jsonl
                └── ariadne.md        ← 方向记忆（压缩/恢复产物）
```

- **项目名**：以会话启动时的工作目录 basename（或目录哈希，防重名）确定
- **迁移策略**：新会话直接写全局布局；读取时先探测全局路径，不存在则 fallback 到工作区 `<cwd>/.paw/sessions/`（旧会话兼容）；提供一次性迁移命令（复制 + 校验）

## 5. 组件设计

### 5.1 update_memory（新工具）— 全局记忆
- 写 `~/.paw/memory.md`：用户长期使用习惯/偏好/项目约定（跨会话）
- 结构：自由格式 Markdown（模型自组织），Paw 校验长度上限（如 8 KiB）与可解析性
- 事件：`session.memory_updated`（payload：内容摘要 + 时间 + 触发 turn）
- 恢复时由系统读取注入（非模型 read_file）

### 5.2 update_ariadne（新工具）— 方向记忆
- 写 `<session>/ariadne.md`：Markdown 结构化（Pi 式 section）：

```markdown
## 方向
当前目标与意图（与 plan 对齐，简述）

## 进度
已完成 / 进行中 / 阻塞（与 todo 对齐，简述）

## 关键决策
- **[决策]**：理由（含被否决的方案，防止重复探索）

## 下一步
1. 有序列表（与 todo 对齐）

## 教训
踩过的坑、错误模式（供后续避免）
```

- **生成指令要求（验证实验 v2 结论）**：`## 进度` 必须保留**所有未完成事项**（含早期任务、被搁置事项），不能只写最新进度——会话中途任务重点切换时，旧任务细节是后续恢复的关键（实验 265 会话暴露：状态块聚焦最新进度导致旧任务细节缺失）
- Paw 校验：section 完整（方向/进度/关键决策/下一步/教训 五段）、长度上限（如 16 KiB）、更新时间戳
- 事件：`session.ariadne_updated`（payload：内容摘要 + seq + 时间）
- 恢复时由系统读取注入；压缩时强制刷新

### 5.3 search_transcript（新工具）— 按需取回
- 检索当前 session 的 transcript 事件流
- 入参：`query`（关键字）、`turn_range`（可选，轮次区间）、`limit`（默认 20 条）
- 返回：匹配记录（role/时间/内容片段/turn_id），完整原文可再按 turn_id 取
- **显式范围约定（D11）**：返回必须包含 `matched`（命中数）与 `searched`（可检索范围：turn 区间/时间窗/总记录数）；**0 命中时明确返回「未找到 + 可检索范围」**，并注入提示「查不到 ≠ 不存在（可能超出检索范围或措辞不同）」，防止模型把「未命中」误判为「事实不存在」
- 用途：模型需要窗口外细节（用户原话、被否决方案、旧错误）时临时取回
- 实现：顺序扫描 transcript（事件流量级小，无需索引）；结果注入当前轮

### 5.4 注入器（新，loop 层）
- `buildStateContext(sessionID)`：读取 plan 投影 + todo 快照 + memory.md + ariadne.md，组装为系统消息块（固定格式，字节稳定以保缓存）
- **陈旧状态标注（D12）**：状态块内每个组成部分附 `updated_at`（来自对应事件/文件时间戳）与来源（如 `plan@2026-08-13T10:00`、`todo@turn_42`）；模型可据此识别陈旧状态并在必要时用 search_transcript/工具核对
- 恢复时与「最近 3 轮」拼接；压缩裁剪后同样重新注入

## 6. 窗口策略（模式 B）

| 时机 | 行为 |
|---|---|
| **恢复** | 系统注入（plan+memory+ariadne+todo）+ **最近 3 轮完整对话** + Recovery；长对话不送。**最近 3 轮清洗（验证实验 v2 结论）**：工具调用参数（input 细节）占最近 3 轮字符量 80%+，清洗为「工具名序列 + 文本」后 token 再降约 70% 且方案质量不降——恢复注入时保留工具名与文本、省略工具参数原文 |
| **运行时** | 对话全量保留，不裁剪不刷新（缓存稳定） |
| **≥90% 窗口** | 触发状态压缩：强制模型刷新 plan/todo/memory/ariadne → 裁剪窗口外对话，但**保留当前进行中的轮完整（输入+工具+输出）+ 前 2 轮**（共 3 轮）→ 重新注入状态块 |

- 3 轮语义：恢复与压缩统一（当前轮 + 前 2 轮；恢复时无当前轮则取最近 3 轮）
- **触发时机（D10）**：90% 阈值**只在实际触发点检查**——工具循环空闲点（模型等待用户输入/工具结果已全部归位）或 turn 边界（`turn_completed`/`turn_failed` 后）；**不在工具执行中途打断**。「进行中轮」在触发点必然完整（输入+工具调用+结果齐备），无需处理未完成轮；若压缩请求恰逢未完成轮，推迟到该轮完成
- 90% 阈值基于现有估算（`estimateMessageTokens`）；可配置（settings `context.stateCompactionRatio`，默认 0.9）
- 压缩触发时**不调用摘要模型**（零摘要请求）；若状态刷新后仍超窗口（极端），降级为模式 A 摘要压缩兜底（记录事件）

## 7. settings 设计

```jsonc
// ~/.paw/settings.json（与现有 settings 合并）
{
  "context": {
    "mode": "state",            // "summary" | "state"；默认 state（状态压缩），可切回 summary
    "stateCompactionRatio": 0.9,// 状态压缩触发阈值
    "resumeRecentTurns": 3,     // 恢复/压缩保留的完整轮数
    "stateBlockStable": true    // 状态块字节稳定（保缓存），变化时整体替换
  }
}
```

## 8. 事件目录（新增）

| 事件 | payload | 说明 |
|---|---|---|
| `session.memory_updated` | `{summary, updated_at, turn_id}` | 全局记忆更新（事件流留痕，内容在文件） |
| `session.ariadne_updated` | `{summary, seq, updated_at, turn_id}` | 方向记忆更新 |
| `session.state_compacted` | `{triggered_at, ratio, kept_turns, dropped_messages}` | 状态压缩发生的事实（审计） |

## 9. 边界与决策记录

| # | 决策 | 理由 | 状态 |
|---|---|---|---|
| D1 | 双模式 settings 切换，默认 state | 用户决策：状态压缩为新默认；模式 A（摘要）可显式切回 | 已定 |
| D2 | Ariadne 用 update_ariadne 独立工具（非 update_memory 复用） | 全局记忆与方向记忆语义/生命周期不同（跨会话 vs 会话级） | 已定 |
| D3 | 恢复不送长对话，只送状态 + 最近 3 轮 | 省 token、零摘要；细节靠 search_transcript | 已定 |
| D4 | 运行时对话全量保留到 90% | 缓存稳定（字节前缀不重排）；压缩低频 | 已定 |
| D5 | 压缩保留当前轮 + 前 2 轮 | 进行中的工作不丢失（输入/工具/输出完整） | 已定 |
| D6 | 压缩不调摘要模型；仍超则降级模式 A | 状态刷新是主要路径；极端场景兜底 | 已定 |
| D7 | 会话迁移到 `~/.paw/projects/<项目>/sessions/` | 全局项目布局统一（用户决策）；旧会话读取 fallback | 已定 |
| D8 | Ariadne 命名（希腊神话迷宫之线） | 用户决策（替代拼音「天枢」） | 已定 |
| D9 | 状态块字节稳定（整体替换，不局部编辑） | 保 provider 前缀缓存命中 | 已定 |
| D10 | 压缩触发推迟到工具循环空闲点/turn 边界，不在工具执行中途打断 | 保证「当前轮」在触发点必然完整；避免中断未完成轮（idea-evaluator FF3） | 已定 |
| D11 | search_transcript 显式返回命中数 + 可检索范围，0 命中提示「查不到≠不存在」 | 防模型把「未命中」误判为「事实不存在」（idea-evaluator FF4） | 已定 |
| D12 | 状态块各组成部分标注 updated_at + 来源 | 模型可识别陈旧状态，必要时核对（idea-evaluator FF2） | 已定 |
| D13 | 实施前先做 validation experiment（全量历史 vs 状态+3 轮 A/B） | 「3 轮+状态」恢复质量无直接先例，实验决定参数成立性（idea-evaluator FF1） | 已定 |

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 状态（plan/todo/ariadne）与实际工作漂移（模型未及时刷新） | 压缩/恢复强制刷新；事件流留痕可审计 |
| 恢复后模型缺对话细节 | search_transcript 按需取回；最近 3 轮保底 |
| 90% 触发时状态刷新中断 turn | 触发推迟到工具循环空闲点/turn 边界（D10）；刷新指令限定在裁剪前一次调用内；失败降级模式 A |
| 会话路径迁移破坏旧数据 | 只读 fallback + 一次性迁移命令（复制校验后切换） |
| 全局 memory 泄漏项目特定信息 | update_memory 工具校验 + 用户可编辑 memory.md |
| 缓存稳定假设对非 DeepSeek provider 不成立 | 状态块整体替换策略对所有 provider 无害（仅少赚缓存）；可切回模式 A |

## 11. 实施计划（3 个垂直切片，验证实验先行）

### 切片 0：Validation experiment（D13，先行，独立于功能实现）
- 已执行两轮（v1 人工状态块单会话、v2 模型生成状态块 3 会话），报告：`docs/superpowers/research/2026-08-13-state-compression-validation.md`
- **v2 结论**：状态块生成质量 3/3；「状态块 + 清洗后最近 3 轮」恢复成本为全量的 **10–18%**（通过标准 ≤30% 大幅通过）；文件层定位准确率高，函数名层幻觉可由工具验证纠正
- **实施要点**：① ariadne 生成指令保留所有未完成事项（§5.2）；② 恢复时最近 3 轮清洗工具参数、保留工具名（§6）；③ 全量历史在恢复场景诱导行为模仿（提示设计倾向，非阻塞项）

### 切片 1：恢复路径（独立可验证、可回滚）
1. 全局项目布局 + 会话路径迁移 + fallback 读取 + 一次性迁移命令
2. `buildStateContext` 注入器（plan/todo/memory/ariadne → 稳定状态块 + D12 时间戳标注）
3. 模式 B 恢复流程（注入 + 最近 3 轮 + Recovery，不送长对话）
4. `search_transcript` 工具（关键字 + turn 范围 + D11 显式范围约定）
5. settings 接入（`context.mode` 等）+ TUI 状态展示

### 切片 2：状态工具与事件
6. `update_memory` 工具 + `session.memory_updated` 事件 + memory.md 读写校验
7. `update_ariadne` 工具 + `session.ariadne_updated` 事件 + ariadne.md 读写校验

### 切片 3：运行时状态压缩
8. 模式 B 运行时：90% 阈值（D10 触发时机）→ 强制刷新 → 裁剪保留当前轮+前 2 轮 → 重注入
9. 降级路径（仍超窗口 → 模式 A 摘要）+ `session.state_compacted` 事件
10. 测试：恢复零摘要、压缩保留轮次语义、触发时机（工具中途不打断）、路径迁移兼容、双模式切换、状态块字节稳定

## 12. 待确认项（评审时拍板）

- [ ] 全局 memory.md 是否需要结构化校验（自由 Markdown vs 强制 section）
- [ ] search_transcript 是否同时提供 `read_turn(turnID)` 原文读取（当前设计：检索结果含片段，完整原文按 turn_id 二次取）
- [ ] 项目名冲突（同名目录）处理：basename + 哈希后缀
- [ ] 状态块注入位置（system 尾部 vs 独立 user 块）——影响缓存前缀布局
