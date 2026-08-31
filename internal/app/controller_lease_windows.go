//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const controllerOwnerLockOffsetHigh = uint32(0x80000000)

func lockControllerFile(file *os.File) error {
	return lockWindowsControllerRegion(file, controllerOwnerLockOffsetHigh, true, "controller file")
}

func unlockControllerFile(file *os.File) error {
	return unlockWindowsControllerRegion(file, controllerOwnerLockOffsetHigh, "controller file")
}

func lockControllerMetadataFile(file *os.File) error {
	return lockWindowsControllerRegion(file, 0, false, "controller metadata file")
}

func unlockControllerMetadataFile(file *os.File) error {
	return unlockWindowsControllerRegion(file, 0, "controller metadata file")
}

func lockWindowsControllerRegion(file *os.File, offsetHigh uint32, failImmediately bool, name string) error {
	var flags uint32 = windows.LOCKFILE_EXCLUSIVE_LOCK
	if failImmediately {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	var overlapped windows.Overlapped
	overlapped.OffsetHigh = offsetHigh
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlapped)
	if failImmediately && errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errControllerLeaseBusy
	}
	if err != nil {
		return fmt.Errorf("lock %s: %w", name, err)
	}
	return nil
}

func unlockWindowsControllerRegion(file *os.File, offsetHigh uint32, name string) error {
	var overlapped windows.Overlapped
	overlapped.OffsetHigh = offsetHigh
	if err := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("unlock %s: %w", name, err)
	}
	return nil
}
