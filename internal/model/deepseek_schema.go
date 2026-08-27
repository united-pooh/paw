package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	deepSeekPlaceholderProperty = "__paw_empty"
	deepSeekEnvelopeProperty    = "__paw_arguments_json"
	deepSeekSchemaSummaryLimit  = 1536
)

type argumentProjectionKind uint8

const (
	projectionIdentity argumentProjectionKind = iota
	projectionObject
	projectionArray
	projectionJSONCodec
	projectionEnvelope
)

type argumentProjection struct {
	kind       argumentProjectionKind
	properties map[string]argumentPropertyProjection
	item       *argumentProjection
	envelope   string
}

type argumentPropertyProjection struct {
	projection *argumentProjection
	optional   bool
	allowsNull bool
}

func identityProjection() *argumentProjection {
	return &argumentProjection{kind: projectionIdentity}
}

func (projection *argumentProjection) isIdentity() bool {
	return projection == nil || projection.kind == projectionIdentity
}

func (projection *argumentProjection) restore(value any) (any, error) {
	if projection == nil {
		return value, nil
	}
	switch projection.kind {
	case projectionIdentity:
		return value, nil
	case projectionJSONCodec:
		encoded, ok := value.(string)
		if !ok {
			// DeepSeek Responses currently treats strict as best effort. Accept a
			// native value when the model already returned the original shape.
			return value, nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
			return nil, fmt.Errorf("JSON codec 字段不是有效的 JSON 字符串: %w", err)
		}
		return decoded, nil
	case projectionEnvelope:
		object, ok := value.(map[string]any)
		if ok {
			if encoded, exists := object[projection.envelope]; exists {
				return projection.item.restore(encoded)
			}
		}
		// Also accept a native root value because Responses does not currently
		// enforce the strict envelope on every compatible endpoint.
		return projection.item.restore(value)
	case projectionArray:
		array, ok := value.([]any)
		if !ok {
			return value, nil
		}
		for i := range array {
			restored, err := projection.item.restore(array[i])
			if err != nil {
				return nil, fmt.Errorf("数组元素 %d: %w", i, err)
			}
			array[i] = restored
		}
		return array, nil
	case projectionObject:
		object, ok := value.(map[string]any)
		if !ok {
			return value, nil
		}
		delete(object, deepSeekPlaceholderProperty)
		for name, property := range projection.properties {
			current, exists := object[name]
			if !exists {
				continue
			}
			if current == nil && property.optional && !property.allowsNull {
				delete(object, name)
				continue
			}
			restored, err := property.projection.restore(current)
			if err != nil {
				return nil, fmt.Errorf("字段 %q: %w", name, err)
			}
			object[name] = restored
		}
		return object, nil
	default:
		return nil, fmt.Errorf("未知参数投影类型 %d", projection.kind)
	}
}

type deepSeekSchemaCompiler struct {
	toolName string
	root     map[string]any
	refs     map[string]bool
}

func prepareDeepSeekTools(tools []ToolDefinition) (PreparedToolSet, error) {
	prepared := make(PreparedToolSet, 0, len(tools))
	for _, tool := range tools {
		compiled, err := compileDeepSeekToolSchema(tool.Name, tool.InputSchema)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, PreparedTool{
			Name: tool.Name, Description: tool.Description,
			Parameters: compiled.wire, Strict: true,
			OriginalSchema: compiled.original,
			projection:     compiled.projection,
			validator:      compiled.validator,
			schema:         compiled.schema,
		})
	}
	return prepared, nil
}

type compiledDeepSeekSchema struct {
	wire       json.RawMessage
	original   json.RawMessage
	projection *argumentProjection
	validator  *jsonschema.Schema
	schema     map[string]any
}

