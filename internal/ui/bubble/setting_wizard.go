package bubble

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/settings"
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
	w.selected[settingWizardTranslate] = selectedSettingIndex(settingOptions(settingWizardTranslate), boolSettingLabel(current.UI.TranslateOnDoubleClick))
	return w
}

func boolSettingLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func selectedSettingIndex(options []settingOption, value string) int {
	for i, option := range options {
		if option.label == value {
			return i
		}
	}
	return 0
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
	case settingWizardTranslate:
		return []settingOption{
			{label: "on", description: "double-click a word to translate it", apply: func(cfg *settings.Config) { cfg.UI.TranslateOnDoubleClick = true }},
			{label: "off", description: "double-click only selects the word", apply: func(cfg *settings.Config) { cfg.UI.TranslateOnDoubleClick = false }},
			{label: "on (session only)", description: "enable now without writing the settings file", sessionOnly: true, apply: func(cfg *settings.Config) { cfg.UI.TranslateOnDoubleClick = true }},
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
	case settingWizardTranslate:
		return "Translate selected word"
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
		if options[index].sessionOnly {
			m.settingWizard.sessionOnly = true
		}
	}
	m.settingWizard.step++
	return m
}

func (m appModel) applySettingWizard() appModel {
	if m.settingWizard == nil {
		return m
	}
	cfg := settings.Normalize(m.settingWizard.draft)
	sessionOnly := m.settingWizard.sessionOnly
	switch {
	case sessionOnly:
		// 面板里的动态开关：只更新内存、不写配置文件。
		if m.settingsConfig != nil {
			m.settingsConfig.UpdateRuntime(cfg)
		}
	case m.settingsConfig != nil:
		if err := m.settingsConfig.SaveSettings(cfg); err != nil {
			m.settingWizard.err = err.Error()
			return m
		}
	}
	// compression.mode 已并入配置中心 General 扁平列表（syncRunnerSettings），
	// 向导不再单独热切换压缩模式。
	m.settingWizard = nil
	body := renderSettingsSummary(cfg)
	if sessionOnly {
		body += "\napply mode: session only (not written to disk)"
	}
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "settings",
		body:  body,
	})
	m.relayout()
	return m
}

func (m appModel) renderSettingWizardBox() string {
	if m.settingWizard == nil {
		return ""
	}
	var body string
	if m.settingWizard.step == settingWizardConfirm {
		body = m.renderSettingConfirmStep()
	} else {
		body = m.renderSettingChoiceStep()
	}
	return m.renderFullscreenPanel(body)
}

func (m appModel) renderSettingChoiceStep() string {
	step := m.settingWizard.step
	lines := []string{wizardTitleStyle.Render(settingStepTitle(step))}
	options := settingOptions(step)
	maxItems := clampInt(m.currentLayout().frameHeight-7, 1, len(options))
	selected := m.settingWizard.selected[step]
	start := maxInt(0, selected-maxItems+1)
	end := minInt(len(options), start+maxItems)
	for i := start; i < end; i++ {
		option := options[i]
		text := "  " + option.label + "  " + option.description
		if i == selected {
			text = selectedProviderStyle.Render("> " + option.label + "  " + option.description)
		} else {
			text = unselectedProviderStyle.Render(text)
		}
		lines = append(lines, text)
	}
	if maxItems < len(options) {
		lines = append(lines, fmt.Sprintf("(%d/%d) Use up/down to scroll", selected+1, len(options)))
	}
	lines = append(lines, "Enter next, b back, esc cancel.")
	return strings.Join(lines, "\n")
}

func (m appModel) renderSettingConfirmStep() string {
	cfg := settings.Normalize(m.settingWizard.draft)
	lines := []string{
		wizardTitleStyle.Render("Confirm settings"),
		renderSettingsSummary(cfg),
	}
	if m.settingWizard.sessionOnly {
		lines = append(lines, "Apply mode: session only — takes effect now, not written to disk. Enter to apply, b back, esc cancel.")
	} else {
		lines = append(lines, "Press enter to save, b to go back, esc to cancel.")
	}
	if m.settingWizard.err != "" {
		lines = append(lines, labelErrorStyle.Render(m.settingWizard.err))
	}
	return strings.Join(lines, "\n")
}

func renderSettingsSummary(cfg settings.Config) string {
	cfg = settings.Normalize(cfg)
	return fmt.Sprintf(
		"subagent.context=%s\nsubagent.run=%s\nui.translate_on_double_click=%s",
		cfg.Subagent.DefaultContextMode,
		cfg.Subagent.DefaultRunMode,
		boolSettingLabel(cfg.UI.TranslateOnDoubleClick),
	)
}
