//go:build linux

package task

import (
	"os"
	"strconv"
	"strings"
)

// countCurrentUserProcesses 返回当前 real UID 名下的进程数。Linux 对 RLIMIT_NPROC
// 按 real UID 计费，/proc 顶层数字目录每个对应一个线程组组长（即一个进程），
// 线程不重复计数，与内核口径一致。
func countCurrentUserProcesses() (uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	ruid := os.Getuid()
	var count uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		owner, err := procRealUID("/proc/" + entry.Name() + "/status")
		if err != nil {
			// 进程可能在扫描期间退出，跳过即可。
			continue
		}
		if owner == ruid {
			count++
		}
	}
	return count, nil
}

// procRealUID 解析 /proc/<pid>/status 中 "Uid:" 行的第一个字段（real UID）。
func procRealUID(statusPath string) (int, error) {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, strconv.ErrSyntax
		}
		return strconv.Atoi(fields[1])
	}
	return 0, strconv.ErrSyntax
}
