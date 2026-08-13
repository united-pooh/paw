package es

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	in := Envelope{
		Seq:           42,
		Type:          "goal.paused",
		OccurredAt:    at,
		SchemaVersion: 1,
		Payload:       json.RawMessage(`{"reason":"no_progress","turn_count":7}`),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Envelope
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Seq != 42 || out.Type != "goal.paused" || !out.OccurredAt.Equal(at) || out.SchemaVersion != 1 {
		t.Fatalf("round trip mismatch: %+v", out)
	}
	if string(out.Payload) != `{"reason":"no_progress","turn_count":7}` {
		t.Fatalf("payload mismatch: %s", out.Payload)
	}
}

func TestEnvelopeJSONIsSingleLineSafe(t *testing.T) {
	payload := `{"note":"line1\nline2","quote":"a\"b"}`
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(Envelope{
		Seq:           1,
		Type:          "session.user_message",
		OccurredAt:    at,
		SchemaVersion: 1,
		Payload:       json.RawMessage(payload),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if containsNewline(data) {
		t.Fatalf("envelope JSON must not contain raw newlines for JSONL: %q", data)
	}
}

func containsNewline(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}
