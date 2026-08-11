package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	configv2 "paw/internal/config"
)

type configCenterPage int

const (
	configCenterHome configCenterPage = iota
	configCenterProviders
	configCenterProviderActions
	configCenterAddProvider
	configCenterModels
	configCenterModelActions
	configCenterAddModelProvider
	configCenterActive
	configCenterCredentials
	configCenterCredentialActions
	configCenterDiagnostics
	configCenterEdit
)

type configEditKind int

const (
	configEditNone configEditKind = iota
	configEditProviderEndpoint
	configEditProviderTimeout
	configEditProviderRetries
	configEditProviderEnv
	configEditProviderHeaders
	configEditProviderBody
	configEditCustomProviderID
	configEditCustomProviderEndpoint
	configEditModelName
	configEditModelContextWindow
	configEditModelParameters
	configEditNewModelID
	configEditNewModelName
	configEditCredential
)

type configCenterState struct {
	page              configCenterPage
	selected          int
	revision          uint64
	err               string
	notice            string
	noticeSequence    uint64
	targetID          string
	targetSelection   configv2.CatalogSelection
	catalogPage       configCenterPage
	catalogSelections []configv2.CatalogSelection
	catalogLoaded     bool
	editKind          configEditKind
	editValue         string
	draftProviderID   string
	draftProvider     configv2.Provider
	draftModelID      string
	draftModel        configv2.Model
	credentialID      string
	confirmAction     string
}

type configCenterOption struct {
	label       string
	description string
}

const configCenterSavedNoticeDuration = 3 * time.Second

type configCenterSavedExpiredMsg struct {
	state    *configCenterState
	sequence uint64
}

func (state *configCenterState) showSavedNotice() tea.Cmd {
	if state == nil {
		return nil
	}
	state.notice = "Saved"
	state.noticeSequence++
	sequence := state.noticeSequence
	return tea.Tick(configCenterSavedNoticeDuration, func(time.Time) tea.Msg {
		return configCenterSavedExpiredMsg{state: state, sequence: sequence}
	})
}

func (m *appModel) openConfigCenter() {
	if m.configCenterController == nil {
		m.settingWizard = newSettingWizard(m.currentSettings())
		return
	}
	snapshot := m.configCenterController.Snapshot()
	m.settingWizard = nil
	m.modelWizard = nil
	page := configCenterHome
	selected := 0
	if !snapshot.Ready {
		switch {
		case hasBlockingDocumentDiagnostic(snapshot.Diagnostics):
			page = configCenterDiagnostics
		case len(snapshot.Document.Providers) == 0:
			page = configCenterAddProvider
		case len(snapshot.EffectiveModels) == 0:
			page = configCenterAddModelProvider
		case snapshot.ActiveModelID == "":
			page = configCenterActive
		default:
			page = configCenterCredentials
			active := snapshot.Document.Models[snapshot.ActiveModelID]
			selected = sortedIndex(sortedProviderIDs(snapshot.Document.Providers), active.Provider)
		}
	}
	m.configCenter = &configCenterState{page: page, selected: selected, revision: snapshot.Revision}
}

func (m *appModel) handleConfigCommand(invocation string) {
	argument := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(invocation), "/config"))
	if m.configCenterController == nil {
		m.addEntry(transcriptEntry{kind: entryError, title: "config", body: "config-v2 controller is unavailable"})
		return
	}
	switch strings.ToLower(argument) {
	case "":
		m.openConfigCenter()
	case "reload":
		if err := m.configCenterController.ReloadConfig(); err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "config", body: err.Error()})
			return
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "config", body: configStatusSummary(m.configCenterController.Snapshot())})
	case "status":
		m.addEntry(transcriptEntry{kind: entrySystem, title: "config", body: configStatusSummary(m.configCenterController.Snapshot())})
	case "path":
		m.addEntry(transcriptEntry{kind: entrySystem, title: "config", body: m.configCenterController.ConfigPath()})
	default:
		m.addEntry(transcriptEntry{kind: entryError, title: "config", body: "usage: /config [reload|status|path]"})
	}
}

func configStatusSummary(snapshot configv2.Snapshot) string {
	state := "setup required"
	if snapshot.Ready {
		state = "ready"
	}
	return fmt.Sprintf(
		"state=%s\nrevision=%d\nactiveModel=%s\nproviders=%d models=%d registered=%d effective=%d\ndiagnostics=%d\n%s",
		state,
		snapshot.Revision,
		emptyLabel(snapshot.ActiveModelID),
		len(snapshot.Document.Providers),
		len(snapshot.Document.Models),
		len(snapshot.Document.Models),
		len(snapshot.EffectiveModels),
		len(snapshot.Diagnostics),
		discoveryStatusSummary(snapshot.Discovery, time.Now()),
	)
}

