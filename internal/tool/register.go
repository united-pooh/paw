package tool

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"paw/internal/model"
)

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) {
	if r == nil || tool == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[tool.Name()] = tool
}

// ReplaceNamespace atomically replaces tools belonging to one logical
// namespace. Tools that do not belong to that namespace are treated as
// collisions and are never overwritten.
func (r *Registry) ReplaceNamespace(namespace string, tools []Tool) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return fmt.Errorf("tool namespace is empty")
	}
	for _, registered := range tools {
		if registered == nil || strings.TrimSpace(registered.Name()) == "" {
			return fmt.Errorf("tool namespace %q contains an invalid tool", namespace)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	seen := make(map[string]struct{}, len(tools))
	for _, registered := range tools {
		name := registered.Name()
		if _, ok := seen[name]; ok {
			return fmt.Errorf("tool namespace %q contains duplicate tool %q", namespace, name)
		}
		seen[name] = struct{}{}
		if existing, ok := r.tools[name]; ok && toolNamespace(existing) != namespace {
			return fmt.Errorf("tool %q collides with existing namespace %q", name, toolNamespace(existing))
		}
	}
	for name, existing := range r.tools {
		if toolNamespace(existing) == namespace {
			delete(r.tools, name)
		}
	}
	for _, registered := range tools {
		r.tools[registered.Name()] = registered
	}
	return nil
}

// RemoveNamespace removes all tools belonging to a logical namespace.
func (r *Registry) RemoveNamespace(namespace string) {
	if r == nil {
		return
	}
	namespace = strings.TrimSpace(namespace)
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, registered := range r.tools {
		if toolNamespace(registered) == namespace {
			delete(r.tools, name)
		}
	}
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) IsConcurrencySafe(name string, input []byte) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	registered, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	safeTool, ok := registered.(ConcurrencySafeTool)
	if !ok {
		return false
	}
	return safeTool.IsConcurrencySafe(append([]byte(nil), input...))
}

func (r *Registry) Describe() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	descriptions := make([]string, 0, len(r.tools))
	for _, name := range names {
		tool := r.tools[name]
		description := tool.Name() + ": " + tool.Description()
		schema := strings.TrimSpace(string(tool.InputSchema()))
		if schema != "" {
			description += " input_schema=" + schema
		}
		descriptions = append(descriptions, description)
	}
	return descriptions
}

func (r *Registry) DescribeBrief() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	descriptions := make([]string, 0, len(r.tools))
	for _, name := range names {
		tool := r.tools[name]
		description := strings.TrimSpace(tool.Description())
		if description == "" {
			descriptions = append(descriptions, tool.Name())
			continue
		}
		descriptions = append(descriptions, tool.Name()+": "+description)
	}
	return descriptions
}

// Definitions 返回注册表中所有工具的 ToolDefinition 切片，用于原生工具调用请求。
func (r *Registry) Definitions() []model.ToolDefinition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]model.ToolDefinition, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		schema := t.InputSchema()
		if len(schema) == 0 {
			schema = []byte(`{"type":"object","properties":{}}`)
		}
		defs = append(defs, model.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: schema,
		})
	}
	return defs
}

type namespacedTool interface {
	Namespace() string
}

func toolNamespace(registered Tool) string {
	if namespaced, ok := registered.(namespacedTool); ok {
		return strings.TrimSpace(namespaced.Namespace())
	}
	return ""
}
