package plan

import (
	"encoding/json"
	"fmt"
	"time"

	"paw/internal/es"
)

// plan 域持久化事件类型。plan.baseline 仅迁移导入使用（一次性）；
// 会话运行时通知（plan 的 Event* 运行时事件）不进入事件流。
const (
	EventCreated       = "plan.created"
	EventBaseline      = "plan.baseline"
	EventDocUpdated    = "plan.doc_updated"
	EventStatusChanged = "plan.status_changed"
)

type createdPayload struct {
	PlanID    string     `json:"plan_id"`
	Title     string     `json:"title"`
	Path      string     `json:"path,omitempty"`
	Content   string     `json:"content"`
	Status    PlanStatus `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type docUpdatedPayload struct {
	Title     string    `json:"title"`
	Path      string    `json:"path,omitempty"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

type statusChangedPayload struct {
	Status    SessionStatus `json:"status"`
	Reason    PauseReason   `json:"reason,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// RegisterEvents 将 plan 域事件类型注册到 es.Registry。
func RegisterEvents(reg *es.Registry) error {
	specs := []es.TypeSpec{
		{
			Type: EventCreated,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				var p createdPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				return p, nil
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(createdPayload)
				if !ok {
					return fmt.Errorf("plan: payload type mismatch for %s", EventCreated)
				}
				if pp.PlanID == "" || pp.Title == "" || pp.Content == "" {
					return fmt.Errorf("plan: %s requires plan_id/title/content", EventCreated)
				}
				return nil
			},
		},
		{
			Type: EventBaseline,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				var p createdPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				return p, nil
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(createdPayload)
				if !ok {
					return fmt.Errorf("plan: payload type mismatch for %s", EventBaseline)
				}
				if pp.PlanID == "" || pp.Title == "" || pp.Content == "" {
					return fmt.Errorf("plan: %s requires plan_id/title/content", EventBaseline)
				}
				return nil
			},
		},
		{
			Type: EventDocUpdated,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				var p docUpdatedPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				return p, nil
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(docUpdatedPayload)
				if !ok {
					return fmt.Errorf("plan: payload type mismatch for %s", EventDocUpdated)
				}
				if pp.Content == "" {
					return fmt.Errorf("plan: %s requires content", EventDocUpdated)
				}
				return nil
			},
		},
		{
			Type: EventStatusChanged,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				var p statusChangedPayload
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				return p, nil
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(statusChangedPayload)
				if !ok {
					return fmt.Errorf("plan: payload type mismatch for %s", EventStatusChanged)
				}
				if pp.Status == "" {
					return fmt.Errorf("plan: %s requires status", EventStatusChanged)
				}
				return nil
			},
		},
	}
	for _, spec := range specs {
		if err := reg.Register(spec); err != nil {
			return err
		}
	}
	return nil
}
