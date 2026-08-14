package bubble

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const streamTabWidth = 4

type terminalTextSanitizer struct {
	utf8Tail  []byte
	ansiTail  []byte
	pendingCR bool
}

func (s *terminalTextSanitizer) Push(text string) string {
	if text == "" && len(s.utf8Tail) == 0 {
		return ""
	}
	data := make([]byte, 0, len(s.utf8Tail)+len(text))
	data = append(data, s.utf8Tail...)
	data = append(data, text...)

	valid, tail := normalizeUTF8Bytes(data, false)
	s.utf8Tail = tail
	return s.consume(valid)
}

func (s *terminalTextSanitizer) Flush() string {
	var out strings.Builder
	if len(s.utf8Tail) > 0 {
		valid, _ := normalizeUTF8Bytes(s.utf8Tail, true)
		s.utf8Tail = nil
		out.WriteString(s.consume(valid))
	}
	s.ansiTail = nil
	if s.pendingCR {
		out.WriteByte('\n')
		s.pendingCR = false
	}
	return out.String()
}

func (s *terminalTextSanitizer) Reset() {
	s.utf8Tail = nil
	s.ansiTail = nil
	s.pendingCR = false
}

func (s *terminalTextSanitizer) consume(text string) string {
	text = expandC1Controls(text)
	if len(s.ansiTail) > 0 {
		data := make([]byte, 0, len(s.ansiTail)+len(text))
		data = append(data, s.ansiTail...)
		data = append(data, text...)
		text = string(data)
		s.ansiTail = nil
	}
	var out strings.Builder
	for len(text) > 0 {
		seq, _, n, next := ansi.DecodeSequence(text, ansi.NormalState, nil)
		if n <= 0 {
			// Malformed terminal sequences must not stall the stream or leak
			// their current byte into the visible transcript.
			text = text[1:]
			continue
		}
		text = text[n:]

		if next != ansi.NormalState {
			s.ansiTail = append(s.ansiTail[:0], seq...)
			s.ansiTail = append(s.ansiTail, text...)
			break
		}
		if !isDisplaySequence(seq) {
			continue
		}

		r, _ := utf8.DecodeRuneInString(seq)
		switch r {
		case '\r':
			if s.pendingCR {
				out.WriteByte('\n')
			}
			s.pendingCR = true
		case '\n':
			out.WriteByte('\n')
			s.pendingCR = false
		case '\t':
			s.flushPendingCR(&out)
			out.WriteByte('\t')
		default:
			s.flushPendingCR(&out)
			out.WriteString(seq)
		}
	}
	return out.String()
}

func expandC1Controls(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r >= 0x80 && r <= 0x9f {
			out.WriteByte('\x1b')
			out.WriteRune(r - 0x40)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func (s *terminalTextSanitizer) flushPendingCR(out *strings.Builder) {
	if !s.pendingCR {
		return
	}
	out.WriteByte('\n')
	s.pendingCR = false
}

func normalizeUTF8Bytes(data []byte, final bool) (string, []byte) {
	var out strings.Builder
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			if !final {
				return out.String(), append([]byte(nil), data...)
			}
			out.WriteRune(utf8.RuneError)
			data = data[1:]
			continue
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			out.WriteRune(utf8.RuneError)
			data = data[1:]
			continue
		}
		out.Write(data[:size])
		data = data[size:]
	}
	return out.String(), nil
}

func isDisplaySequence(seq string) bool {
	if seq == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(seq)
	switch r {
	case '\r', '\n', '\t':
		return true
	}
	return !unicode.IsControl(r)
}

type displayToken struct {
	text  string
	width int
	kind  displayTokenKind
}

type displayTokenKind uint8

const (
	displayTokenText displayTokenKind = iota
	displayTokenNewline
	displayTokenTab
)

type streamLineBuffer struct {
	sanitizer   terminalTextSanitizer
	pending     string
	clusterTail string
	column      int
	width       int
	hasContent  bool
	// characterQueue stores complete display tokens received in char output mode
	// but not yet released to the transcript. The queue is the complete buffered
	// canonical stream; rendering advances it one grapheme at a time.
	characterQueue []displayToken
}

func (b *streamLineBuffer) Push(delta string, width int) string {
	width = maxInt(1, width)
	var out strings.Builder
	if b.width != 0 && b.width != width {
		out.WriteString(b.Resize(width))
	}
	b.width = width

	safe := b.sanitizer.Push(delta)
	if safe != "" {
		b.hasContent = true
		out.WriteString(b.accept(safe, width, false))
	}
	return out.String()
}

func (b *streamLineBuffer) PushCharacters(delta string) string {
	width := maxInt(1, b.width)
	b.width = width
	safe := b.sanitizer.Push(delta)
	if safe == "" {
		return ""
	}
	b.hasContent = true
	tokens := displayTokens(b.clusterTail + safe)
	b.clusterTail = ""
	if len(tokens) > 0 && tokens[len(tokens)-1].kind == displayTokenText {
		b.clusterTail = tokens[len(tokens)-1].text
		tokens = tokens[:len(tokens)-1]
	}
	b.characterQueue = append(b.characterQueue, tokens...)
	return ""
}

