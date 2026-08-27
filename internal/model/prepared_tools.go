package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"paw/internal/message"
)

// PreparedTool is the request-level, transport-independent representation of
// one model-facing function. Projection and validator are intentionally local:
// only the wire schema and strict flag are serialized.
type PreparedTool struct {
	Name           string
	Description    string
	Parameters     json.RawMessage
	Strict         bool
	OriginalSchema json.RawMessage

	projection *argumentProjection
	validator  *jsonschema.Schema
	schema     map[string]any
}

type PreparedToolSet []PreparedTool

func preparePassthroughTools(tools []ToolDefinition) PreparedToolSet {
	prepared := make(PreparedToolSet, 0, len(tools))
	for _, tool := range tools {
		schema := append(json.RawMessage(nil), tool.InputSchema...)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		prepared = append(prepared, PreparedTool{
			Name: tool.Name, Description: tool.Description,
			Parameters:     append(json.RawMessage(nil), schema...),
			OriginalSchema: append(json.RawMessage(nil), schema...),
		})
	}
	return prepared
}

func (tools PreparedToolSet) clone() PreparedToolSet {
	cloned := make(PreparedToolSet, len(tools))
	copy(cloned, tools)
	for i := range cloned {
		cloned[i].Parameters = append(json.RawMessage(nil), tools[i].Parameters...)
		cloned[i].OriginalSchema = append(json.RawMessage(nil), tools[i].OriginalSchema...)
	}
	return cloned
}

func preparedToolCacheKey(adapter ModelAdapter, tools []ToolDefinition) string {
	h := sha256.New()
	_, _ = h.Write([]byte(adapter.Name()))
	for _, tool := range tools {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(tool.Name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(tool.Description))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(tool.InputSchema)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Client) prepareTools(adapter ModelAdapter, definitions []ToolDefinition) (PreparedToolSet, error) {
	key := preparedToolCacheKey(adapter, definitions)
	c.toolCacheMu.Lock()
	defer c.toolCacheMu.Unlock()
	if key == c.toolCacheKey {
		return c.toolCache.clone(), nil
	}
	prepared, err := adapter.PrepareTools(definitions)
	if err != nil {
		return nil, err
	}
	c.toolCacheKey = key
	c.toolCache = prepared.clone()
	return prepared.clone(), nil
}

func (tools PreparedToolSet) adaptCalls(calls []message.ToolCall) []message.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	byName := make(map[string]PreparedTool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	adapted := make([]message.ToolCall, len(calls))
	for i, call := range calls {
		adapted[i] = call
		adapted[i].Input = append(json.RawMessage(nil), call.Input...)
		if strings.TrimSpace(call.InputError) != "" {
			continue
		}
		tool, ok := byName[call.Name]
		if !ok || (tool.projection == nil && tool.validator == nil) {
			continue
		}
		input, err := restoreToolArguments(tool, call.Input)
		if err != nil {
			adapted[i].InputError = fmt.Sprintf("工具参数恢复或校验失败，可修正参数后重试: %v", err)
			continue
		}
		adapted[i].Input = input
	}
	return adapted
}

func restoreToolArguments(tool PreparedTool, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("arguments 不是有效 JSON: %w", err)
	}
	if tool.projection != nil {
		var err error
		value, err = tool.projection.restore(value)
		if err != nil {
			return nil, err
		}
	}
	if tool.validator != nil {
		if coerced, changed, err := coerceValueToSchema(value, tool.schema); err != nil {
			return nil, fmt.Errorf("参数类型修正失败: %w", err)
		} else if changed {
			value = coerced
		}
		if err := tool.validator.Validate(value); err != nil {
			return nil, fmt.Errorf("不符合原始 Schema: %w", err)
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("恢复后的 arguments 无法编码: %w", err)
	}
	return json.RawMessage(data), nil
}

func adaptPreparedToolEvents(ctx context.Context, input <-chan StreamEvent, tools PreparedToolSet) <-chan StreamEvent {
	output := make(chan StreamEvent)
	go func() {
		defer close(output)
		for event := range input {
			if len(event.ToolCalls) != 0 {
				event.ToolCalls = tools.adaptCalls(event.ToolCalls)
			}
			select {
			case <-ctx.Done():
				return
			case output <- event:
			}
		}
	}()
	return output
}