func compileDeepSeekToolSchema(toolName string, raw json.RawMessage) (compiledDeepSeekSchema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return compiledDeepSeekSchema{}, fmt.Errorf("DeepSeek 工具 %q Schema $: 非法 JSON: %w", toolName, err)
	}
	root, ok := decoded.(map[string]any)
	if !ok || root == nil {
		return compiledDeepSeekSchema{}, fmt.Errorf("DeepSeek 工具 %q Schema $: 必须是 JSON object", toolName)
	}
	originalData, err := json.Marshal(root)
	if err != nil {
		return compiledDeepSeekSchema{}, fmt.Errorf("DeepSeek 工具 %q Schema $: 序列化失败: %w", toolName, err)
	}
	compiler := deepSeekSchemaCompiler{toolName: toolName, root: root, refs: make(map[string]bool)}
	wire, projection, err := compiler.compileNode(root, "$")
	if err != nil {
		return compiledDeepSeekSchema{}, err
	}
	if wire["type"] != "object" {
		envelope := availableEnvelopeProperty(root)
		wire = map[string]any{
			"type": "object",
			"properties": map[string]any{
				envelope: codecWireSchema(root),
			},
			"required":             []any{envelope},
			"additionalProperties": false,
		}
		projection = &argumentProjection{kind: projectionEnvelope, envelope: envelope, item: &argumentProjection{kind: projectionJSONCodec}}
	}
	wireData, err := json.Marshal(wire)
	if err != nil {
		return compiledDeepSeekSchema{}, fmt.Errorf("DeepSeek 工具 %q Schema $: wire Schema 序列化失败: %w", toolName, err)
	}
	return compiledDeepSeekSchema{
		wire: json.RawMessage(wireData), original: json.RawMessage(originalData),
		projection: projection, validator: compileOriginalSchemaValidator(root), schema: root,
	}, nil
}

// normalizeDeepSeekToolSchema remains as a small compatibility helper for
// focused schema tests and callers that only need the wire representation.
func normalizeDeepSeekToolSchema(toolName string, raw json.RawMessage) (json.RawMessage, error) {
	compiled, err := compileDeepSeekToolSchema(toolName, raw)
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), compiled.wire...), nil
}

