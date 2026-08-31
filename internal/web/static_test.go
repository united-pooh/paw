package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandlerServesSPAAndHasCachePolicy(t *testing.T) {
	handler := StaticHandler()
	for _, path := range []string{"/", "/index.html", "/sessions/example"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Paw Workbench") {
			t.Fatalf("GET %s = %d %s", path, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s cache = %q", path, recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestStaticHandlerServesHashedAssetImmutableAndAPI404JSON(t *testing.T) {
	handler := StaticHandler()
	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	body := index.Body.String()
	assetStart := strings.Index(body, "/assets/")
	if assetStart < 0 {
		t.Fatalf("index has no asset: %s", body)
	}
	assetEnd := strings.IndexAny(body[assetStart:], `"'`)
	if assetEnd < 0 {
		t.Fatalf("asset URL is malformed: %s", body)
	}
	assetPath := body[assetStart : assetStart+assetEnd]
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetPath, nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset = %d cache=%q", asset.Code, asset.Header().Get("Cache-Control"))
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if api.Code != http.StatusNotFound || !strings.Contains(api.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("API unknown = %d %s", api.Code, api.Body.String())
	}
}
