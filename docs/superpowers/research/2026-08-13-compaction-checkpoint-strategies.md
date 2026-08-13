# 压缩 / 检查点 / 记忆分离：Pi × Reasonix × dsh 调研与方向评估

**日期：** 2026-08-13
**触发问题：** 恢复历史远大于窗口的会话时，压缩的 summary 请求消耗大量 token（缓存已消失时全价）。
**用户两个思路：** ① 压缩时保存检查点，从检查点恢复；② 用 memory + todo 分开存储替代检查点/压缩。

## 1. 三家怎么做的（证据实证）

### 1.1 Pi（pi_agent_rust / pi-mono）

- **压缩 = 结构化 checkpoint summary**（`src/compaction.rs:1068` SUMMARIZATION_PROMPT）：
  输出格式为 `## Goal` / `## Constraints & Preferences` / `## Progress (Done / In Progress / Blocked)` / `## Key Decisions` / `## Next Steps` / `## Critical Context`——**明确写给「另一个 LLM 用来继续工作」**，不是对话概述。
- **后台压缩 worker**（`src/compaction_worker.rs`）：压缩移出前台 turn 路径，在现有 runtime 上后台执行，结果在**后续 turn 生效**；配额控制（cooldown 60s / timeout 120s / max 100 次每 session）。
- `CompactionEntry` 是 session 日志事件（含 summary + cut point）；构建 provider context 时在保留区域前插入摘要、**省略更老消息**；迭代式摘要（prior summaries 纳入新摘要）。
- 保守估算（`CHARS_PER_TOKEN_ESTIMATE=3`，宁可早压不可超窗）。
- No-op 检查：没有可摘要内容时不发 LLM 请求、不写事件。

### 1.2 Reasonix（DeepSeek-Reasonix）

- **三区域上下文模型**（`docs/ARCHITECTURE.md` Pillar 1）：
  - `IMMUTABLE PREFIX`：system + tool_specs + few_shots，会话固定、哈希钉住 → 缓存命中候选
  - `APPEND-ONLY LOG`：`[assistant₁][tool₁][assistant₂]...` 单调增长、**绝不重写** → 保留前序 turn 字节前缀
  - `VOLATILE SCRATCH`：推理过程/瞬态计划，每轮重置、永不发送
- **前缀缓存稳定性是首要不变量**（DeepSeek 缓存计费 ~10%）：任何重排/重写/时间戳注入都会破坏缓存；实测 435M tokens / **99.82% cache hit**。
- **Turn-end auto-compaction**（Pillar 3 §4.2）：超过 3000 token 的 tool result 在 turn 结束时截断到 3000——「模型本轮已读过全文；后续轮次见摘要，需要时 re-read」；40% 主动阈值 / 80% 紧急阈值。
- **辅助调用固定用 flash 模型**（含 summarizer、子代理）——不为「转述工具结果」付 pro 价。
- **memory 分开存储**：`~/.reasonix/memory/`（用户记忆）+ `REASONIX.md`（项目记忆）+ `remember/forget/list` 工具——**与压缩并存，不是替代**。

### 1.3 deepseek-harness（上轮已调研，此处补压缩/检查点视角）

- 压缩不碰日志；`compaction/start|end|summary|prune` 是**持久化事件**（压缩动作本身可审计）。
- `session/flush` 每 turn 结束的持久化 checkpoint + checkpoint-policy（崩溃恢复，与压缩无关）。
- 容量事实由 adapter 持有（`resolveModel` → `contextWindow`），compaction-basic 按 `{provider, model}` 精确路由；summarizer 与主模型解耦（可指定不同 provider/model）。

## 2. 对你两个思路的验证

| 思路 | 调研结论 |
|---|---|
| ① 压缩时保存检查点，从检查点恢复 | **Pi 已验证此模式**：压缩产物就是结构化 checkpoint（Goal/Progress/Decisions/Next Steps），恢复时从 checkpoint 继续。dsh 的 checkpoint 是崩溃恢复层（与压缩分离）。Paw 的 goal/plan 事件流已含大量结构化状态，可复用。 |
| ② memory/todo 分开存储替代压缩 | **部分成立**：Reasonix（memory 目录）、astrcodey（memory/goal/todo 三个扩展分开）、Paw（todo 快照事件 + goal 聚合）都做结构化分离——但它们**全部与压缩并存，无一替代压缩**。原因：模型继续工作需要对话级上下文（diff、错误、中间推理），仅靠结构化状态不够。结构化存储能**降低压缩频率**，不能消除压缩。 |

