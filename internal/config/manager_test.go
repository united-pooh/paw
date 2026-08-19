package config

import (
	"bytes"
	"context"
	"encoding/json"
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
// exactly which startup or explicit refresh path performs live discovery.
type fakeModelDiscoverer struct {
	calls  []string
	models []DiscoveredModel
	err    error
}

func (f *fakeModelDiscoverer) Discover(_ context.Context, providerID string, _ Provider, _ string) ([]DiscoveredModel, error) {
	f.calls = append(f.calls, providerID)
	return append([]DiscoveredModel(nil), f.models...), f.err
}

type blockingModelDiscoverer struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	models  []DiscoveredModel
}

func (d *blockingModelDiscoverer) Discover(ctx context.Context, _ string, _ Provider, _ string) ([]DiscoveredModel, error) {
	d.calls.Add(1)
	close(d.started)
	select {
	case <-d.release:
		return append([]DiscoveredModel(nil), d.models...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type countingCredentialStore struct {
	inner *FakeCredentialStore
	gets  atomic.Int32
}

func (s *countingCredentialStore) Get(ctx context.Context, id string) (string, error) {
	s.gets.Add(1)
	return s.inner.Get(ctx, id)
}

func (s *countingCredentialStore) Set(ctx context.Context, id, secret string) error {
	return s.inner.Set(ctx, id, secret)
}

func (s *countingCredentialStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

type credentialObservingDiscoverer struct {
	store          *countingCredentialStore
	calls          atomic.Int32
	getsAtDiscover atomic.Int32
	credential     string
}

func (d *credentialObservingDiscoverer) Discover(_ context.Context, _ string, _ Provider, credential string) ([]DiscoveredModel, error) {
	d.calls.Add(1)
	d.getsAtDiscover.Store(d.store.gets.Load())
	d.credential = credential
	return []DiscoveredModel{{Name: "live"}}, nil
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

func TestManagerDiscoveryRunsAgainstEveryEnabledProvider(t *testing.T) {
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

	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "manual"}, {Name: "live-active"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if len(discoverer.calls) != 2 || discoverer.calls[0] != "active" || discoverer.calls[1] != "passive" {
		t.Fatalf("discovery calls = %#v, want active then passive", discoverer.calls)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Ready {
		t.Fatalf("snapshot not ready: %#v", snapshot.Diagnostics)
	}
	for _, id := range []string{"active/manual", "active/live-active", "passive/manual", "passive/live-active"} {
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
	if len(cache.Providers) != 2 || len(cache.Providers["active"].Models) != 2 || strings.Join(cache.Providers["passive"].Models, ",") != "live-active,manual" {
		t.Fatalf("cache did not retain live results for all providers: %#v", cache)
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

func TestManagerMultipleProvidersWithoutActiveDiscoversAll(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["one"] = discoveryTestProvider("http://127.0.0.1:1111/v1")
	document.Providers["two"] = discoveryTestProvider("http://127.0.0.1:2222/v1")
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "discovered"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if len(discoverer.calls) != 2 || discoverer.calls[0] != "one" || discoverer.calls[1] != "two" {
		t.Fatalf("discovery calls = %#v, want one then two", discoverer.calls)
	}
	for _, id := range []string{"one/discovered", "two/discovered"} {
		if _, ok := snapshot.EffectiveModels[id]; !ok {
			t.Fatalf("effective catalog missing %q: %#v", id, snapshot.EffectiveModels)
		}
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

func TestManagerExplicitDiscoveryRefreshRequestsAgain(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:1234/v1")
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "first"}}}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	before := manager.Snapshot()
	discoverer.models = []DiscoveredModel{{Name: "second"}}
	refreshed, err := manager.RefreshModelDiscovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(discoverer.calls) != 2 {
		t.Fatalf("discovery calls = %#v, want startup plus explicit refresh", discoverer.calls)
	}
	if refreshed.Revision != before.Revision+1 {
		t.Fatalf("refresh revision = %d, want %d", refreshed.Revision, before.Revision+1)
	}
	if _, ok := refreshed.EffectiveModels["local/second"]; !ok {
		t.Fatalf("refreshed catalog missing second model: %#v", refreshed.EffectiveModels)
	}
	if _, ok := refreshed.EffectiveModels["local/first"]; ok {
		t.Fatalf("refreshed catalog retained stale model: %#v", refreshed.EffectiveModels)
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

func TestManagerHTTPDiscoveryFiltersRawUnsafeNamesBeforeCatalogCacheAndSelectors(t *testing.T) {
	paddedOverlong := " " + strings.Repeat("x", 512) + " "
	tests := []struct {
		name            string
		rawNames        []string
		wantCache       string
		wantDiscovered  int
		wantFiltered    int
		wantCatalogSize int
	}{
		{
			name:            "mixed safe and rejected",
			rawNames:        []string{" safe ", "bad\x1bmodel", paddedOverlong, "   "},
			wantCache:       "safe",
			wantDiscovered:  4,
			wantFiltered:    3,
			wantCatalogSize: 2,
		},
		{
			name:            "all rejected",
			rawNames:        []string{"bad\nmodel", paddedOverlong, "\t"},
			wantDiscovered:  3,
			wantFiltered:    3,
			wantCatalogSize: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearDetectionEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				items := make([]map[string]string, len(test.rawNames))
				for index, name := range test.rawNames {
					items[index] = map[string]string{"id": name}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"data": items}); err != nil {
					t.Errorf("encode discovery response: %v", err)
				}
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
				Discoverer:   NewHTTPModelDiscoverer(server.Client()),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })

			snapshot := manager.Snapshot()
			if snapshot.Discovery.DiscoveredCount != test.wantDiscovered || snapshot.Discovery.FilteredCount != test.wantFiltered {
				t.Fatalf("discovery counts = discovered %d filtered %d, want %d/%d", snapshot.Discovery.DiscoveredCount, snapshot.Discovery.FilteredCount, test.wantDiscovered, test.wantFiltered)
			}
			if len(snapshot.EffectiveModels) != test.wantCatalogSize {
				t.Fatalf("effective catalog = %#v", snapshot.EffectiveModels)
			}
			if test.wantCache != "" {
				if item, ok := snapshot.EffectiveModels["local/"+test.wantCache]; !ok || item.Model.Name != test.wantCache {
					t.Fatalf("safe model missing from catalog: %#v", snapshot.EffectiveModels)
				}
			}
			for _, rawName := range test.rawNames {
				if rawName == " safe " {
					continue
				}
				id := "local/" + strings.TrimSpace(rawName)
				if _, err := snapshot.CatalogSelection(id); err == nil {
					t.Fatalf("unsafe raw model became selectable as %q", id)
				}
			}
			cache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
			if err != nil {
				t.Fatal(err)
			}
			entry := cache.Providers["local"]
			if got := strings.Join(entry.Models, ","); got != test.wantCache {
				t.Fatalf("cached models = %q, want %q", got, test.wantCache)
			}
			for _, cached := range entry.Models {
				if unsafeDiscoveredModelName(cached) || strings.TrimSpace(cached) != cached {
					t.Fatalf("unsafe model reached cache: %q", cached)
				}
			}
		})
	}
}

func TestManagerDiscoveryRereadsGlobalAndWorkspaceAfterBlockingRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, true)
	initialProvider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	initial := emptyDocument()
	initial.Providers["local"] = initialProvider
	initial.Models["local/one"] = Model{Provider: "local", Name: "one", ContextWindow: 10}
	initial.Models["local/two"] = Model{Provider: "local", Name: "two", ContextWindow: 10}
	initial.ActiveModel = "local/one"
	writeManagerDocument(t, paths, initial)
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	initialWorkspace := `{"schemaVersion":2,"activeModel":"local/one","models":{"local/one":{"parameters":{"temperature":0.1}}}}`
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(initialWorkspace), 0o600); err != nil {
		t.Fatal(err)
	}

	discoverer := &blockingModelDiscoverer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		models:  []DiscoveredModel{{Name: "stale-live"}},
	}
	t.Cleanup(func() {
		select {
		case <-discoverer.release:
		default:
			close(discoverer.release)
		}
	})
	type openResult struct {
		manager *Manager
		err     error
	}
	opened := make(chan openResult, 1)
	go func() {
		manager, err := Open(context.Background(), Options{
			Paths:       paths,
			Credentials: &FakeCredentialStore{Unavailable: true},
			Discoverer:  discoverer,
			Debounce:    20 * time.Millisecond,
		})
		opened <- openResult{manager: manager, err: err}
	}()

	select {
	case <-discoverer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for blocking discovery")
	}
	finalProvider := discoveryTestProvider("http://127.0.0.1:5678/v1")
	finalDocument := emptyDocument()
	finalDocument.Providers["local"] = finalProvider
	finalDocument.Models["local/one"] = Model{Provider: "local", Name: "one", ContextWindow: 10}
	finalDocument.Models["local/two"] = Model{Provider: "local", Name: "two", ContextWindow: 42}
	finalDocument.ActiveModel = "local/two"
	writeManagerDocument(t, paths, finalDocument)
	finalWorkspace := `{"schemaVersion":2,"activeModel":"local/two","models":{"local/two":{"stream":false,"parameters":{"temperature":0.9}}}}`
	if err := atomicWriteFile(paths.WorkspaceConfig, []byte(finalWorkspace), 0o600); err != nil {
		t.Fatal(err)
	}
	close(discoverer.release)

	var result openResult
	select {
	case result = <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for manager open")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	manager := result.manager
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if snapshot.ActiveModelID != "local/two" || snapshot.Document.Providers["local"].Endpoint != finalProvider.Endpoint {
		t.Fatalf("final global config was not reread: active=%q provider=%#v", snapshot.ActiveModelID, snapshot.Document.Providers["local"])
	}
	if snapshot.Active.APIBaseURL != finalProvider.Endpoint || snapshot.Active.ModelContextLimitTokens["two"] != 42 {
		t.Fatalf("final runtime did not use reread global config: %#v", snapshot.Active)
	}
	if snapshot.Active.Stream || snapshot.Active.ModelExtraBody["two"]["temperature"] != float64(0.9) {
		t.Fatalf("final runtime did not use reread workspace config: %#v", snapshot.Active)
	}
	if _, ok := snapshot.EffectiveModels["local/stale-live"]; ok {
		t.Fatalf("discovery from the old endpoint remained applicable: %#v", snapshot.EffectiveModels)
	}
	if got := discoverer.calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
}

func TestManagerDiscoveryPersistsLiveOnlyAfterFinalDocumentConfirmsProvenance(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	queriedProvider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	currentProvider := discoveryTestProvider("http://127.0.0.1:5678/v1")
	initial := emptyDocument()
	initial.Providers["local"] = queriedProvider
	initial.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	initial.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, initial)

	cachedAt := time.Unix(700, 0).UTC()
	originalCache := discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(currentProvider),
				Format:              currentProvider.Discovery.Format,
				DiscoveredAt:        cachedAt,
				Models:              []string{"cached-current"},
			},
		},
	}
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, originalCache); err != nil {
		t.Fatal(err)
	}

	discoverer := &blockingModelDiscoverer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		models:  []DiscoveredModel{{Name: "stale-live"}},
	}
	t.Cleanup(func() {
		select {
		case <-discoverer.release:
		default:
			close(discoverer.release)
		}
	})
	type openResult struct {
		manager *Manager
		err     error
	}
	opened := make(chan openResult, 1)
	go func() {
		manager, err := Open(context.Background(), Options{
			Paths:        paths,
			Credentials:  &FakeCredentialStore{Unavailable: true},
			DisableWatch: true,
			Discoverer:   discoverer,
		})
		opened <- openResult{manager: manager, err: err}
	}()

	select {
	case <-discoverer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for blocking discovery")
	}
	current := cloneDocument(initial)
	current.Providers["local"] = currentProvider
	writeManagerDocument(t, paths, current)
	close(discoverer.release)

	var result openResult
	select {
	case result = <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for manager open")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	manager := result.manager
	t.Cleanup(func() { _ = manager.Close() })

	snapshot := manager.Snapshot()
	if snapshot.Document.Providers["local"].Endpoint != currentProvider.Endpoint || snapshot.Active.APIBaseURL != currentProvider.Endpoint {
		t.Fatalf("final snapshot did not use current provider: document=%#v active=%#v", snapshot.Document.Providers["local"], snapshot.Active)
	}
	if _, ok := snapshot.EffectiveModels["local/cached-current"]; !ok || snapshot.Discovery.Source != "cache" {
		t.Fatalf("final snapshot did not use matching loaded cache: %#v / %#v", snapshot.EffectiveModels, snapshot.Discovery)
	}
	if _, ok := snapshot.EffectiveModels["local/stale-live"]; ok {
		t.Fatalf("stale live result remained applicable: %#v", snapshot.EffectiveModels)
	}
	if got := discoverer.calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}

	writeManagerDocument(t, paths, initial)
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	reverted := manager.Snapshot()
	if _, ok := reverted.EffectiveModels["local/stale-live"]; ok || reverted.Discovery.Source == "live" {
		t.Fatalf("unconfirmed live result became applicable on a later reload: %#v / %#v", reverted.EffectiveModels, reverted.Discovery)
	}
	if got := discoverer.calls.Load(); got != 1 {
		t.Fatalf("discovery calls after reload = %d, want 1", got)
	}
	if entry := manager.discoveryCache.Providers["local"]; entry.EndpointFingerprint != discoveryEndpointFingerprint(currentProvider) || strings.Join(entry.Models, ",") != "cached-current" || !entry.DiscoveredAt.Equal(cachedAt) {
		t.Fatalf("in-memory loaded cache was overwritten by stale live result: %#v", manager.discoveryCache)
	}
	diskCache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
	if err != nil {
		t.Fatal(err)
	}
	if entry := diskCache.Providers["local"]; entry.EndpointFingerprint != discoveryEndpointFingerprint(currentProvider) || strings.Join(entry.Models, ",") != "cached-current" || !entry.DiscoveredAt.Equal(cachedAt) {
		t.Fatalf("persistent cache was overwritten by stale live result: %#v", diskCache)
	}
}

