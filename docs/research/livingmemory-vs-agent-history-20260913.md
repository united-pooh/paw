# Agent History MCP 与 Astrbot LivingMemory 对比（2026-09-13）

## 结论范围

本报告比较两类不同产品：Agent History MCP 面向 Paw transcript 的可核验原文召回；LivingMemory 面向 AstrBot 的长期记忆抽取、生命周期和关系图谱。不能把 LivingMemory 的“记忆召回”直接当作完整 transcript 搜索，也不能把 MCP 的索引搜索当作自动记忆管理。

## 已验证实现事实

| 维度 | Agent History MCP | LivingMemory |
|---|---|---|
| 输入 | Paw `session.user_message`、assistant 文本/非 redacted reasoning、tool 名、tool result | 对话经总结/atomize 后的 memory atoms；重要记忆可另存 source conversation |
| 检索 | SQLite FTS5 BM25；可选本地 BGE dense；RRF；精确子串优先 | 文档路与图路的 BM25 + vector，RRF 融合；importance/temporal/scope 过滤 |
| 结果证据 | `project_id`、`session_id`、`seq`、字符 offset、snippet，可 fetch 原文 | memory atom 摘要；高重要度记录才保留 source；截断文本无法由升级恢复 |
| 更新 | 每次请求检查 transcript 快照；追加增量索引；重写重建；损坏时当前源文件字面扫描 | 后台总结/索引维护；影子代际构建、失败回滚、断点重建；需要 AstrBot 写入流程 |
| 会话隔离 | 工具参数强制要求 project/session，路径和索引按 session 隔离 | 可按 session/user/global/persona；allowlist 和 aliases；scope 变更只影响新写入记录 |
| 外部服务 | 默认不需要；BGE 模型随仓库 | LLM provider 用于总结/重要性，embedding provider 用于向量；可选 reranker |
| 维护能力 | 当前仓库只读检索，不做记忆衰减、合并或图谱 | TTL、每日衰减、访问强化、归档/恢复、合并、图谱和 Dashboard |

来源：LivingMemory README 宣称文档路/图路 BM25+vector 和 memory atoms；配置文档明确 provider、top-k、scope、TTL 和 reranker 设置；发布文档说明 2.6.1 是稳定线、2.7.0-beta.1 是测试线，并警告完整测试不替代真实 provider 验证。

## 同一真实 Paw transcript 的实验

数据：本地 Paw 项目 `go-code-15b33139` 的 session `f7a7e94f41498259d7f75031c42eb5fa`，transcript 25.0 MiB，Paw 原生投影 1,831 条可检索记录。冻结 60 个在该语料中唯一的 6–8 字符短语作为可验证目标。每次取 Top-5。

| 指标 | Agent History MCP Paw backend | LivingMemory FTS-only 微基准 |
|---|---:|---:|
| Top-1 | 95.0% | 3.3% |
| Top-5 | 98.3% | 11.7% |
| 单次查询中位延迟 | 66.6 ms | 0.03 ms |
| P95 查询延迟 | 74.2 ms | 0.07 ms |
| MCP 首次建立索引 | 约 22.1 s，25.0 MiB transcript | 不适用；需先由 AstrBot 总结/写入 atoms |
| MCP 索引规模 | 约 33,739 chunks 的 SQLite 索引（本实验配置） | 仅 SQLite atoms+FTS 微基准，不含 FAISS、graph、LLM、embedding |
| 进程最大常驻内存 | 约 68.7 MiB（词法模式） | 未测完整 AstrBot；微基准约 65.6 MiB Python 进程 |

### 重要限制

LivingMemory 的 3.3%/11.7% 不是完整插件准确率：该微基准把 Paw 原文直接塞进 LivingMemory 风格 FTS 表，没有运行 AstrBot 的 LLM 总结、embedding provider、graph route 或 temporal ranking；而唯一短语也不符合 unicode61 对长中文串的最佳分词方式。因此它只能证明“裸 FTS 路径不能代表完整 LivingMemory”，不能用来宣称 MCP 全面胜出。

同样，MCP 的 95.0%/98.3% 是“同一 session、唯一短语、已有索引”的检索结果，不代表自然语言同义改写的准确率，也不代表跨 session 记忆质量。首次索引耗时和查询热延迟应分开预算。

## 硬件与运行成本

