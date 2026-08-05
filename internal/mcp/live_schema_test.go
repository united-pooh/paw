package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/model"
)

// This opt-in smoke test performs initialize + tools/list only. It never calls
// a discovered tool, and process stderr stays inside the redacting tail buffer.
func TestLiveCodeGraphAndJinaSchemasPrepareForDeepSeek(t *testing.T) {
	if os.Getenv("PAW_RUN_MCP_SCHEMA_SNAPSHOT") != "1" {
		t.Skip("set PAW_RUN_MCP_SCHEMA_SNAPSHOT=1 to inspect live CodeGraph/Jina schemas")
	}
	jinaKey := os.Getenv("JINA_API_KEY")
	if jinaKey == "" {
		t.Skip("JINA_API_KEY is required for the opt-in Jina schema snapshot")
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	servers := []ServerConfig{
		{Name: "codegraph", Command: "codegraph", Args: []string{"serve", "--mcp"}, WorkDir: repositoryRoot, Enabled: true},
		{Name: "jina", Command: "npx", Args: []string{"-y", "mcp-remote", "https://mcp.jina.ai/v1", "--header", "Authorization: Bearer " + jinaKey}, WorkDir: repositoryRoot, Enabled: true},
	}
	for _, config := range servers {
		t.Run(config.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			session, err := startSession(ctx, config)
			if err != nil {
				t.Fatalf("start %s: %v", config.Name, err)
			}
			defer func() { _ = session.Close(context.Background()) }()
			var initialized initializeResult
			if err := session.Call(ctx, "initialize", map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]string{"name": clientName, "version": clientVersion},
			}, &initialized); err != nil {
				t.Fatalf("initialize %s: %v", config.Name, err)
			}
			if err := session.Notify(ctx, "notifications/initialized", nil); err != nil {
				t.Fatalf("initialized notification %s: %v", config.Name, err)
			}
			listed, err := listTools(ctx, session)
			if err != nil {
				t.Fatalf("tools/list %s: %v", config.Name, err)
			}
			definitions := make([]model.ToolDefinition, 0, len(listed))
			for _, item := range listed {
				if isSensitiveMCPToolName(item.Name) {
					continue
				}
				schema := append(json.RawMessage(nil), item.InputSchema...)
				definitions = append(definitions, model.ToolDefinition{Name: config.Name + "__" + item.Name, Description: item.Description, InputSchema: schema})
			}
			prepared, err := (model.DeepSeekAdapter{}).PrepareTools(definitions)
			if err != nil {
				t.Fatalf("prepare %s schemas: %v", config.Name, err)
			}
			if len(prepared) != len(definitions) {
				t.Fatalf("prepared %d of %d %s tools", len(prepared), len(definitions), config.Name)
			}
		})
	}
}
