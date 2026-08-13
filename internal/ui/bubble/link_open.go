package bubble

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var openTerminalURL = openTerminalURLInBrowser

// openTerminalURLCmd 为可点击目标生成打开命令：网页 URL 交给浏览器，
// 本地绝对路径 / file:// URL 交给系统默认程序（如文本编辑器）打开文件。
// 与输出网页链接一致，点击 transcript 中的文件引用即可打开对应文件。
func openTerminalURLCmd(target string) tea.Cmd {
	target = strings.TrimSpace(target)
	if !isClickableTerminalTarget(target) {
		return nil
	}
	return func() tea.Msg {
		_ = openTerminalURL(terminalOpenPath(target))
		return nil
	}
}

// terminalOpenPath 把可点击目标转成系统打开命令使用的本地路径：
// file:// URL 解出文件系统路径（含 %xx 转义还原）；其余目标原样返回。
func terminalOpenPath(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "file" {
		return target
	}
	path := parsed.Path
	if path == "" {
		path = parsed.Opaque
	}
	if path == "" {
		return target
	}
	if unescaped, err := url.PathUnescape(path); err == nil {
		path = unescaped
	}
	return path
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