func TestManagerDiscoveryWatcherClosesPostReadRegistrationGap(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("PAW_TEST_LOCAL", "secret")
	paths := isolatedPaths(t, true)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	provider.Auth = Auth{Env: []string{"PAW_TEST_LOCAL"}}
	initial := emptyDocument()
	initial.Providers["local"] = provider
	initial.Models["local/one"] = Model{Provider: "local", Name: "one"}
	initial.Models["local/two"] = Model{Provider: "local", Name: "two"}
	initial.ActiveModel = "local/one"
	writeManagerDocument(t, paths, initial)
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(`{"schemaVersion":2,"activeModel":"local/one"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	discoverer := &blockingModelDiscoverer{
		started: make(chan struct{}),
		release: make(chan struct{}),
		models:  []DiscoveredModel{{Name: "old-live"}},
	}
	t.Cleanup(func() {
		select {
		case <-discoverer.release:
		default:
			close(discoverer.release)
		}
	})
	type openResult struct {
		manager *Manager
		err     error
	}
	opened := make(chan openResult, 1)
	go func() {
		manager, err := Open(context.Background(), Options{
			Paths:       paths,
			Credentials: &FakeCredentialStore{Unavailable: true},
			Discoverer:  discoverer,
			Debounce:    20 * time.Millisecond,
		})
		opened <- openResult{manager: manager, err: err}
	}()

	select {
	case <-discoverer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for startup discovery")
	}
	finalProvider := cloneProvider(provider)
	finalProvider.Endpoint = "http://127.0.0.1:5678/v1"
	finalDocument := cloneDocument(initial)
	finalDocument.Providers["local"] = finalProvider
	finalDocument.ActiveModel = "local/two"
	writeManagerDocument(t, paths, finalDocument)
	if err := atomicWriteFile(paths.WorkspaceConfig, []byte(`{"schemaVersion":2,"activeModel":"local/two","models":{"local/two":{"stream":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	close(discoverer.release)

	var result openResult
	select {
	case result = <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for manager open")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	manager := result.manager
	t.Cleanup(func() { _ = manager.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := manager.Snapshot()
		if snapshot.ActiveModelID == "local/two" && snapshot.Active.APIBaseURL == finalProvider.Endpoint && !snapshot.Active.Stream {
			if _, ok := snapshot.EffectiveModels["local/old-live"]; ok {
				t.Fatalf("old discovery remained applicable after watched endpoint change: %#v", snapshot.EffectiveModels)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-registration changes were missed: active=%q endpoint=%q stream=%v", snapshot.ActiveModelID, snapshot.Active.APIBaseURL, snapshot.Active.Stream)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := discoverer.calls.Load(); got != 1 {
		t.Fatalf("discovery calls after queued watcher reload = %d", got)
	}
}

func TestManagerDiscoveryDisabledThenEnabledUsesLoadedCacheWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	disabled := false
	provider.Discovery.Enabled = &disabled
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)

	enabledProvider := cloneProvider(provider)
	enabled := true
	enabledProvider.Discovery.Enabled = &enabled
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(enabledProvider),
				Format:              enabledProvider.Discovery.Format,
				DiscoveredAt:        time.Unix(400, 0).UTC(),
				Models:              []string{"cached-after-enable"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected"}}}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, ok := manager.Snapshot().EffectiveModels["local/cached-after-enable"]; ok {
		t.Fatal("disabled provider unexpectedly used cached discovery")
	}

	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", enabledProvider)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.EffectiveModels["local/cached-after-enable"]; !ok || after.Discovery.Source != "cache" {
		t.Fatalf("enabled provider did not recover loaded cache: %#v / %#v", after.EffectiveModels, after.Discovery)
	}
	if len(discoverer.calls) != 0 {
		t.Fatalf("discovery calls after enabling provider = %#v", discoverer.calls)
	}
}

func TestManagerDiscoveryMismatchedThenMatchingReloadUsesLoadedCacheWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	for _, testCase := range []struct {
		name   string
		mutate func(*Provider)
	}{
		{name: "endpoint", mutate: func(provider *Provider) { provider.Endpoint = "http://127.0.0.1:5678/v1" }},
		{name: "path", mutate: func(provider *Provider) { provider.Discovery.Path = "other-models" }},
		{name: "format", mutate: func(provider *Provider) { provider.Discovery.Format = DiscoveryFormatOllamaTags }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			paths := isolatedPaths(t, false)
			matchingProvider := discoveryTestProvider("http://127.0.0.1:1234/v1")
			mismatchedProvider := cloneProvider(matchingProvider)
			testCase.mutate(&mismatchedProvider)
			document := emptyDocument()
			document.Providers["local"] = mismatchedProvider
			document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
			document.ActiveModel = "local/manual"
			writeManagerDocument(t, paths, document)
			if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
				Version: discoveryCacheVersion,
				Providers: map[string]discoveryCacheEntry{
					"local": {
						EndpointFingerprint: discoveryEndpointFingerprint(matchingProvider),
						Format:              matchingProvider.Discovery.Format,
						DiscoveredAt:        time.Unix(500, 0).UTC(),
						Models:              []string{"cached-after-match"},
					},
				},
			}); err != nil {
				t.Fatal(err)
			}
			discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected"}}}
			manager, err := Open(context.Background(), Options{
				Paths:                 paths,
				Credentials:           &FakeCredentialStore{Unavailable: true},
				DisableWatch:          true,
				DisableModelDiscovery: true,
				Discoverer:            discoverer,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			if _, ok := manager.Snapshot().EffectiveModels["local/cached-after-match"]; ok {
				t.Fatal("mismatched provider unexpectedly used cached discovery")
			}

			document.Providers["local"] = matchingProvider
			writeManagerDocument(t, paths, document)
			if err := manager.Reload(); err != nil {
				t.Fatal(err)
			}
			snapshot := manager.Snapshot()
			if _, ok := snapshot.EffectiveModels["local/cached-after-match"]; !ok || snapshot.Discovery.Source != "cache" {
				t.Fatalf("matching reload did not recover loaded cache: %#v / %#v", snapshot.EffectiveModels, snapshot.Discovery)
			}
			if len(discoverer.calls) != 0 {
				t.Fatalf("discovery calls after matching reload = %#v", discoverer.calls)
			}
		})
	}
}

func TestManagerDiscoveryCredentialResolutionUsesEnvironmentOnly(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("PAW_TEST_LOCAL", "secret")
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	provider.Auth = Auth{Env: []string{"PAW_TEST_LOCAL"}}
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	store := &countingCredentialStore{inner: &FakeCredentialStore{Values: map[string]string{"provider/local": "keyring-secret"}}}
	discoverer := &credentialObservingDiscoverer{store: store}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: store, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if got := discoverer.calls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
	if got := store.gets.Load(); got != 0 {
		t.Fatalf("keyring was consulted %d times; secrets must come from env only", got)
	}
	if discoverer.credential != "secret" || manager.Snapshot().Active.APIKey != "secret" {
		t.Fatalf("credential was not propagated safely: discovery=%q runtime=%q", discoverer.credential, manager.Snapshot().Active.APIKey)
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

func TestManagerDiscoveryDiagnosticsFollowCurrentApplicability(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Providers["other"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:9999/v1"}
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.Models["other/manual"] = Model{Provider: "other", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoverer := &fakeModelDiscoverer{err: errors.New("offline")}

	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	assertWarningDiagnostic(t, manager.Snapshot(), "continuing with manual models")

	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetActiveModel("other/manual")})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "continuing with manual models")
	after, err = manager.Update(context.Background(), after.Revision, []Operation{SetActiveModel("local/manual")})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "continuing with manual models")

	disabledProvider := cloneProvider(provider)
	disabled := false
	disabledProvider.Discovery.Enabled = &disabled
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", disabledProvider)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoWarningDiagnostic(t, after, "continuing with manual models")
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "continuing with manual models")

	mismatchedProvider := cloneProvider(provider)
	mismatchedProvider.Endpoint = "http://127.0.0.1:5678/v1"
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", mismatchedProvider)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoWarningDiagnostic(t, after, "continuing with manual models")
	mismatchedProvider = cloneProvider(provider)
	mismatchedProvider.Discovery.Format = DiscoveryFormatOllamaTags
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", mismatchedProvider)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoWarningDiagnostic(t, after, "continuing with manual models")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after applicability updates = %#v", discoverer.calls)
	}
}

