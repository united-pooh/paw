# Paw TUI 工具轨道信息减法与状态色块实施计划

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Paw TUI 的工具主行收敛为“工具名: 真实目标 + 状态色块”，隐藏 MCP 长命名空间，并保证文件目标至少显示为真实相对路径。

**Architecture:** 保留原始工具名、tool-use ID 和输入 JSON 不变，在 Bubble Tea 渲染层增加纯函数显示目录。显示目录负责 MCP/native 别名、动作描述和目标提取；Runner 通过可选的工作区根目录能力向 UI 提供路径格式化上下文。现有工具事务匹配、折叠、hover、固定布局和运行耗时机制继续复用。

**Tech Stack:** Go, Bubble Tea, Lipgloss, `github.com/charmbracelet/x/ansi`, 现有 `internal/tool/file` 路径格式化语义。

---

## 文件边界

- Modify: `internal/loop/runner.go` — 暴露只读 `WorkspaceRoot()` 能力，不改变现有 `Runner` 接口。
- Test: `internal/loop/runner_test.go` — 验证带 instruction root 的 Runner 返回同一根目录。
- Modify: `internal/ui/bubble/bubble.go` — 增加可选 `WorkspaceRootProvider`，在创建 `appModel` 时注入根目录。
- Modify: `internal/ui/bubble/types.go` — 给 `appModel` 增加 `workspaceRoot`。
- Create: `internal/ui/bubble/tool_display.go` — 显示目录、MCP/native 别名、动作描述和目标格式化。
- Create: `internal/ui/bubble/tool_display_test.go` — 显示目录和路径规则的纯函数测试。
- Modify: `internal/tool/file/path.go` — 暴露只读 `DisplayPath` 包装函数，复用现有相对/绝对路径语义。
- Modify: `internal/tool/file/path_test.go` — 覆盖工作区内、工作区外和斜杠统一。
- Modify: `internal/ui/bubble/transcript.go` — 使用显示目录生成工具主行和 citation 显示名称，加入中文状态色块与宽度预算。
- Modify: `internal/ui/bubble/utils.go` — 保留旧 `toolSummaryTarget` 兼容入口，新增带 workspace root 的目标提取调用。
- Modify: `internal/ui/bubble/subagent_picker.go` — 历史/子 agent 工具条目使用同一目标路径规则。
- Modify: `internal/ui/bubble/session_picker.go` — 恢复会话工具条目传入同一 workspace root。
- Modify: `internal/ui/bubble/styles.go` — 增加完成、运行中、出错三种状态色块样式。
- Modify: `internal/ui/bubble/bubble_test.go` — 更新工具生命周期、citation 和渲染断言。
- Modify: `internal/ui/bubble/tool_track_test.go` — 更新工具轨道、宽度、hover、展开和运行态断言。

保持不变：工作区中已有的 `internal/ui/bubble/bubble.go`、`markdown.go`、`selection.go` 等用户未提交修改；实现时只修改本计划列出的相关行，并在提交前检查工作区差异。

### Task 1: 注入 Runner 工作区根目录

**Files:**
- Modify: `internal/loop/runner.go:146-161`
- Test: `internal/loop/runner_test.go`
- Modify: `internal/ui/bubble/bubble.go:21-40,169-181`
- Modify: `internal/ui/bubble/types.go:325-342`

- [ ] **Step 1: Add the optional root provider without widening the existing Runner interface**

在 `internal/loop/runner.go` 的 `NewRunnerWithInstructionRoot` 后增加：

```go
// WorkspaceRoot 返回 Runner 解析工具相对路径时使用的工作区根目录。
// 空字符串表示调用方没有提供工作区根目录。
func (runner *Runner) WorkspaceRoot() string {
	if runner == nil {
		return ""
	}
	return runner.workRoot
}
```

在 `internal/ui/bubble/bubble.go` 的可选 Runner capability 区域增加：

```go
// WorkspaceRootProvider exposes the same root used by tool execution.
// It is optional so existing fake runners remain source-compatible.
type WorkspaceRootProvider interface {
	WorkspaceRoot() string
}
```

- [ ] **Step 2: Add the model field and copy the provider value during UI startup**

在 `appModel` 的 `runner` 字段后增加：

```go
workspaceRoot string
```

在 `UI.Run` 创建 `appModel` 后、设置 `mcpController` 前加入：

