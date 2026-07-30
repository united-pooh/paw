package selecttool

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeInputNormalizesSingleSelection(t *testing.T) {
	got, err := decodeInput(json.RawMessage(`{"prompt":"  Choose environment  ","mode":"single","options":[{"id":"prod","label":" Production ","description":" Live traffic "},{"id":"stage","label":"Staging"}],"initial_selected_id":"stage"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Request{Prompt: "Choose environment", Mode: ModeSingle, Options: []Option{{ID: "prod", Label: "Production", Description: "Live traffic"}, {ID: "stage", Label: "Staging"}}, InitialSelectedIDs: []string{"stage"}, MinSelect: 1, MaxSelect: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestDecodeInputDefaultsMultipleBounds(t *testing.T) {
	got, err := decodeInput(json.RawMessage(`{"prompt":"Choose signals","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.MinSelect != 0 || got.MaxSelect != 2 {
		t.Fatalf("bounds=%d..%d", got.MinSelect, got.MaxSelect)
	}
}

func TestRequestClone(t *testing.T) {
	r := Request{Options: []Option{{ID: "a"}}, InitialSelectedIDs: []string{"a"}}
	c := r.Clone()
	c.Options[0].ID = "b"
	c.InitialSelectedIDs[0] = "b"
	if r.Options[0].ID != "a" || r.InitialSelectedIDs[0] != "a" {
		t.Fatal("clone shares slices")
	}
}

func TestDecodeInputRejectsInvalidRequests(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"empty prompt", `{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}`, "prompt is required"},
		{"bad mode", `{"prompt":"Pick","mode":"other","options":[{"id":"a","label":"A"}]}`, `mode must be "single" or "multiple"`},
		{"no options", `{"prompt":"Pick","mode":"single","options":[]}`, "options must contain at least one option"},
		{"empty id", `{"prompt":"Pick","mode":"single","options":[{"id":" ","label":"A"}]}`, "options[0].id is required"},
		{"empty label", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":" "}]}`, "options[0].label is required"},
		{"duplicate id", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"},{"id":"a","label":"Again"}]}`, "duplicate option id: a"},
		{"single initial ids", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"initial_selected_ids":["a"]}`, "initial_selected_ids is only valid in multiple mode"},
		{"single min", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"min_select":0}`, "min_select is only valid in multiple mode"},
		{"single max", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"max_select":1}`, "max_select is only valid in multiple mode"},
		{"multiple initial", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_id":"a"}`, "initial_selected_id is only valid in single mode"},
		{"unknown single", `{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"initial_selected_id":"missing"}`, "initial_selected_id references unknown option id: missing"},
		{"unknown multiple", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_ids":["missing"]}`, "initial_selected_ids references unknown option id: missing"},
		{"duplicate selected", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_ids":["a","a"]}`, "duplicate initial selected id: a"},
		{"min low", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"min_select":-1}`, "min_select must be between 0 and 1"},
		{"max high", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"max_select":2}`, "max_select must be between 0 and 1"},
		{"bounds", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"min_select":1,"max_select":0}`, "min_select must not exceed max_select"},
		{"initial above max", `{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"initial_selected_ids":["a","b"],"max_select":1}`, "initial selection count 2 exceeds max_select 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeInput(json.RawMessage(tt.raw))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error=%v want=%q", err, tt.want)
			}
		})
	}
}
