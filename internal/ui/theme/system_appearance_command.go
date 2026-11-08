//go:build !darwin || !cgo

package theme

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func DetectSystemMode(ctx context.Context) (ThemeMode, error) {
	switch runtime.GOOS {
	case "darwin":
		const script = `ObjC.import('Foundation');
function run() {
    const value = ObjC.unwrap($.NSUserDefaults.standardUserDefaults.objectForKey('AppleInterfaceStyle'));
    if (value === undefined || value === null) return 'light';
    if (value === 'Dark') return 'dark';
    throw new Error('Unsupported system appearance value');
}`
		value, err := systemAppearanceCommand(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script)
		if err != nil {
			return "", err
		}
		switch value {
		case "dark":
			return ModeDark, nil
		case "light":
			return ModeLight, nil
		}
	case "linux":
		value, err := systemAppearanceCommand(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme")
		if err != nil {
			return "", err
		}
		switch value {
		case "'prefer-dark'":
			return ModeDark, nil
		case "'prefer-light'", "'default'":
			return ModeLight, nil
		}
	case "windows":
		root := os.Getenv("SystemRoot")
		if root == "" {
			return "", errors.New("system appearance query requires SystemRoot")
		}
		value, err := systemAppearanceCommand(ctx, filepath.Join(root, "System32", "reg.exe"),
			"query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, "/v", "AppsUseLightTheme")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(value, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[0] != "AppsUseLightTheme" || fields[1] != "REG_DWORD" {
				continue
			}
			switch fields[2] {
			case "0x0":
				return ModeDark, nil
			case "0x1":
				return ModeLight, nil
			}
		}
	default:
		return "", fmt.Errorf("system appearance detection is unsupported on %s", runtime.GOOS)
	}
	return "", errors.New("system appearance query returned an unsupported value")
}

func systemAppearanceCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	var output appearanceOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("system appearance query: %w", err)
	}
	return strings.TrimSpace(output.String()), nil
}

type appearanceOutput struct{ buffer bytes.Buffer }

func (b *appearanceOutput) String() string { return b.buffer.String() }

func (b *appearanceOutput) Write(p []byte) (int, error) {
	if len(p) > 4096-b.buffer.Len() {
		return 0, errors.New("system appearance output exceeded 4096 bytes")
	}
	return b.buffer.Write(p)
}
