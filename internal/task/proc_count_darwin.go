//go:build darwin

package task

import (
	"os"

	"golang.org/x/sys/unix"
)

// countCurrentUserProcesses 返回当前 real UID 名下的进程数。XNU 对 RLIMIT_NPROC
// 按 real UID 计费（chgproccnt），所以基线必须用同一口径统计。
func countCurrentUserProcesses() (uint64, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return 0, err
	}
	ruid := uint32(os.Getuid())
	var count uint64
	for i := range procs {
		if procs[i].Eproc.Pcred.P_ruid == ruid {
			count++
		}
	}
	return count, nil
}
