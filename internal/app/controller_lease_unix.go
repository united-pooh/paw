//go:build darwin || linux

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockControllerFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errControllerLeaseBusy
	}
	if err != nil {
		return fmt.Errorf("lock controller file: %w", err)
	}
	return nil
}

func unlockControllerFile(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock controller file: %w", err)
	}
	return nil
}

func lockControllerMetadataFile(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock controller metadata file: %w", err)
	}
	return nil
}

func unlockControllerMetadataFile(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock controller metadata file: %w", err)
	}
	return nil
}
