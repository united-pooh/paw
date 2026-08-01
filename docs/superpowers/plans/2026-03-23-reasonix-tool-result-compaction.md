# Reasonix Tool Result 上下文压缩对齐实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为 Paw 增加 DeepSeek-Reasonix 风格的分阶段上下文维护：旧大型 Tool Result 先归档并 snip/prune，空间仍不足时才摘要，同时支持 cold resume、默认错误/用户标记保留和防重复压缩。

**架构：** 在 `internal/loop` 中拆出配置映射、归档、Tool Result 维护和压力编排四个专注单元；现有 `context_compaction.go` 继续负责语义 fold。append-only session journal 保持完整，所有维护只改 Runner 的内存 history 投影，恢复历史和运行中历史复用同一维护入口。

**技术栈：** Go、现有 `message.Message`/`session.JSONLStore`/`tool.Registry`、JSONL、SHA-256、标准库文件 I/O、表驱动测试。

---

## 文件结构

### 创建

- `internal/loop/context_maintenance_config.go`：loop 层运行时配置、默认值、settings 映射和严格校验。
- `internal/loop/context_maintenance_config_test.go`：默认值、阈值顺序、非法配置测试。
- `internal/loop/compaction_archive.go`：安全 session 目录、JSONL 写入、哈希索引、归档复用。
- `internal/loop/compaction_archive_test.go`：归档原子性、去重、索引损坏和路径安全测试。
- `internal/loop/tool_result_maintenance.go`：stale 结果识别、keep policy、调用组保护、snip/prune 和 marker 解析。
- `internal/loop/tool_result_maintenance_test.go`：snip/prune、UTF-8、批量结果、错误保留、工具 hint 测试。
- `internal/loop/context_pressure.go`：50/60/80/90 水位编排、经济性检查、通知与 stuck 状态。
- `internal/loop/context_pressure_test.go`：各水位、prune 免摘要、强制摘要、防循环测试。
- `internal/loop/context_resume_test.go`：真实 JSONL store 的 cold-resume 集成测试。

### 修改

- `internal/settings/settings.go`：增加持久化的 context maintenance 配置并在 Load/Save 时严格校验。
- `internal/settings/settings_test.go`：默认配置、配置文件 round-trip、非法配置拒绝测试。
- `internal/tool/tool.go`：增加可选 `ReadOnlyTool`、`SnipHinter`、`SnipHint`。
- `internal/tool/exec/bash.go`：声明有副作用及均衡首尾裁剪策略。
- `internal/tool/webfetch/webfetch.go`：声明只读及头部优先裁剪策略。
- `internal/tool/mcp/tool.go`：显式声明未知 MCP 工具按有副作用策略处理。
- `internal/loop/runner.go`：增加配置、archive、soft notice、连续压缩状态；接入 cold resume 和每轮维护。
- `internal/loop/context_compaction.go`：keep/fold 分区、fold archive、mechanical fallback、结果统计和固定 tail 配置化。
- `internal/loop/context_compaction_test.go`：保留组、手动 fallback、fold archive 和 transcript 回归测试。
- `cmd/agent/main.go`：把 settings 中的维护配置注入 Runner。
- `cmd/agent/main_test.go` 或现有 `cmd/agent/register_test.go`：验证 buildRunner 配置注入及默认 archive 根路径。
- `internal/ui/bubble/command_helpers.go`：若 `/compact` 的结果文案在此生成，加入 archive/mechanical 统计。
- `internal/ui/bubble/bubble_test.go`：更新 `ContextCompactionResult` 测试夹具与 `/compact` 展示断言。

---

### 任务 1：增加配置与工具裁剪能力接口

**文件：**
- 修改：`internal/settings/settings.go:14-136`
- 修改：`internal/settings/settings_test.go`
- 修改：`internal/tool/tool.go:8-30`
- 创建：`internal/loop/context_maintenance_config.go`
- 创建：`internal/loop/context_maintenance_config_test.go`

- [ ] **步骤 1：为 settings 默认值和严格校验编写失败测试**

在 `internal/settings/settings_test.go` 增加：

```go
func TestDefaultConfigEnablesReasonixContextMaintenance(t *testing.T) {
    cfg := DefaultConfig()
    got := cfg.ContextMaintenance
    if got.SoftCompactRatio != 0.50 || got.ToolResultSnipRatio != 0.60 ||
        got.CompactRatio != 0.80 || got.CompactForceRatio != 0.90 ||
        got.CompactTargetRatio != 0.50 {
        t.Fatalf("unexpected ratios: %+v", got)
    }
    if got.TailTokens != 16384 || got.MinToolResultBytes != 1024 {
        t.Fatalf("unexpected budgets: %+v", got)
    }
    if !got.KeepErrors || !got.KeepUserMarked || !got.ArchiveEnabled {
        t.Fatalf("maintenance must default on: %+v", got)
    }
}

func TestLoadRejectsInvalidContextMaintenanceOrder(t *testing.T) {
    path := filepath.Join(t.TempDir(), "settings.json")
    raw := `{
      "context_maintenance": {
        "soft_compact_ratio": 0.7,
        "tool_result_snip_ratio": 0.6,
        "compact_ratio": 0.8,
        "compact_force_ratio": 0.9,
        "compact_target_ratio": 0.5,
        "tail_tokens": 16384,
        "min_tool_result_bytes": 1024,
        "keep_errors": true,
        "keep_user_marked": true,
        "archive_enabled": true
      }
    }`
    if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
        t.Fatal(err)
    }
    if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "soft_compact_ratio") {
        t.Fatalf("Load() error = %v", err)
    }
}
```

- [ ] **步骤 2：运行 settings 测试确认失败**

运行：

```bash
go test ./internal/settings -run 'Test(DefaultConfigEnablesReasonixContextMaintenance|LoadRejectsInvalidContextMaintenanceOrder)' -count=1
```

