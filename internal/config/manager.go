package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

type retainedDiscovery struct {
	Models              []DiscoveredModel
	EndpointFingerprint string
	Format              string
	DiscoveredAt        time.Time
	Origin              string
	FilteredCount       int
}

type pendingDiscovery struct {
	ProviderID  string
	Discovery   retainedDiscovery
	CacheModels []string
}

type retainedDiscoveryDiagnostic struct {
	Diagnostic          Diagnostic
	Kind                string
	ProviderID          string
	EndpointFingerprint string
	Format              string
}

const (
	discoveryDiagnosticCacheRead = "cache-read"
	discoveryDiagnosticTarget    = "target"
)

type Manager struct {
	paths                       Paths
	credentials                 CredentialStore
	debounce                    time.Duration
	discoverer                  ModelDiscoverer
	discoveryCacheWriter        func(string, discoveryCacheFile) error
	configWriter                configCASWriter
	configWriteHook             func() error
	disableModelDiscovery       bool
	retainedDiscoveries         map[string]retainedDiscovery
	pendingDiscovery            *pendingDiscovery
	discoveryCache              discoveryCacheFile
	discoveryCacheInvalid       bool
	discoveryStatus             DiscoveryStatus
	discoveryTargetFingerprint  string
	discoveryTargetFormat       string
	startupDiscoveryDiagnostics []retainedDiscoveryDiagnostic

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
	return openManager(ctx, options, writeDiscoveryCache)
}

