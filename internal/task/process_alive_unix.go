//go:build !windows

package task

import (
	"errors"
	"syscall"
)

// processAlive 报告 pid 对应的进程是否仍然存在（signal 0 探测）。
// EPERM 表示进程存在但无权限发送信号，同样视为存活。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
