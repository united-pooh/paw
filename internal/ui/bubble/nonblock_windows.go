//go:build windows

package bubble

import (
	"syscall"
	"unsafe"
)

// procPeekNamedPipe: BOOL PeekNamedPipe(HANDLE, LPVOID, DWORD, LPDWORD, LPDWORD, LPDWORD)
// 非破坏式地查询命名管道是否有积压数据。
var procPeekNamedPipe = syscall.NewLazyDLL("kernel32.dll").NewProc("PeekNamedPipe")

// peekOne 在 Windows 上做一次非阻塞 peek 拼接。
//
// Windows 没有 fcntl/O_NONBLOCK。对管道句柄（Windows Terminal / 多数
// ConPTY 前端经命名管道投递输入），用 PeekNamedPipe 检查是否有积压字节，
// 仅当确实有数据时才阻塞 Read——等价于 Unix 的非阻塞 peek：有续接就读到，
// 无数据立即返回 (0, false)，不会把已挂起的 ESC 当可读数据卡住。
//
// 对真正的控制台句柄（CONIN$）PeekNamedPipe 会失败，返回 (0, false)，
// 表示无法可靠 peek——此时不做聚合，ESC 键仍正常，仅触控板连续滑动时
// 偶发的序列切分在控制台场景下无法修复（与上游行为一致）。
func (r *escCoalescingReader) peekOne(buf []byte) (int, bool) {
	// 只查询可读字节数，不消耗数据（lpBuffer=nil, lpBytesRead=nil）。
	var avail uint32
	r1, _, _ := procPeekNamedPipe.Call(
		r.File.Fd(), // hNamedPipe
		0,           // lpBuffer
		0,           // nBufferSize
		0,           // lpBytesRead
		uintptr(unsafe.Pointer(&avail)), // lpTotalBytesAvail
		0,                               // lpBytesLeftThisMessage
	)
	if r1 == 0 {
		// 非管道句柄（如控制台）无法 peek，视为无续接。
		return 0, false
	}
	if avail == 0 {
		return 0, false
	}

	// 已确认有数据，阻塞读不会挂起。
	m, err := r.File.Read(buf)
	if err == nil && m > 0 {
		return m, true
	}
	return 0, false
}
