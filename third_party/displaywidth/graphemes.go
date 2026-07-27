package displaywidth

import "github.com/clipperhouse/uax29/v2/graphemes"

// Graphemes is an iterator over grapheme clusters.
type Graphemes[T ~string | []byte] struct {
	iter    *graphemes.Iterator[T]
	options Options
}

// Next advances the iterator to the next grapheme cluster.
func (g *Graphemes[T]) Next() bool {
	return g.iter.Next()
}

// Value returns the current grapheme cluster.
func (g *Graphemes[T]) Value() T {
	return g.iter.Value()
}

// Width returns the display width of the current grapheme cluster.
func (g *Graphemes[T]) Width() int {
	return graphemeWidth(g.options, g.Value())
}

// StringGraphemes returns an iterator over grapheme clusters for s.
func StringGraphemes(s string) Graphemes[string] {
	return DefaultOptions.StringGraphemes(s)
}

// StringGraphemes returns an iterator over grapheme clusters for s using the
// receiver's options.
func (options Options) StringGraphemes(s string) Graphemes[string] {
	g := graphemes.FromString(s)
	g.AnsiEscapeSequences = options.ControlSequences
	g.AnsiEscapeSequences8Bit = options.ControlSequences8Bit
	return Graphemes[string]{iter: g, options: options}
}

// BytesGraphemes returns an iterator over grapheme clusters for s.
func BytesGraphemes(s []byte) Graphemes[[]byte] {
	return DefaultOptions.BytesGraphemes(s)
}

// BytesGraphemes returns an iterator over grapheme clusters for s using the
// receiver's options.
func (options Options) BytesGraphemes(s []byte) Graphemes[[]byte] {
	g := graphemes.FromBytes(s)
	g.AnsiEscapeSequences = options.ControlSequences
	g.AnsiEscapeSequences8Bit = options.ControlSequences8Bit
	return Graphemes[[]byte]{iter: g, options: options}
}
