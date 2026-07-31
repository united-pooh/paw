# Edit 工具 Claude Code 行为对齐实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让 Paw 内置 Edit 严格要求先 Read、保留已有文件权限，并让 Bubble TUI 使用工具执行前后的真实完整文件内容渲染 Edit/Write diff。

**架构：** 文件工具层新增严格 Read 验证和可指定权限的原子写入；内置 Edit/Write 通过一个仅供 Runner 使用的可选能力接口暴露安全解析后的变更目标。Runner 在工具执行前后捕获 `FileMutationSnapshot`，只通过 UI 事件传递；Bubble 在成功结果到达时用真实快照重建工具条目，快照缺失时继续走现有局部字段回退路径。

**技术栈：** Go 1.25、标准库 `os`/`path/filepath`/`encoding/json`/`sync`、现有 Bubble Tea/Lip Gloss TUI、Go `testing`。

---

## 实施约束

- 当前工作区已有与本功能无关的未提交改动，尤其包括 `internal/loop/runner.go`、`internal/loop/runner_test.go`、`internal/ui/bubble/*` 和 `internal/tool/file/path.go`。每次修改前先检查目标文件当前 diff；提交时只暂存本任务的精确文件或交互式 hunk，不得覆盖、还原或顺带提交既有改动。
- 不改变 `tool.Tool.Run(ctx, json.RawMessage) (string, error)`。
- 不把完整文件内容放进 `message.ToolResult.Content`、模型上下文或 token trace 的 `content` 字段。
- 不按工具名称识别内置文件工具；通过 Go 能力接口识别，避免误处理 MCP 同名工具。
- Edit/Write 仍不是并发安全工具，不新增 `IsConcurrencySafe`。
- 保留相对路径和工作区内绝对路径支持。
- 不增加模糊匹配、缩进纠正或换行符归一化。
- 每个 Go 任务提交前运行 `gofmt` 和对应聚焦测试。

## 文件结构

### 文件工具与权限

- **修改** `internal/tool/file/read_state.go` — 增加严格 `VerifyRequired`，保留宽松 `Verify`。
- **修改** `internal/tool/file/read_state_test.go` — 严格验证、路径隔离和并发测试。
- **修改** `internal/tool/file/atomic.go` — 让原子写入显式接收权限位。
- **修改** `internal/tool/file/atomic_test.go` — 指定权限与临时文件清理测试。
- **修改** `internal/tool/file/edit.go` — 严格 Read 前置、精确错误、保留权限、更新描述。
- **修改** `internal/tool/file/edit_test.go` — 全面覆盖新 Edit 契约。
- **修改** `internal/tool/file/write.go` — 适配原子写入新签名，保持 Write 既有语义。
- **修改** `internal/tool/file/write_test.go` — 锁定 Write 新建/覆盖权限行为。

### Runner 快照通道

- **修改** `internal/tool/tool.go` — 定义可选的 `FileMutationTool` 能力接口和目标描述结构。
- **修改** `internal/tool/file/edit.go` — 实现 `FileMutationTarget`。
- **修改** `internal/tool/file/write.go` — 实现 `FileMutationTarget`。
- **修改** `internal/ui/ui.go` — 定义可选 `FileMutationSnapshot`，扩展工具调用/结果事件。
- **修改** `internal/loop/runner.go` — 仅为能力接口工具捕获 Before/After，并传给 UI。
- **修改** `internal/loop/runner_test.go` — 验证成功、失败、降级、MCP 同名隔离和模型结果隔离。

### Bubble 展示

- **修改** `internal/ui/bubble/types.go` — transcript 条目保存可选快照。
- **修改** `internal/ui/bubble/app.go` — 把调用/结果事件中的快照交给 transcript。
- **修改** `internal/ui/bubble/transcript.go` — 成功结果到达时用真实快照重建 Edit/Write body。
- **修改** `internal/ui/bubble/diff.go` — 明确真实快照优先、局部字段回退的内容选择函数。
- **修改** `internal/ui/bubble/utils.go` — 文件变更格式化函数接收可选完整前后内容。
- **修改** `internal/ui/bubble/diff_test.go` — `replace_all` 全文件 diff、优先级与回退测试。
- **修改** `internal/ui/bubble/tool_track_test.go` — 工具事件端到端状态与错误展示测试。

---

### 任务 1：为 ReadStateStore 增加严格 Read 前置验证

**文件：**
- 修改：`internal/tool/file/read_state.go:10-58`
- 测试：`internal/tool/file/read_state_test.go`

- [ ] **步骤 1：编写 `VerifyRequired` 的失败测试**

在 `internal/tool/file/read_state_test.go` 增加：

```go
func TestReadStateStoreVerifyRequiredRejectsMissingBaseline(t *testing.T) {
	s := NewReadStateStore()
	err := s.VerifyRequired("core/utils.py", []byte("current"))
	if err == nil {
		t.Fatal("VerifyRequired without prior Read = nil, want error")
	}
	if !strings.Contains(err.Error(), "file must be read before editing: core/utils.py; use Read first") {
		t.Fatalf("err = %q", err)
	}
}

func TestReadStateStoreVerifyRequiredAcceptsMatchingBaseline(t *testing.T) {
	s := NewReadStateStore()
	s.Record("core/utils.py", []byte("current"))
	if err := s.VerifyRequired("core/utils.py", []byte("current")); err != nil {
		t.Fatalf("VerifyRequired = %v", err)
	}
}

func TestReadStateStoreVerifyRequiredRejectsChangedContent(t *testing.T) {
	s := NewReadStateStore()
	s.Record("core/utils.py", []byte("before"))
	err := s.VerifyRequired("core/utils.py", []byte("after"))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-read error", err)
	}
}

func TestReadStateStoreStrictAndLenientVerificationAreIndependent(t *testing.T) {
	s := NewReadStateStore()
	if err := s.Verify("unread.txt", []byte("x")); err != nil {
		t.Fatalf("lenient Verify = %v, want nil", err)
	}
	if err := s.VerifyRequired("unread.txt", []byte("x")); err == nil {
		t.Fatal("strict VerifyRequired = nil, want error")
	}
}
```

