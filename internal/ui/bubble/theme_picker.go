package bubble

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"paw/internal/theme"
)

type themePickerState struct {
	original    theme.ThemeID
	selected    theme.ThemeID
	selectedIdx int
	saveError   string
}

func (m *appModel) openThemePicker() {
	items := theme.List()
	idx := 0
	for i, item := range items {
		if item.ID == m.theme.ID {
			idx = i
			break
		}
	}
	m.themePicker = &themePickerState{original: m.theme.ID, selected: m.theme.ID, selectedIdx: idx}
	m.pending = nil
	m.clearCompletionAndRelayout()
}

func (m *appModel) previewThemeIndex(index int) error {
	items := theme.List()
	if len(items) == 0 {
		return fmt.Errorf("no themes are registered")
	}
	index = clampInt(index, 0, len(items)-1)
	candidate := items[index]
	if err := m.applyTheme(candidate.ID); err != nil {
		return err
	}
	m.themePicker.selectedIdx = index
	m.themePicker.selected = candidate.ID
	m.themePicker.saveError = ""
	return nil
}

func (m appModel) handleThemePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.themePicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if err := m.previewThemeIndex(m.themePicker.selectedIdx - 1); err != nil {
			m.themePicker.saveError = err.Error()
		}
	case "down", "j":
		if err := m.previewThemeIndex(m.themePicker.selectedIdx + 1); err != nil {
			m.themePicker.saveError = err.Error()
		}
	case "home":
		if err := m.previewThemeIndex(0); err != nil {
			m.themePicker.saveError = err.Error()
		}
	case "end":
		if err := m.previewThemeIndex(len(theme.List()) - 1); err != nil {
			m.themePicker.saveError = err.Error()
		}
	case "enter":
		cfg := m.currentSettings()
		if cfg.UI.Theme == m.themePicker.selected {
			m.themePicker = nil
			return m, nil
		}
		cfg.UI.Theme = m.themePicker.selected
		if m.settingsConfig != nil {
			if err := m.settingsConfig.SaveSettings(cfg); err != nil {
				m.themePicker.saveError = "Unable to save theme: " + err.Error()
				return m, nil
			}
		}
		m.themePicker = nil
	case "esc", "ctrl+c":
		original := m.themePicker.original
		if err := m.applyTheme(original); err != nil {
			m.themePicker.saveError = err.Error()
			return m, nil
		}
		m.themePicker = nil
	}
	return m, nil
}

func (m appModel) renderThemePickerBox() string {
	if m.themePicker == nil {
		return ""
	}
	m.activateThemeStyles()
	items := theme.List()
	lines := []string{m.styles.ModalTitle.Render("Theme")}
	available := maxInt(1, m.currentLayout().transcriptHeight-7)
	start := maxInt(0, m.themePicker.selectedIdx-available+1)
	end := minInt(len(items), start+available)
	for i := start; i < end; i++ {
		item := items[i]
		mark := "  "
		if i == m.themePicker.selectedIdx {
			mark = "› "
		}
		saved := " "
		if item.ID == m.currentSettings().UI.Theme {
			saved = "●"
		}
		swatches := themeSwatches(item)
		label := fmt.Sprintf("%s%-22s %-22s %-5s %s %s", mark, item.Name, item.ID, item.Mode, swatches, saved)
		style := m.styles.Unselected
		if i == m.themePicker.selectedIdx {
			style = m.styles.Selected
		}
		lines = append(lines, style.Render(truncateStyledCellLine(label, 74)))
	}
	if m.themePicker.saveError != "" {
		lines = append(lines, m.styles.LabelError.Render(m.themePicker.saveError))
	}
	lines = append(lines, m.styles.StatusMuted.Render("↑/↓ preview  enter save  esc cancel"))
	return m.renderModalPanel(strings.Join(lines, "\n"))
}

func themeSwatches(item theme.Theme) string {
	colors := []string{item.Colors.Signal, item.Colors.MarkdownLink, item.Colors.LabelAssistant, item.Colors.WorktreeClean, item.Colors.LabelError}
	parts := make([]string, 0, len(colors))
	for _, color := range colors {
		parts = append(parts, lipgloss.NewStyle().Background(lipgloss.Color(color)).Render("  "))
	}
	return strings.Join(parts, "")
}
