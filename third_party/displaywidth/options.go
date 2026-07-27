package displaywidth

// Options controls ambiguous East Asian widths and ANSI parsing.
type Options struct {
	EastAsianWidth       bool
	ControlSequences     bool
	ControlSequences8Bit bool
}

var DefaultOptions Options
