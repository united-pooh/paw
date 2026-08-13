package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const updateTodoInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "explanation": {"type": "string"},
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "id": {"type": "string"},
          "content": {"type": "string"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
        },
        "required": ["id", "content", "status"]
      }
    }
  },
  "required": ["items"]
}`

type Tool struct {
	broker *Broker
	nowFn  func() time.Time
	// OnUpsert 在快照发布后调用（事件溯源接线：写入 session.todo_upserted
	// 事件）。nil 时跳过；错误由调用方决定处理（接线侧 best-effort）。
	OnUpsert func(ctx context.Context, snapshot Snapshot) error
}

func NewTool(broker *Broker) *Tool {
	return &Tool{broker: broker, nowFn: time.Now}
}

func (*Tool) Name() string { return "update_todo" }

func (*Tool) Description() string {
	return "Maintain a full todo snapshot for complex multi-step work. Use it before substantial execution and when status materially changes; do not create a todo list for simple questions or one-step edits. Submit the complete ordered list every time, preserve stable item ids, use only pending/in_progress/completed, and keep at most one item in_progress."
}

func (*Tool) InputSchema() json.RawMessage {
	return json.RawMessage(updateTodoInputSchema)
}

func (*Tool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input, err := DecodeUpdateInput(raw)
	if err != nil {
		return "", err
	}
	nowFn := time.Now
	if t != nil && t.nowFn != nil {
		nowFn = t.nowFn
	}
	snapshot := Snapshot{
		Explanation: input.Explanation,
		Items:       append([]Item{}, input.Items...),
		UpdatedAt:   nowFn().UTC(),
	}
	if t != nil && t.broker != nil {
		t.broker.Publish(snapshot)
	}
	if t != nil && t.OnUpsert != nil {
		_ = t.OnUpsert(ctx, snapshot)
	}
	data, err := json.Marshal(UpdateResult{Accepted: true, Snapshot: snapshot})
	if err != nil {
		return "", fmt.Errorf("encode update_todo result: %w", err)
	}
	return string(data), nil
}
