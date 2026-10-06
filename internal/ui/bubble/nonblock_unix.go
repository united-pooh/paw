//go:build !windows

package bubble

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// peekOne 在非阻塞模式下读一次底层 fd。读到字节返回 (n, true)；
// 无数据可读（EAGAIN/EWOULDBLOCK）返回 (0, false)。读完恢复 fd 原状态。
func (r *escCoalescingReader) peekOne(buf []byte) (int, bool) {
	raw, err := r.File.SyscallConn()
	if err != nil {
		return 0, false
	}
	n := 0
	_ = raw.Control(func(fd uintptr) {
		flags, err := fcntl(fd, syscall.F_GETFL, 0)
		if err != nil {
			return
		}
		if _, err = fcntl(fd, syscall.F_SETFL, flags|syscall.O_NONBLOCK); err != nil {
			return
		}
		defer fcntl(fd, syscall.F_SETFL, flags) //nolint:errcheck
		// os.File.Read 会让 Go poller 等待 EAGAIN；这里必须直接读取 fd。
		if got, err := syscall.Read(int(fd), buf); err == nil {
			n = got
		}
	})
	return n, n > 0
}

func (r *escCoalescingReader) waitForContinuation(deadline time.Time) bool {
	raw, err := r.File.SyscallConn()
	if err != nil {
		return false
	}
	ready := false
	_ = raw.Control(func(fd uintptr) {
		for remaining := time.Until(deadline); remaining > 0; remaining = time.Until(deadline) {
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			timeout := int((remaining + time.Millisecond - 1) / time.Millisecond)
			n, err := unix.Poll(fds, timeout)
			if err == syscall.EINTR {
				continue
			}
			ready = err == nil && n > 0 && fds[0].Revents&unix.POLLIN != 0
			return
		}
	})
	return ready
}

// fcntl 包装系统调用，统一处理 errno。
func fcntl(fd, cmd, arg uintptr) (uintptr, error) {
	r1, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, cmd, arg)
	if errno != 0 {
		return 0, errno
	}
	return r1, nil
}
