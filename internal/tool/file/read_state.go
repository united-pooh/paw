package file

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

const maxReadDiffBytes = 8 * 1024

type readBaseline struct {
	hash    string
	offset  int
	limit   int
	page    []byte
	hasPage bool
}

// ReadStateStore records a per-path content hash when files are Read, so that
// Edit/Write can detect external modification between the last Read and the
// write (lost-update / stale-write protection). If no prior Read was recorded
// for a path, Verify is lenient and returns nil.
type ReadStateStore struct {
	mu     sync.Mutex
	states map[string]readBaseline
}

func NewReadStateStore() *ReadStateStore {
	return &ReadStateStore{states: make(map[string]readBaseline)}
}

// Record stores the content hash for path as the last-seen-on-Read baseline.
func (s *ReadStateStore) Record(path string, content []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string]readBaseline)
	}
	s.states[path] = readBaseline{hash: contentHash(content)}
}

// RecordRead stores the complete-file hash and the bounded page returned by a
// paginated Read. The page is retained only so stale-write errors can show a
// small diff without keeping a complete large-file snapshot in memory.
func (s *ReadStateStore) RecordRead(path, hash string, offset, limit int, page []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string]readBaseline)
	}
	copyPage := append([]byte(nil), page...)
	s.states[path] = readBaseline{hash: hash, offset: offset, limit: limit, page: copyPage, hasPage: true}
}

// Verify returns an error if a prior Read recorded a hash for path and
// current no longer matches it. Returns nil when no prior Read was recorded.
func (s *ReadStateStore) Verify(path string, current []byte) error {
	baseline, ok := s.baseline(path)
	if !ok {
		return nil
	}
	return verifyRecordedHash(path, baseline.hash, current)
}

// VerifyRequired returns an error unless a prior Read recorded a hash for path.
// When a baseline exists, current must still match it.
func (s *ReadStateStore) VerifyRequired(path string, current []byte) error {
	baseline, ok := s.baseline(path)
	if !ok {
		return fmt.Errorf("file must be read before editing: %s; use Read first", path)
	}
	return verifyRecordedHash(path, baseline.hash, current)
}

// VerifyRequiredWithDiff is the strict verification path used by Edit/Write.
// When the complete-file hash is stale, it appends a bounded diff for the
// most recently returned Read page. A hash change outside that page is still
// reported even when the page contents are unchanged.
func (s *ReadStateStore) VerifyRequiredWithDiff(path string, current []byte) error {
	baseline, ok := s.baseline(path)
	if !ok {
		return fmt.Errorf("file must be read before editing: %s; use Read first", path)
	}
	if contentHash(current) == baseline.hash {
		return nil
	}

	err := fmt.Errorf("file has been modified since last read: %s; read it again before editing", path)
	if !baseline.hasPage {
		return err
	}
	currentPage := lineRangeContent(current, baseline.offset, baseline.limit)
	diff := boundedReadDiff(string(baseline.page), currentPage)
	if diff == "" {
		return fmt.Errorf("%w\nread range unchanged (offset=%d, limit=%d); file changed outside the last Read range", err, baseline.offset, baseline.limit)
	}
	return fmt.Errorf("%w\nread-range diff (offset=%d, limit=%d):\n%s", err, baseline.offset, baseline.limit, diff)
}

func (s *ReadStateStore) baseline(path string) (readBaseline, bool) {
	if s == nil {
		return readBaseline{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	baseline, ok := s.states[path]
	baseline.page = append([]byte(nil), baseline.page...)
	return baseline, ok
}

func verifyRecordedHash(path, recorded string, current []byte) error {
	if got := contentHash(current); got != recorded {
		return fmt.Errorf("file has been modified since last read: %s; read it again before editing", path)
	}
	return nil
}

// RecordAfterWrite updates the baseline to the freshly written content so a
// subsequent Edit on the same file does not falsely report stale.
func (s *ReadStateStore) RecordAfterWrite(path string, content []byte) {
	s.Record(path, content)
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func lineRangeContent(content []byte, offset, limit int) string {
	if offset < 0 || limit <= 0 || len(content) == 0 {
		return ""
	}
	lines := bytes.SplitAfter(content, []byte{'\n'})
	if offset >= len(lines) {
		return ""
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}
	return string(bytes.Join(lines[offset:end], nil))
}

func boundedReadDiff(oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	oldLines := strings.SplitAfter(oldContent, "\n")
	newLines := strings.SplitAfter(newContent, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldEnd, newEnd := len(oldLines), len(newLines)
	for oldEnd > prefix && newEnd > prefix && oldLines[oldEnd-1] == newLines[newEnd-1] {
		oldEnd--
		newEnd--
	}

	var out strings.Builder
	out.WriteString("--- last-read\n+++ current\n")
	appendLines := func(prefix byte, lines []string) bool {
		for _, line := range lines {
			candidate := string(prefix) + line
			if out.Len()+len(candidate) > maxReadDiffBytes {
				const marker = "... diff truncated ...\n"
				remaining := maxReadDiffBytes - out.Len()
				if remaining > 0 {
					if remaining > len(marker) {
						remaining = len(marker)
					}
					out.WriteString(marker[:remaining])
				}
				return false
			}
			out.WriteString(candidate)
		}
		return true
	}
	if !appendLines('-', oldLines[prefix:oldEnd]) {
		return out.String()
	}
	appendLines('+', newLines[prefix:newEnd])
	return out.String()
}

func rewriteStaleReadError(operation, display string, err error) error {
	text := err.Error()
	suffix := ""
	if newline := strings.IndexByte(text, '\n'); newline >= 0 {
		suffix = text[newline:]
	}
	return fmt.Errorf("file has been modified since last read: %s; read it again before %s%s", display, operation, suffix)
}
