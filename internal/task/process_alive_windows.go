//go:build windows

package task

// processAlive 在 Windows 上无法用 signal 0 可靠探测进程存活（os.Process.Signal
// 总是成功）。保守返回 true：宁可让孤儿任务保持 running 也不误杀其他实例
// 仍在运行的任务；Windows 上子进程随宿主退出，残留任务由用户通过
// TaskStop 或历史清理处理。
func processAlive(pid int) bool {
	return pid > 0
}