| 成本项 | Agent History MCP | LivingMemory |
|---|---|---|
| 最低可用 CPU | 普通 CPU；BM25 不需 GPU；dense 使用本地 ONNX CPU | 普通 CPU 可运行 BM25，但 embedding/LLM 由 AstrBot provider 决定；FAISS 与图路增加 CPU/内存 |
| GPU | 非必需 | 非必需，但本地 embedding/LLM 若走 GPU 可降低延迟 |
| 网络/API | 默认 0；本地模型 | 取决于 LLM/embedding provider；每轮总结和重建可能产生调用费用/限流 |
| 内存峰值 | 词法模式实测约 69 MiB；dense 还要加载约 23 MiB ONNX 模型及运行时 | 完整值取决于 AstrBot、FAISS、graph、provider；仅仓库代码和 requirements 不能给出可信峰值 |
| 磁盘 | transcript + SQLite chunks；向量 lane 会增加每 chunk 向量 | SQLite atoms/source/backups + FAISS 文档/图索引 + 可选 graph 数据；保留 source 会增加数据库大小 |
| 写入成本 | 文件增长后做增量索引；不调用 LLM | 总结、重要性评估、embedding、图谱更新；首次/重建成本显著更高 |
| 维护后台 | 无长期衰减任务 | 每日 decay、cleanup、archive、index validation/rebuild |

## 优缺点

### Agent History MCP 优势

1. 原文可核验：返回 seq 和 offset，可继续 fetch，不依赖摘要是否正确。
2. 边界清晰：每次调用显式 project/session，适合 Paw 的当前会话恢复。
3. 资源可预测：本地 BM25 查询不需要 LLM/API；索引更新只处理 transcript 变化部分。
4. 故障语义明确：索引坏掉时扫描当前源快照并标记 `degraded=true`，不会静默返回旧索引。
5. 对代码、错误码、工具名和配置键等精确字符串更可靠。

### Agent History MCP 劣势

1. 还没有 LivingMemory 的记忆抽取、去重、合并、TTL、强化和关系图。
2. 同义改写效果依赖 aliases/dense；没有经过大规模真实改写集验收时不能保证。
3. 原文 transcript 越长，索引和快照校验成本越高；dense lane 的 CPU 延迟明显高于词法 lane。
4. 目前需要 Paw host 增加适配器，仓库自身不能自动替换 Paw 的原生工具。

### LivingMemory 优势

1. 把长对话压缩为可管理的 memory atoms，适合跨 session 的长期偏好、人物关系和项目事实。
2. 文档路、图路、BM25、vector、importance、时间和 scope 共同排序，支持关系型查询。
3. 具备归档、恢复、衰减、访问强化、备份、影子重建和 Dashboard 运维能力。
4. 配置提供 session/persona/global scope、identity aliases、recent-memory slots、reranker 和 graph 开关。

### LivingMemory 劣势

1. 召回对象通常是摘要/atom，不是完整 transcript；低重要性或过期记忆可能不在 active 索引。
2. 首次使用依赖 AstrBot 的 LLM/embedding provider；网络、费用、限流和模型变更会进入系统关键路径。
3. 完整 graph + FAISS + source retention 的 CPU、内存和磁盘成本高于 MCP 的本地词法路径。
4. scope/embedding/summary 策略改变后，已有数据可能需要迁移、重嵌入或重新总结。
5. 它是 AstrBot 插件，不能直接作为 Paw 的 MCP transcript backend；AGPL-3.0 也意味着不能无条件复制其实现代码到其他许可证项目。

## 适用决策

| 需求 | 推荐 |
|---|---|
| 找到旧错误、用户原话、工具输出并核验上下文 | Agent History MCP |
| 低成本、离线、可预测延迟的当前 Paw 会话检索 | Agent History MCP |
| 跨多会话记住偏好、关系、项目事实 | LivingMemory |
| 需要 TTL、归档、图谱可视化和人工维护 | LivingMemory |
| 两者都要 | MCP 负责 transcript evidence；LivingMemory 作为异步长期记忆层，二者通过 source seq/时间/会话 ID 建立回链 |

## 上线建议

不要直接把 LivingMemory 当作 MCP 的替换实现。最稳妥的组合是：Paw 的 `search_transcript` 先路由到 MCP，保证原文证据和故障兜底；LivingMemory 以异步消费者方式接收已完成 turn，生成长期 memory atoms。只有在至少 200 条、跨多个真实 session、包含关键词/同义改写/无答案查询的冻结测试集上分别测完 Top-1、Top-5、P95、总查找时间、索引新鲜度和资源峰值后，才决定是否让某一路成为默认。

## 外部资料

