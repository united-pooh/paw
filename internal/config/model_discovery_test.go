package config

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPModelDiscovererOpenAIList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-value" {
			t.Errorf("authorization=%q", got)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":" b "},{"id":"a"},{"id":"a"},{"id":""},{"id":"   "}]}`)
	}))
	defer server.Close()

	discoverer := NewHTTPModelDiscoverer(server.Client())
	got, err := discoverer.Discover(context.Background(), "local", Provider{
		Endpoint: server.URL + "/v1",
		Discovery: &DiscoveryConfig{
			Path:   "/v1/models",
			Format: DiscoveryFormatOpenAIList,
		},
	}, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	want := []DiscoveredModel{{ProviderID: "local", Name: "a"}, {ProviderID: "local", Name: "b"}}
	if !slices.Equal(want, got) {
		t.Fatalf("models=%#v", got)
	}
}

func TestHTTPModelDiscovererOllamaTagsUsesOriginRelativePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen:latest"},{"name":" llama3 "},{"name":"qwen:latest"}]}`)
	}))
	defer server.Close()

	got, err := NewHTTPModelDiscoverer(server.Client()).Discover(context.Background(), "ollama", Provider{
		Endpoint: server.URL + "/v1",
		Discovery: &DiscoveryConfig{
			Path:   "/api/tags",
			Format: DiscoveryFormatOllamaTags,
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []DiscoveredModel{{ProviderID: "ollama", Name: "llama3"}, {ProviderID: "ollama", Name: "qwen:latest"}}
	if !slices.Equal(want, got) {
		t.Fatalf("models=%#v", got)
	}
}

func TestHTTPModelDiscovererCopiesOnlyNonSensitiveHeadersAndKeepsErrorsSafe(t *testing.T) {
	const (
		credentialSecret = "credential-secret"
		headerSecret     = "header-secret"
		apiSecret        = "api-secret"
		cookieSecret     = "cookie-secret"
		responseSecret   = "response-secret"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Trace"); got != "trace-value" {
			t.Errorf("X-Trace=%q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credentialSecret {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("X-API-Key=%q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie=%q", got)
		}
		if r.Host == "evil.invalid" {
			t.Errorf("Host header overrode request origin: %q", r.Host)
		}
		_, _ = io.WriteString(w, credentialSecret+" "+headerSecret+" "+apiSecret+" "+cookieSecret+" "+responseSecret)
	}))
	defer server.Close()

	_, err := NewHTTPModelDiscoverer(server.Client()).Discover(context.Background(), "local", Provider{
		Endpoint: server.URL,
		Headers: map[string]string{
			"X-Trace":             "trace-value",
			"Authorization":       "Bearer " + headerSecret,
			"Proxy-Authorization": headerSecret,
			"X-API-Key":           apiSecret,
			"Cookie":              "session=" + cookieSecret,
			"Host":                "evil.invalid",
		},
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, credentialSecret)
	if err == nil {
		t.Fatal("expected invalid response error")
	}
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) || discoveryErr.Kind != "invalid_response" {
		t.Fatalf("error=%T %v", err, err)
	}
	for _, secret := range []string{credentialSecret, headerSecret, apiSecret, cookieSecret, responseSecret} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestHTTPModelDiscovererClassifiesUnauthorizedWithoutLeakingBody(t *testing.T) {
	const secret = "unauthorized-response-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, secret)
	}))
	defer server.Close()

	_, err := NewHTTPModelDiscoverer(server.Client()).Discover(context.Background(), "local", Provider{
		Endpoint:  server.URL,
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, "credential-secret")
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) {
		t.Fatalf("error=%T %v", err, err)
	}
	if discoveryErr.Kind != "unauthorized" || discoveryErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("discovery error=%#v", discoveryErr)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestDiscoveryErrorDoesNotRenderUnsafeKindOrCause(t *testing.T) {
	err := (&DiscoveryError{
		Kind:       "unsafe-secret-kind",
		StatusCode: http.StatusBadGateway,
		Err:        errors.New("unsafe-secret-cause"),
	}).Error()
	if strings.Contains(err, "unsafe-secret") {
		t.Fatalf("unsafe error=%q", err)
	}
	if !strings.Contains(err, "kind=unknown") || !strings.Contains(err, "status=502") {
		t.Fatalf("error lost safe classification: %q", err)
	}
}

