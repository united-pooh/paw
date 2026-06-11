package tool

import (
	"sort"
	"strings"
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
