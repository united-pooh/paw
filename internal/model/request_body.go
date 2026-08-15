package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// RequestBody is an arbitrary JSON object merged into a provider request body.
type RequestBody map[string]any

func CloneRequestBody(body RequestBody) RequestBody {
	if body == nil {
		return nil
	}
	cloned := make(RequestBody, len(body))
	for key, value := range body {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func CloneModelExtraBodies(values map[string]RequestBody) map[string]RequestBody {
	if values == nil {
		return nil
	}
	cloned := make(map[string]RequestBody, len(values))
	for modelName, body := range values {
		cloned[modelName] = CloneRequestBody(body)
	}
	return cloned
}

func CloneConfig(cfg Config) Config {
	cfg.Models = append([]string(nil), cfg.Models...)
	cfg.Headers = cloneStringMap(cfg.Headers)
	cfg.Proxy = CloneProxyConfig(cfg.Proxy)
	cfg.ExtraBody = CloneRequestBody(cfg.ExtraBody)
	cfg.ModelExtraBody = CloneModelExtraBodies(cfg.ModelExtraBody)
	cfg.ModelContextLimitTokens = cloneModelContextLimits(cfg.ModelContextLimitTokens)
	cfg.Profiles = cloneProfiles(cfg.Profiles)
	return cfg
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(value)).Interface()
}

func cloneReflectValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneReflectValue(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result.SetMapIndex(cloneReflectValue(iter.Key()), cloneReflectValue(iter.Value()))
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflectValue(value.Index(index)))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(cloneReflectValue(value.Index(index)))
		}
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(cloneReflectValue(value.Elem()))
		return result
	default:
		return value
	}
}

func MergeRequestBodies(base, override RequestBody) RequestBody {
	merged := CloneRequestBody(base)
	if merged == nil {
		merged = make(RequestBody)
	}
	for key, overrideValue := range override {
		baseObject, baseOK := jsonObject(merged[key])
		overrideObject, overrideOK := jsonObject(overrideValue)
		if baseOK && overrideOK {
			merged[key] = MergeRequestBodies(baseObject, overrideObject)
			continue
		}
		merged[key] = cloneJSONValue(overrideValue)
	}
	return merged
}

func jsonObject(value any) (RequestBody, bool) {
	if value == nil {
		return nil, false
	}
	switch typed := value.(type) {
	case RequestBody:
		if typed == nil {
			return nil, false
		}
		return typed, true
	case map[string]any:
		if typed == nil {
			return nil, false
		}
		return RequestBody(typed), true
	}

	valueRef := reflect.ValueOf(value)
	if valueRef.Kind() != reflect.Map || valueRef.IsNil() || valueRef.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	object := make(RequestBody, valueRef.Len())
	iter := valueRef.MapRange()
	for iter.Next() {
		object[iter.Key().String()] = iter.Value().Interface()
	}
	return object, true
}

func MarshalRequestBody(base any, extra RequestBody) ([]byte, error) {
	baseData, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshal base request body: %w", err)
	}
	var baseObject RequestBody
	if err := json.Unmarshal(baseData, &baseObject); err != nil {
		return nil, fmt.Errorf("decode base request body object: %w", err)
	}
	if baseObject == nil {
		return nil, fmt.Errorf("decode base request body object: expected a JSON object")
	}
	merged := MergeRequestBodies(baseObject, extra)
	data, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged request body: %w", err)
	}
	return data, nil
}

func EffectiveExtraRequestBody(cfg Config) RequestBody {
	return MergeRequestBodies(cfg.ExtraBody, cfg.ModelExtraBody[strings.TrimSpace(cfg.Model)])
}

// FilterChatCompletionsExtraRequestBody removes fields that belong to the
// adapter-generated tool request. ExtraBody may still provide ordinary model
// parameters, but it cannot replace tools or strict function metadata.
func FilterChatCompletionsExtraRequestBody(body RequestBody) RequestBody {
	if body == nil {
		return nil
	}
	filtered := CloneRequestBody(body)
	for _, field := range []string{"tools", "tool_choice", "function", "strict", "parameters"} {
		delete(filtered, field)
	}
	return filtered
}

func EffectiveChatCompletionsExtraRequestBody(cfg Config) RequestBody {
	return FilterChatCompletionsExtraRequestBody(EffectiveExtraRequestBody(cfg))
}

var openAIProtectedRequestFields = map[string]struct{}{
	"model": {}, "messages": {}, "tools": {}, "stream": {}, "stream_options": {},
}

var responsesProtectedRequestFields = map[string]struct{}{
	"model": {}, "input": {}, "tools": {}, "stream": {},
}

var anthropicProtectedRequestFields = map[string]struct{}{
	"model": {}, "system": {}, "messages": {}, "tools": {}, "stream": {}, "stream_options": {},
}

func ValidateExtraRequestBodies(cfg Config) error {
	profileID := strings.TrimSpace(cfg.ProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Provider)
	}
	if profileID == "" {
		profileID = "default"
	}
	protected := openAIProtectedRequestFields
	ignoreChatCompletionsToolFields := true
	if shouldUseResponsesAPI(cfg) {
		protected = responsesProtectedRequestFields
		ignoreChatCompletionsToolFields = false
	} else if isAnthropicTransport(cfg.Transport) {
		protected = anthropicProtectedRequestFields
		ignoreChatCompletionsToolFields = false
	}
	if err := validateProtectedRequestFields(profileID, "extraBody", cfg.ExtraBody, protected, ignoreChatCompletionsToolFields); err != nil {
		return err
	}
	knownModels := make(map[string]struct{})
	for _, modelName := range AvailableModels(cfg) {
		knownModels[modelName] = struct{}{}
	}
	for _, modelName := range sortedModelExtraBodyKeys(cfg.ModelExtraBody) {
		if _, ok := knownModels[modelName]; !ok {
			return fmt.Errorf("model profile %q: modelExtraBody references unknown model %q", profileID, modelName)
		}
		location := fmt.Sprintf("modelExtraBody[%q]", modelName)
		if cfg.ModelExtraBody[modelName] == nil {
			return fmt.Errorf("model profile %q: %s must be a JSON object", profileID, location)
		}
		if err := validateProtectedRequestFields(profileID, location, cfg.ModelExtraBody[modelName], protected, ignoreChatCompletionsToolFields); err != nil {
			return err
		}
	}
	return nil
}

func validateProtectedRequestFields(profileID, location string, body RequestBody, protected map[string]struct{}, ignoreChatCompletionsToolFields bool) error {
	for _, field := range sortedRequestBodyKeys(body) {
		if ignoreChatCompletionsToolFields && isIgnoredChatCompletionsToolField(field) {
			continue
		}
		if _, blocked := protected[field]; blocked {
			return fmt.Errorf("model profile %q: %s contains protected field %q", profileID, location, field)
		}
	}
	return nil
}

func isIgnoredChatCompletionsToolField(field string) bool {
	switch field {
	case "tools", "tool_choice", "function", "strict", "parameters":
		return true
	default:
		return false
	}
}

func sortedRequestBodyKeys(body RequestBody) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedModelExtraBodyKeys(values map[string]RequestBody) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isAnthropicTransport(transport string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(transport)), "anthropic")
}