func discoveryStatusSummary(status configv2.DiscoveryStatus, now time.Time) string {
	return fmt.Sprintf(
		"discovery source=%s provider=%s discovered=%d filtered=%d effective=%d cache=%s attempted=%t cacheProviders=%d attemptedAt=%s succeededAt=%s discoveredAt=%s age=%s skip=%s error=%s",
		emptyLabel(status.Source),
		emptyLabel(status.ProviderID),
		status.DiscoveredCount,
		status.FilteredCount,
		status.EffectiveCount,
		emptyLabel(status.CacheState),
		status.Attempted,
		status.CacheProviders,
		discoveryTimeLabel(status.AttemptedAt),
		discoveryTimeLabel(status.SucceededAt),
		discoveryTimeLabel(status.DiscoveredAt),
		discoveryAgeLabel(status.DiscoveredAt, now),
		safeDiscoveryStatusLabel(status.SkippedReason),
		safeDiscoveryStatusLabel(status.LastError),
	)
}

func discoveryTimeLabel(value time.Time) string {
	if value.IsZero() {
		return "(none)"
	}
	return value.UTC().Format(time.RFC3339)
}

func discoveryAgeLabel(discoveredAt, now time.Time) string {
	if discoveredAt.IsZero() {
		return "(none)"
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(discoveredAt)
	if age < 0 {
		age = 0
	}
	return age.Round(time.Second).String()
}

func safeDiscoveryStatusLabel(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "(none)"
	}
	const maximumRunes = 240
	runes := []rune(value)
	if len(runes) > maximumRunes {
		value = string(runes[:maximumRunes-1]) + "…"
	}
	return value
}

func emptyLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}

func (m appModel) handleConfigCenterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.configCenter
	if state == nil {
		return m, nil
	}
	state.err = ""
	if state.page == configCenterEdit {
		return m.handleConfigEditKey(msg)
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		m.configCenter = nil
		return m, nil
	case "b", "backspace":
		m.configCenterBack()
		return m, nil
	case "up", "k":
		m.moveConfigCenterSelection(-1)
		return m, nil
	case "down", "j":
		m.moveConfigCenterSelection(1)
		return m, nil
	case "enter":
		return m.advanceConfigCenter(), nil
	}
	return m, nil
}

func (m *appModel) moveConfigCenterSelection(delta int) {
	options := m.configCenterOptions()
	if len(options) == 0 {
		return
	}
	m.configCenter.selected = (m.configCenter.selected + delta + len(options)) % len(options)
	m.configCenter.confirmAction = ""
}

func (m *appModel) configCenterBack() {
	if m.configCenter == nil {
		return
	}
	switch m.configCenter.page {
	case configCenterHome:
		m.configCenter = nil
		return
	case configCenterProviderActions, configCenterAddProvider:
		m.configCenter.page = configCenterProviders
	case configCenterModelActions, configCenterAddModelProvider:
		m.configCenter.page = configCenterModels
	case configCenterCredentialActions:
		m.configCenter.page = configCenterCredentials
	case configCenterModels, configCenterActive:
		m.configCenter.invalidateCatalog()
		m.configCenter.page = configCenterHome
	default:
		m.configCenter.page = configCenterHome
	}
	m.configCenter.selected = 0
	m.configCenter.err = ""
	m.configCenter.confirmAction = ""
}