func TestManagerSuccessfulDiscoveryCacheRepairClearsReadWarning(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := os.WriteFile(paths.ModelDiscoveryCache, []byte(`{"version":1,"providers":`), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := Open(context.Background(), Options{
		Paths:        paths,
		Credentials:  &FakeCredentialStore{Unavailable: true},
		DisableWatch: true,
		Discoverer:   &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "repaired"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshot := manager.Snapshot()
	assertNoWarningDiagnostic(t, snapshot, "cache could not be read")
	if manager.discoveryCacheInvalid || snapshot.Discovery.CacheState != "updated" {
		t.Fatalf("successful repair retained invalid cache state: invalid=%v status=%#v", manager.discoveryCacheInvalid, snapshot.Discovery)
	}
	cache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
	if err != nil || strings.Join(cache.Providers["local"].Models, ",") != "repaired" {
		t.Fatalf("repaired cache = %#v, err=%v", cache, err)
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

func TestManagerDiscoveryCacheWriteFailurePreservesLoadedCacheAndLivePrecedence(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	discoveredAt := time.Unix(600, 0).UTC()
	oldCache := discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(provider),
				Format:              provider.Discovery.Format,
				DiscoveredAt:        discoveredAt,
				Models:              []string{"cached-old"},
			},
		},
	}
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, oldCache); err != nil {
		t.Fatal(err)
	}
	var attempted discoveryCacheFile
	forcedWriteFailure := func(_ string, cache discoveryCacheFile) error {
		attempted = cloneDiscoveryCache(cache)
		entry := cache.Providers["local"]
		entry.Models = []string{"writer-poison"}
		cache.Providers["local"] = entry
		return errors.New("forced cache write failure")
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "live-new"}}}

	manager, err := openManager(context.Background(), Options{
		Paths:        paths,
		Credentials:  &FakeCredentialStore{Unavailable: true},
		DisableWatch: true,
		Discoverer:   discoverer,
	}, forcedWriteFailure)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshot := manager.Snapshot()
	if _, ok := snapshot.EffectiveModels["local/live-new"]; !ok {
		t.Fatalf("process-local live result missing after cache failure: %#v", snapshot.EffectiveModels)
	}
	if _, ok := snapshot.EffectiveModels["local/cached-old"]; ok {
		t.Fatalf("cached result overrode process-local live result: %#v", snapshot.EffectiveModels)
	}
	if snapshot.Discovery.Source != "live" || snapshot.Discovery.CacheProviders != 1 || snapshot.Discovery.CacheState != "write-failed" {
		t.Fatalf("cache failure status = %#v", snapshot.Discovery)
	}
	if strings.Join(attempted.Providers["local"].Models, ",") != "live-new" {
		t.Fatalf("writer did not receive updated clone: %#v", attempted)
	}
	if strings.Join(manager.discoveryCache.Providers["local"].Models, ",") != "cached-old" {
		t.Fatalf("failed write mutated loaded cache: %#v", manager.discoveryCache)
	}
	diskCache, err := readDiscoveryCache(paths.ModelDiscoveryCache)
	if err != nil || strings.Join(diskCache.Providers["local"].Models, ",") != "cached-old" {
		t.Fatalf("failed write changed disk cache: %#v, err=%v", diskCache, err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if reloaded := manager.Snapshot(); reloaded.Discovery.Source != "live" {
		t.Fatalf("reload lost live precedence: %#v", reloaded.Discovery)
	} else if _, ok := reloaded.EffectiveModels["local/cached-old"]; ok {
		t.Fatalf("reload restored stale cache over live result: %#v", reloaded.EffectiveModels)
	}
	changedProvider := cloneProvider(provider)
	changedProvider.Endpoint = "http://127.0.0.1:5678/v1"
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", changedProvider)})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "cache could not be updated")
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatal(err)
	}
	assertWarningDiagnostic(t, after, "cache could not be updated")
	if len(discoverer.calls) != 1 {
		t.Fatalf("discovery calls after failed-write reload/update = %#v", discoverer.calls)
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
	if got := len(manager.discoveryCache.Providers); got != 0 {
		t.Fatalf("failed cache write mutated loaded cache: %#v", manager.discoveryCache)
	}
	if snapshot := manager.Snapshot(); snapshot.Discovery.CacheProviders != 0 || snapshot.Discovery.CacheState != "write-failed" {
		t.Fatalf("failed cache write reported mutated cache state: %#v", snapshot.Discovery)
	}
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

func assertNoWarningDiagnostic(t *testing.T, snapshot Snapshot, substring string) {
	t.Helper()
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Severity == "warning" && strings.Contains(diagnostic.Message, substring) {
			t.Fatalf("unexpected warning diagnostic containing %q: %#v", substring, snapshot.Diagnostics)
		}
	}
}

func TestAtomicWriteNewConfigFilePreservesExistingWinner(t *testing.T) {
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	external := []byte("external config winner\n")
	if err := os.WriteFile(paths.GlobalConfig, external, 0o640); err != nil {
		t.Fatal(err)
	}

	err := atomicWriteNewConfigFile(paths.GlobalConfig, []byte("losing starter\n"), 0o600)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("initial config write error = %v, want revision conflict", err)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, external) {
		t.Fatalf("existing config winner was overwritten: got=%q want=%q", written, external)
	}
	info, err := os.Stat(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("existing winner mode changed = %v, want 0640", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(paths.Home, ".paw-config-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary config files survived initial CAS conflict: %#v", matches)
	}
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

// TestOpenRegeneratesStarterForBlankConfigFile 验证空（0 字节/纯空白/仅注释）
// 的配置文件在启动时按首次运行重建，不再以 "EOF" 阻塞后续所有 /config 更新。
func TestOpenRegeneratesStarterForBlankConfigFile(t *testing.T) {
	clearDetectionEnv(t)
	for _, content := range [][]byte{nil, []byte("  \n"), []byte("// only a comment\n")} {
		paths := isolatedPaths(t, false)
		if err := os.MkdirAll(paths.Home, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.GlobalConfig, content, 0o600); err != nil {
			t.Fatal(err)
		}
		manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
		snapshot := manager.Snapshot()
		for _, diagnostic := range snapshot.Diagnostics {
			if strings.Contains(diagnostic.Message, "EOF") {
				t.Fatalf("blank config %q still surfaced EOF: %#v", content, snapshot.Diagnostics)
			}
		}
		raw, err := os.ReadFile(paths.GlobalConfig)
		if err != nil {
			t.Fatal(err)
		}
		if blankConfigRaw(raw) {
			t.Fatalf("blank config %q was not regenerated", content)
		}
		if _, _, err := parseAndValidateGlobal(raw, paths.GlobalConfig); err != nil {
			t.Fatalf("regenerated config for %q does not parse: %v", content, err)
		}
		// 修复后 /config 的更新（如选择服务商）必须能直接成功。
		updated, err := manager.Update(context.Background(), snapshot.Revision, []Operation{
			UpsertProvider("local", Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}),
		})
		if err != nil {
			t.Fatalf("update after blank repair failed for %q: %v", content, err)
		}
		if _, ok := updated.Document.Providers["local"]; !ok {
			t.Fatalf("provider missing after update for %q: %#v", content, updated.Document.Providers)
		}
	}
}

// TestUpdateHealsBlankBaseSnapshot 验证更新路径的自愈：基准快照建立时配置
// 文件为空（启动加载以 EOF 失败的场景），更新应从空 v2 文档开始打补丁并
// 直接修复文件，而不是反复返回 "EOF"。
func TestUpdateHealsBlankBaseSnapshot(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	// 模拟「启动时配置文件为空」建立的失败快照：Raw 是空内容、文档为空。
	blank := []byte(" \n")
	if err := os.WriteFile(paths.GlobalConfig, blank, 0o600); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.snapshot = Snapshot{
		Document:           emptyDocument(),
		Revision:           manager.snapshot.Revision,
		Raw:                blank,
		globalConfigExists: true,
		LoadedAt:           time.Now(),
	}
	manager.mu.Unlock()

	updated, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{
		UpsertProvider("local", Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}),
		UpsertModel("local/one", Model{Provider: "local", Name: "one"}),
		SetActiveModel("local/one"),
	})
	if err != nil {
		t.Fatalf("update on blank base failed: %v", err)
	}
	if updated.Document.Providers["local"].Endpoint != "http://127.0.0.1:8000/v1" || updated.ActiveModelID != "local/one" {
		t.Fatalf("healed update produced %#v active=%q", updated.Document, updated.ActiveModelID)
	}
	raw, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseAndValidateGlobal(raw, paths.GlobalConfig); err != nil {
		t.Fatalf("healed config file does not parse: %v", err)
	}
}

