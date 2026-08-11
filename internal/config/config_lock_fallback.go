//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package config

import "sync"

type fallbackConfigLock struct {
	mu   sync.Mutex
	refs int
}

var fallbackConfigLocks = struct {
	sync.Mutex
	byPath map[string]*fallbackConfigLock
}{byPath: map[string]*fallbackConfigLock{}}

// Platforms without unix.Flock or Windows LockFileEx retain buildability and
// serialize Manager instances within the process. Supported desktop/server
// targets use the cross-process implementations in the platform files.
func acquireConfigWriteLock(configPath string) (func(), error) {
	fallbackConfigLocks.Lock()
	lock := fallbackConfigLocks.byPath[configPath]
	if lock == nil {
		lock = &fallbackConfigLock{}
		fallbackConfigLocks.byPath[configPath] = lock
	}
	lock.refs++
	fallbackConfigLocks.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		fallbackConfigLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(fallbackConfigLocks.byPath, configPath)
		}
		fallbackConfigLocks.Unlock()
	}, nil
}
