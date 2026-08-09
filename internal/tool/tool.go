package tool

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, input json.RawMessage) (string, error)
	InputSchema() json.RawMessage
}

// StreamTool is an optional capability for tools that stream output while
// running, enabling interruption support (context cancellation returns the
// output collected so far and interrupted=true). The Tool interface remains
// the compatibility path for all tools.
type StreamTool interface {
	Stream(ctx context.Context, input json.RawMessage) (output string, interrupted bool, err error)
}

type ConcurrencySafeTool interface {
	IsConcurrencySafe(input json.RawMessage) bool
}

type SnipHint struct {
	Head      int
	Tail      int
	HeadChars int
	TailChars int
}

type SnipHinter interface {
	SnipHint() SnipHint
}

type ReadOnlyTool interface {
	ReadOnly() bool
}

// FileMutationTarget describes the resolved workspace path a tool may mutate
// and whether it exists before the mutation runs.
type FileMutationTarget struct {
	Path         string
	BeforeExists bool
}

// FileMutationTool is an optional capability used by the runner to safely
// inspect a registered tool's mutation target without executing the tool.
type FileMutationTool interface {
	FileMutationTarget(input json.RawMessage) (FileMutationTarget, error)
}
