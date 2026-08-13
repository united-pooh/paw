//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package plan

import (
	"os"

	"golang.org/x/sys/unix"
)

// acquirePlanProjectionLock 对投影文件路径加独占锁（跟随 internal/config
// CAS writer 锁模式）。锁文件为 path+".lock"。
func acquirePlanProjectionLock(path string) (func(), error) {
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	for {
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		_ = lockFile.Close()
	}, nil
}
