package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultDiscoveryTimeout = 3 * time.Second
	maxDiscoveryBodyBytes   = 2 << 20
)

// ModelDiscoverer retrieves the models exposed by one provider.
type ModelDiscoverer interface {
	Discover(context.Context, string, Provider, string) ([]DiscoveredModel, error)
}

// HTTPModelDiscoverer performs bounded, same-origin HTTP model discovery.
type HTTPModelDiscoverer struct {
	client       *http.Client
	maxBodyBytes int64
}

// DiscoveryError classifies a discovery failure without exposing request
// credentials, headers, URLs, or response bodies through Error.
type DiscoveryError struct {
	Kind       string
	StatusCode int
	Err        error
}

func (e *DiscoveryError) Error() string {
	if e == nil {
		return "model discovery failed: kind=unknown"
	}
	kind := safeDiscoveryErrorKind(e.Kind)
	summary := discoveryErrorSummary(kind)
	if e.StatusCode > 0 {
		return fmt.Sprintf("model discovery failed: kind=%s status=%d: %s", kind, e.StatusCode, summary)
	}
	return fmt.Sprintf("model discovery failed: kind=%s: %s", kind, summary)
}

func (e *DiscoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewHTTPModelDiscoverer copies client before removing its cookie jar and
// installing a no-redirect policy, so callers can continue using their client
// unchanged. A nil client leaves the discoverer invalid rather than silently
// inheriting process-global HTTP behavior.
func NewHTTPModelDiscoverer(client *http.Client) *HTTPModelDiscoverer {
	discoverer := &HTTPModelDiscoverer{maxBodyBytes: maxDiscoveryBodyBytes}
	if client == nil {
		return discoverer
	}
	cloned := *client
	cloned.Jar = nil
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	discoverer.client = &cloned
	return discoverer
}

func (d *HTTPModelDiscoverer) Discover(ctx context.Context, providerID string, provider Provider, credential string) ([]DiscoveredModel, error) {
	if ctx == nil {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: errors.New("nil context")}
	}
	if d == nil || d.client == nil {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: errors.New("missing HTTP client")}
	}
	if provider.Discovery == nil {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: errors.New("missing discovery configuration")}
	}
	cfg := *provider.Discovery
	if cfg.TimeoutSeconds < 0 || cfg.TimeoutSeconds > 10 {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: errors.New("discovery timeout is outside the supported range")}
	}
	if cfg.Format != DiscoveryFormatOpenAIList && cfg.Format != DiscoveryFormatOllamaTags {
		return nil, &DiscoveryError{Kind: "unsupported_format", Err: errors.New("unsupported discovery response format")}
	}
	target, err := discoveryURL(provider.Endpoint, cfg.Path)
	if err != nil {
		return nil, &DiscoveryError{Kind: "invalid_url", Err: err}
	}

	timeout := defaultDiscoveryTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return nil, &DiscoveryError{Kind: "invalid_url", Err: err}
	}
	if err := copyDiscoveryHeaders(request.Header, provider.Headers); err != nil {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: err}
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}

	response, err := d.client.Do(request)
	if err != nil {
		return nil, classifyDiscoveryIOError(requestContext, "request_failed", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &DiscoveryError{
			Kind:       discoveryHTTPStatusKind(response.StatusCode),
			StatusCode: response.StatusCode,
		}
	}

	bodyLimit := int64(maxDiscoveryBodyBytes)
	if d.maxBodyBytes > 0 {
		bodyLimit = d.maxBodyBytes
	}
	limited := io.LimitReader(response.Body, bodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, classifyDiscoveryIOError(requestContext, "read_failed", err)
	}
	if int64(len(body)) > bodyLimit {
		return nil, &DiscoveryError{Kind: "response_too_large"}
	}

	names, err := parseDiscoveredModelNames(cfg.Format, body)
	if err != nil {
		return nil, &DiscoveryError{Kind: "invalid_response", Err: err}
	}
	models := make([]DiscoveredModel, len(names))
	for index, name := range names {
		models[index] = DiscoveredModel{ProviderID: providerID, Name: name}
	}
	return models, nil
}

func discoveryURL(endpoint, discoveryPath string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid provider endpoint: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.Opaque != "" {
		return "", errors.New("provider endpoint must be an absolute HTTP(S) URL")
	}
	if err := validateDiscoveryPath(discoveryPath); err != nil {
		return "", err
	}
	reference, err := url.Parse(discoveryPath)
	if err != nil {
		return "", fmt.Errorf("invalid discovery path: %w", err)
	}

	result := *base
	result.RawQuery = ""
	result.ForceQuery = false
	result.Fragment = ""
	result.RawFragment = ""

	var resolvedPath, resolvedEscapedPath string
	switch {
	case strings.HasPrefix(discoveryPath, "/"):
		resolvedPath = reference.Path
		resolvedEscapedPath = reference.EscapedPath()
	case discoveryPath == "":
		resolvedPath = base.Path
		resolvedEscapedPath = base.EscapedPath()
	default:
		resolvedPath = appendDiscoveryPath(base.Path, reference.Path)
		resolvedEscapedPath = appendDiscoveryPath(base.EscapedPath(), reference.EscapedPath())
	}
	result.Path = resolvedPath
	result.RawPath = ""
	if resolvedEscapedPath != (&url.URL{Path: resolvedPath}).EscapedPath() {
		result.RawPath = resolvedEscapedPath
	}
	return result.String(), nil
}

func appendDiscoveryPath(basePath, discoveryPath string) string {
	if discoveryPath == "" {
		return basePath
	}
	if basePath == "" {
		return "/" + discoveryPath
	}
	if strings.HasSuffix(basePath, "/") {
		return basePath + discoveryPath
	}
	return basePath + "/" + discoveryPath
}

