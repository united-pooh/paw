package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestManagerStartsDiscoversPaginatedCapabilitiesAndCalls(t *testing.T) {
	manager, err := Start(context.Background(), Config{
		Path: "/tmp/mcp.toml",
		Servers: map[string]ServerConfig{
			"codegraph": {
				Name:    "codegraph",
				Command: os.Args[0],
				Args:    []string{"-test.run=TestMCPManagerHelper"},
				WorkDir: t.TempDir(),
				Enabled: true,
				Env:     map[string]string{"GO_WANT_MCP_MANAGER_HELPER": "1"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	snapshot := manager.Snapshot()
	if len(snapshot.Tools) != 7 {
		t.Fatalf("discovered tools=%d, want 7 (2 MCP + 5 virtual)", len(snapshot.Tools))
	}
	if !snapshotHasTool(snapshot, "codegraph__explore") || !snapshotHasTool(snapshot, "codegraph__context") {
		t.Fatalf("snapshot=%#v", snapshot.Tools)
	}
	statuses := manager.Status()
	if len(statuses) != 1 || statuses[0].State != "running" || statuses[0].Tools != 2 || statuses[0].Resources != 2 || statuses[0].Templates != 1 || statuses[0].Prompts != 2 {
		t.Fatalf("statuses=%#v", statuses)
	}

	result, err := manager.Call(context.Background(), "codegraph__explore", json.RawMessage(`{"query":"main"}`))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result != "explored:main" {
		t.Fatalf("Call()=%q", result)
	}
	if _, err := manager.Call(context.Background(), "codegraph__missing", nil); err == nil {
		t.Fatal("unknown MCP tool was accepted")
	}

	resources, err := manager.Call(context.Background(), "codegraph__list_resources", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(resources, "file://main.go") {
		t.Fatalf("list_resources=(%q,%v)", resources, err)
	}
	prompt, err := manager.Call(context.Background(), "codegraph__get_prompt", json.RawMessage(`{"name":"explain"}`))
	if err != nil || !strings.Contains(prompt, "user: explain this graph") {
		t.Fatalf("get_prompt=(%q,%v)", prompt, err)
	}
}

func TestManagerKeepsDisabledServerWithoutLaunchingIt(t *testing.T) {
	manager, err := Start(context.Background(), Config{Servers: map[string]ServerConfig{
		"disabled": {Name: "disabled", Command: "does-not-exist", WorkDir: t.TempDir(), Enabled: false},
	}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	statuses := manager.Status()
	if len(statuses) != 1 || statuses[0].State != "disabled" {
		t.Fatalf("statuses=%#v", statuses)
	}
}

func TestCodeGraphSmokeWhenEnabled(t *testing.T) {
	if os.Getenv("PAW_RUN_CODEGRAPH_SMOKE") != "1" {
		t.Skip("set PAW_RUN_CODEGRAPH_SMOKE=1 to run the local CodeGraph smoke test")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := Start(context.Background(), Config{Servers: map[string]ServerConfig{
		"codegraph": {
			Name:    "codegraph",
			Command: "codegraph",
			Args:    []string{"serve", "--mcp"},
			WorkDir: root,
			Enabled: true,
		},
	}})
	if err != nil {
		t.Fatalf("CodeGraph Start() error = %v", err)
	}
	defer func() { _ = manager.Close(context.Background()) }()
	var explore bool
	for _, item := range manager.Snapshot().Tools {
		if item.Name == "codegraph__codegraph_explore" {
			explore = true
		}
	}
	if !explore {
		t.Fatalf("CodeGraph tool missing: %#v", manager.Snapshot().Tools)
	}
	result, err := manager.Call(context.Background(), "codegraph__codegraph_status", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(result, "CodeGraph") {
		t.Fatalf("CodeGraph status=(%q,%v)", result, err)
	}
}

func snapshotHasTool(snapshot Snapshot, name string) bool {
	for _, item := range snapshot.Tools {
		if item.Name == name {
			return true
		}
	}
	return false
}

func TestMCPManagerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_MANAGER_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.Method == "" {
			continue
		}
		if request.Method == "notifications/initialized" {
			continue
		}
		result, err := managerHelperResult(request.Method, request.Params)
		if err != nil {
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcError{Code: -32601, Message: err.Error()}})
			continue
		}
		_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
	}
}

func managerHelperResult(method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "initialize":
		return json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{},"resources":{},"prompts":{}},"serverInfo":{"name":"helper","version":"1"}}`), nil
	case "tools/list":
		var page struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &page)
		if page.Cursor == "page-2" {
			return json.RawMessage(`{"tools":[{"name":"context","description":"Context","inputSchema":{"type":"object","properties":{"path":{"type":"string"}}}}]}`), nil
		}
		return json.RawMessage(`{"tools":[{"name":"explore","description":"Explore","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],"nextCursor":"page-2"}`), nil
	case "resources/list":
		var page struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &page)
		if page.Cursor == "resource-2" {
			return json.RawMessage(`{"resources":[{"uri":"file://README.md","name":"README"}]}`), nil
		}
		return json.RawMessage(`{"resources":[{"uri":"file://main.go","name":"main"}],"nextCursor":"resource-2"}`), nil
	case "resources/templates/list":
		return json.RawMessage(`{"resourceTemplates":[{"uriTemplate":"file:///{path}","name":"files"}]}`), nil
	case "prompts/list":
		var page struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &page)
		if page.Cursor == "prompt-2" {
			return json.RawMessage(`{"prompts":[{"name":"review","description":"Review"}]}`), nil
		}
		return json.RawMessage(`{"prompts":[{"name":"explain","description":"Explain"}],"nextCursor":"prompt-2"}`), nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(params, &call)
		var input struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(call.Arguments, &input)
		return json.RawMessage(`{"content":[{"type":"text","text":"explored:` + input.Query + `"}]}`), nil
	case "resources/read":
		return json.RawMessage(`{"contents":[{"uri":"file://main.go","mimeType":"text/plain","text":"package main"}]}`), nil
	case "prompts/get":
		return json.RawMessage(`{"description":"Graph explanation","messages":[{"role":"user","content":{"type":"text","text":"explain this graph"}}]}`), nil
	default:
		return nil, os.ErrNotExist
	}
}
