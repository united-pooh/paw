package message

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMessageProviderDataJSONRoundTrip(t *testing.T) {
	original := Message{
		Role:         RoleAssistant,
		Content:      "checking",
		ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}]}`),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !bytes.Equal(restored.ProviderData, original.ProviderData) {
		t.Fatalf("ProviderData = %s, want %s", restored.ProviderData, original.ProviderData)
	}
}

func TestMessageWithoutProviderDataRemainsCompatible(t *testing.T) {
	var restored Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"done"}`), &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(restored.ProviderData) != 0 {
		t.Fatalf("ProviderData = %s, want empty", restored.ProviderData)
	}
}
