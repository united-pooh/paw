# Findings

## Current understanding

The baseline uses one vector per session and truncates encoder input to 512 tokens. Long-session tail content is therefore invisible to dense retrieval. Expanded FTS is configured but has no expansions.jsonl.

## Active hypothesis

Window-level indexing plus bounded deterministic query expansion should improve recall without materially increasing query memory. A reranker is expected to improve Top-1 after recall improves.

## Open questions

How much index growth is acceptable? Does chunking improve Top-5 on paraphrased queries without harming latency?
