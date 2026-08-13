//go:build windows

package plan

import (
	"os"

	"golang.org/x/sys/windows"
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
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(lockFile.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = lockFile.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(lockFile.Fd()), 0, 1, 0, overlapped)
		_ = lockFile.Close()
	}, nil
}
