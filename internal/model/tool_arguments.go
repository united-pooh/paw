package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeToolArguments accepts only a complete JSON object. Native provider
// protocols must never silently replace malformed tool arguments with {}.
func decodeToolArguments(provider, callID, name string, raw []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	label := strings.TrimSpace(name)
	if label == "" {
		label = strings.TrimSpace(callID)
	}
	if len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
	}
	return append(json.RawMessage(nil), trimmed...), nil
}
