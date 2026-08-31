package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	controllerLockFileName         = "controller.lock"
	controllerMetadataLockFileName = "controller.lock.meta"
)

var (
	ErrWorkspaceLocked           = errors.New("workspace is controlled by another process")
	ErrControllerLockUnsupported = errors.New("controller lock is unsupported on this platform")
	errControllerLeaseBusy       = errors.New("controller lease is busy")
)

type ControllerMode string

const (
	ControllerModeTUI ControllerMode = "tui"
	ControllerModeWeb ControllerMode = "web"
)

type controllerLeaseOwner struct {
	PID        int            `json:"pid"`
	InstanceID string         `json:"instance_id"`
	Mode       ControllerMode `json:"mode"`
	StartedAt  time.Time      `json:"started_at"`
}

type WorkspaceLockedError struct {
	OwnerPID        int
	OwnerInstanceID string
	OwnerMode       ControllerMode
}

func (e *WorkspaceLockedError) Error() string {
	if e == nil {
		return ErrWorkspaceLocked.Error()
	}
	if e.OwnerPID == 0 {
		return ErrWorkspaceLocked.Error()
	}
	return fmt.Sprintf("%s: pid=%d mode=%s", ErrWorkspaceLocked, e.OwnerPID, e.OwnerMode)
}

func (*WorkspaceLockedError) Unwrap() error {
	return ErrWorkspaceLocked
}

type ControllerLease struct {
	file       *os.File
	instanceID string
	closeOnce  sync.Once
	closeErr   error
}

func AcquireControllerLease(storeRoot string, mode ControllerMode) (*ControllerLease, error) {
	if mode != ControllerModeTUI && mode != ControllerModeWeb {
		return nil, fmt.Errorf("invalid controller mode %q", mode)
	}
	if err := os.MkdirAll(storeRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create controller store root: %w", err)
	}

	metadataFile, err := openControllerLockFile(filepath.Join(storeRoot, controllerMetadataLockFileName))
	if err != nil {
		return nil, fmt.Errorf("open controller metadata lock: %w", err)
	}
	if err := lockControllerMetadataFile(metadataFile); err != nil {
		_ = metadataFile.Close()
		return nil, err
	}
	metadataHeld := true
	defer func() {
		if metadataHeld {
			_ = closeControllerMetadataLock(metadataFile)
		}
	}()

	file, err := openControllerLockFile(filepath.Join(storeRoot, controllerLockFileName))
	if err != nil {
		return nil, fmt.Errorf("open controller lock: %w", err)
	}
	if err := lockControllerFile(file); err != nil {
		if errors.Is(err, errControllerLeaseBusy) {
			owner := readControllerLeaseOwner(file)
			_ = file.Close()
			if releaseErr := closeControllerMetadataLock(metadataFile); releaseErr != nil {
				return nil, releaseErr
			}
			metadataHeld = false
			return nil, &WorkspaceLockedError{
				OwnerPID:        owner.PID,
				OwnerInstanceID: owner.InstanceID,
				OwnerMode:       owner.Mode,
			}
		}
		_ = file.Close()
		return nil, err
	}

	instanceID, err := newControllerInstanceID()
	if err != nil {
		_ = unlockControllerFile(file)
		_ = file.Close()
		return nil, err
	}
	owner := controllerLeaseOwner{
		PID:        os.Getpid(),
		InstanceID: instanceID,
		Mode:       mode,
		StartedAt:  time.Now().UTC(),
	}
	if err := writeControllerLeaseOwner(file, owner); err != nil {
		_ = unlockControllerFile(file)
		_ = file.Close()
		return nil, err
	}
	if err := closeControllerMetadataLock(metadataFile); err != nil {
		_ = unlockControllerFile(file)
		_ = file.Close()
		return nil, err
	}
	metadataHeld = false
	return &ControllerLease{file: file, instanceID: instanceID}, nil
}

func (l *ControllerLease) InstanceID() string {
	if l == nil {
		return ""
	}
	return l.instanceID
}

func (l *ControllerLease) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.file == nil {
			return
		}
		l.closeErr = errors.Join(unlockControllerFile(l.file), l.file.Close())
	})
	return l.closeErr
}

func openControllerLockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func closeControllerMetadataLock(file *os.File) error {
	return errors.Join(unlockControllerMetadataFile(file), file.Close())
}

func newControllerInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate controller instance ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func writeControllerLeaseOwner(file *os.File, owner controllerLeaseOwner) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("encode controller lock diagnostics: %w", err)
	}
	data = append(data, '\n')
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate controller lock diagnostics: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek controller lock diagnostics: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write controller lock diagnostics: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync controller lock diagnostics: %w", err)
	}
	return nil
}

func readControllerLeaseOwner(file *os.File) controllerLeaseOwner {
	if _, err := file.Seek(0, 0); err != nil {
		return controllerLeaseOwner{}
	}
	var owner controllerLeaseOwner
	if err := json.NewDecoder(file).Decode(&owner); err != nil {
		return controllerLeaseOwner{}
	}
	return owner
}
