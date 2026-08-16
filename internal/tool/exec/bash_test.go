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

	if got, want := canonicalPathForTest(t, strings.TrimSpace(output)), canonicalPathForTest(t, root); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	output, err = tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"subdir"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := canonicalPathForTest(t, strings.TrimSpace(output)), canonicalPathForTest(t, subdir); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestBashToolRejectsWorkingDirOutsideRoot(t *testing.T) {
	tool := &BashTool{Root: t.TempDir()}

	_, err := tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"../outside"}`))
	if err == nil || !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("Run() error = %v, want workspace escape error", err)
	}
}

func TestBashToolAcceptsAbsoluteWorkingDirInsideRoot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	tool := &BashTool{Root: root}
	output, err := tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"`+subdir+`"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := canonicalPathForTest(t, strings.TrimSpace(output)), canonicalPathForTest(t, subdir); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func canonicalPathForTest(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return canonical
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

// TestBashToolSandboxedBlocksWorkerHostileCommands 验证 worker 沙箱模式追加拦截
// 提权/权限/块设备写命令，同时主 agent（非 Sandboxed）不受这些追加规则影响。
func TestBashToolSandboxedBlocksWorkerHostileCommands(t *testing.T) {
	sandboxed := &BashTool{Sandboxed: true}
	nonSandboxed := &BashTool{}

	hostile := []string{
		"sudo apt-get update",
		"sudo pkill -9 dockerd",
		"su root -c whoami",
		"chmod 777 script.sh",
		"chown admin:admin data.txt",
		"echo hi > /dev/sda",
		"dd if=x of=/dev/sdb",
	}
	for _, cmd := range hostile {
		if hit, _ := sandboxed.checkCommandSafety(cmd); !hit {
			t.Errorf("sandboxed checkCommandSafety(%q) = allowed, want blocked", cmd)
		}
		// 主 agent 只在全局模式 命中时拦截；sudo/chmod 等不由沙箱规则触发。
		globalHit, _ := checkCommandSafety(cmd)
		if nonSandboxedHit, _ := nonSandboxed.checkCommandSafety(cmd); nonSandboxedHit != globalHit {
			t.Errorf("non-sandboxed checkCommandSafety(%q) inconsistency", cmd)
		}
	}

	// worker 沙箱仍放行常规开发命令与 /dev/null 重定向。
	allowed := []string{
		"go test ./...",
		"ls -la",
		"echo hello > /dev/null",
		"echo note > notes.txt",
		"cat /dev/null",
	}
	for _, cmd := range allowed {
		if hit, _ := sandboxed.checkCommandSafety(cmd); hit {
			t.Errorf("sandboxed checkCommandSafety(%q) = blocked, want allowed", cmd)
		}
	}

	// 全局高危模式对两种模式都生效。
	for _, tool := range []*BashTool{sandboxed, nonSandboxed} {
		if hit, _ := tool.checkCommandSafety("rm -rf /"); !hit {
			t.Errorf("checkCommandSafety(rm -rf /) should always be blocked")
		}
	}
}