func copyDiscoveryHeaders(destination http.Header, headers map[string]string) error {
	if err := validateProviderHeaders(headers); err != nil {
		return err
	}
	for _, name := range sortedProviderHeaderNames(headers) {
		if excludedDiscoveryHeader(name) {
			continue
		}
		destination.Set(name, headers[name])
	}
	return nil
}

func validateProviderHeaders(headers map[string]string) error {
	seen := make(map[string]struct{}, len(headers))
	for _, name := range sortedProviderHeaderNames(headers) {
		if name != strings.TrimSpace(name) {
			return errors.New("header name has leading or trailing whitespace")
		}
		if !validHTTPHeaderName(name) {
			return errors.New("header name contains an invalid token character")
		}
		canonicalName := strings.ToLower(name)
		if _, exists := seen[canonicalName]; exists {
			return fmt.Errorf("duplicate header name %q", canonicalName)
		}
		seen[canonicalName] = struct{}{}
		if !validHTTPHeaderValue(headers[name]) {
			return fmt.Errorf("header %q contains invalid value characters", canonicalName)
		}
	}
	return nil
}

func sortedProviderHeaderNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		if !isHTTPTokenByte(name[index]) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	case value >= '0' && value <= '9':
		return true
	}
	switch value {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func validHTTPHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == 0x7f || (character < 0x20 && character != '\t') {
			return false
		}
	}
	return true
}

func excludedDiscoveryHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-auth-token",
		"cookie", "set-cookie", "host", "content-length", "transfer-encoding":
		return true
	default:
		return false
	}
}

func parseDiscoveredModelNames(format string, body []byte) ([]string, error) {
	var fieldName string
	switch format {
	case DiscoveryFormatOpenAIList:
		fieldName = "data"
	case DiscoveryFormatOllamaTags:
		fieldName = "models"
	default:
		return nil, errors.New("unsupported discovery response format")
	}

	list, err := requiredDiscoveryList(body, fieldName)
	if err != nil {
		return nil, err
	}
	var names []string
	switch format {
	case DiscoveryFormatOpenAIList:
		var items []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(list, &items); err != nil {
			return nil, fmt.Errorf("discovery field %q must be an array: %w", fieldName, err)
		}
		for _, item := range items {
			names = append(names, item.ID)
		}
	case DiscoveryFormatOllamaTags:
		var items []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(list, &items); err != nil {
			return nil, fmt.Errorf("discovery field %q must be an array: %w", fieldName, err)
		}
		for _, item := range items {
			names = append(names, item.Name)
		}
	}

	// Preserve decoded names exactly. Manager owns the trust boundary and must
	// reject controls and overlong UTF-8 byte sequences before trim or dedup so
	// filtering remains observable in DiscoveryStatus.
	return names, nil
}

func requiredDiscoveryList(body []byte, fieldName string) (json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("discovery response must be a JSON object: %w", err)
	}
	if payload == nil {
		return nil, errors.New("discovery response must be a non-null JSON object")
	}
	list, exists := payload[fieldName]
	if !exists {
		return nil, fmt.Errorf("discovery response is missing required field %q", fieldName)
	}
	if bytes.Equal(bytes.TrimSpace(list), []byte("null")) {
		return nil, fmt.Errorf("discovery field %q must not be null", fieldName)
	}
	return list, nil
}

func classifyDiscoveryIOError(ctx context.Context, fallbackKind string, err error) *DiscoveryError {
	if contextErr := ctx.Err(); contextErr != nil {
		switch {
		case errors.Is(contextErr, context.Canceled):
			return &DiscoveryError{Kind: "canceled", Err: context.Canceled}
		case errors.Is(contextErr, context.DeadlineExceeded):
			return &DiscoveryError{Kind: "timeout", Err: context.DeadlineExceeded}
		}
	}
	if errors.Is(err, context.Canceled) {
		return &DiscoveryError{Kind: "canceled", Err: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &DiscoveryError{Kind: "timeout", Err: context.DeadlineExceeded}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &DiscoveryError{Kind: "timeout", Err: err}
	}
	return &DiscoveryError{Kind: fallbackKind, Err: err}
}

func discoveryHTTPStatusKind(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "auth_failed"
	case http.StatusTooManyRequests:
		return "rate_limited"
	}
	if statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest {
		return "redirect"
	}
	return "http_status"
}

func safeDiscoveryErrorKind(kind string) string {
	switch kind {
	case "invalid_config", "unsupported_format", "invalid_url", "request_failed", "timeout", "canceled",
		"read_failed", "response_too_large", "auth_failed", "rate_limited", "redirect",
		"http_status", "invalid_response":
		return kind
	default:
		return "unknown"
	}
}

func discoveryErrorSummary(kind string) string {
	switch kind {
	case "invalid_config":
		return "discovery configuration is invalid"
	case "unsupported_format":
		return "discovery response format is unsupported"
	case "invalid_url":
		return "discovery URL is invalid"
	case "request_failed":
		return "HTTP request failed"
	case "timeout":
		return "request timed out"
	case "canceled":
		return "request was canceled"
	case "read_failed":
		return "response could not be read"
	case "response_too_large":
		return "response exceeded the size limit"
	case "auth_failed":
		return "provider authentication failed"
	case "rate_limited":
		return "provider rate limited the request"
	case "redirect":
		return "provider returned a redirect"
	case "http_status":
		return "provider returned an unexpected HTTP status"
	case "invalid_response":
		return "provider returned an invalid response"
	default:
		return "discovery failed"
	}
}
