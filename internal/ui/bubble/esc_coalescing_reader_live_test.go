//go:build !windows

package bubble

import (
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/muesli/cancelreader"
)

type escapeReadResult struct {
	text string
	err  error
}

func readEscapeBeforeDeadline(t *testing.T, reader io.Reader, writer *os.File, size int) string {
	t.Helper()
	result := make(chan escapeReadResult, 1)
	go func() {
		buf := make([]byte, size)
		n, err := reader.Read(buf)
		result <- escapeReadResult{text: string(buf[:n]), err: err}
	}()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("Read: %v", got.err)
		}
		return got.text
	case <-time.After(250 * time.Millisecond):
		writer.Close()
		<-result
		t.Fatal("Read waited for another key or EOF while terminal writer remained open")
		return ""
	}
}

func TestLoneESCOpenWriterDeliveredInOneRead(t *testing.T) {
	r, writer := newPipeReader(t, 64)
	t.Cleanup(func() { r.Close(); writer.Close() })
	if _, err := writer.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	if got := readEscapeBeforeDeadline(t, r, writer, 256); got != "\x1b" {
		t.Fatalf("single Read = %q, want lone ESC", got)
	}
}

func TestDelayedMouseContinuationDeliveredInOneRead(t *testing.T) {
	r, writer := newPipeReader(t, 64)
	t.Cleanup(func() { r.Close(); writer.Close() })
	if _, err := writer.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(5 * time.Millisecond)
		writer.Write([]byte("[<64;59;25M"))
	}()
	got := readEscapeBeforeDeadline(t, r, writer, 256)
	<-done
	if got != "\x1b[<64;59;25M" {
		t.Fatalf("single Read = %q, want complete mouse event", got)
	}
}

func TestMouseAtBufferBoundaryThroughCancelReader(t *testing.T) {
	for _, prefix := range []int{191, 192, 250, 255, 256, 447} {
		t.Run(strconv.Itoa(prefix), func(t *testing.T) {
			r, writer := newPipeReader(t, 64)
			t.Cleanup(func() { r.Close(); writer.Close() })
			reader, err := cancelreader.NewReader(r)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reader.Close() })
			want := strings.Repeat("x", prefix) + "\x1b[<64;59;25M"
			if _, err := writer.Write([]byte(want)); err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			for got.Len() < len(want) {
				chunk := readEscapeBeforeDeadline(t, reader, writer, 256)
				if chunk == "" || trailingInputSequence([]byte(chunk)) > 0 {
					t.Fatalf("prefix=%d: Read leaked incomplete event %q", prefix, chunk)
				}
				got.WriteString(chunk)
			}
			if got.String() != want {
				t.Fatalf("prefix=%d: bytes changed: %q", prefix, got.String())
			}
		})
	}
}

func TestESCReadPreservesOriginalDescriptorFlags(t *testing.T) {
	for _, nonblocking := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocking", true: "nonblocking"}[nonblocking], func(t *testing.T) {
			r, writer := newPipeReader(t, 64)
			t.Cleanup(func() { r.Close(); writer.Close() })
			raw, err := r.File.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var expected uintptr
			raw.Control(func(fd uintptr) {
				expected, err = fcntl(fd, syscall.F_GETFL, 0)
				if nonblocking {
					expected |= syscall.O_NONBLOCK
				} else {
					expected &^= syscall.O_NONBLOCK
				}
				_, err = fcntl(fd, syscall.F_SETFL, expected)
			})
			if err != nil {
				t.Fatal(err)
			}
			writer.Write([]byte("[A"))
			buf := make([]byte, 2)
			if n, ok := r.peekOne(buf); !ok || n != 2 || string(buf) != "[A" {
				t.Fatalf("peek = %q, %d, %v", buf, n, ok)
			}
			var actual uintptr
			raw.Control(func(fd uintptr) { actual, err = fcntl(fd, syscall.F_GETFL, 0) })
			if err != nil || actual != expected {
				t.Fatalf("fd flags = %#x (%v), want %#x", actual, err, expected)
			}
		})
	}
}

func TestInputUnicodeAtReadBoundary(t *testing.T) {
	for _, fixture := range []struct {
		prefix int
		text   string
	}{{191, "中"}, {189, "🐾"}, {190, "🐾"}, {191, "🐾"}, {190, "\x1b中"}, {190, "\x1b🐾"}} {
		t.Run(strconv.Itoa(fixture.prefix)+"/"+fixture.text, func(t *testing.T) {
			r, writer := newPipeReader(t, 64)
			t.Cleanup(func() { r.Close(); writer.Close() })
			want := strings.Repeat("x", fixture.prefix) + fixture.text
			writer.Write([]byte(want))
			var got strings.Builder
			for got.Len() < len(want) {
				chunk := readEscapeBeforeDeadline(t, r, writer, 256)
				if chunk == "" || !utf8.ValidString(chunk) {
					t.Fatalf("Read split a UTF-8 rune: %q", chunk)
				}
				got.WriteString(chunk)
			}
			if got.String() != want {
				t.Fatalf("bytes changed: got %q, want %q", got.String(), want)
			}
		})
	}
}

func TestInputCancelAfterEscapeRead(t *testing.T) {
	for _, input := range []string{"\x1b", "\x1b[<64;59;25M"} {
		t.Run(strconv.Quote(input), func(t *testing.T) {
			r, writer := newPipeReader(t, 64)
			t.Cleanup(func() { r.Close(); writer.Close() })
			reader, err := cancelreader.NewReader(r)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reader.Close() })
			writer.Write([]byte(input))
			if got := readEscapeBeforeDeadline(t, reader, writer, 256); got != input {
				t.Fatalf("Read = %q, want %q", got, input)
			}
			done := make(chan error, 1)
			go func() { _, err := reader.Read(make([]byte, 256)); done <- err }()
			reader.Cancel()
			select {
			case err := <-done:
				if err != cancelreader.ErrCanceled {
					t.Fatalf("Read after cancel: %v", err)
				}
			case <-time.After(250 * time.Millisecond):
				writer.Close()
				<-done
				t.Fatal("cancel did not release input Read")
			}
		})
	}
}

func TestInputSmallBuffersPreserveBytes(t *testing.T) {
	for _, size := range []int{1, 2, 8, 64} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			r, writer := newPipeReader(t, 64)
			t.Cleanup(func() { r.Close(); writer.Close() })
			want := "\x1b[<64;59;25M中文🐾"
			writer.Write([]byte(want))
			writer.Close()
			buf := make([]byte, size)
			var got strings.Builder
			for {
				n, err := r.Read(buf)
				got.Write(buf[:n])
				if err == io.EOF {
					break
				}
				if err != nil || n == 0 {
					t.Fatalf("Read = %d, %v", n, err)
				}
			}
			if got.String() != want {
				t.Fatalf("bytes changed: got %q, want %q", got.String(), want)
			}
		})
	}
}

func TestInputX10MouseAtReadBoundary(t *testing.T) {
	r, writer := newPipeReader(t, 64)
	t.Cleanup(func() { r.Close(); writer.Close() })
	want := strings.Repeat("x", 189) + "\x1b[M\x20\xc3\xf0"
	writer.Write([]byte(want))
	if got := readEscapeBeforeDeadline(t, r, writer, 256); got != want {
		t.Fatalf("Read split X10 mouse event: got %q, want %q", got, want)
	}
}
