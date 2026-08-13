package config

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
)

type DiscoveredModel struct {
	ProviderID string
	Name       string
}

type CatalogStats struct {
	Discovered int
	Filtered   int
	Merged     int
}

type modelIdentity struct {
	providerID string
	name       string
}

func buildEffectiveCatalog(document Document, discovered map[string][]DiscoveredModel) (map[string]CatalogModel, CatalogStats) {
	catalog := make(map[string]CatalogModel, len(document.Models))
	occupied := make(map[string]CatalogModel, len(document.Models))
	configuredIdentities := make(map[modelIdentity]struct{}, len(document.Models))
	manualModels := make(map[modelIdentity]Model, len(document.Models))

	manualIDs := make([]string, 0, len(document.Models))
	for id := range document.Models {
		manualIDs = append(manualIDs, id)
	}
	sort.Strings(manualIDs)

	// replaceMode marks providers whose successful discovery supersedes their
	// hand-written model list. Manual models then serve only as an offline
	// fallback when discovery has never produced a result.
	replaceMode := make(map[string]bool)
	for providerID, provider := range document.Providers {
		resolved := mergePreset(providerID, provider)
		if resolved.Discovery == nil || resolved.Discovery.Enabled == nil || !*resolved.Discovery.Enabled {
			continue
		}
		if resolved.Discovery.Mode == DiscoveryModeReplace && len(discovered[providerID]) > 0 {
			replaceMode[strings.TrimSpace(providerID)] = true
		}
	}

	for _, id := range manualIDs {
		configured := cloneModel(document.Models[id])
		identity := identityForModel(configured)
		configuredIdentities[identity] = struct{}{}
		manualModels[identity] = configured
		if replaceMode[strings.TrimSpace(configured.Provider)] {
			continue
		}
		item := CatalogModel{ID: id, Model: configured, Source: ModelSourceConfigured}
		catalog[id] = item
		occupied[id] = item
	}

	providerIDs := make([]string, 0, len(document.Providers))
	for providerID := range document.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	stats := CatalogStats{}
	for _, providerID := range providerIDs {
		resolved := mergePreset(providerID, document.Providers[providerID])
		if resolved.Discovery == nil || resolved.Discovery.Enabled == nil || !*resolved.Discovery.Enabled {
			continue
		}
		cfg := *cloneDiscoveryConfig(resolved.Discovery)
		normalizedProviderID := strings.TrimSpace(providerID)
		supersedes := replaceMode[normalizedProviderID]
		names := make([]string, 0, len(discovered[providerID]))
		for _, candidate := range discovered[providerID] {
			candidateProviderID := strings.TrimSpace(candidate.ProviderID)
			if candidateProviderID != "" && candidateProviderID != normalizedProviderID {
				continue
			}
			names = append(names, candidate.Name)
		}
		filteredNames, filtered := filterDiscoveredModels(names, cfg)
		stats.Discovered += len(filteredNames) + filtered
		stats.Filtered += filtered
		for _, name := range filteredNames {
			model := builtinDiscoveredModel(providerID, name, resolved)
			identity := identityForModel(model)
			if manual, ok := manualModels[identity]; ok {
				model = inheritDiscoveredModelMetadata(model, manual)
			}
			if !supersedes {
				if _, manuallyConfigured := configuredIdentities[identity]; manuallyConfigured {
					continue
				}
			}
			id := stableDiscoveredModelID(providerID, name, occupied)
			item := CatalogModel{ID: id, Model: model, Source: ModelSourceDiscovered}
			catalog[id] = item
			occupied[id] = item
		}
	}

	stats.Merged = len(catalog)
	return catalog, stats
}

// inheritDiscoveredModelMetadata carries the metadata a user attached to a
// hand-written model over to its discovered counterpart. In replace mode the
// discovered list owns the model names, but the user's parameters, context
// window, capabilities, and adapter are still honored.
func inheritDiscoveredModelMetadata(discovered, manual Model) Model {
	if manual.Adapter != "" {
		discovered.Adapter = manual.Adapter
	}
	if discovered.ContextWindow == 0 {
		discovered.ContextWindow = manual.ContextWindow
	}
	if discovered.Stream == nil {
		discovered.Stream = cloneBoolPointer(manual.Stream)
	}
	if discovered.Capabilities.Tools == nil {
		discovered.Capabilities.Tools = cloneBoolPointer(manual.Capabilities.Tools)
	}
	if discovered.Capabilities.Vision == nil {
		discovered.Capabilities.Vision = cloneBoolPointer(manual.Capabilities.Vision)
	}
	if discovered.Capabilities.Reasoning == nil {
		discovered.Capabilities.Reasoning = cloneBoolPointer(manual.Capabilities.Reasoning)
	}
	if discovered.Capabilities.Attachment == nil {
		discovered.Capabilities.Attachment = cloneBoolPointer(manual.Capabilities.Attachment)
	}
	if discovered.Parameters == nil {
		discovered.Parameters = cloneAnyMap(manual.Parameters)
	}
	return discovered
}

