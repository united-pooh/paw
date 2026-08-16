//go:build !darwin && !linux

package subagent

// ApplyWorkerResourceLimits 在没有 POSIX rlimit 的平台上为尽力而为的空操作：
// 资源限制依赖平台原语，缺失时不影响 worker 正常工作。
func ApplyWorkerResourceLimits(SandboxLimits) error {
	return nil
}