func (m appModel) advanceConfigCenter() appModel {
	state := m.configCenter
	if state == nil {
		return m
	}
	options := m.configCenterOptions()
	if len(options) == 0 {
		return m
	}
	if state.selected < 0 || state.selected >= len(options) {
		state.selected = 0
	}
	snapshot := m.configCenterController.Snapshot()
	switch state.page {
	case configCenterHome:
		switch state.selected {
		case 0:
			m.configCenter = nil
			m.settingWizard = newSettingWizard(m.currentSettings())
			return m
		case 1:
			state.page = configCenterProviders
		case 2:
			state.invalidateCatalog()
			state.page = configCenterModels
		case 3:
			state.invalidateCatalog()
			state.page = configCenterActive
		case 4:
			state.page = configCenterCredentials
		case 5:
			state.page = configCenterDiagnostics
		}
		state.selected = 0
	case configCenterProviders:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		if state.selected == len(ids) {
			state.page = configCenterAddProvider
			state.selected = 0
		} else {
			state.targetID = ids[state.selected]
			state.page = configCenterProviderActions
			state.selected = 0
		}
	case configCenterAddProvider:
		presets := sortedPresetIDs()
		if state.selected < len(presets) {
			m.addPresetProvider(presets[state.selected])
		} else {
			retries := 3
			state.draftProvider = configv2.Provider{Transport: configv2.TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1", TimeoutSeconds: 60, Retries: &retries}
			m.openConfigEdit(configEditCustomProviderID, "", "")
		}
	case configCenterProviderActions:
		m.advanceProviderAction(snapshot)
	case configCenterModels:
		selections := state.catalogSelections
		if state.selected == len(selections) {
			state.page = configCenterAddModelProvider
			state.selected = 0
		} else if state.selected < len(selections) {
			state.targetSelection = selections[state.selected]
			state.targetID = state.targetSelection.ID
			state.page = configCenterModelActions
			state.selected = 0
		}
	case configCenterAddModelProvider:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		if len(ids) > 0 {
			state.draftModel = configv2.Model{Provider: ids[state.selected]}
			m.openConfigEdit(configEditNewModelID, ids[state.selected]+"/", "")
		}
	case configCenterModelActions:
		m.advanceModelAction(snapshot)
	case configCenterActive:
		if state.selected < len(state.catalogSelections) {
			m.activateConfigCenterCatalogSelection(state.catalogSelections[state.selected])
		}
	case configCenterCredentials:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		if len(ids) > 0 {
			state.targetID = ids[state.selected]
			state.page = configCenterCredentialActions
			state.selected = 0
		}
	case configCenterCredentialActions:
		m.advanceCredentialAction(snapshot)
	case configCenterDiagnostics:
		state.page = configCenterHome
		state.selected = 0
	}
	return m
}

func (m *appModel) advanceProviderAction(snapshot configv2.Snapshot) {
	state := m.configCenter
	provider, ok := snapshot.Document.Providers[state.targetID]
	if !ok {
		state.err = "provider changed externally; reopen the page"
		return
	}
	switch state.selected {
	case 0:
		resolved := provider
		m.openConfigEdit(configEditProviderEndpoint, resolved.Endpoint, "Endpoint")
	case 1:
		transports := []string{configv2.TransportOpenAIResponses, configv2.TransportOpenAICompatible, configv2.TransportAnthropicCompatible}
		current := 0
		for i, value := range transports {
			if value == provider.Transport {
				current = i
			}
		}
		provider.Transport = transports[(current+1)%len(transports)]
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
	case 2:
		value := ""
		if provider.TimeoutSeconds > 0 {
			value = strconv.Itoa(provider.TimeoutSeconds)
		}
		m.openConfigEdit(configEditProviderTimeout, value, "Timeout seconds (blank inherits preset)")
	case 3:
		value := ""
		if provider.Retries != nil {
			value = strconv.Itoa(*provider.Retries)
		}
		m.openConfigEdit(configEditProviderRetries, value, "Retries, 0-20 (blank inherits preset)")
	case 4:
		provider.Stream = cycleOptionalBool(provider.Stream)
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
	case 5:
		m.openConfigEdit(configEditProviderEnv, strings.Join(provider.Auth.Env, ", "), "Credential env candidates (ordered, comma-separated)")
	case 6:
		m.openConfigEdit(configEditProviderHeaders, marshalCompactObject(provider.Headers), "Headers JSONC")
	case 7:
		m.openConfigEdit(configEditProviderBody, marshalCompactObject(provider.Body), "Body JSONC")
	case 8:
		if !state.confirmDestructive("delete-provider:"+state.targetID, "Press Enter again to delete this provider and all of its models") {
			return
		}
		var operations []configv2.Operation
		for id, value := range snapshot.Document.Models {
			if value.Provider == state.targetID {
				operations = append(operations, configv2.DeleteModel(id))
			}
		}
		remaining := ""
		for id, value := range snapshot.Document.Models {
			if value.Provider != state.targetID {
				remaining = id
				break
			}
		}
		operations = append([]configv2.Operation{configv2.SetActiveModel(remaining)}, operations...)
		operations = append(operations, configv2.DeleteProvider(state.targetID))
		m.applyConfigOperations(operations...)
		if state.err == "" {
			state.page = configCenterProviders
			state.selected = 0
		}
	}
}

func (m *appModel) advanceModelAction(snapshot configv2.Snapshot) {
	state := m.configCenter
	selection := state.targetSelection
	if selection.ID == "" || selection.ID != state.targetID {
		var err error
		selection, err = snapshot.CatalogSelection(state.targetID)
		if err != nil {
			state.err = "model changed externally; reopen the page"
			return
		}
		state.targetSelection = selection
	}
	if selection.Source == configv2.ModelSourceDiscovered {
		if state.selected == 0 {
			m.activateConfigCenterCatalogSelection(selection)
			if state.err == "" {
				state.page = configCenterModels
				state.selected = 0
			}
		}
		return
	}
	configuredModel, ok := snapshot.Document.Models[state.targetID]
	if !ok {
		state.err = "model changed externally; reopen the page"
		return
	}
	switch state.selected {
	case 0:
		m.activateConfigCenterCatalogSelection(selection)
	case 1:
		m.openConfigEdit(configEditModelName, configuredModel.Name, "Upstream model name")
	case 2:
		adapters := []string{"", configv2.AdapterGPT, configv2.AdapterDeepSeek, configv2.AdapterOpenAICompatible}
		current := 0
		for i, value := range adapters {
			if value == configuredModel.Adapter {
				current = i
			}
		}
		configuredModel.Adapter = adapters[(current+1)%len(adapters)]
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 3:
		value := ""
		if configuredModel.ContextWindow > 0 {
			value = strconv.Itoa(configuredModel.ContextWindow)
		}
		m.openConfigEdit(configEditModelContextWindow, value, "Context window (blank inherits/defaults)")
	case 4:
		configuredModel.Stream = cycleOptionalBool(configuredModel.Stream)
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 5:
		configuredModel.Capabilities.Tools = cycleOptionalBool(configuredModel.Capabilities.Tools)
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 6:
		configuredModel.Capabilities.Vision = cycleOptionalBool(configuredModel.Capabilities.Vision)
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 7:
		configuredModel.Capabilities.Reasoning = cycleOptionalBool(configuredModel.Capabilities.Reasoning)
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 8:
		configuredModel.Capabilities.Attachment = cycleOptionalBool(configuredModel.Capabilities.Attachment)
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
	case 9:
		m.openConfigEdit(configEditModelParameters, marshalCompactObject(configuredModel.Parameters), "Parameters JSONC")
	case 10:
		if !state.confirmDestructive("delete-model:"+state.targetID, "Press Enter again to delete this model") {
			return
		}
		remaining := ""
		for id := range snapshot.Document.Models {
			if id != state.targetID {
				remaining = id
				break
			}
		}
		operations := []configv2.Operation{}
		if snapshot.ActiveModelID == state.targetID {
			operations = append(operations, configv2.SetActiveModel(remaining))
		}
		operations = append(operations, configv2.DeleteModel(state.targetID))
		m.applyConfigOperations(operations...)
		if state.err == "" {
			state.page = configCenterModels
			state.selected = 0
		}
	}
}

func (m *appModel) advanceCredentialAction(snapshot configv2.Snapshot) {
	state := m.configCenter
	provider := snapshot.Document.Providers[state.targetID]
	resolvedCredential := strings.TrimSpace(provider.Auth.Credential)
	if resolvedCredential == "" {
		if preset, ok := configv2.BuiltinPresets()[firstNonEmptyUI(provider.Preset, state.targetID)]; ok {
			resolvedCredential = preset.Provider.Auth.Credential
		}
	}
	if resolvedCredential == "" {
		resolvedCredential = "provider/" + state.targetID
	}
	state.credentialID = resolvedCredential
	switch state.selected {
	case 0:
		m.openConfigEdit(configEditCredential, "", "API key (masked)")
	case 1:
		if !state.confirmDestructive("delete-credential:"+state.targetID, "Press Enter again to delete this keyring credential") {
			return
		}
		store := m.configCenterController.CredentialStore()
		if store == nil {
			state.err = "system credential store is unavailable"
			return
		}
		active := snapshot.Document.Models[snapshot.ActiveModelID]
		if active.Provider == state.targetID && !providerHasEnvironmentCredential(state.targetID, provider) {
			state.err = "switch to another model or configure an environment credential before deleting the active key"
			return
		}
		if err := store.Delete(context.Background(), resolvedCredential); err != nil && !errors.Is(err, configv2.ErrCredentialNotFound) {
			state.err = err.Error()
			return
		}
		_ = m.configCenterController.ReloadConfig()
	}
}

func (state *configCenterState) confirmDestructive(action, message string) bool {
	if state.confirmAction != action {
		state.confirmAction = action
		state.err = message
		return false
	}
	state.confirmAction = ""
	return true
}

func (m *appModel) addPresetProvider(id string) {
	preset, ok := configv2.BuiltinPresets()[id]
	if !ok {
		return
	}
	snapshot := m.configCenterController.Snapshot()
	if _, exists := snapshot.Document.Providers[id]; exists {
		m.configCenter.err = "provider already exists"
		return
	}
	provider := preset.Provider
	provider.Preset = id
	operations := []configv2.Operation{configv2.UpsertProvider(id, provider)}
	modelID := preset.DefaultModelID
	if _, exists := snapshot.Document.Models[modelID]; exists {
		modelID = id + "/default"
	}
	configuredModel := preset.DefaultModel
	configuredModel.Provider = id
	operations = append(operations, configv2.UpsertModel(modelID, configuredModel))
	if snapshot.ActiveModelID == "" {
		operations = append(operations, configv2.SetActiveModel(modelID))
	}
	m.applyConfigOperations(operations...)
	if m.configCenter.err == "" {
		updated := m.configCenterController.Snapshot()
		if updated.Ready {
			m.configCenter.page = configCenterProviders
			m.configCenter.selected = 0
		} else {
			m.configCenter.page = configCenterCredentials
			m.configCenter.selected = sortedIndex(sortedProviderIDs(updated.Document.Providers), id)
		}
	}
}

func (m *appModel) openConfigEdit(kind configEditKind, value, label string) {
	state := m.configCenter
	state.page = configCenterEdit
	state.editKind = kind
	state.editValue = value
	state.err = label
}

func (m appModel) handleConfigEditKey(msg tea.KeyMsg) (appModel, tea.Cmd) {
	state := m.configCenter
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "esc":
		m.finishConfigEdit(false)
		return m, nil
	case "enter", "ctrl+s", "cmd+s", "command+s", "super+s":
		return m, m.saveConfigEdit()
	case "backspace":
		if len(state.editValue) > 0 {
			r := []rune(state.editValue)
			state.editValue = string(r[:len(r)-1])
		}
		return m, nil
	}
	if len(msg.Runes) > 0 && !msg.Alt {
		state.editValue += string(msg.Runes)
	}
	return m, nil
}

