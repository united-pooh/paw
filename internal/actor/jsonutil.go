package actor

import (
	"encoding/json"
)

func jsonUnmarshal(raw json.RawMessage, target any) error {
	return json.Unmarshal(raw, target)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
