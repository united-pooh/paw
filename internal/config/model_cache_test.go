package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestModelDiscoveryCacheRoundTripAndFingerprintIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-discovery-cache.json")
	credential := "credential-do-not-cache"
	provider := Provider{
		Endpoint: "https://EXAMPLE.com:443/v1/?token=endpoint-secret#fragment",
		APIPath:  "/responses?api_key=api-path-secret",
		Auth:     Auth{Credential: credential, Env: []string{"SECRET_API_KEY"}},
		Headers: map[string]string{
			"Authorization": "Bearer header-secret",
			"Cookie":        "session=cookie-secret",
		},
		Body: map[string]any{"secret": "body-secret"},
		Discovery: &DiscoveryConfig{
			Enabled:        boolPointer(true),
			Path:           "models",
			Format:         DiscoveryFormatOpenAIList,
			TimeoutSeconds: 7,
			Include:        []string{"chat-*"},
			Exclude:        []string{"*-private"},
		},
	}
	fingerprint := discoveryEndpointFingerprint(provider)
	if !validDiscoveryCacheFingerprint(fingerprint) {
		t.Fatalf("fingerprint=%q", fingerprint)
	}
	cache := discoveryCacheFile{Version: discoveryCacheVersion, Providers: map[string]discoveryCacheEntry{
		"local": {
			EndpointFingerprint: fingerprint,
			Format:              DiscoveryFormatOpenAIList,
			DiscoveredAt:        time.Date(2026, time.August, 11, 8, 9, 10, 0, time.FixedZone("test", 8*60*60)),
			Models:              []string{"z-model", "a-model", "z-model", " a-model "},
		},
		"empty": {
			EndpointFingerprint: fingerprint,
			Format:              DiscoveryFormatOpenAIList,
			DiscoveredAt:        time.Unix(20, 0).UTC(),
			Models:              []string{},
		},
	}}
	if err := writeDiscoveryCache(path, cache); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cache.Providers["local"].Models, []string{"z-model", "a-model", "z-model", " a-model "}) {
		t.Fatalf("write mutated caller cache: %#v", cache.Providers["local"].Models)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("info=%v err=%v", info, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	serialized := strings.ToLower(string(raw))
	for _, forbidden := range []string{
		credential,
		"authorization",
		"headers",
		"cookie",
		"header-secret",
		"cookie-secret",
		"endpoint-secret",
		"api-path-secret",
		"body-secret",
		"secret_api_key",
	} {
		if strings.Contains(serialized, strings.ToLower(forbidden)) {
			t.Fatalf("cache contains forbidden credential or request field %q: %s", forbidden, raw)
		}
	}

	loaded, err := readDiscoveryCache(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := matchingCacheEntry(loaded, "local", fingerprint, DiscoveryFormatOpenAIList)
	if !ok {
		t.Fatal("matching cache entry was not found")
	}
	if want := []string{"a-model", "z-model"}; !reflect.DeepEqual(entry.Models, want) {
		t.Fatalf("models=%v want=%v", entry.Models, want)
	}
	if entry.DiscoveredAt.Location() != time.UTC {
		t.Fatalf("timestamp location=%v want UTC", entry.DiscoveredAt.Location())
	}
	empty, ok := matchingCacheEntry(loaded, "empty", fingerprint, DiscoveryFormatOpenAIList)
	if !ok || empty.Models == nil || len(empty.Models) != 0 {
		t.Fatalf("empty models=%#v ok=%v", empty.Models, ok)
	}
	if _, ok := matchingCacheEntry(loaded, "local", testDiscoveryFingerprint("b"), DiscoveryFormatOpenAIList); ok {
		t.Fatal("mismatched fingerprint was reused")
	}
	if _, ok := matchingCacheEntry(loaded, "local", fingerprint, DiscoveryFormatOllamaTags); ok {
		t.Fatal("mismatched format was reused")
	}

	entry.Models[0] = "mutated"
	entryAgain, ok := matchingCacheEntry(loaded, "local", fingerprint, DiscoveryFormatOpenAIList)
	if !ok || entryAgain.Models[0] != "a-model" {
		t.Fatalf("matching entry aliases cache storage: %#v ok=%v", entryAgain, ok)
	}
}

func TestModelDiscoveryEndpointFingerprintHashesResolvedURL(t *testing.T) {
	base := Provider{
		Endpoint: "https://example.com/v1",
		Discovery: &DiscoveryConfig{
			Path:   "models",
			Format: DiscoveryFormatOpenAIList,
		},
	}
	baseFingerprint := discoveryEndpointFingerprint(base)
	if !validDiscoveryCacheFingerprint(baseFingerprint) {
		t.Fatalf("fingerprint=%q", baseFingerprint)
	}

	equivalent := base
	equivalent.Endpoint = "HTTPS://EXAMPLE.COM:443?token=secret#fragment"
	equivalent.Discovery = &DiscoveryConfig{
		Enabled:        boolPointer(false),
		Path:           "v1/models",
		Format:         DiscoveryFormatOpenAIList,
		TimeoutSeconds: 10,
		Include:        []string{"one-*"},
		Exclude:        []string{"two-*"},
	}
	equivalent.APIPath = "/responses"
	equivalent.Auth = Auth{Credential: "credential-secret", Env: []string{"API_KEY"}}
	equivalent.Headers = map[string]string{"Authorization": "Bearer secret", "Cookie": "session=secret"}
	equivalent.Body = map[string]any{"api_key": "body-secret"}
	equivalent.TimeoutSeconds = 99
	equivalent.Retries = intPointer(5)
	equivalent.Stream = boolPointer(false)
	if got := discoveryEndpointFingerprint(equivalent); got != baseFingerprint {
		t.Fatalf("equivalent resolved URL changed fingerprint: got=%q want=%q", got, baseFingerprint)
	}

	changedFormat := base
	changedFormat.Discovery = &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOllamaTags}
	if got := discoveryEndpointFingerprint(changedFormat); got == baseFingerprint {
		t.Fatalf("format did not change fingerprint: %q", got)
	}
}

func TestModelDiscoveryEndpointFingerprintPreservesRequestPathSemantics(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
	}{
		{name: "trailing slash", left: "models", right: "models/"},
		{name: "repeated slash", left: "models/list", right: "models//list"},
		{name: "dot segment", left: "models/list", right: "models/./list"},
		{name: "escaped path spelling", left: "models/list", right: "models/%6cist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := Provider{Endpoint: "https://example.com/v1", Discovery: &DiscoveryConfig{Path: tt.left, Format: DiscoveryFormatOpenAIList}}
			left := discoveryEndpointFingerprint(provider)
			provider.Discovery = &DiscoveryConfig{Path: tt.right, Format: DiscoveryFormatOpenAIList}
			right := discoveryEndpointFingerprint(provider)
			if !validDiscoveryCacheFingerprint(left) || !validDiscoveryCacheFingerprint(right) {
				t.Fatalf("left=%q right=%q", left, right)
			}
			if left == right {
				t.Fatalf("distinct request paths collided: %q and %q => %q", tt.left, tt.right, left)
			}
		})
	}
}