预期：FAIL，提示 `Config.ContextMaintenance` 或 `ContextMaintenanceConfig` 未定义。

- [ ] **步骤 3：实现 settings 配置结构、默认值和 Validate**

在 `internal/settings/settings.go` 增加：

```go
type ContextMaintenanceConfig struct {
    SoftCompactRatio    float64 `json:"soft_compact_ratio"`
    ToolResultSnipRatio float64 `json:"tool_result_snip_ratio"`
    CompactRatio        float64 `json:"compact_ratio"`
    CompactForceRatio   float64 `json:"compact_force_ratio"`
    CompactTargetRatio  float64 `json:"compact_target_ratio"`
    TailTokens          int     `json:"tail_tokens"`
    MinToolResultBytes  int     `json:"min_tool_result_bytes"`
    KeepErrors          bool    `json:"keep_errors"`
    KeepUserMarked      bool    `json:"keep_user_marked"`
    ArchiveEnabled      bool    `json:"archive_enabled"`
}
```

把字段加入 `Config`：

```go
type Config struct {
    Subagent           SubagentConfig           `json:"subagent"`
    UI                 UIConfig                 `json:"ui"`
    ContextMaintenance ContextMaintenanceConfig `json:"context_maintenance"`
}
```

增加默认构造和严格校验：

```go
func DefaultContextMaintenanceConfig() ContextMaintenanceConfig {
    return ContextMaintenanceConfig{
        SoftCompactRatio: 0.50, ToolResultSnipRatio: 0.60,
        CompactRatio: 0.80, CompactForceRatio: 0.90,
        CompactTargetRatio: 0.50, TailTokens: 16384,
        MinToolResultBytes: 1024, KeepErrors: true,
        KeepUserMarked: true, ArchiveEnabled: true,
    }
}

func Validate(cfg Config) error {
    c := cfg.ContextMaintenance
    if !(c.SoftCompactRatio > 0 && c.SoftCompactRatio <= c.ToolResultSnipRatio) {
        return fmt.Errorf("context_maintenance.soft_compact_ratio must be > 0 and <= tool_result_snip_ratio")
    }
    if c.ToolResultSnipRatio > c.CompactRatio {
        return fmt.Errorf("context_maintenance.tool_result_snip_ratio must be <= compact_ratio")
    }
    if c.CompactRatio > c.CompactForceRatio || c.CompactForceRatio >= 1 {
        return fmt.Errorf("context_maintenance ratios must satisfy compact_ratio <= compact_force_ratio < 1")
    }
    if c.CompactTargetRatio <= 0 || c.CompactTargetRatio >= c.CompactRatio {
        return fmt.Errorf("context_maintenance.compact_target_ratio must be > 0 and < compact_ratio")
    }
    if c.TailTokens <= 0 || c.MinToolResultBytes <= 0 {
        return fmt.Errorf("context_maintenance token and byte budgets must be positive")
    }
    return nil
}
```

`DefaultConfig` 填充默认值；`Load` 在 `Normalize` 后调用 `Validate`；`Save` 在写文件前调用 `Validate`。不要用 Normalize 静默修复非法 context maintenance 数值。

- [ ] **步骤 4：定义 loop 运行时配置和 settings 映射测试**

在新文件 `internal/loop/context_maintenance_config_test.go` 增加：

```go
func TestContextMaintenanceConfigFromSettings(t *testing.T) {
    in := settings.DefaultContextMaintenanceConfig()
    got, err := contextMaintenanceConfigFromSettings(in)
    if err != nil {
        t.Fatal(err)
    }
    if got.tailTokens != 16384 || !got.keepErrors || !got.archiveEnabled {
        t.Fatalf("mapped config = %+v", got)
    }
}
```

运行：

```bash
go test ./internal/loop -run TestContextMaintenanceConfigFromSettings -count=1
```

预期：FAIL，提示映射函数未定义。

- [ ] **步骤 5：实现 loop 内部配置**

在 `internal/loop/context_maintenance_config.go` 定义未导出的运行时结构和映射：

```go
type contextMaintenanceConfig struct {
    softCompactRatio    float64
    toolResultSnipRatio float64
    compactRatio        float64
    compactForceRatio   float64
    compactTargetRatio  float64
    tailTokens          int
    minToolResultBytes  int
    keepErrors          bool
    keepUserMarked      bool
    archiveEnabled      bool
}

func contextMaintenanceConfigFromSettings(in settings.ContextMaintenanceConfig) (contextMaintenanceConfig, error)
```

复用 `settings.Validate` 的同一约束，不在 loop 层另设不同默认值。增加 Runner 公共注入方法的签名，具体字段接入在任务 6 完成：

```go
func (runner *Runner) SetContextMaintenanceConfig(cfg settings.ContextMaintenanceConfig) error
```

- [ ] **步骤 6：增加可选工具能力接口**

在 `internal/tool/tool.go` 增加：

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

基础 `Tool` 接口保持不变。

- [ ] **步骤 7：运行任务 1 测试**

运行：

```bash
go test ./internal/settings ./internal/loop ./internal/tool -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/settings/settings.go internal/settings/settings_test.go \
  internal/tool/tool.go \
  internal/loop/context_maintenance_config.go \
  internal/loop/context_maintenance_config_test.go
git commit -m "✨ feat(loop): add context maintenance configuration"
```

---

### 任务 2：实现安全、可去重的 JSONL compaction archive

**文件：**
- 创建：`internal/loop/compaction_archive.go`
- 创建：`internal/loop/compaction_archive_test.go`

- [ ] **步骤 1：编写归档成功与去重的失败测试**

在 `internal/loop/compaction_archive_test.go` 增加：

