package actor

import (
	"context"
	"time"
)

// sysRef 是 System 级 Ref 实现。
type sysRef struct {
	s  *System
	id ActorID
}

func (r sysRef) Tell(ctx context.Context, msg Msg) error { return r.s.Tell(ctx, r.id, msg) }

func (r sysRef) Ask(ctx context.Context, msg Msg, timeout time.Duration) (Msg, error) {
	if msg.MsgID == "" {
		msg.MsgID = newMsgID()
	}
	ch := make(chan Msg, 1)
	r.s.mu.Lock()
	r.s.promises[msg.MsgID] = ch
	r.s.mu.Unlock()
	if err := r.s.Tell(ctx, r.id, msg); err != nil {
		r.s.mu.Lock()
		delete(r.s.promises, msg.MsgID)
		r.s.mu.Unlock()
		return Msg{}, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case reply := <-ch:
		return reply, nil
	case <-timer.C:
		return Msg{}, ErrAskTimeout // 仅放弃等待，消息继续处理（ADR-3）
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}
}

// deliverPromise 投递 Ask 回执（按 MsgID 去重：首个到达者生效）。
func (s *System) deliverPromise(correlation string, msg Msg) {
	s.mu.Lock()
	ch, ok := s.promises[correlation]
	if ok {
		delete(s.promises, correlation)
	}
	s.mu.Unlock()
	if ok {
		select {
		case ch <- msg:
		default: // Ask 已超时放弃，回执丢弃（ADR-3）
		}
	}
}
