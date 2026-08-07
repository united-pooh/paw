//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package exec

import (
	execpkg "os/exec"
	"syscall"
)

func configureProcessGroup(cmd *execpkg.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(cmd *execpkg.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
