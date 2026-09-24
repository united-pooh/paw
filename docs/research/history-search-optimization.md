# Agent History Search MCP 同义改写优化研究

日期：2026-09-12

## 研究问题

1. 为什么当前索引对“现象描述”或同义改写的 Top-5 命中率只有约 50%？
2. 哪些改造能在不依赖云端 API 的前提下提升召回，并保持本地低延迟？
3. 何时可以用统一评测证明它足以替代 `transcript_search`？

## 当前证据

本地 128 个 Codex 会话、约 2.14M 字符的实验显示：索引构建约 3.64 秒，热查询通常 1.7–3.0 ms，首次 embedding 查询约 294 ms。Codex 对 8 条真实查询逐条判定后，Top-1 为 37.5%，Top-5 为 50%。主要失败来自两个结构性问题：

- 会话级向量只编码前 4,000 字符，后续解决方案可能完全不参与 dense 检索。
- 原查询、BM25、dense 三路虽然做了 RRF，但没有显式的查询改写、同义词别名或交叉编码重排。

## 文献与方法证据

对话检索研究普遍把“把口语/省略问题改写成独立查询”作为独立阶段。CRDR 通过只修改必要部分同时改善改写和 dense ranking（Qian & Dou, EMNLP 2022）：[ACL Anthology](https://aclanthology.org/2022.emnlp-main.311/)。AdaCQR 明确对齐 term-based 与 semantic-based 检索，报告了跨检索器的改进（Lai et al., COLING 2025）：[ACL Anthology](https://aclanthology.org/2025.coling-main.515/)。ConvGQR 将 query rewriting 与 query expansion 联合，并利用潜在答案词扩展查询：[ACL Anthology](https://aclanthology.org/2023.acl-long.274/)。Query2doc 证明由模型生成的伪文档可以同时改善 sparse 与 dense retrieval：[ACL Anthology](https://aclanthology.org/2023.emnlp-main.585/)。

这些结果不能直接证明某个模型在本地 transcript 上有效，但支持一个工程方向：保留原查询，同时增加改写/扩展分支，最后用统一重排，而不是只替换原查询。

## 推荐实现顺序

### P0：修正文档粒度

把每个会话切成 300–800 token 的 turn/主题窗口，索引项保存 `session_id`、`turn_start`、`turn_end`、时间戳。dense 向量对窗口编码，`fetch_history` 仍返回完整会话。此项优先级最高，因为它直接修复“相关答案在 4,000 字符之后”的漏召回。

### P1：本地查询扩展

并行保留三路查询：

- 原始 query
- 规则改写：提取文件名、函数名、错误码、命令和项目名
- 本地术语别名：例如“卡顿/不跟手/延迟”，“过期/陈旧/未更新”，“思考不对齐/completion mismatch”

每路分别检索，再用 RRF 融合。不要把扩展词直接覆盖原 query，以免 query drift。

### P2：候选重排

对 union Top-30 使用本地 cross-encoder；优先评估 `BAAI/bge-reranker-v2-m3`。若模型体积或延迟不可接受，先实现确定性重排：实体覆盖、精确短语、错误码/路径命中、候选窗口时间邻近性。现有 `--rerank` 可以作为基线，但需要在 chunk 后重新标定权重。

### P3：可选 HyDE/本地 LLM 改写

当规则扩展和 dense 仍无命中时，再调用本地 LLM 生成 hypothetical answer/query；原 query 权重保持 0.6–0.8，HyDE 只作为补充分支。该路径应作为 opt-in，因为它会增加延迟并可能引入事实漂移。

## 验收门槛

建立 60 条人工标注集：实体型、现象型、口语同义改写各 20 条；每条标注相关 session 和可接受证据窗口。比较：

- BM25
- 当前 hybrid
- hybrid + chunk
- hybrid + chunk + 规则扩展
- hybrid + chunk + 扩展 + rerank

发布门槛：Top-1 ≥ 70%，Top-5 ≥ 90%，语义查询 P95 < 500 ms；若启用本地 LLM 改写，P95 < 2 s。还必须记录改写成功率、误报率、`fetch_history` 成功率、索引增量时间、返回 token 数和磁盘占用。

## 结论

最可能带来最大收益的不是继续调 RRF 权重，而是“窗口化索引 + 原查询/规则扩展并行检索 + 候选重排”。这三项可保持本地运行和可解释性；HyDE 或 LLM 改写应放在后续实验，不应直接成为默认路径。

## 源码审计补充

对当前仓库 commit `0126788` 的审计发现：默认 `variant=A`，因此指纹 lane 未启用；`data/expansions.jsonl` 缺失，`expanded_docs=0`，所以扩展检索实际为空；`vectors.npy` 是每会话单向量，且编码器最多接收 512 tokens。实验 corpus 中 128 个会话有 65 个超过 4,000 字符，最长约 652,028 字符。这解释了多个“历史中确实存在，但 Top-5 未命中”的失败。

### 下一实现切片

先实现 chunk-level 索引：256–512 tokens、stride 64；每个 chunk 保存 session id 和 turn 范围，BM25 与 dense 都在 chunk 上运行，返回结果按 session 去重。然后加入离线 alias builder，生成 `expansions.jsonl`，每条扩展保留来源、生成器版本和原始窗口，防止不可审计的运行时幻觉。最后才接 reranker，并在 60 条人工同义改写集上比较每个增量。

不建议把 LLM rewrite 直接设为默认：它应作为 fallback，并单独计量改写耗时、漂移率和失败率。
