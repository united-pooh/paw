package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/app"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

func TestBootstrapWorkspaceAndSessionHandlers(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	runtime := newWebTestRuntime(t, workspaceRoot)
	recent, err := app.NewRecentWorkspaceStore(filepath.Join(t.TempDir(), "recent.json"))
	if err != nil {
		t.Fatal(err)
	}
	factory := func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error) { return runtime, nil }
	supervisor := app.NewSupervisor(app.SupervisorConfig{Capacity: 2, Factory: factory, Recent: recent})
	auth := NewAuthStore(false)
	server, err := NewServer(ServerConfig{Listen: "127.0.0.1:0", Supervisor: supervisor, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	client, cookie := authenticatedClient(t, server, auth)

	openBody, _ := json.Marshal(openWorkspaceRequest{Path: workspaceRoot})
	response := doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/open", openBody, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("open status = %d body=%s", response.StatusCode, readBody(response))
	}
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/bootstrap", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status = %d", response.StatusCode)
	}
	var bootstrap bootstrapResponse
	decodeResponse(t, response, &bootstrap)
	if bootstrap.LoadedRuntimes != 1 || len(bootstrap.RecentWorkspaces) != 1 {
		t.Fatalf("bootstrap = %#v", bootstrap)
	}

	createBody, _ := json.Marshal(createSessionRequest{CommandID: "create-1", SessionID: "session-1"})
	response = doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions", createBody, cookie)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", response.StatusCode, readBody(response))
	}
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions?limit=10", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d", response.StatusCode)
	}
	var page app.SessionPage
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].SessionID != "session-1" {
		t.Fatalf("session page = %#v", page)
	}
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions/session-1", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d body=%s", response.StatusCode, readBody(response))
	}
	var snapshot app.SessionSnapshot
	decodeResponse(t, response, &snapshot)
	if snapshot.SessionID != "session-1" || snapshot.StreamID == "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions/session-1/export", nil, cookie)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Disposition") == "" {
		t.Fatalf("export = %d headers=%v", response.StatusCode, response.Header)
	}

	response = doJSON(t, client, http.MethodDelete, server.URL()+"/api/recent-workspaces/"+string(workspace.ID), nil, cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("forget status = %d", response.StatusCode)
	}
	if _, ok := supervisor.Runtime(workspace.ID); !ok {
		t.Fatal("forget recent closed loaded runtime")
	}
	response = doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/close", []byte(`{}`), cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("close status = %d body=%s", response.StatusCode, readBody(response))
	}
}

type webBlockingTurnRunner struct {
	mu      sync.Mutex
	current string
	started chan struct{}
	release chan struct{}
}

func (r *webBlockingTurnRunner) CurrentSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}
func (r *webBlockingTurnRunner) LoadSession(_ context.Context, sessionID string) (loop.SessionLoadResult, error) {
	r.mu.Lock()
	r.current = sessionID
	r.mu.Unlock()
	return loop.SessionLoadResult{}, nil
}
func (r *webBlockingTurnRunner) RunTurnWithTiming(ctx context.Context, _ string, _ string, _ time.Time) (loop.TurnExecution, error) {
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return loop.TurnExecution{}, ctx.Err()
	}
	return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "answer"}}, nil
}

func TestMessageHandlerAcceptsSubmitAndReturnsBusy(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	coordinator := app.NewWorkspaceCoordinator()
	hub, err := app.NewEventHub(app.EventHubConfig{WorkspaceID: workspace.ID, StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &webBlockingTurnRunner{current: "s1", started: make(chan struct{}), release: make(chan struct{})}
	runtime := &app.WorkspaceRuntime{Root: workspaceRoot, Store: store, Coordinator: coordinator, EventHub: hub}
	runtime.SessionService = app.NewSessionService(store, coordinator)
	runtime.TurnService = app.NewTurnService(runner, store, coordinator, hub, nil)
	supervisor := app.NewSupervisor(app.SupervisorConfig{Factory: func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error) { return runtime, nil }})
	if _, err := supervisor.Open(context.Background(), app.WorkspaceRuntimeOptions{Root: workspaceRoot}); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStore(false)
	server, err := NewServer(ServerConfig{Listen: "127.0.0.1:0", Supervisor: supervisor, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	client, cookie := authenticatedClient(t, server, auth)
	body, _ := json.Marshal(submitMessageRequest{CommandID: "submit-1", SessionVersion: 0, Text: "hello"})
	response := doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions/s1/messages", body, cookie)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("submit = %d %s", response.StatusCode, readBody(response))
	}
	<-runner.started
	body, _ = json.Marshal(submitMessageRequest{CommandID: "submit-2", SessionVersion: 0, Text: "hello"})
	response = doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/sessions/s2/messages", body, cookie)
	if response.StatusCode != http.StatusConflict || !strings.Contains(readBody(response), "workspace_busy") {
		t.Fatalf("busy response = %d", response.StatusCode)
	}
	close(runner.release)
}

func TestServerShutdownStopsHTTPAndSupervisor(t *testing.T) {
	workspaceRoot := t.TempDir()
	runtime := newWebTestRuntime(t, workspaceRoot)
	supervisor := app.NewSupervisor(app.SupervisorConfig{Factory: func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error) { return runtime, nil }})
	auth := NewAuthStore(false)
	server, err := NewServer(ServerConfig{Listen: "127.0.0.1:0", Supervisor: supervisor, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Open(context.Background(), app.WorkspaceRuntimeOptions{Root: workspaceRoot}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatal(err)
	}
	if supervisor.LoadedCount() != 0 {
		t.Fatalf("loaded runtimes = %d", supervisor.LoadedCount())
	}
}

func newWebTestRuntime(t *testing.T, root string) *app.WorkspaceRuntime {
	t.Helper()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := mustWorkspace(t, root)
	hub, err := app.NewEventHub(app.EventHubConfig{WorkspaceID: workspace.ID, StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := app.NewWorkspaceCoordinator()
	runtime := &app.WorkspaceRuntime{Root: workspace.Path, Store: store, Coordinator: coordinator, EventHub: hub}
	runtime.SessionService = app.NewSessionService(store, coordinator)
	return runtime
}

func authenticatedClient(t *testing.T, server *Server, auth *AuthStore) (*http.Client, *http.Cookie) {
	t.Helper()
	token, err := auth.NewBootstrapToken()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	response, err := http.Post(server.URL()+"/api/auth/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("exchange = %d cookies=%#v body=%s", response.StatusCode, response.Cookies(), readBody(response))
	}
	return http.DefaultClient, response.Cookies()[0]
}

func doJSON(t *testing.T, client *http.Client, method, url string, body []byte, cookie *http.Cookie) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(response *http.Response) string {
	if response == nil || response.Body == nil {
		return ""
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	return string(data)
}

func mustWorkspace(t *testing.T, root string) app.WorkspacePath {
	t.Helper()
	workspace, err := app.CanonicalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}
