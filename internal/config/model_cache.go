package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	pathpkg "path"
	"sort"
	"strings"
	"time"
)

const discoveryCacheVersion = 1

type discoveryCacheFile struct {
	Version   int                            `json:"version"`
	Providers map[string]discoveryCacheEntry `json:"providers"`
}

type discoveryCacheEntry struct {
	EndpointFingerprint string    `json:"endpointFingerprint"`
	Format              string    `json:"format"`
	DiscoveredAt        time.Time `json:"discoveredAt"`
	Models              []string  `json:"models"`
}

func discoveryEndpointFingerprint(provider Provider) string {
	discoveryPath := ""
	format := ""
	if provider.Discovery != nil {
		discoveryPath = normalizeDiscoveryFingerprintPath(provider.Discovery.Path)
		format = strings.ToLower(strings.TrimSpace(provider.Discovery.Format))
	}
	fingerprintInput := struct {
		Endpoint      string `json:"endpoint"`
		DiscoveryPath string `json:"discoveryPath"`
		Format        string `json:"format"`
	}{
		Endpoint:      normalizeEndpointOriginPath(provider.Endpoint),
		DiscoveryPath: discoveryPath,
		Format:        format,
	}
	raw, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readDiscoveryCache(path string) (discoveryCacheFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyDiscoveryCache(), nil
		}
		return discoveryCacheFile{}, fmt.Errorf("read model discovery cache %s: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cache *discoveryCacheFile
	if err := decoder.Decode(&cache); err != nil {
		return discoveryCacheFile{}, fmt.Errorf("decode model discovery cache %s: %w", path, err)
	}
	if cache == nil {
		return discoveryCacheFile{}, fmt.Errorf("decode model discovery cache %s: cache must be a JSON object", path)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return discoveryCacheFile{}, fmt.Errorf("decode model discovery cache %s: %w", path, err)
	}
	cache.Providers = normalizedDiscoveryCacheProviders(cache.Providers)
	return *cache, nil
}

func writeDiscoveryCache(path string, cache discoveryCacheFile) error {
	cache.Providers = normalizedDiscoveryCacheProviders(cache.Providers)
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model discovery cache: %w", err)
	}
	raw = append(raw, '\n')
	if err := atomicWriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write model discovery cache %s: %w", path, err)
	}
	return nil
}

func matchingCacheEntry(cache discoveryCacheFile, providerID, fingerprint, format string) (discoveryCacheEntry, bool) {
	if cache.Version != discoveryCacheVersion {
		return discoveryCacheEntry{}, false
	}
	entry, ok := cache.Providers[providerID]
	if !ok || entry.EndpointFingerprint != fingerprint || entry.Format != format {
		return discoveryCacheEntry{}, false
	}
	entry.Models = sortedModelValues(entry.Models)
	return entry, true
}

func emptyDiscoveryCache() discoveryCacheFile {
	return discoveryCacheFile{
		Version:   discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{},
	}
}

func normalizedDiscoveryCacheProviders(providers map[string]discoveryCacheEntry) map[string]discoveryCacheEntry {
	if providers == nil {
		return map[string]discoveryCacheEntry{}
	}
	normalized := make(map[string]discoveryCacheEntry, len(providers))
	for providerID, entry := range providers {
		if !entry.DiscoveredAt.IsZero() {
			entry.DiscoveredAt = entry.DiscoveredAt.UTC()
		}
		entry.Models = sortedModelValues(entry.Models)
		normalized[providerID] = entry
	}
	return normalized
}

func sortedModelValues(models []string) []string {
	if models == nil {
		return nil
	}
	sorted := append([]string(nil), models...)
	sort.Strings(sorted)
	return sorted
}

func normalizeEndpointOriginPath(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "invalid-endpoint"
	}
	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if (scheme != "http" && scheme != "https") || hostname == "" {
		return "invalid-endpoint"
	}

	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}

	endpointPath := normalizeFingerprintPath(parsed.EscapedPath(), true)
	return scheme + "://" + host + endpointPath
}

func normalizeDiscoveryFingerprintPath(discoveryPath string) string {
	parsed, err := url.Parse(strings.TrimSpace(discoveryPath))
	if err != nil {
		return "invalid-discovery-path"
	}
	return normalizeFingerprintPath(parsed.EscapedPath(), false)
}

func normalizeFingerprintPath(value string, absoluteDefault bool) string {
	if value == "" {
		if absoluteDefault {
			return "/"
		}
		return ""
	}
	absolute := strings.HasPrefix(value, "/")
	cleaned := pathpkg.Clean(value)
	if cleaned == "." {
		if absoluteDefault || absolute {
			return "/"
		}
		return ""
	}
	if absolute && !strings.HasPrefix(cleaned, "/") {
		return "/" + cleaned
	}
	return cleaned
}
