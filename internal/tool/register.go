package tool

import (
	"sort"
	"strings"

	"gocode/internal/model"
)

type Registry struct {
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
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[tool.Name()] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Describe() []string {
	if r == nil || len(r.tools) == 0 {
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

// Definitions 返回注册表中所有工具的 ToolDefinition 切片，用于原生工具调用请求。
func (r *Registry) Definitions() []model.ToolDefinition {
	if r == nil || len(r.tools) == 0 {
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