func TestHTTPModelDiscovererDoesNotFollowRedirectsOrMutateClient(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			http.Redirect(w, r, "/redirected", http.StatusFound)
		case "/redirected":
			followed.Store(true)
			_, _ = io.WriteString(w, `{"data":[{"id":"should-not-be-seen"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var callerRedirectPolicyCalled atomic.Bool
	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		callerRedirectPolicyCalled.Store(true)
		return nil
	}
	discoverer := NewHTTPModelDiscoverer(client)

	_, err := discoverer.Discover(context.Background(), "local", Provider{
		Endpoint:  server.URL,
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, "")
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) {
		t.Fatalf("error=%T %v", err, err)
	}
	if discoveryErr.Kind != "redirect" || discoveryErr.StatusCode != http.StatusFound {
		t.Fatalf("discovery error=%#v", discoveryErr)
	}
	if followed.Load() {
		t.Fatal("redirect target was requested")
	}
	if callerRedirectPolicyCalled.Load() {
		t.Fatal("caller redirect policy was used by discoverer copy")
	}
	if client.CheckRedirect == nil {
		t.Fatal("caller client was mutated")
	}
	if err := client.CheckRedirect(nil, nil); err != nil || !callerRedirectPolicyCalled.Load() {
		t.Fatalf("caller redirect policy changed: called=%v err=%v", callerRedirectPolicyCalled.Load(), err)
	}
}

func TestHTTPModelDiscovererTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := NewHTTPModelDiscoverer(client).Discover(ctx, "local", Provider{
		Endpoint:  "http://example.invalid/v1",
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, "")
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) || discoveryErr.Kind != "timeout" {
		t.Fatalf("error=%T %v", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout does not unwrap to context deadline: %v", err)
	}
}

func TestHTTPModelDiscovererAppliesDefaultAndConfiguredTimeouts(t *testing.T) {
	tests := []struct {
		name           string
		timeoutSeconds int
		want           time.Duration
	}{
		{name: "default", want: defaultDiscoveryTimeout},
		{name: "configured", timeoutSeconds: 1, want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("request context has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining <= 0 || remaining > tt.want || remaining < tt.want-time.Second/2 {
					t.Fatalf("deadline remaining=%v want approximately %v", remaining, tt.want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
					Request:    r,
				}, nil
			})}

			_, err := NewHTTPModelDiscoverer(client).Discover(context.Background(), "local", Provider{
				Endpoint: "http://example.invalid/v1",
				Discovery: &DiscoveryConfig{
					Path:           "models",
					Format:         DiscoveryFormatOpenAIList,
					TimeoutSeconds: tt.timeoutSeconds,
				},
			}, "")
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHTTPModelDiscovererCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}

	_, err := NewHTTPModelDiscoverer(client).Discover(ctx, "local", Provider{
		Endpoint:  "http://example.invalid/v1",
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, "")
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) || discoveryErr.Kind != "canceled" {
		t.Fatalf("error=%T %v", err, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation does not unwrap to context cancellation: %v", err)
	}
}

func TestHTTPModelDiscovererRejectsResponseLargerThanTwoMiB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Repeat("x", maxDiscoveryBodyBytes+1))
	}))
	defer server.Close()

	_, err := NewHTTPModelDiscoverer(server.Client()).Discover(context.Background(), "local", Provider{
		Endpoint:  server.URL,
		Discovery: &DiscoveryConfig{Path: "models", Format: DiscoveryFormatOpenAIList},
	}, "")
	var discoveryErr *DiscoveryError
	if !errors.As(err, &discoveryErr) || discoveryErr.Kind != "response_too_large" {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestDiscoveryURL(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		path      string
		want      string
		wantError bool
	}{
		{name: "append endpoint path", endpoint: "https://example.com/v1", path: "models", want: "https://example.com/v1/models"},
		{name: "append after slash", endpoint: "https://example.com/v1/", path: "models", want: "https://example.com/v1/models"},
		{name: "replace endpoint path", endpoint: "https://example.com/v1", path: "/api/tags", want: "https://example.com/api/tags"},
		{name: "empty endpoint path", endpoint: "https://example.com", path: "models", want: "https://example.com/models"},
		{name: "encoded path stays encoded", endpoint: "https://example.com/v1", path: "model%20catalog", want: "https://example.com/v1/model%20catalog"},
		{name: "endpoint query and fragment are not inherited", endpoint: "https://example.com/v1?token=secret#fragment", path: "models", want: "https://example.com/v1/models"},
		{name: "absolute URL", endpoint: "https://example.com/v1", path: "https://evil.invalid/models", wantError: true},
		{name: "scheme", endpoint: "https://example.com/v1", path: "models:evil", wantError: true},
		{name: "network path", endpoint: "https://example.com/v1", path: "//evil.invalid/models", wantError: true},
		{name: "userinfo", endpoint: "https://example.com/v1", path: "//user:pass@evil.invalid/models", wantError: true},
		{name: "query", endpoint: "https://example.com/v1", path: "models?token=secret", wantError: true},
		{name: "empty query", endpoint: "https://example.com/v1", path: "models?", wantError: true},
		{name: "fragment", endpoint: "https://example.com/v1", path: "models#secret", wantError: true},
		{name: "empty fragment", endpoint: "https://example.com/v1", path: "models#", wantError: true},
		{name: "parent segment", endpoint: "https://example.com/v1", path: "models/../secrets", wantError: true},
		{name: "encoded parent segment", endpoint: "https://example.com/v1", path: "models/%2e%2e/secrets", wantError: true},
		{name: "invalid endpoint", endpoint: "://bad", path: "models", wantError: true},
		{name: "non HTTP endpoint", endpoint: "file:///tmp/v1", path: "models", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := discoveryURL(tt.endpoint, tt.path)
			if tt.wantError {
				if err == nil {
					t.Fatalf("URL=%q, expected error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("URL=%q want=%q", got, tt.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