func TestModelDiscoveryEndpointFingerprintRejectsInvalidDiscoveryURLs(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
	}{
		{name: "missing discovery config", provider: Provider{Endpoint: "https://example.com/v1"}},
		{name: "invalid endpoint", provider: Provider{Endpoint: "://bad", Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList}}},
		{name: "endpoint userinfo", provider: Provider{Endpoint: "https://user:password@example.com/v1", Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList}}},
		{name: "invalid endpoint port", provider: Provider{Endpoint: "https://example.com:bad/v1", Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList}}},
		{name: "invalid discovery path", provider: Provider{Endpoint: "https://example.com/v1", Discovery: &DiscoveryConfig{Path: "models/../secret", Format: DiscoveryFormatOpenAIList}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := discoveryEndpointFingerprint(tt.provider); got != "" {
				t.Fatalf("invalid discovery URL produced reusable fingerprint %q", got)
			}
		})
	}
}

func TestModelDiscoveryCacheMissingAndCorruptBehavior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	cache, err := readDiscoveryCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if cache.Version != discoveryCacheVersion || cache.Providers == nil || len(cache.Providers) != 0 {
		t.Fatalf("missing cache=%#v", cache)
	}

	if err := os.WriteFile(path, []byte(`{"version":1,"providers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDiscoveryCache(path); err == nil {
		t.Fatal("corrupt cache did not return an error")
	}
}

func TestModelDiscoveryCacheRejectsOversizedAndSparseFilesBeforeDecode(t *testing.T) {
	if maxDiscoveryCacheBytes != 4*int64(maxDiscoveryBodyBytes) {
		t.Fatalf("max cache bytes=%d want four times discovery body limit", maxDiscoveryCacheBytes)
	}
	tests := []struct {
		name  string
		write func(string) error
	}{
		{
			name: "oversized dense file",
			write: func(path string) error {
				return os.WriteFile(path, []byte(strings.Repeat("x", int(maxDiscoveryCacheBytes)+1)), 0o600)
			},
		},
		{
			name: "oversized sparse file",
			write: func(path string) error {
				file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				if err := file.Truncate(maxDiscoveryCacheBytes + 1); err != nil {
					_ = file.Close()
					return err
				}
				return file.Close()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.json")
			if err := tt.write(path); err != nil {
				t.Fatal(err)
			}
			_, err := readDiscoveryCache(path)
			if err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("error=%v, want size-limit diagnostic", err)
			}
		})
	}
}

func TestModelDiscoveryCacheStrictlyValidatesFileAndEntries(t *testing.T) {
	fingerprint := testDiscoveryFingerprint("a")
	validEntry := fmt.Sprintf(`{"endpointFingerprint":%q,"format":%q,"discoveredAt":"2026-08-11T10:00:00Z","models":[]}`, fingerprint, DiscoveryFormatOpenAIList)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "null root", raw: `null`},
		{name: "missing version", raw: `{"providers":{}}`},
		{name: "unsupported version", raw: `{"version":2,"providers":{}}`},
		{name: "missing providers", raw: `{"version":1}`},
		{name: "null providers", raw: `{"version":1,"providers":null}`},
		{name: "null entry", raw: `{"version":1,"providers":{"local":null}}`},
		{name: "missing fingerprint", raw: `{"version":1,"providers":{"local":{"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`},
		{name: "null fingerprint", raw: `{"version":1,"providers":{"local":{"endpointFingerprint":null,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`},
		{name: "bad fingerprint prefix", raw: `{"version":1,"providers":{"local":{"endpointFingerprint":"md5:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`},
		{name: "short fingerprint", raw: `{"version":1,"providers":{"local":{"endpointFingerprint":"sha256:aa","format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`},
		{name: "uppercase fingerprint", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`, testDiscoveryFingerprint("A"))},
		{name: "missing format", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`, fingerprint)},
		{name: "null format", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":null,"discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`, fingerprint)},
		{name: "unsupported format", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"other","discoveredAt":"2026-08-11T10:00:00Z","models":[]}}}`, fingerprint)},
		{name: "missing timestamp", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","models":[]}}}`, fingerprint)},
		{name: "null timestamp", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":null,"models":[]}}}`, fingerprint)},
		{name: "zero timestamp", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"0001-01-01T00:00:00Z","models":[]}}}`, fingerprint)},
		{name: "invalid timestamp", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"not-a-time","models":[]}}}`, fingerprint)},
		{name: "missing models", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z"}}}`, fingerprint)},
		{name: "null models", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":null}}}`, fingerprint)},
		{name: "empty model", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[""]}}}`, fingerprint)},
		{name: "control model", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":["bad\u001bname"]}}}`, fingerprint)},
		{name: "too long model", raw: fmt.Sprintf(`{"version":1,"providers":{"local":{"endpointFingerprint":%q,"format":"openai-list","discoveredAt":"2026-08-11T10:00:00Z","models":[%q]}}}`, fingerprint, strings.Repeat("m", 513))},
		{name: "unknown root field", raw: fmt.Sprintf(`{"version":1,"providers":{"local":%s},"extra":true}`, validEntry)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if cache, err := readDiscoveryCache(path); err == nil {
				t.Fatalf("malformed cache was accepted: %#v", cache)
			}
		})
	}

	models := make([]string, maxDiscoveryCacheModels+1)
	for index := range models {
		models[index] = fmt.Sprintf("model-%d", index)
	}
	raw, err := json.Marshal(discoveryCacheFile{Version: discoveryCacheVersion, Providers: map[string]discoveryCacheEntry{
		"local": {
			EndpointFingerprint: fingerprint,
			Format:              DiscoveryFormatOpenAIList,
			DiscoveredAt:        time.Unix(10, 0).UTC(),
			Models:              models,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) > maxDiscoveryCacheBytes {
		t.Fatalf("model-count fixture exceeds byte limit: %d", len(raw))
	}
	path := filepath.Join(t.TempDir(), "too-many-models.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if cache, err := readDiscoveryCache(path); err == nil {
		t.Fatalf("over-count cache was accepted: providers=%d", len(cache.Providers))
	}
}

func TestWriteDiscoveryCacheRejectsMalformedCache(t *testing.T) {
	validEntry := discoveryCacheEntry{
		EndpointFingerprint: testDiscoveryFingerprint("a"),
		Format:              DiscoveryFormatOpenAIList,
		DiscoveredAt:        time.Unix(10, 0).UTC(),
		Models:              []string{"model"},
	}
	tests := []struct {
		name  string
		cache discoveryCacheFile
	}{
		{name: "unsupported version", cache: discoveryCacheFile{Version: 2, Providers: map[string]discoveryCacheEntry{}}},
		{name: "nil providers", cache: discoveryCacheFile{Version: discoveryCacheVersion}},
		{name: "malformed entry", cache: discoveryCacheFile{Version: discoveryCacheVersion, Providers: map[string]discoveryCacheEntry{"local": {EndpointFingerprint: validEntry.EndpointFingerprint, Format: validEntry.Format, DiscoveredAt: validEntry.DiscoveredAt}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.json")
			if err := writeDiscoveryCache(path, tt.cache); err == nil {
				t.Fatal("malformed cache was written")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid write created cache file: %v", err)
			}
		})
	}
}

func TestModelDiscoveryCacheMatchingRejectsMalformedEntries(t *testing.T) {
	fingerprint := testDiscoveryFingerprint("a")
	valid := discoveryCacheEntry{
		EndpointFingerprint: fingerprint,
		Format:              DiscoveryFormatOpenAIList,
		DiscoveredAt:        time.Unix(10, 0).UTC(),
		Models:              []string{"b", "a", "b"},
	}
	cache := discoveryCacheFile{Version: discoveryCacheVersion, Providers: map[string]discoveryCacheEntry{"local": valid}}
	entry, ok := matchingCacheEntry(cache, "local", fingerprint, DiscoveryFormatOpenAIList)
	if !ok || !reflect.DeepEqual(entry.Models, []string{"a", "b"}) {
		t.Fatalf("valid entry=%#v ok=%v", entry, ok)
	}
	if _, ok := matchingCacheEntry(cache, "unknown", fingerprint, DiscoveryFormatOpenAIList); ok {
		t.Fatal("unknown provider matched")
	}

	tests := []struct {
		name   string
		mutate func(*discoveryCacheFile, *discoveryCacheEntry)
	}{
		{name: "unsupported version", mutate: func(cache *discoveryCacheFile, _ *discoveryCacheEntry) { cache.Version = 2 }},
		{name: "nil providers", mutate: func(cache *discoveryCacheFile, _ *discoveryCacheEntry) { cache.Providers = nil }},
		{name: "bad fingerprint", mutate: func(_ *discoveryCacheFile, entry *discoveryCacheEntry) { entry.EndpointFingerprint = "sha256:bad" }},
		{name: "bad format", mutate: func(_ *discoveryCacheFile, entry *discoveryCacheEntry) { entry.Format = "other" }},
		{name: "zero timestamp", mutate: func(_ *discoveryCacheFile, entry *discoveryCacheEntry) { entry.DiscoveredAt = time.Time{} }},
		{name: "nil models", mutate: func(_ *discoveryCacheFile, entry *discoveryCacheEntry) { entry.Models = nil }},
		{name: "unsafe model", mutate: func(_ *discoveryCacheFile, entry *discoveryCacheEntry) { entry.Models = []string{"bad\nmodel"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidateCache := discoveryCacheFile{Version: cache.Version, Providers: map[string]discoveryCacheEntry{"local": valid}}
			candidateEntry := candidateCache.Providers["local"]
			tt.mutate(&candidateCache, &candidateEntry)
			if candidateCache.Providers != nil {
				candidateCache.Providers["local"] = candidateEntry
			}
			if entry, ok := matchingCacheEntry(candidateCache, "local", candidateEntry.EndpointFingerprint, candidateEntry.Format); ok {
				t.Fatalf("malformed entry matched: %#v", entry)
			}
		})
	}
}

func testDiscoveryFingerprint(hexCharacter string) string {
	return "sha256:" + strings.Repeat(hexCharacter, 64)
}
