package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const migrationMarkerName = ".migration-v2.json"

type rawMCPConfig struct {
	Servers map[string]map[string]any `toml:"mcp_servers"`
}

type migrationMarker struct {
	MigratedAt    string `json:"migratedAt"`
	SchemaVersion int    `json:"schemaVersion"`
	Source        string `json:"source"`
}

// copyLegacyAssets migrates configuration into the active Paw directory. The
// historical ~/.paw files are copied only when the active directory differs;
// an older platform-specific v2 directory is merged with ~/.paw, preserving
// the active directory's values on conflicts. The v2 migration is guarded by
// a marker so a later startup cannot re-merge changed legacy files.
func copyLegacyAssets(paths Paths) []Diagnostic {
	var diagnostics []Diagnostic
	if legacyHome := strings.TrimSpace(paths.LegacyAssetsHome); legacyHome != "" && !sameConfigDirectoryPath(legacyHome, paths.Home) {
		diagnostics = append(diagnostics, copyLegacyUserAssets(legacyHome, paths)...)
	}
	if legacyV2Home := strings.TrimSpace(paths.LegacyV2Home); legacyV2Home != "" && !sameConfigDirectoryPath(legacyV2Home, paths.Home) && legacyV2AssetsPresent(legacyV2Home) {
		markerPath := filepath.Join(paths.Home, migrationMarkerName)
		markerInfo, markerErr := os.Stat(markerPath)
		switch {
		case markerErr == nil && !markerInfo.IsDir():
			// The one-time migration has completed.
		case markerErr == nil && markerInfo.IsDir():
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: markerPath, Message: "legacy v2 migration marker is a directory"})
		case os.IsNotExist(markerErr):
			migrationDiagnostics := migrateLegacyV2Assets(legacyV2Home, paths)
			diagnostics = append(diagnostics, migrationDiagnostics...)
			if len(migrationDiagnostics) == 0 {
				if err := writeMigrationMarker(markerPath, legacyV2Home); err != nil {
					diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: markerPath, Message: "legacy v2 migration marker was not written: " + err.Error()})
				}
			}
		default:
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: markerPath, Message: "legacy v2 migration marker could not be inspected: " + markerErr.Error()})
		}
	}
	return diagnostics
}

func legacyV2AssetsPresent(sourceRoot string) bool {
	for _, name := range []string{
		"config.jsonc",
		"config-v1.backup.json",
		"model-discovery-cache.json",
		"settings.json",
		"mcp.toml",
	} {
		if _, err := os.Stat(filepath.Join(sourceRoot, name)); err == nil {
			return true
		}
	}
	return directoryExists(filepath.Join(sourceRoot, "skills"))
}

func writeMigrationMarker(path, source string) error {
	marker := migrationMarker{
		MigratedAt:    time.Now().UTC().Format(time.RFC3339),
		SchemaVersion: 2,
		Source:        source,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	committed = true
	return nil
}

func copyLegacyUserAssets(legacyHome string, paths Paths) []Diagnostic {
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

func migrateLegacyV2Assets(sourceRoot string, paths Paths) []Diagnostic {
	var diagnostics []Diagnostic

	copyIfMissing := func(source, destination string) {
		if _, err := os.Stat(destination); err == nil {
			return
		}
		if _, err := os.Stat(source); err != nil {
			return
		}
		if err := copyFileExclusive(source, destination); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: source, Message: "legacy v2 asset was not copied: " + err.Error()})
		}
	}

	copyIfMissing(filepath.Join(sourceRoot, "config.jsonc"), paths.GlobalConfig)
	copyIfMissing(filepath.Join(sourceRoot, "config-v1.backup.json"), filepath.Join(paths.Home, "config-v1.backup.json"))
	copyIfMissing(filepath.Join(sourceRoot, "model-discovery-cache.json"), paths.ModelDiscoveryCache)

	mergeIfBoth := func(source, destination string, merge func(string, string, string) error) {
		sourceInfo, sourceErr := os.Stat(source)
		if sourceErr != nil || sourceInfo.IsDir() {
			return
		}
		destinationInfo, destinationErr := os.Stat(destination)
		if destinationErr != nil {
			if os.IsNotExist(destinationErr) {
				if err := copyFileExclusive(source, destination); err != nil {
					diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: source, Message: "legacy v2 asset was not copied: " + err.Error()})
				}
			}
			return
		}
		if destinationInfo.IsDir() {
			return
		}
		if err := merge(source, destination, destination); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: source, Message: "legacy v2 asset was not merged: " + err.Error()})
		}
	}

	mergeIfBoth(filepath.Join(sourceRoot, "settings.json"), paths.Settings, mergeJSONConfigFiles)
	mergeIfBoth(filepath.Join(sourceRoot, "mcp.toml"), paths.MCP, mergeMCPConfigFiles)

	if sourceSkills := filepath.Join(sourceRoot, "skills"); directoryExists(sourceSkills) {
		if err := copyDirectory(sourceSkills, paths.Skills); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Severity: "warning", File: sourceSkills, Message: "legacy v2 skills were not copied: " + err.Error()})
		}
	}
	return diagnostics
}

func mergeJSONConfigFiles(source, preferred, destination string) error {
	sourceRaw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	preferredRaw, err := os.ReadFile(preferred)
	if err != nil {
		return err
	}
	sourceObject, err := DecodeJSONObject(sourceRaw)
	if err != nil {
		return fmt.Errorf("parse source JSONC: %w", err)
	}
	preferredObject, err := DecodeJSONObject(preferredRaw)
	if err != nil {
		return fmt.Errorf("parse preferred JSON: %w", err)
	}
	merged := mergeJSONObjectPreferLeft(preferredObject, sourceObject)
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(destination, data, 0o600)
}

func mergeJSONObjectPreferLeft(preferred, supplement map[string]any) map[string]any {
	merged := make(map[string]any, len(preferred)+len(supplement))
	for key, value := range preferred {
		merged[key] = value
	}
	for key, value := range supplement {
		current, exists := merged[key]
		if !exists {
			merged[key] = value
			continue
		}
		preferredObject, preferredOK := current.(map[string]any)
		supplementObject, supplementOK := value.(map[string]any)
		if preferredOK && supplementOK {
			merged[key] = mergeJSONObjectPreferLeft(preferredObject, supplementObject)
		}
	}
	return merged
}

func mergeMCPConfigFiles(source, preferred, destination string) error {
	sourceConfig, err := readMCPConfig(source)
	if err != nil {
		return fmt.Errorf("parse source TOML: %w", err)
	}
	preferredConfig, err := readMCPConfig(preferred)
	if err != nil {
		return fmt.Errorf("parse preferred TOML: %w", err)
	}
	if preferredConfig.Servers == nil {
		preferredConfig.Servers = map[string]map[string]any{}
	}
	for name, server := range sourceConfig.Servers {
		if _, exists := preferredConfig.Servers[name]; !exists {
			preferredConfig.Servers[name] = server
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	var encoded strings.Builder
	if err := toml.NewEncoder(&encoded).Encode(preferredConfig); err != nil {
		return err
	}
	return atomicWriteFile(destination, []byte(encoded.String()), 0o600)
}

func readMCPConfig(path string) (rawMCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return rawMCPConfig{}, err
	}
	config := rawMCPConfig{}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return rawMCPConfig{}, err
	}
	return config, nil
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func sameConfigDirectoryPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	return left != "." && left == right
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
