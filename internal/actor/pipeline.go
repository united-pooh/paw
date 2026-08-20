package actor

import (
	"context"
	"fmt"

	"paw/internal/es"
)

// processOne 执行 Journal-First 消息处理协议（spec §6.1）：
//
//	① sys.inbox.received 落盘（Ephemeral 消息跳过 ①③）
//	② Receive：ctx.Persist/ctx.Send（Outbox 先落盘，I2）
//	③ sys.inbox.done 落盘
//	④ 每 K 条领域事件：快照
//
// panic 交由监督器（退避重启 / 死信隔离）。
func (c *cell) processOne(msg Msg) {
	if c.quarantine.Load() || c.poisoned.Load() {
		return
	}
	c.mu.Lock()
	ready := c.activated && c.actor != nil && c.ledger != nil
	c.mu.Unlock()
	if !ready {
		// pump 的激活守卫是主防线，此处兜底：激活失败或重激活窗口内
		// 绝不以 nil actor/ledger 消费（否则 nil pointer panic → 监督
		// 退避重启 → 激活再次失败的重启风暴）。
		return
	}
	ctx := &Context{sys: c.system, cell: c, msg: msg}
	defer func() {
		if r := recover(); r != nil {
			if fmt.Sprint(r) == errSimCrash.Error() {
				// 模拟崩溃：中止本 cell 一切后续处理，等价进程死亡。
				c.poisoned.Store(true)
				return
			}
			c.system.supervisor.onPanic(c, msg, fmt.Errorf("%v", r))
		}
	}()

	if msg.Durability != Ephemeral {
		if msg.MsgID == "" {
			msg.MsgID = newMsgID()
			ctx.msg = msg
		}
		// 账本去重：已完结消息直接丢弃（跨重投/Outbox 补发的幂等键）。
		if c.ledger.isDone(msg.MsgID) {
			return
		}
		if env, err := c.system.journal.appendSys(context.Background(), c.id, sysInboxReceived, inboxReceivedPayload{
			MsgID: msg.MsgID, Msg: mustMarshalMsg(msg),
		}); err != nil {
			c.system.logger("actor: journal received %s: %v", c.id, err)
			return
		} else {
			c.noteSeq(env.Seq)
		}
		c.system.journal.fire(StageInboxReceived, c.id)
		c.ledger.received[msg.MsgID] = msg
	} else if msg.MsgID == "" {
		msg.MsgID = newMsgID()
		ctx.msg = msg
	}

	c.actor.Receive(ctx, msg)

	// ② 组提交：本条消息缓冲的领域事件一次落盘。
	if len(ctx.buffer) > 0 {
		if _, last, err := c.system.journal.appendDomain(context.Background(), c.id, ctx.buffer); err != nil {
			c.system.logger("actor: journal domain flush %s: %v", c.id, err)
		} else {
			c.noteDomainEvents(int64(len(ctx.buffer)))
			c.noteSeq(last)
		}
		ctx.buffer = nil
		c.system.journal.fire(StageDomainFlushed, c.id)
	}

	if msg.Durability != Ephemeral {
		if env, err := c.system.journal.appendSys(context.Background(), c.id, sysInboxDone, inboxDonePayload{MsgID: msg.MsgID}); err != nil {
			c.system.logger("actor: journal done %s: %v", c.id, err)
			return
		} else {
			c.noteSeq(env.Seq)
		}
		c.system.journal.fire(StageInboxDone, c.id)
		c.ledger.done[msg.MsgID] = true
	}

	// ④ 快照间隔。
	c.maybeSnapshot()
}

// noteDomainEvents 记录已落盘领域事件数并推进 seq 视图。
func (c *cell) noteDomainEvents(n int64) {
	c.mu.Lock()
	c.domainSeen += n
	c.mu.Unlock()
}

// noteSeq 以 journal 分配的 seq 为准推进（消灭手工计数漂移）。
func (c *cell) noteSeq(seq int64) {
	c.mu.Lock()
	if seq > c.lastSeq {
		c.lastSeq = seq
	}
	c.mu.Unlock()
}

func (c *cell) maybeSnapshot() {
	c.mu.Lock()
	due := c.domainSeen >= int64(c.system.snapshotInterval)
	c.mu.Unlock()
	if due {
		c.snapshot(context.Background())
	}
}

// sys payload 结构体（journal 落盘形态）。
type inboxReceivedPayload struct {
	MsgID string  `json:"msg_id"`
	Msg   msgJSON `json:"msg"`
}

type inboxDonePayload struct {
	MsgID string `json:"msg_id"`
}

type outboxSentPayload struct {
	MsgID  string  `json:"msg_id"`
	Target string  `json:"target"`
	Msg    msgJSON `json:"msg"`
}

type outboxDeliveredPayload struct {
	MsgID string `json:"msg_id"`
}