func (m *appModel) saveConfigEdit() tea.Cmd {
	state := m.configCenter
	if state == nil {
		return nil
	}
	kind := state.editKind
	beforeRevision := m.configCenterController.Snapshot().Revision
	m.finishConfigEdit(true)
	if state.err != "" || state.page == configCenterEdit {
		return nil
	}
	afterRevision := m.configCenterController.Snapshot().Revision
	if afterRevision == beforeRevision && kind != configEditCredential {
		return nil
	}
	return state.showSavedNotice()
}

func (m *appModel) finishConfigEdit(save bool) {
	state := m.configCenter
	if state == nil {
		return
	}
	kind := state.editKind
	value := strings.TrimSpace(state.editValue)
	back := func() {
		if kind == configEditCredential {
			state.editValue = ""
		}
		switch kind {
		case configEditProviderEndpoint, configEditProviderTimeout, configEditProviderRetries, configEditProviderEnv, configEditProviderHeaders, configEditProviderBody:
			state.page = configCenterProviderActions
		case configEditModelName, configEditModelContextWindow, configEditModelParameters:
			state.page = configCenterModelActions
		case configEditCredential:
			state.page = configCenterCredentialActions
		default:
			state.page = configCenterModels
		}
		state.editKind = configEditNone
		state.err = ""
	}
	if !save {
		back()
		return
	}
	snapshot := m.configCenterController.Snapshot()
	switch kind {
	case configEditProviderEndpoint:
		provider := snapshot.Document.Providers[state.targetID]
		provider.Endpoint = value
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditProviderTimeout:
		timeout, err := parseOptionalBoundedInt(value, 1, 3600, "timeout")
		if err != nil {
			state.err = err.Error()
			return
		}
		provider := snapshot.Document.Providers[state.targetID]
		provider.TimeoutSeconds = 0
		if timeout != nil {
			provider.TimeoutSeconds = *timeout
		}
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditProviderRetries:
		retries, err := parseOptionalBoundedInt(value, 0, 20, "retries")
		if err != nil {
			state.err = err.Error()
			return
		}
		provider := snapshot.Document.Providers[state.targetID]
		provider.Retries = retries
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditProviderEnv:
		provider := snapshot.Document.Providers[state.targetID]
		provider.Auth.Env = splitCredentialEnvCandidates(value)
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditProviderHeaders:
		object, err := configv2.DecodeJSONObject([]byte(value))
		if err != nil {
			state.err = err.Error()
			return
		}
		headers := map[string]string{}
		for key, item := range object {
			text, ok := item.(string)
			if !ok {
				state.err = "header values must be strings"
				return
			}
			headers[key] = text
		}
		provider := snapshot.Document.Providers[state.targetID]
		provider.Headers = headers
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditProviderBody:
		object, err := configv2.DecodeJSONObject([]byte(value))
		if err != nil {
			state.err = err.Error()
			return
		}
		provider := snapshot.Document.Providers[state.targetID]
		provider.Body = object
		m.applyConfigOperations(configv2.UpsertProvider(state.targetID, provider))
		if state.err == "" {
			back()
		}
	case configEditCustomProviderID:
		if value == "" || strings.ContainsAny(value, " /\\") {
			state.err = "provider ID must be non-empty and contain no spaces or slashes"
			return
		}
		state.draftProviderID = value
		m.openConfigEdit(configEditCustomProviderEndpoint, state.draftProvider.Endpoint, "Endpoint")
	case configEditCustomProviderEndpoint:
		state.draftProvider.Endpoint = value
		m.applyConfigOperations(configv2.UpsertProvider(state.draftProviderID, state.draftProvider))
		if state.err == "" {
			state.page = configCenterProviders
			state.editKind = configEditNone
		}
	case configEditModelName:
		configuredModel := snapshot.Document.Models[state.targetID]
		configuredModel.Name = value
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
		if state.err == "" {
			back()
		}
	case configEditModelContextWindow:
		contextWindow, err := parseOptionalBoundedInt(value, 1, int(^uint(0)>>1), "context window")
		if err != nil {
			state.err = err.Error()
			return
		}
		configuredModel := snapshot.Document.Models[state.targetID]
		configuredModel.ContextWindow = 0
		if contextWindow != nil {
			configuredModel.ContextWindow = *contextWindow
		}
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
		if state.err == "" {
			back()
		}
	case configEditModelParameters:
		object, err := configv2.DecodeJSONObject([]byte(value))
		if err != nil {
			state.err = err.Error()
			return
		}
		configuredModel := snapshot.Document.Models[state.targetID]
		configuredModel.Parameters = object
		m.applyConfigOperations(configv2.UpsertModel(state.targetID, configuredModel))
		if state.err == "" {
			back()
		}
	case configEditNewModelID:
		if value == "" || strings.ContainsAny(value, " \t\r\n\\") || !strings.Contains(value, "/") {
			state.err = "model ID must look like provider/model"
			return
		}
		if _, exists := snapshot.Document.Models[value]; exists {
			state.err = "model ID already exists"
			return
		}
		state.draftModelID = value
		m.openConfigEdit(configEditNewModelName, "", "Upstream model name")
	case configEditNewModelName:
		if value == "" {
			state.err = "model name cannot be empty"
			return
		}
		state.draftModel.Name = value
		m.applyConfigOperations(configv2.UpsertModel(state.draftModelID, state.draftModel))
		if state.err == "" {
			state.page = configCenterModels
			state.editKind = configEditNone
		}
	case configEditCredential:
		if value == "" {
			state.err = "credential cannot be empty"
			return
		}
		store := m.configCenterController.CredentialStore()
		if store == nil {
			state.err = "system credential store is unavailable"
			return
		}
		if err := store.Set(context.Background(), state.credentialID, value); err != nil {
			state.err = err.Error()
			return
		}
		configuredProvider := snapshot.Document.Providers[state.targetID]
		if strings.TrimSpace(configuredProvider.Auth.Credential) == "" {
			configuredProvider.Auth.Credential = state.credentialID
			m.applyConfigOperations(configv2.UpsertProvider(state.targetID, configuredProvider))
			if state.err != "" {
				return
			}
		} else {
			if err := m.configCenterController.ReloadConfig(); err != nil {
				state.err = err.Error()
				return
			}
			state.revision = m.configCenterController.Snapshot().Revision
		}
		back()
	}
}

