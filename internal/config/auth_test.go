package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAuthJSONRoundTripUnset(t *testing.T) {
	var auth Auth
	if err := json.Unmarshal([]byte(`{"env":["PAW_TEST_KEY"]}`), &auth); err != nil {
		t.Fatal(err)
	}
	if auth.CredentialSet || auth.Credential != "" {
		t.Fatalf("unset credential should stay unset, got set=%v value=%q", auth.CredentialSet, auth.Credential)
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["credential"]; present {
		t.Fatalf("unset credential must be omitted on marshal, got %s", raw)
	}
	if !reflect.DeepEqual(fields["env"], []any{"PAW_TEST_KEY"}) {
		t.Fatalf("env lost in round trip: %s", raw)
	}
}

func TestAuthJSONRoundTripExplicitEmptyClearsCredential(t *testing.T) {
	var auth Auth
	if err := json.Unmarshal([]byte(`{"credential":""}`), &auth); err != nil {
		t.Fatal(err)
	}
	if !auth.CredentialSet || auth.Credential != "" {
		t.Fatalf("explicit empty credential must be recorded, got set=%v value=%q", auth.CredentialSet, auth.Credential)
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"credential":""}` {
		t.Fatalf("explicit empty credential must round-trip, got %s", raw)
	}
}

func TestAuthJSONRoundTripValue(t *testing.T) {
	input := `{"credential":"provider/deepseek","env":["DEEPSEEK_API_KEY"]}`
	var auth Auth
	if err := json.Unmarshal([]byte(input), &auth); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Auth
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !roundTrip.CredentialSet || roundTrip.Credential != "provider/deepseek" || !reflect.DeepEqual(roundTrip.Env, []string{"DEEPSEEK_API_KEY"}) {
		t.Fatalf("round trip mismatch: %s", raw)
	}
}

func TestAuthMarshalEmitsProgrammaticCredential(t *testing.T) {
	// Auth values built in code (not parsed from JSON) must still serialize
	// their credential so config writes do not silently drop it.
	auth := Auth{Credential: "provider/code", Env: []string{"PAW_TEST_KEY"}}
	raw, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["credential"] != "provider/code" {
		t.Fatalf("programmatic credential dropped on marshal: %s", raw)
	}
}

func TestMergePresetCredentialInheritsWhenUnset(t *testing.T) {
	var provider Provider
	if err := json.Unmarshal([]byte(`{"preset":"deepseek"}`), &provider); err != nil {
		t.Fatal(err)
	}
	merged := mergePreset("deepseek", provider)
	if merged.Auth.Credential != "provider/deepseek" {
		t.Fatalf("credential = %q, want preset provider/deepseek", merged.Auth.Credential)
	}
	if !reflect.DeepEqual(merged.Auth.Env, []string{"DEEPSEEK_API_KEY"}) {
		t.Fatalf("preset env lost: %#v", merged.Auth.Env)
	}
}

func TestMergePresetCredentialExplicitEmptyClears(t *testing.T) {
	var provider Provider
	if err := json.Unmarshal([]byte(`{"preset":"deepseek","auth":{"credential":""}}`), &provider); err != nil {
		t.Fatal(err)
	}
	merged := mergePreset("deepseek", provider)
	if merged.Auth.Credential != "" {
		t.Fatalf("explicit empty credential should clear preset reference, got %q", merged.Auth.Credential)
	}
	// Env is untouched by the clearing and remains the preset's fallback.
	if !reflect.DeepEqual(merged.Auth.Env, []string{"DEEPSEEK_API_KEY"}) {
		t.Fatalf("preset env lost: %#v", merged.Auth.Env)
	}
}

func TestMergePresetCredentialOverrides(t *testing.T) {
	var provider Provider
	if err := json.Unmarshal([]byte(`{"preset":"deepseek","auth":{"credential":"provider/other"}}`), &provider); err != nil {
		t.Fatal(err)
	}
	merged := mergePreset("deepseek", provider)
	if merged.Auth.Credential != "provider/other" {
		t.Fatalf("credential = %q, want provider/other", merged.Auth.Credential)
	}
}

func TestMergePresetCredentialProgrammaticOverride(t *testing.T) {
	// Auth built in code (CredentialSet=false) with a non-empty value still
	// overrides the preset, matching pre-existing behavior.
	provider := Provider{Preset: "deepseek", Auth: Auth{Credential: "provider/code"}}
	merged := mergePreset("deepseek", provider)
	if merged.Auth.Credential != "provider/code" {
		t.Fatalf("credential = %q, want provider/code", merged.Auth.Credential)
	}
}
