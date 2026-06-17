package bubble

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"gocode/internal/settings"
	"strings"
)

func newSettingWizard(current settings.Config) *settingWizard {
	current = settings.Normalize(current)
	w := &settingWizard{
		step:     settingWizardContext,
		selected: make(map[settingWizardStep]int),
		draft:    current,
	}
	w.selected[settingWizardContext] = selectedSettingIndex(settingOptions(settingWizardContext), string(current.Subagent.DefaultContextMode))
	w.selected[settingWizardRunMode] = selectedSettingIndex(settingOptions(settingWizardRunMode), string(current.Subagent.DefaultRunMode))
	w.selected[settingWizardMeterLocation] = selectedSettingIndex(settingOptions(settingWizardMeterLocation), string(current.UI.ContextMeterLocation))
	w.selected[settingWizardLimit] = selectedSettingLimitIndex(settingOptions(settingWizardLimit), current.UI.ContextLimitTokens)
	return w
}

func selectedSettingIndex(options []settingOption, value string) int {
	for i, option := range options {
		if option.label == value {
			return i
		}
	}
	return 0
}

func selectedSettingLimitIndex(options []settingOption, limit int) int {
	label := fmt.Sprintf("%d", limit)
	return selectedSettingIndex(options, label)
}

func settingOptions(step settingWizardStep) []settingOption {
	switch step {
	case settingWizardContext:
		return []settingOption{
			{label: string(settings.ContextModeEmpty), description: "temporary empty session", apply: func(cfg *settings.Config) { cfg.Subagent.DefaultContextMode = settings.ContextModeEmpty }},
			{label: string(settings.ContextModeFork), description: "fork parent committed history", apply: func(cfg *settings.Config) { cfg.Subagent.DefaultContextMode = settings.ContextModeFork }},
		}
	case settingWizardRunMode:
		return []settingOption{
			{label: string(settings.RunModeSync), description: "wait for subagent result", apply: func(cfg *settings.Config) { cfg.Subagent.DefaultRunMode = settings.RunModeSync }},
			{label: string(settings.RunModeBackground), description: "return a task id immediately", apply: func(cfg *settings.Config) { cfg.Subagent.DefaultRunMode = settings.RunModeBackground }},
		}
	case settingWizardMeterLocation:
		return []settingOption{
			{label: string(settings.MeterLocationInputAbove), description: "show context meter in the right sidebar", apply: func(cfg *settings.Config) { cfg.UI.ContextMeterLocation = settings.MeterLocationInputAbove }},
			{label: string(settings.MeterLocationHeader), description: "show context meter in the header", apply: func(cfg *settings.Config) { cfg.UI.ContextMeterLocation = settings.MeterLocationHeader }},
		}
	case settingWizardLimit:
		return []settingOption{
			{label: fmt.Sprintf("%d", settings.DefaultContextLimitTokens), description: "1024 * 1024 default", apply: func(cfg *settings.Config) { cfg.UI.ContextLimitTokens = settings.DefaultContextLimitTokens }},
			{label: "200000", description: "smaller local model context", apply: func(cfg *settings.Config) { cfg.UI.ContextLimitTokens = 200000 }},
			{label: "128000", description: "common long-context baseline", apply: func(cfg *settings.Config) { cfg.UI.ContextLimitTokens = 128000 }},
		}
	default:
		return nil
	}
}

func settingStepTitle(step settingWizardStep) string {
	switch step {
	case settingWizardContext:
		return "Subagent context"
	case settingWizardRunMode:
		return "Subagent run mode"
	case settingWizardMeterLocation:
		return "Context meter"
	case settingWizardLimit:
		return "Context limit"
	default:
		return "Confirm settings"
	}
}

func (m appModel) handleSettingWizardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingWizard == nil {
		return m, nil
	}
	m.settingWizard.err = ""
	switch msg.String() {
	case "ctrl+c", "esc":
		m.settingWizard = nil
		return m, nil
	case "b", "backspace":
		if m.settingWizard.step > settingWizardContext {
			m.settingWizard.step--
		}
		return m, nil
	case "up", "k":
		m.moveSettingSelection(-1)
		return m, nil
	case "down", "j":
		m.moveSettingSelection(1)
		return m, nil
	case "enter":
		return m.advanceSettingWizard(), nil
	}
	return m, nil
}

func (m *appModel) moveSettingSelection(delta int) {
	options := settingOptions(m.settingWizard.step)
	if len(options) == 0 {
		return
	}
	next := m.settingWizard.selected[m.settingWizard.step] + delta
	if next < 0 {
		next = len(options) - 1
	}
	if next >= len(options) {
		next = 0
	}
	m.settingWizard.selected[m.settingWizard.step] = next
}

func (m appModel) advanceSettingWizard() appModel {
	if m.settingWizard == nil {
		return m
	}
	if m.settingWizard.step == settingWizardConfirm {
		return m.applySettingWizard()
	}
	options := settingOptions(m.settingWizard.step)
	if len(options) > 0 {
		index := m.settingWizard.selected[m.settingWizard.step]
		if index < 0 || index >= len(options) {
			index = 0
		}
		options[index].apply(&m.settingWizard.draft)
	}
	m.settingWizard.step++
	return m
}

func (m appModel) applySettingWizard() appModel {
	if m.settingWizard == nil {
		return m
	}
	cfg := settings.Normalize(m.settingWizard.draft)
	if m.settingsConfig != nil {
		if err := m.settingsConfig.SaveSettings(cfg); err != nil {
			m.settingWizard.err = err.Error()
			return m
		}
	}
	m.settingWizard = nil
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "settings",
		body:  renderSettingsSummary(cfg),
	})
	m.relayout()
	return m
}

func (m appModel) renderSettingWizardBox() string {
	if m.settingWizard == nil {
		return ""
	}
	width := maxInt(32, m.width-2)
	var body string
	if m.settingWizard.step == settingWizardConfirm {
		body = m.renderSettingConfirmStep()
	} else {
		body = m.renderSettingChoiceStep()
	}
	return wizardPanelStyle.Width(width).Render(body)
}

func (m appModel) renderSettingChoiceStep() string {
	step := m.settingWizard.step
	lines := []string{wizardTitleStyle.Render(settingStepTitle(step))}
	options := settingOptions(step)
	for i, option := range options {
		text := "  " + option.label + "  " + option.description
		if i == m.settingWizard.selected[step] {
			text = selectedProviderStyle.Render("> " + option.label + "  " + option.description)
		} else {
			text = unselectedProviderStyle.Render(text)
		}
		lines = append(lines, text)
	}
	lines = append(lines, "Enter next, b back, esc cancel.")
	return strings.Join(lines, "\n")
}

func (m appModel) renderSettingConfirmStep() string {
	cfg := settings.Normalize(m.settingWizard.draft)
	lines := []string{
		wizardTitleStyle.Render("Confirm settings"),
		renderSettingsSummary(cfg),
		"Press enter to save, b to go back, esc to cancel.",
	}
	if m.settingWizard.err != "" {
		lines = append(lines, labelErrorStyle.Render(m.settingWizard.err))
	}
	return strings.Join(lines, "\n")
}

func renderSettingsSummary(cfg settings.Config) string {
	cfg = settings.Normalize(cfg)
	return fmt.Sprintf(
		"subagent.context=%s\nsubagent.run=%s\nui.context_limit=%d\nui.context_meter=%s",
		cfg.Subagent.DefaultContextMode,
		cfg.Subagent.DefaultRunMode,
		cfg.UI.ContextLimitTokens,
		cfg.UI.ContextMeterLocation,
	)
}
