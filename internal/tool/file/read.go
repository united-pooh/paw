package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type ReadTool struct {
	mu               sync.RWMutex
	Root             string
	ReadRoots        []string
	ReadState        *ReadStateStore
	AllowOutsideRoot bool
}

type readInput struct {
	FilePath string `json:"file_path"`
}

func (t *ReadTool) Name() string {
	return "Read"
}

func (t *ReadTool) Description() string {
	if t.OutsideRootAllowed() {
		return "读取单个文件的完整内容；dangerously/yolo 模式下允许工作区外路径。Edit/Write 覆盖文件前必须先读取"
	}
	return "读取工作区内单个文件的完整内容；Edit/Write 覆盖已存在文件前必须先读取"
}

func (t *ReadTool) SetAllowOutsideRoot(enabled bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.AllowOutsideRoot = enabled
	t.mu.Unlock()
}

func (t *ReadTool) OutsideRootAllowed() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.AllowOutsideRoot
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

	var target string
	var err error
	if t.OutsideRootAllowed() {
		target = strings.TrimSpace(in.FilePath)
		if !filepath.IsAbs(target) {
			target = filepath.Join(t.Root, target)
		}
		target, err = filepath.Abs(target)
		if err == nil {
			target = filepath.Clean(target)
		}
	} else {
		target, err = resolvePathWithinRoots(t.Root, in.FilePath, t.ReadRoots)
	}
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
