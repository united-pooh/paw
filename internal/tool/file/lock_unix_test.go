//go:build darwin || linux

package file

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func mutationLockPath(root string) string {
	return filepath.Join(root, ".paw", "locks", "mutation.lock")
}

// TestMutationLockExclusiveAcrossOpeners 验证 flock 在该工作区锁文件上是跨打开
// 描述符独占的（等价跨 worker 进程）：持锁期间第二个 opener 非阻塞获取失败，
// 释放后可成功获取。
func TestMutationLockExclusiveAcrossOpeners(t *testing.T) {
	root := t.TempDir()

	held := make(chan struct{})
	released := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = withMutationLock(root, func() error {
			close(held) // 此时锁已被独占
			<-released
			return nil
		})
		close(done)
	}()

	<-held
	probe, err := os.OpenFile(mutationLockPath(root), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open mutation lock: %v", err)
	}
	defer probe.Close()
	if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("flock should fail while mutation lock is held")
	}

	close(released) // 释放锁
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("withMutationLock did not release promptly")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return // 释放后可获取
		}
		if time.Now().After(deadline) {
			t.Fatal("flock still blocked after release")
		}
		time.Sleep(5 * time.Millisecond)
	}
}