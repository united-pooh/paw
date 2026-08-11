package config

import (
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

// NewHTTPModelDiscoverer copies client before installing a no-redirect policy,
// so callers can continue using their client unchanged. A nil client uses a
// copy of http.DefaultClient.
func NewHTTPModelDiscoverer(client *http.Client) *HTTPModelDiscoverer {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPModelDiscoverer{
		client:       &cloned,
		maxBodyBytes: maxDiscoveryBodyBytes,
	}
}

func (d *HTTPModelDiscoverer) Discover(ctx context.Context, providerID string, provider Provider, credential string) ([]DiscoveredModel, error) {
	if ctx == nil {
		return nil, &DiscoveryError{Kind: "invalid_config", Err: errors.New("nil context")}
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
	copyDiscoveryHeaders(request.Header, provider.Headers)
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}

	client := http.DefaultClient
	if d != nil && d.client != nil {
		client = d.client
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyDiscoveryIOError(requestContext, "request_failed", err)
	}
	defer response.Body.Close()

	bodyLimit := int64(maxDiscoveryBodyBytes)
	if d != nil && d.maxBodyBytes > 0 {
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
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &DiscoveryError{
			Kind:       discoveryHTTPStatusKind(response.StatusCode),
			StatusCode: response.StatusCode,
		}
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
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.Opaque != "" {
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

func copyDiscoveryHeaders(destination http.Header, headers map[string]string) {
	for name, value := range headers {
		if excludedDiscoveryHeader(name) {
			continue
		}
		destination.Set(name, value)
	}
}

func excludedDiscoveryHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-auth-token",
		"cookie", "set-cookie", "host", "content-length", "transfer-encoding":
		return true
	default:
		return false
	}
}

func parseDiscoveredModelNames(format string, body []byte) ([]string, error) {
	var names []string
	switch format {
	case DiscoveryFormatOpenAIList:
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		for _, item := range payload.Data {
			names = append(names, item.ID)
		}
	case DiscoveryFormatOllamaTags:
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		for _, item := range payload.Models {
			names = append(names, item.Name)
		}
	default:
		return nil, errors.New("unsupported discovery response format")
	}

	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			unique[name] = struct{}{}
		}
	}
	names = names[:0]
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
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
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
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
		"read_failed", "response_too_large", "unauthorized", "forbidden", "rate_limited", "redirect",
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
	case "unauthorized":
		return "provider rejected the credential"
	case "forbidden":
		return "provider denied the request"
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
