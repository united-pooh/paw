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

	replacements := count
	plural := "s"
	if replacements == 1 {
		plural = ""
	}
	return fmt.Sprintf("edited %s (%d replacement%s)", relativePath(t.Root, target), replacements, plural), nil
}
