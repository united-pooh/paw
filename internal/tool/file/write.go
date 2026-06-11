package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct {
	Root string
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Name() string {
	return "Write"
}

func (t *WriteTool) Description() string {
	return "写入工作区内文件，并用新内容覆盖整个文件"
}

func (t *WriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`)
}

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

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), relativePath(t.Root, target)), nil
}
