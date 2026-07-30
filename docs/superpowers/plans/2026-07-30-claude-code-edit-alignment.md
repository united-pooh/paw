# Claude Code Edit Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a native `Edit` tool, read-after-write conflict protection, and a structured line-diff with a folded `+N -M` summary rendered in the Bubble Tea UI — aligning Paw's file-mutation tooling with Claude Code's `Edit` + structured-diff + stale-write triple, by reusing and upgrading the existing `OldContent`/`lcsEditScript` preview path rather than building a parallel pipeline.

**Architecture:** A new `EditTool` (`internal/tool/file/edit.go`) accepts `{file_path, old_string, new_string, replace_all?}`, resolves the path under the workspace root, reads the file, enforces `old_string` uniqueness (unless `replace_all`), applies a string replacement, and writes back atomically. A new shared `ReadStateStore` (`internal/tool/file/read_state.go`) records a per-path SHA-256 content hash when `ReadTool` runs; `EditTool` and `WriteTool` verify the on-disk hash matches the last-read hash before writing and error on external modification. The `Tool.Run() (string, error)` signature is unchanged — the line diff stays UI-side, computed from `ToolCallEvent.OldContent` plus the tool input fields. The existing `lcsEditScript`/`fileMutationDiffPreview` logic in `internal/ui/bubble/utils.go` is refactored into a named `DiffLine` type (`internal/ui/bubble/diff.go`) with added/removed counts, reused by `formatFileMutationToolCallBody` to emit a folded `+N -M` summary on the tool track line. The Bubble Tea renderer already colors `+`/`-` lines via `renderToolDetailLines`/`diffDetailLineMarker`; this plan adds tests confirming that path for `Edit` and `Write`.

**Tech Stack:** Go 1.25.0 (module `paw`), Bubble Tea `tea.Cmd`, Lip Gloss, `crypto/sha256`, Go `testing` package. No new external dependencies.

## Global Constraints

- Module path is `paw`; Go 1.25.0. Use only the standard library (`crypto/sha256`, `os`, `path/filepath`, `strings`, `sync`, `encoding/json`, `fmt`). Do NOT add any dependency.
- Do NOT change the `Tool.Run(ctx, json.RawMessage) (string, error)` signature (defined in `internal/tool/tool.go:8`) or introduce a `StructuredTool`/`ToolOutput`/`Metadata` interface in this plan — that is a separate future plan. The diff is computed UI-side.
- Do NOT change spinner, equalizer, context-meter, ripple, or terminal-cursor animation behavior. This plan touches only tool and transcript-rendering code.
- Path safety: every file tool must resolve paths via `resolvePathWithinRoot`/`resolvePathWithinRoots` (`internal/tool/file/path.go`) so writes cannot escape the workspace root.
- `EditTool` and `WriteTool` are NOT concurrency-safe — do NOT implement `IsConcurrencySafe`; the runner serializes them. `ReadTool` keeps returning `true` from `IsConcurrencySafe`.
- Tests must NOT add wall-clock sleeps; use `t.TempDir()` for filesystem isolation.
- File writes preserve `0o644` permissions and create parent dirs via `os.MkdirAll`, matching existing `WriteTool`.
- Both tool registries must register `EditTool` and share one `*ReadStateStore`: `cmd/agent/main.go:registerTools` (line 574) and `internal/subagent/manager.go:newBaseToolRegistry` (line 1042).
- `gofmt -w` every modified `.go` file before committing; `go test ./... -count=1` and `go vet ./...` must pass after each task.

## Non-goals

- Introducing a `StructuredTool`/`ToolOutput`/`Metadata` channel (future plan).
- A global Git diff panel or `git diff HEAD` integration (excluded per scope decision).
- LSP / VSCode editor notifications.
- Changing `WriteTool`'s input schema or the `Write` tool name.
- Changing diff colors, glyphs, or the numbered-line format beyond adding the `+N -M` summary token.
- UTF-16 / CRLF preservation (out of scope; Paw writes UTF-8 bytes as today).

## File Structure

- **Create** `internal/tool/file/edit.go` — `EditTool` struct + `Run` (path resolve, read, uniqueness, replace, atomic write).
- **Create** `internal/tool/file/atomic.go` — `atomicWriteFile(target string, content []byte) error` shared by `EditTool` and `WriteTool`.
- **Create** `internal/tool/file/edit_test.go` — `EditTool` behavior tests.
- **Create** `internal/tool/file/read_state.go` — `ReadStateStore` (mutex-guarded `map[string]string` of path→hash), `Record`, `Verify`, `RecordAfterWrite`.
- **Create** `internal/tool/file/read_state_test.go` — `ReadStateStore` + stale-write tests.
- **Create** `internal/tool/file/write_test.go` — `WriteTool` stale-write + atomic-write tests (currently `WriteTool` has no dedicated tests).
- **Modify** `internal/tool/file/read.go` — add `ReadState *ReadStateStore` field; call `Record` after a successful read.
- **Modify** `internal/tool/file/write.go` — add `ReadState *ReadStateStore` field; verify stale-write before writing; switch `os.WriteFile` to `atomicWriteFile`; record new state after write.
- **Modify** `cmd/agent/main.go:574-599` (`registerTools`) — construct one `*ReadStateStore`, inject into `ReadTool`/`WriteTool`/`EditTool`; register `EditTool`.
- **Modify** `internal/subagent/manager.go:1042-1053` (`newBaseToolRegistry`) — same injection + `EditTool` registration.
- **Create** `internal/ui/bubble/diff.go` — `DiffLine` type, `structuredDiff`, `renderDiffPreview`, `diffCounts`, `fileMutationContents`.
- **Create** `internal/ui/bubble/diff_test.go` — characterization tests for current diff behavior + new `DiffLine`/counts/summary tests.
- **Modify** `internal/ui/bubble/utils.go:96-211` — refactor `fileMutationDiffPreview` + `lcsEditScript` to use `diff.go`; add folded `+N -M` summary in `formatFileMutationToolCallBody`.
- **Create** `cmd/agent/register_test.go` — assert `registerTools` registers `Edit`.

---
### Task 1: Native `EditTool` with atomic write

**Files:**
- Create: `internal/tool/file/atomic.go`
- Create: `internal/tool/file/edit.go`
- Test: `internal/tool/file/edit_test.go`
- Modify: `cmd/agent/main.go:574-599` (`registerTools`)
- Modify: `internal/subagent/manager.go:1042-1053` (`newBaseToolRegistry`)
- Create: `cmd/agent/register_test.go`

