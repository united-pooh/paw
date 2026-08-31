//go:build !darwin && !linux && !windows

package app

import "os"

func lockControllerFile(*os.File) error {
	return ErrControllerLockUnsupported
}

func unlockControllerFile(*os.File) error {
	return nil
}

func lockControllerMetadataFile(*os.File) error {
	return ErrControllerLockUnsupported
}

func unlockControllerMetadataFile(*os.File) error {
	return nil
}
