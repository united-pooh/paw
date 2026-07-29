package bubble

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var openTerminalURL = openTerminalURLInBrowser

func openTerminalURLCmd(target string) tea.Cmd {
	target = strings.TrimSpace(target)
	if !isClickableTerminalURL(target) {
		return nil
	}
	return func() tea.Msg {
		_ = openTerminalURL(target)
		return nil
	}
}

func openTerminalURLInBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open URL: %w", err)
	}
	go func() {
		_ = command.Wait()
	}()
	return nil
}