```go
func TestCompactionArchiveWritesOriginalMessageAndReusesHash(t *testing.T) {
    root := t.TempDir()
    archive, err := newCompactionArchive(root, "session-1", true)
    if err != nil {
        t.Fatal(err)
    }
    msg := message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
        ToolUseID: "call-1", Content: strings.Repeat("x", 2048),
    }}
    req := archiveRequest{
        Operation: "snip", MessageIndex: 3, ToolResultIndex: 0,
        ToolUseID: "call-1", ToolName: "Read", OriginalBytes: 2048,
        Message: msg, OriginalContent: msg.ToolResult.Content,
    }
    first, err := archive.archive([]archiveRequest{req})
    if err != nil {
        t.Fatal(err)
    }
    second, err := archive.archive([]archiveRequest{req})
    if err != nil {
        t.Fatal(err)
    }
    if first.Paths[0] != second.Paths[0] {
        t.Fatalf("archive path not reused: %q != %q", first.Paths[0], second.Paths[0])
    }
    data, err := os.ReadFile(first.Paths[0])
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Contains(data, []byte(strings.Repeat("x", 64))) {
        t.Fatal("archive does not contain original content")
    }
}
```

再增加路径安全测试：

```go
func TestCompactionArchiveSanitizesSessionID(t *testing.T) {
    archive, err := newCompactionArchive(t.TempDir(), "../../escape", true)
    if err != nil {
        t.Fatal(err)
    }
    if strings.Contains(filepath.ToSlash(archive.dir), "../") {
        t.Fatalf("unsafe archive dir: %s", archive.dir)
    }
}
```

- [ ] **步骤 2：运行归档测试确认失败**

运行：

```bash
go test ./internal/loop -run TestCompactionArchive -count=1
```

预期：FAIL，提示 `newCompactionArchive`、`archiveRequest` 未定义。

- [ ] **步骤 3：实现归档数据结构和安全目录**

在 `internal/loop/compaction_archive.go` 定义：

```go
type compactionArchive struct {
    dir       string
    sessionID string
    enabled   bool
    now       func() time.Time
}

type archiveRequest struct {
    Operation       string
    MessageIndex    int
    ToolResultIndex int
    ToolUseID       string
    ToolName        string
    OriginalBytes   int
    Message         message.Message
    OriginalContent string
}

type archiveResult struct {
    Paths []string
    ByKey map[string]string
}
```

目录固定为：

```go
filepath.Join(workRoot, ".paw", "sessions", safeSessionID(sessionID), "compactions")
```

`safeSessionID` 只允许字母、数字、`.`、`_`、`-`，其他 rune 替换为 `_`；空结果使用 `session`。创建后通过 `filepath.Rel` 验证目标未逃出 `workRoot/.paw/sessions`。

- [ ] **步骤 4：实现 JSONL 写入、Sync 和哈希索引**

归档键：

```go
func archiveKey(sessionID, toolUseID, content string) string {
    sum := sha256.Sum256([]byte(sessionID + "\x00" + toolUseID + "\x00" + content))
    return hex.EncodeToString(sum[:])
}
```

`archive` 的顺序必须是：

1. 读取 `index.jsonl` 中可解析的记录；损坏行跳过；
2. 复用已有合法路径；
3. 把新记录写入同一个时间戳临时文件；
4. `file.Sync()`、`file.Close()`；
5. 原子 rename 为 `YYYYMMDD-HHMMSS.mmm-<operation>.jsonl`；
6. 追加 index 记录并 `Sync()`；
7. 返回路径映射。

归档记录结构：

```go
type archiveRecord struct {
    Operation       string          `json:"operation"`
    SessionID       string          `json:"session_id"`
    MessageIndex    int             `json:"message_index"`
    ToolResultIndex int             `json:"tool_result_index"`
    ToolUseID       string          `json:"tool_use_id"`
    ToolName        string          `json:"tool_name"`
    OriginalBytes   int             `json:"original_bytes"`
    Message         message.Message `json:"message"`
    CreatedAt       time.Time       `json:"created_at"`
}
```

- [ ] **步骤 5：编写失败不改写上层状态所需的注入测试**

给 `compactionArchive` 增加可测试的 `openFile` 或 `writeFile` 函数字段。在测试中让它返回 `errors.New("disk full")`，断言 `archive` 返回错误且没有最终 `.jsonl` 文件。

运行：

```bash
go test ./internal/loop -run 'TestCompactionArchive(Writes|Sanitizes|Failure)' -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/loop/compaction_archive.go internal/loop/compaction_archive_test.go
git commit -m "✨ feat(loop): archive compacted context records"
```

---

### 任务 3：实现调用组边界、Keep Policy 与 recent tail 规划

**文件：**
- 创建：`internal/loop/tool_result_maintenance.go`
- 创建：`internal/loop/tool_result_maintenance_test.go`
- 修改：`internal/loop/context_compaction.go:193-264`

- [ ] **步骤 1：编写错误调用组和用户 keep marker 的失败测试**

构造一条 assistant 多工具调用和一条批量结果消息：

```go
func TestKeepIndexesPreserveErrorToolCallGroup(t *testing.T) {
    history := []message.Message{
        {Role: message.RoleAssistant, ToolUses: []message.ToolCall{
            {ID: "a", Name: "Read"}, {ID: "b", Name: "Bash"},
        }},
        {Role: message.RoleUser, ToolResults: []message.ToolResult{
            {ToolUseID: "a", Content: "ok"},
            {ToolUseID: "b", Content: "error: build failed", IsError: true},
        }},
    }
    keep := keepMessageIndexes(history, keepPolicy{errors: true, userMarked: true})
    if !keep[0] || !keep[1] {
        t.Fatalf("tool group not preserved: %v", keep)
    }
}

func TestIsUserMarkedMessage(t *testing.T) {
    for _, text := range []string{" [[KEEP]] fact", "[keep] fact", "<keep>fact", "<!-- keep --> fact"} {
        if !isUserMarkedMessage(message.Message{Role: message.RoleUser, Content: text}) {
            t.Fatalf("not marked: %q", text)
        }
    }
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：

```bash
go test ./internal/loop -run 'Test(KeepIndexes|IsUserMarked)' -count=1
```

预期：FAIL，相关 helper 未定义。

- [ ] **步骤 3：实现 keep policy 与调用组查找**

在 `tool_result_maintenance.go` 增加：

```go
type keepPolicy struct {
    errors     bool
    userMarked bool
}

