package config

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// autoRegisterEnabled reports whether a provider opted into writing a
// successful discovery result back to config.jsonc. Absent or nil is off.
func autoRegisterEnabled(provider Provider) bool {
	return provider.Discovery != nil &&
		provider.Discovery.AutoRegister != nil &&
		*provider.Discovery.AutoRegister
}

// discoveryAutoRegistrationOps turns the live discovery results confirmed in
// this round into targeted config.jsonc operations. The semantics are
// "upstream list is authoritative": for every autoRegister provider whose
// discovery succeeded, models the upstream no longer serves are deleted and
// missing upstream models are added. When the active model itself is deleted,
// activeModel falls back to the lowest surviving model ID of that provider.
//
// confirmed holds the provider IDs whose live result was accepted in this
// round, so a failed refresh never rewrites config.jsonc from stale results.
func discoveryAutoRegistrationOps(document Document, activeID string, confirmed []string, retained map[string]retainedDiscovery) []Operation {
	providerIDs := make([]string, 0, len(confirmed))
	seen := make(map[string]bool, len(confirmed))
	for _, providerID := range confirmed {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" || seen[providerID] {
			continue
		}
		seen[providerID] = true
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	operations := make([]Operation, 0)
	active := strings.TrimSpace(activeID)
	for _, providerID := range providerIDs {
		configured, ok := document.Providers[providerID]
		if !ok {
			continue
		}
		resolved := mergePreset(providerID, configured)
		if !autoRegisterEnabled(resolved) {
			continue
		}
		discovery, ok := retained[providerID]
		if !ok {
			continue
		}

		names := discoveredModelNames(providerID, discovery.Models)
		if resolved.Discovery != nil {
			names, _ = filterDiscoveredModels(names, *resolved.Discovery)
		}
		desired := make(map[string]bool, len(names))
		for _, name := range names {
			desired[name] = true
		}

		existingIDs := make([]string, 0, len(document.Models))
		for id, model := range document.Models {
			if strings.TrimSpace(model.Provider) == providerID {
				existingIDs = append(existingIDs, id)
			}
		}
		sort.Strings(existingIDs)

		occupied := catalogOccupancy(document.Models)
		surviving := make([]string, 0, len(existingIDs)+len(names))
		removed := make(map[string]bool)
		kept := make(map[string]bool, len(names))

		// Upstream no longer serves these models: delete them outright.
		for _, id := range existingIDs {
			name := strings.TrimSpace(document.Models[id].Name)
			if desired[name] {
				kept[name] = true
				surviving = append(surviving, id)
				continue
			}
			removed[id] = true
			delete(occupied, id)
			operations = append(operations, DeleteModel(id))
		}

		// Upstream models that are missing locally: register them.
		for _, name := range names {
			if kept[name] {
				continue
			}
			id := stableDiscoveredModelID(providerID, name, occupied)
			model := builtinDiscoveredModel(providerID, name, resolved)
			operations = append(operations, UpsertModel(id, model))
			occupied[id] = CatalogModel{ID: id, Model: model, Source: ModelSourceDiscovered}
			kept[name] = true
			surviving = append(surviving, id)
		}

		if active != "" && removed[active] {
			sort.Strings(surviving)
			fallback := ""
			if len(surviving) > 0 {
				fallback = surviving[0]
			}
			operations = append(operations, SetActiveModel(fallback))
			active = fallback
		}
	}
	return operations
}

