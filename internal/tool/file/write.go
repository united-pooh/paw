package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type WriteTool struct {
	Root      string
	ReadState *ReadStateStore
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