- [LivingMemory GitHub](https://github.com/lxfight-s-Astrbot-Plugins/astrbot_plugin_livingmemory)
- [LivingMemory 配置文档](https://lxfight-s-astrbot-plugins.github.io/astrbot_plugin_livingmemory/en/configuration)
- [LivingMemory 发布与升级说明](https://lxfight-s-astrbot-plugins.github.io/astrbot_plugin_livingmemory/en/releases)

## Claude Code 生成的真实单轮会话测试

为验证“先由 Claude Code 生成短指令，再生成 session summary，再进行 FTS 搜索”的完整链路，使用本地 Claude Code 2.1.228 生成 40 个不重复的软件工程单轮会话。每条包含短 instruction、assistant_summary、3 个 keywords 和不复用原关键词的 paraphrase。测试 transcript 共 80 条 Paw message/assistant records，摘要作为 LivingMemory atom 内容。

这不是人工挑选的 8-case 试验；但它仍是合成任务集，不能替代从历史操作日志中抽取的真实用户查询。

| 查询层 | 系统 | Top-1 | Top-5 | 中位延迟 | P95 延迟 |
|---|---|---:|---:|---:|---:|
| 关键词 | MCP lexical/BM25 | 52.5% | 75.0% | 0.498 ms | 0.758 ms |
| 关键词 | LivingMemory Atom FTS | 62.5% | 70.0% | 0.013 ms | 0.025 ms |
| 同义改写 | MCP lexical/BM25 | 10.0% | 27.5% | 0.539 ms | 0.697 ms |
| 同义改写 | MCP + 本地 BGE dense | 15.0% | 40.0% | 3.09 ms | 3.79 ms |
| 同义改写 | LivingMemory Atom FTS | 0.0% | 0.0% | 0.010 ms | 0.011 ms |

关键词 Top-1/Top-5 受“一个关键词可能命中多条摘要”影响，因此不是唯一目标命中率；同义改写的 gold 是生成该摘要的对应 session。LivingMemory FTS 只做了真实 SQLite FTS 查询，没有 LLM summary 质量评分、向量 provider 或 graph route，不能据此评价完整 LivingMemory。

资源：MCP lexical 建立 80 条/80 chunks 的索引约 17.5 KiB transcript 对应 81.9 KiB SQLite；dense 索引加载随附约 23 MiB 的 BGE ONNX 模型，40 条改写查询中位约 3.1 ms。LivingMemory FTS 数据库约 53.2 KiB；完整插件还需 AstrBot 运行时、provider、FAISS/graph 和后台维护，无法从裸 FTS 微基准推断完整 RSS 或 API 成本。

该测试给出的工程判断是：MCP 的 dense lane 确实比纯词法更能处理同义改写（Top-5 从 27.5% 提到 40.0%），但绝对准确率仍低；LivingMemory 的核心优势来自“总结/原子化后再检索”，不是 FTS 本身。要公平比较长期记忆，应让两者都处理同一批原始会话，并以人工复核或独立 LLM 复核的事实答案作为 gold，同时记录总结调用次数、token、embedding 调用和失败重建成本。

## 线性混合权重消融（Claude 40-session 集）

定义 `alpha` 为 BM25 rank 分数权重，`1-alpha` 为 dense rank 分数权重；两路先转为倒数排名分数后线性相加。结果如下：

| alpha（BM25） | 关键词 Top-1 | 关键词 Top-5 | 同义改写 Top-1 | 同义改写 Top-5 |
|---:|---:|---:|---:|---:|
| 0.0 | 30.0% | 42.5% | **20.0%** | **50.0%** |
| 0.2 | 30.0% | 65.0% | 20.0% | **50.0%** |
| 0.4 | 32.5% | 75.0% | 20.0% | 42.5% |
| 0.6 | 55.0% | 77.5% | 12.5% | 37.5% |
| 0.8 | **67.5%** | **77.5%** | 7.5% | 32.5% |
| 1.0 | 62.5% | 75.0% | 2.5% | 12.5% |

这验证了“混合权重加权排序”确实有价值，但不存在一个同时最优的固定权重。建议生产逻辑为：

```text
有精确子串或高置信 BM25 命中：alpha ≈ 0.8
没有精确命中、疑似同义改写：alpha ≈ 0.0–0.2
dense 不可用：alpha = 1.0
```

这个结果仍然是 40 条 Claude 生成样本上的模型选择实验；正式上线前应在按 session 隔离的真实查询集上重新估计 alpha，并使用独立验证集防止权重过拟合。
