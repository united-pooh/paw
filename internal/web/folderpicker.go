package web

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// pickFolder 打开操作系统原生文件夹选择对话框并返回所选路径；
// 用户取消选择时返回 ("", nil)。定义为变量以便测试替换。
var pickFolder = pickFolderNative

// isCancelExit 判断原生源选择框进程退出码是否表示“用户取消”。
func isCancelExit(err error, codes ...int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	for _, code := range codes {
		if exitErr.ExitCode() == code {
			return true
		}
	}
	return false
}

func pickFolderNative() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "选择工作区文件夹")`).Output()
		if err != nil {
			if isCancelExit(err, 1) {
				return "", nil // 用户在系统对话框中取消
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil

	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = '选择工作区文件夹'; if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $d.SelectedPath }`
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil

	default: // linux 及其他：优先 zenity，其次 kdialog
		if path, lookErr := exec.LookPath("zenity"); lookErr == nil && path != "" {
			out, err := exec.Command("zenity", "--file-selection", "--directory", "--title=选择工作区文件夹").Output()
			if err != nil {
				if isCancelExit(err, 1) {
					return "", nil
				}
				return "", err
			}
			return strings.TrimSpace(string(out)), nil
		}
		if path, lookErr := exec.LookPath("kdialog"); lookErr == nil && path != "" {
			out, err := exec.Command("kdialog", "--getexistingdirectory", "--title", "选择工作区文件夹").Output()
			if err != nil {
				if isCancelExit(err, 1) {
					return "", nil
				}
				return "", err
			}
			return strings.TrimSpace(string(out)), nil
		}
		return "", errors.New("no native folder picker available: zenity or kdialog required")
	}
}
