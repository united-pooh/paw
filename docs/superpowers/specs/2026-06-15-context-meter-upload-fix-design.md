# Context Meter Token 显示重设计

**Date:** 2026-06-15  
**Status:** Approved（v3 — 动态箭头，thinking 阶段也显示 ↓）

## 问题描述

### 根因

原来的 `↑`（上传）用 `sessionUsed - sessionOutput` 计算，`sessionUsed` 将每次 API 调用的完整 context 大小累加：

- **场景 A**：每轮对话后 `↑` 跳增量 = 当前完整 context 大小，3 轮后 `↑` 远大于用户预期。
- **场景 B（tool use）**：`runModelTurn` 每个工具轮重置 `turnState`，一次用户输入触发 3 工具轮时，`↑` 累加 3 次完整 input。

---

## 新设计：单数字 + 动态箭头

### 语义

| 元素 | 含义 | 来源 |
|---|---|---|
| 数字 | 当前 context 总 token 数（input + output） | `stats.UsedTokens` |
| `↑` | 外部操作阶段：用户输入、工具执行、等待模型响应 | `m.isGenerating == false` |
| `↓` | 模型生成阶段：extended thinking 或流式输出文本 | `m.isGenerating == true` |

**示例**：
- 用户发送消息后、模型响应前：`1.1k↑ 23%(8%)  [bar]  free(77%)`
- 模型 extended thinking 中：`1.1k↓ 23%(8%)  [bar]  free(77%)`
- 模型流式输出文本中：`1.2k↓ 25%(8%)  [bar]  free(75%)`
- 工具调用执行中：`1.2k↑ 25%(8%)  [bar]  free(75%)`
- 空闲：`1.2k↑ 25%(8%)  [bar]  free(75%)`

旧格式 `1000↑ 100↓ 23%(8%)` 变为 `1.1k↓ 23%(8%)`，更简洁。

---

## `isGenerating` 状态字段

### 为什么需要新字段

`m.activeAssistant != -1` 只在**输出文本**（`assistantDeltaMsg`）时为非 -1；
extended thinking（`thinkingDeltaMsg`）不经过该路径，thinking 期间 `activeAssistant == -1`。
因此需要单独追踪 `isGenerating`。

### 生命周期

```
turnStartedAt 设置（用户提交）
    → isGenerating = false（↑，等待模型）

thinkingDeltaMsg 到达
    → isGenerating = true（↓，模型推理中）

assistantDeltaMsg 到达（第一个文本 token）
    → isGenerating = true（↓，模型输出中）

toolCallMsg 到达
    → isGenerating = false（↑，工具执行中）

toolResultMsg / doneMsg 到达
    → isGenerating = false（↑，等待下一个子轮）

turnFinishedMsg 到达
    → isGenerating = false（↑，空闲）
```

### 在 `appModel` 中新增字段

```go
// types.go 或 bubble.go 的 appModel struct
isGenerating bool
```

### 在 `app.go` Update 中设置

```go
case thinkingDeltaMsg:
    m.isGenerating = true         // 新增
    m.appendThinkingDelta(string(msg))

case assistantDeltaMsg:
    m.isGenerating = true         // 新增
    m.appendAssistantDelta(string(msg))

case toolCallMsg:
    m.isGenerating = false        // 新增
    m.activeAssistant = -1
    // ... 现有逻辑不变

case doneMsg:
    m.isGenerating = false        // 新增
    m.activeAssistant = -1

case turnFinishedMsg:
    m.isGenerating = false        // 新增
    m.queryGuard.FinishModel()
    // ... 现有逻辑不变
```

---

## `context_meter.go` 改动

### `formatContextUsageLabel` 签名变更

```go
// 改前
func formatContextUsageLabel(upload, output, used, cache, limit int) string

// 改后
func formatContextUsageLabel(used, cache, limit int, isGenerating bool) string
```

**改后实现**：

```go
func formatContextUsageLabel(used, cache, limit int, isGenerating bool) string {
    arrow := "↑"
    if isGenerating {
        arrow = "↓"
    }
    parts := []string{formatCompactTokenCount(used) + arrow}
    parts = append(parts, fmt.Sprintf("%s(%s)", formatContextPercent(used, limit), formatContextPercent(cache, limit)))
    return strings.Join(parts, " ")
}
```

### `contextMeterLine` 变更

```go
// 改前（需要 session 字段）
sessionUsed := maxInt(0, stats.SessionUsedTokens)
sessionOutput := clampInt(stats.SessionOutputTokens, 0, sessionUsed)
sessionUpload := maxInt(0, sessionUsed-sessionOutput)
usedLabel := formatContextUsageLabel(sessionUpload, sessionOutput, used, cache, limit)

// 改后
usedLabel := formatContextUsageLabel(used, cache, limit, m.isGenerating)
```

