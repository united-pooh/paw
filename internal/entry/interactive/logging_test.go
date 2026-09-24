package interactive

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"paw/internal/platform/pawpath"
)

func TestDiagnosticLogsStayOffTerminalAndRestoreOnExit(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	projectDir, err := pawpath.ProjectDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "logs", "tui.log")
	previous := log.Writer()
	var terminal bytes.Buffer
	log.SetOutput(&terminal)
	t.Cleanup(func() { log.SetOutput(previous) })

	for run := 0; run < 2; run++ {
		restore, err := redirectDiagnosticLogs(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(restore)
		var workers sync.WaitGroup
		for worker := 0; worker < 16; worker++ {
			workers.Go(func() {
				log.Printf("tool history repair on session load: run=%d worker=%d", run, worker)
			})
		}
		workers.Wait()
		if terminal.Len() != 0 {
			t.Fatalf("diagnostic logs reached terminal: %q", terminal.String())
		}
		restore()
		if log.Writer() != &terminal {
			t.Fatal("exit did not restore original log writer")
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "tool history repair on session load:"); got != 32 {
		t.Fatalf("diagnostic log contains %d records, want all 32 records across both runs", got)
	}
	log.Print("after TUI exit")
	if !strings.Contains(terminal.String(), "after TUI exit") {
		t.Fatalf("post-exit logs did not return to original writer: %q", terminal.String())
	}
}

func TestDiagnosticLogOpenFailureKeepsOriginalWriter(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	projectDir, err := pawpath.ProjectDir(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "logs", "tui.log")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	previous := log.Writer()
	restore, err := redirectDiagnosticLogs(root)
	if err == nil {
		restore()
		t.Fatal("redirectDiagnosticLogs() succeeded with a directory in place of the log file")
	}
	if !strings.Contains(err.Error(), "open TUI diagnostic log") {
		t.Fatalf("error = %v, want file open context", err)
	}
	if log.Writer() != previous {
		t.Fatal("failed log open changed the original log writer")
	}
}

func TestRunDiagnosticLogFailureLeavesTerminalAndLoggerUntouched(t *testing.T) {
	home := filepath.Join(t.TempDir(), "blocked-home")
	if err := os.WriteFile(home, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAW_CONFIG_HOME", home)

	terminal, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	previousOutput := os.Stdout
	previousLogger := log.Writer()
	var diagnostics bytes.Buffer
	os.Stdout = terminal
	log.SetOutput(&diagnostics)
	t.Cleanup(func() {
		os.Stdout = previousOutput
		log.SetOutput(previousLogger)
		_ = terminal.Close()
	})

	err = Run(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "TUI diagnostic log") {
		t.Errorf("Run() error = %v, want clear diagnostic log setup failure", err)
	}
	if log.Writer() != &diagnostics {
		t.Error("Run() changed logger despite setup failure")
	}
	written, err := os.ReadFile(terminal.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("Run() wrote %q to terminal before diagnostic logging was ready", written)
	}
}
