# Context Meter ↑ Bug Fix Design

**Date:** 2026-06-15  
**Status:** Approved

## 问题描述

### 根因

`↑` 的计算公式为 `sessionUsed - sessionOutput`，而 `sessionUsed` 将每次 API 调用的**完整 context 大小**累加。这导致：

- **场景 A**：每轮对话后 `↑` 跳增量 = 当前完整 context 大小，而非新增 token 数。3 轮对话后 `↑` 可能比用户预期大 3–6 倍。
- **场景 B（tool use）**：`runModelTurn` 每个工具轮重置 `turnState`（`state.usageKnown=false`），每轮从 0 开始计 delta，一次用户输入触发 3 个工具轮时，`↑` 累加 3 次完整 input。

### 受影响文件

- `internal/ui/bubble/context_meter.go`
- `internal/loop/runner.go`（ContextStats struct）
- `internal/ui/bubble/bubble_test.go`（测试用例）

---

## 设计方案（方案 A）

### 核心改动：`↑` 语义变更

| | 改前 | 改后 |
|---|---|---|
| `↑` 含义 | session 累计 input token 数（每轮 API 完整 input 之和） | 当前 context 中 input 侧 token 数 |
| 公式 | `sessionUsed - sessionOutput` | `current.UsedTokens - current.OutputTokens` |
| 直觉 | 历史总上传量 | bar 里有多少是 input |

`↓` 保持不变：`SessionOutputTokens`（session 内输出累计）。

### `context_meter.go` 改动

`contextMeterLine()` 中：

```go
// 改前
sessionUsed := maxInt(0, stats.SessionUsedTokens)
sessionOutput := clampInt(stats.SessionOutputTokens, 0, sessionUsed)
sessionUpload := maxInt(0, sessionUsed-sessionOutput)
usedLabel := formatContextUsageLabel(sessionUpload, sessionOutput, used, cache, limit)

// 改后
currentOutput := clampInt(stats.OutputTokens, 0, used)
currentUpload := maxInt(0, used-currentOutput)
sessionOutput := clampInt(stats.SessionOutputTokens, 0, maxInt(1, stats.SessionUsedTokens))
usedLabel := formatContextUsageLabel(currentUpload, sessionOutput, used, cache, limit)
```

### Dead code 清理

**`animatedContextTokens` dead params**

函数签名中 `used` 和 `cache` 参数从不在函数体内使用：

```go
// 改前
func (m appModel) animatedContextTokens(used, cache, limit int) (int, int, float64)

// 改后
func (m appModel) animatedContextTokens(limit int) (int, int, float64)
```

三处调用同步更新：
1. `contextMeterLine()`: `m.animatedContextTokens(barUsed, barCache, limit)` → `m.animatedContextTokens(limit)`
2. `updateContextMeterAnimation()`: `m.animatedContextTokens(m.contextMeter.targetUsed, m.contextMeter.targetCache, limit)` → `m.animatedContextTokens(limit)`

**`ContextStats.SessionCacheTokens` 删除**

该字段在 `runner.go` 中计算，但没有任何消费方。从结构体和 `ContextStats()` 返回值中删除。`OutputTokens` 字段保留（方案 A 需要）。

---

## 数据流（改后）

```
API 返回 usage
  → runner.usage 更新（current：最新一次 API 调用的 token 统计）
  → runner.sessionUsage 更新（session：跨轮累计）

contextStats() 读取两者
  → ContextStats{
      UsedTokens:          current.used,     ← bar 填充 + ↑ 计算
      CacheTokens:         current.cache,    ← bar cache 区
      OutputTokens:        current.output,   ← ↑ 计算（新增使用）
      SessionUsedTokens:   session.used,     ← 暂时保留（↓ clamp 用）
      SessionOutputTokens: session.output,   ← ↓ 显示
      LimitTokens:         limit,
    }

contextMeterLine()
  ↑ = UsedTokens - OutputTokens   ← 当前 context input 侧
  ↓ = SessionOutputTokens          ← session 累计 output
  bar = animated(UsedTokens)       ← 不变
```

---

## 测试策略

### 修改现有测试

`bubble_test.go` 中的 context meter 测试用例目前使用 `SessionUsedTokens`/`SessionOutputTokens` 组合来验证 `↑` 的值，改动后需更新为使用 `UsedTokens`/`OutputTokens`。

### 新增中文测试（`bubble_test.go`）

覆盖以下场景：

1. **首轮无输出时 `↑` 正确**：`UsedTokens=1000, OutputTokens=0` → `↑=1000`
2. **有输出时 `↑` 正确**：`UsedTokens=1100, OutputTokens=100` → `↑=1000`
3. **多轮 context 增长后 `↑` 不重复累加**：Turn 2 的 `UsedTokens=2000, OutputTokens=200` → `↑=1800`（不是 session 累加值）
4. **tool-use 多轮后 `↑` 反映最终状态**：3 轮工具调用后 `UsedTokens=5500, OutputTokens=200` → `↑=5300`
5. **`OutputTokens > UsedTokens` 的防御情况**：clamp 后 `↑` 不为负

### 新增中文测试（`runner_test.go`）

1. **`ContextStats.OutputTokens` 正确反映当前轮 output**
2. **`SessionCacheTokens` 字段已删除，无编译错误**

---

## 边界情况

| 情况 | 处理方式 |
|---|---|
| 尚未收到任何 usage（会话开始） | `UsedTokens=0, OutputTokens=0` → `↑=0`，行为不变 |
| `OutputTokens > UsedTokens`（API 异常） | `clampInt(OutputTokens, 0, used)` 保证 `↑ ≥ 0` |
| `/clear` 后 | `UsedTokens` 归零，`↑` 归零，行为正确 |
| 纯 tool-use 轮（output 全是 tool call）| 同上，以最终 `UsedTokens/OutputTokens` 为准 |

---

## 文件变更清单

| 文件 | 变更类型 | 内容 |
|---|---|---|
| `internal/ui/bubble/context_meter.go` | 修改 | `↑` 计算公式；`animatedContextTokens` 删除 dead params |
| `internal/loop/runner.go` | 修改 | 删除 `ContextStats.SessionCacheTokens` 字段及赋值 |
| `internal/ui/bubble/bubble_test.go` | 修改+新增 | 更新现有断言；新增中文场景测试 |
| `internal/loop/runner_test.go` | 新增 | 新增中文场景测试（ContextStats 字段验证） |