func isErrorToolResult(result message.ToolResult) bool {
    if result.IsError {
        return true
    }
    text := strings.ToLower(strings.TrimSpace(result.Content))
    return strings.HasPrefix(text, "error:") || strings.HasPrefix(text, "blocked:")
}
```

`keepMessageIndexes` 必须：

- 只对最后一个 compaction summary 之后的区域应用 policy；
- 标记 `[keep]` 用户消息；
- 若结果消息中任一 result 是错误，则标记该结果消息；
- 向前查找包含对应 `ToolUseID` 的 assistant 消息；
- 标记 assistant 消息及其紧随的整条批量结果消息，不拆兄弟 result。

- [ ] **步骤 4：编写 recent tail 不拆调用组的失败测试**

```go
func TestProtectedTailStartMovesBeforeToolCallGroup(t *testing.T) {
    history := []message.Message{
        {Role: message.RoleSystem, Content: "sys"},
        {Role: message.RoleUser, Content: "task"},
        {Role: message.RoleAssistant, ToolUses: []message.ToolCall{{ID: "a", Name: "Read"}}},
        {Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "a", Content: strings.Repeat("x", 2000)}},
        {Role: message.RoleAssistant, Content: "done"},
    }
    start := protectedTailStart(history, 1, 1, 2)
    if start != 2 {
        t.Fatalf("tail start = %d, want assistant tool call at 2", start)
    }
}
```

- [ ] **步骤 5：实现 token-budget recent tail**

实现：

```go
func protectedTailStart(history []message.Message, head, budgetTokens, minMessages int) int
```

从新到旧累加 `estimateMessageTokens`，至少保留 `minMessages`；若边界落在含 Tool Result 的 user 消息上，向前移动到对应 assistant Tool Call。若 assistant 有多条 Tool Call，整条 assistant 和紧随的结果消息视为一组。

把 `planHistoryCompaction` 改为接收配置：

```go
func planHistoryCompaction(history []message.Message, limit int, cfg contextMaintenanceConfig) (head, tail int)
```

预算使用：

```go
budget := min(cfg.tailTokens, int(float64(limit)*cfg.compactTargetRatio))
```

- [ ] **步骤 6：扩展 compaction partition 的保留规则**

修改签名：

```go
func partitionCompactionRegion(region []message.Message, limit int, policy keepPolicy) (kept, fold []message.Message)
```

原样保留：既有 summary、小型无 Tool Result 用户消息、用户 marked 消息、错误工具调用组。测试断言顺序不变。

- [ ] **步骤 7：运行任务 3 测试**

```bash
go test ./internal/loop -run 'Test(KeepIndexes|IsUserMarked|ProtectedTail|PartitionCompaction)' -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/loop/tool_result_maintenance.go \
  internal/loop/tool_result_maintenance_test.go \
  internal/loop/context_compaction.go
git commit -m "✨ feat(loop): preserve tool groups during context cleanup"
```

---

### 任务 4：实现 Tool Result snip/prune 和 SnipHint 解析

**文件：**
- 修改：`internal/loop/tool_result_maintenance.go`
- 修改：`internal/loop/tool_result_maintenance_test.go`
- 修改：`internal/tool/exec/bash.go`
- 修改：`internal/tool/webfetch/webfetch.go`
- 修改：`internal/tool/mcp/tool.go`

- [ ] **步骤 1：编写 snip、prune、批量结果和 UTF-8 失败测试**

增加表驱动测试，至少包含：

```go
func TestMaintainToolResultsSnipsOnlyStaleLargeResults(t *testing.T) {
    old := strings.Repeat("旧结果\n", 600)
    recent := strings.Repeat("recent\n", 600)
    history := maintenanceFixture(old, recent)
    archive := mustTestArchive(t)
    got, stats, err := maintainToolResults(history, maintenanceRequest{
        mode: maintenanceSnip, tailStart: len(history) - 2,
        minBytes: 1024, policy: keepPolicy{errors: true, userMarked: true},
        archive: archive, registry: tool.NewRegistry(),
    })
    if err != nil {
        t.Fatal(err)
    }
    if stats.results != 1 || !strings.HasPrefix(toolResultsFromMessage(got[3])[0].Content, snippedToolResultMarker) {
        t.Fatalf("stats=%+v history=%+v", stats, got)
    }
    if toolResultsFromMessage(got[len(got)-1])[0].Content != recent {
        t.Fatal("recent result was rewritten")
    }
}

func TestMaintainToolResultsPreservesMessageShape(t *testing.T) {
    // 单结果仍使用 ToolResult；批量结果仍使用 ToolResults，且只改 Content。
}

func TestSnipToolResultKeepsValidUTF8(t *testing.T) {
    got := snipToolResult("Read", strings.Repeat("你好世界", 1000), "archive.jsonl", snipStrategy{headChars: 11, tailChars: 9})
    if !utf8.ValidString(got) {
        t.Fatal("invalid UTF-8")
    }
}
```

- [ ] **步骤 2：运行测试确认失败**

```bash
go test ./internal/loop -run 'Test(MaintainToolResults|SnipToolResult)' -count=1
```

预期：FAIL，maintenance 类型和函数未定义。

- [ ] **步骤 3：实现维护模式、marker 和结果复制**

增加：

```go
const (
    snippedToolResultMarker = "[snipped tool result — "
    prunedToolResultMarker  = "[elided tool result — "
)

