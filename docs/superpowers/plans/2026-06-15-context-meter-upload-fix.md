# Context Meter 动态箭头重设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 context meter 的 `↑`/`↓` 改为单数字（当前 context 大小）+动态箭头（模型推理/输出时 `↓`，其余 `↑`），同时清理 ContextStats 冗余字段和 dead params。

**Architecture:** 在 `appModel` 新增 `isGenerating bool` 字段，由 `thinkingDeltaMsg`/`assistantDeltaMsg` 设为 true，由 `toolCallMsg`/`doneMsg`/`turnFinishedMsg` 清零。`context_meter.go` 读取该字段决定箭头方向，数字恒为 `stats.UsedTokens`。`runner.go` 的 `ContextStats` 精简为 3 字段（UsedTokens、CacheTokens、LimitTokens）。

**Tech Stack:** Go 1.21+, Bubble Tea TUI, testify-style table tests

---

## 文件结构

| 文件 | 变更 |
|---|---|
| `internal/loop/runner.go` | ContextStats 精简为 3 字段，ContextStats() 方法同步 |
| `internal/ui/bubble/types.go` | appModel struct 新增 `isGenerating bool` |
| `internal/ui/bubble/app.go` | 5 处消息处理设置 isGenerating |
| `internal/ui/bubble/context_meter.go` | formatContextUsageLabel 新签名；contextMeterLine 用 isGenerating；animatedContextTokens 删 dead params |
| `internal/ui/bubble/bubble_test.go` | 修复存量测试；新增 6 条中文场景测试 |
| `internal/loop/runner_test.go` | 新增 3 条中文场景测试 |

---

### Task 1: 精简 ContextStats 结构体

**Files:**
- Modify: `internal/loop/runner.go:59-67`（struct）及 `224-250`（方法）
- Test: `internal/loop/runner_test.go`

- [ ] **Step 1: 写失败测试（验证三字段语义）**

在 `runner_test.go` 末尾追加：