**Interfaces:**
- Consumes: `resolvePathWithinRoot(root, target string) (string, error)` (`internal/tool/file/path.go:9`); `relativePath(root, target string) string` (`internal/tool/file/path.go:49`); the `tool.Tool` interface (`internal/tool/tool.go:8`: `Name() string`, `Description() string`, `Run(ctx, json.RawMessage) (string, error)`, `InputSchema() json.RawMessage`).
- Produces: `EditTool{Root string}` (no `ReadState` yet — added in Task 2) implementing `tool.Tool`; input schema `{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["file_path","old_string","new_string"]}`; result string `edited <relpath> (<n> replacement<s>)`. Also produces `atomicWriteFile(target string, content []byte) error`.

- [ ] **Step 1: Write the failing test for `atomicWriteFile`**

Create `internal/tool/file/atomic_test.go`:

```go
package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "out.txt")

	if err := atomicWriteFile(target, []byte("hello")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "hello" {
		t.Fatalf("after first write got %q, %v", string(got), err)
	}

	if err := atomicWriteFile(target, []byte("world")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != "world" {
		t.Fatalf("after overwrite got %q, %v", string(got), err)
	}

	// No leftover temp files in the directory.
	entries, err := os.ReadDir(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in dir, got %d", len(entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/file -run TestAtomicWriteFileCreatesAndOverwrites -count=1`
Expected: FAIL with `undefined: atomicWriteFile`.

- [ ] **Step 3: Implement `atomicWriteFile`**

Create `internal/tool/file/atomic.go`:

```go
package file

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes content to target by first writing a temp file in
// the same directory and renaming it, so a crash never leaves a partially
// written file. Parent directories are created. Files are written 0o644.
func atomicWriteFile(target string, content []byte) error {
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
	if err := tmp.Chmod(0o644); err != nil {
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/file -run TestAtomicWriteFileCreatesAndOverwrites -count=1`
Expected: PASS.

- [ ] **Step 5: Write the failing tests for `EditTool`**

Create `internal/tool/file/edit_test.go`:

```go
package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newEditTool(t *testing.T) (*EditTool, string) {
	t.Helper()
	root := t.TempDir()
	return &EditTool{Root: root}, root
}

func writeExistingFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func runEdit(t *testing.T, tool *EditTool, input string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), []byte(input))
}

func TestEditReplacesUniqueMatch(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "package a\n\nfunc run() int { return 1 }\n")

	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q, want replacement count", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(got), "return 2") || strings.Contains(string(got), "return 1") {
		t.Fatalf("file not updated: %q", string(got))
	}
}

func TestEditReplaceAllReplacesEveryMatch(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\nx\nx\n")

	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"y","replace_all":true}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "3 replacements") {
		t.Fatalf("output = %q, want 3 replacements", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != "y\ny\ny\n" {
		t.Fatalf("file = %q, want y\\ny\\ny\\n", string(got))
	}
}

func TestEditRejectsAmbiguousMatchWithoutReplaceAll(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\nx\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "matches 2 locations") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != "x\nx\n" {
		t.Fatalf("file must be unchanged on error, got %q", string(got))
	}
}

func TestEditRejectsMissingOldString(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "nothing here\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "old_string not found") {
		t.Fatalf("err = %v, want not-found error", err)
	}
}

func TestEditRejectsIdenticalOldAndNew(t *testing.T) {
	tool, root := newEditTool(t)
	writeExistingFile(t, root, "a.go", "x\n")

	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"x","new_string":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("err = %v, want must-differ error", err)
	}
}

func TestEditRejectsMissingFile(t *testing.T) {
	tool, _ := newEditTool(t)
	_, err := runEdit(t, tool, `{"file_path":"missing.go","old_string":"x","new_string":"y"}`)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEditRejectsPathOutsideRoot(t *testing.T) {
	tool, _ := newEditTool(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runEdit(t, tool, `{"file_path":"`+outside+`","old_string":"x","new_string":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "escapes allowed roots") {
		t.Fatalf("err = %v, want escape error", err)
	}
}

func TestEditSupportsMultilineOldString(t *testing.T) {
	tool, root := newEditTool(t)
	original := "func a() {\n\treturn 1\n}\nfunc b() {\n\treturn 1\n}\n"
	writeExistingFile(t, root, "a.go", original)
	multiline := "func b() {\n\treturn 1\n}"
	replacement := "func b() {\n\treturn 2\n}"
	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":`+jsonString(multiline)+`,"new_string":`+jsonString(replacement)+`}`)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(got), "func b() {\n\treturn 2\n}") || strings.Contains(string(got), "func a() {\n\treturn 2") {
		t.Fatalf("multiline edit wrong: %q", string(got))
	}
}
```

Add the `jsonString` helper to `edit_test.go`:

```go
// jsonString encodes s as a JSON string literal so multi-line old_string/
// new_string values can be embedded directly into the JSON input passed to Run.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
```

(Add `"encoding/json"` to the test imports.)

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/tool/file -run TestEdit -count=1`
Expected: FAIL with `undefined: EditTool`.

- [ ] **Step 7: Implement `EditTool`**

Create `internal/tool/file/edit.go`:

```go
package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditTool performs an exact string replacement on an existing file in the
// workspace. It mirrors Claude Code's Edit contract: old_string must be unique
// unless replace_all is true, and the file is overwritten atomically.
type EditTool struct {
	Root string
	// ReadState is wired in Task 2 for stale-write protection.
	ReadState *ReadStateStore
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditTool) Name() string { return "Edit" }

func (t *EditTool) Description() string {
	return "对工作区内已有文件做精确字符串替换：将 old_string 替换为 new_string。" +
		"默认 old_string 必须在文件中唯一出现；设 replace_all=true 可替换全部匹配。" +
		`示例输入: {"file_path":"internal/foo.go","old_string":"return 1","new_string":"return 2"}`
}

func (t *EditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["file_path","old_string","new_string"]}`)
}

