package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

func normalizeDeepSeekToolSchema(toolName string, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("DeepSeek 工具 %q Schema $: 非法 JSON: %w", toolName, err)
	}
	schema, ok := decoded.(map[string]any)
	if !ok || schema == nil {
		return nil, fmt.Errorf("DeepSeek 工具 %q Schema $: 必须是 JSON object", toolName)
	}
	if err := normalizeDeepSeekSchemaNode(toolName, "$", schema); err != nil {
		return nil, err
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("DeepSeek 工具 %q Schema $: 序列化失败: %w", toolName, err)
	}
	return append(json.RawMessage(nil), data...), nil
}

func normalizeDeepSeekSchemaNode(toolName, path string, node map[string]any) error {
	if ref, ok := node["$ref"].(string); ok && ref != "" {
		return nil
	}
	if additionalProperties, exists := node["additionalProperties"]; exists {
		allowed, ok := additionalProperties.(bool)
		if !ok || allowed {
			return fmt.Errorf("DeepSeek 工具 %q Schema %s.additionalProperties: 必须是 false", toolName, path)
		}
	}
	if typ, ok := node["type"].(string); ok && typ == "object" {
		properties, ok := node["properties"].(map[string]any)
		if !ok {
			properties = map[string]any{}
			node["properties"] = properties
		}
		originalRequired := make(map[string]bool)
		if required, ok := node["required"].([]any); ok {
			for _, item := range required {
				if name, ok := item.(string); ok {
					originalRequired[name] = true
				}
			}
		}
		keys := make([]string, 0, len(properties))
		for name := range properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			child, ok := properties[name].(map[string]any)
			if !ok {
				return fmt.Errorf("DeepSeek 工具 %q Schema %s.properties[%q]: 必须是 JSON object", toolName, path, name)
			}
			childPath := path + ".properties[" + name + "]"
			if err := normalizeDeepSeekSchemaNode(toolName, childPath, child); err != nil {
				return err
			}
			if !originalRequired[name] {
				makeNullable(child)
			}
		}
		required := make([]any, 0, len(keys))
		for _, name := range keys {
			required = append(required, name)
		}
		node["required"] = required
		node["additionalProperties"] = false
	}
	if items, ok := node["items"].(map[string]any); ok {
		if err := normalizeDeepSeekSchemaNode(toolName, path+".items", items); err != nil {
			return err
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if values, ok := node[key].([]any); ok {
			for index, value := range values {
				child, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("DeepSeek 工具 %q Schema %s.%s[%d]: 必须是 JSON object", toolName, path, key, index)
				}
				if err := normalizeDeepSeekSchemaNode(toolName, fmt.Sprintf("%s.%s[%d]", path, key, index), child); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := node[key].(map[string]any); ok {
			for name, value := range defs {
				child, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("DeepSeek 工具 %q Schema %s.%s[%q]: 必须是 JSON object", toolName, path, key, name)
				}
				if err := normalizeDeepSeekSchemaNode(toolName, path+"."+key+"["+name+"]", child); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func makeNullable(schema map[string]any) {
	if _, exists := schema["nullable"]; exists {
		return
	}
	if typ, ok := schema["type"].(string); ok {
		schema["type"] = []any{typ, "null"}
		return
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		anyOf = append(anyOf, map[string]any{"type": "null"})
		schema["anyOf"] = anyOf
		return
	}
	schema["anyOf"] = []any{map[string]any{"type": "null"}, schemaCopy(schema)}
}

func schemaCopy(schema map[string]any) map[string]any {
	data, _ := json.Marshal(schema)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}