type maintenanceMode uint8
const (
    maintenanceSnip maintenanceMode = iota + 1
    maintenancePrune
)

type maintenanceStats struct {
    results    int
    savedChars int
    archives   []string
}

type maintenanceRequest struct {
    mode      maintenanceMode
    tailStart int
    minBytes  int
    policy    keepPolicy
    archive   *compactionArchive
    registry  *tool.Registry
}
```

深拷贝每条可能改写的 `message.Message` 及 `ToolResult(s)`，先收集全部 archive request；归档全部成功后再统一替换 Content。archive 返回错误时直接返回原 `history` 和零 stats。

- [ ] **步骤 4：实现多行/单行 snip 与 prune marker 解析**

实现以下函数：

```go
func snipToolResult(toolName, content, archivePath string, strategy snipStrategy) string
func pruneToolResult(toolName, content, archivePath string) string
func parseToolResultMarker(content string) (toolResultMarker, bool)
func firstUTF8Bytes(s string, n int) string
func lastUTF8Bytes(s string, n int) string
```

marker parser 只接受本代码生成的完整格式；非法 marker 返回 `false`。prune 已 snip 内容时使用 marker 中的原始字节数和 archive 路径，不对 snipped 文本重新归档。

- [ ] **步骤 5：编写 SnipHinter 优先级失败测试**

定义测试工具：

```go
type hintedTool struct{ fakeTool }
func (hintedTool) SnipHint() tool.SnipHint {
    return tool.SnipHint{Head: 3, Tail: 2, HeadChars: 100, TailChars: 50}
}
```

断言：显式 hint 优先；只读默认 `80/12`；未知默认 `40/40`；负数 hint 回退默认。

- [ ] **步骤 6：实现工具策略解析**

```go
type snipStrategy struct {
    head, tail           int
    headChars, tailChars int
}

func snipStrategyFor(registry *tool.Registry, name string) snipStrategy
```

默认：

```go
var defaultReadOnlySnip = snipStrategy{head: 80, tail: 12, headChars: 10000, tailChars: 2000}
var defaultSideEffectingSnip = snipStrategy{head: 40, tail: 40, headChars: 8000, tailChars: 8000}
```

- [ ] **步骤 7：为内置工具声明能力**

`BashTool`：

```go
func (*BashTool) ReadOnly() bool { return false }
func (*BashTool) SnipHint() tool.SnipHint {
    return tool.SnipHint{Head: 40, Tail: 40, HeadChars: 8000, TailChars: 8000}
}
```

`webfetch.Tool`：

```go
func (*Tool) ReadOnly() bool { return true }
func (*Tool) SnipHint() tool.SnipHint {
    return tool.SnipHint{Head: 80, Tail: 12, HeadChars: 10000, TailChars: 2000}
}
```

MCP adapter 显式返回 `ReadOnly() == false`，因为协议元数据当前不能可靠声明副作用。

- [ ] **步骤 8：运行任务 4 测试**

```bash
go test ./internal/loop ./internal/tool/exec ./internal/tool/webfetch ./internal/tool/mcp -count=1
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/loop/tool_result_maintenance.go \
  internal/loop/tool_result_maintenance_test.go \
  internal/tool/exec/bash.go internal/tool/webfetch/webfetch.go \
  internal/tool/mcp/tool.go
git commit -m "✨ feat(loop): snip and prune stale tool results"
```

---

### 任务 5：实现 50/60/80/90 压力维护管线

**文件：**
- 创建：`internal/loop/context_pressure.go`
- 创建：`internal/loop/context_pressure_test.go`
- 修改：`internal/loop/context_compaction.go`

- [ ] **步骤 1：编写各水位的失败测试**

用 fake summarizer 记录调用次数，构造可控 token 历史。测试表：

```go
func TestMaintainContextProjectionThresholds(t *testing.T) {
    tests := []struct {
        name          string
        ratio         float64
        wantSnip      bool
        wantPrune     bool
        wantSummaries int
    }{
        {"below soft", 0.49, false, false, 0},
        {"soft only", 0.50, false, false, 0},
        {"snip", 0.60, true, false, 0},
        {"prune clears pressure", 0.80, false, true, 0},
        {"force compact", 0.90, false, true, 1},
    }
    // 每个 case 设置 context limit 与 history，使 estimateMessageTokens 接近 ratio。
}
```

另写：

```go
func TestPruneAvoidsSummaryWhenReestimatedBelowThreshold(t *testing.T)
func TestNonForcedCompactionSkipsUneconomicFold(t *testing.T)
func TestForcedCompactionBypassesEconomics(t *testing.T)
```

- [ ] **步骤 2：运行压力测试确认失败**

```bash
go test ./internal/loop -run 'Test(MaintainContextProjection|PruneAvoids|NonForced|ForcedCompaction)' -count=1
```

预期：FAIL，`maintainContextProjection` 未定义。

- [ ] **步骤 3：实现统一维护结果和入口**

在 `context_pressure.go` 定义：

```go
type contextMaintenanceResult struct {
    history              []message.Message
    compaction           *ContextCompactionResult
    snippedResults       int
    prunedResults        int
    estimatedTokensSaved int
    archivePaths         []string
    summaryPerformed     bool
}

