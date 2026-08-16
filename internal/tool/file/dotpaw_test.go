package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForbidDotPawBlocksWorkerWriteUnderPaw 验证沙箱 worker 的 Write/Edit 拒绝
// 写入 root/.paw，同时放行常规工作区文件与未置位 ForbidDotPaw 的普通工具。
func TestForbidDotPawBlocksWorkerWriteUnderPaw(t *testing.T) {
	root := t.TempDir()
	pawDir := filepath.Join(root, ".paw")
	if err := os.MkdirAll(pawDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.paw): %v", err)
	}
	ctx := context.Background()

	// Write：沙箱 worker 拦截 .paw。
	sandboxedWrite := &WriteTool{Root: root, ForbidDotPaw: true}
	if _, err := sandboxedWrite.Run(ctx, []byte(`{"file_path":".paw/secret.toml","content":"x"}`)); err == nil || !strings.Contains(err.Error(), ".paw") {
		t.Fatalf("sandboxed Write(.paw) error = %v, want .paw block", err)
	}
	// Edit：沙箱 worker 拦截 .paw（在 read-state 校验之前先命中 ForbidDotPaw）。
	f := filepath.Join(pawDir, "f.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sandboxedEdit := &EditTool{Root: root, ForbidDotPaw: true}
	if _, err := sandboxedEdit.Run(ctx, []byte(`{"file_path":".paw/f.txt","old_string":"hello","new_string":"bye"}`)); err == nil || !strings.Contains(err.Error(), ".paw") {
		t.Fatalf("sandboxed Edit(.paw) error = %v, want .paw block", err)
	}

	// 常规工作区文件在沙箱 worker 下仍可正常写入。
	if _, err := sandboxedWrite.Run(ctx, []byte(`{"file_path":"a.txt","content":"ok"}`)); err != nil {
		t.Fatalf("sandboxed Write(a.txt) error = %v, want allowed", err)
	}

	// 未置位 ForbidDotPaw 的普通工具（主 agent）允许写 .paw（由内部逻辑自行管理）。
	plain := &WriteTool{Root: root}
	if _, err := plain.Run(ctx, []byte(`{"file_path":".paw/secret.toml","content":"x"}`)); err != nil {
		t.Fatalf("plain Write(.paw) error = %v, want allowed", err)
	}
}

// TestIsInsideDotPawRoot 校验边界判定：.paw 目录内为 true，工作区其余为 false。
func TestIsInsideDotPawRoot(t *testing.T) {
	root := "/wsp"
	cases := []struct {
		target string
		want   bool
	}{
		{"/wsp/.paw/x", true},
		{"/wsp/.paw", true},
		{"/wsp/.paw/sub/y", true},
		{"/wsp/a.txt", false},
		{"/wsp/paw/x", false}, // 非 .paw 前缀
		{"/wsp/.pawx", false}, // 非目录边界
		{"/other/.paw/x", false},
	}
	for _, c := range cases {
		got, err := isInsideDotPawRoot(root, c.target)
		if err != nil {
			t.Fatalf("isInsideDotPawRoot(%q): %v", c.target, err)
		}
		if got != c.want {
			t.Errorf("isInsideDotPawRoot(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}