package interactive

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"paw/internal/platform/pawpath"
)

func redirectDiagnosticLogs(root string) (func(), error) {
	projectDir, err := pawpath.ProjectDir(root)
	if err != nil {
		return nil, fmt.Errorf("resolve TUI diagnostic log directory: %w", err)
	}
	dir := filepath.Join(projectDir, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create TUI diagnostic log directory %q: %w", dir, err)
	}
	path := filepath.Join(dir, "tui.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open TUI diagnostic log %q: %w", path, err)
	}
	previous := log.Writer()
	// Standard log output must not move the terminal cursor behind Bubble Tea.
	log.SetOutput(file)
	return sync.OnceFunc(func() {
		log.SetOutput(previous)
		_ = file.Close()
	}), nil
}
