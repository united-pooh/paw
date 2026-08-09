//go:build !windows

package bubble

import (
	"errors"
	"os"
	"syscall"
)

// peekOne 在非阻塞模式下读一次底层 fd。读到字节返回 (n, true)；
// 无数据可读（EAGAIN/EWOULDBLOCK）返回 (0, false)。读完恢复 fd 原状态。
func (r *escCoalescingReader) peekOne(buf []byte) (int, bool) {
	if err := setNonblock(r.File, true); err != nil {
		return 0, false
	}
	defer setNonblock(r.File, false) //nolint:errcheck

	m, err := r.File.Read(buf)
	if err == nil && m > 0 {
		return m, true
	}
	if err != nil && (errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)) {
		return 0, false
	}
	// 其它错误（如 EOF）也视为无续接。
	return 0, false
}

// setNonblock 切换 fd 的 O_NONBLOCK 标志。
func setNonblock(f *os.File, nonblock bool) error {
	fd := f.Fd()
	arg, err := fcntl(fd, syscall.F_GETFL, 0)
	if err != nil {
		return err
	}
	if nonblock {
		arg |= syscall.O_NONBLOCK
	} else {
		arg &^= syscall.O_NONBLOCK
	}
	_, err = fcntl(fd, syscall.F_SETFL, arg)
	return err
}

// fcntl 包装系统调用，统一处理 errno。
func fcntl(fd, cmd, arg uintptr) (uintptr, error) {
	r1, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, cmd, arg)
	if errno != 0 {
		return 0, errno
	}
	return r1, nil
}
