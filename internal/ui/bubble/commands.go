// 本文件定义 Bubble Tea 中异步执行模型调用、终端命令和动画帧的命令。
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

// runTurnCmd 把一次模型对话运行封装成 Bubble Tea 命令。
func runTurnCmd(ctx context.Context, runner Runner, draft inputDraft) tea.Cmd {
	return func() tea.Msg {
		if runner == nil {
			return turnFinishedMsg{err: fmt.Errorf("runner is nil")}
		}
		var err error
		if richRunner, ok := runner.(RichInputRunner); ok {
			_, err = richRunner.RunRichTurn(ctx, messageFromInputDraft(draft))
		} else {
			_, err = runner.RunTurn(ctx, draft.Text)
		}
		var restoreDraft *inputDraft
		if len(imageTokensInDraft(draft)) > 0 {
			copyDraft := cloneInputDraft(draft)
			restoreDraft = &copyDraft
		}
		return turnFinishedMsg{err: err, restoreDraft: restoreDraft}
	}
}

func imageTokensInDraft(draft inputDraft) []inputToken {
	var images []inputToken
	for _, token := range normalizeInputTokens(draft.Text, draft.Tokens) {
		if token.Kind == inputTokenImage && token.Image != nil {
			images = append(images, token)
		}
	}
	return images
}

// runShellCmd 在 bash 中执行终端命令，并把 stdout、stderr 和错误汇总为消息。
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

// syncRunningFlags keeps legacy view flags in step with QueryGuard.
func (m *appModel) syncRunningFlags() {
	m.running = m.queryGuard.IsRunning()
	m.runningTerminal = m.queryGuard.IsTerminalRunning()
}

func (m appModel) isWorkRunning() bool {
	return m.queryGuard.IsRunning() || m.running
}

func (m appModel) isModelWorkRunning() bool {
	return m.queryGuard.IsModelRunning() || (m.running && !m.runningTerminal)
}

func (m appModel) isTerminalWorkRunning() bool {
	return m.queryGuard.IsTerminalRunning() || (m.running && m.runningTerminal)
}

func (m *appModel) reconcileLegacyRunningState() {
	if m == nil || m.running {
		return
	}
	if m.queryGuard.IsModelRunning() {
		m.queryGuard.FinishModel()
	}
	if m.queryGuard.IsTerminalRunning() {
		m.queryGuard.FinishTerminal()
	}
}

// startNextQueuedTurn starts the oldest queued model turn when completion
// leaves the guard idle and the UI context has not been canceled.
func (m *appModel) startNextQueuedTurn() tea.Cmd {
	if m == nil || !m.queryGuard.CanStartQueued() || m.ctx.Err() != nil {
		return nil
	}
	draft, ok := m.chatQueue.DequeueDraft()
	if !ok {
		return nil
	}
	if !m.queryGuard.StartModel() {
		_ = m.chatQueue.EnqueueDraft(draft)
		return nil
	}
	m.resetStreamingBuffers()
	m.turnStartedAt = time.Now()
	m.syncRunningFlags()
	return runTurnCmd(m.ctx, m.runner, draft)
}

// cursorFrameTick 安排下一次光标动画帧更新。
func cursorFrameTick() tea.Cmd {
	return tea.Tick(cursorFrameInterval, func(t time.Time) tea.Msg {
		return cursorFrameMsg(t)
	})
}

// shellResultBody 把终端命令执行结果转换为适合 transcript 展示的文本。
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
