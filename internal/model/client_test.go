package model

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetRequestHeadersOmitsAuthorizationWhenAPIKeyEmpty(t *testing.T) {
	t.Setenv(CustomAPIKeyEnvName, "")

	client := NewClient(Config{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       time.Minute,
	})
	if err := client.ApplyModelConfig(Config{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKey:        "",
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       time.Minute,
	}); err != nil {
		t.Fatalf("ApplyModelConfig() error = %v", err)
	}

	req := httptest.NewRequest("POST", CustomAPIBaseURL+CustomChatPath, nil)
	client.setRequestHeaders(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