// ReleaseCharacters releases at most count complete display tokens. Tokens stay
// grouped by grapheme, so Unicode combining marks and joined emoji are never
// split. Unreleased tokens remain buffered for subsequent cursor frames.
func (b *streamLineBuffer) ReleaseCharacters(count int) string {
	if count <= 0 || len(b.characterQueue) == 0 {
		return ""
	}
	if count > len(b.characterQueue) {
		count = len(b.characterQueue)
	}
	var out strings.Builder
	for _, token := range b.characterQueue[:count] {
		switch token.kind {
		case displayTokenNewline:
			out.WriteByte('\n')
		case displayTokenTab:
			out.WriteByte('\t')
		default:
			out.WriteString(token.text)
		}
	}
	b.characterQueue = b.characterQueue[count:]
	return out.String()
}

func (b *streamLineBuffer) HasPendingCharacters() bool {
	return len(b.characterQueue) > 0
}

func (b *streamLineBuffer) FlushCharacters(width int) string {
	width = maxInt(1, width)
	b.width = width
	safe := b.sanitizer.Flush()
	if safe != "" {
		b.characterQueue = append(b.characterQueue, displayTokens(b.clusterTail+safe)...)
	} else if b.clusterTail != "" {
		b.characterQueue = append(b.characterQueue, displayTokens(b.clusterTail)...)
	}
	b.clusterTail = ""
	out := b.ReleaseCharacters(len(b.characterQueue))
	b.Reset()
	return out
}
func (b *streamLineBuffer) Resize(width int) string {
	width = maxInt(1, width)
	b.width = width
	hidden := b.pending + b.clusterTail
	b.pending = ""
	b.clusterTail = ""
	b.column = 0
	if hidden == "" {
		return ""
	}
	return b.accept(hidden, width, false)
}

func (b *streamLineBuffer) Flush(width int) string {
	width = maxInt(1, width)
	var out strings.Builder
	if b.width != 0 && b.width != width {
		out.WriteString(b.Resize(width))
	}
	b.width = width

	safe := b.sanitizer.Flush()
	out.WriteString(b.accept(safe, width, true))
	if b.clusterTail != "" {
		out.WriteString(b.accept(b.clusterTail, width, true))
		b.clusterTail = ""
	}
	if b.pending != "" {
		out.WriteString(b.pending)
	}
	b.Reset()
	return out.String()
}

func (b *streamLineBuffer) Reset() {
	b.sanitizer.Reset()
	b.pending = ""
	b.clusterTail = ""
	b.column = 0
	b.width = 0
	b.hasContent = false
	b.characterQueue = nil
}

func (b *streamLineBuffer) HasContent() bool {
	return b.hasContent
}

func (b *streamLineBuffer) accept(text string, width int, final bool) string {
	if text == "" && b.clusterTail == "" {
		return ""
	}
	tokens := displayTokens(b.clusterTail + text)
	b.clusterTail = ""
	if !final && len(tokens) > 0 && tokens[len(tokens)-1].kind == displayTokenText {
		b.clusterTail = tokens[len(tokens)-1].text
		tokens = tokens[:len(tokens)-1]
	}

	var out strings.Builder
	for _, token := range tokens {
		switch token.kind {
		case displayTokenNewline:
			out.WriteString(b.pending)
			out.WriteByte('\n')
			b.pending = ""
			b.column = 0
		case displayTokenTab:
			spaces := streamTabWidth - b.column%streamTabWidth
			for range spaces {
				b.appendToken(&out, " ", 1, width)
			}
		default:
			b.appendToken(&out, token.text, token.width, width)
		}
	}
	return out.String()
}

func (b *streamLineBuffer) appendToken(out *strings.Builder, text string, tokenWidth, width int) {
	if tokenWidth > 0 && b.column > 0 && b.column+tokenWidth > width {
		out.WriteString(b.pending)
		b.pending = ""
		b.column = 0
	}
	b.pending += text
	b.column += maxInt(0, tokenWidth)
	if b.column >= width {
		out.WriteString(b.pending)
		b.pending = ""
		b.column = 0
	}
}

func displayTokens(text string) []displayToken {
	tokens := make([]displayToken, 0, len(text))
	for remaining := text; remaining != ""; {
		cluster, width := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		token := displayToken{text: cluster, width: width, kind: displayTokenText}
		switch cluster {
		case "\n":
			token.kind = displayTokenNewline
		case "\t":
			token.kind = displayTokenTab
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func sanitizeTerminalText(text string) string {
	var sanitizer terminalTextSanitizer
	safe := sanitizer.Push(text) + sanitizer.Flush()
	return expandDisplayTabs(safe)
}

func expandDisplayTabs(text string) string {
	if !strings.ContainsRune(text, '\t') {
		return text
	}
	var out strings.Builder
	column := 0
	for _, token := range displayTokens(text) {
		switch token.kind {
		case displayTokenNewline:
			out.WriteByte('\n')
			column = 0
		case displayTokenTab:
			spaces := streamTabWidth - column%streamTabWidth
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		default:
			out.WriteString(token.text)
			column += maxInt(0, token.width)
		}
	}
	return out.String()
}
