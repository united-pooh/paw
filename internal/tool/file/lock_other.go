//go:build !darwin && !linux

package file

// withMutationLock 在没有 flock 的平台上为无锁空操作：跨进程写协调依赖平台
// 原语，缺失时并不阻断正常写入（尽力而为）。
func withMutationLock(root string, fn func() error) error {
	return fn()
}