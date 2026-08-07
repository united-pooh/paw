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

// ToolOutputStream identifies the source stream for a streamed tool output
// event.
type ToolOutputStream string

const (
	ToolOutputStdout ToolOutputStream = "stdout"
	ToolOutputStderr ToolOutputStream = "stderr"
)

// ToolOutputEvent is one chunk of output emitted by an optional streaming
// tool capability.
type ToolOutputEvent struct {
	Stream ToolOutputStream
	Chunk  string
}

// StreamTool is an optional capability for tools that can emit output while
// running. The Tool interface remains the compatibility path for all tools.
type StreamTool interface {
	Stream(ctx context.Context, input json.RawMessage, emit func(ToolOutputEvent) error) (output string, interrupted bool, err error)
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
