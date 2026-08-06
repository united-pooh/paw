//go:build windows

// Windows 版 ESC 输入 reader：透传包装，不做聚合。
//
// 为什么 Windows 不需要 Unix 版的 ESC 聚合：
//  1. Windows 控制台（ConHost / Windows Terminal）的输入由 BubbleTea 的
//     coninput 后端以 Win32 控制台事件（KEY_EVENT / MOUSE_EVENT）读取，
//     鼠标等输入不经过 ANSI 字节流，不存在 \x1b 与 [<...M 被读边界切断
//     导致 [[[[[[[ 泄漏的问题；
//  2. Windows 没有 fcntl/F_GETFL/O_NONBLOCK，Unix 版基于非阻塞 peek 的
//     聚合方案无法移植（x/sys/windows 亦无对控制台句柄的等价操作）。
//
// 透传行为与直接使用 os.Stdin 等价，同时内嵌 *os.File 保留了
// Fd/Name/ReadWriteCloser，满足 BubbleTea 的 term.File 与
// cancelreader.File 接口，MakeRaw 与取消路径均不受影响。
package bubble

import "os"

// escCoalescingReader 在 Windows 上是 *os.File 的透传包装。
type escCoalescingReader struct {
	*os.File
}

// newESCCoalescingReader 构造透传 reader。f 通常是 os.Stdin。
func newESCCoalescingReader(f *os.File) *escCoalescingReader {
	return &escCoalescingReader{File: f}
}

// Read 直接透传底层 fd，不做任何聚合或缓冲。
func (r *escCoalescingReader) Read(p []byte) (int, error) {
	return r.File.Read(p)
}
