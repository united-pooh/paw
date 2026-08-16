//go:build darwin || linux

package subagent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	workerCPUSeconds    = 120               // RLIMIT_CPU：单 worker 累计 CPU 秒数
	workerFileSizeBytes = 256 * 1024 * 1024 // RLIMIT_FSIZE：单文件写入上限
	workerMaxProcesses  = 64                // RLIMIT_NPROC：worker 可派生的进程数
	workerMaxOpenFiles  = 256               // RLIMIT_NOFILE：单 worker 打开文件描述符上限
)

// ApplyWorkerResourceLimits 在 worker 进程内设置软资源上限，作为 subagent 沙箱
// 资源限制（阶段三）的一部分。只应在 worker 入口调用，主 agent 进程不受影响。
// 设置失败返回错误，由调用方 fail-closed。
func ApplyWorkerResourceLimits() error {
	limits := []struct {
		resource int
		value    uint64
		name     string
	}{
		{unix.RLIMIT_CPU, workerCPUSeconds, "RLIMIT_CPU"},
		{unix.RLIMIT_FSIZE, workerFileSizeBytes, "RLIMIT_FSIZE"},
		{unix.RLIMIT_NPROC, workerMaxProcesses, "RLIMIT_NPROC"},
		{unix.RLIMIT_NOFILE, workerMaxOpenFiles, "RLIMIT_NOFILE"},
	}
	for _, limit := range limits {
		if err := setSoftLimit(limit.resource, limit.value); err != nil {
			return fmt.Errorf("set %s: %w", limit.name, err)
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