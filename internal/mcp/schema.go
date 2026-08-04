package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// validateToolSpecs 在修改 Manager 状态前整批验证工具 schema；
// 任一工具不兼容即返回错误，调用方必须保持旧快照不变。
func validateToolSpecs(specs []ToolSpec) error {
	for _, spec := range specs {
		if err := validateModelToolSchema(spec.Name, spec.ModelSchema()); err != nil {
			return err
		}
	}
	return nil
}

// validateModelToolSchema 验证模型侧函数工具 schema 的 Responses 兼容结构。
// 精确规则：
//  1. 空 schema 使用 {"type":"object","properties":{}}；
//  2. 必须是有效 JSON；
//  3. 顶层必须是 object；
//  4. 若存在 type，必须是字符串 "object"；缺少 type 可接受；
//  5. 若存在 properties，必须是 JSON object；缺少时按空 properties 处理；
//  6. 若存在 required，必须是字符串数组；
//  7. required 中每个名称必须存在于 properties；
//  8. 不因未知 JSON Schema keyword 拒绝，因为 Responses 工具显式 strict:false；
//  9. 错误必须包含工具名和精确原因。
func validateModelToolSchema(toolName string, schema json.RawMessage) error {
	schema = bytes.TrimSpace(schema)
	if len(schema) == 0 {
		return nil
	}
	if !json.Valid(schema) {
		return fmt.Errorf("MCP tool %q schema is not valid JSON", toolName)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		return fmt.Errorf("MCP tool %q schema must be a JSON object: %v", toolName, err)
	}

	if rawType, ok := root["type"]; ok {
		var typeName string
		if err := json.Unmarshal(rawType, &typeName); err != nil || typeName != "object" {
			return fmt.Errorf("MCP tool %q schema type must be \"object\"", toolName)
		}
	}

	properties := map[string]json.RawMessage{}
	if rawProps, ok := root["properties"]; ok {
		if err := json.Unmarshal(rawProps, &properties); err != nil {
			return fmt.Errorf("MCP tool %q schema properties must be an object", toolName)
		}
	}

	if rawRequired, ok := root["required"]; ok {
		var required []string
		if err := json.Unmarshal(rawRequired, &required); err != nil {
			return fmt.Errorf("MCP tool %q schema required must be an array", toolName)
		}
		for _, name := range required {
			if _, ok := properties[name]; !ok {
				return fmt.Errorf("MCP tool %q schema required property %q is not declared in properties", toolName, name)
			}
		}
	}
	return nil
}