func (runner *Runner) maintainContextProjection(
    ctx context.Context,
    history []message.Message,
    allowSummary bool,
) (contextMaintenanceResult, error)
```

入口读取 Runner 当前配置和 context limit，初始压力取：

```go
estimated := estimateMessageTokens(history)
promptTokens := estimated
if usageKnown && usage.PromptTokenCount() > promptTokens {
    promptTokens = usage.PromptTokenCount()
}
```

一旦发生 snip/prune，后续压力只使用 `estimateMessageTokens(rewrittenHistory)`。

- [ ] **步骤 4：实现阶段分支和通知**

逻辑严格按：

```go
switch {
case promptTokens < soft:
    runner.resetContextPressureState()
case promptTokens < snip:
    runner.noticeSoftContextOnce()
case promptTokens < compact:
    // snip only
case promptTokens < force:
    // prune, re-estimate, optional economic summary
default:
    // prune, force summary
}
```

`allowSummary=false` 只允许 soft/snip/prune，用于需要禁止网络调用的恢复或测试路径；正常 cold resume 传 `true`。

- [ ] **步骤 5：实现 fold economics**

```go
func foldEconomics(messages []message.Message) bool {
    return estimateMessageTokens(messages) >= 400
}
```

非强制 summary 前只计算真正 foldable 的消息，不把 kept user/error 组计入潜在收益。

- [ ] **步骤 6：实现连续压缩 stuck 测试与状态机**

测试：连续两次 `summaryPerformed=true` 后第三次不再调用 summarizer；history 压力降到 compact ratio 以下后解除 stuck；stuck 时大工具结果仍会 prune。

Runner 状态字段在任务 6 正式接入，当前先实现 helper：

```go
func (runner *Runner) recordAutomaticCompaction(performed bool, belowThreshold bool)
```

- [ ] **步骤 7：运行任务 5 测试**

```bash
go test ./internal/loop -run 'Test.*(Threshold|Prune|Forced|Econom|Stuck)' -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/loop/context_pressure.go internal/loop/context_pressure_test.go \
  internal/loop/context_compaction.go
git commit -m "✨ feat(loop): stage context cleanup by pressure"
```

---

### 任务 6：接入 Runner、settings 注入与 cold resume

**文件：**
- 修改：`internal/loop/runner.go:68-99,151-164,219-251,321-339`
- 修改：`cmd/agent/main.go:478-558`
- 创建：`internal/loop/context_resume_test.go`
- 修改：`cmd/agent/main_test.go`（若该文件不存在则创建）

- [ ] **步骤 1：编写 Runner 配置注入失败测试**

```go
func TestSetContextMaintenanceConfigInitializesArchive(t *testing.T) {
    runner := NewRunnerWithInstructionRoot(nil, nil, tool.NewRegistry(), nil, "session/unsafe", t.TempDir())
    cfg := settings.DefaultContextMaintenanceConfig()
    if err := runner.SetContextMaintenanceConfig(cfg); err != nil {
        t.Fatal(err)
    }
    if runner.compactionArchive == nil || !runner.contextMaintenance.archiveEnabled {
        t.Fatalf("runner not configured: %+v", runner)
    }
}
```

- [ ] **步骤 2：增加 Runner 状态字段并完成 setter**

在 `Runner` 中增加：

```go
contextMaintenance  contextMaintenanceConfig
compactionArchive   *compactionArchive
softCompactNoticed  bool
consecutiveCompacts int
compactStuck        bool
```

`NewRunnerWithInstructionRoot` 使用 settings 默认配置初始化，保证测试和嵌入调用方不注入时仍得到正确默认行为。`SetContextMaintenanceConfig` 校验后原子替换配置和 archive。

- [ ] **步骤 3：编写 cold-resume 集成失败测试**

在 `context_resume_test.go` 使用真实 store：

```go
func TestColdResumePrunesBeforeFirstModelRequestAndKeepsJournal(t *testing.T) {
    root := t.TempDir()
    store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
    if err != nil { t.Fatal(err) }
    sessionID := "resume-1"
    original := strings.Repeat("tool-output\n", 2000)
    history := coldResumeFixture(original)
    if err := store.Append(context.Background(), sessionID, history...); err != nil { t.Fatal(err) }

    model := &capturingModel{reply: message.Message{Role: message.RoleAssistant, Content: "done"}}
    runner := NewRunnerWithInstructionRoot(model, newTestUI(), tool.NewRegistry(), store, sessionID, root)
    runner.SetContextLimitTokens(estimateMessageTokens(history) + 32)
    if _, err := runner.RunTurn(context.Background(), "continue"); err != nil { t.Fatal(err) }

    if strings.Contains(renderMessages(model.messages), original[:512]) {
        t.Fatal("model received unmaintained tool result")
    }
    persisted, err := store.LoadResolvedHistory(context.Background(), sessionID)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(renderMessages(persisted), original[:512]) {
        t.Fatal("journal lost original tool result")
    }
}
```

- [ ] **步骤 4：在恢复后、追加当前用户输入前运行 cold-resume 管线**

在 `runner.runTurnWithTiming` 的 history 首次加载成功后：

1. 对 `snapshot.ActiveHistory` 或 `LoadResolvedHistory` 结果调用 `maintainContextProjection(ctx, messages, true)`；
2. 把返回的内存投影传给 `setHistoryIfNil`；
3. 不把维护后的消息追加到 store；
4. 保持 `snapshot.Recovery` 原样；
5. 维护失败时通知并继续使用完整恢复 history，除非错误来自强制的手动操作。

这样 cold resume 发生在 `buildTurnHistory(userInput)` 之前。

- [ ] **步骤 5：替换每轮旧的 `maybeCompactHistory` 调用**

在 `round == 0` 路径中调用统一管线：

```go
maintenance, maintainErr := runner.maintainContextProjection(ctx, history, true)
if maintainErr != nil {
    runner.notifySystem("context-compaction", "context cleanup skipped: "+maintainErr.Error())
} else {
    history = maintenance.history
    // 根据 stats 发通知；不要写入 journal。
}
```

确保 cold resume 已维护的 marker 是幂等的，第二次管线不会重复 snip/archive。

- [ ] **步骤 6：在 buildRunner 中注入 settings**

`cmd/agent/main.go` 创建 Runner 后立即执行：

```go
if err := runner.SetContextMaintenanceConfig(settingsController.CurrentSettings().ContextMaintenance); err != nil {
    return nil, "", nil, nil, nil, nil, nil, fmt.Errorf("configure context maintenance: %w", err)
}
```

为此写 buildRunner 级测试，使用临时 HOME/settings 文件提供一个非默认 `tail_tokens`，断言 Runner 的行为采用该值，而不是直接访问私有字段。

- [ ] **步骤 7：运行 cold-resume 与 Runner 测试**

```bash
go test ./internal/loop -run 'Test(SetContextMaintenance|ColdResume)' -count=1
go test ./cmd/agent -run TestBuildRunner -count=1
```

预期：PASS。

- [ ] **步骤 8：Commit**

```bash
git add internal/loop/runner.go internal/loop/context_resume_test.go \
  cmd/agent/main.go cmd/agent/main_test.go
