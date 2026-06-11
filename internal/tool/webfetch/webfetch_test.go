package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolFetchesURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello from server"))
	}))
	defer server.Close()

	tool := &Tool{Client: server.Client()}
	output, err := tool.Run(context.Background(), []byte(`{"url":"`+server.URL+`"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, "status: 200 OK") {
		t.Fatalf("output = %q, want status line", output)
	}
	if !strings.Contains(output, "hello from server") {
		t.Fatalf("output = %q, want response body", output)
	}
}