// TestBlankWorkspaceConfigIsIgnored 验证空的工作区配置（0 字节/纯空白/仅
// 注释，例如写盘被中断）按无覆盖处理：全局配置正常生效，加载与 /config
// 更新都不会被 "<workspace>/config.jsonc: EOF" 阻塞。
func TestBlankWorkspaceConfigIsIgnored(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, true)
	initial := emptyDocument()
	initial.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	initial.Models["local/one"] = Model{Provider: "local", Name: "one"}
	initial.Models["local/two"] = Model{Provider: "local", Name: "two"}
	initial.ActiveModel = "local/one"
	writeManagerDocument(t, paths, initial)
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, "EOF") {
			t.Fatalf("blank workspace config surfaced EOF: %#v", snapshot.Diagnostics)
		}
	}
	if snapshot.ActiveModelID != "local/one" {
		t.Fatalf("blank workspace changed active model: %q", snapshot.ActiveModelID)
	}

	// 更新（如选择服务商/模型）必须能越过空工作区文件直接成功。
	updated, err := manager.Update(context.Background(), snapshot.Revision, []Operation{SetActiveModel("local/two")})
	if err != nil {
		t.Fatalf("update with blank workspace config failed: %v", err)
	}
	if updated.ActiveModelID != "local/two" {
		t.Fatalf("update active model = %q", updated.ActiveModelID)
	}
}

