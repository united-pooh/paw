package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type configCASWriteRequest struct {
	path                  string
	data                  []byte
	mode                  os.FileMode
	expectedGlobal        configFileState
	validateFileStates    func() error
	beforeFinalValidation func() error
}

type configCASWriter func(configCASWriteRequest) error

func atomicWriteNewConfigFile(path string, data []byte, mode os.FileMode) error {
	return atomicWriteConfigFileCAS(configCASWriteRequest{
		path:           path,
		data:           data,
		mode:           mode,
		expectedGlobal: configFileState{},
	})
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".paw-config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	committed = true
	return nil
}

// atomicWriteConfigFileCAS prepares and syncs the replacement before entering
// the serialized commit window. The advisory lock is acquired before the
// in-writer file-state validation and held through atomic replacement, so
// cooperating Paw processes cannot both validate the same global baseline and
// then overwrite one another.
func atomicWriteConfigFileCAS(request configCASWriteRequest) error {
	directory := filepath.Dir(request.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".paw-config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(request.mode); err != nil {
		return err
	}
	if _, err := temporary.Write(request.data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	// This hook is intentionally after temp write+sync and before the final
	// validation. Production leaves it nil; transaction tests use it to place
	// an external edit in the former TOCTOU window deterministically.
	if request.beforeFinalValidation != nil {
		if err := request.beforeFinalValidation(); err != nil {
			return err
		}
	}

	unlock, err := acquireConfigWriteLock(request.path)
	if err != nil {
		return fmt.Errorf("lock config %s: %w", request.path, err)
	}
	defer unlock()

	currentGlobal, err := readConfigFileState(request.path)
	if err != nil {
		return fmt.Errorf("%w: could not verify global configuration before replacement: %v", ErrRevisionConflict, err)
	}
	if !sameConfigFileState(currentGlobal, request.expectedGlobal) {
		return fmt.Errorf("%w: global configuration changed outside the manager; reload and retry", ErrRevisionConflict)
	}
	if request.validateFileStates != nil {
		if err := request.validateFileStates(); err != nil {
			return err
		}
	}
	if err := replaceFile(temporaryPath, request.path); err != nil {
		return fmt.Errorf("replace %s: %w", request.path, err)
	}
	committed = true
	return nil
}
