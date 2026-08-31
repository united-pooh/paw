//go:build darwin || linux

package task

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestWorkerResourceLimitsApplied 验证 worker 只设置软上限、且不越过硬上限，并在
// 结束后把软上限还原为原值，避免影响同进程内的其他测试。limits 传入零值以验证
// 默认路径；显式值路径由数值断言覆盖。
//
// RLIMIT_NPROC 回归：NPROC 在 Linux/macOS 上按真实 UID 统计全系统进程数，不是
// 单 worker 预算；worker 内调低它会让同 UID 的所有 fork 直接 EAGAIN（即
// “fork/exec /bin/bash: resource temporarily unavailable” 的根因）。因此
// ApplyWorkerResourceLimits 必须完全不触碰 NPROC。
func TestWorkerResourceLimitsApplied(t *testing.T) {
	resolved := resolveSandboxLimits(SandboxLimits{})

	// NPROC 基线必须在任何 Apply 之前采集——旧实现会把 NPROC 压到默认值 64。
	var nprocBefore unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &nprocBefore); err != nil {
		t.Fatalf("Getrlimit(RLIMIT_NPROC): %v", err)
	}

	apply := func(resource int, configured uint64) {
		var before unix.Rlimit
		if err := unix.Getrlimit(resource, &before); err != nil {
			t.Fatalf("Getrlimit(%d): %v", resource, err)
		}
		// 设置成功后立即读回，验证 soft 上限 === min(configured, 硬上限)。
		if err := ApplyWorkerResourceLimits(SandboxLimits{}); err != nil {
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
		// 还原 soft 上限。
		if err := unix.Setrlimit(resource, &before); err != nil {
			t.Fatalf("restore %d: %v", resource, err)
		}
	}

	apply(int(unix.RLIMIT_CPU), uint64(resolved.CPUSeconds))
	apply(int(unix.RLIMIT_FSIZE), uint64(resolved.FileSizeMiB)*1024*1024)
	apply(int(unix.RLIMIT_NOFILE), uint64(resolved.OpenFiles))

	if err := ApplyWorkerResourceLimits(SandboxLimits{}); err != nil {
		t.Fatalf("ApplyWorkerResourceLimits: %v", err)
	}
	var nprocAfter unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &nprocAfter); err != nil {
		t.Fatalf("Getrlimit after(RLIMIT_NPROC): %v", err)
	}
	if nprocAfter != nprocBefore {
		t.Errorf("RLIMIT_NPROC changed: before={Cur:%d Max:%d} after={Cur:%d Max:%d}",
			nprocBefore.Cur, nprocBefore.Max, nprocAfter.Cur, nprocAfter.Max)
	}
}

// TestResolveSandboxLimitsDefaults 验证字段 <=0 时回落默认值、显式值透传。
func TestResolveSandboxLimitsDefaults(t *testing.T) {
	withExplicit := resolveSandboxLimits(SandboxLimits{
		CPUSeconds: 7, FileSizeMiB: 8, MaxProcesses: 9, OpenFiles: 10, JobWallSeconds: 11,
	})
	if withExplicit.CPUSeconds != 7 || withExplicit.FileSizeMiB != 8 ||
		withExplicit.MaxProcesses != 9 || withExplicit.OpenFiles != 10 || withExplicit.JobWallSeconds != 11 {
		t.Fatalf("explicit limits not preserved: %#v", withExplicit)
	}
	withZero := resolveSandboxLimits(SandboxLimits{})
	if withZero.JobWallSeconds != 90*60 {
		t.Fatalf("default job wall seconds = %d, want 90 minutes", withZero.JobWallSeconds)
	}
	if withZero.CPUSeconds != defaultWorkerCPUSeconds || withZero.FileSizeMiB != defaultWorkerFileSizeMiB ||
		withZero.MaxProcesses != defaultWorkerMaxProcesses || withZero.OpenFiles != defaultWorkerOpenFiles ||
		withZero.JobWallSeconds != defaultWorkerJobWallSecs {
		t.Fatalf("default limits not applied: %#v", withZero)
	}
}
