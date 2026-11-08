package bubble

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/ui/theme"
)

type systemThemeTickMsg struct{}

type systemThemeResultMsg struct {
	mode theme.ThemeMode
	err  error
}

func resolveTheme(id theme.ThemeID, mode theme.ThemeMode) (theme.Theme, bool) {
	item, ok := theme.ByID(id)
	if !ok || id != theme.DefaultAuto {
		return item, ok
	}
	variant := theme.DefaultDark
	if mode == theme.ModeLight {
		variant = theme.DefaultLight
	}
	resolved, ok := theme.ByID(variant)
	item.Colors = resolved.Colors
	return item, ok
}

func systemThemeTickCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return systemThemeTickMsg{}
		}
	}
}

func detectSystemThemeCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		mode, err := theme.DetectSystemMode(ctx)
		return systemThemeResultMsg{mode: mode, err: err}
	}
}

func (m *appModel) applySystemThemeResult(msg systemThemeResultMsg) {
	if msg.err == nil && msg.mode != theme.ModeDark && msg.mode != theme.ModeLight {
		msg.err = fmt.Errorf("invalid system appearance %q", msg.mode)
	}
	m.systemThemeError = msg.err
	if msg.err != nil {
		return
	}
	changed := m.systemThemeMode != msg.mode
	m.systemThemeMode = msg.mode
	if changed && m.theme.ID == theme.DefaultAuto {
		m.systemThemeError = m.applyTheme(theme.DefaultAuto)
	}
}

func (m appModel) systemThemeStatus() string {
	mode := "深色"
	if m.systemThemeMode == theme.ModeLight {
		mode = "浅色"
	}
	if m.systemThemeError != nil {
		return "系统外观不可用，暂用" + mode
	}
	return "跟随系统外观 · " + mode
}
