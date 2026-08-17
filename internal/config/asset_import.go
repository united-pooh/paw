package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// copyLegacyAssets preserves the one-time import of non-model assets from the
// historical ~/.paw directory. It deliberately never reads config.json; model
// configuration is exclusively config.jsonc.
func copyLegacyAssets(paths Paths) []Diagnostic {
	legacyHome := strings.TrimSpace(paths.LegacyAssetsHome)
	if legacyHome == "" {
		return nil
	}
	var diagnostics []Diagnostic
	assets := []struct {
		source, destination string
		directory           bool
	}{
		{filepath.Join(legacyHome, "settings.json"), paths.Settings, false},
		{filepath.Join(legacyHome, "mcp.toml"), paths.MCP, false},
		{filepath.Join(legacyHome, "skills"), paths.Skills, true},
	}
	for _, asset := range assets {
		if _, err := os.Stat(asset.destination); err == nil && !asset.directory {
			continue
		}
		if _, err := os.Stat(asset.source); err != nil {
			continue
		}
		var err error
		if asset.directory {
			err = copyDirectory(asset.source, asset.destination)
		} else {
			err = copyFileExclusive(asset.source, asset.destination)
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: asset.source, Message: "legacy asset was not copied: " + err.Error()})
		}
	}
	return diagnostics
}

func copyFileExclusive(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	ok = true
	return output.Close()
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		return copyFileExclusive(path, target)
	})
}