- [ ] **步骤 2：运行聚焦测试确认失败**

运行：

```bash
go test ./internal/tool/file -run 'TestReadStateStoreVerifyRequired|TestReadStateStoreStrictAndLenient' -count=1
```

预期：编译失败，提示 `s.VerifyRequired undefined`。

- [ ] **步骤 3：实现共享验证核心和严格方法**

在 `internal/tool/file/read_state.go` 中保留现有 `Verify` 语义，并加入：

```go
func (s *ReadStateStore) VerifyRequired(path string, current []byte) error {
	if s == nil {
		return fmt.Errorf("file must be read before editing: %s; use Read first", path)
	}
	recorded, ok := s.recordedHash(path)
	if !ok {
		return fmt.Errorf("file must be read before editing: %s; use Read first", path)
	}
	return verifyRecordedHash(path, recorded, current)
}

func (s *ReadStateStore) recordedHash(path string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recorded, ok := s.states[path]
	return recorded, ok
}

func verifyRecordedHash(path, recorded string, current []byte) error {
	if got := contentHash(current); got != recorded {
		return fmt.Errorf("file has been modified since last read: %s; read it again before editing", path)
	}
	return nil
}
```

将 `Verify` 改为复用同一核心：

```go
func (s *ReadStateStore) Verify(path string, current []byte) error {
	recorded, ok := s.recordedHash(path)
	if !ok {
		return nil
	}
	return verifyRecordedHash(path, recorded, current)
}
```

- [ ] **步骤 4：补充并发严格验证测试**

在现有并发测试中同时调用 `VerifyRequired`：

```go
go func() {
	defer close(done)
	for i := 0; i < 200; i++ {
		_ = s.Verify("/p", []byte("x"))
		_ = s.VerifyRequired("/p", []byte("x"))
	}
}()
```

- [ ] **步骤 5：运行测试与 race detector**

运行：

```bash
gofmt -w internal/tool/file/read_state.go internal/tool/file/read_state_test.go
go test ./internal/tool/file -run TestReadStateStore -race -count=1
```

预期：PASS，无 data race。

- [ ] **步骤 6：提交严格验证**

```bash
git add internal/tool/file/read_state.go internal/tool/file/read_state_test.go
git commit -m "feat: require a Read baseline for Edit validation"
```

---

### 任务 2：让 Edit 严格要求 Read 并改进精确匹配错误

**文件：**
- 修改：`internal/tool/file/edit.go:11-97`
- 测试：`internal/tool/file/edit_test.go`

- [ ] **步骤 1：调整测试辅助函数，使默认 Edit 带共享 ReadState**

将测试辅助函数改为：

```go
func newEditTool(t *testing.T) (*EditTool, string) {
	t.Helper()
	root := t.TempDir()
	return &EditTool{Root: root, ReadState: NewReadStateStore()}, root
}

func recordEditBaseline(t *testing.T, tool *EditTool, fullPath string) {
	t.Helper()
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	tool.ReadState.Record(fullPath, data)
}
```

对原本用于测试匹配逻辑的成功/匹配失败用例，在 `writeExistingFile` 后调用 `recordEditBaseline`，使测试仍聚焦于原行为。

- [ ] **步骤 2：增加未 Read、nil ReadState、连续 Edit 和重新 Read 测试**

```go
func TestEditRejectsFileThatWasNotRead(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "file must be read before editing: a.go; use Read first") {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(full)
	if string(got) != "return 1\n" {
		t.Fatalf("file changed on rejection: %q", got)
	}
}

func TestEditRejectsNilReadState(t *testing.T) {
	root := t.TempDir()
	writeExistingFile(t, root, "a.go", "return 1\n")
	tool := &EditTool{Root: root}
	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "use Read first") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditSuccessUpdatesBaselineForConsecutiveEdit(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")
	recordEditBaseline(t, tool, full)

	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 2","new_string":"return 3"}`); err != nil {
		t.Fatalf("second Edit without another Read: %v", err)
	}
}