git commit -m "✨ feat(loop): maintain context on resume and turns"
```

---

### 任务 7：增强 summary compaction 的归档、mechanical fallback 和结果统计

**文件：**
- 修改：`internal/loop/context_compaction.go:45-177`
- 修改：`internal/loop/context_compaction_test.go`
- 修改：`internal/ui/bubble/command_helpers.go`
- 修改：`internal/ui/bubble/bubble_test.go`

- [ ] **步骤 1：编写手动 compaction 摘要失败仍成功的失败测试**

```go
func TestManualCompactionUsesMechanicalFoldAfterSummaryFailure(t *testing.T) {
    runner := newCompactionTestRunner(t, failingSummaryModel{err: errors.New("provider down")})
    runner.setHistory(largeFoldableHistory())
    result, err := runner.CompactContext(context.Background(), "keep build state")
    if err != nil {
        t.Fatalf("CompactContext() error = %v", err)
    }
    if !result.Mechanical || result.FoldedMessages == 0 || len(result.ArchivePaths) == 0 {
        t.Fatalf("result = %+v", result)
    }
    if !strings.Contains(result.Summary, "automatic summary was unavailable") {
        t.Fatalf("summary = %q", result.Summary)
    }
}
```

再写 archive 失败时手动操作返回错误、history 不变的测试。

- [ ] **步骤 2：扩展 `ContextCompactionResult`**

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

所有返回 slice 必须复制，避免调用方修改内部状态。

- [ ] **步骤 3：在 fold 前归档完整消息**

`compactHistory` 在调用 summarizer 前执行：

```go
archive, err := runner.compactionArchive.archive(foldArchiveRequests(fold))
```

`operation` 使用 `fold`；archive 失败直接返回原 history 和错误。archive 禁用时仍允许 fold，但 mechanical digest 写明 `not archived`；默认配置始终启用。

- [ ] **步骤 4：统一自动和手动 mechanical fallback**

删除当前 `force` 分支中“手动摘要失败直接返回 error”的行为。改为：

```go
mechanical := false
summary, summaryUsage, err := runner.summarizeHistory(ctx, fold, focus)
if err != nil {
    mechanical = true
    summary = mechanicalFoldSummary(len(fold), archive.Paths)
}
```

对非 timeout 的瞬时错误重试一次：新增 `summarizeHistoryWithRetry`；`context.Canceled` 与 `DeadlineExceeded` 不重试。

- [ ] **步骤 5：确保 transcript 使用维护后的 Tool Result**

保留 `renderCompactionTranscript` 的完整 Content 输出逻辑，因为调用方传入的 fold 已经过 snip/prune。增加回归测试：

- 原始工具输入仍只显示字段名和 key 数；
- snipped marker 原样进入 transcript；
- 不会从 archive 自动展开原文；
- `[keep]` 与错误组不出现在 summarizer transcript。

- [ ] **步骤 6：把维护统计合并到 compaction result**

在 `maintainContextProjection` 中，把本轮 snip/prune 的统计合并到 `ContextCompactionResult`；即使没有 summary，也可通过内部 maintenance result 发通知。手动 `/compact` 的 result 至少包含 fold archive 和 mechanical 标记。

- [ ] **步骤 7：更新 `/compact` 展示测试**

先用 Grep 定位 `ContextCompactionResult` 的格式化函数；在 `internal/ui/bubble/command_helpers.go` 中把文案扩展为包含：

```text
compacted <N> messages: <before> → <after>
archive: <path>
summary unavailable; folded mechanically
```

只在对应字段有值时显示附加行。更新 `bubble_test.go` 的 fake result 和断言。

- [ ] **步骤 8：运行任务 7 测试**

```bash
go test ./internal/loop -run 'Test.*Compaction' -count=1
go test ./internal/ui/bubble -run 'Test.*Compact' -count=1
```

预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/loop/context_compaction.go internal/loop/context_compaction_test.go \
  internal/ui/bubble/command_helpers.go internal/ui/bubble/bubble_test.go
git commit -m "✨ feat(loop): archive and harden summary compaction"
```

---

### 任务 8：补全通知、防循环和配置 round-trip 回归

**文件：**
- 修改：`internal/loop/context_pressure.go`
- 修改：`internal/loop/context_pressure_test.go`
- 修改：`internal/settings/settings_test.go`
- 修改：`internal/loop/runner_test.go`

- [ ] **步骤 1：编写通知只发一次和 stuck 恢复测试**

使用实现 `ui.SystemNotifier` 的测试 UI，断言：

- 50%～60% 连续两轮只发一次 soft notice；
- 低于 50% 后再升至 50% 可再次提示；
- snip 通知包含结果数和估算 token；
- prune 通知包含结果数；
- 连续两次 summary 后只发一次 paused 通知；
- 压力低于 80% 后可再次 summary。

