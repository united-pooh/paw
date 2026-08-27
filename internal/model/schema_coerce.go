package model

import (
	"fmt"
	"strconv"
	"strings"
)

// coerceValueToSchema repairs common LLM type mistakes against an original
// JSON Schema before validation: numeric strings for number/integer fields,
// "true"/"false" strings for booleans, and numbers for string fields. Values
// that cannot be converted are left untouched; validation remains the final
// authority. changed reports whether any value was rewritten.
func coerceValueToSchema(value any, schema map[string]any) (any, bool, error) {
	coerced, changed, err := coerceNode(value, schema)
	if err != nil {
		return value, false, err
	}
	return coerced, changed, nil
}

func coerceNode(value any, node map[string]any) (any, bool, error) {
	if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
		if resolved, ok := resolveLocalSchemaRef(node, ref); ok {
			return coerceNode(value, resolved)
		}
		return value, false, nil
	}
	if branches, ok := node["anyOf"].([]any); ok {
		for _, branch := range branches {
			branchNode, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			if coerced, changed, err := coerceNode(value, branchNode); err == nil && typeMatchesSchema(coerced, branchNode) {
				return coerced, changed, nil
			}
		}
		return value, false, nil
	}

	switch typeName := schemaTypeName(node); typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return value, false, nil
		}
		properties, _ := node["properties"].(map[string]any)
		changed := false
		for name, child := range object {
			propertyNode, ok := properties[name].(map[string]any)
			if !ok {
				continue
			}
			coerced, childChanged, err := coerceNode(child, propertyNode)
			if err != nil {
				return nil, false, fmt.Errorf("字段 %q: %w", name, err)
			}
			if childChanged {
				object[name] = coerced
				changed = true
			}
		}
		return object, changed, nil
	case "array":
		array, ok := value.([]any)
		if !ok {
			return value, false, nil
		}
		items, _ := node["items"].(map[string]any)
		changed := false
		for i := range array {
			coerced, itemChanged, err := coerceNode(array[i], items)
			if err != nil {
				return nil, false, fmt.Errorf("数组元素 %d: %w", i, err)
			}
			if itemChanged {
				array[i] = coerced
				changed = true
			}
		}
		return array, changed, nil
	case "integer":
		if text, ok := value.(string); ok {
			if number, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil && number == float64(int64(number)) {
				return number, true, nil
			}
		}
	case "number":
		if text, ok := value.(string); ok {
			if number, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
				return number, true, nil
			}
		}
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(mustString(value))) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		}
	case "string":
		switch typed := value.(type) {
		case float64:
			if typed == float64(int64(typed)) {
				return strconv.FormatInt(int64(typed), 10), true, nil
			}
			return strconv.FormatFloat(typed, 'f', -1, 64), true, nil
		case bool:
			return strconv.FormatBool(typed), true, nil
		}
	}
	return value, false, nil
}

func mustString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func schemaTypeName(node map[string]any) string {
	if name, ok := node["type"].(string); ok {
		return name
	}
	return ""
}

// typeMatchesSchema reports whether the Go value's dynamic type is compatible
// with the schema's declared type; used to pick an anyOf branch.
func typeMatchesSchema(value any, node map[string]any) bool {
	switch schemaTypeName(node) {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "number":
		_, ok := value.(float64)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	default:
		// Unknown or multi-type nodes stay compatible by default so the
		// validator decides.
		return true
	}
}