// discoveredModelNames normalizes the model names retained for one provider.
func discoveredModelNames(providerID string, models []DiscoveredModel) []string {
	seen := make(map[string]bool, len(models))
	names := make([]string, 0, len(models))
	for _, discovered := range models {
		if candidateProviderID := strings.TrimSpace(discovered.ProviderID); candidateProviderID != "" && candidateProviderID != providerID {
			continue
		}
		if unsafeDiscoveredModelName(discovered.Name) {
			continue
		}
		name := strings.TrimSpace(discovered.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func catalogOccupancy(models map[string]Model) map[string]CatalogModel {
	occupied := make(map[string]CatalogModel, len(models))
	for id, model := range models {
		occupied[id] = CatalogModel{ID: id, Model: cloneModel(model), Source: ModelSourceConfigured}
	}
	return occupied
}

// autoRegisterFromSnapshot writes the confirmed live discovery results back to
// config.jsonc and returns the resulting snapshot. Any failure keeps the base
// snapshot and records a warning diagnostic: auto-registration must never block
// startup or a refresh.
func (m *Manager) autoRegisterFromSnapshot(ctx context.Context, base Snapshot) (Snapshot, bool) {
	operations := discoveryAutoRegistrationOps(base.Document, base.ActiveModelID, m.confirmedDiscoveries, m.retainedDiscoveries)
	if len(operations) == 0 {
		m.clearAutoRegisterDiagnostics()
		return base, false
	}
	files, err := m.verifyConfigFilesUnchanged(base)
	if err != nil {
		m.retainAutoRegisterDiagnostic("model auto-registration could not verify the configuration files before writing")
		return base, false
	}
	raw, err := applyConfigOperations(base.Raw, operations)
	if err != nil {
		m.retainAutoRegisterDiagnostic("model auto-registration could not update the configuration: " + safeAutoRegisterError(err))
		return base, false
	}
	if _, _, err := parseAndValidateGlobal(raw, m.paths.GlobalConfig); err != nil {
		m.retainAutoRegisterDiagnostic("model auto-registration produced an invalid configuration; keeping the previous model list")
		return base, false
	}
	parsed, err := m.parseCandidateWithWorkspace(raw, files.workspace)
	if err != nil {
		m.retainAutoRegisterDiagnostic("model auto-registration produced an invalid configuration; keeping the previous model list")
		return base, false
	}
	candidate := m.candidateFromParsed(ctx, raw, parsed, base.Revision+1)
	if !candidate.Ready && base.Ready {
		m.retainAutoRegisterDiagnostic("model auto-registration would leave no usable active model; keeping the previous model list")
		return base, false
	}

	writer := m.configWriter
	if writer == nil {
		writer = atomicWriteConfigFileCAS
	}
	expectedGlobal := configFileState{exists: base.globalConfigExists, raw: append([]byte(nil), base.Raw...)}
	if err := writer(configCASWriteRequest{
		path:           m.paths.GlobalConfig,
		data:           raw,
		mode:           0o600,
		expectedGlobal: expectedGlobal,
		validateFileStates: func() error {
			_, err := m.verifyConfigFilesUnchanged(base)
			return err
		},
		beforeFinalValidation: m.configWriteHook,
	}); err != nil {
		m.retainAutoRegisterDiagnostic("model auto-registration could not write the configuration; keeping the previous model list")
		return base, false
	}
	m.clearAutoRegisterDiagnostics()
	return candidate, true
}

func safeAutoRegisterError(err error) string {
	if err == nil {
		return "unknown error"
	}
	if errors.Is(err, ErrRevisionConflict) {
		return "the configuration changed outside the manager"
	}
	return "the configuration could not be patched"
}

func (m *Manager) clearAutoRegisterDiagnostics() {
	m.autoRegisterDiagnostic = nil
}

func (m *Manager) retainAutoRegisterDiagnostic(message string) {
	m.autoRegisterDiagnostic = &Diagnostic{
		Severity: "warning",
		File:     m.paths.GlobalConfig,
		Message:  message,
	}
}

// appendAutoRegisterDiagnostic surfaces the current auto-registration warning
// on a snapshot that was built before the write attempt, so a failed write is
// visible in the same refresh that triggered it.
func (m *Manager) appendAutoRegisterDiagnostic(snapshot Snapshot) Snapshot {
	if m.autoRegisterDiagnostic == nil {
		return snapshot
	}
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Message == m.autoRegisterDiagnostic.Message {
			return snapshot
		}
	}
	snapshot.Diagnostics = append(append([]Diagnostic(nil), snapshot.Diagnostics...), *m.autoRegisterDiagnostic)
	return snapshot
}
