package file

import (
	"strings"
	"testing"
)

func TestReadStateStoreVerifyRequiredRejectsMissingBaseline(t *testing.T) {
	s := NewReadStateStore()
	err := s.VerifyRequired("core/utils.py", []byte("current"))
	if err == nil {
		t.Fatal("VerifyRequired without prior Read = nil, want error")
	}
	if !strings.Contains(err.Error(), "file must be read before editing: core/utils.py; use Read first") {
		t.Fatalf("err = %q", err)
	}
}

func TestReadStateStoreVerifyRequiredAcceptsMatchingBaseline(t *testing.T) {
	s := NewReadStateStore()
	s.Record("core/utils.py", []byte("current"))
	if err := s.VerifyRequired("core/utils.py", []byte("current")); err != nil {
		t.Fatalf("VerifyRequired = %v", err)
	}
}

func TestReadStateStoreVerifyRequiredRejectsChangedContent(t *testing.T) {
	s := NewReadStateStore()
	s.Record("core/utils.py", []byte("before"))
	err := s.VerifyRequired("core/utils.py", []byte("after"))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-read error", err)
	}
}

func TestReadStateStoreStrictAndLenientVerificationAreIndependent(t *testing.T) {
	s := NewReadStateStore()
	if err := s.Verify("unread.txt", []byte("x")); err != nil {
		t.Fatalf("lenient Verify = %v, want nil", err)
	}
	if err := s.VerifyRequired("unread.txt", []byte("x")); err == nil {
		t.Fatal("strict VerifyRequired = nil, want error")
	}
}

func TestReadStateStoreVerifyRequiredKeepsPathsIsolated(t *testing.T) {
	s := NewReadStateStore()
	s.Record("read.txt", []byte("same"))
	if err := s.VerifyRequired("unread.txt", []byte("same")); err == nil {
		t.Fatal("VerifyRequired accepted a baseline recorded for another path")
	}
}

func TestReadStateStoreVerifyRequiredRejectsNilStore(t *testing.T) {
	var s *ReadStateStore
	err := s.VerifyRequired("nil.txt", []byte("current"))
	if err == nil || !strings.Contains(err.Error(), "file must be read before editing: nil.txt; use Read first") {
		t.Fatalf("err = %v, want missing-baseline error", err)
	}
}

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

func TestReadStateStoreVerifyRequiredWithDiffUsesLatestReadPage(t *testing.T) {
	s := NewReadStateStore()
	s.RecordRead("/p", contentHash([]byte("old\nkeep\noutside\n")), 0, 2, []byte("old\nkeep\n"))

	err := s.VerifyRequiredWithDiff("/p", []byte("new\nkeep\noutside\n"))
	if err == nil || !strings.Contains(err.Error(), "read-range diff") || !strings.Contains(err.Error(), "-old") || !strings.Contains(err.Error(), "+new") {
		t.Fatalf("err = %v, want latest page diff", err)
	}
}

func TestReadStateStoreVerifyRequiredWithDiffCapsDiff(t *testing.T) {
	s := NewReadStateStore()
	oldContent := strings.Repeat("old\n", 4000)
	newContent := strings.Repeat("new\n", 4000)
	s.RecordRead("/p", contentHash([]byte(oldContent)), 0, 4000, []byte(oldContent))

	err := s.VerifyRequiredWithDiff("/p", []byte(newContent))
	if err == nil {
		t.Fatal("VerifyRequiredWithDiff = nil, want stale error")
	}
	if len([]byte(err.Error())) > maxReadDiffBytes+512 {
		t.Fatalf("error bytes = %d, want bounded diff", len([]byte(err.Error())))
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
			_ = s.VerifyRequired("/p", []byte("x"))
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
