package actor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"paw/internal/es"
)

// errSimCrash 是测试专用哨兵：journal 钩子在指定位点抛出以模拟进程
// 死亡（丢弃内存态，仅 journal 存活）。监督器不重启不隔离。
var errSimCrash = errors.New("simulated crash")

// cell 是一个 actor 的运行时宿主：邮箱 + 激活态 + 内存账本镜像。
// 生命周期：route 命中 → ensureActivated（快照+fold+恢复扫描）→ pump
// 串行消费邮箱 → 空闲超时钝化（快照 + 逐出路由表）。
type cell struct {
	id     ActorID
	system *System
	mbox   *mailbox

	mu                sync.Mutex
	activated         bool
	actor             Actor
	ledger            *runtimeLedger
	suspended         bool
	domainSeen        int64 // 自上次快照以来的领域事件数（含已落盘）
	lastSeq           int64
	cancelPassivation func()

	quarantine atomic.Bool
	pumpLock   atomic.Bool  // 防止重复提交 pump 任务
	reactGen   atomic.Int64 // 重激活代数：排队期间过期的 reactivate 任务直接跳过
	poisoned   atomic.Bool  // 模拟崩溃：中止后续处理（测试专用）
}

func newCell(system *System, id ActorID) *cell {
	return &cell{id: id, system: system, mbox: newMailbox(system.mailboxCap)}
}

