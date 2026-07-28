// ESC 聚合输入 reader。
//
// BubbleTea 的 readAnsiInputs 在 256 字节缓冲上循环 Read。当终端把一条
// SGR 鼠标序列 \x1b[<btn;col;row M 切在 \x1b 与 [ 之间送达（短读），BubbleTea
// 不会等续接：lone \x1b 被当 KeyEscape 投递，后续 [<...M 被当 KeyRunes，
// 于是触控板滑动时输入框偶发出现 [[[[[。
//
// 本 reader 在字节进入 BubbleTea 之前做一道聚合（移植自 Claude Code 的
// termio tokenizer + App.tsx 的 readableLength 守卫思路）：读到一个 chunk 后，
// 若末尾悬空在一条不完整 ESC 序列上，立即对底层 fd 做一次非阻塞 peek。
// 终端对鼠标事件是单次 write 整条序列的，剩余字节通常已在内核缓冲区，
// peek 立即拿到并拼成完整序列，BubbleTea 即可正确解析为 MouseMsg。
// peek 返回 EAGAIN（真按 ESC 后无续接）则把悬空字节当完整事件送出，
// 保证 ESC 键不延迟、不失灵。
package bubble

import (
	"errors"
	"os"
	"syscall"
)

// escCoalescingReader 包装 *os.File，在 Read 时聚合被读边界切断的 ESC 序列。
// 内嵌 *os.File 保留 Fd/Name/ReadWriteCloser，使其同时满足 BubbleTea 的
// term.File 与 cancelreader.File 接口 —— MakeRaw 与 kqueue 路径均不受影响。
type escCoalescingReader struct {
	*os.File
	held    []byte // 跨 Read 挂起的不完整 ESC 序列
	heldMax int    // held 字节上限，防止异常输入无限增长
}

// newESCCoalescingReader 用 f 构造一个 ESC 聚合 reader。f 通常是 os.Stdin。
func newESCCoalescingReader(f *os.File) *escCoalescingReader {
	return &escCoalescingReader{
		File:    f,
		heldMax: 64, // 单条 ESC 序列不会超过此长度；超出视为异常，原样放出
	}
}

// Read 实现 io.Reader。它先冲刷上一次挂起的悬空字节，再读底层 fd，
// 然后对末尾仍悬空的 ESC 序列做非阻塞 peek 拼接。
func (r *escCoalescingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// 先冲刷挂起字节。held 是上次 Read 末尾悬空、已 peek 到的续接，
	// 必须立即完整送出，不能与本次 fd 读取混在一起——否则 fd 读返回
	// EOF 时会把刚放出的字节重新挂起并丢失。所以 held 非空时单独返回。
	if len(r.held) > 0 {
		n := copy(p, r.held)
		if n == len(r.held) {
			r.held = r.held[:0]
		} else {
			r.held = r.held[n:]
		}
		return n, nil
	}

	// 阻塞读底层 fd。cancelreader 已保证此处 fd 可读，立即返回。
	n, err := r.File.Read(p)
	if n == 0 {
		return n, err
	}
	// 读到 EOF 等错误也先把已读字节返回；末尾若悬空则等下次 Read 拼接。

	// 对末尾悬空的 ESC 序列做非阻塞 peek 拼接。
	n = r.coalesceTrailing(p, n)

	// 末尾仍悬空：切下存入 held，等下次 Read 续接。
	// 超出 heldMax 视为异常输入，原样放出避免无限累积。
	if trim := trailingEscapeSlice(p[:n]); trim > 0 {
		if trim <= r.heldMax {
			r.held = append(r.held[:0], p[n-trim:n]...)
			n -= trim
		}
	}
	return n, firstErr(err, nil)
}

// coalesceTrailing 对 p[:n] 末尾悬空的 ESC 序列做非阻塞 peek 拼接，
// 把读到的续接字节追加进 p 并返回新的有效长度。peek EAGAIN 时停止。
func (r *escCoalescingReader) coalesceTrailing(p []byte, n int) int {
	for {
		trim := trailingEscapeSlice(p[:n])
		if trim == 0 {
			return n // 末尾完整
		}
		if n == len(p) {
			return n // p 已满，无法再拼；悬空部分留待下次 Read 处理
		}
		got, ok := r.peekOne(p[n:])
		if !ok {
			return n // peek 无数据（EAGAIN），停止
		}
		n += got
	}
}

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