func (m *appModel) applyConfigOperations(operations ...configv2.Operation) {
	state := m.configCenter
	if state == nil {
		return
	}
	snapshot, err := m.configCenterController.UpdateConfig(context.Background(), state.revision, operations)
	if err != nil {
		state.err = err.Error()
		return
	}
	state.revision = snapshot.Revision
	state.err = ""
	if refreshed, err := snapshot.CatalogSelection(state.targetID); err == nil {
		state.targetSelection = refreshed
	} else {
		state.targetSelection = configv2.CatalogSelection{}
	}
	state.invalidateCatalog()
}

func (m *appModel) activateConfigCenterCatalogSelection(selection configv2.CatalogSelection) {
	state := m.configCenter
	if state == nil {
		return
	}
	if err := m.configCenterController.ActivateCatalogSelection(selection); err != nil {
		state.err = err.Error()
		return
	}
	updated := m.configCenterController.Snapshot()
	state.revision = updated.Revision
	state.err = ""
	state.invalidateCatalog()
	if refreshed, err := updated.CatalogSelection(selection.ID); err == nil {
		state.targetSelection = refreshed
	}
	m.syncRunnerModelContextLimit(m.currentModelConfig())
}

func (state *configCenterState) invalidateCatalog() {
	if state == nil {
		return
	}
	state.catalogLoaded = false
	state.catalogSelections = nil
}

