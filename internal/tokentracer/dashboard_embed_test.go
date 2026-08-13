package tokentracer

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardAssetsContainBuiltEntry(t *testing.T) {
	data, err := fs.ReadFile(dashboardAssets, "dashboard/dist/index.html")
	if err != nil {
		t.Fatalf("read embedded dashboard: %v", err)
	}
	if !bytes.Contains(data, []byte(`<div id="root"></div>`)) {
		t.Fatalf("embedded index missing React root: %s", data)
	}
}

func firstEmbeddedAssetPath(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(dashboardAssets, "dashboard/dist/assets")
	if err != nil {
		t.Fatalf("read embedded dashboard assets: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("embedded dashboard has no assets")
	}
	return "/assets/" + entries[0].Name()
}

func TestDashboardRoutesEmbeddedAssets(t *testing.T) {
	handler := NewServer(New("Paw"), ServerConfig{}).handler()
	for _, tc := range []struct {
		path, contentType string
		status            int
	}{
		{"/", "text/html", http.StatusOK},
		{"/index.html", "text/html", http.StatusOK},
		{firstEmbeddedAssetPath(t), "", http.StatusOK},
		{"/assets/missing.js", "", http.StatusNotFound},
		{"/server.go", "", http.StatusNotFound},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.status {
			t.Fatalf("GET %s = %d, want %d", tc.path, recorder.Code, tc.status)
		}
		if tc.contentType != "" && !strings.Contains(recorder.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("GET %s content-type = %q", tc.path, recorder.Header().Get("Content-Type"))
		}
		if (tc.path == "/" || tc.path == "/index.html") && recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q, want no-store", tc.path, recorder.Header().Get("Cache-Control"))
		}
	}
}
