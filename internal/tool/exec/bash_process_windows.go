//go:build windows

package exec

import execpkg "os/exec"

// Windows has no portable equivalent of Unix process groups here. Killing the
// shell is the safest available fallback; the Unix implementation handles
// descendants as a group on supported platforms.
func configureProcessGroup(_ *execpkg.Cmd) {}

func terminateProcessGroup(cmd *execpkg.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