```go
workspaceRoot := ""
if provider, ok := runner.(WorkspaceRootProvider); ok {
	workspaceRoot = strings.TrimSpace(provider.WorkspaceRoot())
}
appModel.workspaceRoot = workspaceRoot
```

如果 `bubble.go` 当前 import 没有 `strings`，补充标准库 import；不改变已有 Runner capability 的可选性质。

- [ ] **Step 3: Add the focused Runner regression test**

在 `internal/loop/runner_test.go` 增加：

```go
func TestRunnerWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	runner := NewRunnerWithInstructionRoot(nil, nil, nil, nil, "", root)
	if got := runner.WorkspaceRoot(); got != root {
		t.Fatalf("WorkspaceRoot() = %q, want %q", got, root)
	}
}
```

- [ ] **Step 4: Run the focused root test**

Run: `go test ./internal/loop -run TestRunnerWorkspaceRoot -count=1`

Expected: `PASS`; existing fake Runner tests continue compiling because `WorkspaceRootProvider` is optional.

### Task 2: 建立显示目录和真实路径格式化

**Files:**
- Create: `internal/ui/bubble/tool_display.go`
- Test: `internal/ui/bubble/tool_display_test.go`
- Modify: `internal/tool/file/path.go`
- Test: `internal/tool/file/path_test.go`
- Modify: `internal/ui/bubble/utils.go:356-374`

- [ ] **Step 1: Expose the existing file path display semantics**

在 `internal/tool/file/path.go` 的 `displayPath` 函数后增加只读包装，不复制另一套路径规则：

```go
// DisplayPath formats a resolved file path for user-facing output.
// Paths inside root are slash-normalized and relative to root; paths outside
// root remain absolute.
func DisplayPath(root, target string) string {
	return displayPath(root, target)
}
```

- [ ] **Step 2: Write path formatting tests before wiring Bubble display**

在 `internal/tool/file/path_test.go` 增加表驱动测试，至少覆盖：

```go
func TestDisplayPath(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "internal", "ui", "bubble.go")
	outside := filepath.Join(t.TempDir(), "outside.go")
	tests := []struct {
		name, target, want string
	}{
		{name: "relative inside", target: inside, want: "internal/ui/bubble.go"},
		{name: "outside remains absolute", target: outside, want: filepath.ToSlash(outside)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayPath(root, tt.target); got != tt.want {
				t.Fatalf("DisplayPath(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}
```

Use the existing package imports and keep the security validation in `resolvePathWithinRoot`; `DisplayPath` is formatting only.

- [ ] **Step 3: Define the pure display data types and alias rules**

Create `internal/ui/bubble/tool_display.go` with the following stable boundaries:

```go
type toolDisplay struct {
	name   string
	target string
}

type toolDisplayRule struct {
	server     string
	action     string
	targetKeys []string
	pathTarget bool
}

func buildToolDisplay(name string, input json.RawMessage, workspaceRoot string) toolDisplay
func displayToolName(name string) string
func displayToolTarget(name string, input json.RawMessage, workspaceRoot string) string
func displayToolAction(name string) string
```

The implementation must follow these exact rules:

```text
codegraph → CodeGraph
read_url/read_page → 读取页面
search/search_web  → 搜索
unknown MCP suffix → unchanged short suffix, e.g. explore
LS/Glob/Read/Write/Edit/Update → native name plus real target
```

Normalize matching by trimming, lowercasing, and treating `-` and `_` consistently. Split MCP names only at the first `__`; never render the complete namespaced value. For an unknown namespaced name, format `serverDisplayName + ": " + shortAction`.

- [ ] **Step 4: Implement target extraction without exposing field names**

`displayToolTarget` must use `file_path` before `path` for file tools, then the existing primary keys for command, URL, query, and ID. For a native file target with a non-empty `workspaceRoot`, call `filetool.DisplayPath(workspaceRoot, value)`. If formatting returns an empty value or input parsing fails, return the trimmed raw value; never return `file_path=` or `path=`.

Keep the existing function signature as a compatibility wrapper in `utils.go`:

```go
func toolSummaryTarget(name string, input json.RawMessage) string {
	return displayToolTarget(name, input, "")
}
```

The new root-aware call is used by live and restored transcript construction.

- [ ] **Step 5: Add display catalog unit tests**

