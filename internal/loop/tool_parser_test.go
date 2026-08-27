package loop

import (
	"encoding/json"
	"testing"
)

func TestDecodeToolUseEnvelopeRepairsInvalidEscapes(t *testing.T) {
	raw := "{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"Grep\"," +
		"\"input\":{\"pattern\":\"\\\\.`\",\"path\":\"internal/theme\"}}"

	envelope, ok := decodeToolUseEnvelope(raw)
	if !ok {
		t.Fatalf("expected envelope to decode after literal repair, payload=%s", raw)
	}
	if envelope.Name != "Grep" || envelope.ID != "call_1" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}

	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(envelope.Input, &args); err != nil {
		t.Fatalf("input not valid JSON after repair: %v", err)
	}
	if args.Path != "internal/theme" {
		t.Fatalf("unexpected repaired args: %+v", args)
	}
}

func TestDecodeToolUseEnvelopesRepairsInvalidEscapes(t *testing.T) {
	raw := "[{\"type\":\"tool_use\",\"id\":\"call_2\",\"name\":\"Bash\",\"input\":{\"command\":\"grep -n a\\\\.b file\"}}]"

	envelopes, ok := decodeToolUseEnvelopes(raw)
	if !ok {
		t.Fatalf("expected envelopes to decode after literal repair, payload=%s", raw)
	}
	if len(envelopes) != 1 || envelopes[0].Name != "Bash" {
		t.Fatalf("unexpected envelopes: %+v", envelopes)
	}
}
