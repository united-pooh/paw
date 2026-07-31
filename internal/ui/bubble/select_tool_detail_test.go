package bubble

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSelectToolPresentationSubmitted(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Which animals are mammals?","mode":"multiple","options":[{"id":"whale","label":"Whale","description":"Breathes with lungs"},{"id":"shark","label":"Shark","description":"A fish"}]}`)
	result := `{"cancelled":false,"selected_options":[{"id":"whale","label":"Whale"},{"id":"custom_option","label":"Platypus"}]}`
	presentation, ok := parseSelectToolPresentation(input, result)
	if !ok {
		t.Fatal("presentation did not parse")
	}
	if presentation.target != "selected 2 options" {
		t.Fatalf("target=%q", presentation.target)
	}
	want := "Which animals are mammals?\n\nWhale\n  Breathes with lungs\n\nPlatypus\n  Custom option"
	if presentation.detail != want {
		t.Fatalf("detail=%q want=%q", presentation.detail, want)
	}
	if strings.Contains(presentation.detail, "Shark") || strings.Contains(presentation.detail, "selected_options") {
		t.Fatalf("unselected or raw JSON leaked: %q", presentation.detail)
	}
}

func TestSelectToolPresentationCancelled(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	presentation, ok := parseSelectToolPresentation(input, `{"cancelled":true,"selected_options":[]}`)
	if !ok || presentation.target != "cancelled" || presentation.detail != "Pick\n\nSelection cancelled." {
		t.Fatalf("presentation=%#v ok=%v", presentation, ok)
	}
}

func TestSelectToolPresentationRejectsMalformedInputOrResult(t *testing.T) {
	tests := []struct {
		name    string
		input   json.RawMessage
		content string
	}{
		{name: "malformed input", input: json.RawMessage(`{"prompt":`), content: `{"cancelled":false,"selected_options":[]}`},
		{name: "invalid request", input: json.RawMessage(`{"prompt":"Pick","mode":"bogus","options":[]}`), content: `{"cancelled":false,"selected_options":[]}`},
		{name: "malformed result", input: json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"selected_options":`},
		{name: "legacy result", input: json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"cancelled":false,"selected_ids":["a"]}`},
		{name: "nil selected options", input: json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"cancelled":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := parseSelectToolPresentation(test.input, test.content); ok {
				t.Fatal("malformed presentation accepted")
			}
		})
	}
}

func TestSelectToolPresentationRejectsInconsistentSelection(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	for _, content := range []string{
		`{"cancelled":false,"selected_options":[{"id":"missing","label":"Missing"}]}`,
		`{"cancelled":false,"selected_options":[{"id":"a","label":"A"},{"id":"a","label":"Again"}]}`,
		`{"cancelled":true,"selected_options":[{"id":"a","label":"A"}]}`,
	} {
		if _, ok := parseSelectToolPresentation(input, content); ok {
			t.Fatalf("inconsistent result accepted: %s", content)
		}
	}
}

func TestTranscriptRenderKeyIncludesSelectToolInput(t *testing.T) {
	entry := transcriptEntry{kind: entryTool, toolName: "Select", toolStatus: "ok", toolInput: json.RawMessage(`{"prompt":"First"}`)}
	first := transcriptRenderKey(entry, 80, time.Time{})
	entry.toolInput = json.RawMessage(`{"prompt":"Second"}`)
	second := transcriptRenderKey(entry, 80, time.Time{})
	if first == second {
		t.Fatal("tool input did not participate in transcript render cache key")
	}
}