- [ ] **步骤 2：实现精确状态转换**

Runner 加锁更新状态，避免通知与状态不同步：

```go
func (runner *Runner) markSoftNotice() bool
func (runner *Runner) clearPressureLatch()
func (runner *Runner) noteSummaryCompaction() bool
func (runner *Runner) automaticSummaryAllowed() bool
```

`noteSummaryCompaction` 返回是否刚进入 stuck，用于只发送一次通知。

- [ ] **步骤 3：编写 settings round-trip 测试**

保存非默认 context maintenance 配置，再 Load，断言所有 float、int、bool 精确一致。另断言显式非法 `tail_tokens: 0` 被拒绝，而配置文件未包含 `context_maintenance` 时采用默认值。

- [ ] **步骤 4：增加历史投影与 journal 分离回归测试**

在 `runner_test.go` 验证：完成一轮后 `runner.currentHistory()` 可包含 snipped/pruned marker，但 `store.LoadResolvedHistory()` 中原 Tool Result 仍完整。不要断言 UI transcript 使用内存投影；UI 的 session load 仍来自 store。

- [ ] **步骤 5：运行任务 8 测试**

```bash
go test ./internal/settings ./internal/loop -count=1
```

预期：PASS。

- [ ] **步骤 6：Commit**

```bash
git add internal/loop/context_pressure.go internal/loop/context_pressure_test.go \
  internal/loop/runner_test.go internal/settings/settings_test.go
git commit -m "✅ test(loop): cover context maintenance lifecycle"
```

---

### 任务 9：全量验证、竞态检查与文档一致性

**文件：**
- 修改：`docs/superpowers/specs/2026-03-23-reasonix-tool-result-compaction-design.md`（仅在实现发现签名差异时同步最终事实；不得改变已批准行为）
- 修改：`README.md`（仅增加 settings JSON 示例和 archive 位置，不改其他未提交内容）

- [ ] **步骤 1：运行格式化与静态差异检查**

```bash
gofmt -w internal/settings/settings.go internal/settings/settings_test.go \
  internal/tool/tool.go internal/tool/exec/bash.go internal/tool/webfetch/webfetch.go \
  internal/tool/mcp/tool.go internal/loop/context_maintenance_config.go \
  internal/loop/context_maintenance_config_test.go internal/loop/compaction_archive.go \
  internal/loop/compaction_archive_test.go internal/loop/tool_result_maintenance.go \
  internal/loop/tool_result_maintenance_test.go internal/loop/context_pressure.go \
  internal/loop/context_pressure_test.go internal/loop/context_resume_test.go \
  internal/loop/context_compaction.go internal/loop/context_compaction_test.go \
  internal/loop/runner.go internal/loop/runner_test.go cmd/agent/main.go \
  cmd/agent/main_test.go internal/ui/bubble/command_helpers.go internal/ui/bubble/bubble_test.go
git diff --check
```

预期：无输出、退出码 0。

- [ ] **步骤 2：运行聚焦测试**

```bash
go test ./internal/settings ./internal/tool/... ./internal/loop ./cmd/agent ./internal/ui/bubble -count=1
```

预期：全部 PASS。

- [ ] **步骤 3：运行 race-sensitive 测试**

```bash
go test -race ./internal/loop ./internal/settings -count=1
```

预期：PASS，无 data race。若 CI/平台不支持 race，记录具体平台错误，但不能用它掩盖普通测试失败。

- [ ] **步骤 4：运行全量测试**

```bash
go test ./... -count=1
```

预期：PASS。

- [ ] **步骤 5：验证 archive 实际布局**

用一个临时集成测试或 `go test -run TestColdResume... -v` 生成 archive，检查：

```bash
find <temp-root>/.paw/sessions -path '*/compactions/*' -maxdepth 5 -type f -print
```

预期至少存在一个 operation JSONL 和 `index.jsonl`，且 session transcript 中仍有完整原文。

- [ ] **步骤 6：更新 README 配置示例**

在 README 的 settings 示例中加入：

```json
"context_maintenance": {
  "soft_compact_ratio": 0.5,
  "tool_result_snip_ratio": 0.6,
  "compact_ratio": 0.8,
  "compact_force_ratio": 0.9,
  "compact_target_ratio": 0.5,
  "tail_tokens": 16384,
  "min_tool_result_bytes": 1024,
  "keep_errors": true,
  "keep_user_marked": true,
  "archive_enabled": true
}
```

说明 archive 位于 `.paw/sessions/<session-id>/compactions/`，journal 仍保存完整 Tool Result。由于工作区当前已有 README 未提交修改，提交前使用 `git diff README.md` 确认只暂存本任务新增 hunks；必要时用 `git add -p README.md`。

- [ ] **步骤 7：最终规格覆盖自检**

逐项核对已批准规格：

- 50/60/80/90 阈值；
- 固定 token recent tail；
- snip/prune 幂等；
- archive-before-rewrite；
- hash 去重；
- KeepErrors/KeepUserMarked；
- Tool Call/Result group；
- cold resume；
- automatic/manual mechanical fallback；
- stuck 防循环；
- journal 完整性。

任何缺失必须补测试和实现后重新运行步骤 1～4。

- [ ] **步骤 8：最终 Commit**

仅暂存本功能文件，避免纳入工作区已有无关修改：

```bash
git add internal/settings internal/tool internal/loop cmd/agent \
  internal/ui/bubble/command_helpers.go internal/ui/bubble/bubble_test.go \
  docs/superpowers/specs/2026-03-23-reasonix-tool-result-compaction-design.md
git add -p README.md
git commit -m "✨ feat(loop): align tool result compaction with Reasonix"
```

提交后运行：

```bash
git status --short
git log -1 --oneline
```

预期：最新提交为本功能；`git status` 只显示开始实现前已经存在的无关工作区修改。
