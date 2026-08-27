package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// decodeToolArguments accepts only a complete JSON object. Native provider
// protocols must never silently replace malformed tool arguments with {}.
//
// Common LLM mistakes inside string literals (raw control characters, invalid
// escapes) are repaired before parsing; structurally damaged JSON such as
// truncated output still fails so the model can re-issue the call.
func decodeToolArguments(provider, callID, name string, raw []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	label := strings.TrimSpace(name)
	if label == "" {
		label = strings.TrimSpace(callID)
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
	}
	if !json.Valid(trimmed) {
		repaired := repairJSONStringLiterals(trimmed)
		if !json.Valid(repaired) {
			return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
		}
		trimmed = repaired
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s returned invalid JSON object arguments for tool %q", provider, label)
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

var jsonValidEscapes = func() [256]bool {
	var valid [256]bool
	for _, c := range []byte{'"', '\\', '/', 'b', 'f', 'n', 'r', 't'} {
		valid[c] = true
	}
	return valid
}()

func hexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// escapeControlChar returns the JSON escape sequence for a raw control
// character inside a string literal.
func escapeControlChar(c byte) string {
	switch c {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
		const hexDigits = "0123456789abcdef"
		return `\u00` + string([]byte{hexDigits[c>>4], hexDigits[c&0x0f]})
	}
}

// RepairJSONStringLiterals exposes the literal repair pass for stream-side
// lenient parsing: raw control characters and invalid escapes are repaired so
// envelope sniffing and UI scrubbing match the strict arguments decoder.
func RepairJSONStringLiterals(data []byte) []byte {
	return repairJSONStringLiterals(data)
}

// repairJSONStringLiterals fixes two common LLM mistakes without touching any
// other byte of the document:
//   - raw control characters (< 0x20) inside string literals, which must be
//     escaped in valid JSON;
//   - backslashes followed by an invalid escape character, which are doubled.
//
// Structural damage (truncation, stray tokens) is intentionally left intact so
// it still fails validation and surfaces to the model for a re-issue.
func repairJSONStringLiterals(data []byte) []byte {
	inString := false
	repaired := make([]byte, 0, len(data)+8)
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			repaired = append(repaired, c)
			continue
		}
		switch {
		case c == '"':
			inString = false
			repaired = append(repaired, c)
		case c == '\\':
			next := byte(0)
			if i+1 < len(data) {
				next = data[i+1]
			}
			if next == 'u' && i+6 < len(data) &&
				hexDigit(data[i+2]) && hexDigit(data[i+3]) && hexDigit(data[i+4]) && hexDigit(data[i+5]) {
				repaired = append(repaired, data[i:i+6]...)
				i += 5
				continue
			}
			if next == '"' && bytes.IndexByte(data[i+2:], '"') < 0 {
				// No further quote exists, so this one must terminate the
				// string rather than continue it: the backslash is literal.
				repaired = append(repaired, '\\', '\\', '"')
				inString = false
				i++
				continue
			}
			if next == 'u' {
				// \u without four hex digits: double the backslash and keep
				// the u as a literal character.
				repaired = append(repaired, '\\', '\\', 'u')
				i++
				continue
			}
			if next != 0 && jsonValidEscapes[next] {
				repaired = append(repaired, c, next)
				i++
				continue
			}
			repaired = append(repaired, '\\', '\\')
		case c < 0x20:
			repaired = append(repaired, escapeControlChar(c)...)
		default:
			repaired = append(repaired, c)
		}
	}
	return repaired
}