func (m appModel) configCenterCatalogSelections(snapshot configv2.Snapshot) []configv2.CatalogSelection {
	state := m.configCenter
	if state == nil {
		return nil
	}
	if state.catalogLoaded && state.catalogPage == state.page {
		return state.catalogSelections
	}
	ids := sortedCatalogModelIDs(snapshot.EffectiveModels)
	selections := make([]configv2.CatalogSelection, 0, len(ids))
	for _, id := range ids {
		selection, err := snapshot.CatalogSelection(id)
		if err == nil {
			selections = append(selections, selection)
		}
	}
	state.catalogPage = state.page
	state.catalogSelections = selections
	state.catalogLoaded = true
	return state.catalogSelections
}

func (m appModel) configCenterOptions() []configCenterOption {
	state := m.configCenter
	if state == nil {
		return nil
	}
	snapshot := m.configCenterController.Snapshot()
	switch state.page {
	case configCenterHome:
		return []configCenterOption{{"General settings", "UI and subagent defaults"}, {"Providers", fmt.Sprintf("%d configured", len(snapshot.Document.Providers))}, {"Models", fmt.Sprintf("registered=%d effective=%d", len(snapshot.Document.Models), len(snapshot.EffectiveModels))}, {"Active model", emptyLabel(snapshot.ActiveModelID)}, {"Credentials", "system keyring and env fallback"}, {"Diagnostics", fmt.Sprintf("revision %d · %d messages", snapshot.Revision, len(snapshot.Diagnostics))}}
	case configCenterProviders:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		result := make([]configCenterOption, 0, len(ids)+1)
		for _, id := range ids {
			p := snapshot.Document.Providers[id]
			result = append(result, configCenterOption{id, firstNonEmptyUI(p.Preset, p.Transport, p.Endpoint)})
		}
		return append(result, configCenterOption{"+ Add provider", "preset or custom"})
	case configCenterProviderActions:
		p := snapshot.Document.Providers[state.targetID]
		return []configCenterOption{{"Edit endpoint", firstNonEmptyUI(p.Endpoint, "preset default")}, {"Cycle transport", firstNonEmptyUI(p.Transport, "preset default")}, {"Edit timeout", optionalIntLabel(p.TimeoutSeconds, "inherit")}, {"Edit retries", optionalIntPointerLabel(p.Retries)}, {"Cycle stream", optionalBoolLabel(p.Stream)}, {"Edit credential env", firstNonEmptyUI(strings.Join(p.Auth.Env, ", "), "preset/inherit")}, {"Edit headers", "advanced JSONC"}, {"Edit body", "advanced JSONC"}, {"Delete provider", "also removes its models"}}
	case configCenterAddProvider:
		ids := sortedPresetIDs()
		result := make([]configCenterOption, 0, len(ids)+1)
		presets := configv2.BuiltinPresets()
		for _, id := range ids {
			result = append(result, configCenterOption{presets[id].Name, presets[id].Provider.Endpoint})
		}
		return append(result, configCenterOption{"Custom", "OpenAI-compatible endpoint"})
	case configCenterModels:
		selections := m.configCenterCatalogSelections(snapshot)
		result := make([]configCenterOption, 0, len(selections)+1)
		for _, selection := range selections {
			marker := ""
			if selection.ID == snapshot.ActiveModelID {
				marker = "active · "
			}
			description := fmt.Sprintf("%s%s · %s → %s", marker, selection.Source, selection.ProviderKey, selection.ModelName)
			result = append(result, configCenterOption{selection.ID, description})
		}
		return append(result, configCenterOption{"+ Add model", "choose provider, ID and upstream name"})
	case configCenterModelActions:
		selection := state.targetSelection
		if selection.ID == "" || selection.ID != state.targetID {
			selection, _ = snapshot.CatalogSelection(state.targetID)
		}
		if selection.Source == configv2.ModelSourceDiscovered {
			return []configCenterOption{{"Activate and register", state.targetID}}
		}
		v := snapshot.Document.Models[state.targetID]
		return []configCenterOption{{"Activate", state.targetID}, {"Edit upstream name", v.Name}, {"Cycle adapter", emptyLabel(v.Adapter)}, {"Edit context window", optionalIntLabel(v.ContextWindow, "inherit/default")}, {"Cycle stream", optionalBoolLabel(v.Stream)}, {"Cycle tools", optionalBoolLabel(v.Capabilities.Tools)}, {"Cycle vision", optionalBoolLabel(v.Capabilities.Vision)}, {"Cycle reasoning", optionalBoolLabel(v.Capabilities.Reasoning)}, {"Cycle attachments", optionalBoolLabel(v.Capabilities.Attachment)}, {"Edit parameters", "advanced JSONC"}, {"Delete model", v.Provider}}
	case configCenterAddModelProvider:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		result := make([]configCenterOption, 0, len(ids))
		for _, id := range ids {
			result = append(result, configCenterOption{id, "model provider"})
		}
		return result
	case configCenterActive:
		selections := m.configCenterCatalogSelections(snapshot)
		result := make([]configCenterOption, 0, len(selections))
		for _, selection := range selections {
			description := fmt.Sprintf("%s · %s", selection.Source, selection.ModelName)
			if selection.ID == snapshot.ActiveModelID {
				description = "active · " + description
			}
			result = append(result, configCenterOption{selection.ID, description})
		}
		return result
	case configCenterCredentials:
		ids := sortedProviderIDs(snapshot.Document.Providers)
		result := make([]configCenterOption, 0, len(ids))
		for _, id := range ids {
			p := snapshot.Document.Providers[id]
			source := p.Auth.Credential
			if source == "" && len(p.Auth.Env) > 0 {
				source = "env: " + strings.Join(p.Auth.Env, ", ")
			}
			if source == "" {
				source = "preset keyring/env"
			}
			result = append(result, configCenterOption{id, source})
		}
		return result
	case configCenterCredentialActions:
		return []configCenterOption{{"Set or replace", "secret will stay masked"}, {"Delete", "remove from system keyring"}}
	case configCenterDiagnostics:
		return []configCenterOption{{"Back", "revision and validation details are shown below"}}
	}
	return nil
}