func TestEditSucceedsAfterExternalChangeIsReadAgain(t *testing.T) {
	tool, root := newEditTool(t)
	full := writeExistingFile(t, root, "a.go", "return 1\n")
	recordEditBaseline(t, tool, full)
	if err := os.WriteFile(full, []byte("return 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 9","new_string":"return 10"}`); err == nil {
		t.Fatal("stale Edit unexpectedly succeeded")
	}
	recordEditBaseline(t, tool, full)
	if _, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 9","new_string":"return 10"}`); err != nil {
		t.Fatalf("Edit after re-Read: %v", err)
	}
}
```

- [ ] **步骤 3：增加空白和换行必须精确匹配的表驱动测试**

```go
func TestEditDoesNotNormalizeWhitespaceOrLineEndings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		old     string
	}{
		{name: "spaces", content: "  return 1\n", old: " return 1"},
		{name: "tab", content: "\treturn 1\n", old: "    return 1"},
		{name: "crlf", content: "return 1\r\n", old: "return 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, root := newEditTool(t)
			full := writeExistingFile(t, root, "a.go", tc.content)
			recordEditBaseline(t, tool, full)
			input := `{"file_path":"a.go","old_string":` + jsonString(tc.old) + `,"new_string":"return 2"}`
			_, err := runEdit(t, tool, input)
			if err == nil || !strings.Contains(err.Error(), "must match the file contents exactly") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
```

- [ ] **步骤 4：运行测试确认当前实现失败**

运行：

```bash
go test ./internal/tool/file -run 'TestEditRejectsFileThatWasNotRead|TestEditRejectsNilReadState|TestEditSuccessUpdatesBaseline|TestEditSucceedsAfterExternalChangeIsReadAgain|TestEditDoesNotNormalize' -count=1
```

预期：未 Read 和 nil ReadState 用例失败；零匹配错误缺少精确匹配提示。

- [ ] **步骤 5：修改 Edit 描述和严格验证调用**

将描述改为明确约束：

```go
func (t *EditTool) Description() string {
	return "对工作区内已先用 Read 读取的文件做精确字符串替换：old_string 必须与文件内容逐字节匹配。" +
		"默认 old_string 必须唯一；设 replace_all=true 可替换全部匹配。" +
		`示例输入: {"file_path":"internal/foo.go","old_string":"return 1","new_string":"return 2"}`
}
```

在 `Run` 读取文件后使用展示路径做严格验证：

```go	display := relativePath(t.Root, target)
	if t.ReadState == nil {
		return "", fmt.Errorf("file must be read before editing: %s; use Read first", display)
	}
	if err := t.ReadState.VerifyRequired(target, current); err != nil {
		if strings.Contains(err.Error(), "file must be read before editing") {
			return "", fmt.Errorf("file must be read before editing: %s; use Read first", display)
		}
		return "", fmt.Errorf("file has been modified since last read: %s; read it again before editing", display)
	}
```

将零匹配错误改为：

```go
return "", fmt.Errorf("old_string not found in %s; it must match the file contents exactly", display)
```

将多匹配错误中的 `add surrounding context` 改为规格中的 `include more surrounding context`。

- [ ] **步骤 6：运行全部 Edit 测试**

```bash
gofmt -w internal/tool/file/edit.go internal/tool/file/edit_test.go
go test ./internal/tool/file -run TestEdit -count=1
```

预期：PASS。

- [ ] **步骤 7：确认工具描述包含 Read 前置要求**

增加小测试：

```go
func TestEditDescriptionRequiresReadAndExactMatch(t *testing.T) {
	description := (&EditTool{}).Description()
	for _, want := range []string{"Read", "逐字节匹配", "replace_all=true"} {
		if !strings.Contains(description, want) {
			t.Fatalf("Description() = %q, missing %q", description, want)
		}
	}
}
```

运行：

```bash
go test ./internal/tool/file -run 'TestEditDescription|TestEdit' -count=1
```

预期：PASS。

- [ ] **步骤 8：提交 Edit 严格契约**

```bash
git add internal/tool/file/edit.go internal/tool/file/edit_test.go
git commit -m "fix: align Edit with strict Read and exact-match semantics"
```

---

### 任务 3：原子写入保留 Edit 文件权限且不改变 Write 契约

**文件：**
- 修改：`internal/tool/file/atomic.go`
- 测试：`internal/tool/file/atomic_test.go`
- 修改：`internal/tool/file/edit.go`
- 测试：`internal/tool/file/edit_test.go`
- 修改：`internal/tool/file/write.go`
- 测试：`internal/tool/file/write_test.go`

- [ ] **步骤 1：先用测试锁定 Write 当前权限行为**

在 `write_test.go` 增加：

```go
func TestWriteToolNewFileUsesDefaultMode(t *testing.T) {
	root := t.TempDir()
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	if _, err := tool.Run(context.Background(), []byte(`{"file_path":"new.sh","content":"echo hi\n"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "new.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
}

func TestWriteToolOverwriteKeepsExistingContractOfMode0644(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "script.sh")
	if err := os.WriteFile(full, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	if _, err := tool.Run(context.Background(), []byte(`{"file_path":"script.sh","content":"new\n"}`)); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(full)
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want existing Write behavior 0644", got)
	}
}
```

- [ ] **步骤 2：增加原子写入指定 mode 测试**

将原子写入测试调用更新为默认 mode，并增加：

```go
func TestAtomicWriteFileAppliesRequestedMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), "script.sh")
	if err := atomicWriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}
```

- [ ] **步骤 3：增加 Edit 权限保留表驱动测试**

```go
func TestEditPreservesExistingFileMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o600} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			tool, root := newEditTool(t)
			full := writeExistingFile(t, root, "script.sh", "echo old\n")
			if err := os.Chmod(full, mode); err != nil {
				t.Fatal(err)
			}
			recordEditBaseline(t, tool, full)
			if _, err := runEdit(t, tool, `{"file_path":"script.sh","old_string":"old","new_string":"new"}`); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(full)
			if got := info.Mode().Perm(); got != mode {
				t.Fatalf("mode = %04o, want %04o", got, mode)
			}
		})
	}
}
```

- [ ] **步骤 4：运行测试确认 Edit 权限测试失败**

```bash
go test ./internal/tool/file -run 'TestAtomicWriteFileAppliesRequestedMode|TestEditPreservesExistingFileMode|TestWriteTool.*Mode' -count=1
```

预期：编译失败或 Edit 权限被改成 `0644`。

- [ ] **步骤 5：修改原子写入签名**

将 `atomicWriteFile` 改为：

```go
func atomicWriteFile(target string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return fmt.Errorf("rename temp to %s: %w", target, err)
	}
	return nil
}
```

- [ ] **步骤 6：Edit 获取元数据并传原权限**

在读取目标内容后调用：

```go
info, err := os.Stat(target)
if err != nil {
	return "", err
}
if !info.Mode().IsRegular() {
	return "", fmt.Errorf("edit target is not a regular file: %s", display)
}
```

写入改为：

```go
if err := atomicWriteFile(target, []byte(updated), info.Mode().Perm()); err != nil {
	return "", err
}
```

- [ ] **步骤 7：Write 显式传默认权限以保持现有行为**

将 Write 调用改为：

```go
if err := atomicWriteFile(target, []byte(in.Content), 0o644); err != nil {
	return "", err
}
```

同时将其他测试中的 `atomicWriteFile(target, content)` 更新为 `atomicWriteFile(target, content, 0o644)`。

- [ ] **步骤 8：运行文件工具测试**

```bash
gofmt -w internal/tool/file/atomic.go internal/tool/file/atomic_test.go internal/tool/file/edit.go internal/tool/file/edit_test.go internal/tool/file/write.go internal/tool/file/write_test.go
go test ./internal/tool/file -count=1
```

预期：PASS；Edit 保留 `0755`/`0600`，Write 仍使用 `0644`。

- [ ] **步骤 9：提交权限行为**

```bash
git add internal/tool/file/atomic.go internal/tool/file/atomic_test.go internal/tool/file/edit.go internal/tool/file/edit_test.go internal/tool/file/write.go internal/tool/file/write_test.go
git commit -m "fix: preserve file permissions when applying Edit"
```

---

### 任务 4：为内置 Edit/Write 定义安全的文件变更目标能力

**文件：**
- 修改：`internal/tool/tool.go`
- 修改：`internal/tool/file/edit.go`
- 修改：`internal/tool/file/write.go`
- 测试：`internal/tool/file/edit_test.go`
- 测试：`internal/tool/file/write_test.go`

- [ ] **步骤 1：增加能力接口和目标结构**

在 `internal/tool/tool.go` 增加：

```go
type FileMutationTarget struct {
	Path         string
	BeforeExists bool
}

type FileMutationTool interface {
	FileMutationTarget(input json.RawMessage) (FileMutationTarget, error)
}
```

该接口不进入 model schema，只供 Runner 对已注册工具实例做 type assertion。

- [ ] **步骤 2：先写 Edit/Write 目标解析测试**

```go
func TestEditFileMutationTargetResolvesInsideRoot(t *testing.T) {
	root := t.TempDir()
	full := writeExistingFile(t, root, "a.go", "x\n")
	got, err := (&EditTool{Root: root}).FileMutationTarget([]byte(`{"file_path":"a.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != full || !got.BeforeExists {
		t.Fatalf("target = %+v, want path=%q exists=true", got, full)
	}
}

func TestEditFileMutationTargetRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	_, err := (&EditTool{Root: root}).FileMutationTarget([]byte(`{"file_path":"` + outside + `"}`))
	if err == nil {
		t.Fatal("outside path unexpectedly accepted")
	}
}

func TestWriteFileMutationTargetDistinguishesMissingFile(t *testing.T) {
	root := t.TempDir()
	got, err := (&WriteTool{Root: root}).FileMutationTarget([]byte(`{"file_path":"new.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.BeforeExists {
		t.Fatalf("target = %+v, want missing before", got)
	}
}
```

- [ ] **步骤 3：运行测试确认缺少方法**

```bash
go test ./internal/tool/file -run FileMutationTarget -count=1
```

预期：编译失败，EditTool/WriteTool 没有该方法。

- [ ] **步骤 4：实现输入路径解析辅助函数**

在各工具文件中复用其输入结构，或在 `path.go` 增加包内辅助函数。方法实现必须调用现有安全解析：

```go
func (t *EditTool) FileMutationTarget(raw json.RawMessage) (tool.FileMutationTarget, error) {
	var in editInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.FileMutationTarget{}, err
	}
	if strings.TrimSpace(in.FilePath) == "" {
		return tool.FileMutationTarget{}, fmt.Errorf("file_path is required")
	}
	target, err := resolvePathWithinRoot(t.Root, in.FilePath)
	if err != nil {
		return tool.FileMutationTarget{}, err
	}
	if _, err := os.Stat(target); err != nil {
		return tool.FileMutationTarget{}, err
	}
	return tool.FileMutationTarget{Path: target, BeforeExists: true}, nil
}
```

Write 的实现允许不存在：

```go
func (t *WriteTool) FileMutationTarget(raw json.RawMessage) (tool.FileMutationTarget, error) {
	var in writeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.FileMutationTarget{}, err
	}
	if strings.TrimSpace(in.FilePath) == "" {
		return tool.FileMutationTarget{}, fmt.Errorf("file_path is required")
	}
	target, err := resolvePathWithinRoot(t.Root, in.FilePath)
	if err != nil {
		return tool.FileMutationTarget{}, err
	}
	_, statErr := os.Stat(target)
	if statErr != nil && !os.IsNotExist(statErr) {
		return tool.FileMutationTarget{}, statErr
	}
	return tool.FileMutationTarget{Path: target, BeforeExists: statErr == nil}, nil
}
```

为避免 `file` 包名与 `tool` 包冲突，导入别名：

```go
coretool "paw/internal/tool"
```

并使用 `coretool.FileMutationTarget`。

- [ ] **步骤 5：加编译期能力断言**

在对应文件尾部增加：

```go
var _ coretool.FileMutationTool = (*EditTool)(nil)
var _ coretool.FileMutationTool = (*WriteTool)(nil)
```

- [ ] **步骤 6：运行测试**

```bash
gofmt -w internal/tool/tool.go internal/tool/file/edit.go internal/tool/file/write.go internal/tool/file/edit_test.go internal/tool/file/write_test.go
go test ./internal/tool/file -run FileMutationTarget -count=1
go test ./internal/tool/file -count=1
```

预期：PASS。

- [ ] **步骤 7：提交能力接口**

```bash
git add internal/tool/tool.go internal/tool/file/edit.go internal/tool/file/write.go internal/tool/file/edit_test.go internal/tool/file/write_test.go
git commit -m "refactor: expose safe file mutation targets to the runner"
```

---

### 任务 5：Runner 捕获真实 Before/After 且不污染模型结果

**文件：**
- 修改：`internal/ui/ui.go`
- 修改：`internal/loop/runner.go:2174-2269`
- 测试：`internal/loop/runner_test.go`

- [ ] **步骤 1：定义可选快照事件结构**

在 `internal/ui/ui.go` 增加：

```go
type FileMutationSnapshot struct {
	Before       string
	After        string
	BeforeExists bool
	AfterExists  bool
}
```

扩展事件：

```go
type ToolCallEvent struct {
	ID           string
	Name         string
	Input        json.RawMessage
	FileMutation *FileMutationSnapshot
}

type ToolResultEvent struct {
	ToolUseID    string
	Name         string
	Content      string
	IsError      bool
	FileMutation *FileMutationSnapshot
}
```

删除 `OldContentConsumer`，改为：

```go
type FileMutationConsumer interface {
	ConsumesFileMutations() bool
}
```

Bubble UI 后续任务同步实现新接口；在本任务测试 UI 中实现该接口。

- [ ] **步骤 2：增加捕获 UI 测试替身**

在 `runner_test.go` 增加：

```go
type mutationCaptureUI struct {
	calls   []ui.ToolCallEvent
	results []ui.ToolResultEvent
}

func (*mutationCaptureUI) OnAssistantDelta(string) error { return nil }
func (u *mutationCaptureUI) OnToolCall(event ui.ToolCallEvent) error {
	u.calls = append(u.calls, event)
	return nil
}
func (u *mutationCaptureUI) OnToolResult(event ui.ToolResultEvent) error {
	u.results = append(u.results, event)
	return nil
}
func (*mutationCaptureUI) OnDone() error { return nil }
func (*mutationCaptureUI) ConsumesFileMutations() bool { return true }
```

- [ ] **步骤 3：增加内置 Edit 成功快照测试**

测试直接构造 registry、共享 ReadState 和 Runner，先记录 baseline，再调用 `runner.runToolCall`：

```go
func TestRunToolCallEmitsCompleteEditMutationSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	before := "x\nkeep\nx\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	state := toolfile.NewReadStateStore()
	state.Record(path, []byte(before))
	registry := tool.NewRegistry()
	registry.Register(&toolfile.EditTool{Root: root, ReadState: state})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "edit-1", Name: "Edit",
		Input: []byte(`{"file_path":"a.txt","old_string":"x","new_string":"y","replace_all":true}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Content != "edited a.txt (2 replacements)" {
		t.Fatalf("model-visible content = %q", result.Content)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation == nil {
		t.Fatalf("results = %+v", capture.results)
	}
	snapshot := capture.results[0].FileMutation
	if snapshot.Before != before || snapshot.After != "y\nkeep\ny\n" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
```

- [ ] **步骤 4：增加失败、非消费者和同名假工具测试**

在 `runner_test.go` 增加一个不声明 `FileMutationConsumer` 的最小 UI：

```go
type nonMutationUI struct {
	calls   []ui.ToolCallEvent
	results []ui.ToolResultEvent
}

func (*nonMutationUI) OnAssistantDelta(string) error { return nil }
func (u *nonMutationUI) OnToolCall(event ui.ToolCallEvent) error {
	u.calls = append(u.calls, event)
	return nil
}
func (u *nonMutationUI) OnToolResult(event ui.ToolResultEvent) error {
	u.results = append(u.results, event)
	return nil
}
func (*nonMutationUI) OnDone() error { return nil }
```

增加失败时无快照测试：

```go
func TestRunToolCallDoesNotAttachMutationOnEditFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.EditTool{Root: root, ReadState: toolfile.NewReadStateStore()})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "edit-fail", Name: "Edit",
		Input: []byte(`{"file_path":"a.txt","old_string":"x","new_string":"y"}`),
	})
	if err != nil {
		t.Fatalf("runToolCall: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "use Read first") {
		t.Fatalf("result = %+v", result)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("result events = %+v", capture.results)
	}
}
```

增加非消费者不触发快照测试：

```go
func TestRunToolCallSkipsSnapshotForUIWithoutMutationConsumer(t *testing.T) {
	root := t.TempDir()
	registry := tool.NewRegistry()
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: toolfile.NewReadStateStore()})
	capture := &nonMutationUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "write-1", Name: "Write",
		Input: []byte(`{"file_path":"new.txt","content":"hello\n"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.calls) != 1 || capture.calls[0].FileMutation != nil {
		t.Fatalf("call events = %+v", capture.calls)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("result events = %+v", capture.results)
	}
}
```

使用现有 `fakeTool` 验证同名普通工具不被误识别：

```go
func TestRunToolCallDoesNotTreatSameNameToolAsBuiltinMutation(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Edit", output: "fake"})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: t.TempDir()}
	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "fake", Name: "Edit", Input: []byte(`{"file_path":"outside"}`),
	})
	if err != nil || result.Content != "fake" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("same-name non-capability tool got snapshot: %+v", capture.results)
	}
}
```

执行时先核对现有 `fakeTool` 的结果字段名；当前定义若不是 `output`，只调整复合字面量字段，不新增第二个同义 fake tool。

- [ ] **步骤 5：运行聚焦测试确认当前 Runner 失败**

```bash
go test ./internal/loop -run 'TestRunToolCall.*Mutation|TestRunToolCallDoesNotTreatSameName' -count=1
```

预期：事件结构或 Runner 行为不满足测试。

- [ ] **步骤 6：实现局部捕获状态**

在 Runner 中定义私有状态：

```go
type fileMutationCapture struct {
	target   tool.FileMutationTarget
	before   string
	beforeOK bool
}
```

增加准备函数：

```go
func (runner *Runner) prepareFileMutation(call message.ToolCall) *fileMutationCapture {
	consumer, ok := runner.ui.(ui.FileMutationConsumer)
	if !ok || !consumer.ConsumesFileMutations() || runner.registry == nil {
		return nil
	}
	selected, ok := runner.registry.Get(call.Name)
	if !ok {
		return nil
	}
	mutationTool, ok := selected.(tool.FileMutationTool)
	if !ok {
		return nil
	}
	target, err := mutationTool.FileMutationTarget(call.Input)
	if err != nil {
		return nil
	}
	capture := &fileMutationCapture{target: target}
	if target.BeforeExists {
		data, err := os.ReadFile(target.Path)
		if err != nil {
			return nil
		}
		capture.before = string(data)
		capture.beforeOK = true
	}
	return capture
}
```

增加成功后完成函数：

```go
func completeFileMutationCapture(capture *fileMutationCapture, result message.ToolResult) *ui.FileMutationSnapshot {
	if capture == nil || result.IsError {
		return nil
	}
	data, err := os.ReadFile(capture.target.Path)
	if err != nil {
		return nil
	}
	return &ui.FileMutationSnapshot{
		Before: capture.before, After: string(data),
		BeforeExists: capture.target.BeforeExists && capture.beforeOK,
		AfterExists: true,
	}
}
```

- [ ] **步骤 7：重排单次工具调用数据流**

将 `runToolCallWithCheckpoint` 调整为：

```go
capture := runner.prepareFileMutation(call)
if err := runner.emitToolCall(call, capture); err != nil { ... }
result := runner.executeToolCall(ctx, call)
mutation := completeFileMutationCapture(capture, result)
// checkpoint 仍只接收原 message.ToolResult
if err := runner.emitToolResult(call, result, mutation); err != nil { ... }
```

`emitToolCall` 的事件只可携带 Before：

```go
if capture != nil {
	event.FileMutation = &ui.FileMutationSnapshot{
		Before: capture.before,
		BeforeExists: capture.target.BeforeExists && capture.beforeOK,
	}
}
```

`emitToolResult` 把完整 snapshot 放进结果事件。删除旧的按 `call.Name` 读取 `OldContent` 逻辑。

- [ ] **步骤 8：验证模型可见结果和 trace 不含文件内容**

在成功快照测试中额外断言：

```go
if strings.Contains(result.Content, before) || strings.Contains(result.Content, snapshot.After) {
	t.Fatalf("model-visible result leaked file contents: %q", result.Content)
}
```

如果测试 UI 可读取 token trace，断言 trace `content` 仍只是简洁结果；否则至少确认 `emitToolResult` 的 `recordTraceEvent` 不加入 snapshot 字段。

- [ ] **步骤 9：运行 Runner 测试**

```bash
gofmt -w internal/ui/ui.go internal/loop/runner.go internal/loop/runner_test.go
go test ./internal/loop -run 'TestRunToolCall|Test.*ToolCall' -count=1
go test ./internal/loop -count=1
```

预期：PASS。

- [ ] **步骤 10：提交 Runner 快照通道**

由于这些文件已有未提交改动，先运行 `git diff -- internal/loop/runner.go internal/loop/runner_test.go internal/ui/ui.go` 检查归属，再只暂存本任务 hunk：

```bash
git add -p internal/loop/runner.go internal/loop/runner_test.go
git add internal/ui/ui.go
git commit -m "feat: capture real file mutation snapshots for the UI"
```

---

### 任务 6：Bubble 成功结果使用真实快照重建 diff

**文件：**
- 修改：`internal/ui/bubble/bubble.go`
- 修改：`internal/ui/bubble/types.go`
- 修改：`internal/ui/bubble/app.go`
- 修改：`internal/ui/bubble/transcript.go`
- 修改：`internal/ui/bubble/diff.go`
- 修改：`internal/ui/bubble/utils.go`
- 测试：`internal/ui/bubble/diff_test.go`
- 测试：`internal/ui/bubble/tool_track_test.go`

- [ ] **步骤 1：Bubble 声明消费文件变更快照**

将：

```go
func (u *UI) ConsumesOldContent() bool { return true }
```

替换为：

```go
func (u *UI) ConsumesFileMutations() bool { return true }
```

- [ ] **步骤 2：transcript 条目保存可选快照**

在 `transcriptEntry` 增加：

```go
fileMutation *ui.FileMutationSnapshot
```

保存时复制指针内容，避免外部后续修改：

```go
func cloneFileMutation(snapshot *ui.FileMutationSnapshot) *ui.FileMutationSnapshot {
	if snapshot == nil {
		return nil
	}
	copied := *snapshot
	return &copied
}
```

- [ ] **步骤 3：更新 app 事件分发签名**

工具调用：

```go
m.recordToolCallEntry(msg.ID, msg.Name, json.RawMessage(msg.Input), msg.FileMutation)
```

工具结果：

```go
m.recordToolResultEntry(msg.ToolUseID, msg.Name, status, msg.Content, msg.IsError, msg.FileMutation)
```

- [ ] **步骤 4：先写真实快照优先测试**

在 `diff_test.go` 增加一个明确违背局部输入的快照，证明快照优先：

```go
func TestCompletedFileMutationBodyPrefersSnapshotOverEditStrings(t *testing.T) {
	input := editInputJSON(t, "a.txt", "one", "two")
	snapshot := &ui.FileMutationSnapshot{
		Before: "one\nkeep\none\n",
		After: "two\nkeep\ntwo\n",
		BeforeExists: true,
		AfterExists: true,
	}
	body := formatCompletedFileMutationBody("Edit", input, snapshot, "ok")
	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Edit · ok · +2 -2") {
		t.Fatalf("first = %q", first)
	}
	if strings.Count(body, "+ │ two") != 2 || strings.Count(body, "- │ one") != 2 {
		t.Fatalf("body did not use full snapshot: %q", body)
	}
}
```

- [ ] **步骤 5：写快照缺失回退测试**

```go
func TestCompletedFileMutationBodyFallsBackToLegacyInput(t *testing.T) {
	input := editInputJSON(t, "a.txt", "one", "two")
	body := formatCompletedFileMutationBody("Edit", input, nil, "ok")
	if !strings.Contains(firstToolEntryLine(body), "Edit · ok · +1 -1") {
		t.Fatalf("body = %q", body)
	}
}
```

- [ ] **步骤 6：实现内容选择优先级**

在 `diff.go` 增加：

```go
func fileMutationContents(fields []toolDisplayField, snapshot *ui.FileMutationSnapshot, legacyOld string) (old, newContent string, ok bool) {
	if snapshot != nil && snapshot.AfterExists {
		old := ""
		if snapshot.BeforeExists {
			old = snapshot.Before
		}
		return old, snapshot.After, true
	}
	if fc := firstNonEmptyField(fields, "old_content", "old_string", "before"); fc != "" {
		legacyOld = fc
	}
	newContent = firstNonEmptyField(fields, "new_content", "new_string", "replacement", "content", "after")
	if legacyOld == "" && newContent == "" {
		return "", "", false
	}
	return legacyOld, newContent, true
}
```

注意：`diff.go` 需导入 `paw/internal/ui`。不存在 import cycle：`bubble` 依赖 `ui`，`ui` 不依赖 `bubble`。

- [ ] **步骤 7：让格式化函数接受 snapshot**

将内部函数签名统一为：

```go
func formatFileMutationToolCallBody(name string, fields []toolDisplayField, snapshot *ui.FileMutationSnapshot, legacyOld string) string
func fileMutationChangeCounts(fields []toolDisplayField, snapshot *ui.FileMutationSnapshot, legacyOld string) (diffTotals, bool)
func fileMutationDiffPreview(fields []toolDisplayField, snapshot *ui.FileMutationSnapshot, legacyOld string) string
```

新增完成态入口：

```go
func formatCompletedFileMutationBody(name string, input json.RawMessage, snapshot *ui.FileMutationSnapshot, status string) string {
	body := formatFileMutationToolCallBody(name, toolInputFields(input), snapshot, "")
	return setToolCallBodyStatus(body, status)
}
```

运行态调用时传调用事件中的 Before-only snapshot；完成态传完整 snapshot。

- [ ] **步骤 8：结果到达时重建成功文件变更 body**

在 `recordToolResultEntry` 匹配 running entry 后：

```go
entry.fileMutation = cloneFileMutation(snapshot)
if !isError && isFileMutationTool(name) && snapshot != nil {
	entry.body = formatCompletedFileMutationBody(name, entry.toolInput, snapshot, status)
} else {
	entry.body = completeToolCallBody(name, entry.body, status, content)
}
```

这里的 `isFileMutationTool` 只用于展示选择；安全文件读取已经在 Runner 用能力接口完成。MCP 同名工具没有 snapshot，因此不会进入真实快照分支。

- [ ] **步骤 9：增加 transcript 端到端 replace_all 测试**

在 `tool_track_test.go` 构造调用和结果消息：

```go
func TestToolResultRebuildsEditDiffFromCompleteMutationSnapshot(t *testing.T) {
	m := testAppModel(t)
	call := toolCallMsg(ui.ToolCallEvent{
		ID: "edit-1", Name: "Edit",
		Input: []byte(`{"file_path":"a.txt","old_string":"x","new_string":"y","replace_all":true}`),
		FileMutation: &ui.FileMutationSnapshot{Before: "x\nkeep\nx\n", BeforeExists: true},
	})
	updated, _ := m.Update(call)
	m = updated.(appModel)
	result := toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "edit-1", Name: "Edit", Content: "edited a.txt (2 replacements)",
		FileMutation: &ui.FileMutationSnapshot{
			Before: "x\nkeep\nx\n", After: "y\nkeep\ny\n",
			BeforeExists: true, AfterExists: true,
		},
	})
	updated, _ = m.Update(result)
	m = updated.(appModel)
	entry := m.transcript[len(m.transcript)-1]
	if !strings.Contains(firstToolEntryLine(entry.body), "+2 -2") {
		t.Fatalf("body = %q", entry.body)
	}
}
```

本测试直接使用现有 `newTestModel(&fakeRunner{})` 构造 `appModel`，不新增重复 fixture。

- [ ] **步骤 10：增加错误结果无成功 diff 测试**

构造 `IsError:true`、`FileMutation:nil` 的结果，断言：

```go
if strings.Contains(entry.body, "Added") || strings.Contains(entry.body, "+1 -1") {
	t.Fatalf("error entry contains success diff: %q", entry.body)
}
if !entry.toolExpanded || !strings.Contains(entry.toolResult, "use Read first") {
	t.Fatalf("error entry = %+v", entry)
}
```

- [ ] **步骤 11：运行 Bubble 聚焦测试**

```bash
gofmt -w internal/ui/bubble/bubble.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/transcript.go internal/ui/bubble/diff.go internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go internal/ui/bubble/tool_track_test.go
go test ./internal/ui/bubble -run 'TestCompletedFileMutation|TestToolResultRebuildsEditDiff|Test.*Error.*Diff|TestFileMutation' -count=1
```

预期：PASS。

- [ ] **步骤 12：运行 Bubble 全包测试**

```bash
go test ./internal/ui/bubble -count=1
```

预期：PASS，旧会话/旧调用路径仍通过 nil snapshot 回退。

- [ ] **步骤 13：提交 Bubble 展示变更**

这些文件已有大量未提交改动，只暂存本任务 hunk：

```bash
git add -p internal/ui/bubble/bubble.go internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/transcript.go internal/ui/bubble/diff.go internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go internal/ui/bubble/tool_track_test.go
git commit -m "feat: render Edit diffs from complete file snapshots"
```

---

### 任务 7：补齐集成回归与失败降级

**文件：**
- 测试：`internal/loop/runner_test.go`
- 测试：`internal/ui/bubble/diff_test.go`
- 测试：`internal/ui/bubble/tool_track_test.go`
- 修改（仅当新增边界测试暴露缺陷时）：`internal/loop/runner.go`、`internal/ui/bubble/diff.go`、`internal/ui/bubble/utils.go`

- [ ] **步骤 1：增加 Write 新建空文件与已有空文件快照测试**

分别断言：

```go
// 新文件
BeforeExists == false
AfterExists == true
Before == ""
After == "content\n"

// 已有空文件
BeforeExists == true
Before == ""
AfterExists == true
```

该测试保证存在性布尔值不被空字符串混淆。

- [ ] **步骤 2：增加 After 捕获失败不改判工具成功的测试**

注册一个实现 `FileMutationTool` 的测试工具：它返回成功但在 Run 中删除目标文件。断言：

```go
result.IsError == false
result.Content == "ok"
capture.results[0].FileMutation == nil
```

- [ ] **步骤 3：增加快照完全相同不显示 `+0 -0` 的测试**

```go
snapshot := &ui.FileMutationSnapshot{
	Before: "same\n", After: "same\n",
	BeforeExists: true, AfterExists: true,
}
body := formatCompletedFileMutationBody("Write", writeInputJSON(t, "a.txt", "same\n"), snapshot, "ok")
if strings.Contains(body, "+0 -0") {
	t.Fatalf("body = %q", body)
}
```

- [ ] **步骤 4：增加完整文件多处变更上下文和折叠测试**

构造 20 行文件，在首尾各替换一次，断言：

- 摘要为真实总数 `+2 -2`。
- 展开详情包含两处变化。
- 中间长未修改区域包含 `···`。
- 预览仍受 `maxDiffPreviewLines` 限制。

- [ ] **步骤 5：增加绝对路径快照测试**

使用工作区内绝对路径调用 Edit，确认 `FileMutationTarget.Path` 与快照读取正确；使用工作区外绝对路径确认无快照且工具返回路径逃逸错误。

- [ ] **步骤 6：运行分层测试**

```bash
go test ./internal/tool/file -count=1
go test ./internal/loop -count=1
go test ./internal/ui/bubble -count=1
```

预期：全部 PASS。

- [ ] **步骤 7：运行 race detector**

```bash
go test -race ./internal/tool/file ./internal/loop -count=1
```

预期：PASS，无 data race。

- [ ] **步骤 8：运行全量测试和 vet**

```bash
go test ./... -count=1
go vet ./...
```

预期：PASS。如出现与本功能无关的既有失败，记录完整命令和失败输出，不修改无关模块掩盖问题。

- [ ] **步骤 9：检查工作区和空白错误**

```bash
git diff --check
git status --short
git diff --stat
```

确认：

- 没有空白错误。
- 未删除或还原用户原有改动。
- 本功能相关测试文件均已纳入提交。
- `edit-test.txt`、`docs/codex/plans/...` 等现有未跟踪文件没有被误提交。

- [ ] **步骤 10：提交最终回归测试**

```bash
git add -p internal/loop/runner_test.go internal/ui/bubble/diff_test.go internal/ui/bubble/tool_track_test.go
git commit -m "test: cover Edit snapshot fallbacks and edge cases"
```

---

## 规格覆盖自检

- 严格先 Read：任务 1、2。
- stale write 与重新 Read：任务 1、2。
- 连续 Edit 基线更新：任务 2。
- 精确匹配、唯一匹配、`replace_all`：任务 2 及现有测试保留。
- 权限保留：任务 3。
- Write 行为不变：任务 3 的 characterization tests。
- 内置工具类型识别而非名称识别：任务 4、5。
- 完整 Before/After 且不进入模型结果：任务 5。
- After 捕获失败安全降级：任务 7。
- TUI 快照优先、旧输入回退：任务 6。
- `replace_all` 全部变化统计：任务 6。
- 错误无成功 diff：任务 6。
- 空内容与不存在状态区分：任务 7。
- 相对路径、绝对路径和工作区安全边界：任务 4、7。
- 旧会话 nil snapshot 兼容：任务 6 的回退测试。
- 不覆盖工作区既有改动：全局约束及任务 5/6/7 的交互式暂存步骤。

## 占位符与类型一致性自检

- 生产接口统一使用 `tool.FileMutationTool`、`tool.FileMutationTarget`、`ui.FileMutationSnapshot`。
- UI 消费能力统一命名为 `ConsumesFileMutations() bool`。
- 快照在调用事件中可仅含 Before，在成功结果事件中含完整 Before/After。
- `message.ToolResult` 不增加快照字段，避免模型可见数据通道被污染。
- 所有计划中的测试桩必须在执行时写成完整可运行测试；任务 5 步骤 4 明确禁止保留注释桩。
- 无 `TODO`、`TBD`、待定实现或未定义生产类型。