func (t *EditTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	if in.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if in.OldString == in.NewString {
		return "", fmt.Errorf("old_string and new_string must differ")
	}

	target, err := resolvePathWithinRoot(t.Root, in.FilePath)
	if err != nil {
		return "", err
	}

	current, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if t.ReadState != nil {
		if err := t.ReadState.Verify(target, current); err != nil {
			return "", err
		}
	}

	count := strings.Count(string(current), in.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", relativePath(t.Root, target))
	}
	if count > 1 && !in.ReplaceAll {
		return "", fmt.Errorf("old_string matches %d locations in %s; set replace_all=true or add surrounding context to make it unique", count, relativePath(t.Root, target))
	}

	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(string(current), in.OldString, in.NewString)
	} else {
		updated = strings.Replace(string(current), in.OldString, in.NewString, 1)
	}

	if err := atomicWriteFile(target, []byte(updated)); err != nil {
		return "", err
	}
	if t.ReadState != nil {
		t.ReadState.RecordAfterWrite(target, []byte(updated))
	}

	replacements := count
	plural := "s"
	if replacements == 1 {
		plural = ""
	}
	return fmt.Sprintf("edited %s (%d replacement%s)", relativePath(t.Root, target), replacements, plural), nil
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./internal/tool/file -run 'TestEdit|TestAtomic' -count=1`
Expected: PASS (all seven Edit tests + the atomic test).

- [ ] **Step 9: Register `EditTool` in the main agent registry**

Modify `cmd/agent/main.go` in `registerTools` (line 574). After the line `registry.Register(&toolfile.WriteTool{Root: root})` (line 580), add the `EditTool` registration. The edited block becomes:

```go
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
```

Note: `ReadState` fields on `ReadTool`/`WriteTool`/`EditTool` compile even though `ReadStateStore` is defined in Task 2 — but to keep Task 1 self-contained and compiling, define `ReadStateStore` as a minimal stub now OR defer the `ReadState` field injection to Task 2. **Choose the stub path now** so Task 1 compiles: in `read_state.go` (created in Task 2) — instead, for Task 1 only, omit the `ReadState` fields from the registered structs and register `&toolfile.EditTool{Root: root}`, `&toolfile.WriteTool{Root: root}`, `&toolfile.ReadTool{Root: root, ReadRoots: readRoots}` (current shape). Then Task 2 adds the `ReadState` fields and re-wires registration. So for Task 1 the edit is:

```go
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.EditTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
```

- [ ] **Step 10: Register `EditTool` in the subagent base registry**

Modify `internal/subagent/manager.go` in `newBaseToolRegistry` (line 1042). After `registry.Register(&toolfile.WriteTool{Root: root})` (line 1047), add `registry.Register(&toolfile.EditTool{Root: root})`.

```go
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.EditTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
```

- [ ] **Step 11: Write registration test**

Create `cmd/agent/register_test.go`:

```go
package main

import (
	"testing"

	toolfile "paw/internal/tool/file"
	"paw/internal/tool"
)

func TestRegisterToolsIncludesEdit(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	ed, ok := registry.Get("Edit")
	if !ok {
		t.Fatal("Edit tool not registered")
	}
	if ed.Name() != "Edit" {
		t.Fatalf("Edit tool name = %q", ed.Name())
	}
	if _, ok := ed.(*toolfile.EditTool); !ok {
		t.Fatalf("Edit tool concrete type = %T, want *toolfile.EditTool", ed)
	}
}
```

- [ ] **Step 12: Run tests to verify they pass**

Run: `go test ./cmd/agent -run TestRegisterToolsIncludesEdit -count=1 && go test ./internal/subagent -count=1`
Expected: PASS. (If `registerTools` with nil subagent manager panics, pass a real `*subagent.Manager` constructed via its constructor instead; the registration of subagent tools calls `subagent.NewTool(nil, "")` which is safe to register but verify before assuming.)

- [ ] **Step 13: Run full repo tests + vet + format**

Run: `gofmt -w internal/tool/file/edit.go internal/tool/file/atomic.go internal/tool/file/edit_test.go internal/tool/file/atomic_test.go cmd/agent/main.go cmd/agent/register_test.go internal/subagent/manager.go && go test ./... -count=1 && go vet ./...`
Expected: PASS (all tests, including existing `internal/tool/file/path_test.go`).

- [ ] **Step 14: Commit**

```bash
git add internal/tool/file/edit.go internal/tool/file/atomic.go internal/tool/file/edit_test.go internal/tool/file/atomic_test.go cmd/agent/main.go cmd/agent/register_test.go internal/subagent/manager.go
git commit -m "feat: add native Edit tool with exact string replacement and atomic write"
```

---
### Task 2: Stale-write protection via shared `ReadStateStore`

**Files:**
- Create: `internal/tool/file/read_state.go`
- Test: `internal/tool/file/read_state_test.go`
- Create: `internal/tool/file/write_test.go`
- Modify: `internal/tool/file/read.go` (add `ReadState` field + `Record` after read)
- Modify: `internal/tool/file/write.go` (add `ReadState` field + `Verify` before write + `RecordAfterWrite` after, switch to `atomicWriteFile`)
- Modify: `internal/tool/file/edit.go` (already has `ReadState` field from Task 1; ensure `Verify`/`RecordAfterWrite` calls exist — they do)
- Modify: `cmd/agent/main.go:registerTools` (inject one shared `*ReadStateStore`)
- Modify: `internal/subagent/manager.go:newBaseToolRegistry` (inject one shared `*ReadStateStore`)

**Interfaces:**
- Consumes: `ReadStateStore` produced below; existing `resolvePathWithinRoot`/`relativePath`; `atomicWriteFile` (Task 1).
- Produces: `ReadStateStore{Record(path string, content []byte); Verify(path string, current []byte) error; RecordAfterWrite(path string, content []byte)}`. `ReadTool`, `WriteTool`, `EditTool` each gain a `ReadState *ReadStateStore` field.

- [ ] **Step 1: Write the failing tests for `ReadStateStore`**

Create `internal/tool/file/read_state_test.go`:

```go
package file

import (
	"errors"
	"strings"
	"testing"
)

func TestReadStateStoreVerifyNoOpWithoutPriorRead(t *testing.T) {
	s := NewReadStateStore()
	// No prior Record: Verify must be lenient (no error).
	if err := s.Verify("/some/path", []byte("anything")); err != nil {
		t.Fatalf("Verify without prior read = %v, want nil", err)
	}
}

func TestReadStateStoreVerifyPassesAfterUnchangedRead(t *testing.T) {
	s := NewReadStateStore()
	content := []byte("hello\n")
	s.Record("/p", content)
	if err := s.Verify("/p", content); err != nil {
		t.Fatalf("Verify after unchanged read = %v", err)
	}
}

func TestReadStateStoreVerifyFailsOnExternalModification(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("hello\n"))
	err := s.Verify("/p", []byte("hello world\n"))
	if err == nil {
		t.Fatal("expected stale-write error, got nil")
	}
	if !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want modified-since-read", err)
	}
}

