//go:build !unix

package es

import "os"

// 非 unix 平台无 flock：退化为无锁（保持旧实现的行为与既有风险）。
func lockStreamFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

func unlockStreamFile(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