func TestFirstRunIgnoresLegacyConfigJSONButCopiesNonConfigAssets(t *testing.T) {
	clearDetectionEnv(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	legacyHome := filepath.Join(home, ".paw")
	legacyPath := filepath.Join(legacyHome, "config.json")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schemaVersion":1,"modelProfiles":[{"id":"legacy","provider":"deepseek","apiKey":"must-not-import","model":"deepseek-chat"}]}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	legacySettings := []byte(`{"ui":{"theme":"dracula"}}`)
	if err := os.WriteFile(filepath.Join(legacyHome, "settings.json"), legacySettings, 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := ResolvePaths(PathOptions{ConfigHome: filepath.Join(root, "config", "Paw"), UserHomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	// 零凭据时 starter 落到内置 deepseek 默认（Preset 字段标记来源），但
	// legacy v1 的 modelProfiles（含明文 apiKey）绝不能被导入：providers
	// 只允许内置 preset，models 只允许 deepseek/chat。
	if len(snapshot.Document.Providers) != 1 {
		t.Fatalf("expected only the built-in default provider, got %#v", snapshot.Document.Providers)
	}
	provider, ok := snapshot.Document.Providers["deepseek"]
	if !ok || provider.Preset != "deepseek" {
		t.Fatalf("legacy config was imported: %#v", snapshot.Document.Providers)
	}
	if _, ok := snapshot.Document.Models["legacy"]; ok {
		t.Fatalf("legacy profile was imported: %#v", snapshot.Document.Models)
	}
	if len(snapshot.Document.Models) != 1 || snapshot.ActiveModelID != "deepseek/chat" {
		t.Fatalf("unexpected starter models: active=%q models=%#v", snapshot.ActiveModelID, snapshot.Document.Models)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, legacy) {
		t.Fatalf("legacy config was modified: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Home, "config-v1.backup.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy backup should not be created, stat=%v", err)
	}
	if got, err := os.ReadFile(paths.Settings); err != nil || !bytes.Equal(got, legacySettings) {
		t.Fatalf("non-config legacy settings were not copied: got=%q err=%v", got, err)
	}
}

func TestManagerInvalidGlobalStartupExplicitReloadUsesLoadedDiscoveryCacheWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GlobalConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(provider),
				Format:              provider.Discovery.Format,
				DiscoveredAt:        time.Unix(800, 0).UTC(),
				Models:              []string{"cached-after-repair"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected-live"}}}
	manager, err := Open(context.Background(), Options{
		Paths:        paths,
		Credentials:  &FakeCredentialStore{Unavailable: true},
		DisableWatch: true,
		Discoverer:   discoverer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if manager.Snapshot().Ready || len(manager.Snapshot().Diagnostics) == 0 {
		t.Fatalf("invalid startup snapshot=%#v", manager.Snapshot())
	}
	if entry, ok := manager.discoveryCache.Providers["local"]; !ok || strings.Join(entry.Models, ",") != "cached-after-repair" {
		t.Fatalf("valid cache was not loaded independently of startup parsing: %#v", manager.discoveryCache)
	}
	if len(discoverer.calls) != 0 {
		t.Fatalf("invalid startup unexpectedly requested discovery: %#v", discoverer.calls)
	}

	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Ready || snapshot.Discovery.Source != "cache" {
		t.Fatalf("repaired reload did not use loaded cache: ready=%v discovery=%#v diagnostics=%#v", snapshot.Ready, snapshot.Discovery, snapshot.Diagnostics)
	}
	if _, ok := snapshot.EffectiveModels["local/cached-after-repair"]; !ok {
		t.Fatalf("repaired reload catalog = %#v", snapshot.EffectiveModels)
	}
	if len(discoverer.calls) != 0 {
		t.Fatalf("repaired reload unexpectedly requested discovery: %#v", discoverer.calls)
	}
}

func TestManagerInvalidWorkspaceStartupWatcherRepairUsesLoadedDiscoveryCacheWithoutRequest(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, true)
	provider := discoveryTestProvider("http://127.0.0.1:1234/v1")
	document := emptyDocument()
	document.Providers["local"] = provider
	document.Models["local/manual"] = Model{Provider: "local", Name: "manual"}
	document.ActiveModel = "local/manual"
	writeManagerDocument(t, paths, document)
	if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WorkspaceConfig, []byte(`{"schemaVersion":2,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDiscoveryCache(paths.ModelDiscoveryCache, discoveryCacheFile{
		Version: discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{
			"local": {
				EndpointFingerprint: discoveryEndpointFingerprint(provider),
				Format:              provider.Discovery.Format,
				DiscoveredAt:        time.Unix(900, 0).UTC(),
				Models:              []string{"cached-after-watch-repair"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	discoverer := &fakeModelDiscoverer{models: []DiscoveredModel{{Name: "unexpected-live"}}}
	manager, err := Open(context.Background(), Options{
		Paths:       paths,
		Credentials: &FakeCredentialStore{Unavailable: true},
		Discoverer:  discoverer,
		Debounce:    20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if manager.Snapshot().Ready || len(manager.Snapshot().Diagnostics) == 0 {
		t.Fatalf("invalid workspace startup snapshot=%#v", manager.Snapshot())
	}
	if entry, ok := manager.discoveryCache.Providers["local"]; !ok || strings.Join(entry.Models, ",") != "cached-after-watch-repair" {
		t.Fatalf("valid cache was not loaded independently of workspace parsing: %#v", manager.discoveryCache)
	}
	if len(discoverer.calls) != 0 {
		t.Fatalf("invalid workspace startup unexpectedly requested discovery: %#v", discoverer.calls)
	}

	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates
	if err := atomicWriteFile(paths.WorkspaceConfig, []byte(`{"schemaVersion":2,"activeModel":"local/manual"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case snapshot := <-updates:
		if !snapshot.Ready || snapshot.Discovery.Source != "cache" {
			t.Fatalf("watcher repair did not use loaded cache: ready=%v discovery=%#v diagnostics=%#v", snapshot.Ready, snapshot.Discovery, snapshot.Diagnostics)
		}
		if _, ok := snapshot.EffectiveModels["local/cached-after-watch-repair"]; !ok {
			t.Fatalf("watcher repair catalog = %#v", snapshot.EffectiveModels)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for repaired workspace reload")
	}
	if len(discoverer.calls) != 0 {
		t.Fatalf("watcher repair unexpectedly requested discovery: %#v", discoverer.calls)
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

func TestFirstRunWithoutCredentialsDefaultsToDeepSeek(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if snapshot.ActiveModelID != "deepseek/chat" {
		t.Fatalf("active = %q, want deepseek/chat", snapshot.ActiveModelID)
	}
	if _, ok := snapshot.Document.Providers["deepseek"]; !ok {
		t.Fatalf("starter providers missing deepseek: %#v", snapshot.Document.Providers)
	}
	// 凭据尚未设置：Ready 必须仍为 false，RequireReady 仍需引导用户补凭据。
	if snapshot.Ready {
		t.Fatal("defaulted starter without credentials should not be ready")
	}
	setupErr := manager.RequireReady()
	if !errors.As(setupErr, new(*SetupRequiredError)) {
		t.Fatalf("RequireReady() = %v", setupErr)
	}
	if got := setupErr.Error(); !strings.Contains(got, paths.GlobalConfig) {
		t.Fatalf("setup-required error lacks config path: %v", got)
	}
	found := false
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, "defaulted to built-in deepseek") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics missing deepseek default notice: %#v", snapshot.Diagnostics)
	}
	// 设置凭据并 reload 后应直接就绪（开箱可用，无需 /config）。
	t.Setenv("DEEPSEEK_API_KEY", "secret-from-env")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if !manager.Snapshot().Ready || manager.Snapshot().ActiveModelID != "deepseek/chat" {
		t.Fatalf("after key set: ready:%v active:%q diagnostics:%#v", manager.Snapshot().Ready, manager.Snapshot().ActiveModelID, manager.Snapshot().Diagnostics)
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

func TestManagerPreviewUpdateDoesNotWritePublishOrMutate(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	beforeFile, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	updates, cancel := manager.Subscribe()
	defer cancel()
	<-updates

	preview, err := manager.PreviewUpdate(context.Background(), before.Revision, []Operation{SetActiveModel("local/two")})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Revision != before.Revision+1 || preview.ActiveModelID != "local/two" || preview.Active.Model != "two" {
		t.Fatalf("preview = revision %d active %q runtime %q", preview.Revision, preview.ActiveModelID, preview.Active.Model)
	}
	if after := manager.Snapshot(); after.Revision != before.Revision || after.ActiveModelID != before.ActiveModelID || after.ContentHash != before.ContentHash {
		t.Fatalf("preview mutated manager snapshot: before=%#v after=%#v", before, after)
	}
	afterFile, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFile) != string(beforeFile) {
		t.Fatalf("preview changed config file:\nbefore=%s\nafter=%s", beforeFile, afterFile)
	}
	select {
	case update := <-updates:
		t.Fatalf("preview published snapshot: %#v", update)
	default:
	}
}

func TestManagerPreviewUpdateRejectsExternalGlobalAndWorkspaceStateChanges(t *testing.T) {
	tests := []struct {
		name               string
		workspace          bool
		workspaceInitially bool
		mutate             func(t *testing.T, paths Paths, external []byte)
		assertExternal     func(t *testing.T, paths Paths, external []byte)
	}{
		{
			name: "global content replacement",
			mutate: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				if err := atomicWriteFile(paths.GlobalConfig, external, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			assertExternal: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				got, err := os.ReadFile(paths.GlobalConfig)
				if err != nil || !bytes.Equal(got, external) {
					t.Fatalf("external global bytes changed: got=%s err=%v", got, err)
				}
			},
		},
		{
			name: "global deletion",
			mutate: func(t *testing.T, paths Paths, _ []byte) {
				t.Helper()
				if err := os.Remove(paths.GlobalConfig); err != nil {
					t.Fatal(err)
				}
			},
			assertExternal: func(t *testing.T, paths Paths, _ []byte) {
				t.Helper()
				if _, err := os.Stat(paths.GlobalConfig); !os.IsNotExist(err) {
					t.Fatalf("deleted global config was recreated: %v", err)
				}
			},
		},
		{
			name:               "workspace content replacement",
			workspace:          true,
			workspaceInitially: true,
			mutate: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				if err := atomicWriteFile(paths.WorkspaceConfig, external, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			assertExternal: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				got, err := os.ReadFile(paths.WorkspaceConfig)
				if err != nil || !bytes.Equal(got, external) {
					t.Fatalf("external workspace bytes changed: got=%s err=%v", got, err)
				}
			},
		},
		{
			name:               "workspace deletion",
			workspace:          true,
			workspaceInitially: true,
			mutate: func(t *testing.T, paths Paths, _ []byte) {
				t.Helper()
				if err := os.Remove(paths.WorkspaceConfig); err != nil {
					t.Fatal(err)
				}
			},
			assertExternal: func(t *testing.T, paths Paths, _ []byte) {
				t.Helper()
				if _, err := os.Stat(paths.WorkspaceConfig); !os.IsNotExist(err) {
					t.Fatalf("deleted workspace config was recreated: %v", err)
				}
			},
		},
		{
			name:      "workspace creation",
			workspace: true,
			mutate: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.WorkspaceConfig, external, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			assertExternal: func(t *testing.T, paths Paths, external []byte) {
				t.Helper()
				got, err := os.ReadFile(paths.WorkspaceConfig)
				if err != nil || !bytes.Equal(got, external) {
					t.Fatalf("created workspace bytes changed: got=%s err=%v", got, err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearDetectionEnv(t)
			paths := isolatedPaths(t, test.workspace)
			document := emptyDocument()
			document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
			document.Models["local/one"] = Model{Provider: "local", Name: "one"}
			document.Models["local/two"] = Model{Provider: "local", Name: "two"}
			document.ActiveModel = "local/one"
			writeManagerDocument(t, paths, document)
			if test.workspaceInitially {
				if err := os.MkdirAll(filepath.Dir(paths.WorkspaceConfig), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.WorkspaceConfig, []byte(`{"schemaVersion":2}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
			before := manager.Snapshot()
			external := []byte(`{"schemaVersion":2,"models":{"local/two":{"stream":false}}}`)
			if !test.workspace {
				externalDocument := cloneDocument(document)
				externalDocument.ActiveModel = "local/two"
				var err error
				external, err = marshalStarter(externalDocument, "external editor")
				if err != nil {
					t.Fatal(err)
				}
			}
			test.mutate(t, paths, external)

			_, err := manager.PreviewUpdate(context.Background(), before.Revision, []Operation{SetActiveModel("local/two")})
			if !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("preview error = %v, want external state conflict", err)
			}
			after := manager.Snapshot()
			if after.Revision != before.Revision || after.ContentHash != before.ContentHash || !bytes.Equal(after.Raw, before.Raw) {
				t.Fatalf("preview conflict changed manager snapshot: before=%#v after=%#v", before, after)
			}
			test.assertExternal(t, paths, external)
		})
	}
}

func TestManagerCommitRereadsFileStateImmediatelyBeforeWrite(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = Provider{
		Transport: TransportOpenAICompatible,
		Endpoint:  "http://127.0.0.1:1234/v1",
	}
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	externalRaw, err := marshalStarter(document, "external edit during commit validation")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	manager.configWriteHook = func() error {
		close(started)
		<-release
		return nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.Update(context.Background(), before.Revision, []Operation{SetActiveModel("local/two")})
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for candidate construction")
	}
	if err := atomicWriteFile(paths.GlobalConfig, externalRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("update error = %v, want final file-state conflict", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for conflicted update")
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, externalRaw) {
		t.Fatalf("external bytes were overwritten:\nwant=%s\ngot=%s", externalRaw, written)
	}
	if after := manager.Snapshot(); after.Revision != before.Revision || after.ActiveModelID != before.ActiveModelID {
		t.Fatalf("manager snapshot changed after final conflict: before=%#v after=%#v", before, after)
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

func TestUpsertProviderPreservesExplicitEmptyDiscoveryPathRoundTrip(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	provider := Provider{Preset: "ollama", Endpoint: "http://127.0.0.1:11434/v1"}
	document.Providers["ollama"] = provider
	document.Models["ollama/manual"] = Model{Provider: "ollama", Name: "manual"}
	document.ActiveModel = "ollama/manual"
	writeManagerDocument(t, paths, document)
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	provider.Discovery = &DiscoveryConfig{Enabled: boolPointer(true), PathSet: true}
	explicitEmpty, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("ollama", provider)})
	if err != nil {
		t.Fatal(err)
	}
	configured := explicitEmpty.Document.Providers["ollama"].Discovery
	if configured == nil || !configured.PathSet || configured.Path != "" {
		t.Fatalf("explicit empty path presence was lost: %#v", configured)
	}
	resolved := mergePreset("ollama", explicitEmpty.Document.Providers["ollama"])
	if resolved.Discovery == nil || resolved.Discovery.Path != "" || !resolved.Discovery.PathSet {
		t.Fatalf("explicit empty path did not override preset: %#v", resolved.Discovery)
	}
	if got, err := discoveryURL(resolved.Endpoint, resolved.Discovery.Path); err != nil || got != resolved.Endpoint {
		t.Fatalf("explicit empty path URL = %q, err=%v, want endpoint %q", got, err, resolved.Endpoint)
	}
	written, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"path": ""`) {
		t.Fatalf("explicit empty path was omitted from JSONC:\n%s", written)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	roundTripped := manager.Snapshot().Document.Providers["ollama"].Discovery
	if roundTripped == nil || !roundTripped.PathSet || roundTripped.Path != "" {
		t.Fatalf("explicit empty path was lost after reload: %#v", roundTripped)
	}

	provider.Discovery = &DiscoveryConfig{Enabled: boolPointer(true)}
	omitted, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("ollama", provider)})
	if err != nil {
		t.Fatal(err)
	}
	configured = omitted.Document.Providers["ollama"].Discovery
	if configured == nil || configured.PathSet {
		t.Fatalf("omitted path acquired presence: %#v", configured)
	}
	resolved = mergePreset("ollama", omitted.Document.Providers["ollama"])
	if resolved.Discovery == nil || resolved.Discovery.Path != "/api/tags" || !resolved.Discovery.PathSet {
		t.Fatalf("omitted path did not inherit preset: %#v", resolved.Discovery)
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

func TestConcurrentManagersCASAllowsExactlyOneCommit(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"}
	document.Models["local/one"] = Model{Provider: "local", Name: "one"}
	document.Models["local/two"] = Model{Provider: "local", Name: "two"}
	document.Models["local/three"] = Model{Provider: "local", Name: "three"}
	document.ActiveModel = "local/one"
	writeManagerDocument(t, paths, document)

	first := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	second := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	firstBase := first.Snapshot()
	secondBase := second.Snapshot()
	if firstBase.Revision != secondBase.Revision || firstBase.ContentHash != secondBase.ContentHash {
		t.Fatalf("manager baselines differ: first=%d/%s second=%d/%s", firstBase.Revision, firstBase.ContentHash, secondBase.Revision, secondBase.ContentHash)
	}

	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	hook := func() error {
		ready <- struct{}{}
		<-release
		return nil
	}
	first.configWriteHook = hook
	second.configWriteHook = hook

	type updateResult struct {
		manager *Manager
		wanted  string
		value   Snapshot
		err     error
	}
	results := make(chan updateResult, 2)
	for manager, wanted := range map[*Manager]string{first: "local/two", second: "local/three"} {
		go func(manager *Manager, wanted string) {
			value, err := manager.Update(context.Background(), firstBase.Revision, []Operation{SetActiveModel(wanted)})
			results <- updateResult{manager: manager, wanted: wanted, value: value, err: err}
		}(manager, wanted)
	}
	for range 2 {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for both managers to prepare CAS temporary files")
		}
	}
	close(release)

	var winner updateResult
	conflicts := 0
	for range 2 {
		select {
		case result := <-results:
			switch {
			case result.err == nil:
				if winner.manager != nil {
					t.Fatalf("both manager commits succeeded: first winner=%q second winner=%q", winner.wanted, result.wanted)
				}
				winner = result
			case errors.Is(result.err, ErrRevisionConflict):
				conflicts++
				loserSnapshot := result.manager.Snapshot()
				if loserSnapshot.Revision != firstBase.Revision || loserSnapshot.ActiveModelID != "local/one" {
					t.Fatalf("losing manager published candidate: revision=%d active=%q", loserSnapshot.Revision, loserSnapshot.ActiveModelID)
				}
			default:
				t.Fatalf("manager update failed unexpectedly: %v", result.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for concurrent manager commits")
		}
	}
	if winner.manager == nil || conflicts != 1 {
		t.Fatalf("winner=%v conflicts=%d, want one success and one conflict", winner.manager != nil, conflicts)
	}
	if winner.value.ActiveModelID != winner.wanted || winner.manager.Snapshot().ActiveModelID != winner.wanted {
		t.Fatalf("winning manager did not publish winner %q: result=%q snapshot=%q", winner.wanted, winner.value.ActiveModelID, winner.manager.Snapshot().ActiveModelID)
	}

	raw, err := os.ReadFile(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	written, _, err := parseAndValidateGlobal(raw, paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if written.ActiveModel != winner.wanted {
		t.Fatalf("disk active model = %q, want winning commit %q", written.ActiveModel, winner.wanted)
	}
	info, err := os.Stat(paths.GlobalConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("CAS config mode = %v, want 0600", info.Mode().Perm())
	}
	lockInfo, err := os.Stat(paths.GlobalConfig + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("config lock mode = %v, want 0600", lockInfo.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(paths.Home, ".paw-config-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary config files survived concurrent CAS commits: %#v", matches)
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

func TestExplicitReloadReResolvesCredentialFromEnvironmentWhenFileHashIsUnchanged(t *testing.T) {
	clearDetectionEnv(t)
	t.Setenv("PAW_TEST_PRIVATE", "old-secret")
	paths := isolatedPaths(t, false)
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	document := emptyDocument()
	document.Providers["private"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1", Auth: Auth{Env: []string{"PAW_TEST_PRIVATE"}}}
	document.Models["private/model"] = Model{Provider: "private", Name: "model"}
	document.ActiveModel = "private/model"
	raw, _ := marshalStarter(document, "credential reload")
	if err := os.WriteFile(paths.GlobalConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	before := manager.Snapshot()
	if err := os.Setenv("PAW_TEST_PRIVATE", "new-secret"); err != nil {
		t.Fatal(err)
	}
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
