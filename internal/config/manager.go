package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"paw/internal/model"
)

type Options struct {
	Paths                 Paths
	PathOptions           PathOptions
	Credentials           CredentialStore
	DisableWatch          bool
	DisableModelDiscovery bool
	Discoverer            ModelDiscoverer
	Debounce              time.Duration
}

type Manager struct {
	paths                 Paths
	credentials           CredentialStore
	debounce              time.Duration
	discoverer            ModelDiscoverer
	disableModelDiscovery bool
	discoveredModels      map[string][]DiscoveredModel
	discoveryStatus       DiscoveryStatus

	mu          sync.RWMutex
	updateMu    sync.Mutex
	snapshot    Snapshot
	subscribers map[chan Snapshot]struct{}
	closed      bool

	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	done    chan struct{}
}

func Open(ctx context.Context, options Options) (*Manager, error) {
	paths := options.Paths
	if strings.TrimSpace(paths.Home) == "" {
		var err error
		paths, err = ResolvePaths(options.PathOptions)
		if err != nil {
			return nil, err
		}
	}
	if options.Credentials == nil {
		options.Credentials = OSCredentialStore{}
	}
	if options.Debounce <= 0 {
		options.Debounce = 180 * time.Millisecond
	}
	if options.Discoverer == nil {
		options.Discoverer = NewHTTPModelDiscoverer(nil)
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return nil, fmt.Errorf("create Paw config directory: %w", err)
	}
	if err := os.MkdirAll(paths.Skills, 0o700); err != nil {
		return nil, fmt.Errorf("create Paw skills directory: %w", err)
	}
	if err := ensureSchema(paths); err != nil {
		return nil, err
	}

	diagnostics := copyLegacyAssets(paths)
	_, migrationDiagnostics, migrationErr := migrateLegacy(ctx, paths, options.Credentials)
	if migrationErr != nil && !errors.Is(migrationErr, ErrCredentialMigrationBlocked) {
		return nil, migrationErr
	}
	diagnostics = append(diagnostics, migrationDiagnostics...)
	if migrationErr != nil {
		diagnostics = append(diagnostics, Diagnostic{Severity: "error", File: paths.LegacyConfig, Message: migrationErr.Error()})
	}
	if _, err := os.Stat(paths.GlobalConfig); os.IsNotExist(err) && migrationErr == nil {
		document, startupDiagnostics := firstRunDocument(ctx, paths, options.Credentials)
		diagnostics = append(diagnostics, startupDiagnostics...)
		raw, err := marshalStarter(document, "Paw configuration v2 — comments and trailing commas are supported")
		if err != nil {
			return nil, err
		}
		if err := atomicWriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
			return nil, fmt.Errorf("create starter config: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	manager := &Manager{
		paths:                 paths,
		credentials:           options.Credentials,
		debounce:              options.Debounce,
		discoverer:            options.Discoverer,
		disableModelDiscovery: options.DisableModelDiscovery,
		discoveredModels:      map[string][]DiscoveredModel{},
		subscribers:           map[chan Snapshot]struct{}{},
		done:                  make(chan struct{}),
	}
	var candidate Snapshot
	var loadErr error
	if migrationErr != nil {
		raw, _ := marshalStarter(emptyDocument(), "Migration is blocked until the legacy plaintext credential can be moved to a keyring")
		hash := sha256.Sum256(raw)
		candidate = Snapshot{Document: emptyDocument(), Revision: 1, ContentHash: hex.EncodeToString(hash[:]), Diagnostics: diagnostics, Ready: false, LoadedAt: time.Now(), Raw: raw}
		diagnostics = nil
	} else {
		candidate, loadErr = manager.loadCandidate(ctx, 1)
		if loadErr == nil {
			discoveryDiagnostics := manager.initializeModelDiscovery(ctx, candidate.Document, candidate.ActiveModelID)
			candidate, loadErr = manager.candidateFromRaw(ctx, candidate.Raw, 1)
			candidate.Diagnostics = append(discoveryDiagnostics, candidate.Diagnostics...)
		}
	}
	if loadErr != nil {
		raw, readErr := os.ReadFile(paths.GlobalConfig)
		if readErr != nil {
			return nil, loadErr
		}
		hash := sha256.Sum256(raw)
		candidate = Snapshot{Document: emptyDocument(), Revision: 1, ContentHash: hex.EncodeToString(hash[:]), Diagnostics: []Diagnostic{{Severity: "error", File: paths.GlobalConfig, Message: loadErr.Error()}}, Ready: false, LoadedAt: time.Now(), Raw: raw}
	}
	candidate.Diagnostics = append(diagnostics, candidate.Diagnostics...)
	manager.snapshot = candidate
	if !options.DisableWatch {
		if err := manager.startWatcher(ctx); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func ensureSchema(paths Paths) error {
	expected := SchemaBytes()
	installed, err := os.ReadFile(paths.Schema)
	if err == nil && bytes.Equal(installed, expected) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read installed config schema: %w", err)
	}
	if err := os.MkdirAll(paths.Schemas, 0o700); err != nil {
		return err
	}
	if err := atomicWriteFile(paths.Schema, expected, 0o600); err != nil {
		return fmt.Errorf("install config schema: %w", err)
	}
	return nil
}

func firstRunDocument(ctx context.Context, paths Paths, store CredentialStore) (Document, []Diagnostic) {
	requested := strings.TrimSpace(os.Getenv("PAW_MODEL"))
	if requested != "" {
		if document, ok := documentForRequestedModel(requested); ok {
			return document, []Diagnostic{{Severity: "info", File: paths.GlobalConfig, Message: "created from PAW_MODEL"}}
		}
		return emptyDocument(), []Diagnostic{{Severity: "error", File: paths.GlobalConfig, Field: "PAW_MODEL", Message: fmt.Sprintf("PAW_MODEL %q does not identify a built-in provider/model", requested)}}
	}
	candidates := detectedPresets(ctx, store)
	if len(candidates) == 1 {
		return documentForPreset(candidates[0]), []Diagnostic{{Severity: "info", File: paths.GlobalConfig, Message: "automatically selected the only credential-complete provider: " + candidates[0]}}
	}
	if len(candidates) > 1 {
		return emptyDocument(), []Diagnostic{{Severity: "warning", File: paths.GlobalConfig, Message: "multiple provider credentials were detected; choose a model in /config: " + strings.Join(candidates, ", ")}}
	}
	return emptyDocument(), []Diagnostic{{Severity: "warning", File: paths.GlobalConfig, Message: "no provider credentials were detected; add one in /config or set a provider API key environment variable"}}
}

func emptyDocument() Document {
	return Document{Schema: "./schemas/config-v2.schema.json", SchemaVersion: SchemaVersion, Providers: map[string]Provider{}, Models: map[string]Model{}}
}

func documentForPreset(id string) Document {
	preset := builtinPresets[id]
	provider := cloneProvider(preset.Provider)
	provider.Preset = id
	defaultModel := cloneModel(preset.DefaultModel)
	return Document{Schema: "./schemas/config-v2.schema.json", SchemaVersion: SchemaVersion, ActiveModel: preset.DefaultModelID, Providers: map[string]Provider{id: provider}, Models: map[string]Model{preset.DefaultModelID: defaultModel}}
}

func documentForRequestedModel(id string) (Document, bool) {
	parts := strings.SplitN(strings.TrimSpace(id), "/", 2)
	if len(parts) != 2 {
		return Document{}, false
	}
	preset, ok := builtinPresets[parts[0]]
	if !ok || strings.TrimSpace(parts[1]) == "" {
		return Document{}, false
	}
	document := documentForPreset(parts[0])
	configuredModel := document.Models[preset.DefaultModelID]
	delete(document.Models, preset.DefaultModelID)
	if id != preset.DefaultModelID {
		configuredModel.Name = parts[1]
	}
	document.Models[id] = configuredModel
	document.ActiveModel = id
	return document, true
}

func detectedPresets(ctx context.Context, store CredentialStore) []string {
	var candidates []string
	for id, preset := range builtinPresets {
		detected := false
		if preset.RequiresAuth {
			_, _, err := resolveCredential(ctx, store, preset.Provider.Auth)
			detected = err == nil
		} else {
			for _, name := range preset.DetectionEnv {
				if strings.TrimSpace(os.Getenv(name)) != "" {
					detected = true
					break
				}
			}
		}
		if detected {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func marshalStarter(document Document, comment string) ([]byte, error) {
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	header := "// " + strings.TrimSpace(comment) + "\n// Schema: ./schemas/config-v2.schema.json\n"
	return append(append([]byte(header), raw...), '\n'), nil
}

func (m *Manager) loadCandidate(ctx context.Context, revision uint64) (Snapshot, error) {
	raw, err := os.ReadFile(m.paths.GlobalConfig)
	if err != nil {
		return Snapshot{}, err
	}
	return m.candidateFromRaw(ctx, raw, revision)
}

func (m *Manager) initializeModelDiscovery(ctx context.Context, document Document, activeID string) []Diagnostic {
	cache, err := readDiscoveryCache(m.paths.ModelDiscoveryCache)
	diagnostics := []Diagnostic(nil)
	cacheState := "miss"
	if err != nil {
		cache = emptyDiscoveryCache()
		cacheState = "invalid"
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "warning",
			File:     m.paths.ModelDiscoveryCache,
			Message:  "model discovery cache could not be read; continuing without cached models",
		})
	}

	cachedModels, cacheProviders := m.matchingCachedModels(document, cache)
	m.discoveredModels = cachedModels
	if cacheProviders > 0 && cacheState != "invalid" {
		cacheState = "matched"
	}
	m.discoveryStatus = DiscoveryStatus{
		Source:         discoverySourceForCache(cacheProviders),
		CacheProviders: cacheProviders,
		CacheState:     cacheState,
	}
	defer m.refreshDiscoveryCounts(document)

	providerID, skippedReason := discoveryTarget(document, activeID)
	m.discoveryStatus.ProviderID = providerID
	if m.disableModelDiscovery {
		m.discoveryStatus.SkippedReason = "disabled"
		return diagnostics
	}
	if skippedReason != "" {
		m.discoveryStatus.SkippedReason = skippedReason
		return diagnostics
	}

	provider := mergePreset(providerID, document.Providers[providerID])
	if provider.Discovery == nil || provider.Discovery.Enabled == nil || !*provider.Discovery.Enabled {
		m.discoveryStatus.SkippedReason = "provider-disabled"
		return diagnostics
	}

	attemptedAt := time.Now().UTC()
	m.discoveryStatus.Attempted = true
	m.discoveryStatus.AttemptedAt = attemptedAt
	credential := ""
	if provider.Auth.Credential != "" || len(provider.Auth.Env) > 0 {
		credential, _, err = resolveCredential(ctx, m.credentials, provider.Auth)
		if err != nil {
			m.discoveryStatus.LastError = "model discovery credential is unavailable"
			diagnostics = append(diagnostics, m.discoveryFailureDiagnostic(providerID))
			return diagnostics
		}
	}
	if m.discoverer == nil {
		m.discoveryStatus.LastError = "model discovery is unavailable"
		diagnostics = append(diagnostics, m.discoveryFailureDiagnostic(providerID))
		return diagnostics
	}

	models, err := m.discoverer.Discover(ctx, providerID, provider, credential)
	if err != nil {
		m.discoveryStatus.LastError = safeModelDiscoveryError(err)
		diagnostics = append(diagnostics, m.discoveryFailureDiagnostic(providerID))
		return diagnostics
	}

	models, cacheNames := normalizeLiveDiscoveredModels(providerID, models)
	m.discoveredModels[providerID] = models
	succeededAt := time.Now().UTC()
	m.discoveryStatus.Source = "live"
	m.discoveryStatus.SucceededAt = succeededAt
	m.discoveryStatus.DiscoveredAt = succeededAt
	m.discoveryStatus.LastError = ""

	cache.Providers[providerID] = discoveryCacheEntry{
		EndpointFingerprint: discoveryEndpointFingerprint(provider),
		Format:              provider.Discovery.Format,
		DiscoveredAt:        succeededAt,
		Models:              cacheNames,
	}
	if err := writeDiscoveryCache(m.paths.ModelDiscoveryCache, cache); err != nil {
		m.discoveryStatus.CacheState = "write-failed"
		diagnostics = append(diagnostics, Diagnostic{
			Severity: "warning",
			File:     m.paths.ModelDiscoveryCache,
			Message:  "live model discovery succeeded, but its cache could not be updated",
		})
		return diagnostics
	}
	_, m.discoveryStatus.CacheProviders = m.matchingCachedModels(document, cache)
	m.discoveryStatus.CacheState = "updated"
	return diagnostics
}

func discoveryTarget(document Document, activeID string) (providerID, skippedReason string) {
	activeID = strings.TrimSpace(activeID)
	if activeID != "" {
		configuredModel, ok := document.Models[activeID]
		if !ok {
			return "", "active-model-missing"
		}
		providerID = strings.TrimSpace(configuredModel.Provider)
		if _, ok := document.Providers[providerID]; !ok {
			return "", "active-provider-missing"
		}
		return providerID, ""
	}
	if len(document.Providers) == 0 {
		return "", "no-provider"
	}
	if len(document.Providers) > 1 {
		return "", "multiple-providers-without-active-model"
	}
	for providerID := range document.Providers {
		return providerID, ""
	}
	return "", "no-provider"
}

func (m *Manager) matchingCachedModels(document Document, cache discoveryCacheFile) (map[string][]DiscoveredModel, int) {
	matched := make(map[string][]DiscoveredModel)
	providerIDs := make([]string, 0, len(document.Providers))
	for providerID := range document.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		provider := mergePreset(providerID, document.Providers[providerID])
		if provider.Discovery == nil || provider.Discovery.Enabled == nil || !*provider.Discovery.Enabled {
			continue
		}
		entry, ok := matchingCacheEntry(cache, providerID, discoveryEndpointFingerprint(provider), provider.Discovery.Format)
		if !ok {
			continue
		}
		models := make([]DiscoveredModel, len(entry.Models))
		for index, name := range entry.Models {
			models[index] = DiscoveredModel{ProviderID: providerID, Name: name}
		}
		matched[providerID] = models
	}
	return matched, len(matched)
}

func (m *Manager) discoveryFailureDiagnostic(providerID string) Diagnostic {
	fallback := "manual models"
	if _, ok := m.discoveredModels[providerID]; ok {
		m.discoveryStatus.Source = "cache"
		fallback = "matching cached models"
	} else if m.discoveryStatus.CacheProviders > 0 {
		m.discoveryStatus.Source = "cache"
	} else {
		m.discoveryStatus.Source = "manual-only"
	}
	detail := strings.TrimSpace(m.discoveryStatus.LastError)
	if detail == "" {
		detail = "model discovery failed"
	}
	return Diagnostic{
		Severity: "warning",
		File:     m.paths.GlobalConfig,
		Field:    "providers." + providerID + ".discovery",
		Message:  detail + "; continuing with " + fallback,
	}
}

func (m *Manager) refreshDiscoveryCounts(document Document) {
	_, stats := buildEffectiveCatalog(document, m.discoveredModels)
	m.discoveryStatus.DiscoveredCount = stats.Discovered
	m.discoveryStatus.FilteredCount = stats.Filtered
	m.discoveryStatus.EffectiveCount = stats.Merged
}

func discoverySourceForCache(cacheProviders int) string {
	if cacheProviders > 0 {
		return "cache"
	}
	return "manual-only"
}

func safeModelDiscoveryError(err error) string {
	var discoveryError *DiscoveryError
	if errors.As(err, &discoveryError) {
		return discoveryError.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "model discovery failed: kind=canceled: request was canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "model discovery failed: kind=timeout: request timed out"
	}
	return "model discovery failed"
}

func normalizeLiveDiscoveredModels(providerID string, models []DiscoveredModel) ([]DiscoveredModel, []string) {
	normalized := make([]DiscoveredModel, 0, len(models))
	cacheNames := make([]string, 0, len(models))
	for _, discovered := range models {
		candidateProviderID := strings.TrimSpace(discovered.ProviderID)
		if candidateProviderID != "" && candidateProviderID != providerID {
			continue
		}
		discovered.ProviderID = providerID
		normalized = append(normalized, discovered)
		if unsafeDiscoveredModelName(discovered.Name) {
			continue
		}
		name := strings.TrimSpace(discovered.Name)
		if name != "" {
			cacheNames = append(cacheNames, name)
		}
	}
	canonicalNames, err := canonicalDiscoveryCacheModels(cacheNames)
	if err != nil {
		canonicalNames = []string{}
	}
	return normalized, canonicalNames
}

func (m *Manager) candidateFromRaw(ctx context.Context, raw []byte, revision uint64) (Snapshot, error) {
	document, diagnostics, err := parseAndValidateGlobal(raw, m.paths.GlobalConfig)
	if err != nil {
		return Snapshot{}, err
	}
	workspace := WorkspaceDocument{}
	workspaceRaw := []byte(nil)
	if m.paths.WorkspaceConfig != "" {
		workspaceRaw, err = os.ReadFile(m.paths.WorkspaceConfig)
		if err == nil {
			workspace, err = parseAndValidateWorkspace(workspaceRaw, m.paths.WorkspaceConfig, document)
			if err != nil {
				return Snapshot{}, err
			}
		} else if !os.IsNotExist(err) {
			return Snapshot{}, err
		}
	}
	activeID := strings.TrimSpace(document.ActiveModel)
	if workspace.ActiveModel != "" {
		activeID = workspace.ActiveModel
	}
	if override := strings.TrimSpace(os.Getenv("PAW_MODEL")); override != "" {
		if _, ok := document.Models[override]; !ok {
			return Snapshot{}, fmt.Errorf("PAW_MODEL references missing model %q", override)
		}
		activeID = override
	}
	catalog, stats := buildEffectiveCatalog(document, m.discoveredModels)
	runtimeConfig, ready, runtimeDiagnostics := m.runtimeConfig(ctx, document, workspace, activeID, catalog)
	diagnostics = append(diagnostics, runtimeDiagnostics...)
	discoveryStatus := m.discoveryStatus
	discoveryStatus.DiscoveredCount = stats.Discovered
	discoveryStatus.FilteredCount = stats.Filtered
	discoveryStatus = withEffectiveCount(discoveryStatus, stats.Merged)
	hash := sha256.Sum256(append(append(append([]byte(nil), raw...), 0), workspaceRaw...))
	return Snapshot{Document: document, Workspace: workspace, Active: runtimeConfig, ActiveModelID: activeID, EffectiveModels: catalog, Discovery: discoveryStatus, Revision: revision, ContentHash: hex.EncodeToString(hash[:]), Diagnostics: diagnostics, Ready: ready, LoadedAt: time.Now(), Raw: raw}, nil
}

func withEffectiveCount(status DiscoveryStatus, count int) DiscoveryStatus {
	status.EffectiveCount = count
	return status
}

func (m *Manager) runtimeConfig(ctx context.Context, document Document, workspace WorkspaceDocument, activeID string, catalog map[string]CatalogModel) (model.Config, bool, []Diagnostic) {
	if activeID == "" {
		return model.Config{}, false, []Diagnostic{{Severity: "warning", File: m.paths.GlobalConfig, Field: "activeModel", Message: "choose an active model"}}
	}
	catalogModel, ok := catalog[activeID]
	if !ok {
		return model.Config{}, false, []Diagnostic{{Severity: "error", File: m.paths.GlobalConfig, Field: "activeModel", Message: "active model is missing"}}
	}
	configuredModel := cloneModel(catalogModel.Model)
	if override, ok := workspace.Models[activeID]; ok {
		if override.Stream != nil {
			configuredModel.Stream = override.Stream
		}
		if override.Parameters != nil {
			configuredModel.Parameters = mergeAnyMaps(configuredModel.Parameters, override.Parameters)
		}
	}
	provider := mergePreset(configuredModel.Provider, document.Providers[configuredModel.Provider])
	secret := ""
	if provider.Auth.Credential != "" || len(provider.Auth.Env) > 0 {
		value, source, err := resolveCredential(ctx, m.credentials, provider.Auth)
		if err != nil {
			return model.Config{}, false, []Diagnostic{{Severity: "error", File: m.paths.GlobalConfig, Field: "providers." + configuredModel.Provider + ".auth", Message: "active provider credential is unavailable"}}
		}
		secret = value
		_ = source
	}
	stream := true
	if provider.Stream != nil {
		stream = *provider.Stream
	}
	if configuredModel.Stream != nil {
		stream = *configuredModel.Stream
	}
	timeout := provider.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	retries := 3
	if provider.Retries != nil {
		retries = *provider.Retries
	}
	requestConfig := model.Config{
		Provider: configuredModel.Provider, Transport: provider.Transport, Adapter: configuredModel.Adapter,
		ProfileID: configuredModel.Provider, ProfileName: configuredModel.Provider,
		APIBaseURL: strings.TrimRight(provider.Endpoint, "/"), APIPath: provider.APIPath,
		APIKey: secret, Headers: cloneStringMap(provider.Headers), Model: configuredModel.Name,
		ExtraBody: model.RequestBody(cloneAnyMap(provider.Body)), ContextLimitTokens: configuredModel.ContextWindow,
		Timeout: time.Duration(timeout) * time.Second, RetryCount: retries, RetryCountSet: provider.Retries != nil, Stream: stream, StreamSet: provider.Stream != nil || configuredModel.Stream != nil,
	}
	if requestConfig.APIPath == "" {
		switch provider.Transport {
		case TransportOpenAIResponses:
			requestConfig.APIPath = "/responses"
		case TransportAnthropicCompatible:
			requestConfig.APIPath = "/messages"
		default:
			requestConfig.APIPath = "/chat/completions"
		}
	}
	requestConfig.Profiles = synthesizeProfiles(document, catalog, m.credentials, ctx)
	if models := modelsForProvider(catalog, configuredModel.Provider); len(models) > 0 {
		requestConfig.Models = models
	}
	requestConfig.ModelExtraBody = map[string]model.RequestBody{}
	requestConfig.ModelContextLimitTokens = map[string]int{}
	for _, catalogModel := range catalogModelsForProvider(catalog, configuredModel.Provider) {
		value := catalogModel.Model
		if value.Parameters != nil {
			requestConfig.ModelExtraBody[value.Name] = model.RequestBody(cloneAnyMap(value.Parameters))
		}
		requestConfig.ModelContextLimitTokens[value.Name] = value.ContextWindow
	}
	if configuredModel.Parameters != nil {
		requestConfig.ModelExtraBody[configuredModel.Name] = model.RequestBody(cloneAnyMap(configuredModel.Parameters))
	}
	requestConfig.ModelContextLimitTokens[configuredModel.Name] = configuredModel.ContextWindow
	return requestConfig, true, nil
}

func synthesizeProfiles(document Document, catalog map[string]CatalogModel, store CredentialStore, ctx context.Context) []model.Profile {
	providerIDs := make([]string, 0, len(document.Providers))
	for id := range document.Providers {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	profiles := make([]model.Profile, 0, len(providerIDs))
	for _, id := range providerIDs {
		provider := mergePreset(id, document.Providers[id])
		secret, _, _ := resolveCredential(ctx, store, provider.Auth)
		retries := 3
		if provider.Retries != nil {
			retries = *provider.Retries
		}
		profile := model.Profile{ID: id, Name: id, Provider: id, Transport: provider.Transport, APIBaseURL: strings.TrimRight(provider.Endpoint, "/"), APIPath: provider.APIPath, APIKey: secret, Headers: cloneStringMap(provider.Headers), ExtraBody: model.RequestBody(cloneAnyMap(provider.Body)), Timeout: time.Duration(provider.TimeoutSeconds) * time.Second, RetryCount: retries, RetryCountSet: provider.Retries != nil, Stream: true, StreamSet: provider.Stream != nil, CredentialID: provider.Auth.Credential}
		if provider.Stream != nil {
			profile.Stream = *provider.Stream
		}
		profile.Models = modelsForProvider(catalog, id)
		profile.ModelExtraBody = map[string]model.RequestBody{}
		profile.ModelContextLimitTokens = map[string]int{}
		for _, catalogModel := range catalogModelsForProvider(catalog, id) {
			configuredModel := catalogModel.Model
			if configuredModel.Parameters != nil {
				profile.ModelExtraBody[configuredModel.Name] = model.RequestBody(cloneAnyMap(configuredModel.Parameters))
			}
			profile.ModelContextLimitTokens[configuredModel.Name] = configuredModel.ContextWindow
		}
		if len(profile.Models) > 0 {
			profile.Model = profile.Models[0]
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

func modelsForProvider(catalog map[string]CatalogModel, providerID string) []string {
	values := make([]string, 0)
	seen := map[string]bool{}
	for _, catalogModel := range catalogModelsForProvider(catalog, providerID) {
		value := catalogModel.Model
		if !seen[value.Name] {
			values = append(values, value.Name)
			seen[value.Name] = true
		}
	}
	sort.Strings(values)
	return values
}

func catalogModelsForProvider(catalog map[string]CatalogModel, providerID string) []CatalogModel {
	ids := make([]string, 0, len(catalog))
	for id, catalogModel := range catalog {
		if catalogModel.Model.Provider == providerID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	models := make([]CatalogModel, 0, len(ids))
	for _, id := range ids {
		models = append(models, catalog[id])
	}
	return models
}

func mergeAnyMaps(base, override map[string]any) map[string]any {
	result := cloneAnyMap(base)
	if result == nil {
		result = map[string]any{}
	}
	for key, value := range override {
		result[key] = value
	}
	return result
}

func (m *Manager) Snapshot() Snapshot { m.mu.RLock(); defer m.mu.RUnlock(); return m.snapshot.Clone() }
func (m *Manager) ConfigPath() string { return m.paths.GlobalConfig }
func (m *Manager) Paths() Paths       { return m.paths }

func (m *Manager) RequireReady() error {
	snapshot := m.Snapshot()
	if snapshot.Ready {
		return nil
	}
	reason := "configuration is incomplete"
	if len(snapshot.Diagnostics) > 0 {
		reason = snapshot.Diagnostics[len(snapshot.Diagnostics)-1].Message
	}
	return &SetupRequiredError{Path: m.paths.GlobalConfig, Reason: reason}
}

func (m *Manager) Update(ctx context.Context, expectedRevision uint64, operations []Operation) (Snapshot, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Snapshot{}, errors.New("configuration manager is closed")
	}
	if expectedRevision != m.snapshot.Revision {
		current := m.snapshot.Clone()
		m.mu.Unlock()
		return current, fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, expectedRevision, current.Revision)
	}
	raw := append([]byte(nil), m.snapshot.Raw...)
	currentRevision := m.snapshot.Revision
	m.mu.Unlock()
	var err error
	for _, operation := range operations {
		switch operation.Kind {
		case OperationSetActiveModel:
			raw, err = patchJSONCMember(raw, nil, "activeModel", operation.Value, false)
		case OperationUpsertProvider:
			raw, err = patchJSONCObjectMemberFields(raw, []string{"providers"}, operation.ID, operation.Value, []string{"preset", "transport", "endpoint", "apiPath", "auth", "headers", "body", "timeoutSeconds", "retries", "stream", "discovery"})
		case OperationDeleteProvider:
			raw, err = patchJSONCMember(raw, []string{"providers"}, operation.ID, nil, true)
		case OperationUpsertModel:
			raw, err = patchJSONCObjectMemberFields(raw, []string{"models"}, operation.ID, operation.Value, []string{"provider", "name", "adapter", "contextWindow", "stream", "capabilities", "parameters"})
		case OperationDeleteModel:
			raw, err = patchJSONCMember(raw, []string{"models"}, operation.ID, nil, true)
		default:
			err = fmt.Errorf("unsupported config operation %q", operation.Kind)
		}
		if err != nil {
			return Snapshot{}, err
		}
	}
	if _, _, err := parseAndValidateGlobal(raw, m.paths.GlobalConfig); err != nil {
		return Snapshot{}, err
	}
	candidate, err := m.candidateFromRaw(ctx, raw, currentRevision+1)
	if err != nil {
		return Snapshot{}, err
	}
	if !candidate.Ready && m.Snapshot().Ready {
		return Snapshot{}, fmt.Errorf("updated configuration does not have a usable active credential")
	}
	if err := atomicWriteFile(m.paths.GlobalConfig, raw, 0o600); err != nil {
		return Snapshot{}, err
	}
	m.publish(candidate)
	return candidate.Clone(), nil
}

func (m *Manager) Reload() error { return m.reload(context.Background(), true, true) }

func (m *Manager) reload(ctx context.Context, recordFailure, force bool) error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	current := m.Snapshot()
	if _, err := os.Stat(m.paths.GlobalConfig); err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("configuration file was deleted; keeping last-known-good snapshot")
		}
		if recordFailure {
			m.recordDiagnostic(err)
		}
		return err
	}
	candidate, err := m.loadCandidate(ctx, current.Revision+1)
	if err != nil {
		if recordFailure {
			m.recordDiagnostic(err)
		}
		return err
	}
	if candidate.ContentHash == current.ContentHash && !force {
		return nil
	}
	if !candidate.Ready && current.Ready {
		err = fmt.Errorf("reloaded configuration has no usable active credential; keeping last-known-good snapshot")
		if recordFailure {
			m.recordDiagnostic(err)
		}
		return err
	}
	m.publish(candidate)
	return nil
}

func (m *Manager) Subscribe() (<-chan Snapshot, func()) {
	channel := make(chan Snapshot, 1)
	m.mu.Lock()
	if !m.closed {
		m.subscribers[channel] = struct{}{}
		channel <- m.snapshot.Clone()
	} else {
		close(channel)
	}
	m.mu.Unlock()
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			m.mu.Lock()
			if _, ok := m.subscribers[channel]; ok {
				delete(m.subscribers, channel)
				close(channel)
			}
			m.mu.Unlock()
		})
	}
}

