package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	coretool "paw/internal/tool"
	"strings"
)

type WriteTool struct {
	Root      string
	ReadState *ReadStateStore
	// ForbidDotPaw 在 worker 沙箱中置位：拒绝写入 root/.paw（仅内部会话存储写入）。
	ForbidDotPaw bool
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Name() string {
	return "Write"
}

func (t *WriteTool) Description() string {
	return "写入工作区内文件，并用新内容覆盖整个文件；覆盖已存在文件前必须先用 Read 读取（新建文件无需 Read）"
}

func (t *WriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"content":{"type":"string"}},"required":["file_path","content"]}`)
}

func (t *WriteTool) FileMutationTarget(raw json.RawMessage) (coretool.FileMutationTarget, error) {
	var in writeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return coretool.FileMutationTarget{}, err
	}
	if strings.TrimSpace(in.FilePath) == "" {
		return coretool.FileMutationTarget{}, fmt.Errorf("file_path is required")
	}
	target, exists, err := resolveMutationPath(t.Root, in.FilePath, true)
	if err != nil {
		return coretool.FileMutationTarget{}, err
	}
	return coretool.FileMutationTarget{Path: target, BeforeExists: exists}, nil
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

	target, _, err := resolveMutationPath(t.Root, in.FilePath, true)
	if err != nil {
		return "", err
	}
	if t.ForbidDotPaw {
		if err := forbidDotPaw(t.Root, target); err != nil {
			return "", err
		}
	}

	// Read-before-write guard: overwriting an existing file requires a prior
	// Read baseline (same contract as Edit), so the model cannot bypass the
	// read-first rule by rewriting files with Write. New files need no Read.
	display := relativePath(t.Root, target)
	if existing, rerr := os.ReadFile(target); rerr == nil {
		if t.ReadState != nil {
			if verr := t.ReadState.VerifyRequired(target, existing); verr != nil {
				if strings.Contains(verr.Error(), "file must be read before editing") {
					return "", fmt.Errorf("file must be read before writing: %s; use Read first (Read the file, then retry this Write; do not bypass with Bash)", display)
				}
				return "", verr
			}
		}
	} else if !os.IsNotExist(rerr) {
		return "", rerr
	}

	// 跨进程 coordination：同一工作区的并发 worker 写同一文件时，用 OS advisory
	// flock 串行化写入，避免 Write/Edit/后台任务互相覆盖（lost-update）。
	if err := withMutationLock(t.Root, func() error {
		return atomicWriteFile(target, []byte(in.Content), 0o644)
	}); err != nil {
		return "", err
	}
	if t.ReadState != nil {
		t.ReadState.RecordAfterWrite(target, []byte(in.Content))
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), relativePath(t.Root, target)), nil
}

var _ coretool.FileMutationTool = (*WriteTool)(nil)