```go
// TestContextStats_精简后三字段正确 验证 ContextStats 只暴露三个字段，且 UsedTokens = input+output。
func TestContextStats_精简后三字段正确(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 1000, OutputTokens: 100}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	if stats.UsedTokens != 1100 {
		t.Errorf("UsedTokens = %d, want 1100", stats.UsedTokens)
	}
	if stats.CacheTokens != 0 {
		t.Errorf("CacheTokens = %d, want 0", stats.CacheTokens)
	}
	if stats.LimitTokens != 200000 {
		t.Errorf("LimitTokens = %d, want 200000", stats.LimitTokens)
	}
}

// TestContextStats_CacheTokens正确反映命中缓存 验证 CacheHitTokens 进入 CacheTokens。
func TestContextStats_CacheTokens正确反映命中缓存(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 500, CacheReadInputTokens: 300, OutputTokens: 50}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	// UsedTokens = 500 + 300 + 50 = 850
	if stats.UsedTokens != 850 {
		t.Errorf("UsedTokens = %d, want 850", stats.UsedTokens)
	}
	if stats.CacheTokens != 300 {
		t.Errorf("CacheTokens = %d, want 300", stats.CacheTokens)
	}
}

// TestContextStats_无usage时返回零值 验证未收到任何 usage 事件时安全返回零值。
func TestContextStats_无usage时返回零值(t *testing.T) {
	runner := NewRunner(nil, nil, nil, nil, "")
	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 {
		t.Errorf("空 runner ContextStats = %+v, want all zero tokens", stats)
	}
	if stats.LimitTokens != 100000 {
		t.Errorf("LimitTokens = %d, want 100000", stats.LimitTokens)
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败（旧 ContextStats 有 session 字段，新测试引用不存在的字段不会出现，但 bubble_test 里的旧字段引用会爆）**

```bash
cd /Users/united_pooh/PyProjects/go-code && go test ./internal/loop/... -run TestContextStats -v 2>&1 | tail -20
```

Expected: TestContextStats_精简后三字段正确 和其他两个 PASS（旧结构已有这三字段），但 bubble_test 在后续步骤才修。

- [ ] **Step 3: 精简 ContextStats struct（runner.go:59-67）**

```go
type ContextStats struct {
	UsedTokens  int
	CacheTokens int
	LimitTokens int
}
```

- [ ] **Step 4: 精简 ContextStats() 方法（runner.go:224-250）**

```go
func (runner *Runner) ContextStats(limitTokens int, _ string) ContextStats {
	if limitTokens <= 0 {
		limitTokens = 1024 * 1024
	}
	if runner == nil {
		return ContextStats{LimitTokens: limitTokens}
	}

	runner.mu.RLock()
	usage := runner.usage
	usageKnown := runner.usageKnown
	runner.mu.RUnlock()

	current := usageTotalsFromUsage(usage, usageKnown)
	return ContextStats{
		UsedTokens:  current.used,
		CacheTokens: current.cache,
		LimitTokens: limitTokens,
	}
}
```

- [ ] **Step 5: 运行 runner 测试**

```bash
cd /Users/united_pooh/PyProjects/go-code && go test ./internal/loop/... -v 2>&1 | tail -30
```

Expected: 新增的 3 个 TestContextStats_* 全部 PASS。bubble 包因字段缺失暂时编译失败，Task 6 修复。

- [ ] **Step 6: Commit**

```bash
cd /Users/united_pooh/PyProjects/go-code && git add internal/loop/runner.go internal/loop/runner_test.go
git commit -m "refactor :recycle: : 精简 ContextStats 为三字段，删除未使用的 session 字段"
```

---

### Task 2: appModel 新增 isGenerating 字段 + app.go 设置

**Files:**
- Modify: `internal/ui/bubble/types.go:219`（appModel struct）
- Modify: `internal/ui/bubble/app.go`（5 处 Update case）

- [ ] **Step 1: 在 types.go appModel struct 的 activeAssistant 后新增字段**

在 `types.go` 第 219 行 `activeAssistant int` 后插入：

```go
activeAssistant int
isGenerating    bool
```

- [ ] **Step 2: 在 app.go Update 的 5 个 case 中设置 isGenerating**

**`assistantDeltaMsg`（约第 107 行）：**
```go
case assistantDeltaMsg:
	m.isGenerating = true
	m.appendAssistantDelta(string(msg))
	return m, nil
```

**`thinkingDeltaMsg`（约第 110 行）：**
```go
case thinkingDeltaMsg:
	m.isGenerating = true
	m.appendThinkingDelta(string(msg))
	return m, nil
```

**`toolCallMsg`（约第 113 行）：**
```go
case toolCallMsg:
	m.isGenerating = false
	m.activeAssistant = -1
	// … 其余现有代码不变
```

**`doneMsg`（约第 148 行）：**
```go
case doneMsg:
	m.isGenerating = false
	m.activeAssistant = -1
	m.refreshViewport()
	return m, nil
```

**`turnFinishedMsg`（约第 152 行）：**
```go
case turnFinishedMsg:
	m.isGenerating = false
	m.queryGuard.FinishModel()
	// … 其余现有代码不变
