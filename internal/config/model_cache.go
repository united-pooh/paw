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
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	discoveryCacheVersion = 1

	// maxDiscoveryCacheBytes is four times the bounded discovery response size.
	// The extra space covers JSON field names, provider metadata, and escaping
	// while keeping corrupt or sparse cache files from causing unbounded reads.
	maxDiscoveryCacheBytes int64 = 4 * maxDiscoveryBodyBytes

	// maxDiscoveryCacheModels bounds per-entry validation and canonicalization
	// work independently of the file-size limit. It is intentionally generous
	// relative to the two-MiB discovery response budget.
	maxDiscoveryCacheModels = maxDiscoveryBodyBytes / 16
)

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
	if provider.Discovery == nil {
		return ""
	}
	resolved, err := discoveryURL(provider.Endpoint, provider.Discovery.Path)
	if err != nil {
		return ""
	}
	request, err := http.NewRequest(http.MethodGet, resolved, nil)
	if err != nil {
		return ""
	}
	normalizedURL, ok := normalizedDiscoveryFingerprintURL(request.URL)
	if !ok {
		return ""
	}
	fingerprintInput := struct {
		URL    string `json:"url"`
		Format string `json:"format"`
	}{
		URL:    normalizedURL,
		Format: strings.ToLower(strings.TrimSpace(provider.Discovery.Format)),
	}
	raw, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedDiscoveryFingerprintURL(parsed *url.URL) (string, bool) {
	if parsed == nil || parsed.User != nil || parsed.Opaque != "" {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if (scheme != "http" && scheme != "https") || hostname == "" {
		return "", false
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

	// EscapedPath returns RawPath when it is a valid spelling of Path. Do not
	// clean or decode it: repeated/trailing slashes, dot segments, and escape
	// spelling all affect the exact request target when redirects are disabled.
	return scheme + "://" + host + parsed.EscapedPath(), true
}

func readDiscoveryCache(path string) (discoveryCacheFile, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyDiscoveryCache(), nil
		}
		return discoveryCacheFile{}, fmt.Errorf("read model discovery cache %s: %w", path, err)
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxDiscoveryCacheBytes+1))
	if err != nil {
		return discoveryCacheFile{}, fmt.Errorf("read model discovery cache %s: %w", path, err)
	}
	if int64(len(raw)) > maxDiscoveryCacheBytes {
		return discoveryCacheFile{}, fmt.Errorf("read model discovery cache %s: cache exceeds %d-byte limit", path, maxDiscoveryCacheBytes)
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
	normalized, err := normalizeDiscoveryCache(*cache)
	if err != nil {
		return discoveryCacheFile{}, fmt.Errorf("validate model discovery cache %s: %w", path, err)
	}
	return normalized, nil
}

func writeDiscoveryCache(path string, cache discoveryCacheFile) error {
	normalized, err := normalizeDiscoveryCache(cache)
	if err != nil {
		return fmt.Errorf("validate model discovery cache: %w", err)
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model discovery cache: %w", err)
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > maxDiscoveryCacheBytes {
		return fmt.Errorf("encode model discovery cache: cache exceeds %d-byte limit", maxDiscoveryCacheBytes)
	}
	if err := atomicWriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write model discovery cache %s: %w", path, err)
	}
	return nil
}

func matchingCacheEntry(cache discoveryCacheFile, providerID, fingerprint, format string) (discoveryCacheEntry, bool) {
	if cache.Version != discoveryCacheVersion || cache.Providers == nil {
		return discoveryCacheEntry{}, false
	}
	entry, ok := cache.Providers[providerID]
	if !ok {
		return discoveryCacheEntry{}, false
	}
	normalized, err := normalizeDiscoveryCacheEntry(providerID, entry)
	if err != nil || normalized.EndpointFingerprint != fingerprint || normalized.Format != format {
		return discoveryCacheEntry{}, false
	}
	return normalized, true
}

func emptyDiscoveryCache() discoveryCacheFile {
	return discoveryCacheFile{
		Version:   discoveryCacheVersion,
		Providers: map[string]discoveryCacheEntry{},
	}
}

func normalizeDiscoveryCache(cache discoveryCacheFile) (discoveryCacheFile, error) {
	if cache.Version != discoveryCacheVersion {
		return discoveryCacheFile{}, fmt.Errorf("version must be %d", discoveryCacheVersion)
	}
	if cache.Providers == nil {
		return discoveryCacheFile{}, errors.New("providers must be a non-null object")
	}

	providerIDs := make([]string, 0, len(cache.Providers))
	for providerID := range cache.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	normalized := discoveryCacheFile{
		Version:   discoveryCacheVersion,
		Providers: make(map[string]discoveryCacheEntry, len(cache.Providers)),
	}
	for _, providerID := range providerIDs {
		entry, err := normalizeDiscoveryCacheEntry(providerID, cache.Providers[providerID])
		if err != nil {
			return discoveryCacheFile{}, err
		}
		normalized.Providers[providerID] = entry
	}
	return normalized, nil
}

func normalizeDiscoveryCacheEntry(providerID string, entry discoveryCacheEntry) (discoveryCacheEntry, error) {
	if !validDiscoveryCacheFingerprint(entry.EndpointFingerprint) {
		return discoveryCacheEntry{}, fmt.Errorf("providers.%s.endpointFingerprint must match sha256:<64 lowercase hex characters>", providerID)
	}
	if !validDiscoveryCacheFormat(entry.Format) {
		return discoveryCacheEntry{}, fmt.Errorf("providers.%s.format is unsupported", providerID)
	}
	if entry.DiscoveredAt.IsZero() {
		return discoveryCacheEntry{}, fmt.Errorf("providers.%s.discoveredAt must be non-zero", providerID)
	}
	if _, err := entry.DiscoveredAt.MarshalJSON(); err != nil {
		return discoveryCacheEntry{}, fmt.Errorf("providers.%s.discoveredAt is invalid: %w", providerID, err)
	}
	models, err := canonicalDiscoveryCacheModels(entry.Models)
	if err != nil {
		return discoveryCacheEntry{}, fmt.Errorf("providers.%s.models: %w", providerID, err)
	}
	entry.DiscoveredAt = entry.DiscoveredAt.UTC()
	entry.Models = models
	return entry, nil
}

func canonicalDiscoveryCacheModels(models []string) ([]string, error) {
	if models == nil {
		return nil, errors.New("must be a non-null array")
	}
	if len(models) > maxDiscoveryCacheModels {
		return nil, fmt.Errorf("contains %d models, limit is %d", len(models), maxDiscoveryCacheModels)
	}
	unique := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		if unsafeDiscoveredModelName(modelName) {
			return nil, errors.New("contains an unsafe model name")
		}
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return nil, errors.New("contains an empty model name")
		}
		unique[modelName] = struct{}{}
	}
	canonical := make([]string, 0, len(unique))
	for modelName := range unique {
		canonical = append(canonical, modelName)
	}
	sort.Strings(canonical)
	return canonical, nil
}

func validDiscoveryCacheFingerprint(fingerprint string) bool {
	const prefix = "sha256:"
	if len(fingerprint) != len(prefix)+sha256.Size*2 || !strings.HasPrefix(fingerprint, prefix) {
		return false
	}
	for index := len(prefix); index < len(fingerprint); index++ {
		current := fingerprint[index]
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

func validDiscoveryCacheFormat(format string) bool {
	return format == DiscoveryFormatOpenAIList || format == DiscoveryFormatOllamaTags
}
