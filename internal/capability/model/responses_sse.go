package model

import "bytes"

func splitResponsesSSEEvents(data []byte, atEOF bool) (advance int, token []byte, err error) {
	lineStart := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' && data[i] != '\r' {
			continue
		}
		lineEnd := i
		if data[i] == '\r' && i+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			i++
		}
		nextLine := i + 1
		if lineEnd == lineStart {
			return nextLine, data[:lineStart], nil
		}
		lineStart = nextLine
	}
	if atEOF && len(data) != 0 {
		// EOF does not dispatch an SSE event without its terminating blank line.
		return len(data), nil, nil
	}
	return 0, nil, nil
}

func responsesSSEPayload(block []byte) ([]byte, bool) {
	block = bytes.TrimPrefix(block, []byte{0xef, 0xbb, 0xbf})
	block = bytes.ReplaceAll(block, []byte{'\r', '\n'}, []byte{'\n'})
	block = bytes.ReplaceAll(block, []byte{'\r'}, []byte{'\n'})
	lines := bytes.Split(block, []byte{'\n'})
	dataLines := make([][]byte, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found || !bytes.Equal(field, []byte("data")) {
			continue
		}
		if len(value) != 0 && value[0] == ' ' {
			value = value[1:]
		}
		dataLines = append(dataLines, value)
	}
	if len(dataLines) == 0 {
		return nil, false
	}
	return bytes.Join(dataLines, []byte{'\n'}), true
}
