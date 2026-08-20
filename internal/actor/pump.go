package actor

import "context"

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
			// 隔离/挂起/未激活不补（隔离终态不处理；挂起消息有意驻留；
			// 未激活时消息驻留等待激活，补泵只会空转）。
			if c.quarantine.Load() {
				return
			}
			c.mu.Lock()
			suspended := c.suspended
			activated := c.activated
			c.mu.Unlock()
			if !suspended && activated && c.mbox.len() > 0 {
				c.pump()
			}
		}()
		for {
			if c.quarantine.Load() || c.poisoned.Load() {
				return
			}
			c.mu.Lock()
			suspended := c.suspended
			activated := c.activated
			c.mu.Unlock()
			if suspended {
				return // 消息滞留邮箱，Resume 重新泵送
			}
			if !activated {
				// 激活未完成（首次激活失败/重激活进行中或失败）：消息驻留
				// 邮箱，由下次 route 或成功激活后的 pump 接手。此时消费会以
				// nil actor/ledger 触发 panic 和监督重启风暴。
				return
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
