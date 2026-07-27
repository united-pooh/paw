// Package displaywidth provides the clipperhouse/displaywidth API backed by
// the same grapheme-width rules as Ghostty.
package displaywidth

import (
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
	uucode "github.com/rockorager/go-uucode"
)

func String(text string) int {
	return DefaultOptions.String(text)
}

func (options Options) String(text string) int {
	if len(text) == 1 {
		return byteWidth(options, text[0])
	}
	if !options.ControlSequences && !options.ControlSequences8Bit {
		return stringWidthWithUucode(text, options.EastAsianWidth)
	}

	clusters := graphemes.FromString(text)
	clusters.AnsiEscapeSequences = options.ControlSequences
	clusters.AnsiEscapeSequences8Bit = options.ControlSequences8Bit

	width := 0
	for clusters.Next() {
		width += graphemeWidth(options, clusters.Value())
	}
	return width
}

func Bytes(text []byte) int {
	return DefaultOptions.Bytes(text)
}

func (options Options) Bytes(text []byte) int {
	if len(text) == 1 {
		return byteWidth(options, text[0])
	}
	clusters := graphemes.FromBytes(text)
	clusters.AnsiEscapeSequences = options.ControlSequences
	clusters.AnsiEscapeSequences8Bit = options.ControlSequences8Bit

	width := 0
	for clusters.Next() {
		width += graphemeWidth(options, clusters.Value())
	}
	return width
}

func Rune(r rune) int {
	return DefaultOptions.Rune(r)
}

func (options Options) Rune(r rune) int {
	if r < 0 || r > utf8.MaxRune || r >= 0xd800 && r <= 0xdfff {
		return 0
	}
	if r < utf8.RuneSelf {
		return byteWidth(options, byte(r))
	}
	return graphemeWidth(options, string(r))
}

func stringWidthWithUucode(text string, eastAsianWidth bool) int {
	width := 0
	start := 0
	for start < len(text) {
		if text[start] >= utf8.RuneSelf ||
			start+1 < len(text) && text[start+1] >= utf8.RuneSelf {
			break
		}
		if text[start] >= 0x20 && text[start] != 0x7f {
			width++
		}
		start++
	}

	remaining := text[start:]
	clusters := uucode.NewGraphemeWidthIterator(remaining)
	for {
		cluster, ok := clusters.Next()
		if !ok {
			return width
		}
		clusterWidth := min(cluster.Width, 2)
		if eastAsianWidth && clusterWidth == 1 {
			r, _ := utf8.DecodeRuneInString(remaining[cluster.Start:cluster.End])
			if uucode.EastAsianWidth(r) == uucode.EastAsianWidthA {
				clusterWidth = 2
			}
		}
		width += clusterWidth
	}
}

func graphemeWidth[T ~string | []byte](options Options, cluster T) int {
	if len(cluster) == 0 {
		return 0
	}
	if len(cluster) == 1 {
		return byteWidth(options, cluster[0])
	}
	first := cluster[0]
	if first <= 0x1f || first == 0x7f ||
		(options.ControlSequences8Bit && first >= 0x80 && first <= 0x9f) {
		return 0
	}

	width := uucode.StringWidth(string(cluster))
	if width > 2 {
		width = 2
	}
	if options.EastAsianWidth && width == 1 {
		r, _ := utf8.DecodeRune([]byte(cluster))
		if uucode.EastAsianWidth(r) == uucode.EastAsianWidthA {
			return 2
		}
	}
	return width
}

func byteWidth(options Options, value byte) int {
	if value <= 0x1f || value == 0x7f ||
		options.ControlSequences8Bit && value >= 0x80 && value <= 0x9f {
		return 0
	}
	return 1
}
