package config

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/model"
)

func isolatedPaths(t *testing.T, workspace bool) Paths {
	t.Helper()
	root := t.TempDir()
	options := PathOptions{ConfigHome: filepath.Join(root, "config", "Paw"), UserHomeDir: filepath.Join(root, "home")}
	if workspace {
		options.WorkspaceRoot = filepath.Join(root, "workspace")
	}
	paths, err := ResolvePaths(options)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func clearDetectionEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PAW_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL"} {
		t.Setenv(name, "")
	}
}

func openTestManager(t *testing.T, paths Paths, store CredentialStore, watch bool) *Manager {
	t.Helper()
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: store, DisableWatch: !watch, DisableModelDiscovery: true, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

// fakeModelDiscoverer is intentionally countable so lifecycle tests can prove
// that only top-level Open is allowed to perform live discovery.
type fakeModelDiscoverer struct {
	calls  []string
	models []DiscoveredModel
	err    error
}

func (f *fakeModelDiscoverer) Discover(_ context.Context, providerID string, _ Provider, _ string) ([]DiscoveredModel, error) {
	f.calls = append(f.calls, providerID)
	return append([]DiscoveredModel(nil), f.models...), f.err
}

func writeManagerDocument(t *testing.T, paths Paths, document Document) {
	t.Helper()
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := marshalStarter(document, "manager discovery test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func discoveryTestProvider(endpoint string) Provider {
	enabled := true
	return Provider{
		Transport: TransportOpenAICompatible,
		Endpoint:  endpoint,
		Discovery: &DiscoveryConfig{Enabled: &enabled, Path: "models", Format: DiscoveryFormatOpenAIList},
	}
}

func profileByID(t *testing.T, profiles []model.Profile, id string) model.Profile {
	t.Helper()
	for _, profile := range profiles {
		if profile.ID == id {
			return profile
		}
	}
	t.Fatalf("profile %q not found in %#v", id, profiles)
	return model.Profile{}
}

func TestManagerActiveProviderDiscoveryBuildsEffectiveCatalog(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	activeProvider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	passiveProvider := discoveryTestProvider("http://127.0.0.1:5678/v1")
	document := emptyDocument()
	document.Providers["active"] = activeProvider
	document.Providers["passive"] = passiveProvider
	document.Models["active/manual"] = Model{Provider: "active", Name: "manual", Adapter: AdapterDeepSeek, ContextWindow: 123, Parameters: map[string]any{"temperature": 0.2}}
	document.Models["passive/manual"] = Model{Provider: "passive", Name: "manual"}
	document.ActiveModel = "active/manual"
	writeManagerDocument(t, paths, document)

	discoveredAt := time.Unix(100, 0).UTC()
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"passive": {
				EndpointFingerprint: discoveryEndpointFingerprint(passiveProvider),
				Format:              DiscoveryFormatOpenAIList,
				DiscoveredAt:        discoveredAt,
				Models:              []string{"cached-passive"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "manual"}, {Name: "live-active"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if len(discoverer.calls) != 1 || discoverer.calls[0] != "active" {
		t.Fatalf("discovery calls = %#v, want active once", discoverer.calls)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Ready {
		t.Fatalf("snapshot not ready: %#v", snapshot.Diagnostics)
	}
	for _, id := range []string{"active/manual", "active/live-active", "passive/manual", "passive/cached-passive"} {
		if _, ok := snapshot.EffectiveModels[id]; !ok {
			t.Fatalf("effective catalog missing %q: %#v", id, snapshot.EffectiveModels)
		}
	}
	if got := snapshot.Active.Models; strings.Join(got, ",") != "live-active,manual" {
		t.Fatalf("active models = %#v", got)
	}
	if got := snapshot.Active.ModelExtraBody["manual"]["temperature"]; got != float64(0.2) {
		t.Fatalf("active manual parameters = %#v", got)
	}
	if got := snapshot.Active.ModelContextLimitTokens["manual"]; got != 123 {
		t.Fatalf("active manual context = %d", got)
	}
	if got, ok := snapshot.Active.ModelContextLimitTokens["live-active"]; !ok || got != 0 {
		t.Fatalf("active discovered context = %d, present=%v", got, ok)
	}
	profile := profileByID(t, snapshot.Active.Profiles, "active")
	if strings.Join(profile.Models, ",") != "live-active,manual" || profile.ModelContextLimitTokens["manual"] != 123 {
		t.Fatalf("active profile did not use effective catalog: %#v", profile)
	}
	if got := profile.ModelExtraBody["manual"]["temperature"]; got != float64(0.2) {
		t.Fatalf("profile manual parameters = %#v", got)
	}
	if !snapshot.Discovery.Attempted || snapshot.Discovery.ProviderID != "active" || snapshot.Discovery.Source != "live" || snapshot.Discovery.EffectiveCount != 4 {
		t.Fatalf("discovery status = %#v", snapshot.Discovery)
	}

	cache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Providers) != 2 || len(cache.Providers["active"].Models) != 2 || cache.Providers["passive"].Models[0] != "cached-passive" {
		t.Fatalf("cache did not retain all providers: %#v", cache)
	}
}

func TestManagerSingleProviderDiscoveryBootstrapsWithoutActiveModel(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "bootstrapped"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if len(discoverer.calls) != 1 || discoverer.calls[0] != "local" {
		t.Fatalf("discovery calls = %#v", discoverer.calls)
	}
	if _, ok := manager.Snapshot().EffectiveModels["local/bootstrapped"]; !ok {
		t.Fatalf("single-provider bootstrap catalog = %#v", manager.Snapshot().EffectiveModels)
	}
}

func TestManagerMultipleProvidersWithoutActiveSkipsDiscovery(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["one"] = discoveryTestProvider("http://127.0.0.1:1111/v1")
	document.Providers["two"] = discoveryTestProvider("http://127.0.0.1:2222/v1")
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if len(discoverer.calls) != 0 || snapshot.Discovery.Attempted || snapshot.Discovery.SkippedReason == "" {
		t.Fatalf("calls/status = %#v / %#v", discoverer.calls, snapshot.Discovery)
	}
}

func TestManagerDisableModelDiscoverySkipsLiveRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {EndpointFingerprint: discoveryEndpointFingerprint(provider), Format: DiscoveryFormatOpenAIList, DiscoveredAt: time.Unix(100, 0).UTC(), Models: []string{"cached"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, DisableModelDiscovery: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if len(discoverer.calls) != 0 || snapshot.Discovery.Attempted || snapshot.Discovery.SkippedReason == "" {
		t.Fatalf("calls/status = %#v / %#v", discoverer.calls, snapshot.Discovery)
	}
	if _, ok := snapshot.EffectiveModels["local/cached"]; !ok {
		t.Fatalf("disabled live discovery did not use matching cache: %#v", snapshot.EffectiveModels)
	}
}

func TestManagerDiscoveryFailureUsesMatchingCacheAndRemainsReady(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {EndpointFingerprint: discoveryEndpointFingerprint(provider), Format: DiscoveryFormatOpenAIList, DiscoveredAt: time.Unix(100, 0).UTC(), Models: []string{"cached"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{err: errors.New("secret-token=https://example.invalid/?credential=leak")}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if len(discoverer.calls) != 1 || !snapshot.Ready {
		t.Fatalf("calls/ready = %#v / %v; diagnostics=%#v", discoverer.calls, snapshot.Ready, snapshot.Diagnostics)
	}
	if _, ok := snapshot.EffectiveModels["local/cached"]; !ok || snapshot.Discovery.Source != "cache" || snapshot.Discovery.LastError == "" {
		t.Fatalf("cache fallback/status = %#v / %#v", snapshot.EffectiveModels, snapshot.Discovery)
	}
	if strings.Contains(snapshot.Discovery.LastError, "secret-token") || strings.Contains(snapshot.Discovery.LastError, "credential=leak") {
		t.Fatalf("unsafe discovery status error = %q", snapshot.Discovery.LastError)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Severity != "warning" || strings.Contains(diagnostic.Message, "secret-token") || strings.Contains(diagnostic.Message, "credential=leak") {
			t.Fatalf("unsafe or blocking discovery diagnostic = %#v", diagnostic)
		}
	}
}

func TestManagerDiscoveryEmptySuccessReplacesMatchingCache(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {EndpointFingerprint: discoveryEndpointFingerprint(provider), Format: DiscoveryFormatOpenAIList, DiscoveredAt: time.Unix(100, 0).UTC(), Models: []string{"stale"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if _, ok := snapshot.EffectiveModels["local/stale"]; ok || snapshot.Discovery.Source != "live" || !snapshot.Discovery.Attempted {
		t.Fatalf("empty live result did not replace cache: %#v / %#v", snapshot.EffectiveModels, snapshot.Discovery)
	}
	cache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
	if err != nil {
		t.Fatal(err)
	}
	if cache.Providers["local"].Models == nil || len(cache.Providers["local"].Models) != 0 {
		t.Fatalf("empty live cache entry = %#v", cache.Providers["local"])
	}
}

func TestManagerDiscoveryUpdateAndReloadDoNotRequestAgain(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetActiveModel("local/two")}); err != nil {
		t.Fatal(err)
	}
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after reload/update = %#v", discoverer.calls)
	}
	if _, ok := manager.Snapshot().EffectiveModels["local/live"]; !ok {
		t.Fatalf("retained discovery missing after reload/update: %#v", manager.Snapshot().EffectiveModels)
	}
}

func TestManagerDiscoveryHotReloadDoesNotRequestAgain(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, Discoverer: discoverer, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates

	document.ActiveModel = "local/two"
	raw, err := marshalStarter(document, "manager discovery hot reload")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-updates:
		if snapshot.ActiveModelID != "local/two" {
			t.Fatalf("hot reload active = %q", snapshot.ActiveModelID)
		}
		if _, ok := snapshot.EffectiveModels["local/live"]; !ok {
			t.Fatalf("hot reload lost retained discovery: %#v", snapshot.EffectiveModels)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for discovery hot reload")
	}
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after hot reload = %#v", discoverer.calls)
	}
}

func TestManagerDefaultDiscovererPerformsOneSafeHTTPRequest(t *testing.T) {
	clearDetectionEnv(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"live-default"}]}`)
	}))
	defer server.Close()

	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider(server.URL + "/v1")
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)

	manager, err := Open(context.Background(), Options{
		Paths:        paths,
		Credentials:  &FakeCredentialStore{Unavailable: true},
		DisableWatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Ready {
		t.Fatalf("snapshot not ready: %#v", snapshot.Diagnostics)
	}
	if _, ok := snapshot.EffectiveModels["local/live-default"]; !ok {
		t.Fatalf("default discovery result missing: %#v", snapshot.EffectiveModels)
	}
}

func TestManagerDiscoveryEndpointUpdateRejectsRetainedModelsWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "stale-live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, ok := manager.Snapshot().EffectiveModels["local/stale-live"]; !ok {
		t.Fatal("startup discovery result missing")
	}

	provider.Endpoint = "http://127.0.0.1:5678/v1"
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedDiscoveryRejected(t, after, "local/stale-live")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after endpoint update = %#v", discoverer.calls)
	}
}

func TestManagerDiscoveryPathReloadRejectsRetainedModelsWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "stale-live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	provider.Discovery.Path = "other-models"
	document.Providers["local"] = provider
	writeManagerDocument(t, paths, document)
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	assertRetainedDiscoveryRejected(t, manager.Snapshot(), "local/stale-live")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after path reload = %#v", discoverer.calls)
	}
}

func TestManagerDiscoveryFormatWatcherRejectsRetainedModelsWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "stale-live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, Discoverer: discoverer, Debounce: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates

	provider.Discovery.Format = DiscoveryFormatOllamaTags
	document.Providers["local"] = provider
	raw, err := marshalStarter(document, "manager discovery format watcher")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-updates:
		assertRetainedDiscoveryRejected(t, snapshot, "local/stale-live")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for discovery format watcher")
	}
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after format watcher = %#v", discoverer.calls)
	}
}

func assertRetainedDiscoveryRejected(t *testing.T, snapshot Snapshot, staleID string) {
	t.Helper()
	if _, ok := snapshot.EffectiveModels[staleID]; ok {
		t.Fatalf("stale retained model reappeared: %#v", snapshot.EffectiveModels)
	}
	if snapshot.Discovery.Source != "manual-only" || snapshot.Discovery.CacheState != "rejected" {
		t.Fatalf("stale discovery status = %#v", snapshot.Discovery)
	}
	if snapshot.Discovery.DiscoveredAt != (time.Time{}) {
		t.Fatalf("stale discoveredAt retained: %v", snapshot.Discovery.DiscoveredAt)
	}
	if snapshot.Discovery.EffectiveCount != len(snapshot.EffectiveModels) {
		t.Fatalf("effective count = %d, catalog = %d", snapshot.Discovery.EffectiveCount, len(snapshot.EffectiveModels))
	}
}

func TestManagerDiscoveryFailureWithPassiveOnlyCacheIsManualOnly(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	activeProvider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	passiveProvider := discoveryTestProvider("http://127.0.0.1:5678/v1")
	document := emptyDocument()
	document.Providers["active"] = activeProvider
	document.Providers["passive"] = passiveProvider
	document.Models["active/manual"] = Model{Provider: "active", Name: "manual"}
	document.ActiveModel = "active/manual"
	writeManagerDocument(t, paths, document)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"passive": {
				EndpointFingerprint: discoveryEndpointFingerprint(passiveProvider),
				Format:              DiscoveryFormatOpenAIList,
				DiscoveredAt:        time.Unix(100, 0).UTC(),
				Models:              []string{"cached-passive"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{err: errors.New("offline")}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if snapshot.Discovery.Source != "manual-only" || snapshot.Discovery.CacheState != "missing" || snapshot.Discovery.CacheProviders != 1 {
		t.Fatalf("passive-only cache status = %#v", snapshot.Discovery)
	}
	if snapshot.Discovery.DiscoveredAt != (time.Time{}) {
		t.Fatalf("passive cache timestamp leaked into target status: %v", snapshot.Discovery.DiscoveredAt)
	}
	if _, ok := snapshot.EffectiveModels["passive/cached-passive"]; !ok {
		t.Fatalf("passive cached catalog entry missing: %#v", snapshot.EffectiveModels)
	}
}

func TestManagerDiscoveryFailureRecognizesEmptyMatchingTargetCache(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoveredAt := time.Unix(200, 0).UTC()
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(provider),
				Format:              DiscoveryFormatOpenAIList,
				DiscoveredAt:        discoveredAt,
				Models:              []string{},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{err: errors.New("offline")}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if snapshot.Discovery.Source != "cache" || snapshot.Discovery.CacheState != "matched" || snapshot.Discovery.CacheProviders != 1 {
		t.Fatalf("empty matching target cache status = %#v", snapshot.Discovery)
	}
	if !snapshot.Discovery.DiscoveredAt.Equal(discoveredAt) {
		t.Fatalf("discoveredAt = %v, want %v", snapshot.Discovery.DiscoveredAt, discoveredAt)
	}
	if snapshot.Discovery.EffectiveCount != 1 || len(snapshot.EffectiveModels) != 1 {
		t.Fatalf("empty target cache counts/status = %#v / %#v", snapshot.Discovery, snapshot.EffectiveModels)
	}
}

func TestManagerDiscoveryFailurePropagatesTargetCacheTimestamp(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoveredAt := time.Date(2026, time.August, 12, 10, 11, 12, 0, time.UTC)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(provider),
				Format:              DiscoveryFormatOpenAIList,
				DiscoveredAt:        discoveredAt,
				Models:              []string{"cached"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: &fakeModelDiscoverer{err: errors.New("offline")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if got := manager.Snapshot().Discovery.DiscoveredAt; !got.Equal(discoveredAt) {
		t.Fatalf("target cache discoveredAt = %v, want %v", got, discoveredAt)
	}
}

func TestManagerDiscoveryFailureRejectsMismatchedTargetCache(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	oldProvider := discoveryTestProvider("http://127.0.0.1:5678/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(oldProvider),
				Format:              DiscoveryFormatOpenAIList,
				DiscoveredAt:        time.Unix(300, 0).UTC(),
				Models:              []string{"stale"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: &fakeModelDiscoverer{err: errors.New("offline")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if snapshot.Discovery.Source != "manual-only" || snapshot.Discovery.CacheState != "rejected" || snapshot.Discovery.CacheProviders != 0 {
		t.Fatalf("mismatched target cache status = %#v", snapshot.Discovery)
	}
	if snapshot.Discovery.DiscoveredAt != (time.Time{}) {
		t.Fatalf("rejected target cache timestamp retained: %v", snapshot.Discovery.DiscoveredAt)
	}
	if _, ok := snapshot.EffectiveModels["local/stale"]; ok {
		t.Fatalf("mismatched target cache model reused: %#v", snapshot.EffectiveModels)
	}
}

func TestManagerDiscoveryFailureDiagnosticSurvivesReloadAndUpdate(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{err: errors.New("secret-token=do-not-render")}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	assertWarningDiagnostic(t, manager.Snapshot(), "continuing with manual models")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, manager.Snapshot(), "continuing with manual models")
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetActiveModel("local/two")})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "continuing with manual models")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after diagnostic reload/update = %#v", discoverer.calls)
	}
}

func TestManagerDiscoveryCacheWriteFailureDiagnosticSurvivesReloadAndUpdate(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	blockedParent := filepath.Join(paths.Home, "blocked-cache-parent")
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths.ModelDiscoveryCache = filepath.Join(blockedParent, "model-discovery-cache.json")
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "live"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	assertWarningDiagnostic(t, manager.Snapshot(), "cache could not be updated")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, manager.Snapshot(), "cache could not be updated")
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetActiveModel("local/two")})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "cache could not be updated")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after cache diagnostic reload/update = %#v", discoverer.calls)
	}
}

func assertWarningDiagnostic(t *testing.T, snapshot Snapshot, substring string) {
	t.Helper()
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, substring) {
			if diagnostic.Severity != "warning" {
				t.Fatalf("diagnostic severity = %q: %#v", diagnostic.Severity, diagnostic)
			}
			if strings.Contains(diagnostic.Message, "secret-token") {
				t.Fatalf("diagnostic leaked secret: %#v", diagnostic)
			}
			if !snapshot.Ready {
				t.Fatalf("warning diagnostic changed Ready: %#v", snapshot.Diagnostics)
			}
			return
		}
	}
	t.Fatalf("missing warning diagnostic containing %q: %#v", substring, snapshot.Diagnostics)
}

func TestFirstRunCreatesStarterWithoutTouchingUserHome(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if snapshot.Ready {
		t.Fatal("starter snapshot unexpectedly ready")
	}
	if _, err := os.Stat(paths.GlobalConfig); err != nil {
		t.Fatalf("starter config: %v", err)
	}
	if _, err := os.Stat(paths.Schema); err != nil {
		t.Fatalf("embedded schema: %v", err)
	}
	setupErr := manager.RequireReady()
	if !errors.As(setupErr, new(*SetupRequiredError)) {
		t.Fatalf("RequireReady() = %v", setupErr)
	}
	if !strings.Contains(setupErr.Error(), paths.GlobalConfig) || !strings.Contains(setupErr.Error(), "/config") {
		t.Fatalf("setup-required error lacks path or repair command: %v", setupErr)
	}
}

func TestInvalidExistingConfigStartsInDiagnosticModeAndCanReloadAfterRepair(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GlobalConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	if manager.Snapshot().Ready || len(manager.Snapshot().Diagnostics) == 0 {
		t.Fatalf("invalid startup snapshot=%#v", manager.Snapshot())
	}
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/model"] = Model{Provider: "local", Name: "model"}
	document.ActiveModel = "local/model"
	raw, _ := marshalStarter(document, "repaired")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if !manager.Snapshot().Ready {
		t.Fatalf("repaired snapshot not ready: %#v", manager.Snapshot().Diagnostics)
	}
}

func TestFirstRunAutoSelectsSingleCredentialCompletePreset(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret-from-env")
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if !snapshot.Ready || snapshot.ActiveModelID != "deepseek/chat" {
		t.Fatalf("snapshot = ready:%v active:%q", snapshot.Ready, snapshot.ActiveModelID)
	}
	if snapshot.Active.APIKey != "secret-from-env" {
		t.Fatalf("API key was not resolved from env")
	}
	if snapshot.Active.Adapter != AdapterDeepSeek {
		t.Fatalf("adapter = %q", snapshot.Active.Adapter)
	}
}

func TestFirstRunMultipleCandidatesRequiresChoice(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("OPENAI_API_KEY", "one")
	t.Setenv("DEEPSEEK_API_KEY", "two")
	manager := openTestManager(t, isolatedPaths(t, false), &FakeCredentialStore{Unavailable: true}, false)
	if manager.Snapshot().Ready {
		t.Fatal("multiple candidates should not be auto-selected")
	}
	if got := manager.Snapshot().Diagnostics[0].Message; !strings.Contains(got, "multiple") {
		t.Fatalf("diagnostic = %q", got)
	}
}

func TestFirstRunPAWModelWins(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("PAW_MODEL", "deepseek/chat")
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	manager := openTestManager(t, isolatedPaths(t, false), &FakeCredentialStore{Unavailable: true}, false)
	if got := manager.Snapshot().ActiveModelID; got != "deepseek/chat" {
		t.Fatalf("active = %q", got)
	}
}

func TestDocumentForPresetClonesBuiltinState(t *testing.T) {
	const id = "clone-test"
	discoveryEnabled := true
	modelStream := false
	builtinPresets[id] = Preset{
		ID: id,
		Provider: Provider{
			Discovery: &DiscoveryConfig{Enabled: &discoveryEnabled, Include: []string{"chat-*"}},
		},
		DefaultModelID: id + "/default",
		DefaultModel: Model{
			Provider: id,
			Name:     "default",
			Stream:   &modelStream,
			Parameters: map[string]any{
				"nested": map[string]any{"enabled": true},
			},
		},
	}
	t.Cleanup(func() { delete(builtinPresets, id) })

	document := documentForPreset(id)
	provider := document.Providers[id]
	*provider.Discovery.Enabled = false
	provider.Discovery.Include[0] = "changed-*"
	configuredModel := document.Models[id+"/default"]
	*configuredModel.Stream = true
	configuredModel.Parameters["nested"].(map[string]any)["enabled"] = false

	preset := builtinPresets[id]
	if preset.Provider.Discovery == nil || preset.Provider.Discovery.Enabled == nil || !*preset.Provider.Discovery.Enabled || preset.Provider.Discovery.Include[0] != "chat-*" {
		t.Fatalf("preset provider was aliased: %#v", preset.Provider.Discovery)
	}
	if preset.DefaultModel.Stream == nil || *preset.DefaultModel.Stream || preset.DefaultModel.Parameters["nested"].(map[string]any)["enabled"] != true {
		t.Fatalf("preset default model was aliased: %#v", preset.DefaultModel)
	}
}

func TestJSONCTargetedUpdatePreservesCommentsUnknownFieldsAndTrailingComma(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{
  // keep this comment
  "schemaVersion": 2,
  "activeModel": "local/one",
  "providers": {
    "local": { "transport": "openai-compatible", "endpoint": "http://127.0.0.1:1234/v1", "providerExtension": {"keep": true}, },
  },
  "models": {
    "local/one": { "provider": "local", "name": "one" },
  },
  "extension": { "ownedByPlugin": true },
}
`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	after, err := manager.Update(context.Background(), before.Revision, []Operation{
		UpsertModel("local/two", Model{Provider: "local", Name: "two", Parameters: map[string]any{"temperature": 0.2}}),
		SetActiveModel("local/two"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.ActiveModelID != "local/two" {
		t.Fatalf("active = %q", after.ActiveModelID)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if !strings.Contains(text, "// keep this comment") || !strings.Contains(text, `"ownedByPlugin": true`) || !strings.Contains(text, `"providerExtension": {"keep": true}`) {
		t.Fatalf("targeted update lost JSONC content:\n%s", text)
	}
	if _, err := manager.Update(context.Background(), before.Revision, []Operation{SetActiveModel("local/one")}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
}

func TestUpsertProviderAddsAndRemovesDiscovery(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/model"] = Model{Provider: "local", Name: "model"}
	document.ActiveModel = "local/model"
	raw, _ := marshalStarter(document, "provider discovery update")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	enabled := true
	provider.Discovery = &DiscoveryConfig{Enabled: &enabled, Path: "models", Format: DiscoveryFormatOpenAIList, Include: []string{"chat-*"}}
	withDiscovery, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	got := withDiscovery.Document.Providers["local"].Discovery
	if got == nil || got.Enabled == nil || !*got.Enabled || got.Path != "models" || len(got.Include) != 1 || got.Include[0] != "chat-*" {
		t.Fatalf("discovery was not added: %#v", got)
	}

	provider.Discovery = nil
	withoutDiscovery, err := manager.Update(context.Background(), withDiscovery.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutDiscovery.Document.Providers["local"].Discovery; got != nil {
		t.Fatalf("discovery was not removed: %#v", got)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"discovery"`) {
		t.Fatalf("discovery block remained in config:\n%s", written)
	}
}

func TestUpsertProviderPreservesNilAndExplicitEmptyDiscoveryFilters(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/model"] = Model{Provider: "local", Name: "model"}
	document.ActiveModel = "local/model"
	raw, _ := marshalStarter(document, "provider discovery slice round trip")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	enabled := true
	provider.Discovery = &DiscoveryConfig{Enabled: &enabled, Include: []string{}, Exclude: nil}
	withEmptyInclude, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	got := withEmptyInclude.Document.Providers["local"].Discovery
	if got == nil || got.Include == nil || len(got.Include) != 0 || got.Exclude != nil {
		t.Fatalf("include nil/empty distinction was lost: %#v", got)
	}

	provider.Discovery = &DiscoveryConfig{Enabled: &enabled, Include: nil, Exclude: []string{}}
	withEmptyExclude, err := manager.Update(context.Background(), withEmptyInclude.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	got = withEmptyExclude.Document.Providers["local"].Discovery
	if got == nil || got.Include != nil || got.Exclude == nil || len(got.Exclude) != 0 {
		t.Fatalf("exclude nil/empty distinction was lost: %#v", got)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"include"`) || !strings.Contains(string(written), `"exclude": []`) {
		t.Fatalf("upsert did not preserve explicit empty exclude in JSONC:\n%s", written)
	}
}

func TestConcurrentUpdatesUseOptimisticRevision(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	raw, _ := marshalStarter(document, "concurrency")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	revision := manager.Snapshot().Revision
	errorsCh := make(chan error, 2)
	for _, id := range []string{"local/one", "local/two"} {
		go func(id string) {
			_, err := manager.Update(context.Background(), revision, []Operation{SetActiveModel(id)})
			errorsCh <- err
		}(id)
	}
	conflicts := 0
	for range 2 {
		if err := <-errorsCh; errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("revision conflicts=%d want=1", conflicts)
	}
}

func TestWorkspaceCannotOverrideProviderSecurityFields(t *testing.T) {
	global := documentForPreset("deepseek")
	bad := []byte(`{"schemaVersion":2,"providers":{"deepseek":{"endpoint":"https://evil.invalid"}}}`)
	if _, err := parseAndValidateWorkspace(bad, "workspace", global); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("workspace validation error = %v", err)
	}
	badModel := []byte(`{"schemaVersion":2,"models":{"deepseek/chat":{"auth":{"env":["STOLEN"]}}}}`)
	if _, err := parseAndValidateWorkspace(badModel, "workspace", global); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("model override validation error = %v", err)
	}
}

func TestProtectedRequestBodyFieldsAreRejectedBeforePublish(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, ActiveModel: "local/model", Providers: map[string]Provider{"local": {Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1", Body: map[string]any{"model": "hijack"}}}, Models: map[string]Model{"local/model": {Provider: "local", Name: "model"}}}
	if _, err := validateDocument(document, "config.jsonc"); err == nil || !strings.Contains(err.Error(), "protected field") {
		t.Fatalf("protected body error=%v", err)
	}
}

func TestWorkspaceOverridesOnlyActiveModelParameters(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	paths := isolatedPaths(t, true)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	global, _ := marshalStarter(documentForPreset("deepseek"), "test")
	if err := os.WriteFile(paths.GlobalConfig, global, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := `{"schemaVersion":2,"activeModel":"deepseek/chat","models":{"deepseek/chat":{"stream":false,"parameters":{"temperature":0.1}}}}`
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(workspace), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if snapshot.Active.Stream {
		t.Fatal("workspace stream override was ignored")
	}
	if got := snapshot.Active.ModelExtraBody["deepseek-chat"]["temperature"]; got != float64(0.1) {
		t.Fatalf("temperature = %#v", got)
	}
}

func TestExplicitZeroRetriesAndFalseStreamSurviveRuntimeDefaults(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":2,"activeModel":"local/model","providers":{"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1","retries":0,"stream":false}},"models":{"local/model":{"provider":"local","name":"model"}}}`)
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	client := model.NewClient(manager.Snapshot().Active)
	runtime := client.CurrentModelConfig()
	if runtime.RetryCount != 0 || runtime.Stream {
		t.Fatalf("runtime retries/stream = %d/%v", runtime.RetryCount, runtime.Stream)
	}
}

func TestInvalidReloadAndDeletionKeepLastKnownGood(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "secret")
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	if err := os.WriteFile(paths.GlobalConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("invalid reload unexpectedly succeeded")
	}
	if got := manager.Snapshot(); got.Revision != before.Revision || got.ActiveModelID != before.ActiveModelID {
		t.Fatalf("last-known-good changed: %#v", got)
	}
	valid, _ := marshalStarter(documentForPreset("deepseek"), "restored")
	if err := os.WriteFile(paths.GlobalConfig, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.GlobalConfig); err != nil {
		t.Fatal(err)
	}
	stable := manager.Snapshot()
	if err := manager.Reload(); err == nil {
		t.Fatal("deleted config reload unexpectedly succeeded")
	}
	if got := manager.Snapshot(); got.Revision != stable.Revision || !got.Ready {
		t.Fatalf("deleted config replaced snapshot: %#v", got)
	}
}

func TestExplicitReloadReResolvesCredentialWhenFileHashIsUnchanged(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	document := emptyDocument()
	document.Providers["private"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1", Auth: Auth{Credential: "provider/private"}}
	document.Models["private/model"] = Model{Provider: "private", Name: "model"}
	document.ActiveModel = "private/model"
	raw, _ := marshalStarter(document, "credential reload")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FakeCredentialStore{Values: map[string]string{"provider/private": "old-secret"}}
	manager := openTestManager(t, paths, store, false)
	before := manager.Snapshot()
	store.Values["provider/private"] = "new-secret"
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	after := manager.Snapshot()
	if after.Revision != before.Revision+1 || after.Active.APIKey != "new-secret" {
		t.Fatalf("reload revision/key=%d/%q before=%d", after.Revision, after.Active.APIKey, before.Revision)
	}
}

func TestWatcherObservesAtomicReplacement(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	doc := emptyDocument()
	doc.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	doc.Models["local/one"] = Model{Provider: "local", Name: "one"}
	doc.Models["local/two"] = Model{Provider: "local", Name: "two"}
	doc.ActiveModel = "local/one"
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := marshalStarter(doc, "watch test")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, true)
	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates
	doc.ActiveModel = "local/two"
	changed, _ := marshalStarter(doc, "watch test")
	if err := atomicWriteFile(paths.GlobalConfig, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-updates:
		if update.ActiveModelID != "local/two" {
			t.Fatalf("watch update active=%q", update.ActiveModelID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for config watcher")
	}
}

func TestLegacyMigrationIsIdempotentAndImportsPlaintextCredential(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":1,"activeModelProfileId":"deepseek","modelProfiles":[{"id":"deepseek","provider":"deepseek","apiKey":"legacy-secret","models":["deepseek-chat"],"model":"deepseek-chat"}]}`
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LegacyHome, "settings.json"), []byte(`{"ui":{"theme":"dracula"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LegacyHome, "mcp.toml"), []byte("# legacy mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacySkill := filepath.Join(paths.LegacyHome, "skills", "demo")
	if err := os.MkdirAll(legacySkill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySkill, "SKILL.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FakeCredentialStore{Values: map[string]string{}}
	manager := openTestManager(t, paths, store, false)
	if !manager.Snapshot().Ready {
		t.Fatalf("migrated snapshot not ready: %#v", manager.Snapshot().Diagnostics)
	}
	secret, err := store.Get(context.Background(), "provider/deepseek")
	if err != nil || secret != "legacy-secret" {
		t.Fatalf("migrated secret=%q err=%v", secret, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Home, "config-v1.backup.json")); err != nil {
		t.Fatalf("backup: %v", err)
	}
	for _, copied := range []string{paths.Settings, paths.MCP, filepath.Join(paths.Skills, "demo", "SKILL.md")} {
		if _, err := os.Stat(copied); err != nil {
			t.Fatalf("legacy asset %s: %v", copied, err)
		}
	}
	before, _ := os.ReadFile(paths.GlobalConfig)
	if _, _, err := migrateLegacy(context.Background(), paths, store); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(paths.GlobalConfig)
	if string(before) != string(after) {
		t.Fatal("idempotent migration overwrote v2 config")
	}
}

func TestLegacyPlaintextIsNotMigratedWithoutKeyring(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.LegacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":1,"modelProfiles":[{"id":"private","provider":"deepseek","apiKey":"must-not-leak","models":["deepseek-chat"]}]}`
	if err := os.WriteFile(paths.LegacyConfig, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.Snapshot().Ready || manager.RequireReady() == nil || !strings.Contains(manager.Snapshot().Diagnostics[0].Message, "not migrated") {
		t.Fatalf("blocked migration snapshot=%#v", manager.Snapshot())
	}
	if _, statErr := os.Stat(paths.GlobalConfig); !os.IsNotExist(statErr) {
		t.Fatalf("v2 config should not exist, stat=%v", statErr)
	}
}
