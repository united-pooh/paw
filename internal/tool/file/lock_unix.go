//go:build darwin || linux

package file

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// withMutationLock 在 root/.paw/locks/mutation.lock 上持有独占 advisory flock，
// 跨进程（多 worker）串行化同一工作区的文件变更，避免 Write/Edit 并发写同一文件
// 产生 lost-update 与互相覆盖。这是 OS 级锁（flock），不是进程内 Go mutex。
// 平台无 flock 时降级为无锁（window/other），由各 worker 尽力而为。
func withMutationLock(root string, fn func() error) error {
	if root == "" {
		return fn()
	}
	dir := filepath.Join(root, ".paw", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create mutation lock dir: %w", err)
	}
	path := filepath.Join(dir, "mutation.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open mutation lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return fmt.Errorf("acquire mutation lock: %w", err)
	}
	defer func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}()
	return fn()
}