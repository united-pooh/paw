package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMergeRequestBodiesDeepMergesWithoutMutatingInputs(t *testing.T) {
	base := RequestBody{
		"metadata": map[string]any{"team": "platform", "environment": "production"},
		"tags":     []any{"profile"},
	}
	override := RequestBody{
		"metadata": map[string]any{"environment": "development", "feature": "agent"},
		"tags":     []any{"model"},
		"optional": nil,
	}
	got := MergeRequestBodies(base, override)
	want := RequestBody{
		"metadata": RequestBody{"team": "platform", "environment": "development", "feature": "agent"},
		"tags":     []any{"model"},
		"optional": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeRequestBodies() = %#v, want %#v", got, want)
	}
	got["metadata"].(RequestBody)["team"] = "changed"
	got["tags"].([]any)[0] = "changed"
	if base["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("base was mutated: %#v", base)
	}
	if override["tags"].([]any)[0] != "model" {
		t.Fatalf("override was mutated: %#v", override)
	}
}

func TestMergeRequestBodiesReplacesMismatchedTypes(t *testing.T) {
	got := MergeRequestBodies(RequestBody{"value": map[string]any{"nested": true}}, RequestBody{"value": "replacement"})
	if got["value"] != "replacement" {
		t.Fatalf("value = %#v, want replacement", got["value"])
	}
}

func TestEffectiveExtraRequestBodyMergesProfileAndCurrentModel(t *testing.T) {
	cfg := Config{
		Model: "model-a",
		ExtraBody: RequestBody{
			"metadata": map[string]any{
				"team":        "platform",
				"environment": "production",
			},
			"scalar": "profile",
			"tags":   []any{"profile"},
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {
				"metadata": map[string]any{
					"environment": "development",
					"feature":     "agent",
				},
				"scalar":   "model",
				"tags":     []any{"model"},
				"optional": nil,
			},
			"model-b": {"ignored": true},
		},
	}

	got := EffectiveExtraRequestBody(cfg)
	want := RequestBody{
		"metadata": RequestBody{
			"team":        "platform",
			"environment": "development",
			"feature":     "agent",
		},
		"scalar":   "model",
		"tags":     []any{"model"},
		"optional": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveExtraRequestBody() = %#v, want %#v", got, want)
	}

	got["metadata"].(RequestBody)["team"] = "changed"
	got["tags"].([]any)[0] = "changed"
	if cfg.ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("profile extraBody was mutated: %#v", cfg.ExtraBody)
	}
	if cfg.ModelExtraBody["model-a"]["tags"].([]any)[0] != "model" {
		t.Fatalf("model extraBody was mutated: %#v", cfg.ModelExtraBody)
	}
}

func TestCloneRequestBodyPreservesJSONNullAndClonesTypedContainers(t *testing.T) {
	metadata := map[string]string{"team": "platform"}
	tags := []string{"profile"}
	var nilObject map[string]string
	var nilArray []string
	body := RequestBody{
		"metadata": metadata,
		"tags":     tags,
		"object":   nilObject,
		"array":    nilArray,
	}

	cloned := CloneRequestBody(body)
	cloned["metadata"].(map[string]string)["team"] = "changed"
	cloned["tags"].([]string)[0] = "changed"
	if metadata["team"] != "platform" || tags[0] != "profile" {
		t.Fatalf("CloneRequestBody() did not isolate nested typed containers: %#v", body)
	}
	if value := cloned["object"]; reflect.ValueOf(value).Kind() != reflect.Map || !reflect.ValueOf(value).IsNil() {
		t.Fatalf("cloned nil object = %#v, want typed nil map", value)
	}
	if value := cloned["array"]; reflect.ValueOf(value).Kind() != reflect.Slice || !reflect.ValueOf(value).IsNil() {
		t.Fatalf("cloned nil array = %#v, want typed nil slice", value)
	}
}

func TestMarshalRequestBodyPreservesExplicitNull(t *testing.T) {
	data, err := MarshalRequestBody(struct {
		Model string `json:"model"`
	}{Model: "gpt-5.6-sol"}, RequestBody{"optional": nil})
	if err != nil {
		t.Fatalf("MarshalRequestBody() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if value, ok := got["optional"]; !ok || value != nil {
		t.Fatalf("optional = %#v, present=%v; want explicit null", value, ok)
	}
}

func TestMarshalRequestBodyRejectsNonObjectBase(t *testing.T) {
	if _, err := MarshalRequestBody(nil, nil); err == nil || !strings.Contains(err.Error(), "expected a JSON object") {
		t.Fatalf("MarshalRequestBody(nil) error = %v, want JSON object error", err)
	}
}

func TestValidateExtraRequestBodiesRejectsProtectedFields(t *testing.T) {
	tests := []struct {
		transport string
		fields    []string
	}{
		{"openai-compatible", []string{"model", "messages", "stream", "stream_options"}},
		{"anthropic", []string{"model", "system", "messages", "tools", "stream", "stream_options"}},
	}
	for _, tt := range tests {
		for _, field := range tt.fields {
			t.Run(tt.transport+"/extraBody/"+field, func(t *testing.T) {
				err := ValidateExtraRequestBodies(Config{
					ProfileID: "gateway", Transport: tt.transport, Model: "model-a", Models: []string{"model-a"},
					ExtraBody: RequestBody{field: "override"},
				})
				want := `extraBody contains protected field "` + field + `"`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want substring %q", err, want)
				}
			})
			t.Run(tt.transport+"/modelExtraBody/"+field, func(t *testing.T) {
				err := ValidateExtraRequestBodies(Config{
					ProfileID: "gateway", Transport: tt.transport, Model: "model-a", Models: []string{"model-a"},
					ModelExtraBody: map[string]RequestBody{"model-a": {field: "override"}},
				})
				want := `modelExtraBody["model-a"] contains protected field "` + field + `"`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want substring %q", err, want)
				}
			})
		}
	}
}

func TestValidateExtraRequestBodiesAllowsNestedProtectedNamesAndAnthropicMaxTokens(t *testing.T) {
	err := ValidateExtraRequestBodies(Config{
		ProfileID: "anthropic", Transport: "anthropic", Model: "claude", Models: []string{"claude"},
		ExtraBody: RequestBody{
			"max_tokens": 16384,
			"metadata": map[string]any{
				"model":          "label",
				"system":         "label",
				"messages":       "label",
				"tools":          "label",
				"stream":         "label",
				"stream_options": "label",
			},
		},
	})
	if err != nil {
		t.Fatalf("ValidateExtraRequestBodies() error = %v", err)
	}
}

func TestValidateExtraRequestBodiesRejectsUnknownModel(t *testing.T) {
	err := ValidateExtraRequestBodies(Config{
		ProfileID: "gateway", Transport: "openai-compatible", Model: "model-a", Models: []string{"model-a"},
		ModelExtraBody: map[string]RequestBody{"model-typo": {"service_tier": "fast"}},
	})
	if err == nil || !strings.Contains(err.Error(), `modelExtraBody references unknown model "model-typo"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateExtraRequestBodiesRejectsNilModelObject(t *testing.T) {
	err := ValidateExtraRequestBodies(Config{
		ProfileID: "gateway", Transport: "openai-compatible", Model: "model-a", Models: []string{"model-a"},
		ModelExtraBody: map[string]RequestBody{"model-a": nil},
	})
	want := `model profile "gateway": modelExtraBody["model-a"] must be a JSON object`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
