package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"paw/internal/app"
)

func TestTraceDetailHandlerScopedAndNotFound(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	runtime := &app.WorkspaceRuntime{Root: workspaceRoot, TraceDetail: app.NewTraceDetailStore(nil)}
	id, ok := runtime.TraceDetail.Put("tool_result", "full output")
	if !ok {
		t.Fatal("detail rejected")
	}
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
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	client, cookie := authenticatedClient(t, server, auth)
	base := server.URL() + "/api/workspaces/" + string(workspace.ID) + "/trace/"

	response := doJSON(t, client, http.MethodGet, base+id, nil, cookie)
	if response.StatusCode != http.StatusOK || !strings.Contains(readBody(response), "full output") {
		t.Fatalf("detail response = %d %s", response.StatusCode, readBody(response))
	}
	response = doJSON(t, client, http.MethodGet, base+"detail_missing", nil, cookie)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing detail response = %d", response.StatusCode)
	}
}