```

- [ ] **Step 3: 确认编译通过**

```bash
cd /Users/united_pooh/PyProjects/go-code && go build ./internal/ui/bubble/... 2>&1
```

Expected: 无输出（编译成功）。

- [ ] **Step 4: Commit**

```bash
cd /Users/united_pooh/PyProjects/go-code && git add internal/ui/bubble/types.go internal/ui/bubble/app.go
git commit -m "feat :sparkles: : 新增 isGenerating 字段追踪模型推理/输出阶段"
```

---

### Task 3: 重写 context_meter.go

**Files:**
- Modify: `internal/ui/bubble/context_meter.go`

- [ ] **Step 1: 更新 `formatContextUsageLabel` 签名与实现（第 38-45 行）**

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

- [ ] **Step 2: 更新 `contextMeterLine`（第 22-36 行）**

删除所有 session 变量，改用 `m.isGenerating`：

```go
func (m appModel) contextMeterLine(width int) string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := maxInt(0, stats.UsedTokens)
	cache := clampInt(stats.CacheTokens, 0, used)
	usedLabel := formatContextUsageLabel(used, cache, limit, m.isGenerating)
	freeLabel := formatContextFreeLabel(used, limit)
	barUsed := clampInt(used, 0, limit)
	barCache := clampInt(cache, 0, barUsed)
	animatedUsed, animatedCache, pulse := m.animatedContextTokens(limit)
	return renderContextMeterLine(width, usedLabel, freeLabel, animatedUsed, animatedCache, limit, m.thinkingLabel(), pulse)
}
```

- [ ] **Step 3: 清理 `animatedContextTokens` dead params（第 229 行）**

函数签名：
```go
func (m appModel) animatedContextTokens(limit int) (int, int, float64) {
```

函数体不变（`used`/`cache` 参数从未在函数体中使用，直接删除即可）。

更新 `updateContextMeterAnimation`（第 221 行）中的两处调用：
```go
// 原：m.animatedContextTokens(m.contextMeter.targetUsed, m.contextMeter.targetCache, limit)
// 改：
currentUsed, currentCache, _ := m.animatedContextTokens(limit)
```

- [ ] **Step 4: 确认编译**

```bash
cd /Users/united_pooh/PyProjects/go-code && go build ./... 2>&1
```

Expected: 编译成功（bubble_test 可能因旧字段引用失败，Task 4 修复）。

- [ ] **Step 5: Commit**

```bash
cd /Users/united_pooh/PyProjects/go-code && git add internal/ui/bubble/context_meter.go
git commit -m "fix :bug: : 重写 context meter 标签：单数字+动态箭头，清理 dead params"
```

---

### Task 4: 修复存量测试 + 新增中文测试

**Files:**
- Modify: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: 修复 fakeRunner.stats 中的旧字段引用**

找到 `fakeRunner` struct（第 28 行）中 `stats loop.ContextStats`，确认它仍然有效（loop.ContextStats 现在只有 3 字段）。

找到所有用到 `SessionUsedTokens`/`SessionCacheTokens`/`SessionOutputTokens`/`OutputTokens` 的地方并删除：

**TestContextMeterUsesDefaultLimitAndStableSegments（约第 1598-1636 行）：**

```go
runner := &fakeRunner{
    stats: loop.ContextStats{
        UsedTokens:  262144,
        CacheTokens: 104858,
        // LimitTokens 为 0，使用 DefaultContextLimitTokens
    },
}
```

`want` 列表改为（删去 `"2.05k↓"`，`↑` 数字变为 `UsedTokens` 本身）：
```go
for _, want := range []string{"260k↑", "25%(10%)", "free(75%)"} {
```

**TestContextMeterShowsCumulativeCountsBeyondLimit（约第 1638-1658 行）：**

```go
runner := &fakeRunner{
    stats: loop.ContextStats{
        UsedTokens:  800,
        CacheTokens: 100,
        LimitTokens: 1000,
    },
}
```

`want` 列表改为（`↑` 数字 = UsedTokens = 800，去掉 `"1.3k↑"` 和 `"200↓"`）：
```go
for _, want := range []string{"800↑", "80%(10%)", "free(20%)"} {
```

- [ ] **Step 2: 新增中文场景测试（追加到 bubble_test.go 末尾）**

```go
// TestContextMeter_空闲时显示上箭头 验证 isGenerating=false 时标签含 ↑ 不含 ↓。
func TestContextMeter_空闲时显示上箭头(t *testing.T) {
	runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 1000, LimitTokens: 200000}}
	model := newTestModel(runner)
	model.isGenerating = false

	label := formatContextUsageLabel(1000, 0, 200000, model.isGenerating)
	if !strings.Contains(label, "↑") {
		t.Errorf("空闲时标签应含 ↑，实际: %q", label)
	}
	if strings.Contains(label, "↓") {
		t.Errorf("空闲时标签不应含 ↓，实际: %q", label)
	}
}

// TestContextMeter_推理输出时显示下箭头 验证 isGenerating=true（thinking/输出）时标签含 ↓。
func TestContextMeter_推理输出时显示下箭头(t *testing.T) {
	label := formatContextUsageLabel(1000, 0, 200000, true)
	if !strings.Contains(label, "↓") {
		t.Errorf("推理时标签应含 ↓，实际: %q", label)
	}
	if strings.Contains(label, "↑") {
		t.Errorf("推理时标签不应含 ↑，实际: %q", label)
	}
}

// TestContextMeter_工具调用后恢复上箭头 验证工具调用后 isGenerating 恢复 false → ↑。
func TestContextMeter_工具调用后恢复上箭头(t *testing.T) {
	runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 1200, LimitTokens: 200000}}
	model := newTestModel(runner)
	model.isGenerating = true // 先模拟推理中

	// 模拟 toolCallMsg 处理后 isGenerating 被清零
	model.isGenerating = false

	label := formatContextUsageLabel(1200, 0, 200000, model.isGenerating)
	if !strings.Contains(label, "↑") {
		t.Errorf("工具调用后标签应含 ↑，实际: %q", label)
	}
}

