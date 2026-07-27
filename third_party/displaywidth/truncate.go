package displaywidth

import (
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

// TruncateString truncates s to maxWidth and appends tail when truncation is
// required. With ControlSequences enabled, trailing zero-width ANSI sequences
// are preserved so styles cannot bleed into following output.
func (options Options) TruncateString(s string, maxWidth int, tail string) string {
	options.ControlSequences8Bit = false
	maxWidthWithoutTail := maxWidth - options.String(tail)

	var pos, total int
	g := graphemes.FromString(s)
	g.AnsiEscapeSequences = options.ControlSequences
	for g.Next() {
		graphemeWidth := graphemeWidth(options, g.Value())
		if total+graphemeWidth <= maxWidthWithoutTail {
			pos = g.End()
		}
		total += graphemeWidth
		if total <= maxWidth {
			continue
		}
		if !options.ControlSequences {
			return s[:pos] + tail
		}

		var result strings.Builder
		result.Grow(len(s) + len(tail))
		result.WriteString(s[:pos])
		result.WriteString(tail)
		remaining := graphemes.FromString(s[pos:])
		remaining.AnsiEscapeSequences = true
		for remaining.Next() {
			value := remaining.Value()
			if len(value) > 0 && value[0] == 0x1b && options.String(value) == 0 {
				result.WriteString(value)
			}
		}
		return result.String()
	}
	return s
}

// TruncateString truncates s using DefaultOptions.
func TruncateString(s string, maxWidth int, tail string) string {
	return DefaultOptions.TruncateString(s, maxWidth, tail)
}

// TruncateBytes truncates s to maxWidth and appends tail when truncation is
// required. It has the same ANSI-preservation semantics as TruncateString.
func (options Options) TruncateBytes(s []byte, maxWidth int, tail []byte) []byte {
	options.ControlSequences8Bit = false
	maxWidthWithoutTail := maxWidth - options.Bytes(tail)

	var pos, total int
	g := graphemes.FromBytes(s)
	g.AnsiEscapeSequences = options.ControlSequences
	for g.Next() {
		graphemeWidth := graphemeWidth(options, g.Value())
		if total+graphemeWidth <= maxWidthWithoutTail {
			pos = g.End()
		}
		total += graphemeWidth
		if total <= maxWidth {
			continue
		}
		if !options.ControlSequences {
			result := make([]byte, 0, pos+len(tail))
			result = append(result, s[:pos]...)
			return append(result, tail...)
		}

		result := make([]byte, 0, len(s)+len(tail))
		result = append(result, s[:pos]...)
		result = append(result, tail...)
		remaining := graphemes.FromBytes(s[pos:])
		remaining.AnsiEscapeSequences = true
		for remaining.Next() {
			value := remaining.Value()
			if len(value) > 0 && value[0] == 0x1b && options.Bytes(value) == 0 {
				result = append(result, value...)
			}
		}
		return result
	}
	return s
}

// TruncateBytes truncates s using DefaultOptions.
func TruncateBytes(s []byte, maxWidth int, tail []byte) []byte {
	return DefaultOptions.TruncateBytes(s, maxWidth, tail)
}
