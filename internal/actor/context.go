package actor

import (
	"context"
	"fmt"
	"time"

	"paw/internal/es"
)

// Context 是 Receive 的能力句柄（纯句柄袋：零业务逻辑，spec §5 上帝类
// 防线）。所有能力指向“自己”：Self/Send/Persist/Once/Schedule/Suspend。
// 生命周期与单条消息绑定，禁止跨 Receive 保存。
type Context struct {
	sys    *System
	cell   *cell
	msg    Msg
	buffer []es.Envelope // Buffered 领域事件组提交缓冲
}

// 字段数（导出+未导出）：4 ≤ 12（防线审计）。

// Self 返回自身地址。
func (c *Context) Self() Ref { return c.sys.Ref(c.cell.id) }

// SelfID 返回自身身份。
func (c *Context) SelfID() ActorID { return c.cell.id }

// Message 返回当前正在处理的消息。
func (c *Context) Message() Msg { return c.msg }

// State 返回 actor 暴露的状态投影（未实现 Stater 时返回 nil）。
func (c *Context) State() any {
	if stater, ok := c.cell.actor.(Stater); ok {
		return stater.State()
	}
	return nil
}

// Persist 记录一条领域事件。Durable 逐条落盘；Buffered 在消息边界组
// 提交（崩溃时未落盘事件随重投收敛）；Ephemeral 事件不存在持久化语义，
// 返回错误（显示流走订阅总线，P5）。
func (c *Context) Persist(eventType string, payload any, d Durability) error {
	if eventType == "" {
		return fmt.Errorf("actor: event type is required")
	}
	switch d {
	case Ephemeral:
		return fmt.Errorf("actor: ephemeral events are not persistable")
	case Buffered:
		c.buffer = append(c.buffer, es.Envelope{Type: eventType, Payload: mustJSON(payload)})
		return nil
	default: // Durable
		_, last, err := c.sys.journal.appendDomain(context.Background(), c.cell.id,
			[]es.Envelope{{Type: eventType, Payload: mustJSON(payload)}})
		if err == nil {
			c.cell.noteSeq(last)
			c.cell.noteDomainEvents(1)
		}
		return err
	}
}

// Send 经事务性 Outbox 外发（I2：先落盘再投递）。目标邮箱满时返回
// ErrMailboxFull，pending 项由激活扫描补发。
func (c *Context) Send(target ActorID, msg Msg) error {
	return c.cell.send(target, msg)
}

// Once 是幂等原语：同一 key 首次调用返回 true，其后返回 false。
// 作用域为本激活周期（监督重启后重建）；跨重启幂等应由事件溯源状态
// 自行收敛（ADR-5）。
func (c *Context) Once(key string) bool {
	return c.cell.once(key)
}

// Schedule 注册持久化定时器：到期后向自己投递 msg（MsgID=timerID，
// 进入收件账本，天然去重）。重启后由激活扫描重新武装。
func (c *Context) Schedule(after time.Duration, msg Msg) (string, error) {
	if after < 0 {
		return "", fmt.Errorf("actor: negative schedule duration")
	}
	return c.cell.scheduleTimer(after.Milliseconds(), msg)
}

// Suspend 挂起自身：后续消息滞留邮箱，等待外部 Resume。
func (c *Context) Suspend(reason string) error {
	return c.cell.suspend(reason)
}

// Reply 应答当前消息的发起方（Ask 的回执通道，不落盘）。
// 当前消息非 Ask 发起时为 no-op。
func (c *Context) Reply(reply Msg) {
	correlation := c.correlation()
	if correlation == "" {
		return
	}
	c.sys.deliverPromise(correlation, reply)
}

func (c *Context) correlation() string { return c.msg.MsgID }