Create `internal/ui/bubble/tool_display_test.go` with table-driven cases for:

```go
{name: "codegraph__read_url", input: `{"url":"docs"}`, wantName: "CodeGraph", wantTarget: "读取页面"}
{name: "codegraph__explore", input: `{"query":"main"}`, wantName: "CodeGraph", wantTarget: "explore"}
{name: "Edit", input: `{"file_path":"/workspace/internal/ui/bubble.go"}`, root: "/workspace", wantTarget: "internal/ui/bubble.go"}
{name: "LS", input: `{"path":"internal/ui"}`, root: "/workspace", wantTarget: "internal/ui"}
{name: "Edit", input: `{"file_path":"internal/ui/bubble.go"}`, root: "", wantTarget: "internal/ui/bubble.go"}
{name: "Edit", input: `{"file_path":""}`, wantTarget: ""}
{name: "codegraph__unknown", input: `{}`, wantName: "CodeGraph", wantTarget: "unknown"}
```

Assert that no result contains `codegraph__`, `file_path=`, or `path=`.

- [ ] **Step 6: Run the display and path tests**

Run: `go test ./internal/tool/file ./internal/ui/bubble -run 'Test(DisplayPath|DisplayTool|ToolDisplay)' -count=1`

Expected: `PASS`; failures at this point identify alias/path behavior before transcript integration.

### Task 3: Apply display identities to live, restored, and citation tool entries

**Files:**
- Modify: `internal/ui/bubble/transcript.go:245-278,1124-1154`
- Modify: `internal/ui/bubble/subagent_picker.go:182,195-227`
- Modify: `internal/ui/bubble/session_picker.go:79-89,176-183`
- Modify: `internal/ui/bubble/bubble_test.go`
- Modify: `internal/ui/bubble/tool_track_test.go`

- [ ] **Step 1: Make live tool entries use the root-aware target**

Change `recordToolCallEntry` so it preserves raw `toolName` but computes the target with the model root:

```go
toolTarget: displayToolTarget(name, input, m.workspaceRoot),
```

Do not replace `toolName`; result matching must continue to see the raw name and ID.

- [ ] **Step 2: Make restored transcript entries use the same root-aware constructor**

Change the internal constructor to accept a root without breaking tests:

```go
func transcriptEntriesFromMessage(msg message.Message, createdAt time.Time, workspaceRoot string) []transcriptEntry
```

