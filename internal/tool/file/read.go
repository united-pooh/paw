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
	Offset   int    `json:"offset"`
	Limit    *int   `json:"limit"`
}

func (t *ReadTool) Name() string {
	return "Read"
}

func (t *ReadTool) Description() string {
	description := "按行读取单个文件；offset 从 0 开始，limit 为返回行数，默认返回前 1000 行，单次结果最多 128 KiB。"
	if t.OutsideRootAllowed() {
		return description + "dangerously/yolo 模式下允许工作区外路径。Edit/Write 覆盖文件前必须先读取"
	}
	return description + "Edit/Write 覆盖已存在文件前必须先读取"
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
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"offset":{"type":"integer","minimum":0,"default":0},"limit":{"type":"integer","minimum":1,"default":1000}},"required":["file_path"]}`)
}

func (t *ReadTool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *ReadTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	in, limit, err := decodeReadInput(input)
	if err != nil {
		return "", err
	}

	target, err := t.resolveReadPath(in.FilePath)
	if err != nil {
		return "", err
	}
	return t.readTarget(ctx, in, limit, target)
}

func decodeReadInput(input json.RawMessage) (readInput, int, error) {
	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return readInput{}, 0, err
	}
	if strings.TrimSpace(in.FilePath) == "" {
		return readInput{}, 0, fmt.Errorf("file_path is required")
	}
	if in.Offset < 0 {
		return readInput{}, 0, fmt.Errorf("offset must be >= 0")
	}
	limit := defaultReadLimit
	if in.Limit != nil {
		if *in.Limit < 1 {
			return readInput{}, 0, fmt.Errorf("limit must be >= 1")
		}
		limit = *in.Limit
	}
	return in, limit, nil
}

func (t *ReadTool) readTarget(ctx context.Context, in readInput, limit int, target string) (string, error) {
	f, err := os.Open(target)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	page, err := readFilePage(ctx, f, in.Offset, limit)
	if err != nil {
		return "", err
	}
	if in.Offset > page.LineCount {
		return "", fmt.Errorf("offset %d beyond EOF (line count %d)", in.Offset, page.LineCount)
	}
	if t.ReadState != nil {
		t.ReadState.RecordRead(target, page.Hash, page.Offset, page.Limit, page.Content)
	}
	return formatReadPage(page), nil
}

func (t *ReadTool) PermissionReadTarget(input json.RawMessage) (string, bool, error) {
	in, _, err := decodeReadInput(input)
	if err != nil {
		return "", false, err
	}
	target := strings.TrimSpace(in.FilePath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(t.Root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", false, err
	}
	target, err = filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", false, err
	}
	allowed := append([]string{t.Root}, t.ReadRoots...)
	for _, root := range allowed {
		canonicalRoot, rootErr := filepath.Abs(root)
		if rootErr != nil {
			continue
		}
		if evaluated, evalErr := filepath.EvalSymlinks(filepath.Clean(canonicalRoot)); evalErr == nil {
			canonicalRoot = evaluated
		}
		if isWithinRoot(filepath.Clean(canonicalRoot), target) {
			return target, false, nil
		}
	}
	return target, true, nil
}

func (t *ReadTool) RunApprovedRead(ctx context.Context, input json.RawMessage, canonicalPath string) (string, error) {
	in, limit, err := decodeReadInput(input)
	if err != nil {
		return "", err
	}
	target, outside, err := t.PermissionReadTarget(input)
	if err != nil {
		return "", err
	}
	if !outside || target != filepath.Clean(canonicalPath) {
		return "", fmt.Errorf("Read allow-once scope mismatch")
	}
	return t.readTarget(ctx, in, limit, target)
}

func (t *ReadTool) resolveReadPath(filePath string) (string, error) {
	var target string
	var err error
	if t.OutsideRootAllowed() {
		target = strings.TrimSpace(filePath)
		if !filepath.IsAbs(target) {
			target = filepath.Join(t.Root, target)
		}
		target, err = filepath.Abs(target)
		if err == nil {
			target = filepath.Clean(target)
		}
	} else {
		target, err = resolvePathWithinRoots(t.Root, filePath, t.ReadRoots)
	}
	if err != nil {
		return "", err
	}
	return target, nil
}