func (compiler *deepSeekSchemaCompiler) compileNode(node map[string]any, path string) (map[string]any, *argumentProjection, error) {
	if ref, ok := node["$ref"].(string); ok && strings.TrimSpace(ref) != "" {
		resolved, ok := resolveLocalSchemaRef(compiler.root, ref)
		if !ok || compiler.refs[ref] {
			return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
		}
		compiler.refs[ref] = true
		wire, projection, err := compiler.compileNode(resolved, path+".$ref("+ref+")")
		delete(compiler.refs, ref)
		return wire, projection, err
	}
	if hasStructuralCodecKeyword(node) {
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
	if branches, ok := node["anyOf"].([]any); ok {
		return compiler.compileAnyOf(node, branches, path)
	}

	typeName, ok := node["type"].(string)
	if !ok {
		if values, isTypes := node["type"].([]any); isTypes {
			branches := make([]any, 0, len(values))
			for _, value := range values {
				name, isString := value.(string)
				if !isString {
					return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
				}
				branch := schemaCopy(node)
				branch["type"] = name
				branches = append(branches, branch)
			}
			return compiler.compileAnyOf(node, branches, path)
		}
		if inferred := inferEnumType(node["enum"]); inferred != "" {
			typeName = inferred
		} else {
			return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
		}
	}

	switch typeName {
	case "object":
		return compiler.compileObject(node, path)
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
		}
		itemWire, itemProjection, err := compiler.compileNode(items, path+".items")
		if err != nil {
			return nil, nil, err
		}
		wire := scalarWireSchema(node, "array")
		wire["items"] = itemWire
		if itemProjection.isIdentity() {
			return wire, identityProjection(), nil
		}
		return wire, &argumentProjection{kind: projectionArray, item: itemProjection}, nil
	case "string", "number", "integer", "boolean", "null":
		return scalarWireSchema(node, typeName), identityProjection(), nil
	default:
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
}

func (compiler *deepSeekSchemaCompiler) compileObject(node map[string]any, path string) (map[string]any, *argumentProjection, error) {
	propertiesValue, exists := node["properties"]
	if !exists {
		// An object without a properties declaration is a free-form map.
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
	properties, ok := propertiesValue.(map[string]any)
	if !ok {
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
	if additional, exists := node["additionalProperties"]; exists && additional != false {
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
	required := stringSet(node["required"])
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	wireProperties := make(map[string]any, len(properties)+1)
	projectionProperties := make(map[string]argumentPropertyProjection, len(properties))
	projectionNeeded := false
	for _, name := range names {
		child, ok := properties[name].(map[string]any)
		if !ok {
			childWire := codecWireSchema(properties[name])
			optional := !required[name]
			if optional {
				childWire = map[string]any{"anyOf": []any{childWire, map[string]any{"type": "null"}}}
			}
			wireProperties[name] = childWire
			projectionProperties[name] = argumentPropertyProjection{projection: &argumentProjection{kind: projectionJSONCodec}, optional: optional}
			projectionNeeded = true
			continue
		}
		childWire, childProjection, err := compiler.compileNode(child, path+".properties["+name+"]")
		if err != nil {
			return nil, nil, err
		}
		optional := !required[name]
		if optional {
			childWire = map[string]any{"anyOf": []any{childWire, map[string]any{"type": "null"}}}
			projectionNeeded = true
		}
		if !childProjection.isIdentity() {
			projectionNeeded = true
		}
		wireProperties[name] = childWire
		projectionProperties[name] = argumentPropertyProjection{
			projection: childProjection, optional: optional, allowsNull: schemaAllowsNull(child),
		}
	}
	if len(names) == 0 {
		wireProperties[deepSeekPlaceholderProperty] = map[string]any{
			"type": "boolean", "description": "Internal placeholder; set to true.",
		}
		names = append(names, deepSeekPlaceholderProperty)
		projectionNeeded = true
	}
	wire := map[string]any{
		"type": "object", "properties": wireProperties,
		"required": stringsToAny(names), "additionalProperties": false,
	}
	copyDescription(node, wire)
	if !projectionNeeded {
		return wire, identityProjection(), nil
	}
	return wire, &argumentProjection{kind: projectionObject, properties: projectionProperties}, nil
}

func (compiler *deepSeekSchemaCompiler) compileAnyOf(node map[string]any, branches []any, path string) (map[string]any, *argumentProjection, error) {
	if len(branches) == 0 {
		return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
	}
	wireBranches := make([]any, 0, len(branches))
	for index, branch := range branches {
		child, ok := branch.(map[string]any)
		if !ok {
			return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
		}
		wire, projection, err := compiler.compileNode(child, fmt.Sprintf("%s.anyOf[%d]", path, index))
		if err != nil {
			return nil, nil, err
		}
		if !projection.isIdentity() {
			// Branch selection cannot be recovered unambiguously. Encode only
			// this anyOf subtree instead of hiding the entire tool.
			return codecWireSchema(node), &argumentProjection{kind: projectionJSONCodec}, nil
		}
		wireBranches = append(wireBranches, wire)
	}
	wire := map[string]any{"anyOf": wireBranches}
	copyDescription(node, wire)
	return wire, identityProjection(), nil
}

func scalarWireSchema(node map[string]any, typeName string) map[string]any {
	wire := map[string]any{"type": typeName}
	copyDescription(node, wire)
	if enum, ok := node["enum"].([]any); ok && len(enum) != 0 {
		wire["enum"] = enum
	}
	return wire
}

func codecWireSchema(schema any) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "JSON-encoded value. Original schema: " + deterministicSchemaSummary(schema),
	}
}

func deterministicSchemaSummary(schema any) string {
	data, err := json.Marshal(schema)
	if err != nil {
		return "unavailable"
	}
	if len(data) <= deepSeekSchemaSummaryLimit {
		return string(data)
	}
	data = data[:deepSeekSchemaSummaryLimit]
	for !utf8.Valid(data) && len(data) > 0 {
		data = data[:len(data)-1]
	}
	return string(data) + "..."
}

func availableEnvelopeProperty(root map[string]any) string {
	properties, _ := root["properties"].(map[string]any)
	name := deepSeekEnvelopeProperty
	for suffix := 2; ; suffix++ {
		if _, exists := properties[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s_%d", deepSeekEnvelopeProperty, suffix)
	}
}

func hasStructuralCodecKeyword(node map[string]any) bool {
	for _, key := range []string{
		"oneOf", "allOf", "patternProperties", "if", "then", "else",
		"dependentSchemas", "dependencies", "unevaluatedProperties", "$dynamicRef",
	} {
		if _, exists := node[key]; exists {
			return true
		}
	}
	return false
}

func schemaAllowsNull(node map[string]any) bool {
	if node["type"] == "null" {
		return true
	}
	if values, ok := node["type"].([]any); ok {
		for _, value := range values {
			if value == "null" {
				return true
			}
		}
	}
	if enum, ok := node["enum"].([]any); ok {
		for _, value := range enum {
			if value == nil {
				return true
			}
		}
	}
	if branches, ok := node["anyOf"].([]any); ok {
		for _, branch := range branches {
			if child, ok := branch.(map[string]any); ok && schemaAllowsNull(child) {
				return true
			}
		}
	}
	return false
}

func inferEnumType(value any) string {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return ""
	}
	typeName := ""
	for _, value := range values {
		current := ""
		switch value.(type) {
		case string:
			current = "string"
		case bool:
			current = "boolean"
		case float64:
			current = "number"
		case nil:
			current = "null"
		}
		if current == "" || (typeName != "" && current != typeName) {
			return ""
		}
		typeName = current
	}
	return typeName
}

func resolveLocalSchemaRef(root map[string]any, ref string) (map[string]any, bool) {
	if ref == "#" {
		return root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var value any = root
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	resolved, ok := value.(map[string]any)
	return resolved, ok
}

func stringSet(value any) map[string]bool {
	set := make(map[string]bool)
	if values, ok := value.([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok {
				set[text] = true
			}
		}
	}
	return set
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func copyDescription(source, target map[string]any) {
	if description, ok := source["description"].(string); ok && strings.TrimSpace(description) != "" {
		target["description"] = boundedUTF8(description, deepSeekSchemaSummaryLimit)
	}
}

func boundedUTF8(value string, limit int) string {
	if limit < 1 || len(value) <= limit {
		return value
	}
	data := []byte(value)[:limit]
	for !utf8.Valid(data) && len(data) > 0 {
		data = data[:len(data)-1]
	}
	return string(data) + "..."
}

func schemaCopy(schema map[string]any) map[string]any {
	data, _ := json.Marshal(schema)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

type blockedSchemaLoader struct{}

func (blockedSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("remote JSON Schema loading is disabled: %s", url)
}

func compileOriginalSchemaValidator(root map[string]any) *jsonschema.Schema {
	if containsExternalSchemaRef(root) || containsSchemaExtension(root) {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(blockedSchemaLoader{})
	const resource = "https://paw.invalid/tool-schema.json"
	if err := compiler.AddResource(resource, root); err != nil {
		return nil
	}
	validator, err := compiler.Compile(resource)
	if err != nil {
		// Non-standard or unsupported schemas remain callable. The MCP server
		// remains the final authority instead of losing the whole catalog.
		return nil
	}
	return validator
}

func containsExternalSchemaRef(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "$ref" || key == "$dynamicRef" {
				if ref, ok := child.(string); ok && ref != "" && !strings.HasPrefix(ref, "#") {
					return true
				}
			}
			if containsExternalSchemaRef(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsExternalSchemaRef(child) {
				return true
			}
		}
	}
	return false
}

func containsSchemaExtension(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if strings.HasPrefix(strings.ToLower(key), "x-") || containsSchemaExtension(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsSchemaExtension(child) {
				return true
			}
		}
	}
	return false
}
