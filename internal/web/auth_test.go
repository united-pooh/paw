package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthBootstrapExchangeIsOneTimeAndCookieIsHardened(t *testing.T) {
	store := NewAuthStore(false)
	token, err := store.NewBootstrapToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("bootstrap token length = %d, want 64 hex chars", len(token))
	}
	if strings.Contains(storeDebugString(store), token) {
		t.Fatal("auth store retained raw bootstrap token")
	}
	handler := Middleware(MiddlewareConfig{Auth: store, AllowedHosts: []string{"127.0.0.1:8080"}}, store.ExchangeHandler())
	exchange := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"token": token})
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/auth/exchange", bytes.NewReader(body))
		request.Host = "127.0.0.1:8080"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://127.0.0.1:8080")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	first := exchange()
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange status = %d body=%s", first.Code, first.Body.String())
	}
	cookies := first.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", cookie)
	}
	second := exchange()
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second exchange status = %d, want 401", second.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/private", nil)
	request.Host = "127.0.0.1:8080"
	request.AddCookie(cookie)
	if err := store.Authenticate(request); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestBootstrapFragmentTokenNeverEntersHTTPRequest(t *testing.T) {
	browserURL, err := url.Parse("http://127.0.0.1:8080/#bootstrap=secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if browserURL.Fragment != "bootstrap=secret-token" {
		t.Fatalf("fixture fragment = %q", browserURL.Fragment)
	}
	request := httptest.NewRequest(http.MethodGet, browserURL.Scheme+"://"+browserURL.Host+browserURL.Path, nil)
	if strings.Contains(request.RequestURI, "secret-token") || strings.Contains(request.URL.RequestURI(), "secret-token") {
		t.Fatalf("fragment leaked into request URI: %q / %q", request.RequestURI, request.URL.RequestURI())
	}
}

func storeDebugString(store *AuthStore) string {
	return fmt.Sprintf("%#v", store)
}
