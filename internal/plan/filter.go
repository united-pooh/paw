package plan

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"paw/internal/loop"
)

// ModeFilter builds the tool filter that scopes plan-mode turns to read-only
// workspace access plus Write limited to the plans directory and the
// plan_finalize approval tool. It mirrors the OpenCode plan-agent permission
// boundary: plan mode can inspect but must not mutate business code.
func ModeFilter(plansDir string) loop.ToolFilter {
	readOnly := map[string]bool{
		"Read":          true,
		"Glob":          true,
		"Grep":          true,
		"LS":            true,
		"WebFetch":      true,
		"Select":        true,
		"plan_finalize": true,
	}
	return func(name string, input json.RawMessage) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("tool name is empty")
		}
		if readOnly[name] {
			return nil
		}
		if strings.HasPrefix(name, "codegraph__") {
			return nil
		}
		if name == "Write" {
			if len(input) == 0 || string(input) == "null" {
				// Prompt advertisement: allow; the path is checked at call time.
				return nil
			}
			var payload struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(input, &payload); err != nil {
				return fmt.Errorf("tool not allowed in plan mode: Write input is invalid")
			}
			if err := ensureUnder(payload.FilePath, plansDir); err != nil {
				return fmt.Errorf("tool not allowed in plan mode: Write must target the plans directory: %v", err)
			}
			return nil
		}
		return fmt.Errorf("tool not allowed in plan mode: %s", name)
	}
}

func ensureUnder(path, dir string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(dirAbs, abs)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside %q", path, dir)
	}
	return nil
}
