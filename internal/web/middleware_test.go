package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareEnforcesHostAuthOriginAndSecurityHeaders(t *testing.T) {
	store := NewAuthStore(false)
	token, err := store.NewBootstrapToken()
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := store.ExchangeBootstrapToken(token)
	if err != nil {
		t.Fatal(err)
	}
	handler := Middleware(MiddlewareConfig{
		Auth: store, AllowedHosts: []string{"127.0.0.1:8080"}, AllowedOrigin: "http://127.0.0.1:8080",
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	run := func(method, host string, authenticated bool, origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://"+host+"/api/private", nil)
		request.Host = host
		if authenticated {
			request.AddCookie(cookie)
		}
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	if got := run(http.MethodGet, "evil.example", true, "", ""); got.Code != http.StatusMisdirectedRequest {
		t.Fatalf("bad host status = %d", got.Code)
	}
	if got := run(http.MethodGet, "127.0.0.1:8080", false, "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", got.Code)
	}
	if got := run(http.MethodPost, "127.0.0.1:8080", true, "http://evil.example", "cross-site"); got.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", got.Code)
	}
	ok := run(http.MethodPost, "127.0.0.1:8080", true, "http://127.0.0.1:8080", "same-origin")
	if ok.Code != http.StatusNoContent {
		t.Fatalf("same-origin status = %d body=%s", ok.Code, ok.Body.String())
	}
	for _, header := range []string{"X-Request-ID", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if ok.Header().Get(header) == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
}

func TestMiddlewareDecodeJSONRejectsUnknownFieldsAndOversizedBody(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	handler := Middleware(MiddlewareConfig{
		AllowedHosts: []string{"127.0.0.1:8080"}, BodyLimit: 32,
		PublicPaths: map[string]struct{}{"/api/test": {}},
	}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var value input
		if err := DecodeJSON(writer, request, &value); err != nil {
			var tooLarge *http.MaxBytesError
			if strings.Contains(err.Error(), "request body too large") || strings.Contains(err.Error(), "http: request body too large") {
				writeJSONError(writer, http.StatusRequestEntityTooLarge, "body_too_large", err.Error(), RequestID(request.Context()))
				return
			}
			if _, ok := err.(*json.SyntaxError); ok {
				writeJSONError(writer, http.StatusBadRequest, "invalid_json", err.Error(), RequestID(request.Context()))
				return
			}
			if tooLarge != nil {
				writeJSONError(writer, http.StatusRequestEntityTooLarge, "body_too_large", err.Error(), RequestID(request.Context()))
				return
			}
			writeJSONError(writer, http.StatusBadRequest, "invalid_json", err.Error(), RequestID(request.Context()))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/test", bytes.NewBufferString(`{"name":"paw","unknown":true}`))
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
		t.Fatalf("unknown field response = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/test", bytes.NewBufferString(`{"name":"`+strings.Repeat("x", 100)+`"}`))
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized response = %d %s", recorder.Code, recorder.Body.String())
	}
}
