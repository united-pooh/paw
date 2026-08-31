package app

import (
	"context"
	"encoding/json"

	"paw/internal/mcp"
	"paw/internal/message"
)

type emptyMCPBroker struct{}

func (emptyMCPBroker) Snapshot() mcp.Snapshot { return mcp.Snapshot{} }
func (emptyMCPBroker) Call(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}

func testUserMessage(content string) message.Message {
	return message.Message{Role: message.RoleUser, Content: content}
}