// TestContextMeter_零token时不崩溃 验证 UsedTokens=0 时安全返回 "0↑"。
func TestContextMeter_零token时不崩溃(t *testing.T) {
	label := formatContextUsageLabel(0, 0, 200000, false)
	if !strings.Contains(label, "0↑") {
		t.Errorf("零 token 标签应为 0↑，实际: %q", label)
	}
}

// TestContextMeter_多轮后数字只反映当前context 验证数字等于 UsedTokens 而非 session 累加。
func TestContextMeter_多轮后数字只反映当前context(t *testing.T) {
	// 第 3 轮结束，当前 context = 2000，不是 3 轮累加值
	runner := &fakeRunner{stats: loop.ContextStats{UsedTokens: 2000, LimitTokens: 200000}}
	model := newTestModel(runner)
	meter := model.contextMeterLine(60)
	if !strings.Contains(meter, "2k↑") {
		t.Errorf("多轮后 meter 应含当前 context 大小 2k↑，实际: %q", meter)
	}
	// 确保没有错误的累加值出现（如 "6k↑"）
	if strings.Contains(meter, "6k") {
		t.Errorf("meter 不应出现 session 累加值 6k，实际: %q", meter)
	}
}

// TestContextMeter_thinking时isGenerating为true 验证 app.go 里 thinkingDeltaMsg 设置 isGenerating。
func TestContextMeter_thinking时isGenerating为true(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if model.isGenerating {
		t.Fatalf("初始 isGenerating 应为 false")
	}
	// 模拟收到 thinkingDeltaMsg
	updated, _ := model.Update(thinkingDeltaMsg("思考中..."))
	next := updated.(appModel)
	if !next.isGenerating {
		t.Errorf("收到 thinkingDeltaMsg 后 isGenerating 应为 true")
	}
}
```

- [ ] **Step 3: 运行全部测试**

```bash
cd /Users/united_pooh/PyProjects/go-code && go test ./... -v 2>&1 | grep -E "FAIL|PASS|ERROR" | tail -30
```

Expected: 全部 PASS，无 FAIL。

- [ ] **Step 4: Commit**

```bash
cd /Users/united_pooh/PyProjects/go-code && git add internal/ui/bubble/bubble_test.go
git commit -m "test :white_check_mark: : 修复存量测试 + 新增 context meter 中文场景测试"
```

---

### Task 5: 推送

- [ ] **Step 1: 确认全部测试通过**

```bash
cd /Users/united_pooh/PyProjects/go-code && go test ./... 2>&1 | tail -10
```

Expected: `ok` 行，无 FAIL。

- [ ] **Step 2: 推送**

```bash
cd /Users/united_pooh/PyProjects/go-code && git push
```
