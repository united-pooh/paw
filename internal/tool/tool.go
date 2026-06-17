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

type ConcurrencySafeTool interface {
	IsConcurrencySafe(input json.RawMessage) bool
}
