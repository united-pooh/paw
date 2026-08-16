//go:build darwin || linux

package task

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ApplyWorkerResourceLimits 在 worker 进程内设置软资源上限，作为 task 沙箱
// 资源限制（阶段三）的一部分。只应在 worker 入口调用，主 agent 进程不受影响。
// 传入的 limits 中 <=0 的字段回落默认值；设置失败返回错误，由调用方 fail-closed。
func ApplyWorkerResourceLimits(limits SandboxLimits) error {
	limits = resolveSandboxLimits(limits)
	resources := []struct {
		resource int
		value    uint64
		name     string
	}{
		{unix.RLIMIT_CPU, uint64(limits.CPUSeconds), "RLIMIT_CPU"},
		{unix.RLIMIT_FSIZE, uint64(limits.FileSizeMiB) * 1024 * 1024, "RLIMIT_FSIZE"},
		{unix.RLIMIT_NPROC, uint64(limits.MaxProcesses), "RLIMIT_NPROC"},
		{unix.RLIMIT_NOFILE, uint64(limits.OpenFiles), "RLIMIT_NOFILE"},
	}
	for _, item := range resources {
		if err := setSoftLimit(item.resource, item.value); err != nil {
			return fmt.Errorf("set %s: %w", item.name, err)
		}
	}
	return nil
}

// setSoftLimit 把指定资源的软上限设为目标值，但绝不越过当前硬上限（非 root 越界
// 会 EPERM，这里克制地为尽力而为语义，硬上限高于目标时目标直接生效）。
func setSoftLimit(resource int, value uint64) error {
	var current unix.Rlimit
	if err := unix.Getrlimit(resource, &current); err != nil {
		return err
	}
	if value > current.Max {
		value = current.Max
	}
	current.Cur = value
	return unix.Setrlimit(resource, &current)
}
