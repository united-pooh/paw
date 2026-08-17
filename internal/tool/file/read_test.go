package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadToolInputSchemaIncludesOffsetAndLimit(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Type    string `json:"type"`
			Minimum int    `json:"minimum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal((&ReadTool{}).InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file_path", "offset", "limit"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("schema missing %q: %#v", name, schema.Properties)
		}
	}
	if schema.Properties["offset"].Type != "integer" || schema.Properties["offset"].Minimum != 0 {
		t.Fatalf("offset schema = %#v", schema.Properties["offset"])
	}
	if schema.Properties["limit"].Type != "integer" || schema.Properties["limit"].Minimum != 1 {
		t.Fatalf("limit schema = %#v", schema.Properties["limit"])
	}
	if len(schema.Required) != 1 || schema.Required[0] != "file_path" {
		t.Fatalf("required = %#v, want only file_path", schema.Required)
	}
}

func TestReadToolUsesZeroBasedOffsetAndDefaultLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	content := "line-0\nline-1\nline-2\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadTool{Root: root}
	got, err := tool.Run(context.Background(), []byte(`{"file_path":"sample.txt","offset":1,"limit":1}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.HasPrefix(got, "line-1\n") || !strings.Contains(got, "next_offset=2") {
		t.Fatalf("output = %q, want line-1 and next offset", got)
	}

	got, err = tool.Run(context.Background(), []byte(`{"file_path":"sample.txt"}`))
	if err != nil {
		t.Fatalf("default Run() error = %v", err)
	}
	if got != content {
		t.Fatalf("default output = %q, want full short file", got)
	}
}

func TestReadToolReturnsPartialMarkerAtLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "many.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := (&ReadTool{Root: root}).Run(context.Background(), []byte(`{"file_path":"many.txt","limit":2}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.HasPrefix(got, "a\nb\n") || !strings.Contains(got, "[Read partial: offset=0, returned_lines=2, next_offset=2, reason=limit]") {
		t.Fatalf("output = %q, want two lines and partial marker", got)
	}
}

func TestReadToolPreservesCRLFAndUnterminatedLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mixed.txt")
	content := "一\r\n二\n三"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := (&ReadTool{Root: root}).Run(context.Background(), []byte(`{"file_path":"mixed.txt","offset":1,"limit":2}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "二\n三" {
		t.Fatalf("output = %q, want preserved physical lines", got)
	}
}

func TestReadToolEnforcesByteLimitAtLineBoundary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	content := strings.Repeat("x\n", maxReadResultBytes/2)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := (&ReadTool{Root: root}).Run(context.Background(), []byte(`{"file_path":"large.txt","limit":1000000}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len([]byte(got)) > maxReadResultBytes {
		t.Fatalf("output bytes = %d, want <= %d", len([]byte(got)), maxReadResultBytes)
	}
	if !strings.Contains(got, "reason=bytes") {
		tailStart := len(got) - 160
		if tailStart < 0 {
			tailStart = 0
		}
		t.Fatalf("output = %q, want byte-limit marker", got[tailStart:])
	}
}

func TestReadToolByteLimitCountsUTF8Bytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unicode.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("界\n", maxReadContentBytes/len([]byte("界\n"))+100)), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := (&ReadTool{Root: root}).Run(context.Background(), []byte(`{"file_path":"unicode.txt","limit":1000000}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len([]byte(got)) > maxReadResultBytes || !utf8.ValidString(got) {
		t.Fatalf("output bytes=%d valid_utf8=%v, want bounded valid UTF-8", len([]byte(got)), utf8.ValidString(got))
	}
}

func TestReadToolRejectsOversizedSelectedLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxReadResultBytes)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&ReadTool{Root: root}).Run(context.Background(), []byte(`{"file_path":"long.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "single line exceeds Read byte limit") {
		t.Fatalf("err = %v, want oversized-line error", err)
	}
}

func TestReadToolRejectsInvalidRangeAndAllowsEOF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tiny.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{Root: root}

	for _, input := range []string{
		`{"file_path":"tiny.txt","offset":-1}`,
		`{"file_path":"tiny.txt","limit":0}`,
	} {
		if _, err := tool.Run(context.Background(), []byte(input)); err == nil {
			t.Fatalf("input %s unexpectedly succeeded", input)
		}
	}
	if got, err := tool.Run(context.Background(), []byte(`{"file_path":"tiny.txt","offset":2}`)); err != nil || got != "" {
		t.Fatalf("EOF read = %q, %v; want empty success", got, err)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"file_path":"tiny.txt","offset":3}`)); err == nil || !strings.Contains(err.Error(), "offset 3 beyond EOF") {
		t.Fatalf("out-of-range err = %v", err)
	}
}

func TestPartialReadBaselineAllowsEditWhenHashUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("old\nother\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewReadStateStore()
	read := &ReadTool{Root: root, ReadState: state}
	if _, err := read.Run(context.Background(), []byte(`{"file_path":"edit.txt","limit":1}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := (&EditTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"edit.txt","old_string":"old","new_string":"new"}`)); err != nil {
		t.Fatalf("Edit after unchanged partial Read = %v", err)
	}
}

func TestLatestPartialReadReplacesPreviousPageBaseline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pages.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewReadStateStore()
	read := &ReadTool{Root: root, ReadState: state}
	if _, err := read.Run(context.Background(), []byte(`{"file_path":"pages.txt","offset":0,"limit":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Run(context.Background(), []byte(`{"file_path":"pages.txt","offset":1,"limit":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed-first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&EditTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"pages.txt","old_string":"second","new_string":"updated"}`))
	if err == nil || !strings.Contains(err.Error(), "file changed outside the last Read range") {
		t.Fatalf("err = %v, want latest offset=1 page baseline", err)
	}
}

func TestPartialReadStaleEditIncludesPageDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("old\nkeep\noutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewReadStateStore()
	read := &ReadTool{Root: root, ReadState: state}
	if _, err := read.Run(context.Background(), []byte(`{"file_path":"edit.txt","limit":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\nkeep\noutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&EditTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"edit.txt","old_string":"old","new_string":"updated"}`))
	if err == nil || !strings.Contains(err.Error(), "read-range diff") || !strings.Contains(err.Error(), "-old") || !strings.Contains(err.Error(), "+new") {
		t.Fatalf("err = %v, want bounded page diff", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "new\nkeep\noutside\n" {
		t.Fatalf("file changed after stale Edit: %q", got)
	}
}

func TestPartialReadStaleEditReportsChangesOutsidePage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("old\nkeep\noutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewReadStateStore()
	if _, err := (&ReadTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"edit.txt","limit":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\nkeep\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&EditTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"edit.txt","old_string":"old","new_string":"updated"}`))
	if err == nil || !strings.Contains(err.Error(), "file changed outside the last Read range") {
		t.Fatalf("err = %v, want outside-range stale error", err)
	}
}

func TestPartialReadStaleWriteIncludesPageDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "write.txt")
	if err := os.WriteFile(path, []byte("old\nkeep\noutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewReadStateStore()
	if _, err := (&ReadTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"write.txt","limit":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\nkeep\noutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := (&WriteTool{Root: root, ReadState: state}).Run(context.Background(), []byte(`{"file_path":"write.txt","content":"replacement\n"}`))
	if err == nil || !strings.Contains(err.Error(), "read-range diff") || !strings.Contains(err.Error(), "-old") || !strings.Contains(err.Error(), "+new") {
		t.Fatalf("err = %v, want bounded page diff", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "new\nkeep\noutside\n" {
		t.Fatalf("file changed after stale Write: %q", got)
	}
}