Update production call sites in `session_picker.go` and `subagent_picker.go` to pass `m.workspaceRoot` (or the owning model's root). Update existing tests to pass `""`; add one restored-entry test using a temporary root and an absolute `file_path` to assert `internal/...` output.

- [ ] **Step 3: Apply display names to assistant citation rows**

Change `newToolCallCitation` to accept the display context and store only the display name/target in the citation presentation fields:

```go
func newToolCallCitation(toolUseID, name string, input json.RawMessage, workspaceRoot string) toolCitation {
	display := buildToolDisplay(name, input, workspaceRoot)
	return toolCitation{
		toolUseID: strings.TrimSpace(toolUseID),
		name:      display.name,
		target:    display.target,
		status:    "running",
	}
}
```

Keep `toolUseID` unchanged. Result-only citations may use `displayToolName(name)` because they do not receive the original input.

- [ ] **Step 4: Update existing transcript assertions**

Replace old primary-row expectations such as:

```text
✓ Read go.mod · ok
```

with the approved structure and Chinese status text:

```text
✓ Read: go.mod  完成
```

Keep assertions that verify raw JSON fields, tool IDs, result content, and matching behavior remain hidden or preserved as appropriate. Do not change tests unrelated to tool display.

- [ ] **Step 5: Run focused lifecycle and restore tests**

Run: `go test ./internal/ui/bubble -run 'Test(Tool|Historical|.*Citation|.*Transcript)' -count=1`

Expected: `PASS`; the rendered output has short display labels, while result matching still updates one transaction in place.

### Task 4: Render semantic status color blocks and cell-safe summary widths

**Files:**
- Modify: `internal/ui/bubble/styles.go:17-84,86-120`
- Modify: `internal/ui/bubble/transcript.go:906-1027`
- Test: `internal/ui/bubble/tool_track_test.go`
- Test: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: Add theme-derived status block styles**

Add three package-level styles near the existing tool styles:

```go
toolStatusOKStyle      lipgloss.Style
toolStatusRunningStyle lipgloss.Style
toolStatusErrorStyle   lipgloss.Style
```

Initialize them in `rebuildLegacyStyles` with terminal background as the foreground, the existing semantic green/orange/red roles as backgrounds, bold text, and `Padding(0, 1)`. Use `colorWorktreeClean`, `colorWorktreeDirty`, and `colorWorktreeConflict`; do not add new theme colors.

- [ ] **Step 2: Add status label and chip helpers**

In `tool_display.go` or a focused transcript helper, define:

```go
func toolStatusLabel(status string) string
func toolStatusStyle(status string) lipgloss.Style
func renderToolStatusChip(status, duration string) string
```

The labels are `完成`, `运行中`, and `出错`; unknown status uses the raw lower-case status with the muted style. `renderToolStatusChip` returns the styled `label` plus ` · duration` only when duration is non-empty.

- [ ] **Step 3: Rewrite the compact summary width budget**

In `renderCompactToolSummary`, use `buildToolDisplay` for the name/target, render the icon separately, and calculate widths with `lipgloss.Width` after styling the status chip. Preserve the following order when space is tight:

```text
icon → display name and colon → target → status word → duration
```

The final fallback must keep the status word and icon, drop duration before dropping the target, and use `truncateDisplayWidth` / `truncateStyledDisplayWidth` rather than byte slicing. The returned string must remain one terminal row at every width.

- [ ] **Step 4: Preserve hover and focus semantics**

Keep existing underline behavior for hover on the icon/name/target. Do not underline or recolor the semantic status background in a way that removes contrast. Keep `toolFocusedStyle` as the row selection background and verify that the status chip remains legible on the selected row.

- [ ] **Step 5: Add renderer tests for colors, labels, and narrow widths**

Extend `tool_track_test.go` with assertions that ANSI-stripped output contains:

```text
✓ LS: internal/ui · 完成
◌ CodeGraph: 读取页面 · 运行中 · 12s
× Edit: internal/ui/file.go · 出错 · 1.1s
```

Use a color-enabled Lipgloss profile to assert each status chip has the expected background color, then restore the previous profile with `t.Cleanup`. For widths `120, 80, 40, 20`, assert every rendered line satisfies `ansi.StringWidth(line) <= width`. Add a `NO_COLOR=1` test that asserts the Chinese status text remains while ANSI background escapes are absent.

- [ ] **Step 6: Run focused renderer tests**

Run: `go test ./internal/ui/bubble -run 'Test(CompactTool|ToolTrack|RenderTool|.*Color|.*Width|.*Hover)' -count=1`

Expected: `PASS`; status blocks render with semantic colors, and no line exceeds the requested terminal width.

### Task 5: Full regression verification and handoff

**Files:**
- Verify only; no new source file.

- [ ] **Step 1: Run the package-level UI suite**

Run: `go test ./internal/ui/bubble -count=1`

Expected: `PASS`; existing Markdown, selection, theme, input, worktree, hover, and fixed-layout tests remain green.

- [ ] **Step 2: Run the complete Go test and vet suites**

Run: `go test ./... -count=1`

Expected: `PASS` for every package.

Run: `go vet ./...`

Expected: no diagnostics.

- [ ] **Step 3: Run the no-color and diff checks**

Run: `NO_COLOR=1 go test ./internal/ui/bubble -count=1`

Expected: `PASS`; status words remain readable without color escapes.

Run: `git diff --check`

Expected: no whitespace errors. Verify the diff contains only the intended implementation files and does not stage the pre-existing modified files or `edit-test.txt`.

- [ ] **Step 4: Perform a real TUI smoke check**

Run from the workspace root: `NO_COLOR=0 go run cmd/agent/main.go`

Exercise one native file tool, one CodeGraph MCP tool if configured, one running tool, one successful result, one error result, and one expanded result. Confirm the live terminal shows `Tool: target`, colored status blocks, relative file paths, no raw `server__tool` namespace in the main row, and unchanged click/Enter expansion behavior. If MCP is not configured or unavailable, record that limitation and validate the native tool states locally.

- [ ] **Step 5: Review the final diff before implementation handoff**

Run: `git status --short --branch` and `git diff --stat`

Expected: unrelated user changes remain untouched, the new display helper has focused responsibilities, and the final implementation can be reviewed as one coherent TUI rendering change.
