package file

import (
	"strings"
	"testing"
)

func TestReadStateStoreVerifyNoOpWithoutPriorRead(t *testing.T) {
	s := NewReadStateStore()
	// No prior Record: Verify must be lenient (no error).
	if err := s.Verify("/some/path", []byte("anything")); err != nil {
		t.Fatalf("Verify without prior read = %v, want nil", err)
	}
}

func TestReadStateStoreVerifyPassesAfterUnchangedRead(t *testing.T) {
	s := NewReadStateStore()
	content := []byte("hello\n")
	s.Record("/p", content)
	if err := s.Verify("/p", content); err != nil {
		t.Fatalf("Verify after unchanged read = %v", err)
	}
}

func TestReadStateStoreVerifyFailsOnExternalModification(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("hello\n"))
	err := s.Verify("/p", []byte("hello world\n"))
	if err == nil {
		t.Fatal("expected stale-write error, got nil")
	}
	if !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want modified-since-read", err)
	}
}

func TestReadStateStoreRecordAfterWriteResetsBaseline(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("v1\n"))
	s.RecordAfterWrite("/p", []byte("v2\n"))
	// After a write, the recorded baseline is v2; v2 must verify clean.
	if err := s.Verify("/p", []byte("v2\n")); err != nil {
		t.Fatalf("Verify after write = %v", err)
	}
	// v1 must now be stale.
	if err := s.Verify("/p", []byte("v1\n")); err == nil {
		t.Fatal("expected stale error after baseline reset")
	}
}

func TestReadStateStoreVerifyConcurrentSafe(t *testing.T) {
	s := NewReadStateStore()
	s.Record("/p", []byte("x"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = s.Verify("/p", []byte("x"))
		}
	}()
	for i := 0; i < 200; i++ {
		s.Record("/p", []byte("x"))
	}
	<-done
	// No data race detector failure == pass (run with -race).
	if err := s.Verify("/p", []byte("x")); err != nil {
		t.Fatalf("final Verify = %v", err)
	}
}
