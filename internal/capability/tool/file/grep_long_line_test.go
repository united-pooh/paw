package file

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepContinuesAfterOversizedLines(t *testing.T) {
	root := t.TempDir()
	oversized := "needle " + strings.Repeat("x", 1024*1024)
	for path, text := range map[string]string{
		"a.txt": "needle before\n" + oversized + "\nneedle after\n" + oversized,
		"b.txt": "needle other file\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := (&GrepTool{Root: root}).Run(context.Background(), json.RawMessage(`{"pattern":"needle","literal":true}`))
	if err != nil {
		t.Fatalf("Run() = %v; oversized generated lines must not discard all results", err)
	}
	for _, want := range []string{"a.txt:1:needle before", "a.txt:3:needle after", "b.txt:1:needle other file", "a.txt:2", "a.txt:4", "1048576", "incomplete"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q: %s", want, output)
		}
	}
	if strings.Contains(output, "needle xxxx") || len(output) > 2048 {
		t.Fatalf("oversized line leaked into matches (%d bytes)", len(output))
	}
}

type cancelGrepReader struct {
	*strings.Reader
	cancel context.CancelFunc
}

func (r cancelGrepReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestGrepOversizedLineReadCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := bufio.NewReaderSize(cancelGrepReader{Reader: strings.NewReader(strings.Repeat("x", 2*1024*1024)), cancel: cancel}, 64*1024)
	_, _, err := readGrepLine(ctx, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readGrepLine() = %v, want cancellation between fragments", err)
	}
}

func TestGrepOversizedLineWarningsAreBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "generated.map"), []byte(strings.Repeat(strings.Repeat("x", 1024*1024+1)+"\n", 13)), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := (&GrepTool{Root: root}).Run(context.Background(), json.RawMessage(`{"pattern":"never matches","literal":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "13 oversized") || !strings.Contains(output, "3 additional") || strings.Count(output, "generated.map:") != 10 || len(output) > 2048 {
		t.Fatalf("unbounded or incomplete warning: %s", output)
	}
}

func TestGrepLineBoundaryAndResultLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("x", 1024*1024+1) + "\n" + strings.Repeat("y", 1024*1024) + "\nlast match\n"
	if err := os.WriteFile(filepath.Join(root, "boundary.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := (&GrepTool{Root: root}).Run(context.Background(), json.RawMessage(`{"pattern":"^(y+|last match)$","max_results":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "boundary.txt:2:"+strings.Repeat("y", 1024*1024)) || strings.Contains(output, "last match") || !strings.Contains(output, "1 oversized") {
		t.Fatalf("boundary/result budget incorrect (%d bytes)", len(output))
	}
}
