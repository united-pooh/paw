package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigCreatesEmptyFile(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	cfg, err := LoadConfig(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("servers=%d, want 0", len(cfg.Servers))
	}

	path := filepath.Join(home, ".ccagent", "mcp.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file was not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%o, want 600", info.Mode().Perm())
	}
}

func TestLoadConfigReadsCodexStyleServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeMCPConfig(t, home, `
[mcp_servers.codegraph]
command = "codegraph"
args = ["serve", "--mcp"]
cwd = "."
enabled = true

[mcp_servers.codegraph.env]
CODEGRAPH_MCP_TOOLS = "explore,context"
`)

	cfg, err := LoadConfig(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := cfg.Servers["codegraph"]
	if !ok {
		t.Fatal("codegraph server not found")
	}
	if server.Command != "codegraph" || !reflect.DeepEqual(server.Args, []string{"serve", "--mcp"}) {
		t.Fatalf("server=%#v", server)
	}
	if server.WorkDir != workspace || server.Env["CODEGRAPH_MCP_TOOLS"] != "explore,context" {
		t.Fatalf("resolved server=%#v", server)
	}
	if !server.Enabled {
		t.Fatal("server should be enabled")
	}
}

func TestLoadConfigDefaultsEnabledAndWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeMCPConfig(t, home, `
[mcp_servers.defaulted]
command = "server"
`)

	cfg, err := LoadConfig(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.Servers["defaulted"]
	if !server.Enabled {
		t.Fatal("missing enabled should default to true")
	}
	if server.WorkDir != workspace {
		t.Fatalf("workdir=%q, want %q", server.WorkDir, workspace)
	}
	if len(server.Args) != 0 {
		t.Fatalf("args=%v, want empty", server.Args)
	}
}

func TestLoadConfigPreservesDisabledServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeMCPConfig(t, home, `
[mcp_servers.disabled]
command = "server"
enabled = false
`)

	cfg, err := LoadConfig(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := cfg.Servers["disabled"]
	if !ok || server.Enabled {
		t.Fatalf("disabled server=%#v, found=%v", server, ok)
	}
}

func TestLoadConfigRejectsInvalidEnabledServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeMCPConfig(t, home, `
[mcp_servers.bad]
command = ""
`)

	if _, err := LoadConfig(home, workspace); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadConfigRejectsInvalidServerName(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	writeMCPConfig(t, home, `
[mcp_servers."bad.name"]
command = "server"
`)

	if _, err := LoadConfig(home, workspace); err == nil {
		t.Fatal("expected server name validation error")
	}
}

func writeMCPConfig(t *testing.T, home, content string) {
	t.Helper()
	path := filepath.Join(home, ".ccagent", "mcp.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
