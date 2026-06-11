package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashToolRunsCommandInWorkspace(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	tool := &BashTool{Root: root}
	output, err := tool.Run(context.Background(), []byte(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := strings.TrimSpace(output); got != root {
		t.Fatalf("output = %q, want %q", got, root)
	}

	output, err = tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"subdir"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := strings.TrimSpace(output); got != subdir {
		t.Fatalf("output = %q, want %q", got, subdir)
	}
}

func TestBashToolRejectsWorkingDirOutsideRoot(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	_, err := tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"../outside"}`))
	if err == nil || !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("Run() error = %v, want workspace escape error", err)
	}
}

func TestBashToolTimesOut(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	_, err := tool.Run(context.Background(), []byte(`{"command":"sleep 2","timeout_seconds":1}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want timeout error", err)
	}
}

func TestBashToolBlocksRmRf(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	cases := []string{
		`{"command":"rm -rf /tmp/test"}`,
		`{"command":"rm -rf ."}`,
		`{"command":"rm -r /some/dir"}`,
		`{"command":"rm -fr /tmp/foo"}`,
	}
	for _, input := range cases {
		_, err := tool.Run(context.Background(), []byte(input))
		if err == nil || !strings.Contains(err.Error(), "拦截") {
			t.Fatalf("Run(%s) should be blocked, got err=%v", input, err)
		}
	}
}

func TestBashToolBlocksDiskDestructiveOps(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	cases := []string{
		`{"command":"mkfs.ext4 /dev/sda"}`,
		`{"command":"dd if=/dev/zero of=/dev/sda bs=4M"}`,
		`{"command":"shred -u /tmp/file"}`,
	}
	for _, input := range cases {
		_, err := tool.Run(context.Background(), []byte(input))
		if err == nil || !strings.Contains(err.Error(), "拦截") {
			t.Fatalf("Run(%s) should be blocked, got err=%v", input, err)
		}
	}
}

func TestBashToolAllowsSafeCommands(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	cases := []string{
		`{"command":"echo hello"}`,
		`{"command":"ls -la"}`,
		`{"command":"go version"}`,
		`{"command":"cat /dev/null"}`,
	}
	for _, input := range cases {
		_, err := tool.Run(context.Background(), []byte(input))
		// 只要没有被安全拦截即可（命令本身可能不存在，那是执行失败不是拦截）
		if err != nil && strings.Contains(err.Error(), "拦截") {
			t.Fatalf("Run(%s) should not be blocked, got err=%v", input, err)
		}
	}
}

func TestCheckCommandSafetyAllowsNormalCommands(t *testing.T) {
	safe := []string{
		"go test ./...",
		"ls -la",
		"echo hello",
		"git status",
		"rm specific_file.txt", // 删单文件允许
	}
	for _, cmd := range safe {
		blocked, _ := checkCommandSafety(cmd)
		if blocked {
			t.Errorf("checkCommandSafety(%q) = blocked, want allowed", cmd)
		}
	}
}

func TestCheckCommandSafetyBlocksDangerous(t *testing.T) {
	dangerous := []string{
		"rm -rf /",
		"rm -rf .",
		"rm -r /tmp",
		"rm -fr /home/user",
		"mkfs.ext4 /dev/sda",
		"dd if=/dev/zero of=/dev/sda",
		"shred -u /tmp/secret",
	}
	for _, cmd := range dangerous {
		blocked, _ := checkCommandSafety(cmd)
		if !blocked {
			t.Errorf("checkCommandSafety(%q) = allowed, want blocked", cmd)
		}
	}
}
