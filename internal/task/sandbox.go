package task

// SandboxLimits 是 worker 进程资源上限的数值。任何字段 <=0 时回落默认值。
// 该结构经 --sandbox-limits 标志从宿主（config 生效值）传入 worker 进程。
type SandboxLimits struct {
	CPUSeconds     int
	FileSizeMiB    int
	MaxProcesses   int
	OpenFiles      int
	JobWallSeconds int
}

// 默认值与 internal/config 的 SandboxDefault* 保持一致（改这里同步那侧）。
const (
	defaultWorkerCPUSeconds   = 120
	defaultWorkerFileSizeMiB  = 256
	defaultWorkerMaxProcesses = 64
	defaultWorkerOpenFiles    = 256
	defaultWorkerJobWallSecs  = 600
)

func resolveSandboxLimits(limits SandboxLimits) SandboxLimits {
	if limits.CPUSeconds <= 0 {
		limits.CPUSeconds = defaultWorkerCPUSeconds
	}
	if limits.FileSizeMiB <= 0 {
		limits.FileSizeMiB = defaultWorkerFileSizeMiB
	}
	if limits.MaxProcesses <= 0 {
		limits.MaxProcesses = defaultWorkerMaxProcesses
	}
	if limits.OpenFiles <= 0 {
		limits.OpenFiles = defaultWorkerOpenFiles
	}
	if limits.JobWallSeconds <= 0 {
		limits.JobWallSeconds = defaultWorkerJobWallSecs
	}
	return limits
}
