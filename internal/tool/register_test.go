package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type namespacedTestTool struct {
	name      string
	namespace string
}

func (t namespacedTestTool) Name() string        { return t.name }
func (t namespacedTestTool) Description() string { return t.name }
func (t namespacedTestTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t namespacedTestTool) Run(context.Context, json.RawMessage) (string, error) { return "", nil }
func (t namespacedTestTool) Namespace() string                                    { return t.namespace }

func TestRegistryReplaceNamespaceIsAtomicAndRemovesStaleTools(t *testing.T) {
	registry := NewRegistry()
	registry.Register(namespacedTestTool{name: "built_in", namespace: ""})
	if err := registry.ReplaceNamespace("mcp", []Tool{
		namespacedTestTool{name: "codegraph__old", namespace: "mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceNamespace("mcp", []Tool{
		namespacedTestTool{name: "codegraph__new", namespace: "mcp"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("codegraph__old"); ok {
		t.Fatal("stale namespaced tool remains")
	}
	if _, ok := registry.Get("codegraph__new"); !ok {
		t.Fatal("new namespaced tool missing")
	}

	err := registry.ReplaceNamespace("mcp", []Tool{
		namespacedTestTool{name: "built_in", namespace: "mcp"},
	})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision error=%v", err)
	}
	if _, ok := registry.Get("codegraph__new"); !ok {
		t.Fatal("failed replacement removed existing namespace")
	}
}
