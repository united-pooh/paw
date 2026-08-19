package sessionactor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"paw/internal/actor"
	"paw/internal/loop"
)

// replyError 立即应答错误，解除 Ask 方等待（否则将悬到超时）。
func (a *SessionActor) replyError(ctx *actor.Context, kind string, err error) {
	ctx.Reply(actor.Msg{Kind: kind, Payload: mutationError{Error: err.Error()}, Durability: actor.Ephemeral})
}

// SessionActor is the durable mailbox around one session's Engine execution.
type SessionActor struct {
	id    actor.ActorID
	host  *Host
	state State
}

func newSessionActor(id actor.ActorID, host *Host) *SessionActor {
	return &SessionActor{id: id, host: host}
}

func (a *SessionActor) ID() actor.ActorID { return a.id }

func (a *SessionActor) Receive(ctx *actor.Context, msg actor.Msg) {
	switch msg.Kind {
	case msgRunTurn:
		var request turnRequest
		if err := decodePayload(msg.Payload, &request); err != nil {
			ctx.Reply(actor.Msg{Kind: msgRunTurn, Payload: turnReply{Error: err.Error()}, Durability: actor.Ephemeral})
			return
		}
		execution, err := a.host.execute(a.id.Key, msg.MsgID, request)
		if err != nil {
			_ = ctx.Persist(EventTurnInterrupted, interruptedTurn{TurnID: request.Timing.TurnID, Error: err.Error(), At: time.Now().UTC()}, actor.Durable)
			ctx.Reply(actor.Msg{Kind: msgRunTurn, Payload: turnReply{Execution: execution, Error: err.Error()}, Durability: actor.Ephemeral})
			return
		}
		ctx.Reply(actor.Msg{Kind: msgRunTurn, Payload: turnReply{Execution: execution}, Durability: actor.Ephemeral})
	case msgGetState:
		ctx.Reply(actor.Msg{Kind: msgGetState, Payload: a.state.clone(), Durability: actor.Ephemeral})
	case msgGoalBind:
		var binding goalBinding
		if err := decodePayload(msg.Payload, &binding); err != nil {
			a.replyError(ctx, msgGoalBind, err)
			return
		}
		if binding.GoalID == "" {
			a.replyError(ctx, msgGoalBind, fmt.Errorf("goal id is required"))
			return
		}
		if err := ctx.Persist(EventGoalActivated, binding, actor.Durable); err != nil {
			a.replyError(ctx, msgGoalBind, err)
			return
		}
		a.state.ActiveGoalID, a.state.GoalStatus = binding.GoalID, binding.Status
		ctx.Reply(actor.Msg{Kind: msgGoalBind, Payload: a.state.clone(), Durability: actor.Ephemeral})
	case msgGoalClear:
		if err := ctx.Persist(EventGoalCleared, map[string]string{}, actor.Durable); err != nil {
			a.replyError(ctx, msgGoalClear, err)
			return
		}
		a.state.ActiveGoalID, a.state.GoalStatus = "", ""
		ctx.Reply(actor.Msg{Kind: msgGoalClear, Payload: a.state.clone(), Durability: actor.Ephemeral})
	case msgPlanSave:
		var binding planBinding
		if err := decodePayload(msg.Payload, &binding); err != nil {
			a.replyError(ctx, msgPlanSave, err)
			return
		}
		if binding.PlanID == "" {
			a.replyError(ctx, msgPlanSave, fmt.Errorf("plan id is required"))
			return
		}
		if err := ctx.Persist(EventPlanSnapshot, binding, actor.Durable); err != nil {
			a.replyError(ctx, msgPlanSave, err)
			return
		}
		a.state.ActivePlanID, a.state.PlanStatus = binding.PlanID, binding.Status
		a.state.PlanSnapshot = append(json.RawMessage(nil), binding.Snapshot...)
		ctx.Reply(actor.Msg{Kind: msgPlanSave, Payload: a.state.clone(), Durability: actor.Ephemeral})
	case msgPlanClear:
		if err := ctx.Persist(EventPlanCleared, map[string]string{}, actor.Durable); err != nil {
			a.replyError(ctx, msgPlanClear, err)
			return
		}
		a.state.ActivePlanID, a.state.PlanStatus, a.state.PlanSnapshot = "", "", nil
		ctx.Reply(actor.Msg{Kind: msgPlanClear, Payload: a.state.clone(), Durability: actor.Ephemeral})
	case msgPermissionDecision:
		var record permissionRecord
		if err := decodePayload(msg.Payload, &record); err != nil || record.ID == "" {
			return
		}
		if record.Decision != loop.PermissionAllowOnce && record.Decision != loop.PermissionDeny {
			return
		}
		if index := a.state.permissionIndex(record.ID); index >= 0 {
			if a.state.Permissions[index].Decision != "" {
				return
			}
		}
		a.applyPermissionDecision(record)
	}
}

func (a *SessionActor) Snapshot() (json.RawMessage, error) {
	if a.host != nil {
		state, err := a.host.State(context.Background(), a.id.Key)
		if err != nil {
			return nil, err
		}
		return json.Marshal(state)
	}
	return json.Marshal(a.state)
}
func (a *SessionActor) Restore(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, &a.state)
}
func (a *SessionActor) State() any { return a.state.clone() }

func decodePayload(input any, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode session actor payload: %w", err)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode session actor payload: %w", err)
	}
	return nil
}