func TestReadStateStoreRecordAfterWriteResetsBaseline(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("v1\n"))
	s.RecordAfterWrite("/p", []byte("v2\n"))
	// After a write, the recorded baseline is v2; v2 must verify clean.
	if err := s.Verify("/p", []byte("v2\n")); err != nil {
		t.Fatalf("Verify after write = %v", err)
	}
	// v1 must now be stale.
	if err := s.Verify("/p", []byte("v1\n")); err == nil {
		t.Fatal("expected stale error after baseline reset")
	}
}

func TestReadStateStoreVerifyConcurrentSafe(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("x"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = s.Verify("/p", []byte("x"))
		}
	}()
	for i := 0; i < 200; i++ {
		s.Record("/p", []byte("x"))
	}
	<-done
	// No data race detector failure == pass (run with -race).
	if err := s.Verify("/p", []byte("x")); err != nil {
		t.Fatalf("final Verify = %v", err)
	}
	_ = errors.New
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tool/file -run TestReadStateStore -count=1`
Expected: FAIL with `undefined: NewReadStateStore`.

- [ ] **Step 3: Implement `ReadStateStore`**

Create `internal/tool/file/read_state.go`:

```go
package file

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// ReadStateStore records a per-path content hash when files are Read, so that
// Edit/Write can detect external modification between the last Read and the
// write (lost-update / stale-write protection). If no prior Read was recorded
// for a path, Verify is lenient and returns nil.
type ReadStateStore struct {
	mu     sync.Mutex
	states map[string]string
}

func NewReadStateStore() *ReadStateStore {
	return &ReadStateStore{states: make(map[string]string)}
}

// Record stores the content hash for path as the last-seen-on-Read baseline.
func (s *ReadStateStore) Record(path string, content []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string]string)
	}
	s.states[path] = contentHash(content)
}

// Verify returns an error if a prior Read recorded a hash for path and
// current no longer matches it. Returns nil when no prior Read was recorded.
func (s *ReadStateStore) Verify(path string, current []byte) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	recorded, ok := s.states[path]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if got := contentHash(current); got != recorded {
		return fmt.Errorf("file has been modified since last read: %s; read it again before editing", path)
	}
	return nil
}

// RecordAfterWrite updates the baseline to the freshly written content so a
// subsequent Edit on the same file does not falsely report stale.
func (s *ReadStateStore) RecordAfterWrite(path string, content []byte) {
	s.Record(path, content)
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tool/file -run TestReadStateStore -race -count=1`
Expected: PASS.

- [ ] **Step 5: Write the failing stale-write tests for `WriteTool`**

Create `internal/tool/file/write_test.go`:

```go
package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteToolCreatesNewFile(t *testing.T) {
	root := t.TempDir()
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	out, err := tool.Run(context.Background(), []byte(`{"file_path":"new.txt","content":"hi\n"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	if string(got) != "hi\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolOverwritesAfterUnchangedRead(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	if _, err := readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`)); err != nil {
		t.Fatal(err)
	}

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"new\n"}`))
	if err != nil {
		t.Fatalf("overwrite after unchanged read: %v", err)
	}
	got, _ := os.ReadFile(full)
	if string(got) != "new\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolRejectsStaleWriteAfterExternalModification(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`))

	// User/IDE changes the file after the model's Read.
	_ = os.WriteFile(full, []byte("user-changed\n"), 0o644)

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"model-rewrite\n"}`))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-write error", err)
	}
	// File must be untouched on a stale-write rejection.
	got, _ := os.ReadFile(full)
	if string(got) != "user-changed\n" {
		t.Fatalf("file = %q, want user content preserved", string(got))
	}
	_ = errors.New
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/tool/file -run TestWriteTool -count=1`
Expected: FAIL — `WriteTool` has no `ReadState` field and does not verify (Task 1 added the field but registration used `{Root: root}` without `ReadState`; the struct field exists from Task 1's edit.go? No — Task 1's `EditTool` has the field, but `WriteTool` does not yet). Compile error or no stale protection.

- [ ] **Step 7: Wire `ReadState` into `ReadTool`**

Modify `internal/tool/file/read.go`. Add the field to the struct (after `ReadRoots []string`):

```go
type ReadTool struct {
	Root      string
	ReadRoots []string
	ReadState *ReadStateStore
}
```

In `Run`, after the successful `os.ReadFile` and before `return string(data), nil`, record the read state:

```go
	if t.ReadState != nil {
		t.ReadState.Record(target, data)
	}
	return string(data), nil
```

- [ ] **Step 8: Wire `ReadState` + atomic write + stale check into `WriteTool`**

Modify `internal/tool/file/write.go`. Add the field:

```go
type WriteTool struct {
	Root      string
	ReadState *ReadStateStore
}
```

Replace the body of `Run` (currently lines 32-57) with:

```go
func (t *WriteTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	target, err := resolvePathWithinRoot(t.Root, in.FilePath)
	if err != nil {
		return "", err
	}

	// Stale-write guard: if the model Read this file earlier, reject when the
	// on-disk content no longer matches the recorded baseline.
	if existing, rerr := os.ReadFile(target); rerr == nil {
		if t.ReadState != nil {
			if verr := t.ReadState.Verify(target, existing); verr != nil {
				return "", verr
			}
		}
	} else if !os.IsNotExist(rerr) {
		return "", rerr
	}

	if err := atomicWriteFile(target, []byte(in.Content)); err != nil {
		return "", err
	}
	if t.ReadState != nil {
		t.ReadState.RecordAfterWrite(target, []byte(in.Content))
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), relativePath(t.Root, target)), nil
}
```

Note the imports change: `os` is still needed (`os.ReadFile`, `os.IsNotExist`); `path/filepath` is no longer directly used in write.go (it was used for `filepath.Dir` + `os.MkdirAll`, now handled inside `atomicWriteFile`). Remove the now-unused `"path/filepath"` import if `go vet`/compiler flags it; keep `"os"`.

- [ ] **Step 9: Confirm `EditTool` already calls `Verify`/`RecordAfterWrite`**

Re-open `internal/tool/file/edit.go`. The Task 1 implementation already contains:
```go
	if t.ReadState != nil {
		if err := t.ReadState.Verify(target, current); err != nil {
			return "", err
		}
	}
```
before the `strings.Count` line, and:
```go
	if t.ReadState != nil {
		t.ReadState.RecordAfterWrite(target, []byte(updated))
	}
```
after `atomicWriteFile`. No change needed. If the Task 1 code was written exactly as specified, this is a verification step only.

- [ ] **Step 10: Add a stale-write test for `EditTool`**

