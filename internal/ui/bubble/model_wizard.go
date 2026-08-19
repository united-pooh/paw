// 本文件定义 /model provider 切换向导的配置生成、按键处理和渲染。
package bubble

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"paw/internal/model"
)

// currentModelConfig 返回当前模型配置；没有控制器时返回空配置。
func (m appModel) currentModelConfig() model.Config {
	if m.modelConfig == nil {
		return model.Config{}
	}
	return m.modelConfig.CurrentModelConfig()
}

// configForProfile 根据 config.jsonc 运行时快照中的 profile 生成可应用配置。
func (m appModel) configForProfile(profile model.Profile) model.Config {
	cfg := profile.Config()
	cfg.Profiles = m.currentModelConfig().Profiles
	return cfg
}

// prepareModelWizardStep resolves the selected provider and creates the model
// list for the second step of /model. Config-v2 wizards use only the immutable
// catalog selections captured when the wizard opened; minimal controllers keep
// using their configured profile model names.
func (m *appModel) prepareModelWizardStep() {
	if m == nil || m.modelWizard == nil {
		return
	}
	wizard := m.modelWizard
	option := wizard.selectedProvider()
	cfg := m.configForProfile(option.profile)
	wizard.modelOptions = nil
	wizard.modelSelections = nil
	wizard.search = ""
	if wizard.catalogBound {
		for _, selection := range wizard.catalogSelections {
			if selection.ProviderKey != option.id && selection.ProviderKey != option.profile.Provider {
				continue
			}
			wizard.modelOptions = append(wizard.modelOptions, selection.ModelName)
			wizard.modelSelections = append(wizard.modelSelections, selection)
		}
	} else {
		wizard.modelOptions = model.AvailableModels(cfg)
		if len(wizard.modelOptions) == 0 && strings.TrimSpace(cfg.Model) != "" {
			wizard.modelOptions = []string{cfg.Model}
		}
	}
	wizard.selectedModel = 0
	selectedActive := false
	for index, selection := range wizard.modelSelections {
		if selection.ID == wizard.catalogActiveID {
			wizard.selectedModel = index
			selectedActive = true
			break
		}
	}
	if !selectedActive {
		for index, name := range wizard.modelOptions {
			if name == cfg.Model {
				wizard.selectedModel = index
				break
			}
		}
	}
	wizard.err = ""
	// A profile with one available model does not need an extra stop. Profiles
	// with multiple models use the explicit second step.
	if len(wizard.modelOptions) > 1 {
		wizard.step = modelWizardModel
	} else {
		wizard.step = modelWizardConfirm
	}
}

// handleModelWizardKey 处理 /model 向导中的方向键、确认键和取消键。
func (m appModel) handleModelWizardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelWizard == nil {
		return m, nil
	}

	m.modelWizard.err = ""
	switch m.modelWizard.step {
	case modelWizardProvider:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.modelWizard = nil
			return m, nil
		case "up", "k":
			m.modelWizard.selectedIndex--
			if m.modelWizard.selectedIndex < 0 {
				m.modelWizard.selectedIndex = len(m.modelWizard.providerOptions) - 1
			}
			return m, nil
		case "down", "j":
			m.modelWizard.selectedIndex++
			if m.modelWizard.selectedIndex >= len(m.modelWizard.providerOptions) {
				m.modelWizard.selectedIndex = 0
			}
			return m, nil
		case "enter":
			m.prepareModelWizardStep()
			return m, nil
		}
	case modelWizardModel:
		// 搜索框在本步骤始终激活：所有可打印字符一律进入搜索内容，
		// 不再把 b/j/k 等字母劫持为快捷键，避免与搜索输入冲突。
		// 列表移动只用方向键，返回服务商由空搜索时的 Backspace 承担。
		searching := m.modelWizard.search != ""
		switch msg.String() {
		case "ctrl+c":
			m.modelWizard = nil
			return m, nil
		case "esc":
			if searching {
				m.modelWizard.search = ""
				m.modelWizard.resetModelSelectionForSearch()
				return m, nil
			}
			m.modelWizard = nil
			return m, nil
		case "backspace":
			if searching {
				runes := []rune(m.modelWizard.search)
				m.modelWizard.search = string(runes[:len(runes)-1])
				m.modelWizard.resetModelSelectionForSearch()
				return m, nil
			}
			m.modelWizard.step = modelWizardProvider
			m.modelWizard.err = ""
			return m, nil
		case "up":
			m.modelWizard.moveModelSelection(-1)
			return m, nil
		case "down":
			m.modelWizard.moveModelSelection(1)
			return m, nil
		case "enter":
			if len(m.modelWizard.modelIndicesForSearch()) == 0 {
				return m, nil
			}
			m.modelWizard.step = modelWizardConfirm
			return m, nil
		}
		if len(msg.Runes) > 0 && !msg.Alt {
			m.modelWizard.search += string(msg.Runes)
			m.modelWizard.resetModelSelectionForSearch()
			return m, nil
		}
	case modelWizardConfirm:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.modelWizard = nil
			return m, nil
		case "b", "backspace":
			if len(m.modelWizard.modelOptions) > 0 {
				m.modelWizard.step = modelWizardModel
			} else {
				m.modelWizard.step = modelWizardProvider
			}
			return m, nil
		case "enter":
			return m.applyModelWizardSelection(), nil
		}
	}

	return m, nil
}