func (m appModel) renderConfigCenterBox() string {
	state := m.configCenter
	if state == nil {
		return ""
	}
	var lines []string
	if state.page == configCenterEdit {
		label := "Edit value"
		if state.err != "" {
			label = state.err
		}
		display := state.editValue
		if state.editKind == configEditCredential {
			display = strings.Repeat("•", len([]rune(state.editValue)))
		}
		lines = []string{wizardTitleStyle.Render(label), "> " + display}
		if snapshot := m.configCenterController.Snapshot(); snapshot.Revision != state.revision {
			lines = append(lines, labelErrorStyle.Render(fmt.Sprintf("Draft is stale (opened at revision %d, current %d). Reload before saving.", state.revision, snapshot.Revision)))
		}
		if state.notice != "" {
			lines = append(lines, m.styles.StatusSuccess.Bold(true).Render("✓ "+state.notice))
		}
		lines = append(lines, "Ctrl+S or Enter save, esc cancel.")
		return m.renderModalPanel(strings.Join(lines, "\n"))
	}
	lines = append(lines, wizardTitleStyle.Render(configCenterTitle(state.page, state.targetID)))
	if snapshot := m.configCenterController.Snapshot(); snapshot.Revision != state.revision {
		lines = append(lines, labelErrorStyle.Render(fmt.Sprintf("External change detected: revision %d → %d", state.revision, snapshot.Revision)))
	}
	options := m.configCenterOptions()
	selected := state.selected
	if selected >= len(options) {
		selected = maxInt(0, len(options)-1)
	}
	maxItems := clampInt(m.currentLayout().transcriptHeight-8, 1, maxInt(1, len(options)))
	start := maxInt(0, selected-maxItems+1)
	end := minInt(len(options), start+maxItems)
	for i := start; i < end; i++ {
		text := "  " + options[i].label + "  " + options[i].description
		if i == selected {
			text = selectedProviderStyle.Render("> " + options[i].label + "  " + options[i].description)
		} else {
			text = unselectedProviderStyle.Render(text)
		}
		lines = append(lines, text)
	}
	if state.page == configCenterDiagnostics {
		snapshot := m.configCenterController.Snapshot()
		bodyWidth := m.modalPanelBodyWidth()
		lines = append(lines, wrapStyledCellText(fmt.Sprintf("path: %s\nrevision: %d  ready: %v\nregistered=%d effective=%d\n%s", m.configCenterController.ConfigPath(), snapshot.Revision, snapshot.Ready, len(snapshot.Document.Models), len(snapshot.EffectiveModels), discoveryStatusSummary(snapshot.Discovery, time.Now())), bodyWidth)...)
		for _, diagnostic := range snapshot.Diagnostics {
			lines = append(lines, wrapStyledCellText(fmt.Sprintf("[%s] %s %s", diagnostic.Severity, diagnostic.Field, diagnostic.Message), bodyWidth)...)
		}
	}
	if state.err != "" {
		lines = append(lines, labelErrorStyle.Render(state.err))
	}
	if state.notice != "" {
		lines = append(lines, m.styles.StatusSuccess.Bold(true).Render("✓ "+state.notice))
	}
	lines = append(lines, "Enter select, b back, esc close.")
	return m.renderModalPanel(strings.Join(lines, "\n"))
}

