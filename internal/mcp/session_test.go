package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessSessionUsesStdioAndEnvironment(t *testing.T) {
	workDir := t.TempDir()
	session, err := startSession(context.Background(), ServerConfig{
		Name:    "helper",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPProcessHelper"},
		WorkDir: workDir,
		Env:     map[string]string{"GO_WANT_MCP_PROCESS_HELPER": "1", "MCP_HELPER_VALUE": "from-test"},
	})
	if err != nil {
		t.Fatalf("startSession() error = %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close(context.Background())
	})

	var result struct {
		Method string `json:"method"`
		Value  string `json:"value"`
	}
	if err := session.Call(context.Background(), "initialize", map[string]string{"hello": "world"}, &result); err != nil {
		t.Fatalf("Call() error = %v, stderr=%q", err, session.StderrTail())
	}
	if result.Method != "initialize" || result.Value != "from-test" {
		t.Fatalf("result=%#v", result)
	}
	if session.PID() <= 0 {
		t.Fatalf("PID()=%d, want positive process id", session.PID())
	}
}

func TestProcessSessionReportsStderrTail(t *testing.T) {
	workDir := t.TempDir()
	session, err := startSession(context.Background(), ServerConfig{
		Name:    "helper",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPProcessHelper"},
		WorkDir: workDir,
		Env:     map[string]string{"GO_WANT_MCP_PROCESS_HELPER": "1", "MCP_HELPER_VALUE": "stderr-check"},
	})
	if err != nil {
		t.Fatalf("startSession() error = %v", err)
	}
	defer func() { _ = session.Close(context.Background()) }()

	var result map[string]any
	if err := session.Call(context.Background(), "stderr", nil, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && session.StderrTail() == "" {
		time.Sleep(time.Millisecond)
	}
	if got := session.StderrTail(); got == "" {
		t.Fatal("StderrTail() is empty")
	}
}

func TestMCPDiagnosticsRedactAuthorizationAndBearerSecrets(t *testing.T) {
	const secret = "jina_test_secret_123"
	buffer := newTailBuffer(4096)
	_, _ = buffer.Write([]byte("custom headers: { Authorization: 'Bearer " + secret + "' }\n"))
	got := buffer.String()
	if strings.Contains(got, secret) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("stderr was not redacted: %q", got)
	}
	diagnostic := truncateDiagnostic(`request failed Authorization="Bearer ` + secret + `"`)
	if strings.Contains(diagnostic, secret) || !strings.Contains(diagnostic, "[REDACTED]") {
		t.Fatalf("diagnostic was not redacted: %q", diagnostic)
	}
}

func TestMCPProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_PROCESS_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "MCP helper stderr")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		result, _ := json.Marshal(map[string]string{
			"method": request.Method,
			"value":  os.Getenv("MCP_HELPER_VALUE"),
		})
		response := rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
			return
		}
	}
}

func TestMergedEnvironmentOverridesWithoutDroppingBase(t *testing.T) {
	values := mergedEnvironment(map[string]string{"MCP_TEST_VALUE": "override"})
	found := false
	for _, value := range values {
		if value == "MCP_TEST_VALUE=override" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("merged environment does not contain override: %v", values)
	}
}
