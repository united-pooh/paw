package config

// 沙箱默认值。与 internal/task 侧 worker 兜底默认保持一致（改这里时同步那侧）。
const (
	SandboxDefaultMaxWorkers    = 4
	SandboxDefaultQueueCapacity = 8
	SandboxDefaultCPUSeconds    = 120
	SandboxDefaultFileSizeMiB   = 256
	SandboxDefaultMaxProcesses  = 64
	SandboxDefaultOpenFiles     = 256
	SandboxDefaultJobWallSecs   = 90 * 60

	sandboxPoolMaxWorkersCap    = 32
	sandboxPoolQueueCapacityCap = 64
)

// EffectiveSandbox 是全局安全基线 + 工作区覆盖解析后的生效沙箱参数。
// 只读值，分发给进程池（容量）与 worker 进程（资源限制）。
type EffectiveSandbox struct {
	MaxWorkers     int
	QueueCapacity  int
	CPUSeconds     int
	FileSizeMiB    int
	MaxProcesses   int
	OpenFiles      int
	JobWallSeconds int
}

// ResolveEffectiveSandbox 合并文档层级：全局 sandbox 是安全基线（pool 为硬上限、
// limits 为资源硬 cap），工作区 sandbox 只能调 pool 且被全局上限与 sanity 上限
// 夹住；working 区的 limits 字段在验证层即被拒绝，不会到达这里。
func ResolveEffectiveSandbox(document Document, workspace WorkspaceDocument) EffectiveSandbox {
	effective := EffectiveSandbox{
		MaxWorkers:     SandboxDefaultMaxWorkers,
		QueueCapacity:  SandboxDefaultQueueCapacity,
		CPUSeconds:     SandboxDefaultCPUSeconds,
		FileSizeMiB:    SandboxDefaultFileSizeMiB,
		MaxProcesses:   SandboxDefaultMaxProcesses,
		OpenFiles:      SandboxDefaultOpenFiles,
		JobWallSeconds: SandboxDefaultJobWallSecs,
	}
	var poolCapWorkers, poolCapQueue int
	if document.Sandbox != nil {
		if document.Sandbox.Pool != nil {
			if value := document.Sandbox.Pool.MaxWorkers; value != nil && *value > 0 {
				poolCapWorkers = clampInt(*value, 1, sandboxPoolMaxWorkersCap)
				effective.MaxWorkers = poolCapWorkers
			}
			if value := document.Sandbox.Pool.QueueCapacity; value != nil && *value > 0 {
				poolCapQueue = clampInt(*value, 1, sandboxPoolQueueCapacityCap)
				effective.QueueCapacity = poolCapQueue
			}
		}
		if document.Sandbox.Limits != nil {
			applyLimit(&effective.CPUSeconds, document.Sandbox.Limits.CPUSeconds)
			applyLimit(&effective.FileSizeMiB, document.Sandbox.Limits.FileSizeMiB)
			applyLimit(&effective.MaxProcesses, document.Sandbox.Limits.MaxProcesses)
			applyLimit(&effective.OpenFiles, document.Sandbox.Limits.OpenFiles)
			applyLimit(&effective.JobWallSeconds, document.Sandbox.Limits.JobWallSeconds)
		}
	}
	if workspace.Sandbox != nil && workspace.Sandbox.Pool != nil {
		if value := workspace.Sandbox.Pool.MaxWorkers; value != nil && *value > 0 {
			target := *value
			if poolCapWorkers > 0 && target > poolCapWorkers {
				target = poolCapWorkers
			}
			effective.MaxWorkers = clampInt(target, 1, sandboxPoolMaxWorkersCap)
		}
		if value := workspace.Sandbox.Pool.QueueCapacity; value != nil && *value > 0 {
			target := *value
			if poolCapQueue > 0 && target > poolCapQueue {
				target = poolCapQueue
			}
			effective.QueueCapacity = clampInt(target, 1, sandboxPoolQueueCapacityCap)
		}
	}
	return effective
}

func applyLimit(destination *int, value *int) {
	if value != nil && *value > 0 {
		*destination = *value
	}
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// Sandbox 返回当前快照的生效沙箱参数。
func (s Snapshot) Sandbox() EffectiveSandbox {
	return ResolveEffectiveSandbox(s.Document, s.Workspace)
}
