package goal

import (
	"encoding/json"
	"fmt"
	"time"

	"paw/internal/es"
)

// goal 域持久化事件类型。运行时 EventType 常量（events.go）中与状态机
// 相关的类型复用同一常量（goal.started 等）；以下为事件溯源新增类型。
// 纯进度通知（goal.turn.completed / goal.continued / goal.task.started /
// goal.compacted / goal.input.received）与独立 store（evidence / checkpoint）
// 不进入事件流：前者只走运行时 EventSink，后者保持独立存储，见设计文档 5.1。
const (
	EventCreated     = "goal.created"
	EventReplanned   = "goal.replanned"
	EventDeleted     = "goal.deleted"
	EventStatsUpdate = "goal.stats.updated"
)

// ev* 是无类型 string 别名，供注册表与 Apply 在 string 上下文使用
// （events.go 的同名 EventType 常量是类型化常量，需显式转换）。
const (
	evCreated     = EventCreated
	evStarted     = string(EventStarted)
	evPaused      = string(EventPaused)
	evBlocked     = string(EventBlocked)
	evResumed     = string(EventResumed)
	evReplanned   = EventReplanned
	evCompleted   = string(EventCompleted)
	evFailed      = string(EventFailed)
	evCancelled   = string(EventCancelled)
	evDeleted     = EventDeleted
	evStatsUpdate = EventStatsUpdate
)

type createdPayload struct {
	GoalID             string             `json:"goal_id"`
	SessionID          string             `json:"session_id"`
	Objective          string             `json:"objective"`
	AcceptanceCriteria []string           `json:"acceptance_criteria,omitempty"`
	Verification       []VerificationSpec `json:"verification,omitempty"`
	Budget             GoalBudget         `json:"budget"`
	Status             GoalStatus         `json:"status"`
	CreatedAt          time.Time          `json:"created_at"`
}

type reasonPayload struct {
	Reason PauseReason `json:"reason"`
}

type failedPayload struct {
	Reason string `json:"reason,omitempty"`
}

type statsPayload struct {
	TurnsUsed        int    `json:"turns_used"`
	ToolCallsUsed    int    `json:"tool_calls_used"`
	NoProgressCount  int    `json:"no_progress_count"`
	ContinuationUsed int    `json:"continuation_used"`
	CurrentTaskID    string `json:"current_task_id,omitempty"`
	LastDecision     string `json:"last_decision,omitempty"`
}

func decodePayload[T any](raw json.RawMessage) (es.Payload, error) {
	var p T
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return p, nil
}

func errPayloadType(typ string) error {
	return fmt.Errorf("goal: payload type mismatch for %s", typ)
}

func errField(typ, field string) error {
	return fmt.Errorf("goal: %s requires %s", typ, field)
}

// RegisterEvents 将 goal 域事件类型注册到 es.Registry。注册失败（重复类型）
// 返回错误，注册表保持原状。
func RegisterEvents(reg *es.Registry) error {
	specs := []es.TypeSpec{
		{
			Type: evCreated,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				return decodePayload[createdPayload](raw)
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(createdPayload)
				if !ok {
					return errPayloadType(evCreated)
				}
				if pp.GoalID == "" {
					return errField(evCreated, "goal_id")
				}
				if pp.SessionID == "" {
					return errField(evCreated, "session_id")
				}
				if pp.Objective == "" {
					return errField(evCreated, "objective")
				}
				return nil
			},
		},
		{
			Type:   evStarted,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type: evPaused,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				return decodePayload[reasonPayload](raw)
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(reasonPayload)
				if !ok {
					return errPayloadType(evPaused)
				}
				if pp.Reason == "" {
					return errField(evPaused, "reason")
				}
				return nil
			},
		},
		{
			Type: evBlocked,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				return decodePayload[reasonPayload](raw)
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(reasonPayload)
				if !ok {
					return errPayloadType(evBlocked)
				}
				if pp.Reason == "" {
					return errField(evBlocked, "reason")
				}
				return nil
			},
		},
		{
			Type:   evResumed,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type:   evReplanned,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type:   evCompleted,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type: evFailed,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				return decodePayload[failedPayload](raw)
			},
		},
		{
			Type:   evCancelled,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type:   evDeleted,
			Decode: func(raw json.RawMessage) (es.Payload, error) { return struct{}{}, nil },
		},
		{
			Type: evStatsUpdate,
			Decode: func(raw json.RawMessage) (es.Payload, error) {
				return decodePayload[statsPayload](raw)
			},
			Validate: func(p es.Payload) error {
				pp, ok := p.(statsPayload)
				if !ok {
					return errPayloadType(evStatsUpdate)
				}
				if pp.TurnsUsed < 0 || pp.ToolCallsUsed < 0 || pp.NoProgressCount < 0 || pp.ContinuationUsed < 0 {
					return errField(evStatsUpdate, "non-negative counters")
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
