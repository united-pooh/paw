package config

import (
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
		Endpoint: " https://cache-user:" + credential + "@EXAMPLE.com:443/v1/?token=endpoint-secret#fragment ",
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
	cache := discoveryCacheFile{Version: 1, Providers: map[string]discoveryCacheEntry{
		"local": {
			EndpointFingerprint: fingerprint,
			Format:              DiscoveryFormatOpenAIList,
			DiscoveredAt:        time.Unix(10, 0).UTC(),
			Models:              []string{"z-model", "a-model", "m-model"},
		},
	}}
	if err := writeDiscoveryCache(path, cache); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cache.Providers["local"].Models, []string{"z-model", "a-model", "m-model"}) {
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
	if want := []string{"a-model", "m-model", "z-model"}; !reflect.DeepEqual(entry.Models, want) {
		t.Fatalf("models=%v want=%v", entry.Models, want)
	}
	if _, ok := matchingCacheEntry(loaded, "local", "sha256:two", DiscoveryFormatOpenAIList); ok {
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

func TestModelDiscoveryEndpointFingerprintUsesOnlyNormalizedNonSensitiveFields(t *testing.T) {
	base := Provider{
		Endpoint: "https://example.com/v1",
		Discovery: &DiscoveryConfig{
			Path:   "models",
			Format: DiscoveryFormatOpenAIList,
		},
	}
	baseFingerprint := discoveryEndpointFingerprint(base)
	if !strings.HasPrefix(baseFingerprint, "sha256:") || len(baseFingerprint) != len("sha256:")+64 {
		t.Fatalf("fingerprint=%q", baseFingerprint)
	}

	nonSensitiveEquivalent := base
	nonSensitiveEquivalent.Endpoint = " HTTPS://user:password@EXAMPLE.COM:443/v1/?token=secret#fragment "
	nonSensitiveEquivalent.APIPath = "/responses"
	nonSensitiveEquivalent.Auth = Auth{Credential: "credential-secret", Env: []string{"API_KEY"}}
	nonSensitiveEquivalent.Headers = map[string]string{"Authorization": "Bearer secret", "Cookie": "session=secret"}
	nonSensitiveEquivalent.Body = map[string]any{"api_key": "body-secret"}
	nonSensitiveEquivalent.TimeoutSeconds = 99
	nonSensitiveEquivalent.Retries = intPointer(5)
	nonSensitiveEquivalent.Stream = boolPointer(false)
	nonSensitiveEquivalent.Discovery = &DiscoveryConfig{
		Enabled:        boolPointer(false),
		Path:           "models",
		Format:         DiscoveryFormatOpenAIList,
		TimeoutSeconds: 10,
		Include:        []string{"one-*"},
		Exclude:        []string{"two-*"},
	}
	if got := discoveryEndpointFingerprint(nonSensitiveEquivalent); got != baseFingerprint {
		t.Fatalf("non-sensitive fields changed fingerprint: got=%q want=%q", got, baseFingerprint)
	}

	cases := []struct {
		name     string
		provider Provider
	}{
		{name: "origin", provider: Provider{Endpoint: "https://other.example.com/v1", Discovery: base.Discovery}},
		{name: "endpoint path", provider: Provider{Endpoint: "https://example.com/v2", Discovery: base.Discovery}},
		{name: "discovery path", provider: Provider{Endpoint: base.Endpoint, Discovery: &DiscoveryConfig{Path: "other-models", Format: DiscoveryFormatOpenAIList}}},
		{name: "format", provider: Provider{Endpoint: base.Endpoint, Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOllamaTags}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := discoveryEndpointFingerprint(test.provider); got == baseFingerprint {
				t.Fatalf("fingerprint did not change for %s: %q", test.name, got)
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
	if cache.Version != 1 || cache.Providers == nil || len(cache.Providers) != 0 {
		t.Fatalf("missing cache=%#v", cache)
	}

	if err := os.WriteFile(path, []byte(`{"version":1,"providers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDiscoveryCache(path); err == nil {
		t.Fatal("corrupt cache did not return an error")
	}
}

func TestModelDiscoveryCacheMatchingRejectsUnsupportedVersionAndUnknownProvider(t *testing.T) {
	cache := discoveryCacheFile{Version: 1, Providers: map[string]discoveryCacheEntry{
		"local": {
			EndpointFingerprint: "sha256:one",
			Format:              DiscoveryFormatOpenAIList,
			Models:              []string{"b", "a"},
		},
	}}
	if _, ok := matchingCacheEntry(cache, "unknown", "sha256:one", DiscoveryFormatOpenAIList); ok {
		t.Fatal("unknown provider matched")
	}
	cache.Version = 2
	if _, ok := matchingCacheEntry(cache, "local", "sha256:one", DiscoveryFormatOpenAIList); ok {
		t.Fatal("unsupported cache version matched")
	}
}
