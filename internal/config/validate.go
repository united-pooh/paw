package config

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"paw/internal/model"
)

//go:embed schema/config-v2.schema.json
var schemaFiles embed.FS

var compiledConfigSchema = mustCompileConfigSchema()

func mustCompileConfigSchema() *jsonschema.Schema {
	raw, err := schemaFiles.ReadFile("schema/config-v2.schema.json")
	if err != nil {
		panic(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		panic(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const resource = "https://paw.invalid/schemas/config-v2.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		panic(err)
	}
	result, err := compiler.Compile(resource)
	if err != nil {
		panic(err)
	}
	return result
}

func SchemaBytes() []byte {
	raw, _ := schemaFiles.ReadFile("schema/config-v2.schema.json")
	return append([]byte(nil), raw...)
}

func parseAndValidateGlobal(raw []byte, path string) (Document, []Diagnostic, error) {
	normalized, err := normalizeJSONC(raw)
	if err != nil {
		return Document{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return Document{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Document{}, nil, fmt.Errorf("%s: multiple JSON values are not allowed", path)
	} else if err != io.EOF {
		return Document{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := compiledConfigSchema.Validate(generic); err != nil {
		return Document{}, nil, fmt.Errorf("%s: schema: %w", path, err)
	}
	var document Document
	if err := json.Unmarshal(normalized, &document); err != nil {
		return Document{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	diagnostics, err := validateDocument(document, path)
	return document, diagnostics, err
}

func validateDocument(document Document, path string) ([]Diagnostic, error) {
	if document.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: schemaVersion must be %d", path, SchemaVersion)
	}
	for id, provider := range document.Providers {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%s: provider ID cannot be empty", path)
		}
		resolved := mergePreset(id, provider)
		if resolved.Discovery != nil {
			if err := validateDiscoveryConfig(id, *resolved.Discovery); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		switch resolved.Transport {
		case TransportOpenAIResponses, TransportOpenAICompatible, TransportAnthropicCompatible:
		default:
			return nil, fmt.Errorf("%s: providers.%s.transport is unsupported", path, id)
		}
		endpoint, err := url.Parse(resolved.Endpoint)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
			return nil, fmt.Errorf("%s: providers.%s.endpoint must be an http(s) URL", path, id)
		}
		for name := range resolved.Headers {
			lower := strings.ToLower(strings.TrimSpace(name))
			if lower == "authorization" || lower == "proxy-authorization" || lower == "x-api-key" {
				return nil, fmt.Errorf("%s: providers.%s.headers.%s is protected; configure auth instead", path, id, name)
			}
		}
		if field := protectedBodyField(resolved.Body, ""); field != "" {
			return nil, fmt.Errorf("%s: providers.%s.body.%s is protected; configure auth instead", path, id, field)
		}
		if err := model.ValidateExtraRequestBodies(model.Config{ProfileID: id, Transport: resolved.Transport, Model: "__validation__", Models: []string{"__validation__"}, ExtraBody: model.RequestBody(cloneAnyMap(resolved.Body))}); err != nil {
			return nil, fmt.Errorf("%s: providers.%s.body: %w", path, id, err)
		}
	}
	for id, configuredModel := range document.Models {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%s: model ID cannot be empty", path)
		}
		if _, ok := document.Providers[configuredModel.Provider]; !ok {
			return nil, fmt.Errorf("%s: models.%s.provider references missing provider %q", path, id, configuredModel.Provider)
		}
		if strings.TrimSpace(configuredModel.Name) == "" {
			return nil, fmt.Errorf("%s: models.%s.name cannot be empty", path, id)
		}
		resolved := mergePreset(configuredModel.Provider, document.Providers[configuredModel.Provider])
		if configuredModel.Parameters != nil {
			if err := model.ValidateExtraRequestBodies(model.Config{ProfileID: configuredModel.Provider, Transport: resolved.Transport, Model: configuredModel.Name, Models: []string{configuredModel.Name}, ModelExtraBody: map[string]model.RequestBody{configuredModel.Name: model.RequestBody(cloneAnyMap(configuredModel.Parameters))}}); err != nil {
				return nil, fmt.Errorf("%s: models.%s.parameters: %w", path, id, err)
			}
		}
	}
	if document.ActiveModel != "" {
		if _, ok := document.Models[document.ActiveModel]; !ok {
			return nil, fmt.Errorf("%s: activeModel references missing model %q", path, document.ActiveModel)
		}
	}
	return nil, nil
}

func validateDiscoveryConfig(providerID string, cfg DiscoveryConfig) error {
	if cfg.Format != "" && cfg.Format != DiscoveryFormatOpenAIList && cfg.Format != DiscoveryFormatOllamaTags {
		return fmt.Errorf("providers.%s.discovery.format is unsupported", providerID)
	}
	if cfg.TimeoutSeconds < 0 || cfg.TimeoutSeconds > 10 {
		return fmt.Errorf("providers.%s.discovery.timeoutSeconds must be between 1 and 10", providerID)
	}
	if err := validateDiscoveryPath(cfg.Path); err != nil {
		return fmt.Errorf("providers.%s.discovery.path: %w", providerID, err)
	}
	patterns := append(append([]string(nil), cfg.Include...), cfg.Exclude...)
	for _, pattern := range patterns {
		if err := validateModelGlob(pattern); err != nil {
			return fmt.Errorf("providers.%s.discovery pattern %q: %w", providerID, pattern, err)
		}
	}
	return nil
}

func validateDiscoveryPath(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid same-origin path: %w", err)
	}
	if parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || strings.HasPrefix(value, "//") {
		return fmt.Errorf("must be a same-origin path")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return fmt.Errorf("must not contain a query or fragment")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == ".." {
			return fmt.Errorf("must not contain a parent path segment")
		}
	}
	return nil
}

func protectedBodyField(value any, prefix string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := key
		if prefix != "" {
			field = prefix + "." + key
		}
		lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
		switch lower {
		case "api_key", "apikey", "authorization", "access_token", "secret", "password":
			return field
		}
		if nested := protectedBodyField(object[key], field); nested != "" {
			return nested
		}
	}
	return ""
}

func parseAndValidateWorkspace(raw []byte, path string, global Document) (WorkspaceDocument, error) {
	var generic map[string]any
	if err := decodeJSONC(raw, &generic); err != nil {
		return WorkspaceDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	for key := range generic {
		switch key {
		case "schemaVersion", "activeModel", "models":
		default:
			return WorkspaceDocument{}, fmt.Errorf("%s: project field %q is not allowed", path, key)
		}
	}
	if rawModels, ok := generic["models"].(map[string]any); ok {
		for id, rawValue := range rawModels {
			object, ok := rawValue.(map[string]any)
			if !ok {
				return WorkspaceDocument{}, fmt.Errorf("%s: models.%s must be an object", path, id)
			}
			for key := range object {
				if key != "stream" && key != "parameters" {
					return WorkspaceDocument{}, fmt.Errorf("%s: project models.%s.%s is not allowed", path, id, key)
				}
			}
		}
	}
	var workspace WorkspaceDocument
	if err := decodeJSONC(raw, &workspace); err != nil {
		return WorkspaceDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if workspace.SchemaVersion != 0 && workspace.SchemaVersion != SchemaVersion {
		return WorkspaceDocument{}, fmt.Errorf("%s: schemaVersion must be %d", path, SchemaVersion)
	}
	if workspace.ActiveModel != "" {
		if _, ok := global.Models[workspace.ActiveModel]; !ok {
			return WorkspaceDocument{}, fmt.Errorf("%s: activeModel references missing global model %q", path, workspace.ActiveModel)
		}
	}
	for id := range workspace.Models {
		if _, ok := global.Models[id]; !ok {
			return WorkspaceDocument{}, fmt.Errorf("%s: models.%s cannot override a missing global model", path, id)
		}
	}
	for id, override := range workspace.Models {
		if override.Parameters == nil {
			continue
		}
		configuredModel := global.Models[id]
		resolved := mergePreset(configuredModel.Provider, global.Providers[configuredModel.Provider])
		if err := model.ValidateExtraRequestBodies(model.Config{ProfileID: configuredModel.Provider, Transport: resolved.Transport, Model: configuredModel.Name, Models: []string{configuredModel.Name}, ModelExtraBody: map[string]model.RequestBody{configuredModel.Name: model.RequestBody(cloneAnyMap(override.Parameters))}}); err != nil {
			return WorkspaceDocument{}, fmt.Errorf("%s: models.%s.parameters: %w", path, id, err)
		}
	}
	return workspace, nil
}
