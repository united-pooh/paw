package actor

import (
	"fmt"

	"paw/internal/es"
)

// runtimeLedger 是 sys.* 事件两段式 fold 的第一段产物（spec §6.3）：
// 投递账本（inbox 去重/未决）、Outbox 未决、未触发定时器、挂起标志。
// 它只消费 Kind=runtime 事件，domain 事件一律跳过（I5 的构造性保证）。
type runtimeLedger struct {
	// inbox: MsgID → true（已收到）。done 集合内为已完结；两者差集为
	// 崩溃时处理中断的消息（激活时需重投）。
	received map[string]Msg
	done     map[string]bool
	once     map[string]bool

	// outbox: MsgID → 已落盘未确认投递的外发消息。
	outbox map[string]outboxEntry

	// timers: TimerID → 注册未触发的定时器（重启后重新武装）。
	timers map[string]timerEntry

	suspended     bool
	suspendReason string
	// quarantined：流内存在死信终态事件，激活时墓碑化（隔离跨重启持久）。
	quarantined bool

	lastSeq int64
}

type outboxEntry struct {
	Target ActorID
	Msg    Msg
}

type timerEntry struct {
	TimerID string
	After   durationJSON
	Msg     Msg
}

// durationJSON 用毫秒整数落盘（time.Duration 直接 JSON 是纳秒 int64，
// 显式毫秒便于跨语言审计）。
type durationJSON int64

func (d durationJSON) Duration() int64 { return int64(d) }

// foldRuntime 消费一条运行时事件。未知 sys.* 类型记入账本后跳过（向前
// 兼容：旧读者忽略新事件）。
func (l *runtimeLedger) foldRuntime(env es.Envelope) error {
	if env.Seq > l.lastSeq {
		l.lastSeq = env.Seq
	}
	switch env.Type {
	case sysInboxReceived:
		var p struct {
			MsgID string  `json:"msg_id"`
			Msg   msgJSON `json:"msg"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		if p.MsgID != "" {
			msg, err := decodeMsgJSON(p.Msg)
			if err != nil {
				return err
			}
			msg.MsgID = p.MsgID
			l.received[p.MsgID] = msg
		}
	case sysInboxDone:
		var p struct {
			MsgID string `json:"msg_id"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		l.done[p.MsgID] = true
	case sysOutboxSent:
		var p struct {
			MsgID  string  `json:"msg_id"`
			Target string  `json:"target"`
			Msg    msgJSON `json:"msg"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		if p.MsgID == "" {
			return nil
		}
		msg, err := decodeMsgJSON(p.Msg)
		if err != nil {
			return err
		}
		msg.MsgID = p.MsgID
		l.outbox[p.MsgID] = outboxEntry{Target: parseActorID(p.Target), Msg: msg}
	case sysOutboxDelivered:
		var p struct {
			MsgID string `json:"msg_id"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		delete(l.outbox, p.MsgID)
	case sysTimerRegistered:
		var p struct {
			TimerID string  `json:"timer_id"`
			AfterMs int64   `json:"after_ms"`
			Msg     msgJSON `json:"msg"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		if p.TimerID == "" {
			return nil
		}
		msg, err := decodeMsgJSON(p.Msg)
		if err != nil {
			return err
		}
		l.timers[p.TimerID] = timerEntry{TimerID: p.TimerID, After: durationJSON(p.AfterMs), Msg: msg}
	case sysTimerFired:
		var p struct {
			TimerID string `json:"timer_id"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		delete(l.timers, p.TimerID)
	case sysSuspended:
		var p struct {
			Reason string `json:"reason"`
		}
		if err := decodePayload(env, &p); err != nil {
			return err
		}
		l.suspended = true
		l.suspendReason = p.Reason
	case sysResumed:
		l.suspended = false
		l.suspendReason = ""
	case sysDeadLetter:
		// 终态记录：流级隔离标志，跨重启持久。
		l.quarantined = true
	}
	return nil
}

// 已完结判定：done 且 received 均存在（done 无 received 视为终态，防重投）。
func (l *runtimeLedger) isDone(msgID string) bool {
	return l.done[msgID]
}

// noteFired 定时器触发后从未决集合移除。
func (l *runtimeLedger) noteFired(timerID string) { delete(l.timers, timerID) }

// pendingInbox 返回崩溃时处理中断的消息（received 无 done），按落盘序。
func (l *runtimeLedger) pendingInbox() []Msg {
	var out []Msg
	for id, msg := range l.received {
		if !l.done[id] {
			out = append(out, msg)
		}
	}
	return out
}

func decodePayload(env es.Envelope, target any) error {
	if err := jsonUnmarshal(env.Payload, target); err != nil {
		return fmt.Errorf("actor: decode %s: %w", env.Type, err)
	}
	return nil
}

func decodeMsgJSON(raw msgJSON) (Msg, error) {
	return Msg{
		MsgID:      raw.MsgID,
		Kind:       raw.Kind,
		Payload:    cloneRaw(raw.Payload),
		Durability: Durability(raw.Durability),
	}, nil
}

func parseActorID(s string) ActorID {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return ActorID{Type: s[:i], Key: s[i+1:]}
		}
	}
	return ActorID{Type: s}
}

// newRuntimeLedger 构造空账本。
func newRuntimeLedger() *runtimeLedger {
	return &runtimeLedger{
		received: make(map[string]Msg),
		done:     make(map[string]bool),
		once:     make(map[string]bool),
		outbox:   make(map[string]outboxEntry),
		timers:   make(map[string]timerEntry),
	}
}
