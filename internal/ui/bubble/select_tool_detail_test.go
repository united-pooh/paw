package bubble

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func wrapQuestionPresentation(question string) json.RawMessage {
	return json.RawMessage(`{"questions":[` + question + `]}`)
}

func TestSelectToolPresentationSubmitted(t *testing.T) {
	input := wrapQuestionPresentation(`{"prompt":"Which animals are mammals?","mode":"multiple","options":[{"id":"whale","label":"Whale","description":"Breathes with lungs"},{"id":"shark","label":"Shark","description":"A fish"}]}`)
	result := `{"results":[{"cancelled":false,"selected_options":[{"id":"whale","label":"Whale"},{"id":"custom_option","label":"Platypus"}]}]}`
	presentation, ok := parseSelectToolPresentation(input, result)
	if !ok {
		t.Fatal("presentation did not parse")
	}
	if presentation.target != "answered 1 question" {
		t.Fatalf("target=%q", presentation.target)
	}
	want := "Q1  Which animals are mammals?\n\nWhale\n  Breathes with lungs\n\nPlatypus\n  Custom option"
	if presentation.detail != want {
		t.Fatalf("detail=%q want=%q", presentation.detail, want)
	}
	if strings.Contains(presentation.detail, "Shark") || strings.Contains(presentation.detail, "selected_options") {
		t.Fatalf("unselected or raw JSON leaked: %q", presentation.detail)
	}
}

func TestSelectToolPresentationBatch(t *testing.T) {
	input := json.RawMessage(`{"questions":[{"prompt":"Pick env","mode":"single","options":[{"id":"prod","label":"Production"}]},{"prompt":"Pick signals","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}]}`)
	result := `{"results":[{"cancelled":false,"selected_options":[{"id":"prod","label":"Production"}]},{"cancelled":false,"selected_options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}]}`
	presentation, ok := parseSelectToolPresentation(input, result)
	if !ok {
		t.Fatal("presentation did not parse")
	}
	if presentation.target != "answered 2 questions" {
		t.Fatalf("target=%q", presentation.target)
	}
	want := "Q1  Pick env\n\nProduction\n\nQ2  Pick signals\n\nLogs\n\nMetrics"
	if presentation.detail != want {
		t.Fatalf("detail=%q want=%q", presentation.detail, want)
	}
}

func TestSelectToolPresentationCancelled(t *testing.T) {
	input := wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	presentation, ok := parseSelectToolPresentation(input, `{"results":[{"cancelled":true,"selected_options":[]}]}`)
	if !ok || presentation.target != "cancelled" || presentation.detail != "Q1  Pick\n\nSelection cancelled." {
		t.Fatalf("presentation=%#v ok=%v", presentation, ok)
	}
}

func TestSelectToolPresentationRejectsMalformedInputOrResult(t *testing.T) {
	tests := []struct {
		name    string
		input   json.RawMessage
		content string
	}{
		{name: "malformed input", input: json.RawMessage(`{"questions":`), content: `{"results":[{"cancelled":false,"selected_options":[]}]}`},
		{name: "invalid request", input: json.RawMessage(`{"questions":[{"prompt":"Pick","mode":"bogus","options":[]}]}`), content: `{"results":[{"cancelled":false,"selected_options":[]}]}`},
		{name: "malformed result", input: wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"results":`},
		{name: "result count mismatch", input: wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"results":[{"cancelled":false,"selected_options":[]},{"cancelled":false,"selected_options":[]}]}`},
		{name: "legacy result", input: wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"cancelled":false,"selected_ids":["a"]}`},
		{name: "nil selected options", input: wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`), content: `{"results":[{"cancelled":false}]}`},
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
	input := wrapQuestionPresentation(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	for _, content := range []string{
		`{"results":[{"cancelled":false,"selected_options":[{"id":"missing","label":"Missing"}]}]}`,
		`{"results":[{"cancelled":false,"selected_options":[{"id":"a","label":"A"},{"id":"a","label":"Again"}]}]}`,
		`{"results":[{"cancelled":true,"selected_options":[{"id":"a","label":"A"}]}]}`,
	} {
		if _, ok := parseSelectToolPresentation(input, content); ok {
			t.Fatalf("inconsistent result accepted: %s", content)
		}
	}
}

func TestTranscriptRenderKeyIncludesSelectToolInput(t *testing.T) {
	entry := transcriptEntry{kind: entryTool, toolName: "question", toolStatus: "ok", toolInput: json.RawMessage(`{"questions":[{"prompt":"First"}]}`)}
	first := transcriptRenderKey(entry, 80, time.Time{}, false)
	entry.toolInput = json.RawMessage(`{"questions":[{"prompt":"Second"}]}`)
	second := transcriptRenderKey(entry, 80, time.Time{}, false)
	if first == second {
		t.Fatal("tool input did not participate in transcript render cache key")
	}
}

func TestSelectToolPresentationNormalizesInputAndAllowsUnknownFields(t *testing.T) {
	input := json.RawMessage(`{"questions":[{"prompt":"  Pick  ","mode":"single","future":true,"options":[{"id":" a ","label":" A ","description":" Detail ","extra":1}]}]}`)
	presentation, ok := parseSelectToolPresentation(input, `{"results":[{"cancelled":false,"selected_options":[{"id":" a ","label":" A "}],"future":"ok"}]}`)
	if !ok || presentation.target != "answered 1 question" || presentation.detail != "Q1  Pick\n\nA\n  Detail" {
		t.Fatalf("presentation=%#v ok=%v", presentation, ok)
	}
}