func (w *modelWizard) modelIndicesForSearch() []int {
	if w == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(w.search))
	indices := make([]int, 0, len(w.modelOptions))
	for index, name := range w.modelOptions {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		indices = append(indices, index)
	}
	return indices
}

func (w *modelWizard) resetModelSelectionForSearch() {
	indices := w.modelIndicesForSearch()
	if len(indices) == 0 {
		w.selectedModel = -1
		return
	}
	w.selectedModel = indices[0]
}

func (w *modelWizard) moveModelSelection(delta int) {
	indices := w.modelIndicesForSearch()
	if len(indices) == 0 {
		w.selectedModel = -1
		return
	}
	position := 0
	for index, modelIndex := range indices {
		if modelIndex == w.selectedModel {
			position = index
			break
		}
	}
	position = (position + delta + len(indices)) % len(indices)
	w.selectedModel = indices[position]
}

// applyModelWizardSelection persists and applies the selected model. A
// config-v2 wizard activates the exact origin-bound catalog selection captured
// for the displayed row; it never re-resolves provider/model text at confirm.
func (m appModel) applyModelWizardSelection() appModel {
	if m.modelWizard == nil {
		return m
	}
	wizard := m.modelWizard
	if wizard.catalogBound {
		selection, ok := wizard.selectedCatalogSelection()
		if !ok {
			wizard.err = "selected model is unavailable"
			return m
		}
		if m.configCenterController == nil {
			wizard.err = "config-v2 controller is unavailable"
			return m
		}
		if err := m.configCenterController.ActivateCatalogSelection(selection); err != nil {
			wizard.err = err.Error()
			return m
		}
		cfg := m.currentModelConfig()
		m.syncRunnerModelContextLimit(cfg)
		m.modelWizard = nil
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "model",
			body:  fmt.Sprintf("id=%s provider=%s base=%s path=%s model=%s context=%d retries=%d key=%s", selection.ID, cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, model.EffectiveContextLimitTokens(cfg), cfg.RetryCount, cfg.APIKeyEnvName),
		})
		return m
	}
	if m.modelConfig == nil {
		wizard.err = "model config controller is unavailable"
		return m
	}

	option := wizard.selectedProvider()
	cfg := m.configForProfile(option.profile)
	if selected := wizard.selectedModelName(); selected != "" {
		cfg.Model = selected
	}
	if saver, ok := m.modelConfig.(ModelConfigSaver); ok {
		if err := saver.SaveModelConfig(cfg); err != nil {
			wizard.err = err.Error()
			return m
		}
	}
	if err := m.modelConfig.ApplyModelConfig(cfg); err != nil {
		wizard.err = err.Error()
		return m
	}
	m.syncRunnerModelContextLimit(cfg)

	m.modelWizard = nil
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "model",
		body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s context=%d retries=%d key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, model.EffectiveContextLimitTokens(cfg), cfg.RetryCount, cfg.APIKeyEnvName),
	})
	return m
}

// renderModelWizardBox 渲染 /model 向导面板。
func (m appModel) renderModelWizardBox() string {
	if m.modelWizard == nil {
		return ""
	}
	var body string
	switch m.modelWizard.step {
	case modelWizardProvider:
		body = m.renderProviderStep()
	case modelWizardModel:
		body = m.renderModelStep()
	case modelWizardConfirm:
		body = m.renderModelConfirmStep()
	default:
		body = "Unknown model wizard step"
	}
	return m.renderModalPanel(body)
}

