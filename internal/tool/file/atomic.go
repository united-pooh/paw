package file

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes content to target by first writing a temp file in
// the same directory and renaming it, so a crash never leaves a partially
// written file. Parent directories are created, and mode is applied explicitly.
func atomicWriteFile(target string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return fmt.Errorf("rename temp to %s: %w", target, err)
	}
	return nil
}