// firstErr 返回第一个非 nil 的错误。
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
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

// trailingEscapeSlice 返回 b 末尾"可能是不完整 ESC 序列"的字节数。
// 返回 0 表示末尾完整（可安全交给 BubbleTea）。判定移植自 Claude Code
// termio/tokenize.ts 的 CSI/OSC 状态机，仅做末尾悬空判断，不重写完整解析。
//
// 算法：从左到右扫描，逐条跳过已完整的 ESC 序列，记录最后一条序列的起点。
// 扫描结束后若最后一条序列未完整，则其起点到末尾即为悬空长度。
//
// 覆盖的悬空形态：lone ESC、CSI 头、SGR 鼠标未完成、OSC 未到 ST/BEL、
// SS3 未到终止符、DCS/APC 未到 ST。
func trailingEscapeSlice(b []byte) int {
	lastStart := -1 // 最后一条 ESC 序列的起点；-1 表示尚无
	i := 0
	for i < len(b) {
		if b[i] != 0x1b {
			i++
			continue
		}
		lastStart = i
		complete, consumed := escapeSequenceComplete(b[i:])
		if complete {
			i += consumed
			lastStart = -1 // 该序列完整，重置；后续若再遇 ESC 会重设
			continue
		}
		break // 末尾序列未完整
	}
	if lastStart < 0 {
		return 0
	}
	return len(b) - lastStart
}

// escapeSequenceComplete 判断 seq（以 ESC 开头）是否已构成一条完整序列。
// 返回 (complete, consumed)。简化状态机，覆盖鼠标/CSI/OSC/SS3/DCS 常见形态。
func escapeSequenceComplete(seq []byte) (bool, int) {
	if len(seq) == 0 || seq[0] != 0x1b {
		return true, 0
	}
	if len(seq) == 1 {
		return false, 0 // lone ESC
	}
	switch seq[1] {
	case '[': // CSI
		return csiComplete(seq[2:])
	case ']': // OSC，以 BEL 或 ST(\x1b\\) 结束
		return oscComplete(seq[2:])
	case 'P', '_', 'X', '^': // DCS / APC / SOS / PM，以 ST 结束
		return stringTerminatedComplete(seq[2:])
	case 'O': // SS3，后接单字节终止符 0x40-0x7e
		if len(seq) < 3 {
			return false, 0
		}
		if seq[2] >= 0x40 && seq[2] <= 0x7e {
			return true, 3
		}
		return true, 2 // 非法，按完整处理交给上层
	default:
		// 两字符 ESC 序列（ESC + final）或非法。
		return true, 2
	}
}

// csiComplete 判断 CSI 参数段（去掉 \x1b[ 后）是否到达终止符。
// 参数字节 0x30-0x3f，中间字节 0x20-0x2f，终止符 0x40-0x7e。
func csiComplete(params []byte) (bool, int) {
	for i, c := range params {
		switch {
		case c >= 0x40 && c <= 0x7e: // 终止符
			return true, i + 3
		case c >= 0x30 && c <= 0x3f, c >= 0x20 && c <= 0x2f:
			// 参数/中间字节，继续
		default:
			return true, i + 3 // 非法字节，按完整处理
		}
	}
	return false, 0 // 参数未到终止符，悬空
}

// oscComplete 判断 OSC 内容是否遇到 BEL 或 ST 结束。
func oscComplete(content []byte) (bool, int) {
	for i, c := range content {
		if c == 0x07 { // BEL
			return true, i + 3
		}
		if c == 0x1b && i+1 < len(content) && content[i+1] == '\\' { // ST
			return true, i + 4
		}
		if c == 0x1b && i+1 >= len(content) {
			return false, 0 // ESC 已出现但 \ 尚未到达，悬空
		}
	}
	return false, 0
}

// stringTerminatedComplete 判断 DCS/APC 等是否遇到 ST(\x1b\\) 结束。
func stringTerminatedComplete(content []byte) (bool, int) {
	return oscComplete(content) // 同样的 ST/BEL 终止逻辑
}
