package bubble

import (
	"bytes"
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os/exec"
	"strings"
	"time"
)

func runTurnCmd(ctx context.Context, runner Runner, input string) tea.Cmd {
	return func() tea.Msg {
		if runner == nil {
			return turnFinishedMsg{err: fmt.Errorf("runner is nil")}
		}
		_, err := runner.RunTurn(ctx, input)
		return turnFinishedMsg{err: err}
	}
}

func runShellCmd(ctx context.Context, command string) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "bash", "-lc", command)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return shellFinishedMsg{
			command: command,
			stdout:  stdout.String(),
			stderr:  stderr.String(),
			err:     err,
		}
	}
}

func cursorFrameTick() tea.Cmd {
	return tea.Tick(cursorFrameInterval, func(t time.Time) tea.Msg {
		return cursorFrameMsg(t)
	})
}

func shellResultBody(msg shellFinishedMsg) string {
	var parts []string
	if stdout := strings.TrimRight(msg.stdout, "\n"); stdout != "" {
		parts = append(parts, stdout)
	}
	if stderr := strings.TrimRight(msg.stderr, "\n"); stderr != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	if msg.err != nil {
		parts = append(parts, "error: "+msg.err.Error())
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n")
}
