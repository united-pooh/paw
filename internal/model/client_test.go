package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"testing"
	"time"
)

func TestSetRequestHeadersOmitsAuthorizationWhenAPIKeyEmpty(t *testing.T) {
	t.Setenv("TEST_API_KEY", "")

	client := NewClient(Config{
		Provider:      "test-gateway",
		APIBaseURL:    "http://gateway.test/v1",
		APIPath:       "/chat/completions",
		APIKeyEnvName: "TEST_API_KEY",
		Model:         "test-model",
		Timeout:       time.Minute,
	})
	if err := client.ApplyModelConfig(Config{
		Provider:      "test-gateway",
		APIBaseURL:    "http://gateway.test/v1",
		APIPath:       "/chat/completions",
		APIKey:        "",
		APIKeyEnvName: "TEST_API_KEY",
		Model:         "test-model",
		Timeout:       time.Minute,
	}); err != nil {
		t.Fatalf("ApplyModelConfig() error = %v", err)
	}

	req := httptest.NewRequest("POST", "http://gateway.test/v1/chat/completions", nil)
	client.setRequestHeaders(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestRunMessageRetriesTransientHTTPFailures(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 3 {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "test-gateway",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "test-model",
		RetryCount: 2,
		Timeout:    time.Second,
	})
	got, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}})
	if err != nil {
		t.Fatalf("RunMessage() error = %v", err)
	}
	if got != "recovered" || requests != 3 {
		t.Fatalf("response=%q requests=%d, want recovered/3", got, requests)
	}
}

func TestRunMessageStopsAfterConfiguredRetries(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, fmt.Sprintf("failure %d", requests), http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "test-gateway",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "test-model",
		RetryCount: 1,
		Timeout:    time.Second,
	})
	if _, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}); err == nil {
		t.Fatal("RunMessage() error = nil, want final provider error")
	}
	if requests != 2 {
		t.Fatalf("requests=%d, want 2", requests)
	}
}