Append to `internal/tool/file/edit_test.go`:

```go
func TestEditRejectsStaleWriteAfterExternalModification(t *testing.T) {
	root := t.TempDir()
	full := writeExistingFile(t, root, "a.go", "func a() int { return 1 }\n")

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.go"}`))

	// External modification after the model's Read.
	_ = os.WriteFile(full, []byte("func a() int { return 9 }\n"), 0o644)

	tool := &EditTool{Root: root, ReadState: readState}
	_, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-write error", err)
	}
}

func TestEditSucceedsAfterUnchangedRead(t *testing.T) {
	root := t.TempDir()
	writeExistingFile(t, root, "a.go", "func a() int { return 1 }\n")

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.go"}`))

	tool := &EditTool{Root: root, ReadState: readState}
	out, err := runEdit(t, tool, `{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "1 replacement") {
		t.Fatalf("output = %q", out)
	}
}
```

- [ ] **Step 11: Run file-package tests with -race**

Run: `go test ./internal/tool/file -race -count=1`
Expected: PASS (all ReadStateStore, WriteTool, EditTool stale-write tests).

- [ ] **Step 12: Inject the shared `*ReadStateStore` in both registries**

Modify `cmd/agent/main.go` `registerTools` — replace the Task 1 registration block with the wired version:

```go
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
```

Modify `internal/subagent/manager.go` `newBaseToolRegistry` the same way:

```go
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
```

Update the `cmd/agent/register_test.go` expectation if needed — the test asserts `*toolfile.EditTool`, which still holds; the concrete type is unchanged.

- [ ] **Step 13: Run full repo tests + vet + format**

Run: `gofmt -w internal/tool/file/read_state.go internal/tool/file/read.go internal/tool/file/write.go internal/tool/file/edit.go internal/tool/file/read_state_test.go internal/tool/file/write_test.go internal/tool/file/edit_test.go cmd/agent/main.go internal/subagent/manager.go && go test ./... -race -count=1 && go vet ./...`
Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add internal/tool/file/read_state.go internal/tool/file/read_state_test.go internal/tool/file/write_test.go internal/tool/file/read.go internal/tool/file/write.go cmd/agent/main.go internal/subagent/manager.go
git commit -m "feat: add read-state stale-write protection to Read/Write/Edit"
```

---
### Task 3: Structured `DiffLine` type (behavior-preserving refactor)

**Files:**
- Create: `internal/ui/bubble/diff.go`
- Create: `internal/ui/bubble/diff_test.go`
- Modify: `internal/ui/bubble/utils.go:108-265` (refactor `fileMutationDiffPreview` + `lcsEditScript` to use `diff.go`)

**Interfaces:**
- Consumes: existing `splitLines`, `limitDiffPreviewLines`, `maxInt`, `minInt` (all in `internal/ui/bubble/utils.go`).
- Produces: `DiffLine{Kind rune; Number int; Text string}`, `structuredDiff(oldLines, newLines []string) []DiffLine`, `renderDiffPreview(lines []DiffLine) string`, `diffCounts(lines []DiffLine) (added, removed int)`, `fileMutationContents(fields []toolDisplayField, oldContent string) (old, new string)`. The public behavior of `fileMutationDiffPreview` is byte-identical to before this task.

- [ ] **Step 1: Write characterization tests capturing CURRENT diff behavior**

These tests pin the existing `fileMutationDiffPreview` output so the refactor cannot change it. Create `internal/ui/bubble/diff_test.go`:

```go
package bubble

import (
	"strings"
	"testing"
)

func editInputJSON(t *testing.T, file, oldStr, newStr string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": file, "old_string": oldStr, "new_string": newStr})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeInputJSON(t *testing.T, file, content string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": file, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFileMutationDiffPreviewReplaceRegion(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if !strings.Contains(got, "- │ return 1") {
		t.Fatalf("missing removed line in %q", got)
	}
	if !strings.Contains(got, "+ │ return 2") {
		t.Fatalf("missing added line in %q", got)
	}
}

func TestFileMutationDiffPreviewNewFile(t *testing.T) {
	input := writeInputJSON(t, "new.go", "package p\n")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if !strings.Contains(got, "+ │ package p") {
		t.Fatalf("new-file diff missing + line: %q", got)
	}
	if strings.Contains(got, " - ") {
		t.Fatalf("new-file diff must have no removed lines: %q", got)
	}
}

func TestFileMutationDiffPreviewFullFileViaOldContent(t *testing.T) {
	input := writeInputJSON(t, "a.go", "a\nb\nx\n")
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "a\nb\nc\n")
	if !strings.Contains(got, "- │ c") || !strings.Contains(got, "+ │ x") {
		t.Fatalf("full-file diff wrong: %q", got)
	}
	// context lines preserved
	if !strings.Contains(got, "  │ a") && !strings.Contains(got, "│ a") {
		t.Fatalf("context line a missing: %q", got)
	}
}

func TestFileMutationDiffPreviewCollapsesUnchangedRuns(t *testing.T) {
	// 1 change in a long file: collapsed run marker must appear.
	old := strings.Repeat("line\n", 20)
	newS := strings.Replace(old, "line\n", "changed\n", 1)
	input := writeInputJSON(t, "a.go", newS)
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, old)
	if !strings.Contains(got, "···") {
		t.Fatalf("expected collapse marker in %q", got)
	}
}

func TestFileMutationDiffPreviewEmpty(t *testing.T) {
	input := []byte(`{"file_path":"a.go"}`)
	fields := toolInputFields(input)
	got := fileMutationDiffPreview(fields, "")
	if got != "" {
		t.Fatalf("expected empty diff, got %q", got)
	}
}
```

Add `"encoding/json"` to the test imports.

- [ ] **Step 2: Run tests to verify they pass BEFORE refactoring**

Run: `go test ./internal/ui/bubble -run TestFileMutationDiffPreview -count=1`
Expected: PASS. These are characterization tests — they must pass against the current implementation. If any fails, fix the test expectation to match current output (the goal is to pin behavior, not to prescribe it), then proceed.

- [ ] **Step 3: Implement `diff.go` (named types + extracted functions)**

Create `internal/ui/bubble/diff.go`. This re-implements the existing `lcsEditScript` numbering logic in terms of a named `DiffLine`:

```go
package bubble

import (
	"fmt"
	"strings"
)

// DiffLine is one line of a structured line diff.
// Kind is ' ' (unchanged), '+' (added), or '-' (removed).
// Number is the old-file line number; added lines carry the line number at
// the insertion position and do not advance the counter.
type DiffLine struct {
	Kind   rune
	Number int
	Text   string
}

type lcsOp struct {
	kind rune
	text string
}

// lcsOps computes a Myers/LCS shortest edit script between a and b. It returns
// ops in forward (file) order. This is the body of the former lcsEditScript,
// minus the unused oi/ni fields.
func lcsOps(a, b []string) []lcsOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	ops := []lcsOp{}
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append(ops, lcsOp{' ', a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, lcsOp{'+', b[j-1]})
			j--
		default:
			ops = append(ops, lcsOp{'-', a[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// structuredDiff computes the LCS edit script between oldLines and newLines and
// returns ordered DiffLines carrying old-file line numbers, matching Claude
// Code's numberDiffLines strategy: unchanged/removed advance the counter; an
// added line shows the current number without advancing; a removed block
// advances through the block then rewinds so subsequent lines keep their number.
func structuredDiff(oldLines, newLines []string) []DiffLine {
	ops := lcsOps(oldLines, newLines)
	numbered := make([]DiffLine, 0, len(ops))
	lineNum := 1
	for idx := 0; idx < len(ops); {
		op := ops[idx]
		switch op.kind {
		case ' ':
			numbered = append(numbered, DiffLine{' ', lineNum, op.text})
			lineNum++
			idx++
		case '+':
			numbered = append(numbered, DiffLine{'+', lineNum, op.text})
			idx++
		case '-':
			numRemoved := 0
			for idx < len(ops) && ops[idx].kind == '-' {
				numbered = append(numbered, DiffLine{'-', lineNum, ops[idx].text})
				lineNum++
				numRemoved++
				idx++
			}
			lineNum -= numRemoved
		}
	}
	return numbered
}

// diffCounts returns the number of added and removed lines in a structured diff.
func diffCounts(lines []DiffLine) (added, removed int) {
	for _, l := range lines {
		switch l.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// renderDiffPreview applies a 3-line context window around changed lines,
// collapses unchanged runs with "···", formats each line with a number column,
// and caps total output length via limitDiffPreviewLines.
func renderDiffPreview(lines []DiffLine) string {
	maxLine := 0
	for _, n := range lines {
		if n.Number > maxLine {
			maxLine = n.Number
		}
	}
	width := 1
	if maxLine > 0 {
		width = len(fmt.Sprintf("%d", maxLine))
	}

	const context = 3
	visible := make([]bool, len(lines))
	for i, n := range lines {
		if n.Kind != ' ' {
			for j := maxInt(0, i-context); j <= minInt(len(lines)-1, i+context); j++ {
				visible[j] = true
			}
		}
	}

	out := []string{}
	prevVisible := false
	for i, n := range lines {
		if !visible[i] {
			prevVisible = false
			continue
		}
		if !prevVisible && i > 0 {
			out = append(out, "···")
		}
		prevVisible = true
		switch n.Kind {
		case '-':
			out = append(out, fmt.Sprintf("%*d - │ %s", width, n.Number, n.Text))
		case '+':
			out = append(out, fmt.Sprintf("%*d + │ %s", width, n.Number, n.Text))
		default:
			out = append(out, fmt.Sprintf("%*d   │ %s", width, n.Number, n.Text))
		}
	}
	return strings.Join(limitDiffPreviewLines(out), "\n")
}

// fileMutationContents extracts the old/new content pair used to compute a
// file-mutation diff. Edit-style tools carry old_string/new_string in their
// input fields; Write carries content and receives oldContent from the runner.
func fileMutationContents(fields []toolDisplayField, oldContent string) (old, new string) {
	if fc := firstNonEmptyField(fields, "old_content", "old_string", "before"); fc != "" {
		oldContent = fc
	}
	newContent := firstNonEmptyField(fields, "new_content", "new_string", "replacement", "content", "after")
	return oldContent, newContent
}
```

- [ ] **Step 4: Write tests for the new `diff.go` functions**

Append to `internal/ui/bubble/diff_test.go`:

```go
func TestStructuredDiffNumbersRemoveBlockRewind(t *testing.T) {
	old := []string{"a", "b", "c", "d"}
	newS := []string{"a", "d"} // remove b,c
	lines := structuredDiff(old, newS)
	// Expect: ' ' a(1), '-' b(2), '-' c(3), ' ' d(4)
	if len(lines) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(lines), lines)
	}
	if lines[1].Kind != '-' || lines[1].Number != 2 || lines[1].Text != "b" {
		t.Fatalf("line[1] = %+v, want - b @2", lines[1])
	}
	if lines[2].Kind != '-' || lines[2].Number != 3 || lines[2].Text != "c" {
		t.Fatalf("line[2] = %+v, want - c @3", lines[2])
	}
	if lines[3].Kind != ' ' || lines[3].Number != 4 || lines[3].Text != "d" {
		t.Fatalf("line[3] = %+v, want ' ' d @4", lines[3])
	}
}

func TestStructuredDiffAddDoesNotAdvanceNumber(t *testing.T) {
	old := []string{"a", "c"}
	newS := []string{"a", "b", "c"} // insert b at line 2
	lines := structuredDiff(old, newS)
	// Expect: ' ' a(1), '+' b(2), ' ' c(2)
	if lines[1].Kind != '+' || lines[1].Number != 2 || lines[1].Text != "b" {
		t.Fatalf("line[1] = %+v, want + b @2", lines[1])
	}
	if lines[2].Kind != ' ' || lines[2].Number != 2 || lines[2].Text != "c" {
		t.Fatalf("line[2] = %+v, want ' ' c @2 (not advanced)", lines[2])
	}
}

func TestDiffCounts(t *testing.T) {
	lines := structuredDiff([]string{"a", "b", "c"}, []string{"a", "x", "c"})
	added, removed := diffCounts(lines)
	if added != 1 || removed != 1 {
		t.Fatalf("counts = +%d -%d, want +1 -1", added, removed)
	}
}
```

- [ ] **Step 5: Run new-function tests (they pass immediately)**

Run: `go test ./internal/ui/bubble -run 'TestStructuredDiff|TestDiffCounts' -count=1`
Expected: PASS.

- [ ] **Step 6: Refactor `fileMutationDiffPreview` and remove old `lcsEditScript`**

In `internal/ui/bubble/utils.go`, replace the entire body of `fileMutationDiffPreview` (lines 108-211) with:

```go
func fileMutationDiffPreview(fields []toolDisplayField, oldContent string) string {
	oldContent, newContent := fileMutationContents(fields, oldContent)

	if oldContent == "" && newContent == "" {
		return ""
	}
	// 新建文件（无旧内容）：只显示 + 行
	if oldContent == "" {
		return strings.Join(limitDiffPreviewLines(numberedLines("+", newContent)), "\n")
	}
	// 删除文件或清空（无新内容）：只显示 - 行
	if newContent == "" {
		return strings.Join(limitDiffPreviewLines(numberedLines("-", oldContent)), "\n")
	}

	lines := structuredDiff(splitLines(oldContent), splitLines(newContent))
	return renderDiffPreview(lines)
}
```

Then delete the old `lcsEditScript` function (lines 213-265) entirely — its body now lives in `diff.go` as `lcsOps` + `structuredDiff`. Keep `splitLines`, `numberedLines`, `formatNumberedDiffLine`, `limitDiffPreviewLines`, `firstNonEmptyField` where they are.

- [ ] **Step 7: Run ALL characterization tests — they must still pass**

Run: `go test ./internal/ui/bubble -run 'TestFileMutationDiffPreview|TestStructuredDiff|TestDiffCounts' -count=1`
Expected: PASS (identical to pre-refactor). This proves the refactor is behavior-preserving.

- [ ] **Step 8: Run full bubble + repo tests + vet + format**

Run: `gofmt -w internal/ui/bubble/diff.go internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go && go test ./... -count=1 && go vet ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/ui/bubble/diff.go internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go
git commit -m "refactor: extract structured DiffLine diff with counts (behavior-preserving)"
```

---
### Task 4: Folded `+N -M` summary on the tool track line

**Files:**
- Modify: `internal/ui/bubble/utils.go:96-106` (`formatFileMutationToolCallBody`)
- Test: `internal/ui/bubble/diff_test.go` (append)

**Interfaces:**
- Consumes: `structuredDiff`, `diffCounts`, `fileMutationContents` (Task 3); `splitLines`, `firstNonEmptyField`, `toolInputFields` (utils.go); `setToolCallBodyStatus` (utils.go:315) which inserts the status token as the second `·`-part.
- Produces: a tool-call body whose first line is `<Name> · +<added> -<removed>` when changes exist (e.g. `Edit · +1 -1`), so `setToolCallBodyStatus` yields `Edit · running · +1 -1` (running) and `Edit · ok · +1 -1` (done). When there are no changes or no diff content, the first line stays `<Name>` (no `+0 -0`).

- [ ] **Step 1: Write the failing tests for the folded summary**

Append to `internal/ui/bubble/diff_test.go`:

```go
func TestFormatFileMutationToolCallBodyEditSummary(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatFileMutationToolCallBody("Edit", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Edit · +1 -1") {
		t.Fatalf("first line = %q, want summary Edit · +1 -1", first)
	}
	if !strings.Contains(body, "a.go") {
		t.Fatalf("missing target in body: %q", body)
	}
	if !strings.Contains(body, "+ │ return 2") || !strings.Contains(body, "- │ return 1") {
		t.Fatalf("missing diff lines: %q", body)
	}
}

func TestFormatFileMutationToolCallBodyWriteNewFileSummary(t *testing.T) {
	input := writeInputJSON(t, "new.go", "package p\n")
	body := formatFileMutationToolCallBody("Write", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Write · +1 -0") {
		t.Fatalf("first line = %q, want Write · +1 -0", first)
	}
}

func TestFormatFileMutationToolCallBodyNoDiffNoSummary(t *testing.T) {
	// Neither old nor new content: no summary token, just the name.
	input := []byte(`{"file_path":"a.go"}`)
	body := formatFileMutationToolCallBody("Write", toolInputFields(input), "")
	first := firstToolEntryLine(body)
	if first != "Write" {
		t.Fatalf("first line = %q, want Write (no counts)", first)
	}
}

func TestFormatRunningToolCallBodyInsertsStatusBeforeCounts(t *testing.T) {
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatRunningToolCallBody("Edit", input, "")
	first := firstToolEntryLine(body)
	// status inserted as 2nd part: Edit · running · +1 -1
	if !strings.Contains(first, "Edit · running · +1 -1") {
		t.Fatalf("first line = %q, want Edit · running · +1 -1", first)
	}
	// Completing the tool replaces running with ok; counts survive.
	completed := completeRunningToolCallBody(body, "ok")
	firstCompleted := firstToolEntryLine(completed)
	if !strings.Contains(firstCompleted, "Edit · ok · +1 -1") {
		t.Fatalf("completed first line = %q, want Edit · ok · +1 -1", firstCompleted)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/bubble -run 'TestFormatFileMutationToolCallBody|TestFormatRunningToolCallBodyInsertsStatusBeforeCounts' -count=1`
Expected: FAIL — current `formatFileMutationToolCallBody` produces first line `Edit` (no counts), so `Edit · +1 -1` is absent.

- [ ] **Step 3: Implement the summary token in `formatFileMutationToolCallBody`**

In `internal/ui/bubble/utils.go`, replace the body of `formatFileMutationToolCallBody` (lines 96-106) with:

```go
func formatFileMutationToolCallBody(name string, fields []toolDisplayField, oldContent string) string {
	summary := name
	if totals, ok := fileMutationChangeCounts(fields, oldContent); ok {
		summary = fmt.Sprintf("%s · +%d -%d", name, totals.added, totals.removed)
	}
	lines := []string{summary}
	if target := firstNonEmptyField(fields, "file_path", "path"); target != "" {
		lines = append(lines, target)
	}
	if diff := fileMutationDiffPreview(fields, oldContent); diff != "" {
		lines = append(lines, diff)
	}
	return strings.Join(lines, "\n")
}

type diffTotals struct{ added, removed int }

// fileMutationChangeCounts reports added/removed line counts for the
// file-mutation diff. Returns false when there is no old or new content
// (no diff to summarize).
func fileMutationChangeCounts(fields []toolDisplayField, oldContent string) (diffTotals, bool) {
	old, newContent := fileMutationContents(fields, oldContent)
	if old == "" && newContent == "" {
		return diffTotals{}, false
	}
	if old == "" {
		return diffTotals{added: len(splitLines(newContent))}, true
	}
	if newContent == "" {
		return diffTotals{removed: len(splitLines(old))}, true
	}
	added, removed := diffCounts(structuredDiff(splitLines(old), splitLines(newContent)))
	return diffTotals{added: added, removed: removed}, true
}
```

Ensure `"fmt"` is imported in `utils.go` (it already is — `fmt.Sprintf` is used elsewhere in the file).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/bubble -run 'TestFormatFileMutationToolCallBody|TestFormatRunningToolCallBodyInsertsStatusBeforeCounts|TestFileMutationDiffPreview' -count=1`
Expected: PASS (new summary tests AND the Task 3 characterization tests still pass).

- [ ] **Step 5: Run full repo tests + vet + format**

Run: `gofmt -w internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go && go test ./... -count=1 && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/bubble/utils.go internal/ui/bubble/diff_test.go
git commit -m "feat: show +N -M change summary on Edit/Write tool track line"
```

---

### Task 5: End-to-end Edit/Write diff rendering + full verification

**Files:**
- Test: `internal/ui/bubble/diff_test.go` (append)
- No production changes — this task verifies the full path from `formatRunningToolCallBody` (used by `recordToolCallEntry`) through the diff renderer, and runs the whole repo.

**Interfaces:**
- Consumes: `formatRunningToolCallBody(name, input, oldContent) string` (`utils.go:307`, called by `recordToolCallEntry` at `transcript.go:271`), `renderToolDetailLines(lines []string, width int) string` (`transcript.go:1277`), `firstToolEntryLine` (`utils.go:346`).
- Produces: tests proving the Edit region diff and the Write full-file diff both reach the rendered body with the `+N -M` summary, and a smoke test that `renderToolDetailLines` colors `+`/`-` lines without panic.

- [ ] **Step 1: Write the end-to-end Edit rendering test**

Append to `internal/ui/bubble/diff_test.go`:

```go
func TestEditRunningBodyRendersRegionDiff(t *testing.T) {
	old := "func run() int {\n\treturn 1\n}\n"
	input := editInputJSON(t, "internal/foo.go", "\treturn 1", "\treturn 2")
	body := formatRunningToolCallBody("Edit", input, old)

	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Edit · running · +1 -1") {
		t.Fatalf("summary line = %q", first)
	}
	if !strings.Contains(body, "internal/foo.go") {
		t.Fatalf("missing target: %q", body)
	}
	if !strings.Contains(body, "- │ \treturn 1") || !strings.Contains(body, "+ │ \treturn 2") {
		t.Fatalf("missing region diff lines: %q", body)
	}
}

func TestWriteRunningBodyRendersFullFileDiff(t *testing.T) {
	old := "package p\n\nfunc a() int { return 1 }\n"
	newContent := "package p\n\nfunc a() int { return 2 }\n"
	input := writeInputJSON(t, "internal/foo.go", newContent)
	body := formatRunningToolCallBody("Write", input, old)

	first := firstToolEntryLine(body)
	if !strings.Contains(first, "Write · running · +1 -1") {
		t.Fatalf("summary line = %q", first)
	}
	// Full-file diff: unchanged context lines + the changed line.
	if !strings.Contains(body, "- │ func a() int { return 1 }") {
		t.Fatalf("missing removed line: %q", body)
	}
	if !strings.Contains(body, "+ │ func a() int { return 2 }") {
		t.Fatalf("missing added line: %q", body)
	}
}

func TestRenderToolDetailLinesHandlesNumberedDiff(t *testing.T) {
	old := "a\nreturn 1\nb\n"
	input := editInputJSON(t, "a.go", "return 1", "return 2")
	body := formatRunningToolCallBody("Edit", input, old)
	// Drop the first two lines (summary + target) to isolate diff lines.
	parts := strings.SplitN(body, "\n", 3)
	diffLines := []string{}
	if len(parts) == 3 {
		diffLines = strings.Split(parts[2], "\n")
	}
	rendered := renderToolDetailLines(diffLines, 80)
	// Text content must survive styling (lipgloss escape sequences wrap it).
	if !strings.Contains(rendered, "return 1") || !strings.Contains(rendered, "return 2") {
		t.Fatalf("rendered diff lost text: %q", rendered)
	}
}
```

- [ ] **Step 2: Run the rendering tests**

Run: `go test ./internal/ui/bubble -run 'TestEditRunningBodyRendersRegionDiff|TestWriteRunningBodyRendersFullFileDiff|TestRenderToolDetailLinesHandlesNumberedDiff' -count=1`
Expected: PASS.

- [ ] **Step 3: Run the entire test suite + vet**

Run: `go test ./... -count=1 && go vet ./...`
Expected: PASS — every package, including `internal/tool/file`, `cmd/agent`, `internal/subagent`, `internal/ui/bubble`, `internal/loop`.

- [ ] **Step 4: Verify `git diff --check` is clean**

Run: `git diff --check`
Expected: no whitespace errors.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/bubble/diff_test.go
git commit -m "test: cover end-to-end Edit/Write diff rendering with +N -M summary"
```

---

## Self-Review

**1. Spec coverage.** The conversation's scope was "原生 Edit、读后写冲突保护、结构化 diff、Write diff、Bubble Tea 展示" (native Edit, stale-write protection, structured diff, Write diff, Bubble Tea display), excluding LSP/VSCode notifications and a global Git diff panel.

- Native `Edit` → Task 1. ✓
- 读后写冲突保护 (stale-write) → Task 2. ✓
- 结构化 diff (DiffLine + counts) → Task 3. ✓
- Write diff (full-file via OldContent) → Task 3 characterizes it + Task 5 verifies it end-to-end. ✓
- Bubble Tea 展示 (`+N -M` folded summary + colored hunks) → Task 4 (summary) + Task 5 (renderer). ✓
- Excluded: StructuredTool/ToolOutput interface (Non-goals), Git diff panel (Non-goals), LSP (Non-goals). ✓

**2. Placeholder scan.** No `TBD`/`TODO`/`implement later`/`appropriate error handling`/`Similar to` in any step. Every code step contains the actual Go source. Registration tests cover the one place a nil-`subagentManager` assumption is made (Task 1 Step 12 notes the fallback).

**3. Type consistency.** `DiffLine{Kind rune; Number int; Text string}` is used identically in `structuredDiff` (Task 3), `diffCounts`, `renderDiffPreview`, and the tests. `fileMutationContents` returns `(old, new string)` and is consumed by both `fileMutationDiffPreview` and `fileMutationChangeCounts`. `ReadStateStore.Record/Verify/RecordAfterWrite` signatures match their call sites in `ReadTool` (`read.go`), `WriteTool` (`write.go`), and `EditTool` (`edit.go`). `atomicWriteFile(target string, content []byte) error` matches call sites in `EditTool` and `WriteTool`. Field name `ReadState *ReadStateStore` is consistent across the three tools and both registries.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-30-claude-code-edit-alignment.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
