package selecttool

import (
	"encoding/json"
	"reflect"
	"testing"
)

func wrapQuestions(question string) string {
	return `{"questions":[` + question + `]}`
}

func TestDecodeInputNormalizesSingleSelection(t *testing.T) {
	got, err := decodeInput(json.RawMessage(wrapQuestions(`{"prompt":"  Choose environment  ","mode":"single","options":[{"id":"prod","label":" Production ","description":" Live traffic "},{"id":"stage","label":"Staging"}],"initial_selected_id":"stage"}`)))
	if err != nil {
		t.Fatal(err)
	}
	want := []Request{{Prompt: "Choose environment", Mode: ModeSingle, Options: []Option{{ID: "prod", Label: "Production", Description: "Live traffic"}, {ID: "stage", Label: "Staging"}}, InitialSelectedIDs: []string{"stage"}, MinSelect: 1, MaxSelect: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestDecodeInputDefaultsMultipleBounds(t *testing.T) {
	got, err := decodeInput(json.RawMessage(wrapQuestions(`{"prompt":"Choose signals","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].MinSelect != 0 || got[0].MaxSelect != 2 {
		t.Fatalf("bounds=%d..%d", got[0].MinSelect, got[0].MaxSelect)
	}
}

func TestDecodeInputBatchPreservesOrder(t *testing.T) {
	got, err := decodeInput(json.RawMessage(`{"questions":[{"prompt":"First","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":"Second","mode":"multiple","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Prompt != "First" || got[1].Prompt != "Second" || got[1].MaxSelect != 2 {
		t.Fatalf("got %#v", got)
	}
}

func TestDecodeInputAcceptsSerializedOptionalDefaults(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "single with empty multiple defaults",
			raw:  wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"initial_selected_id":"a","initial_selected_ids":[],"min_select":1,"max_select":1}`),
			want: []string{"a"},
		},
		{
			name: "multiple with empty single default",
			raw:  wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_id":"","initial_selected_ids":[],"min_select":0,"max_select":1}`),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeInput(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got[0].InitialSelectedIDs, tt.want) {
				t.Fatalf("selected=%v want=%v", got[0].InitialSelectedIDs, tt.want)
			}
		})
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

func TestResultCloneCopiesSelectedOptions(t *testing.T) {
	original := Result{SelectedOptions: []SelectedOption{{ID: "logs", Label: "Logs"}}}
	cloned := original.Clone()
	cloned.SelectedOptions[0].Label = "Changed"
	if original.SelectedOptions[0].Label != "Logs" {
		t.Fatalf("clone shares selected options: %#v", original)
	}
}

func TestBatchResultCloneCopiesResults(t *testing.T) {
	original := BatchResult{Results: []Result{{SelectedOptions: []SelectedOption{{ID: "a", Label: "A"}}}}}
	cloned := original.Clone()
	cloned.Results[0].SelectedOptions[0].Label = "Changed"
	if original.Results[0].SelectedOptions[0].Label != "A" {
		t.Fatalf("clone shares results: %#v", original)
	}
}

func TestDecodeInputRejectsReservedCustomOptionID(t *testing.T) {
	_, err := decodeInput(json.RawMessage(wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"custom_option","label":"Other"}]}`)))
	if err == nil || err.Error() != `questions[0]: option id "custom_option" is reserved` {
		t.Fatalf("error=%v", err)
	}
}

func TestDecodeInputRejectsInvalidRequests(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"empty prompt", wrapQuestions(`{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}`), "questions[0]: prompt is required"},
		{"bad mode", wrapQuestions(`{"prompt":"Pick","mode":"other","options":[{"id":"a","label":"A"}]}`), `questions[0]: mode must be "single" or "multiple"`},
		{"no options", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[]}`), "questions[0]: options must contain at least one option"},
		{"empty id", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":" ","label":"A"}]}`), "questions[0]: options[0].id is required"},
		{"empty label", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":" "}]}`), "questions[0]: options[0].label is required"},
		{"duplicate id", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"},{"id":"a","label":"Again"}]}`), "questions[0]: duplicate option id: a"},
		{"single too many initial ids", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"initial_selected_ids":["a","b"]}`), "questions[0]: initial_selected_ids must contain at most one id in single mode"},
		{"single bad min", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"min_select":0}`), "questions[0]: min_select must be 1 in single mode"},
		{"single bad max", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"max_select":0}`), "questions[0]: max_select must be 1 in single mode"},
		{"conflicting single aliases", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"initial_selected_id":"a","initial_selected_ids":["b"]}`), "questions[0]: initial_selected_id conflicts with initial_selected_ids"},
		{"conflicting multiple aliases", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"initial_selected_id":"a","initial_selected_ids":["b"]}`), "questions[0]: initial_selected_id conflicts with initial_selected_ids"},
		{"unknown single", wrapQuestions(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}],"initial_selected_id":"missing"}`), "questions[0]: initial_selected_id references unknown option id: missing"},
		{"unknown multiple", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_ids":["missing"]}`), "questions[0]: initial_selected_ids references unknown option id: missing"},
		{"duplicate selected", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"initial_selected_ids":["a","a"]}`), "questions[0]: duplicate initial selected id: a"},
		{"min low", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"min_select":-1}`), "questions[0]: min_select must be between 0 and 1"},
		{"max high", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"max_select":2}`), "questions[0]: max_select must be between 0 and 1"},
		{"bounds", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}],"min_select":1,"max_select":0}`), "questions[0]: min_select must not exceed max_select"},
		{"initial above max", wrapQuestions(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"initial_selected_ids":["a","b"],"max_select":1}`), "questions[0]: initial selection count 2 exceeds max_select 1"},
		{"no questions", `{}`, "questions must contain at least one question"},
		{"second question invalid", `{"questions":[{"prompt":"First","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":" ","mode":"single","options":[{"id":"b","label":"B"}]}]}`, "questions[1]: prompt is required"},
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
