package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"paw/internal/app"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
	selecttool "paw/internal/tool/select"
)

type interactionTurnRunner struct {
	mu      sync.Mutex
	current string
	started chan struct{}
	release chan struct{}
}

func (r *interactionTurnRunner) CurrentSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}
func (r *interactionTurnRunner) LoadSession(_ context.Context, sessionID string) (loop.SessionLoadResult, error) {
	r.mu.Lock()
	r.current = sessionID
	r.mu.Unlock()
	return loop.SessionLoadResult{}, nil
}
func (r *interactionTurnRunner) PrepareSteer(string) (loop.SteerAdmission, bool) {
	return &webSteerAdmission{}, true
}
func (r *interactionTurnRunner) RunTurnWithTiming(ctx context.Context, _ string, _ string, _ time.Time) (loop.TurnExecution, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
		return loop.TurnExecution{}, ctx.Err()
	}
	return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "answer"}}, nil
}

func TestInteractionHandlersAnswerQuestionAndDecidePermission(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	coordinator := app.NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	hub, err := app.NewEventHub(app.EventHubConfig{WorkspaceID: workspace.ID, StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	interactions := app.NewInteractionHub(workspace.ID, coordinator, hub)
	t.Cleanup(func() { _ = interactions.Close() })
	runtime := &app.WorkspaceRuntime{Root: workspaceRoot, Store: store, Coordinator: coordinator, Interactions: interactions, EventHub: hub}
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
	base := server.URL() + "/api/workspaces/" + string(workspace.ID) + "/sessions/s1/interactions"

	go func() { _, _ = interactions.RequestQuestion(context.Background(), "s1", "turn-1", questionRequest()) }()
	requestID := waitForPending(t, coordinator, "s1", app.InteractionKindQuestion)
	body, _ := json.Marshal(answerQuestionRequest{SelectedOption: "b"})
	response := doJSON(t, client, http.MethodPost, base+"/"+requestID+"/answer", body, cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("answer status = %d body=%s", response.StatusCode, readBody(response))
	}
	response = doJSON(t, client, http.MethodPost, base+"/"+requestID+"/answer", body, cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("repeated answer status = %d", response.StatusCode)
	}

	go func() {
		_, _ = interactions.RequestPermission(context.Background(), "s1", "turn-1", loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "call-1", CanonicalPath: "/tmp/file.go"})
	}()
	requestID = waitForPending(t, coordinator, "s1", app.InteractionKindPermission)
	body, _ = json.Marshal(decidePermissionRequest{Decision: "deny"})
	response = doJSON(t, client, http.MethodPost, base+"/"+requestID+"/decision", body, cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("decision status = %d body=%s", response.StatusCode, readBody(response))
	}
	response = doJSON(t, client, http.MethodPost, base+"/"+requestID+"/decision", body, cookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("repeated decision status = %d", response.StatusCode)
	}
	response = doJSON(t, client, http.MethodPost, base+"/missing/decision", body, cookie)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing decision status = %d", response.StatusCode)
	}
	response = doJSON(t, client, http.MethodPost, base+"/missing/answer", []byte(`{}`), cookie)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing answer status = %d", response.StatusCode)
	}
}

func questionRequest() selecttool.Request {
	return selecttool.Request{Prompt: "Pick", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}}
}

func waitForPending(t *testing.T, coordinator *app.WorkspaceCoordinator, sessionID, kind string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		for _, pending := range coordinator.SessionSnapshot(sessionID).Pending {
			if pending.Kind == kind {
				return pending.RequestID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %s interaction was not registered", kind)
		}
		time.Sleep(time.Millisecond)
	}
}
