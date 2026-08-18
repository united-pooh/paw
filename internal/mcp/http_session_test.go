package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSessionCallUsesHeadersSessionIDAndSSE(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization=%q", got)
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/json") || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Errorf("accept=%q", r.Header.Get("Accept"))
		}

		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests.Add(1)
		switch request.Method {
		case "initialize":
			if got := r.Header.Get(httpSessionHeader); got != "" {
				t.Errorf("first request session id=%q, want empty", got)
			}
			w.Header().Set(httpSessionHeader, "session-1")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2025-06-18"}`),
			})
		case "tools/list":
			if got := r.Header.Get(httpSessionHeader); got != "session-1" {
				t.Errorf("session id=%q, want session-1", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			notification, _ := json.Marshal(rpcNotification{
				JSONRPC: "2.0",
				Method:  "notifications/tools/list_changed",
				Params:  json.RawMessage(`{"changed":true}`),
			})
			response, _ := json.Marshal(rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`{"tools":[]}`),
			})
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\nevent: message\ndata: %s\n\n", notification, response)
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	session, err := newHTTPSession(ServerConfig{
		Name:    "jina",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer test-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())

	var initialized initializeResult
	if err := session.Call(context.Background(), "initialize", nil, &initialized); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initialized.ProtocolVersion != "2025-06-18" {
		t.Fatalf("protocolVersion=%q", initialized.ProtocolVersion)
	}

	var listed pagedTools
	if err := session.Call(context.Background(), "tools/list", nil, &listed); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if requests.Load() != 2 || len(listed.Tools) != 0 {
		t.Fatalf("requests=%d tools=%#v", requests.Load(), listed.Tools)
	}
	select {
	case notification := <-session.Notifications():
		if notification.Method != "notifications/tools/list_changed" {
			t.Fatalf("notification=%#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HTTP notification")
	}
}

func TestHTTPSessionNotifyAcceptsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	session, err := newHTTPSession(ServerConfig{Name: "jina", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if err := session.Notify(context.Background(), "notifications/initialized", nil); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
}

func TestManagerStartsHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(httpSessionHeader, "manager-session")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}}}`),
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`{"tools":[{"name":"search","description":"search web","inputSchema":{"type":"object","properties":{}}}]}`),
			})
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result:  json.RawMessage(`{"content":[{"type":"text","text":"searched:kimi"}]}`),
			})
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	manager, err := Start(context.Background(), Config{Servers: map[string]ServerConfig{
		"jina": {Name: "jina", URL: server.URL, Enabled: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	waitReady(t, manager)

	if !snapshotHasTool(manager.Snapshot(), "jina__search") {
		t.Fatalf("snapshot=%#v", manager.Snapshot().Tools)
	}
	status := manager.Status()
	if len(status) != 1 || status[0].State != "running" || status[0].PID != 0 {
		t.Fatalf("status=%#v", status)
	}
	result, err := manager.Call(context.Background(), "jina__search", json.RawMessage(`{"query":"kimi"}`))
	if err != nil || result != "searched:kimi" {
		t.Fatalf("tools/call=(%q,%v)", result, err)
	}
}