// ensureActivated 幂等激活：provider 构造 → 快照恢复 → 两段式 fold →
// 恢复扫描（中断消息重投 / Outbox 补发 / 定时器重武装 / 挂起标志）。
func (c *cell) ensureActivated(ctx context.Context) error {
	c.mu.Lock()
	if c.activated {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// 激活在 cell 所属分片上串行执行；activateLocked 自管锁。
	c.system.submitCell(c, func() {
		if err := c.activateLocked(ctx); err != nil {
			c.system.logger("actor: activate %s failed: %v", c.id, err)
			return // 保持未激活；下一条消息重试
		}
		c.pump()
	})
	return nil
}

// activateLocked 完成激活：provider 构造 → 快照恢复 → 两段式 fold →
// 恢复扫描。自管锁；仅允许在 cell 所属分片线程调用。流级死信（隔离
// 终态已持久化）时墓碑化：激活成功但永久拒绝处理。
func (c *cell) activateLocked(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activated {
		return nil
	}
	provider, ok := c.system.providers[c.id.Type]
	if !ok {
		return ErrNoProvider
	}
	actor := provider(c.id)
	ledger := newRuntimeLedger()

	envs, err := c.system.journal.load(ctx, c.id)
	if err != nil {
		return err
	}
	var snapSeq int64
	if sourced, ok := actor.(EventSourced); ok {
		if snap, has, err := c.system.journal.readSnapshot(ctx, c.id); err == nil && has {
			if err := sourced.Restore(snap.State); err != nil {
				return err
			}
			snapSeq = snap.Seq
		}
	}
	var domainSeen int64
	for _, env := range envs {
		if env.Seq <= snapSeq {
			continue
		}
		if env.Kind == es.KindRuntime {
			if err := ledger.foldRuntime(env); err != nil {
				return err
			}
			continue
		}
		// 两段式 fold 第二段：domain 只进 actor（I5）。
		if sourced, ok := actor.(EventSourced); ok {
			if err := sourced.Fold(env); err != nil {
				return err
			}
		}
		domainSeen++
		if env.Seq > ledger.lastSeq {
			ledger.lastSeq = env.Seq
		}
	}

	c.actor = actor
	c.ledger = ledger
	c.activated = true
	c.suspended = ledger.suspended
	c.domainSeen = domainSeen
	c.lastSeq = ledger.lastSeq

	// 流级死信：墓碑化（跨重启持久，不再处理任何消息）。
	if ledger.quarantined {
		c.quarantine.Store(true)
		return nil
	}

	// 恢复动作（spec §6.2 / §7）。
	for _, msg := range ledger.pendingInbox() {
		_ = c.mbox.push(delivery{msg: msg}) // 激活期邮箱空，不会满
	}
	for _, entry := range ledger.outbox {
		c.system.routeAfterRecovery(entry.Target, entry.Msg)
	}
	for _, t := range ledger.timers {
		c.system.wheel.arm(c.id, t.TimerID, t.After.Duration(), t.Msg)
	}
	return nil
}

// pump 在 cell 分片上串行消费邮箱至空。挂起态只消费控制任务。
func (c *cell) pump() {
	c.mu.Lock()
	if c.cancelPassivation != nil {
		c.cancelPassivation()
		c.cancelPassivation = nil
	}
	c.mu.Unlock()
	if !c.pumpLock.CompareAndSwap(false, true) {
		return // 已有 pump 在跑或排队
	}
	c.system.submitCell(c, func() {
		defer func() {
			c.pumpLock.Store(false)
			// 复位后复查：释放窗口内新入队的消息可能无人泵送，补一轮。
			// 隔离/挂起态不补（隔离终态不处理；挂起消息有意驻留）。
			if c.quarantine.Load() {
				return
			}
			c.mu.Lock()
			suspended := c.suspended
			c.mu.Unlock()
			if !suspended && c.mbox.len() > 0 {
				c.pump()
			}
		}()
		for {
			if c.quarantine.Load() || c.poisoned.Load() {
				return
			}
			c.mu.Lock()
			suspended := c.suspended
			c.mu.Unlock()
			if suspended {
				return // 消息滞留邮箱，Resume 重新泵送
			}
			item, ok := c.mbox.pop()
			if !ok {
				c.schedulePassivation()
				return
			}
			if item.control != nil {
				item.control()
				continue
			}
			c.processOne(item.msg)
		}
	})
}

// schedulePassivation 空闲到达钝化阈值时：快照 → 逐出路由表。
func (c *cell) schedulePassivation() {
	if c.system.passivateAfter <= 0 {
		return
	}
	cancel := c.system.clock.After(c.system.passivateAfter, func() {
		c.system.submitCell(c, func() {
			if c.mbox.len() > 0 || c.quarantine.Load() {
				return
			}
			c.snapshot(context.Background())
			c.system.router.evict(c.id, c)
		})
	})
	c.mu.Lock()
	c.cancelPassivation = cancel
	c.mu.Unlock()
}

// snapshot 写快照（快照是缓存，失败不致命，仅记录）。
func (c *cell) snapshot(ctx context.Context) {
	c.mu.Lock()
	actor, lastSeq := c.actor, c.lastSeq
	c.mu.Unlock()
	esr, ok := actor.(EventSourced)
	if !ok || actor == nil {
		return
	}
	state, err := esr.Snapshot()
	if err != nil {
		c.system.logger("actor: snapshot %s state: %v", c.id, err)
		return
	}
	if err := c.system.journal.writeSnapshot(ctx, c.id, lastSeq, state); err != nil {
		c.system.logger("actor: snapshot %s write: %v", c.id, err)
		return
	}
	c.mu.Lock()
	c.domainSeen = 0 // 以快照点为基线重新计数
	c.mu.Unlock()
	c.system.journal.fire(StageSnapshotted, c.id)
}

// reactivate 丢弃内存态（journal 为唯一事实源），按监督策略重建。
// 代数守卫：多个 reactivate 任务排队时，仅最新代执行，避免 pending
// 消息被重复重投（旧任务捕获旧代数，检测到代数落后即跳过）。
func (c *cell) reactivate() {
	gen := c.reactGen.Add(1)
	c.system.submitCell(c, func() {
		if c.quarantine.Load() || c.reactGen.Load() != gen {
			return
		}
		c.mu.Lock()
		c.activated = false
		c.actor = nil
		c.ledger = nil
		c.suspended = false
		c.mu.Unlock()
		if err := c.activateLocked(context.Background()); err != nil {
			c.system.logger("actor: reactivate %s failed: %v", c.id, err)
			return
		}
		c.pump()
	})
}
