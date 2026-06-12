// 本文件定义 /model provider 切换向导的配置生成、按键处理和渲染。
package bubble

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"gocode/internal/model"
	"os"
	"strings"
	"time"
)

// currentModelConfig 返回当前模型配置；没有控制器时使用 custom 默认配置兜底。
func (m appModel) currentModelConfig() model.Config {
	if m.modelConfig == nil {
		return model.Config{
			Provider:      model.ProviderCustom,
			APIBaseURL:    model.CustomAPIBaseURL,
			APIPath:       model.CustomChatPath,
			APIKey:        model.CustomDefaultAPIKey,
			Model:         model.CustomDefaultModel,
			APIKeyEnvName: model.CustomAPIKeyEnvName,
			Timeout:       time.Minute,
		}
	}
	return m.modelConfig.CurrentModelConfig()
}

// configForProvider 根据目标 provider 生成可应用的模型配置。
func (m appModel) configForProvider(provider string) (model.Config, error) {
	current := m.currentModelConfig()
	switch provider {
	case model.ProviderCustom:
		cfg := current
		currentProvider := cfg.Provider
		cfg.Provider = model.ProviderCustom
		if strings.TrimSpace(cfg.APIBaseURL) == "" || currentProvider == model.ProviderDeepSeek {
			cfg.APIBaseURL = model.CustomAPIBaseURL
		}
		if strings.TrimSpace(cfg.APIPath) == "" || currentProvider == model.ProviderDeepSeek {
			cfg.APIPath = model.CustomChatPath
		}
		if strings.TrimSpace(cfg.Model) == "" || currentProvider == model.ProviderDeepSeek {
			cfg.Model = model.CustomDefaultModel
		}
		cfg.APIKeyEnvName = model.CustomAPIKeyEnvName
		cfg.APIKey = strings.TrimSpace(os.Getenv(cfg.APIKeyEnvName))
		if cfg.APIKey == "" {
			cfg.APIKey = model.CustomDefaultAPIKey
		}
		return cfg, nil
	case model.ProviderDeepSeek:
		cfg := current
		cfg.Provider = model.ProviderDeepSeek
		cfg.APIBaseURL = model.DeepSeekAPIBaseURL
		cfg.APIPath = model.DeepSeekChatPath
		cfg.Model = model.DeepSeekDefaultModel
		cfg.APIKeyEnvName = model.DeepSeekAPIKeyEnvName
		cfg.APIKey = strings.TrimSpace(os.Getenv(cfg.APIKeyEnvName))
		if cfg.APIKey == "" {
			return model.Config{}, fmt.Errorf("missing %s", cfg.APIKeyEnvName)
		}
		return cfg, nil
	default:
		return model.Config{}, fmt.Errorf("unknown provider: %s", provider)
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
				m.modelWizard.selectedIndex = len(modelProviderOptions) - 1
			}
			return m, nil
		case "down", "j":
			m.modelWizard.selectedIndex++
			if m.modelWizard.selectedIndex >= len(modelProviderOptions) {
				m.modelWizard.selectedIndex = 0
			}
			return m, nil
		case "enter":
			m.modelWizard.step = modelWizardConfirm
			return m, nil
		}
	case modelWizardConfirm:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.modelWizard = nil
			return m, nil
		case "b", "backspace":
			m.modelWizard.step = modelWizardProvider
			return m, nil
		case "enter":
			return m.applyModelWizardSelection(), nil
		}
	}

	return m, nil
}

// applyModelWizardSelection 持久化并应用当前选中的 provider 配置。
func (m appModel) applyModelWizardSelection() appModel {
	if m.modelWizard == nil {
		return m
	}
	if m.modelConfig == nil {
		m.modelWizard.err = "model config controller is unavailable"
		return m
	}

	option := m.modelWizard.selectedProvider()
	cfg, err := m.configForProvider(option.id)
	if err != nil {
		m.modelWizard.err = err.Error()
		return m
	}
	if saver, ok := m.modelConfig.(ModelConfigSaver); ok {
		if err := saver.SaveModelConfig(cfg); err != nil {
			m.modelWizard.err = err.Error()
			return m
		}
	}
	if err := m.modelConfig.ApplyModelConfig(cfg); err != nil {
		m.modelWizard.err = err.Error()
		return m
	}

	m.modelWizard = nil
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "model",
		body:  fmt.Sprintf("provider=%s base=%s path=%s model=%s key=%s", cfg.Provider, cfg.APIBaseURL, cfg.APIPath, cfg.Model, cfg.APIKeyEnvName),
	})
	return m
}

// renderModelWizardBox 渲染 /model 向导面板。
func (m appModel) renderModelWizardBox() string {
	if m.modelWizard == nil {
		return ""
	}
	width := maxInt(32, m.width-2)
	var body string
	switch m.modelWizard.step {
	case modelWizardProvider:
		body = m.renderProviderStep()
	case modelWizardConfirm:
		body = m.renderModelConfirmStep()
	default:
		body = "Unknown model wizard step"
	}
	return wizardPanelStyle.Width(width).Render(body)
}

// renderProviderStep 渲染 provider 列表选择步骤。
func (m appModel) renderProviderStep() string {
	lines := []string{
		wizardTitleStyle.Render("Model provider"),
		"Choose a provider:",
	}
	for i, option := range modelProviderOptions {
		text := "  " + option.label + "  " + option.description
		if i == m.modelWizard.selectedIndex {
			text = selectedProviderStyle.Render("> " + option.label + "  " + option.description)
		} else {
			text = unselectedProviderStyle.Render(text)
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

// renderModelConfirmStep 渲染 provider 配置确认步骤和错误提示。
func (m appModel) renderModelConfirmStep() string {
	option := m.modelWizard.selectedProvider()
	cfg, err := m.configForProvider(option.id)
	lines := []string{wizardTitleStyle.Render("Confirm model provider")}
	if err != nil {
		lines = append(lines,
			fmt.Sprintf("provider: %s", option.label),
			labelErrorStyle.Render(err.Error()),
			"Set the missing environment variable, then press enter again.",
			"Press b to choose another provider.",
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines,
		fmt.Sprintf("provider: %s", cfg.Provider),
		fmt.Sprintf("base url: %s", cfg.APIBaseURL),
		fmt.Sprintf("path: %s", cfg.APIPath),
		fmt.Sprintf("model: %s", cfg.Model),
		fmt.Sprintf("key env: %s", cfg.APIKeyEnvName),
		"Press enter to apply, b to go back, esc to cancel.",
	)
	if m.modelWizard.err != "" {
		lines = append(lines, labelErrorStyle.Render(m.modelWizard.err))
	}
	return strings.Join(lines, "\n")
}