## 3. 候选方向（creative-ai，≥5 个真正不同的方向）

| # | 方向 | 核心机制 | 借鉴 | 解决痛点 | 代价/风险 |
|---|---|---|---|---|---|
| A | **结构化 checkpoint 压缩** | 压缩产物从「对话概述」改为 Pi 式结构化 checkpoint（Goal/Progress/Decisions/Next Steps），作为事件写入 plan/goal 流；恢复时优先从最新 checkpoint 继续 | Pi | 恢复大会话时不再需要重读/重摘要；恢复质量高 | 摘要 prompt 改造；checkpoint 与现有 summary 消息格式兼容 |
| B | **缓存优先重构**（三区域 + tool-result 截断） | immutable prefix 钉住 + append-only log 绝不重写 + turn-end 截断 >3000 token 的 tool result | Reasonix | 长会话持续低成本（缓存命中率高）；压缩频率大幅下降 | 改动大；DeepSeek 外 provider 缓存语义不同（Anthropic 需显式 breakpoint） |
| C | **后台异步压缩 worker** | 压缩移出前台 turn；恢复时先用「旧压缩摘要 + 尾部」立即继续，压缩后台执行、下轮生效 | Pi | 恢复不阻塞、不等待大 summary；前台零延迟 | 状态管理复杂化；结果延迟一 turn |
| D | **结构化状态分离**（深化思路 2） | goal/todo/决策从对话流结构化提取（Paw 已有 goal 聚合 + todo 快照事件），恢复上下文 = 结构化状态块 + 最近 N 条消息；压缩只处理中间对话 | Reasonix/astrcodey/Paw 现状 | 恢复上下文更小更准；压缩频率降低 | 需要「对话 → 结构化状态」的提取/同步机制；状态块新鲜度 |
| E | **四层上下文组装**（A+D 混合） | immutable 系统前缀 + 结构化状态块（goal/todo 投影）+ checkpoint 摘要块 + append-only 尾部；每层独立缓存/失效 | Pi+Reasonix | 最完整；缓存友好 + 恢复友好 + 压缩频率低 | 最大改动；分层边界需要设计 |
| F | **缓存感知压缩触发**（最小改动） | 用 provider cache 统计（hit/miss）决定压缩时机——cache hit 时延后压缩（避免破坏缓存），miss 时提前压缩；压缩本身也只在 miss 时做 | Reasonix 指标 | 直接省钱；改动小 | 需要 cache 统计回传；对无缓存计费的 provider 无效 |

## 4. 推荐

**E 是终态，但落地路径建议 A → F → C 分步**：
1. **A（结构化 checkpoint）**解决你的恢复痛点（低风险、Pi 已验证、Paw 的 plan/goal 事件可承接）——检查点即事件，恢复即读检查点。
2. **F（缓存感知触发）**解决长期成本（最小改动，Reasonix 已证明指标价值）。
3. **C（后台 worker）**把压缩从「恢复路径上的同步阻塞 + 大请求」移走（Pi 已验证）。

**不建议单独做 B 的全量重构**（DeepSeek 专属机制，Paw 多 provider）；**D 单独做不足以替代压缩**（调研结论：结构化状态是压缩的补充不是替代），但 D 的元素应并入 E/A。

## 5. 证据索引

- Pi：`github.com/Dicklesworthstone/pi_agent_rust` `src/compaction.rs`（SUMMARIZATION_PROMPT:1068、summarize_entries:1565）、`src/compaction_worker.rs`（配额:60s/120s/100）
- Reasonix：`github.com/esengine/DeepSeek-Reasonix` `docs/ARCHITECTURE.md`（Pillar 1 三区域、Pillar 3 §4.2 turn-end 截断）、`src/memory.ts` / `user-memory.ts` / `project-memory.ts`
- dsh：`.agents/notes/implemented/architecture/2026-07-20-routed-model-context-and-compaction-policy.md`、`packages/core/session/src/known-event-types.ts`（compaction/* 事件）
- Paw：`internal/loop/context_compaction.go`、`internal/loop/context_pressure.go`、`internal/plan/es_store.go`、`internal/goal/es_store.go`、`internal/session/journal.go`（todo_snapshot）

**未验证项：** Reasonix main-v2（Go 重写）源码未读（API 限流），依据 legacy TS 架构文档；pi-mono 原始 JS 版未逐行对比 Rust port。
