package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"reflect"
	"strings"
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

func TestClientConfigIsolatedAcrossConstructionApplicationAndReads(t *testing.T) {
	extraBody := RequestBody{
		"metadata": map[string]any{"source": "caller"},
		"tags":     []any{"caller"},
	}
	modelExtraBody := map[string]RequestBody{
		"model-a": {"metadata": map[string]any{"model": "caller"}},
	}
	cfg := Config{
		ProfileID:      "gateway",
		Model:          "model-a",
		Models:         []string{"model-a"},
		ExtraBody:      extraBody,
		ModelExtraBody: modelExtraBody,
	}
	client := NewClient(cfg)

	extraBody["metadata"].(map[string]any)["source"] = "changed-after-construction"
	extraBody["tags"].([]any)[0] = "changed-after-construction"
	modelExtraBody["model-a"]["metadata"].(map[string]any)["model"] = "changed-after-construction"

	got := client.CurrentModelConfig()
	if got.ExtraBody["metadata"].(map[string]any)["source"] != "caller" {
		t.Fatalf("constructed config shared extraBody: %#v", got.ExtraBody)
	}
	if got.ModelExtraBody["model-a"]["metadata"].(map[string]any)["model"] != "caller" {
		t.Fatalf("constructed config shared modelExtraBody: %#v", got.ModelExtraBody)
	}
	got.ExtraBody["metadata"].(map[string]any)["source"] = "changed-through-read"
	got.ModelExtraBody["model-a"]["metadata"].(map[string]any)["model"] = "changed-through-read"
	readAgain := client.CurrentModelConfig()
	if readAgain.ExtraBody["metadata"].(map[string]any)["source"] != "caller" ||
		readAgain.ModelExtraBody["model-a"]["metadata"].(map[string]any)["model"] != "caller" {
		t.Fatalf("CurrentModelConfig() exposed mutable nested values: %#v", readAgain)
	}

	newExtraBody := RequestBody{"nested": map[string]any{"value": "applied"}}
	newModelExtraBody := map[string]RequestBody{"model-a": {"nested": map[string]any{"value": "model"}}}
	newCfg := Config{
		ProfileID:      "gateway",
		Model:          "model-a",
		Models:         []string{"model-a"},
		ExtraBody:      newExtraBody,
		ModelExtraBody: newModelExtraBody,
	}
	if err := client.ApplyModelConfig(newCfg); err != nil {
		t.Fatalf("ApplyModelConfig() error = %v", err)
	}
	newExtraBody["nested"].(map[string]any)["value"] = "changed-after-apply"
	newModelExtraBody["model-a"]["nested"].(map[string]any)["value"] = "changed-after-apply"
	applied := client.CurrentModelConfig()
	if applied.ExtraBody["nested"].(map[string]any)["value"] != "applied" ||
		applied.ModelExtraBody["model-a"]["nested"].(map[string]any)["value"] != "model" {
		t.Fatalf("ApplyModelConfig() retained caller-owned nested values: %#v", applied)
	}
}

func TestRunMessageKeepsBaseRequestShapeWithoutExtraConfiguration(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "test-gateway",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "model-a",
		Timeout:    time.Second,
	})
	if _, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("RunMessage() error = %v", err)
	}

	want := map[string]any{
		"model": "model-a",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"stream": false,
	}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("request body = %#v, want %#v", received, want)
	}
}

func TestRunMessageUsesSelectedProfileModelExtraBody(t *testing.T) {
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received = append(received, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	profile := Profile{
		ID:         "gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Models:     []string{"model-a", "model-b"},
		Model:      "model-b",
		ExtraBody: RequestBody{
			"metadata":    map[string]any{"profile": "gateway", "shared": "profile"},
			"temperature": 0.2,
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"service_tier": "economy"},
			"model-b": {
				"metadata":     map[string]any{"model": "model-b", "shared": "model"},
				"temperature":  0.7,
				"service_tier": "priority",
			},
		},
	}
	client := NewClient(profile.Config())
	call := func() {
		t.Helper()
		if _, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}); err != nil {
			t.Fatalf("RunMessage() error = %v", err)
		}
	}
	call()

	selected := profile.Config()
	selected.Model = "model-a"
	if err := client.ApplyModelConfig(selected); err != nil {
		t.Fatalf("ApplyModelConfig() model switch error = %v", err)
	}
	call()

	if len(received) != 2 {
		t.Fatalf("captured %d request bodies, want 2", len(received))
	}
	if received[0]["model"] != "model-b" || received[0]["service_tier"] != "priority" {
		t.Fatalf("model-b request = %#v, want selected model body", received[0])
	}
	if received[1]["model"] != "model-a" || received[1]["service_tier"] != "economy" {
		t.Fatalf("model-a request = %#v, want selected model body", received[1])
	}
	metadata, ok := received[0]["metadata"].(map[string]any)
	if !ok || metadata["profile"] != "gateway" || metadata["model"] != "model-b" || metadata["shared"] != "model" {
		t.Fatalf("model-b metadata = %#v, want recursively merged profile/model values", received[0]["metadata"])
	}
}

func TestClientRejectsProtectedRequestBodyAtApplyAndRunBoundaries(t *testing.T) {
	base := Config{
		ProfileID: "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
	}
	for _, test := range []struct {
		name string
		body RequestBody
	}{
		{name: "profile", body: RequestBody{"messages": "override"}},
		{name: "model", body: RequestBody{"model": "override"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			if test.name == "profile" {
				cfg.ExtraBody = test.body
			} else {
				cfg.ModelExtraBody = map[string]RequestBody{"model-a": test.body}
			}
			client := NewClient(cfg)
			err := client.ApplyModelConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), "contains protected field") {
				t.Fatalf("ApplyModelConfig() error = %v, want protected-field error", err)
			}
		})
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	invalid := base
	invalid.APIBaseURL = server.URL
	invalid.APIPath = "/chat/completions"
	invalid.ExtraBody = RequestBody{"stream": true}
	client := NewClient(invalid)
	if _, err := client.RunMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}); err == nil || !strings.Contains(err.Error(), "校验请求体配置失败") {
		t.Fatalf("RunMessage() error = %v, want validation error", err)
	}
	if requests != 0 {
		t.Fatalf("RunMessage() sent %d requests for invalid configuration, want 0", requests)
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
