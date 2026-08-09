package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// normalizeJSONC converts comments and trailing commas to spaces. Keeping the
// byte length stable lets targeted edits operate on the original JSONC text.
func normalizeJSONC(raw []byte) ([]byte, error) {
	out := append([]byte(nil), raw...)
	inString := false
	escaped := false
	for i := 0; i < len(out); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if out[i] == '\\' {
				escaped = true
				continue
			}
			if out[i] == '"' {
				inString = false
			}
			continue
		}
		if out[i] == '"' {
			inString = true
			continue
		}
		if out[i] != '/' || i+1 >= len(out) {
			continue
		}
		switch out[i+1] {
		case '/':
			out[i], out[i+1] = ' ', ' '
			for i += 2; i < len(out) && out[i] != '\n' && out[i] != '\r'; i++ {
				out[i] = ' '
			}
			i--
		case '*':
			out[i], out[i+1] = ' ', ' '
			closed := false
			for i += 2; i < len(out); i++ {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					closed = true
					break
				}
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated block comment")
			}
		}
	}
	if inString {
		return nil, fmt.Errorf("unterminated JSON string")
	}
	for i := 0; i < len(out); i++ {
		if out[i] != ',' {
			continue
		}
		j := i + 1
		for j < len(out) && isJSONSpace(out[j]) {
			j++
		}
		if j < len(out) && (out[j] == '}' || out[j] == ']') {
			out[i] = ' '
		}
	}
	return out, nil
}

func decodeJSONC(raw []byte, destination any) error {
	normalized, err := normalizeJSONC(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values are not allowed")
	} else if err != io.EOF {
		return err
	}
	return nil
}