// renderModelStep renders the models available under the selected provider.
func (m appModel) renderModelStep() string {
	lines := []string{
		wizardTitleStyle.Render("Choose model"),
		fmt.Sprintf("Provider: %s", m.modelWizard.selectedProvider().label),
	}

	icon := m.styles.InputPrompt.Copy().Bold(true).Render("⌕")
	var searchText string
	if m.modelWizard.search == "" {
		searchText = m.styles.StatusMuted.Render("搜索模型…")
	} else {
		searchText = m.styles.Body.Render(m.modelWizard.search)
	}
	searchStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.InputPrompt.GetForeground()).
		Padding(0, 1)
	lines = append(lines, renderFixedStyledPanel(searchStyle, m.modalPanelBodyWidth(), 3, icon+" "+searchText))

	indices := m.modelWizard.modelIndicesForSearch()
	if len(indices) == 0 {
		lines = append(lines, labelErrorStyle.Render("No matching models"))
	} else {
		selectedPosition := 0
		for position, index := range indices {
			if index == m.modelWizard.selectedModel {
				selectedPosition = position
				break
			}
		}
		maxItems := clampInt(m.currentLayout().transcriptHeight-10, 1, len(indices))
		start := maxInt(0, selectedPosition-maxItems+1)
		end := minInt(len(indices), start+maxItems)
		for position := start; position < end; position++ {
			index := indices[position]
			name := m.modelWizard.modelOptions[index]
			if index == m.modelWizard.selectedModel {
				lines = append(lines, selectedProviderStyle.Render("> "+name))
			} else {
				lines = append(lines, unselectedProviderStyle.Render("  "+name))
			}
		}
	}
	lines = append(lines, "输入筛选 · ↑/↓ 移动 · Enter 选择 · Esc 清除/关闭 · Backspace 返回服务商")
	return strings.Join(lines, "\n")
}

// renderProviderStep 渲染 provider 列表选择步骤。
func (m appModel) renderProviderStep() string {
	lines := []string{
		wizardTitleStyle.Render("Model provider"),
		"Choose a provider:",
	}
	maxItems := clampInt(m.currentLayout().transcriptHeight-7, 1, len(m.modelWizard.providerOptions))
	start := maxInt(0, m.modelWizard.selectedIndex-maxItems+1)
	end := minInt(len(m.modelWizard.providerOptions), start+maxItems)
	for i := start; i < end; i++ {
		option := m.modelWizard.providerOptions[i]
		text := "  " + option.label + "  " + option.description
		if i == m.modelWizard.selectedIndex {
			text = selectedProviderStyle.Render("> " + option.label + "  " + option.description)
		} else {
			text = unselectedProviderStyle.Render(text)
		}
		lines = append(lines, text)
	}
	if maxItems < len(m.modelWizard.providerOptions) {
		lines = append(lines, fmt.Sprintf("(%d/%d) Use up/down to scroll", m.modelWizard.selectedIndex+1, len(m.modelWizard.providerOptions)))
	}
	return strings.Join(lines, "\n")
}

// renderModelConfirmStep 渲染 provider 配置确认步骤和错误提示。
func (m appModel) renderModelConfirmStep() string {
	option := m.modelWizard.selectedProvider()
	cfg := m.configForProfile(option.profile)
	lines := []string{wizardTitleStyle.Render("Confirm model")}

	lines = append(lines,
		fmt.Sprintf("provider: %s", cfg.Provider),
		fmt.Sprintf("base url: %s", cfg.APIBaseURL),
		fmt.Sprintf("path: %s", cfg.APIPath),
		fmt.Sprintf("model: %s", m.modelWizard.selectedModelNameOr(cfg.Model)),
		fmt.Sprintf("models: %s", strings.Join(model.AvailableModels(cfg), ", ")),
		fmt.Sprintf("key env: %s", cfg.APIKeyEnvName),
		"Press enter to apply, b to go back, esc to cancel.",
	)
	if m.modelWizard.err != "" {
		lines = append(lines, labelErrorStyle.Render(m.modelWizard.err))
	}
	return strings.Join(lines, "\n")
}
