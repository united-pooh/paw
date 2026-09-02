package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/app"
)

func newCompletionsTestServer(t *testing.T, workspaceRoot string) (*Server, app.WorkspacePath, *http.Cookie) {
	t.Helper()
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	_, cookie := authenticatedClient(t, server, auth)
	return server, workspace, cookie
}

func TestCompletionsHandlerListsWorkspaceFiles(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "docs", "guide.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, workspace, cookie := newCompletionsTestServer(t, workspaceRoot)
	client := http.DefaultClient

	// 空 query：列出顶层条目，目录带 dir 标记
	response := doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%40&query=", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("completions status = %d body=%s", response.StatusCode, readBody(response))
	}
	var payload struct {
		Items []completionItem `json:"items"`
	}
	decodeResponse(t, response, &payload)
	var sawReadme, sawDocs bool
	for _, item := range payload.Items {
		if item.Label == "README.md" && !item.Dir {
			sawReadme = true
		}
		if item.Label == "docs/" && item.Dir {
			sawDocs = true
		}
	}
	if !sawReadme || !sawDocs {
		t.Fatalf("items = %#v, want README.md 与 docs/", payload.Items)
	}

	// 前缀过滤：read 只命中 README.md
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%40&query=read", nil, cookie)
	decodeResponse(t, response, &payload)
	if len(payload.Items) != 1 || payload.Items[0].Label != "README.md" {
		t.Fatalf("filtered items = %#v", payload.Items)
	}

	// 目录下钻：docs/ 前缀命中目录内文件
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%40&query=docs/g", nil, cookie)
	decodeResponse(t, response, &payload)
	if len(payload.Items) != 1 || payload.Items[0].Label != "guide.md" {
		t.Fatalf("nested items = %#v", payload.Items)
	}

	// 非法 trigger 返回 400
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%23&query=x", nil, cookie)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid trigger status = %d", response.StatusCode)
	}
}

func TestCompletionsHandlerListsCommandsAndSkills(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", configHome)
	skillDir := filepath.Join(configHome, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Demo Skill\ndescription: 演示技能\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, workspace, cookie := newCompletionsTestServer(t, t.TempDir())
	client := http.DefaultClient

	response := doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%2F&query=", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("completions status = %d body=%s", response.StatusCode, readBody(response))
	}
	var payload struct {
		Items []completionItem `json:"items"`
	}
	decodeResponse(t, response, &payload)
	labels := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		labels = append(labels, item.Label)
	}
	joined := strings.Join(labels, ",")
	for _, want := range []string{"/task", "/streamma", "/demo-skill"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("labels = %v, want 包含 %s", labels, want)
		}
	}

	// $ 技能触发
	response = doJSON(t, client, http.MethodGet, server.URL()+"/api/workspaces/"+string(workspace.ID)+"/completions?trigger=%24&query=demo", nil, cookie)
	decodeResponse(t, response, &payload)
	if len(payload.Items) != 1 || payload.Items[0].Label != "$demo-skill" {
		t.Fatalf("skill items = %#v", payload.Items)
	}
}

func TestPickWorkspaceHandler(t *testing.T) {
	original := pickFolder
	t.Cleanup(func() { pickFolder = original })

	auth := NewAuthStore(false)
	server, err := NewServer(ServerConfig{Listen: "127.0.0.1:0", Supervisor: app.NewSupervisor(app.SupervisorConfig{}), Auth: auth})
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
	_, cookie := authenticatedClient(t, server, auth)
	client := http.DefaultClient

	pickFolder = func() (string, error) { return "/tmp/picked", nil }
	response := doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/pick", nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pick status = %d body=%s", response.StatusCode, readBody(response))
	}
	var payload struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	decodeResponse(t, response, &payload)
	if payload.Path != "/tmp/picked" || payload.Cancelled {
		t.Fatalf("pick payload = %#v", payload)
	}

	// 用户取消：cancelled=true 且无路径
	pickFolder = func() (string, error) { return "", nil }
	response = doJSON(t, client, http.MethodPost, server.URL()+"/api/workspaces/pick", nil, cookie)
	var cancelPayload struct {
		Path      string `json:"path"`
		Cancelled bool   `json:"cancelled"`
	}
	decodeResponse(t, response, &cancelPayload)
	if !cancelPayload.Cancelled || cancelPayload.Path != "" {
		t.Fatalf("cancel payload = %#v", cancelPayload)
	}
}
