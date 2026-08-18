package actor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestSigkillCrashMatrix 是真实 SIGKILL 崩溃矩阵（spec ADR-8）：
// 协议 ①-④ 每个位点 × 递进崩溃点，反复杀死子进程直至场景完成，
// 最后 verify 断言恢复不变量（seq 连续 / exactly-once / 账本闭合）。
func TestSigkillCrashMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess matrix skipped in -short")
	}
	bin := filepath.Join(t.TempDir(), "crashprobe")
	out, err := exec.Command("go", "build", "-o", bin, "paw/internal/actor/testdata/crashprobe").CombinedOutput()
	if err != nil {
		t.Skipf("cannot build probe: %v\n%s", err, out)
	}

	stages := []string{
		StageInboxReceived,
		StageDomainFlushed,
		StageOutboxSent,
		StageInboxDone,
		StageSnapshotted,
		StageTimerRegistered,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			// 递进崩溃：每轮崩溃点后移一位，直至本轮无崩溃点可命中。
			for hit := 1; hit <= 24; hit++ {
				cmd := exec.Command(bin)
				cmd.Env = append(os.Environ(),
					"PROBE_DIR="+dir,
					fmt.Sprintf("PROBE_STAGE=%s", stage),
					fmt.Sprintf("PROBE_HIT=%d", hit),
				)
				output, err := cmd.CombinedOutput()
				if err == nil {
					if strings.Contains(string(output), "COMPLETE") {
						break
					}
					t.Fatalf("unexpected clean exit without COMPLETE: %s", output)
				}
				// 期望死亡方式：SIGKILL（真正的进程级崩溃）。
				if exitErr, ok := err.(*exec.ExitError); ok {
					if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
						continue // 按计划阵亡，下一轮后移崩溃点
					}
				}
				t.Fatalf("probe died unexpectedly: %v\n%s", err, output)
			}
			// 场景完成：verify 断言 journal 不变量。
			cmd := exec.Command(bin)
			cmd.Env = append(os.Environ(), "PROBE_DIR="+dir, "PROBE_MODE=verify")
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "VERIFY-OK") {
				t.Fatalf("verify failed: %v\n%s", err, output)
			}
		})
	}
}
