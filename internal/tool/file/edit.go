package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	coretool "paw/internal/tool"
	"strings"
)

// EditTool performs an exact string replacement on an existing file in the
// workspace. It mirrors Claude Code's Edit contract: the file must have been
// Read first, old_string must match exactly and be unique unless replace_all is
// true, and the file is overwritten atomically.
type EditTool struct {
	Root      string
	ReadState *ReadStateStore
	// ForbidDotPaw 在 worker 沙箱中置位：拒绝编辑 root/.paw 下的文件。
	ForbidDotPaw bool
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditTool) Name() string { return "Edit" }

func (t *EditTool) Description() string {
	return "对工作区内已先用 Read 读取的文件做精确字符串替换：编辑前必须先调用 Read 读取该文件，否则会被拒绝。" +
		"old_string 必须与文件内容逐字节匹配。" +
		"默认 old_string 必须唯一；设 replace_all=true 可替换全部匹配。" +
		`示例输入: {"file_path":"internal/foo.go","old_string":"return 1","new_string":"return 2"}`
}

func (t *EditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["file_path","old_string","new_string"]}`)
}

func (t *EditTool) FileMutationTarget(raw json.RawMessage) (coretool.FileMutationTarget, error) {
	var in editInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return coretool.FileMutationTarget{}, err
	}
	if strings.TrimSpace(in.FilePath) == "" {
		return coretool.FileMutationTarget{}, fmt.Errorf("file_path is required")
	}
	target, exists, err := resolveMutationPath(t.Root, in.FilePath, false)
	if err != nil {
		return coretool.FileMutationTarget{}, err
	}
	return coretool.FileMutationTarget{Path: target, BeforeExists: exists}, nil
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

	target, _, err := resolveMutationPath(t.Root, in.FilePath, false)
	if err != nil {
		return "", err
	}
	if t.ForbidDotPaw {
		if err := forbidDotPaw(t.Root, target); err != nil {
			return "", err
		}
	}

	display := relativePath(t.Root, target)
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat edit target %s: %w", display, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("edit target is not a regular file: %s", display)
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read edit target %s: %w", display, err)
	}
	if t.ReadState == nil {
		return "", readFirstEditError(display)
	}
	if err := t.ReadState.VerifyRequired(target, current); err != nil {
		if strings.Contains(err.Error(), "file must be read before editing") {
			return "", readFirstEditError(display)
		}
		return "", fmt.Errorf("file has been modified since last read: %s; read it again before editing", display)
	}
	count := strings.Count(string(current), in.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s; it must match the file contents exactly", display)
	}
	if count > 1 && !in.ReplaceAll {
		return "", fmt.Errorf("old_string matches %d locations in %s; set replace_all=true or include more surrounding context to make it unique", count, display)
	}

	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(string(current), in.OldString, in.NewString)
	} else {
		updated = strings.Replace(string(current), in.OldString, in.NewString, 1)
	}

	// 跨进程 coordination：多 worker 并发编辑同一文件时用 OS advisory flock 串行化。
	if err := withMutationLock(t.Root, func() error {
		return atomicWriteFile(target, []byte(updated), info.Mode().Perm())
	}); err != nil {
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

var _ coretool.FileMutationTool = (*EditTool)(nil)

// readFirstEditError 返回引导模型正确操作的拦截消息：先 Read 再重试 Edit，
// 并明确禁止用 Write 或 Bash 重写文件绕过 read-before-edit 契约。
func readFirstEditError(display string) error {
	return fmt.Errorf(
		"file must be read before editing: %s; use Read first (call the Read tool on this path, then retry this Edit; do not bypass by rewriting the file with Write or Bash)",
		display,
	)
}