// DecodeJSONObject parses a JSONC object for advanced configuration editors.
func DecodeJSONObject(raw []byte) (map[string]any, error) {
	var value map[string]any
	if err := decodeJSONC(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

type objectMember struct {
	Key        string
	PairStart  int
	ValueStart int
	ValueEnd   int
	Comma      int
}

func parseObjectMembers(normalized []byte, objectStart int) ([]objectMember, int, error) {
	if objectStart < 0 || objectStart >= len(normalized) || normalized[objectStart] != '{' {
		return nil, 0, fmt.Errorf("expected JSON object")
	}
	var members []objectMember
	i := skipJSONSpace(normalized, objectStart+1)
	for i < len(normalized) {
		if normalized[i] == '}' {
			return members, i, nil
		}
		pairStart := i
		if normalized[i] != '"' {
			return nil, 0, fmt.Errorf("object key expected at byte %d", i)
		}
		keyEnd, err := scanJSONString(normalized, i)
		if err != nil {
			return nil, 0, err
		}
		var key string
		if err := json.Unmarshal(normalized[i:keyEnd], &key); err != nil {
			return nil, 0, err
		}
		i = skipJSONSpace(normalized, keyEnd)
		if i >= len(normalized) || normalized[i] != ':' {
			return nil, 0, fmt.Errorf("colon expected after %q", key)
		}
		valueStart := skipJSONSpace(normalized, i+1)
		valueEnd, err := scanJSONValue(normalized, valueStart)
		if err != nil {
			return nil, 0, fmt.Errorf("field %q: %w", key, err)
		}
		i = skipJSONSpace(normalized, valueEnd)
		comma := -1
		if i < len(normalized) && normalized[i] == ',' {
			comma = i
			i = skipJSONSpace(normalized, i+1)
		} else if i >= len(normalized) || normalized[i] != '}' {
			return nil, 0, fmt.Errorf("comma or object end expected after %q", key)
		}
		members = append(members, objectMember{Key: key, PairStart: pairStart, ValueStart: valueStart, ValueEnd: valueEnd, Comma: comma})
	}
	return nil, 0, fmt.Errorf("unterminated JSON object")
}

func scanJSONString(data []byte, start int) (int, error) {
	if start >= len(data) || data[start] != '"' {
		return 0, fmt.Errorf("string expected")
	}
	escaped := false
	for i := start + 1; i < len(data); i++ {
		if escaped {
			escaped = false
			continue
		}
		if data[i] == '\\' {
			escaped = true
			continue
		}
		if data[i] == '"' {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}

func scanJSONValue(data []byte, start int) (int, error) {
	if start >= len(data) {
		return 0, fmt.Errorf("value expected")
	}
	if data[start] == '"' {
		return scanJSONString(data, start)
	}
	if data[start] == '{' || data[start] == '[' {
		open := data[start]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		stack := []byte{close}
		inString, escaped := false, false
		for i := start + 1; i < len(data); i++ {
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if data[i] == '\\' {
					escaped = true
					continue
				}
				if data[i] == '"' {
					inString = false
				}
				continue
			}
			switch data[i] {
			case '"':
				inString = true
			case '{':
				stack = append(stack, '}')
			case '[':
				stack = append(stack, ']')
			case '}', ']':
				if len(stack) == 0 || data[i] != stack[len(stack)-1] {
					return 0, fmt.Errorf("mismatched JSON delimiter")
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return i + 1, nil
				}
			}
		}
		return 0, fmt.Errorf("unterminated JSON container")
	}
	i := start
	for i < len(data) && data[i] != ',' && data[i] != '}' && data[i] != ']' && !isJSONSpace(data[i]) {
		i++
	}
	if i == start {
		return 0, fmt.Errorf("value expected")
	}
	return i, nil
}

func findObject(normalized []byte, path ...string) (int, []objectMember, int, error) {
	start := skipJSONSpace(normalized, 0)
	for _, key := range path {
		members, _, err := parseObjectMembers(normalized, start)
		if err != nil {
			return 0, nil, 0, err
		}
		found := false
		for _, member := range members {
			if member.Key == key {
				start = member.ValueStart
				found = true
				break
			}
		}
		if !found {
			return 0, nil, 0, fmt.Errorf("object %q not found", strings.Join(path, "."))
		}
	}
	members, end, err := parseObjectMembers(normalized, start)
	return start, members, end, err
}

func patchJSONCMember(raw []byte, path []string, key string, value json.RawMessage, remove bool) ([]byte, error) {
	normalized, err := normalizeJSONC(raw)
	if err != nil {
		return nil, err
	}
	start, members, end, err := findObject(normalized, path...)
	if err != nil {
		return nil, err
	}
	for index, member := range members {
		if member.Key != key {
			continue
		}
		if !remove {
			formatted, err := indentJSONValue(value, lineIndent(raw, member.ValueStart))
			if err != nil {
				return nil, err
			}
			return replaceBytes(raw, member.ValueStart, member.ValueEnd, formatted), nil
		}
		deleteStart, deleteEnd := member.PairStart, member.ValueEnd
		if member.Comma >= 0 {
			deleteEnd = member.Comma + 1
		} else if trailingComma := findJSONCComma(raw, member.ValueEnd, end); trailingComma >= 0 {
			deleteEnd = trailingComma + 1
		} else if index > 0 && members[index-1].Comma >= 0 {
			deleteStart = members[index-1].Comma
		}
		return replaceBytes(raw, deleteStart, deleteEnd, nil), nil
	}
	if remove {
		return append([]byte(nil), raw...), nil
	}
	formatted, err := indentJSONValue(value, lineIndent(raw, start)+"  ")
	if err != nil {
		return nil, err
	}
	baseIndent := lineIndent(raw, start)
	entry := []byte(strconv.Quote(key) + ": " + string(formatted))
	prefix := "\n" + baseIndent + "  "
	if len(members) > 0 {
		prefix = ",\n" + baseIndent + "  "
		// A JSONC trailing comma was blanked in the normalized view. Reuse it
		// as the separator instead of creating a double comma.
		if findJSONCComma(raw, members[len(members)-1].ValueEnd, end) >= 0 {
			prefix = "\n" + baseIndent + "  "
		}
	}
	suffix := "\n" + baseIndent
	return replaceBytes(raw, end, end, append(append([]byte(prefix), entry...), []byte(suffix)...)), nil
}

func findJSONCComma(raw []byte, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(raw) {
		end = len(raw)
	}
	for index := start; index < end; index++ {
		if raw[index] == '/' && index+1 < end {
			switch raw[index+1] {
			case '/':
				index += 2
				for index < end && raw[index] != '\n' && raw[index] != '\r' {
					index++
				}
			case '*':
				index += 2
				for index+1 < end && !(raw[index] == '*' && raw[index+1] == '/') {
					index++
				}
				index++
			}
			continue
		}
		if raw[index] == ',' {
			return index
		}
	}
	return -1
}

func patchJSONCObjectMemberFields(raw []byte, path []string, key string, value json.RawMessage, knownFields []string) ([]byte, error) {
	normalized, err := normalizeJSONC(raw)
	if err != nil {
		return nil, err
	}
	_, members, _, err := findObject(normalized, path...)
	if err != nil {
		return nil, err
	}
	exists := false
	for _, member := range members {
		if member.Key == key {
			exists = true
			break
		}
	}
	if !exists {
		return patchJSONCMember(raw, path, key, value, false)
	}
	var fields map[string]json.RawMessage
	normalizedValue, err := normalizeJSONC(value)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(normalizedValue, &fields); err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(knownFields))
	for _, field := range knownFields {
		known[field] = true
		fieldValue, ok := fields[field]
		if ok {
			raw, err = patchJSONCMember(raw, append(append([]string(nil), path...), key), field, fieldValue, false)
		} else {
			raw, err = patchJSONCMember(raw, append(append([]string(nil), path...), key), field, nil, true)
		}
		if err != nil {
			return nil, err
		}
	}
	// Raw Operation users may intentionally add extension fields. Typed UI
	// operations do not contain them, while existing unknown fields remain.
	for field, fieldValue := range fields {
		if known[field] {
			continue
		}
		raw, err = patchJSONCMember(raw, append(append([]string(nil), path...), key), field, fieldValue, false)
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func indentJSONValue(value []byte, indent string) ([]byte, error) {
	normalized, err := normalizeJSONC(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return nil, err
	}
	formatted = bytes.ReplaceAll(formatted, []byte("\n"), []byte("\n"+indent))
	return formatted, nil
}

func lineIndent(data []byte, at int) string {
	if at > len(data) {
		at = len(data)
	}
	start := bytes.LastIndexByte(data[:at], '\n') + 1
	end := start
	for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[start:end])
}

func replaceBytes(raw []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(raw)-(end-start)+len(replacement))
	out = append(out, raw[:start]...)
	out = append(out, replacement...)
	out = append(out, raw[end:]...)
	return out
}

func skipJSONSpace(data []byte, at int) int {
	for at < len(data) && isJSONSpace(data[at]) {
		at++
	}
	return at
}

func isJSONSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}
