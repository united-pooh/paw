package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/app"
)

type testHeartbeatTicker struct {
	ch   chan time.Time
	once sync.Once
}

func (t *testHeartbeatTicker) C() <-chan time.Time { return t.ch }
func (t *testHeartbeatTicker) Stop()               { t.once.Do(func() { close(t.ch) }) }

func TestSSEWritesReplayWireFormatAndQueryCursorWins(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	runtime := newWebTestRuntime(t, workspaceRoot)
	first := publishSSEEvent(t, runtime.EventHub, "one")
	second := publishSSEEvent(t, runtime.EventHub, "two")
	supervisor := app.NewSupervisor(app.SupervisorConfig{Factory: func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error) { return runtime, nil }})
	if _, err := supervisor.Open(context.Background(), app.WorkspaceRuntimeOptions{Root: workspaceRoot}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+string(workspace.ID)+"/events?after=stream-test:"+itoa(first.Sequence), nil)
	request.SetPathValue("workspace_id", string(workspace.ID))
	request.Header.Set("Last-Event-ID", "stream-test:0")
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		SSEHandler{Supervisor: supervisor, Heartbeat: time.Hour}.ServeHTTP(recorder, request)
		close(done)
	}()
	waitForBody(t, recorder, "id: stream-test:"+itoa(second.Sequence))
	cancel()
	<-done
	body := recorder.String()
	if strings.Contains(body, `"text":"one"`) || !strings.Contains(body, `event: assistant.delta`) || !strings.Contains(body, `"text":"two"`) {
		t.Fatalf("SSE body = %s", body)
	}
}

func TestSSEHeartbeatResetAndClientCancel(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	runtime := newWebTestRuntime(t, workspaceRoot)
	publishSSEEvent(t, runtime.EventHub, "one")
	supervisor := app.NewSupervisor(app.SupervisorConfig{Factory: func(context.Context, app.WorkspaceRuntimeOptions) (*app.WorkspaceRuntime, error) { return runtime, nil }})
	if _, err := supervisor.Open(context.Background(), app.WorkspaceRuntimeOptions{Root: workspaceRoot}); err != nil {
		t.Fatal(err)
	}
	ticker := &testHeartbeatTicker{ch: make(chan time.Time, 1)}
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+string(workspace.ID)+"/events", nil)
	request.SetPathValue("workspace_id", string(workspace.ID))
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := newFlushRecorder()
	done := make(chan struct{})
	go func() {
		SSEHandler{Supervisor: supervisor, NewTicker: func(time.Duration) heartbeatTicker { return ticker }}.ServeHTTP(recorder, request)
		close(done)
	}()
	ticker.ch <- time.Now()
	waitForBody(t, recorder, ": heartbeat")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop on client cancel")
	}

	resetRequest := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+string(workspace.ID)+"/events?after=old:0", nil)
	resetRequest.SetPathValue("workspace_id", string(workspace.ID))
	resetRecorder := newFlushRecorder()
	SSEHandler{Supervisor: supervisor}.ServeHTTP(resetRecorder, resetRequest)
	if !strings.Contains(resetRecorder.String(), "event: event.reset_required") || !strings.Contains(resetRecorder.String(), "stream_mismatch") {
		t.Fatalf("reset SSE = %s", resetRecorder.String())
	}
}

func TestSSERequiresCookieThroughServerMiddleware(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := mustWorkspace(t, workspaceRoot)
	runtime := newWebTestRuntime(t, workspaceRoot)
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
	response, err := http.Get(server.URL() + "/api/workspaces/" + string(workspace.ID) + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("SSE unauthenticated status = %d", response.StatusCode)
	}
}

type flushRecorder struct {
	mu     sync.Mutex
	body   bytes.Buffer
	header http.Header
	status int
}

func newFlushRecorder() *flushRecorder          { return &flushRecorder{header: make(http.Header)} }
func (r *flushRecorder) Header() http.Header    { return r.header }
func (r *flushRecorder) WriteHeader(status int) { r.status = status }
func (r *flushRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(data)
}
func (r *flushRecorder) Flush()         {}
func (r *flushRecorder) String() string { r.mu.Lock(); defer r.mu.Unlock(); return r.body.String() }

func publishSSEEvent(t *testing.T, hub *app.EventHub, text string) app.AppEvent {
	t.Helper()
	event, err := app.NewAppEvent("workspace", "session", "turn", app.EventAssistantDelta, time.Now(), 1, app.AssistantDeltaPayload{PartID: "part", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	published, err := hub.Publish(event)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func waitForBody(t *testing.T, recorder *flushRecorder, contains string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(recorder.String(), contains) {
		if time.Now().After(deadline) {
			t.Fatalf("body %q does not contain %q", recorder.String(), contains)
		}
		time.Sleep(time.Millisecond)
	}
}

func itoa(value uint64) string { return fmt.Sprintf("%d", value) }
