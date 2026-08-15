package bubble

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatSelectToolCallBodyHidesOptionPayload(t *testing.T) {
	body := formatToolCallBody("question", json.RawMessage(`{"questions":[{"prompt":"Choose environment","mode":"single","options":[{"id":"prod","label":"Production"},{"id":"stage","label":"Staging"}]}]}`), "")
	if body != "question\n1 question" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "Production") || strings.Contains(body, "prod") {
		t.Fatalf("payload leaked: %q", body)
	}
}

func TestFormatSelectToolCallBodyBatchCount(t *testing.T) {
	body := formatToolCallBody("question", json.RawMessage(`{"questions":[{"prompt":"A","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":"B","mode":"single","options":[{"id":"b","label":"B"}]}]}`), "")
	if body != "question\n2 questions" {
		t.Fatalf("body = %q", body)
	}
}

func TestCompleteSelectToolCallBodySummarizesResult(t *testing.T) {
	running := formatRunningToolCallBody("question", json.RawMessage(`{"questions":[{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}]}]}`), "")
	got := completeToolCallBody("question", running, "ok", `{"results":[{"cancelled":false,"selected_options":[{"id":"a","label":"A"}]}]}`)
	if strings.Split(got, "\n")[0] != "question  answered 1 question" {
		t.Fatalf("body = %q", got)
	}
	got = completeToolCallBody("question", running, "ok", `{"results":[{"cancelled":true,"selected_options":[]}]}`)
	if strings.Split(got, "\n")[0] != "question  cancelled" {
		t.Fatalf("body = %q", got)
	}
}

func TestCompleteSelectToolCallBodyBatchSummary(t *testing.T) {
	running := formatRunningToolCallBody("question", json.RawMessage(`{"questions":[{"prompt":"A","mode":"single","options":[{"id":"a","label":"A"}]},{"prompt":"B","mode":"single","options":[{"id":"b","label":"B"}]}]}`), "")
	got := completeToolCallBody("question", running, "ok", `{"results":[{"cancelled":false,"selected_options":[{"id":"a","label":"A"}]},{"cancelled":false,"selected_options":[{"id":"b","label":"B"}]}]}`)
	if strings.Split(got, "\n")[0] != "question  answered 2 questions" {
		t.Fatalf("body = %q", got)
	}
}

func TestSelectToolDisplayTargetAndResultSummary(t *testing.T) {
	input := json.RawMessage(`{"questions":[{"prompt":"Choose environment","mode":"single","options":[{"id":"prod","label":"Production"}]}]}`)
	if got, ok := selectToolCallTarget("question", input); !ok || got != "" {
		t.Fatalf("target = %q, %v", got, ok)
	}
	if got, ok := selectToolResultTarget("question", "ok", `{"results":[{"cancelled":false,"selected_options":[{"id":"prod","label":"Production"}]}]}`); !ok || got != "answered 1 question" {
		t.Fatalf("summary = %q, %v", got, ok)
	}
	if got, ok := selectToolResultTarget("question", "ok", `{"results":[{"cancelled":true,"selected_options":[]}]}`); !ok || got != "cancelled" {
		t.Fatalf("cancel summary = %q, %v", got, ok)
	}
	if got, ok := selectToolResultTarget("question", "ok", `{"results":[{"cancelled":false,"selected_options":[]},{"cancelled":false,"selected_options":[]}]}`); !ok || got != "answered 2 questions" {
		t.Fatalf("batch summary = %q, %v", got, ok)
	}
}
