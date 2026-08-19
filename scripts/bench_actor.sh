#!/usr/bin/env bash
# spec §10 性能预算基准入口（验收标准 #6：基准脚本入库 scripts/）。
# 用法：scripts/bench_actor.sh
# 指标对应关系：
#   BenchmarkDurableAskRoundtrip -> Durable 消息摊销开销预算 < 1ms
#   BenchmarkColdActivation      -> 激活延迟预算 < 50ms（1k 消息尾事件，纯 fold 路径）
set -euo pipefail
cd "$(dirname "$0")/.."
go test -run '^$' -bench 'BenchmarkDurableAskRoundtrip|BenchmarkColdActivation' \
  -benchtime 2s -count 3 ./internal/actor/
