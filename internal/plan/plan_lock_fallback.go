//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package plan

import "sync"

type fallbackPlanLock struct {
	mu   sync.Mutex
	refs int
}

var fallbackPlanLocks = struct {
	sync.Mutex
	byPath map[string]*fallbackPlanLock
}{byPath: map[string]*fallbackPlanLock{}}

// Platforms without unix.Flock or Windows LockFileEx retain buildability and
// serialize projection writers within the process. Supported desktop/server
// targets use the cross-process implementations in the platform files.
func acquirePlanProjectionLock(path string) (func(), error) {
	fallbackPlanLocks.Lock()
	lock := fallbackPlanLocks.byPath[path]
	if lock == nil {
		lock = &fallbackPlanLock{}
		fallbackPlanLocks.byPath[path] = lock
	}
	lock.refs++
	fallbackPlanLocks.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		fallbackPlanLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(fallbackPlanLocks.byPath, path)
		}
		fallbackPlanLocks.Unlock()
	}, nil
}
