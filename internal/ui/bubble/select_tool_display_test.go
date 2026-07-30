package bubble

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatSelectToolCallBodyHidesOptionPayload(t *testing.T) {
	body := formatToolCallBody("Select", json.RawMessage(`{"prompt":"Choose environment","mode":"single","options":[{"id":"prod","label":"Production"},{"id":"stage","label":"Staging"}]}`), "")
	if body != "Select\nmode  single\nprompt  Choose environment" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "Production") || strings.Contains(body, "prod") {
		t.Fatalf("payload leaked: %q", body)
	}
}

func TestCompleteSelectToolCallBodySummarizesResult(t *testing.T) {
	running := formatRunningToolCallBody("Select", json.RawMessage(`{"prompt":"Pick","mode":"multiple","options":[{"id":"a","label":"A"}]}`), "")
	got := completeToolCallBody("Select", running, "ok", `{"cancelled":false,"selected_ids":["a"]}`)
	if strings.Split(got, "\n")[0] != "Select · selected 1 option" {
		t.Fatalf("body = %q", got)
	}
	got = completeToolCallBody("Select", running, "ok", `{"cancelled":false,"selected_ids":["a","b"]}`)
	if strings.Split(got, "\n")[0] != "Select · selected 2 options" {
		t.Fatalf("body = %q", got)
	}
	got = completeToolCallBody("Select", running, "ok", `{"cancelled":true,"selected_ids":[]}`)
	if strings.Split(got, "\n")[0] != "Select · cancelled" {
		t.Fatalf("body = %q", got)
	}
}

func TestSelectToolDisplayTargetAndResultSummary(t *testing.T) {
	input := json.RawMessage(`{"prompt":"Choose environment","mode":"single","options":[{"id":"prod","label":"Production"}]}`)
	if got, ok := selectToolCallTarget("Select", input); !ok || got != "Choose environment" {
		t.Fatalf("target = %q, %v", got, ok)
	}
	if got, ok := selectToolResultTarget("Select", "ok", `{"cancelled":false,"selected_ids":["prod"]}`); !ok || got != "selected 1 option" {
		t.Fatalf("summary = %q, %v", got, ok)
	}
	if got, ok := selectToolResultTarget("Select", "ok", `{"cancelled":true,"selected_ids":[]}`); !ok || got != "cancelled" {
		t.Fatalf("cancel summary = %q, %v", got, ok)
	}
}
