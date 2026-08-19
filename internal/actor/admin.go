package actor

import (
	"context"
	"fmt"

	"paw/internal/es"
)

// Suspend 挂起 actor：消息滞留邮箱，等待外部决策（human-in-the-loop
// 场景，如权限审批）。挂起标志经 sys.suspended 持久化，跨重启生效。
func (s *System) Suspend(id ActorID, reason string) error {
	select {
	case <-s.stopped:
		return ErrStopped
	default:
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if _, ok := s.providers[id.Type]; !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, id.Type)
	}
	c := s.router.ensure(s, id)
	if err := c.ensureActivated(context.Background()); err != nil {
		return err
	}
	return c.suspend(reason)
}

// Activate 重建 actor 及其账本但不改变挂起状态（幂等；宿主在读取
// Durable 状态前调用，确保后续投递落在已激活的 cell 上）。
func (s *System) Activate(id ActorID) error {
	select {
	case <-s.stopped:
		return ErrStopped
	default:
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if _, ok := s.providers[id.Type]; !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, id.Type)
	}
	c := s.router.ensure(s, id)
	return c.ensureActivated(context.Background())
}

// PersistDomain 追加一条 Durable 领域事件而不改变活动 actor 的内存
// 状态。同步宿主端口可在 Receive 内调用它落事实（如 tool.started）；
// 活动状态变更仍必须经 actor 邮箱，激活与快照从 Durable 流重建。
func (s *System) PersistDomain(ctx context.Context, id ActorID, eventType string, payload any) error {
	select {
	case <-s.stopped:
		return ErrStopped
	default:
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if _, ok := s.providers[id.Type]; !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, id.Type)
	}
	env, err := s.journal.append(ctx, id, es.KindDomain, eventType, payload)
	if err != nil {
		return err
	}
	if c := s.router.get(id); c != nil {
		c.noteSeq(env.Seq)
		c.noteDomainEvents(1)
	}
	return nil
}

// Resume 恢复挂起的 actor（human-in-the-loop 决策完成后调用）。
// 冷路径：cell 未激活时直接读 journal 判定挂起标志，避免误激活
// 非挂起 actor。
func (s *System) Resume(id ActorID) error {
	select {
	case <-s.stopped:
		return ErrStopped
	default:
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if _, ok := s.providers[id.Type]; !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, id.Type)
	}
	c := s.router.get(id)
	if c == nil {
		envelopes, err := s.journal.load(context.Background(), id)
		if err != nil {
			return err
		}
		ledger := newRuntimeLedger()
		for _, envelope := range envelopes {
			if envelope.Kind == es.KindRuntime {
				if err := ledger.foldRuntime(envelope); err != nil {
					return err
				}
			}
		}
		if len(envelopes) == 0 || !ledger.suspended {
			return fmt.Errorf("actor: %s is not active or suspended", id)
		}
		c = s.router.ensure(s, id)
	}
	_ = c.ensureActivated(context.Background())
	return c.resume()
}
