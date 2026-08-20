//go:build unix

package es

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockStreamFile 对路径加排他文件锁（跨进程互斥），返回持锁句柄。
// 调用方必须以 unlockStreamFile 释放。
func lockStreamFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func unlockStreamFile(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}