func configCenterTitle(page configCenterPage, target string) string {
	switch page {
	case configCenterHome:
		return "Paw configuration"
	case configCenterProviders:
		return "Providers"
	case configCenterProviderActions:
		return "Provider · " + target
	case configCenterAddProvider:
		return "Add provider"
	case configCenterModels:
		return "Models"
	case configCenterModelActions:
		return "Model · " + target
	case configCenterAddModelProvider:
		return "Choose model provider"
	case configCenterActive:
		return "Active model"
	case configCenterCredentials:
		return "Credentials"
	case configCenterCredentialActions:
		return "Credential · " + target
	case configCenterDiagnostics:
		return "Diagnostics"
	}
	return "Paw configuration"
}

func sortedProviderIDs(values map[string]configv2.Provider) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func sortedModelRegistryIDs(values map[string]configv2.Model) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func sortedCatalogModelIDs(values map[string]configv2.CatalogModel) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func sortedPresetIDs() []string {
	presets := configv2.BuiltinPresets()
	ids := make([]string, 0, len(presets))
	for id := range presets {
		if id == "custom" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func firstNonEmptyUI(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func marshalCompactObject(value any) string {
	if value == nil {
		return "{}"
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func hasBlockingDocumentDiagnostic(diagnostics []configv2.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if !strings.EqualFold(strings.TrimSpace(diagnostic.Severity), "error") {
			continue
		}
		field := strings.TrimSpace(diagnostic.Field)
		if strings.HasSuffix(field, ".auth") {
			continue
		}
		return true
	}
	return false
}

func sortedIndex(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return 0
}

func cycleOptionalBool(value *bool) *bool {
	if value == nil {
		next := true
		return &next
	}
	if *value {
		next := false
		return &next
	}
	return nil
}

func optionalBoolLabel(value *bool) string {
	if value == nil {
		return "inherit"
	}
	if *value {
		return "enabled"
	}
	return "disabled"
}

func optionalIntLabel(value int, fallback string) string {
	if value <= 0 {
		return fallback
	}
	return strconv.Itoa(value)
}

func optionalIntPointerLabel(value *int) string {
	if value == nil {
		return "inherit"
	}
	return strconv.Itoa(*value)
}

func parseOptionalBoundedInt(value string, minimum, maximum int, label string) (*int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return nil, fmt.Errorf("%s must be an integer between %d and %d", label, minimum, maximum)
	}
	return &parsed, nil
}

func splitCredentialEnvCandidates(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func providerHasEnvironmentCredential(providerID string, provider configv2.Provider) bool {
	envNames := append([]string(nil), provider.Auth.Env...)
	if len(envNames) == 0 {
		if preset, ok := configv2.BuiltinPresets()[firstNonEmptyUI(provider.Preset, providerID)]; ok {
			envNames = append(envNames, preset.Provider.Auth.Env...)
		}
	}
	for _, name := range envNames {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}