func openManager(ctx context.Context, options Options, cacheWriter func(string, discoveryCacheFile) error) (*Manager, error) {
	if cacheWriter == nil {
		cacheWriter = writeDiscoveryCache
	}
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
		options.Discoverer = NewHTTPModelDiscoverer(http.DefaultClient)
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
		if err := atomicWriteNewConfigFile(paths.GlobalConfig, raw, 0o600); err != nil {
			if !errors.Is(err, ErrRevisionConflict) {
				return nil, fmt.Errorf("create starter config: %w", err)
			}
			if _, statErr := os.Stat(paths.GlobalConfig); statErr != nil {
				return nil, fmt.Errorf("create starter config: %w", err)
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	manager := &Manager{
		paths:                 paths,
		credentials:           options.Credentials,
		debounce:              options.Debounce,
		discoverer:            options.Discoverer,
		discoveryCacheWriter:  cacheWriter,
		configWriter:          atomicWriteConfigFileCAS,
		disableModelDiscovery: options.DisableModelDiscovery,
		retainedDiscoveries:   map[string]retainedDiscovery{},
		discoveryCache:        emptyDiscoveryCache(),
		subscribers:           map[chan Snapshot]struct{}{},
		done:                  make(chan struct{}),
	}
	manager.loadModelDiscoveryCache()
	var candidate Snapshot
	var loadErr error
	if migrationErr != nil {
		raw, _ := marshalStarter(emptyDocument(), "Migration is blocked until the legacy plaintext credential can be moved to a keyring")
		hash := sha256.Sum256(raw)
		files, _ := manager.readConfigFileStates()
		candidate = Snapshot{
			Document: emptyDocument(), Revision: 1, ContentHash: hex.EncodeToString(hash[:]),
			Diagnostics: diagnostics, Ready: false, LoadedAt: time.Now(), Raw: raw,
			globalConfigExists: files.global.exists, workspaceConfigExists: files.workspace.exists,
			workspaceRaw: append([]byte(nil), files.workspace.raw...),
		}
		diagnostics = nil
	} else {
		document, activeID, err := manager.loadDiscoverySelection()
		if err == nil {
			manager.initializeModelDiscovery(ctx, document, activeID)
		}
	}

	var watchContext context.Context
	if !options.DisableWatch {
		var err error
		watchContext, err = manager.registerWatcher(ctx)
		if err != nil {
			return nil, err
		}
	}
	cleanupRegisteredWatcher := func() {
		if manager.cancel != nil {
			manager.cancel()
		}
		if manager.watcher != nil {
			_ = manager.watcher.Close()
		}
	}

	// The discovery selection above is deliberately parse-only. Register all
	// watches before the one full runtime/profile load so both global and
	// workspace changes during discovery, or immediately after it, are either
	// observed by this synchronous read or queued for the watch loop.
	if migrationErr == nil {
		candidate, loadErr = manager.loadStartupCandidate(ctx, 1)
	}
	if loadErr != nil {
		raw, readErr := os.ReadFile(paths.GlobalConfig)
		if readErr != nil {
			cleanupRegisteredWatcher()
			return nil, loadErr
		}
		hash := sha256.Sum256(raw)
		files, _ := manager.readConfigFileStates()
		candidate = Snapshot{
			Document: emptyDocument(), Revision: 1, ContentHash: hex.EncodeToString(hash[:]),
			Diagnostics: []Diagnostic{{Severity: "error", File: paths.GlobalConfig, Message: loadErr.Error()}},
			Ready:       false, LoadedAt: time.Now(), Raw: raw,
			globalConfigExists: files.global.exists, workspaceConfigExists: files.workspace.exists,
			workspaceRaw: append([]byte(nil), files.workspace.raw...),
		}
	}
	candidate.Diagnostics = append(diagnostics, candidate.Diagnostics...)
	manager.snapshot = candidate
	if watchContext != nil {
		go manager.watchLoop(watchContext)
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

func (m *Manager) loadStartupCandidate(ctx context.Context, revision uint64) (Snapshot, error) {
	raw, err := os.ReadFile(m.paths.GlobalConfig)
	if err != nil {
		m.discardPendingModelDiscovery()
		return Snapshot{}, err
	}
	parsed, err := m.parseCandidate(raw)
	if err != nil {
		m.discardPendingModelDiscovery()
		return Snapshot{}, err
	}
	m.finalizeModelDiscovery(parsed.document)
	return m.candidateFromParsed(ctx, raw, parsed, revision), nil
}

type configFileState struct {
	exists bool
	raw    []byte
}

type configFileStates struct {
	global    configFileState
	workspace configFileState
}

type parsedCandidate struct {
	document        Document
	workspace       WorkspaceDocument
	workspaceRaw    []byte
	workspaceExists bool
	activeID        string
	diagnostics     []Diagnostic
}

func readConfigFileState(path string) (configFileState, error) {
	if strings.TrimSpace(path) == "" {
		return configFileState{}, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		return configFileState{exists: true, raw: raw}, nil
	}
	if os.IsNotExist(err) {
		return configFileState{}, nil
	}
	return configFileState{}, fmt.Errorf("read config file %s: %w", path, err)
}

func (m *Manager) readConfigFileStates() (configFileStates, error) {
	global, err := readConfigFileState(m.paths.GlobalConfig)
	if err != nil {
		return configFileStates{}, err
	}
	workspace, err := readConfigFileState(m.paths.WorkspaceConfig)
	if err != nil {
		return configFileStates{}, err
	}
	return configFileStates{global: global, workspace: workspace}, nil
}

func (m *Manager) loadDiscoverySelection() (Document, string, error) {
	raw, err := os.ReadFile(m.paths.GlobalConfig)
	if err != nil {
		return Document{}, "", err
	}
	parsed, err := m.parseCandidate(raw)
	if err != nil {
		return Document{}, "", err
	}
	return parsed.document, parsed.activeID, nil
}

func (m *Manager) parseCandidate(raw []byte) (parsedCandidate, error) {
	workspaceState, err := readConfigFileState(m.paths.WorkspaceConfig)
	if err != nil {
		return parsedCandidate{}, err
	}
	return m.parseCandidateWithWorkspace(raw, workspaceState)
}

func (m *Manager) parseCandidateWithWorkspace(raw []byte, workspaceState configFileState) (parsedCandidate, error) {
	document, diagnostics, err := parseAndValidateGlobal(raw, m.paths.GlobalConfig)
	if err != nil {
		return parsedCandidate{}, err
	}
	workspace := WorkspaceDocument{}
	if workspaceState.exists {
		workspace, err = parseAndValidateWorkspace(workspaceState.raw, m.paths.WorkspaceConfig, document)
		if err != nil {
			return parsedCandidate{}, err
		}
	}
	activeID := document.ActiveModel
	if workspace.ActiveModel != "" {
		activeID = workspace.ActiveModel
	}
	if override := strings.TrimSpace(os.Getenv("PAW_MODEL")); override != "" {
		if _, ok := document.Models[override]; !ok {
			return parsedCandidate{}, fmt.Errorf("PAW_MODEL references missing model %q", override)
		}
		activeID = override
	}
	return parsedCandidate{
		document:        document,
		workspace:       workspace,
		workspaceRaw:    append([]byte(nil), workspaceState.raw...),
		workspaceExists: workspaceState.exists,
		activeID:        activeID,
		diagnostics:     diagnostics,
	}, nil
}

func (m *Manager) loadModelDiscoveryCache() {
	cache, err := readDiscoveryCache(m.paths.ModelDiscoveryCache)
	m.discoveryCacheInvalid = err != nil
	if err != nil {
		cache = emptyDiscoveryCache()
		m.startupDiscoveryDiagnostics = append(m.startupDiscoveryDiagnostics, retainedDiscoveryDiagnostic{
			Kind: discoveryDiagnosticCacheRead,
			Diagnostic: Diagnostic{
				Severity: "warning",
				File:     m.paths.ModelDiscoveryCache,
				Message:  "model discovery cache could not be read; continuing without cached models",
			},
		})
	}
	m.discoveryCache = cache
	m.retainedDiscoveries = map[string]retainedDiscovery{}
}

func (m *Manager) initializeModelDiscovery(ctx context.Context, document Document, activeID string) {
	_, cacheProviders := matchingCachedDiscoveries(document, m.discoveryCache)
	m.discoveryStatus = DiscoveryStatus{
		Source:         "manual-only",
		CacheProviders: cacheProviders,
		CacheState:     "missing",
	}

	providerID, skippedReason := discoveryTarget(document, activeID)
	m.discoveryStatus.ProviderID = providerID
	if skippedReason != "" {
		m.discoveryStatus.SkippedReason = skippedReason
		return
	}

	provider := mergePreset(providerID, document.Providers[providerID])
	if provider.Discovery == nil || provider.Discovery.Enabled == nil || !*provider.Discovery.Enabled {
		m.discoveryStatus.CacheState = cacheStateForProvider(m.discoveryCache, m.discoveryCacheInvalid, providerID, "", "")
		m.discoveryStatus.SkippedReason = "provider-disabled"
		return
	}

	fingerprint := discoveryEndpointFingerprint(provider)
	format := provider.Discovery.Format
	m.discoveryTargetFingerprint = fingerprint
	m.discoveryTargetFormat = format
	targetCache, targetCacheMatched := matchingCacheEntry(m.discoveryCache, providerID, fingerprint, format)
	m.discoveryStatus.CacheState = cacheStateForProvider(m.discoveryCache, m.discoveryCacheInvalid, providerID, fingerprint, format)
	if targetCacheMatched {
		m.discoveryStatus.Source = "cache"
		m.discoveryStatus.DiscoveredAt = targetCache.DiscoveredAt
	}
	if m.disableModelDiscovery {
		m.discoveryStatus.SkippedReason = "disabled"
		return
	}

	m.discoveryStatus.Attempted = true
	m.discoveryStatus.AttemptedAt = time.Now().UTC()
	credential := ""
	var err error
	if provider.Auth.Credential != "" || len(provider.Auth.Env) > 0 {
		credential, _, err = resolveCredential(ctx, m.credentials, provider.Auth)
		if err != nil {
			m.discoveryStatus.LastError = "model discovery credential is unavailable"
			m.retainTargetDiscoveryDiagnostic(m.discoveryFailureDiagnostic(providerID, targetCacheMatched), providerID, fingerprint, format)
			return
		}
	}
	if m.discoverer == nil {
		m.discoveryStatus.LastError = "model discovery is unavailable"
		m.retainTargetDiscoveryDiagnostic(m.discoveryFailureDiagnostic(providerID, targetCacheMatched), providerID, fingerprint, format)
		return
	}

	models, err := m.discoverer.Discover(ctx, providerID, provider, credential)
	if err != nil {
		m.discoveryStatus.LastError = safeModelDiscoveryError(err)
		m.retainTargetDiscoveryDiagnostic(m.discoveryFailureDiagnostic(providerID, targetCacheMatched), providerID, fingerprint, format)
		return
	}

	models, cacheNames, filteredCount := normalizeLiveDiscoveredModels(providerID, models)
	succeededAt := time.Now().UTC()
	discovery := retainedDiscovery{
		Models:              cloneDiscoveredModels(models),
		EndpointFingerprint: fingerprint,
		Format:              format,
		DiscoveredAt:        succeededAt,
		Origin:              "live",
		FilteredCount:       filteredCount,
	}
	m.pendingDiscovery = &pendingDiscovery{
		ProviderID:  providerID,
		Discovery:   discovery,
		CacheModels: append([]string{}, cacheNames...),
	}
	m.discoveryStatus.Source = "live"
	m.discoveryStatus.SucceededAt = succeededAt
	m.discoveryStatus.DiscoveredAt = succeededAt
	m.discoveryStatus.LastError = ""
}

func (m *Manager) finalizeModelDiscovery(document Document) {
	pending := m.pendingDiscovery
	m.pendingDiscovery = nil
	if pending == nil {
		return
	}

	configuredProvider, exists := document.Providers[pending.ProviderID]
	if !exists {
		m.discardUnconfirmedLiveDiscovery()
		return
	}
	provider := mergePreset(pending.ProviderID, configuredProvider)
	if provider.Discovery == nil || provider.Discovery.Enabled == nil || !*provider.Discovery.Enabled {
		m.discardUnconfirmedLiveDiscovery()
		return
	}
	if discoveryEndpointFingerprint(provider) != pending.Discovery.EndpointFingerprint || provider.Discovery.Format != pending.Discovery.Format {
		m.discardUnconfirmedLiveDiscovery()
		return
	}

	retained := pending.Discovery
	retained.Models = cloneDiscoveredModels(retained.Models)
	m.retainedDiscoveries[pending.ProviderID] = retained
	updatedCache := cloneDiscoveryCache(m.discoveryCache)
	updatedCache.Providers[pending.ProviderID] = discoveryCacheEntry{
		EndpointFingerprint: retained.EndpointFingerprint,
		Format:              retained.Format,
		DiscoveredAt:        retained.DiscoveredAt,
		Models:              append([]string{}, pending.CacheModels...),
	}
	if err := m.discoveryCacheWriter(m.paths.ModelDiscoveryCache, cloneDiscoveryCache(updatedCache)); err != nil {
		m.discoveryStatus.CacheState = "write-failed"
		m.retainTargetDiscoveryDiagnostic(Diagnostic{
			Severity: "warning",
			File:     m.paths.ModelDiscoveryCache,
			Message:  "live model discovery succeeded, but its cache could not be updated",
		}, pending.ProviderID, retained.EndpointFingerprint, retained.Format)
		return
	}
	m.discoveryCache = updatedCache
	m.discoveryCacheInvalid = false
	_, m.discoveryStatus.CacheProviders = matchingCachedDiscoveries(document, m.discoveryCache)
	m.discoveryStatus.CacheState = "updated"
}

func (m *Manager) discardPendingModelDiscovery() {
	if m.pendingDiscovery == nil {
		return
	}
	m.pendingDiscovery = nil
	m.discardUnconfirmedLiveDiscovery()
}

func (m *Manager) discardUnconfirmedLiveDiscovery() {
	m.discoveryStatus = clearDiscoveryAttempt(m.discoveryStatus)
	m.discoveryStatus.Source = "manual-only"
	m.discoveryStatus.DiscoveredAt = time.Time{}
}

func (m *Manager) retainTargetDiscoveryDiagnostic(diagnostic Diagnostic, providerID, fingerprint, format string) {
	m.startupDiscoveryDiagnostics = append(m.startupDiscoveryDiagnostics, retainedDiscoveryDiagnostic{
		Diagnostic:          diagnostic,
		Kind:                discoveryDiagnosticTarget,
		ProviderID:          providerID,
		EndpointFingerprint: fingerprint,
		Format:              format,
	})
}

func cloneDiscoveryCache(cache discoveryCacheFile) discoveryCacheFile {
	cloned := discoveryCacheFile{
		Version:   cache.Version,
		Providers: make(map[string]discoveryCacheEntry, len(cache.Providers)),
	}
	for providerID, entry := range cache.Providers {
		if entry.Models != nil {
			entry.Models = append([]string{}, entry.Models...)
		}
		cloned.Providers[providerID] = entry
	}
	return cloned
}

func discoveryTarget(document Document, activeID string) (providerID, skippedReason string) {
	if activeID != "" {
		configuredModel, ok := document.Models[activeID]
		if !ok {
			return "", "active-model-missing"
		}
		providerID = configuredModel.Provider
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

func matchingCachedDiscoveries(document Document, cache discoveryCacheFile) (map[string]retainedDiscovery, int) {
	matched := make(map[string]retainedDiscovery)
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
		fingerprint := discoveryEndpointFingerprint(provider)
		entry, ok := matchingCacheEntry(cache, providerID, fingerprint, provider.Discovery.Format)
		if !ok {
			continue
		}
		models := make([]DiscoveredModel, len(entry.Models))
		for index, name := range entry.Models {
			models[index] = DiscoveredModel{ProviderID: providerID, Name: name}
		}
		matched[providerID] = retainedDiscovery{
			Models:              models,
			EndpointFingerprint: fingerprint,
			Format:              provider.Discovery.Format,
			DiscoveredAt:        entry.DiscoveredAt,
			Origin:              "cache",
		}
	}
	return matched, len(matched)
}

func cacheStateForProvider(cache discoveryCacheFile, invalid bool, providerID, fingerprint, format string) string {
	if invalid {
		return "invalid"
	}
	if _, exists := cache.Providers[providerID]; !exists {
		return "missing"
	}
	if _, ok := matchingCacheEntry(cache, providerID, fingerprint, format); ok {
		return "matched"
	}
	return "rejected"
}

func (m *Manager) discoveryFailureDiagnostic(providerID string, targetCacheMatched bool) Diagnostic {
	fallback := "manual models"
	if targetCacheMatched {
		fallback = "matching cached models"
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

func cloneDiscoveredModels(models []DiscoveredModel) []DiscoveredModel {
	return append([]DiscoveredModel(nil), models...)
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

func normalizeLiveDiscoveredModels(providerID string, models []DiscoveredModel) ([]DiscoveredModel, []string, int) {
	normalizedProviderID := strings.TrimSpace(providerID)
	unique := make(map[string]struct{}, len(models))
	filteredCount := 0
	for _, discovered := range models {
		candidateProviderID := strings.TrimSpace(discovered.ProviderID)
		if candidateProviderID != "" && candidateProviderID != normalizedProviderID {
			continue
		}
		if unsafeDiscoveredModelName(discovered.Name) {
			filteredCount++
			continue
		}
		name := strings.TrimSpace(discovered.Name)
		if name == "" {
			filteredCount++
			continue
		}
		unique[name] = struct{}{}
	}
	cacheNames := make([]string, 0, len(unique))
	for name := range unique {
		cacheNames = append(cacheNames, name)
	}
	sort.Strings(cacheNames)
	normalized := make([]DiscoveredModel, len(cacheNames))
	for index, name := range cacheNames {
		normalized[index] = DiscoveredModel{ProviderID: providerID, Name: name}
	}
	return normalized, append([]string{}, cacheNames...), filteredCount
}

func (m *Manager) matchingRetainedDiscoveries(document Document) (map[string][]DiscoveredModel, map[string]retainedDiscovery) {
	// Cache entries are derived afresh from the immutable full cache for every
	// candidate. Applicable process-local live results then replace the cache
	// entry for their provider, including an intentionally empty live result.
	matched, _ := matchingCachedDiscoveries(document, m.discoveryCache)
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
		retained, ok := m.retainedDiscoveries[providerID]
		if !ok || retained.EndpointFingerprint != discoveryEndpointFingerprint(provider) || retained.Format != provider.Discovery.Format {
			continue
		}
		retained.Models = cloneDiscoveredModels(retained.Models)
		matched[providerID] = retained
	}

	models := make(map[string][]DiscoveredModel, len(matched))
	provenance := make(map[string]retainedDiscovery, len(matched))
	for providerID, retained := range matched {
		retained.Models = cloneDiscoveredModels(retained.Models)
		models[providerID] = retained.Models
		provenance[providerID] = retained
	}
	return models, provenance
}

func (m *Manager) applicableDiscoveryDiagnostics(document Document, activeID string) []Diagnostic {
	providerID, skippedReason := discoveryTarget(document, activeID)
	fingerprint := ""
	format := ""
	enabled := false
	if skippedReason == "" && providerID != "" {
		provider := mergePreset(providerID, document.Providers[providerID])
		enabled = provider.Discovery != nil && provider.Discovery.Enabled != nil && *provider.Discovery.Enabled
		if provider.Discovery != nil {
			fingerprint = discoveryEndpointFingerprint(provider)
			format = provider.Discovery.Format
		}
	}

	diagnostics := make([]Diagnostic, 0, len(m.startupDiscoveryDiagnostics))
	for _, retained := range m.startupDiscoveryDiagnostics {
		switch retained.Kind {
		case discoveryDiagnosticCacheRead:
			if m.discoveryCacheInvalid {
				diagnostics = append(diagnostics, retained.Diagnostic)
			}
		case discoveryDiagnosticTarget:
			if enabled && providerID == retained.ProviderID && fingerprint == retained.EndpointFingerprint && format == retained.Format {
				diagnostics = append(diagnostics, retained.Diagnostic)
			}
		}
	}
	return diagnostics
}

func (m *Manager) discoveryStatusForDocument(document Document, activeID string, matched map[string]retainedDiscovery) DiscoveryStatus {
	_, cacheProviders := matchingCachedDiscoveries(document, m.discoveryCache)
	providerID, skippedReason := discoveryTarget(document, activeID)
	status := DiscoveryStatus{
		ProviderID:     providerID,
		Source:         "manual-only",
		CacheProviders: cacheProviders,
		CacheState:     "missing",
		SkippedReason:  skippedReason,
	}
	if providerID == "" || skippedReason != "" {
		if m.discoveryCacheInvalid {
			status.CacheState = "invalid"
		}
		return status
	}

	provider := mergePreset(providerID, document.Providers[providerID])
	enabled := provider.Discovery != nil && provider.Discovery.Enabled != nil && *provider.Discovery.Enabled
	fingerprint := ""
	format := ""
	if provider.Discovery != nil {
		fingerprint = discoveryEndpointFingerprint(provider)
		format = provider.Discovery.Format
	}
	sameStartupTarget := enabled && providerID == m.discoveryStatus.ProviderID && fingerprint == m.discoveryTargetFingerprint && format == m.discoveryTargetFormat
	if sameStartupTarget {
		status = m.discoveryStatus
		status.ProviderID = providerID
		status.CacheProviders = cacheProviders
	} else {
		status.CacheState = cacheStateForProvider(m.discoveryCache, m.discoveryCacheInvalid, providerID, fingerprint, format)
	}

	status.Source = "manual-only"
	status.DiscoveredAt = time.Time{}
	if retained, ok := matched[providerID]; ok {
		status.Source = retained.Origin
		status.DiscoveredAt = retained.DiscoveredAt
	}

	switch {
	case m.disableModelDiscovery:
		status.SkippedReason = "disabled"
	case !enabled:
		status = clearDiscoveryAttempt(status)
		status.ProviderID = providerID
		status.Source = "manual-only"
		status.CacheProviders = cacheProviders
		status.DiscoveredAt = time.Time{}
		status.SkippedReason = "provider-disabled"
		if _, exists := m.discoveryCache.Providers[providerID]; exists && status.CacheState != "invalid" {
			status.CacheState = "rejected"
		}
	case !sameStartupTarget:
		status.SkippedReason = ""
	}
	return status
}

func clearDiscoveryAttempt(status DiscoveryStatus) DiscoveryStatus {
	status.Attempted = false
	status.AttemptedAt = time.Time{}
	status.SucceededAt = time.Time{}
	status.LastError = ""
	return status
}

func (m *Manager) candidateFromRaw(ctx context.Context, raw []byte, revision uint64) (Snapshot, error) {
	parsed, err := m.parseCandidate(raw)
	if err != nil {
		return Snapshot{}, err
	}
	return m.candidateFromParsed(ctx, raw, parsed, revision), nil
}

func (m *Manager) candidateFromRawWithWorkspace(ctx context.Context, raw []byte, workspaceState configFileState, revision uint64) (Snapshot, error) {
	parsed, err := m.parseCandidateWithWorkspace(raw, workspaceState)
	if err != nil {
		return Snapshot{}, err
	}
	return m.candidateFromParsed(ctx, raw, parsed, revision), nil
}

func (m *Manager) candidateFromParsed(ctx context.Context, raw []byte, parsed parsedCandidate, revision uint64) Snapshot {
	discoveredModels, retainedProvenance := m.matchingRetainedDiscoveries(parsed.document)
	catalog, stats := buildEffectiveCatalog(parsed.document, discoveredModels)
	runtimeConfig, ready, runtimeDiagnostics := m.runtimeConfig(ctx, parsed.document, parsed.workspace, parsed.activeID, catalog)
	diagnostics := append([]Diagnostic(nil), parsed.diagnostics...)
	diagnostics = append(diagnostics, m.applicableDiscoveryDiagnostics(parsed.document, parsed.activeID)...)
	diagnostics = append(diagnostics, runtimeDiagnostics...)
	discoveryStatus := m.discoveryStatusForDocument(parsed.document, parsed.activeID, retainedProvenance)
	prefilteredCount := 0
	for _, retained := range retainedProvenance {
		prefilteredCount += retained.FilteredCount
	}
	discoveryStatus.DiscoveredCount = stats.Discovered + prefilteredCount
	discoveryStatus.FilteredCount = stats.Filtered + prefilteredCount
	discoveryStatus = withEffectiveCount(discoveryStatus, stats.Merged)
	hash := sha256.Sum256(append(append(append([]byte(nil), raw...), 0), parsed.workspaceRaw...))
	return Snapshot{
		Document: parsed.document, Workspace: parsed.workspace, Active: runtimeConfig,
		ActiveModelID: parsed.activeID, EffectiveModels: catalog, Discovery: discoveryStatus,
		Revision: revision, ContentHash: hex.EncodeToString(hash[:]), Diagnostics: diagnostics,
		Ready: ready, LoadedAt: time.Now(), Raw: raw,
		globalConfigExists: true, workspaceConfigExists: parsed.workspaceExists,
		workspaceRaw: append([]byte(nil), parsed.workspaceRaw...),
	}
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

func sameConfigFileState(left, right configFileState) bool {
	return left.exists == right.exists && bytes.Equal(left.raw, right.raw)
}

func (m *Manager) verifyConfigFilesUnchanged(base Snapshot) (configFileStates, error) {
	current, err := m.readConfigFileStates()
	if err != nil {
		return configFileStates{}, fmt.Errorf("%w: could not verify configuration files: %v", ErrRevisionConflict, err)
	}
	expectedGlobal := configFileState{exists: base.globalConfigExists, raw: base.Raw}
	if !sameConfigFileState(current.global, expectedGlobal) {
		return configFileStates{}, fmt.Errorf("%w: global configuration changed outside the manager; reload and retry", ErrRevisionConflict)
	}
	expectedWorkspace := configFileState{exists: base.workspaceConfigExists, raw: base.workspaceRaw}
	if !sameConfigFileState(current.workspace, expectedWorkspace) {
		return configFileStates{}, fmt.Errorf("%w: workspace configuration changed outside the manager; reload and retry", ErrRevisionConflict)
	}
	return current, nil
}

// PreviewUpdate builds and validates the Snapshot produced by operations at an
// expected revision without writing the config file or publishing the result.
func (m *Manager) PreviewUpdate(ctx context.Context, expectedRevision uint64, operations []Operation) (Snapshot, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	candidate, _, _, err := m.prepareUpdate(ctx, expectedRevision, operations)
	if err != nil {
		return candidate, err
	}
	return candidate.Clone(), nil
}

func (m *Manager) Update(ctx context.Context, expectedRevision uint64, operations []Operation) (Snapshot, error) {
	return m.commitUpdate(ctx, expectedRevision, operations, nil)
}

// commitPreview uses the same commit path as Update but additionally requires
// the prospective Snapshot to remain identical to the non-publishing preview.
func (m *Manager) commitPreview(ctx context.Context, expectedRevision uint64, operations []Operation, preview Snapshot) (Snapshot, error) {
	return m.commitUpdate(ctx, expectedRevision, operations, &preview)
}

func (m *Manager) commitUpdate(ctx context.Context, expectedRevision uint64, operations []Operation, preview *Snapshot) (Snapshot, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	candidate, raw, base, err := m.prepareUpdate(ctx, expectedRevision, operations)
	if err != nil {
		return candidate, err
	}
	if preview != nil && !sameUpdatePreview(*preview, candidate) {
		return m.Snapshot(), fmt.Errorf("%w: prospective configuration changed before commit", ErrRevisionConflict)
	}
	if _, err := m.verifyConfigFilesUnchanged(base); err != nil {
		return m.Snapshot(), err
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
		return Snapshot{}, err
	}
	m.publish(candidate)
	return candidate.Clone(), nil
}

func sameUpdatePreview(left, right Snapshot) bool {
	left = left.Clone()
	right = right.Clone()
	return left.Revision == right.Revision &&
		left.ContentHash == right.ContentHash &&
		left.ActiveModelID == right.ActiveModelID &&
		left.Ready == right.Ready &&
		bytes.Equal(left.Raw, right.Raw) &&
		left.globalConfigExists == right.globalConfigExists &&
		left.workspaceConfigExists == right.workspaceConfigExists &&
		bytes.Equal(left.workspaceRaw, right.workspaceRaw) &&
		reflect.DeepEqual(left.Document, right.Document) &&
		reflect.DeepEqual(left.Workspace, right.Workspace) &&
		reflect.DeepEqual(left.EffectiveModels, right.EffectiveModels) &&
		reflect.DeepEqual(left.Active, right.Active)
}

// prepareUpdate requires updateMu and is shared by preview and commit so both
// paths apply identical optimistic-concurrency, file-state, patching, and
// validation rules.
func (m *Manager) prepareUpdate(ctx context.Context, expectedRevision uint64, operations []Operation) (Snapshot, []byte, Snapshot, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Snapshot{}, nil, Snapshot{}, errors.New("configuration manager is closed")
	}
	if expectedRevision != m.snapshot.Revision {
		current := m.snapshot.Clone()
		m.mu.Unlock()
		return current, nil, Snapshot{}, fmt.Errorf("%w: expected %d, current %d", ErrRevisionConflict, expectedRevision, current.Revision)
	}
	base := m.snapshot.Clone()
	m.mu.Unlock()

	files, err := m.verifyConfigFilesUnchanged(base)
	if err != nil {
		return base, nil, Snapshot{}, err
	}
	raw := append([]byte(nil), base.Raw...)
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
			return Snapshot{}, nil, Snapshot{}, err
		}
	}
	if _, _, err := parseAndValidateGlobal(raw, m.paths.GlobalConfig); err != nil {
		return Snapshot{}, nil, Snapshot{}, err
	}
	candidate, err := m.candidateFromRawWithWorkspace(ctx, raw, files.workspace, base.Revision+1)
	if err != nil {
		return Snapshot{}, nil, Snapshot{}, err
	}
	if !candidate.Ready && base.Ready {
		return Snapshot{}, nil, Snapshot{}, fmt.Errorf("updated configuration does not have a usable active credential")
	}
	return candidate, raw, base, nil
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

func (m *Manager) registerWatcher(parent context.Context) (context.Context, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	directories := map[string]bool{filepath.Dir(m.paths.GlobalConfig): true}
	if m.paths.WorkspaceConfig != "" {
		directory := filepath.Dir(m.paths.WorkspaceConfig)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			_ = watcher.Close()
			return nil, err
		}
		directories[directory] = true
	}
	for directory := range directories {
		if err := watcher.Add(directory); err != nil {
			_ = watcher.Close()
			return nil, fmt.Errorf("watch config directory %s: %w", directory, err)
		}
	}
	ctx, cancel := context.WithCancel(parent)
	m.watcher = watcher
	m.cancel = cancel
	return ctx, nil
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
