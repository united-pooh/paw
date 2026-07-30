package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type ReadTool struct {
	Root      string
	ReadRoots []string
	ReadState *ReadStateStore
}

type readInput struct {
	FilePath string `json:"file_path"`
}

func (t *ReadTool) Name() string {
	return "Read"
}

func (t *ReadTool) Description() string {
	return "读取工作区内单个文件的完整内容"
}

func (t *ReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`)
}

func (t *ReadTool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *ReadTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	target, err := resolvePathWithinRoots(t.Root, in.FilePath, t.ReadRoots)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if t.ReadState != nil {
		t.ReadState.Record(target, data)
	}
	return string(data), nil
}