### `animatedContextTokens` dead params 清理

```go
// 改前：used/cache 参数从不在函数体内使用
func (m appModel) animatedContextTokens(used, cache, limit int) (int, int, float64)

// 改后
func (m appModel) animatedContextTokens(limit int) (int, int, float64)
```

三处调用同步更新（`contextMeterLine` 和 `updateContextMeterAnimation` 各一处）。

---

## `runner.go` — `ContextStats` 精简

session 字段不再被 context_meter 消费，删除冗余字段：

```go
// 改前
type ContextStats struct {
    UsedTokens          int
    CacheTokens         int
    OutputTokens        int        // 未被使用
    SessionUsedTokens   int        // 仅用于旧 ↑ 计算
    SessionCacheTokens  int        // 从未使用
    SessionOutputTokens int        // 仅用于旧 ↓ 显示
    LimitTokens         int
}

// 改后
type ContextStats struct {
    UsedTokens  int
    CacheTokens int
    LimitTokens int
}
```

`ContextStats()` 方法同步精简。`runner.sessionUsage` 内部字段保留，留给未来潜在统计用途。

---

## 数据流（改后）

```
用户提交 → isGenerating=false（↑）
thinkingDeltaMsg → isGenerating=true（↓）
assistantDeltaMsg → isGenerating=true（↓）
toolCallMsg → isGenerating=false（↑）
doneMsg → isGenerating=false（↑）
turnFinishedMsg → isGenerating=false（↑，空闲）

API 返回 usage → runner.usage 更新 → ContextStats{UsedTokens, CacheTokens, LimitTokens}

contextMeterLine()
  数字 = UsedTokens
  箭头 = ↑/↓（由 isGenerating 决定）
  bar  = animated(UsedTokens)
```

---

## 测试策略

### 修改现有测试（`bubble_test.go`）

- 移除所有 `SessionUsedTokens`/`SessionOutputTokens`/`OutputTokens` 字段的使用
- `formatContextUsageLabel` 断言更新为新签名
- context meter 标签断言格式：`{used}{arrow} {used%}({cache%})`

### 新增中文测试（`bubble_test.go`）

覆盖以下场景（用中文注释标注）：

1. **空闲时显示 ↑**：`isGenerating=false, UsedTokens=1000` → 标签含 `1k↑`
2. **thinking 时显示 ↓**：`isGenerating=true, UsedTokens=1000` → 标签含 `1k↓`
3. **文本输出时显示 ↓**：`isGenerating=true, UsedTokens=1100` → 标签含 `↓`
4. **工具调用后恢复 ↑**：`isGenerating=false` → 标签含 `↑`
5. **UsedTokens=0 时显示 0↑ 不崩溃**
6. **多轮对话后数字只反映当前 context，不累加**

### 新增中文测试（`runner_test.go`）

1. **`ContextStats` 精简后无编译错误，三字段均正确**
2. **`UsedTokens` = input + output（ContextTokenCount 语义）**
3. **`CacheTokens` 正确反映 cache hit**

---

## 边界情况

| 情况 | 处理 |
|---|---|
| 尚未收到任何 usage | `UsedTokens=0, isGenerating=false` → `0↑` |
| `/clear` 后 | `UsedTokens` 归零，`isGenerating=false` → `0↑` |
| tool use 3 轮后 | 数字 = 最终轮 `UsedTokens`，不累加 |
| `thinkingDeltaMsg` 后紧接 `assistantDeltaMsg` | `isGenerating` 保持 true，无闪烁 |
| 程序启动首帧，尚未交互 | `isGenerating=false`（zero value）→ `↑` |

---

## 文件变更清单

| 文件 | 变更类型 | 内容 |
|---|---|---|
| `internal/ui/bubble/types.go` 或 `bubble.go` | 修改 | `appModel` 新增 `isGenerating bool` |
| `internal/ui/bubble/app.go` | 修改 | 5 处消息处理设置 `isGenerating` |
| `internal/ui/bubble/context_meter.go` | 修改 | 新 `formatContextUsageLabel`；`contextMeterLine` 用 `isGenerating`；清理 dead params |
| `internal/loop/runner.go` | 修改 | `ContextStats` 精简为 3 字段；`ContextStats()` 同步 |
| `internal/ui/bubble/bubble_test.go` | 修改+新增 | 更新现有断言；新增 6 条中文场景测试 |
| `internal/loop/runner_test.go` | 新增 | 新增 3 条中文场景测试 |