func (m *Manager) publish(snapshot Snapshot) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.snapshot = snapshot.Clone()
	for channel := range m.subscribers {
		select {
		case channel <- snapshot.Clone():
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- snapshot.Clone():
			default:
			}
		}
	}
	m.mu.Unlock()
}

func (m *Manager) recordDiagnostic(err error) {
	m.mu.Lock()
	snapshot := m.snapshot.Clone()
	snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{Severity: "error", File: m.paths.GlobalConfig, Message: err.Error()})
	m.snapshot = snapshot
	for channel := range m.subscribers {
		select {
		case channel <- snapshot.Clone():
		default:
		}
	}
	m.mu.Unlock()
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	watcher := m.watcher
	m.mu.Unlock()
	if watcher != nil {
		_ = watcher.Close()
		<-m.done
	}
	m.mu.Lock()
	for channel := range m.subscribers {
		close(channel)
		delete(m.subscribers, channel)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) startWatcher(parent context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	directories := map[string]bool{filepath.Dir(m.paths.GlobalConfig): true}
	if m.paths.WorkspaceConfig != "" {
		directory := filepath.Dir(m.paths.WorkspaceConfig)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			_ = watcher.Close()
			return err
		}
		directories[directory] = true
	}
	for directory := range directories {
		if err := watcher.Add(directory); err != nil {
			_ = watcher.Close()
			return fmt.Errorf("watch config directory %s: %w", directory, err)
		}
	}
	ctx, cancel := context.WithCancel(parent)
	m.watcher = watcher
	m.cancel = cancel
	go m.watchLoop(ctx)
	return nil
}

func (m *Manager) watchLoop(ctx context.Context) {
	defer close(m.done)
	var timer *time.Timer
	var timerChannel <-chan time.Time
	schedule := func() {
		if timer == nil {
			timer = time.NewTimer(m.debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(m.debounce)
		}
		timerChannel = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if sameConfigPath(event.Name, m.paths.GlobalConfig) || sameConfigPath(event.Name, m.paths.WorkspaceConfig) {
				schedule()
			}
		case _, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
		case <-timerChannel:
			timerChannel = nil
			_ = m.reload(context.Background(), true, false)
		}
	}
}

func sameConfigPath(left, right string) bool {
	if right == "" {
		return false
	}
	leftAbs, _ := filepath.Abs(left)
	rightAbs, _ := filepath.Abs(right)
	return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}
