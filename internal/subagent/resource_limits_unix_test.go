//go:build darwin || linux

package subagent

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestWorkerResourceLimitsApplied 验证 worker 只设置软上限、且不越过硬上限，并在
// 结束后把软上限还原为原值，避免影响同进程内的其他测试。
func TestWorkerResourceLimitsApplied(t *testing.T) {
	apply := func(resource int, configured uint64) unix.Rlimit {
		var before unix.Rlimit
		if err := unix.Getrlimit(resource, &before); err != nil {
			t.Fatalf("Getrlimit(%d): %v", resource, err)
		}
		// 设置成功后立即读回，验证软上限 === min(configured, 硬上限)。
		if err := ApplyWorkerResourceLimits(); err != nil {
			t.Fatalf("ApplyWorkerResourceLimits: %v", err)
		}
		var after unix.Rlimit
		if err := unix.Getrlimit(resource, &after); err != nil {
			t.Fatalf("Getrlimit after(%d): %v", resource, err)
		}
		want := configured
		if want > before.Max {
			want = before.Max
		}
		if after.Cur != want {
			t.Errorf("resource %d Cur = %d, want %d", resource, after.Cur, want)
		}
		// 还原软上限。
		before.Cur = before.Cur
		if err := unix.Setrlimit(resource, &before); err != nil {
			t.Fatalf("restore %d: %v", resource, err)
		}
		return after
	}

	apply(int(unix.RLIMIT_CPU), workerCPUSeconds)
	apply(int(unix.RLIMIT_FSIZE), workerFileSizeBytes)
	apply(int(unix.RLIMIT_NPROC), workerMaxProcesses)
	apply(int(unix.RLIMIT_NOFILE), workerMaxOpenFiles)
}