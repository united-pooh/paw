//go:build !windows

// ESC 聚合输入 reader。
//
// BubbleTea 的 readAnsiInputs 在 256 字节缓冲上循环 Read。当终端把一条
// SGR 鼠标序列 \x1b[<btn;col;row M 切在 \x1b 与 [ 之间送达（短读），BubbleTea
// 不会等续接：lone \x1b 被当 KeyEscape 投递，后续 [<...M 被当 KeyRunes，
// 于是触控板滑动时输入框偶发出现 [[[[[。
//
// 本 reader 为尾部 ESC 序列预留续接空间，在同一次 Read 内最多等待 15ms。
// 已到达的鼠标续接会立即拼接；真正的 ESC 在截止后原样送出。
// 不跨 Read 挂起字节，因为外层 cancelreader 会先等待底层 fd 可读。
package bubble

import (
	"os"
	"time"
	"unicode/utf8"
)

// escCoalescingReader 包装 *os.File，在 Read 时聚合被读边界切断的 ESC 序列。
// 内嵌 *os.File 保留 Fd/Name/ReadWriteCloser，使其同时满足 BubbleTea 的
// term.File 与 cancelreader.File 接口 —— MakeRaw 与 kqueue 路径均不受影响。
type escCoalescingReader struct {
	*os.File
	maxSequenceBytes int
}

// newESCCoalescingReader 用 f 构造一个 ESC 聚合 reader。f 通常是 os.Stdin。
func newESCCoalescingReader(f *os.File) *escCoalescingReader {
	return &escCoalescingReader{
		File:             f,
		maxSequenceBytes: 64,
	}
}

// Read 实现 io.Reader，在同一次读取内拼接尾部不完整的 ESC 序列。
func (r *escCoalescingReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	limit := len(p)
	if limit > r.maxSequenceBytes {
		limit -= r.maxSequenceBytes
	}
	n, err := r.File.Read(p[:limit])
	if n == 0 {
		return n, err
	}
	n = r.coalesceTrailing(p, n)
	return n, err
}

// 逐字节续接，避免拼好一条序列后又吞入下一条不完整序列。
func (r *escCoalescingReader) coalesceTrailing(p []byte, n int) int {
	deadline := time.Now().Add(15 * time.Millisecond)
	for {
		trim := trailingInputSequence(p[:n])
		if trim == 0 || trim > r.maxSequenceBytes || n == len(p) {
			return n
		}
		if !r.waitForContinuation(deadline) {
			return n
		}
		got, ok := r.peekOne(p[n : n+1])
		if !ok {
			return n
		}
		n += got
	}
}

// trailingInputSequence 返回尾部不完整 ESC 序列或 UTF-8 字符的字节数。
// 返回 0 表示末尾完整（可安全交给 BubbleTea）。判定移植自 Claude Code
// termio/tokenize.ts 的 CSI/OSC 状态机，仅做末尾悬空判断，不重写完整解析。
//
// 算法：从左到右扫描，逐条跳过已完整的 ESC 序列，记录最后一条序列的起点。
// 扫描结束后若最后一条序列未完整，则其起点到末尾即为悬空长度。
//
// 覆盖的悬空形态：lone ESC、CSI 头、SGR 鼠标未完成、OSC 未到 ST/BEL、
// SS3 未到终止符、DCS/APC 未到 ST。
func trailingInputSequence(b []byte) int {
	lastStart := -1 // 最后一条 ESC 序列的起点；-1 表示尚无
	i := 0
	for i < len(b) {
		if b[i] != 0x1b {
			if !utf8.FullRune(b[i:]) {
				return len(b) - i
			}
			_, size := utf8.DecodeRune(b[i:])
			i += size
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
		if len(seq) >= 3 && seq[2] == 'M' {
			return len(seq) >= 6, 6 // X10 鼠标头后是三个原始坐标字节
		}
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
		if !utf8.FullRune(seq[1:]) {
			return false, 0
		}
		_, size := utf8.DecodeRune(seq[1:])
		// 两字符 ESC 序列（ESC + final）或非法。
		return true, 1 + size
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