func filterDiscoveredModels(names []string, cfg DiscoveryConfig) ([]string, int) {
	uniqueNormalized := make(map[string]struct{}, len(names))
	filtered := 0
	for _, rawName := range names {
		if unsafeDiscoveredModelName(rawName) {
			filtered++
			continue
		}
		name := strings.TrimSpace(rawName)
		if name == "" {
			filtered++
			continue
		}
		uniqueNormalized[name] = struct{}{}
	}
	normalized := make([]string, 0, len(uniqueNormalized))
	for name := range uniqueNormalized {
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)

	result := make([]string, 0, len(normalized))
	for _, name := range normalized {
		keep := !heuristicallyExcludedModel(name)
		if len(cfg.Include) > 0 {
			keep = matchesAnyModelGlob(cfg.Include, name)
		}
		if keep && matchesAnyModelGlob(cfg.Exclude, name) {
			keep = false
		}
		if keep {
			result = append(result, name)
		} else {
			filtered++
		}
	}
	return result, filtered
}

func unsafeDiscoveredModelName(name string) bool {
	if len(name) > 512 {
		return true
	}
	for _, current := range name {
		if unicode.IsControl(current) {
			return true
		}
	}
	return false
}

func stableDiscoveredModelID(providerID, modelName string, occupied map[string]CatalogModel) string {
	normalizedProviderID := strings.TrimSpace(providerID)
	modelName = strings.TrimSpace(modelName)
	base := normalizedProviderID + "/" + modelName
	identity := modelIdentity{providerID: providerID, name: modelName}
	if item, exists := occupied[base]; !exists || identityForModel(item.Model) == identity {
		return base
	}

	hash := sha256.Sum256([]byte(normalizedProviderID + "\x00" + modelName))
	hashed := fmt.Sprintf("%s~%x", base, hash[:4])
	if item, exists := occupied[hashed]; !exists || identityForModel(item.Model) == identity {
		return hashed
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s~%d", hashed, suffix)
		if item, exists := occupied[candidate]; !exists || identityForModel(item.Model) == identity {
			return candidate
		}
	}
}

func builtinDiscoveredModel(providerID, modelName string, provider Provider) Model {
	modelName = strings.TrimSpace(modelName)
	presetID := strings.TrimSpace(provider.Preset)
	if presetID == "" {
		presetID = strings.TrimSpace(providerID)
	}
	adapter := AdapterOpenAICompatible
	switch strings.ToLower(presetID) {
	case "deepseek":
		adapter = AdapterDeepSeek
	case "openai":
		adapter = AdapterGPT
	}
	return Model{Provider: providerID, Name: modelName, Adapter: adapter}
}

func validateModelGlob(pattern string) error {
	_, err := path.Match(modelGlobPath(pattern), "")
	return err
}

func matchModelGlob(pattern, value string) bool {
	matched, err := path.Match(modelGlobPath(pattern), modelGlobPath(value))
	return err == nil && matched
}

func modelGlobPath(value string) string {
	// path.Match deliberately prevents wildcards from crossing '/'. Discovery
	// globs define model names as a flat namespace, so encode slash as a normal
	// rune before matching while retaining path.Match's syntax validation.
	return strings.ReplaceAll(value, "/", "\U000F0000")
}

func matchesAnyModelGlob(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchModelGlob(pattern, value) {
			return true
		}
	}
	return false
}

func heuristicallyExcludedModel(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{
		"embedding", "rerank", "moderation", "transcrib", "text-to-speech",
		"dall-e", "stable-diffusion", "text-to-image", "image-generation", "gpt-image",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		switch token {
		case "embed", "speech", "tts", "whisper":
			return true
		}
	}
	return false
}

func identityForModel(model Model) modelIdentity {
	return modelIdentity{providerID: model.Provider, name: strings.TrimSpace(model.Name)}
}
