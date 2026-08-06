//go:build windows

package bubble

import (
	"os"
	"testing"
	"time"
)

// TestWindowsReaderPassesThrough: 透传 reader 应原样返回管道数据，
// 不做聚合、不吞字节。
func TestWindowsReaderPassesThrough(t *testing.T) {
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer rp.Close()

	r := newESCCoalescingReader(rp)
	want := "\x1b[<64;59;25M"

	go func() {
		_, _ = wp.Write([]byte(want))
		_ = wp.Close()
	}()

	buf := make([]byte, 64)
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := r.Read(buf)
		done <- result{n, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Read() error = %v", res.err)
		}
		if got := string(buf[:res.n]); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read() timed out")
	}
}