type timerRegisteredPayload struct {
	TimerID string  `json:"timer_id"`
	AfterMs int64   `json:"after_ms"`
	Msg     msgJSON `json:"msg"`
}

type timerFiredPayload struct {
	TimerID string `json:"timer_id"`
}

func mustMarshalMsg(m Msg) msgJSON {
	encoded, err := marshalMsg(m)
	if err != nil {
		return msgJSON{MsgID: m.MsgID, Kind: m.Kind, Payload: []byte("null")}
	}
	return encoded
}

// sendSelf 落 outbox 并投递到目标（I2：先落盘后投递）。
func (c *cell) send(target ActorID, msg Msg) error {
	if msg.MsgID == "" {
		msg.MsgID = newMsgID()
	}
	encoded := mustMarshalMsg(msg)
	payload := outboxSentPayload{MsgID: msg.MsgID, Target: target.String(), Msg: encoded}
	env, err := c.system.journal.appendSys(context.Background(), c.id, sysOutboxSent, payload)
	if err != nil {
		return err
	}
	c.noteSeq(env.Seq)
	c.system.journal.fire(StageOutboxSent, c.id)
	if err := c.system.route(target, msg); err != nil {
		// 未投出：账本留 pending，激活时补发（事务性 Outbox）。
		c.mu.Lock()
		c.ledger.outbox[msg.MsgID] = outboxEntry{Target: target, Msg: msg}
		c.mu.Unlock()
		return err
	}
	if env, err := c.system.journal.appendSys(context.Background(), c.id, sysOutboxDelivered, outboxDeliveredPayload{MsgID: msg.MsgID}); err != nil {
		return err
	} else {
		c.noteSeq(env.Seq)
		delete(c.ledger.outbox, msg.MsgID)
	}
	return nil
}

// scheduleTimer 注册持久化定时器（重启后由激活扫描重新武装）。
func (c *cell) scheduleTimer(after int64, msg Msg) (string, error) {
	timerID := "t-" + newMsgID()[2:]
	if msg.MsgID == "" {
		msg.MsgID = timerID
	}
	payload := timerRegisteredPayload{TimerID: timerID, AfterMs: after, Msg: mustMarshalMsg(msg)}
	env, err := c.system.journal.appendSys(context.Background(), c.id, sysTimerRegistered, payload)
	if err != nil {
		return "", err
	}
	c.noteSeq(env.Seq)
	c.system.journal.fire(StageTimerRegistered, c.id)
	c.mu.Lock()
	c.ledger.timers[timerID] = timerEntry{TimerID: timerID, After: durationJSON(after), Msg: msg}
	c.mu.Unlock()
	c.system.wheel.arm(c.id, timerID, after, msg)
	return timerID, nil
}

// suspend 挂起：消息滞留邮箱，等待外部 Resume（human-in-the-loop）。
func (c *cell) suspend(reason string) error {
	env, err := c.system.journal.appendSys(context.Background(), c.id, sysSuspended, map[string]string{"reason": reason})
	if err != nil {
		return err
	}
	c.noteSeq(env.Seq)
	c.mu.Lock()
	c.suspended = true
	c.mu.Unlock()
	return nil
}

// resume 恢复处理并重新泵送滞留消息。
func (c *cell) resume() error {
	env, err := c.system.journal.appendSys(context.Background(), c.id, sysResumed, map[string]string{})
	if err != nil {
		return err
	}
	c.noteSeq(env.Seq)
	c.mu.Lock()
	c.suspended = false
	c.mu.Unlock()
	c.pump()
	return nil
}

// deadLetter 终结一条消息并隔离 cell（监督器调用）。同步闭合收件账本
// （该消息 done），隔离终态经 sysDeadLetter 持久化，跨重启生效。
func (c *cell) deadLetter(msg Msg, cause error) {
	if err := c.system.journal.appendSysMsg(context.Background(), c.id, sysDeadLetter, map[string]string{
		"msg_id": msg.MsgID, "error": cause.Error(),
	}); err != nil {
		// 隔离终态落盘失败必须可见：若静默丢失，重启后毒消息可能被
		// 重新投递而再次触发监督循环。这里保留隔离的内存态并记录，
		// 运维可据此人工核账。
		c.system.logger("actor: dead letter journal write for %s FAILED (quarantine kept in memory only): %v", c.id, err)
	}
	if msg.MsgID != "" {
		_ = c.system.journal.appendSysMsg(context.Background(), c.id, sysInboxDone, inboxDonePayload{MsgID: msg.MsgID})
		c.mu.Lock()
		if c.ledger != nil {
			c.ledger.done[msg.MsgID] = true
		}
		c.mu.Unlock()
	}
	c.quarantine.Store(true)
	c.mbox.close()
	c.system.logger("actor: %s quarantined: %v", c.id, cause)
}

var _ = es.KindDomain // 保持 es 导入（fold 语义注释见 cell.go）
