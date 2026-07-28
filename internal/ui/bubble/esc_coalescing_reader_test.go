package bubble

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// newPipeReader 构造一个基于 os.Pipe 的 ESC 聚合 reader 及其写入端。
// os.Pipe 的 fd 支持 fcntl/O_NONBLOCK，可模拟真实终端的读边界。
func newPipeReader(t *testing.T, heldMax int) (*escCoalescingReader, *os.File) {
	t.Helper()
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	r := &escCoalescingReader{File: rp, heldMax: heldMax}
	return r, wp
}

// drainAll 反复 Read 直到 EOF 或超时，返回累计字节。
func drainAll(t *testing.T, r *escCoalescingReader) string {
	t.Helper()
	var buf strings.Builder
	tmp := make([]byte, 64)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			break
		}
	}
	return buf.String()
}

// TestSplitMouseSequenceCoalesced: \x1b 与 [<...M 分两次写，
// reader 应聚合成完整序列，不产生 lone \x1b。
func TestSplitMouseSequenceCoalesced(t *testing.T) {
	r, wp := newPipeReader(t, 64)
	go func() {
		wp.Write([]byte{0x1b})
		time.Sleep(5 * time.Millisecond)
		wp.Write([]byte("[<64;59;25M"))
		wp.Close()
	}()
	got := drainAll(t, r)
	want := "\x1b[<64;59;25M"
	if got != want {
		t.Fatalf("got %q, want %q (ESC should not leak as lone byte)", got, want)
	}
}

// TestLoneESCPassesThrough: 真 ESC 无续接，应原样送出（不延迟、不吞）。
func TestLoneESCPassesThrough(t *testing.T) {
	r, wp := newPipeReader(t, 64)
	go func() {
		wp.Write([]byte{0x1b})
		time.Sleep(20 * time.Millisecond) // 确保无续接
		wp.Close()
	}()
	got := drainAll(t, r)
	if got != "\x1b" {
		t.Fatalf("got %q, want lone \\x1b", got)
	}
}

// TestCSIFragmentHeldAcrossReads: \x1b[ 写入后无续接，应挂起；
// 之后写 A 应与挂起部分拼成 \x1b[A。
func TestCSIFragmentHeldAcrossReads(t *testing.T) {
	r, wp := newPipeReader(t, 64)
	go func() {
		wp.Write([]byte{0x1b, '['})
		time.Sleep(20 * time.Millisecond)
		wp.Write([]byte{'A'})
		wp.Close()
	}()
	got := drainAll(t, r)
	if got != "\x1b[A" {
		t.Fatalf("got %q, want \\x1b[A", got)
	}
}

// TestPlainTextNotFragmented: 普通文本应原样通过，held 为空。
func TestPlainTextNotFragmented(t *testing.T) {
	r, wp := newPipeReader(t, 64)
	go func() {
		wp.Write([]byte("hello world"))
		wp.Close()
	}()
	got := drainAll(t, r)
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
	if len(r.held) != 0 {
		t.Fatalf("held = %q, want empty after complete text", r.held)
	}
}

// TestCompleteSequenceFollowedByText: 一条完整 CSI 序列 + 普通文本，
// 完整序列不应被切，文本应原样跟随。
func TestCompleteSequenceFollowedByText(t *testing.T) {
	r, wp := newPipeReader(t, 64)
	go func() {
		wp.Write([]byte("\x1b[Ahello"))
		wp.Close()
	}()
	got := drainAll(t, r)
	if got != "\x1b[Ahello" {
		t.Fatalf("got %q, want \\x1b[Ahello", got)
	}
}

// TestHeldCapProtectsAgainstBloat: 连续写大量 \x1b[ 无续接，
// held 不应超过 heldMax。
func TestHeldCapProtectsAgainstBloat(t *testing.T) {
	r, wp := newPipeReader(t, 8) // 小上限便于触发
	go func() {
		// 写 20 个 \x1b[ 序列头，均无续接到终止符
		wp.Write(bytes.Repeat([]byte{0x1b, '['}, 20))
		wp.Close()
	}()
	_ = drainAll(t, r)
	if len(r.held) > r.heldMax {
		t.Fatalf("held len = %d, want <= %d", len(r.held), r.heldMax)
	}
}
