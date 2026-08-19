// Package actor 提供单机虚拟 actor 运行时（L2，spec §3/§5）。
//
// 核心契约：
//   - Actor = {ID, 事件流, 状态缓存, 邮箱}；激活 = 快照 + 尾部事件 fold；
//   - Journal-First 消息处理协议（spec §6.1）：received → Receive → done；
//   - Outbox 先落盘后投递（I2）；分片单写者保证 actor 内串行（I1）；
//   - sys.* 运行时事件与 domain 事件分命名空间，两段式 fold（I5/ADR-9）。
//
// 本包禁止 import 任何 L3/L4 领域包（I6，测试强制）。
package actor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"paw/internal/es"
)

// Durability 是事件/消息的持久化级别（ADR-4）。
type Durability uint8

const (
	// Durable 逐条 fsync：权限、task 生命周期、todo 等关键事实。
	Durable Durability = iota
	// Buffered 组提交：领域事件在消息边界统一落盘。
	Buffered
	// Ephemeral 不落盘：仅限显示流（ADR-1），不进入事件日志。
	Ephemeral
)

// ActorID 是虚拟 actor 的位置透明身份。事件流路径：{type}/{key}.jsonl。
type ActorID struct {
	Type string
	Key  string
}

func (id ActorID) String() string { return id.Type + "/" + id.Key }

// Validate 拒绝空字段与路径穿越（流文件名由此拼接）。
func (id ActorID) Validate() error {
	if id.Type == "" || id.Key == "" {
		return fmt.Errorf("actor: id type and key are required")
	}
	for _, part := range []string{id.Type, id.Key} {
		if len(part) > 255 || strings.ContainsAny(part, "/\\\x00") || part == "." || part == ".." || strings.Contains(part, "..") {
			return fmt.Errorf("actor: unsafe id component %q", part)
		}
	}
	return nil
}

// Msg 是邮箱消息。MsgID 是投递去重的幂等键（ADR-5）：同 ID 消息在同一
// actor 的持久化收件账本内至多处理一次；为空时运行时生成。
type Msg struct {
	MsgID      string
	Kind       string
	Payload    any
	Durability Durability
}

// Actor 是领域行为单元。实现者保持无锁：状态只在 Receive 内变更（I1
// 由运行时的分片单写者保证）。
type Actor interface {
	ID() ActorID
	Receive(ctx *Context, msg Msg)
}

// EventSourced 是可选的事件溯源能力：Fold 只会收到 Kind=domain 的事件
// （I5：sys.* 由运行时消费，绝不进入 Fold）；快照是缓存，可随时删除，
// 代价是全量重放。
type EventSourced interface {
	Fold(env es.Envelope) error
	Snapshot() (json.RawMessage, error)
	Restore(state json.RawMessage) error
}

// StreamStore persists one actor type's event streams. The default adapter is
// es.JSONLStore; domains may inject a compatible store when an actor stream
// must share an existing append-only ledger.
type StreamStore interface {
	Append(ctx context.Context, aggregateID string, events []es.Envelope) (firstSeq, lastSeq int64, err error)
	Load(ctx context.Context, aggregateID string) (events []es.Envelope, truncated bool, err error)
	WriteSnapshot(ctx context.Context, aggregateID string, seq int64, state json.RawMessage) error
	ReadSnapshot(ctx context.Context, aggregateID string) (es.Snapshot, bool, error)
}

// Stater 暴露当前状态投影（ctx.State() 委托至此）。
type Stater interface {
	State() any
}

// Ref 是 actor 的地址句柄（spec §5）。
type Ref interface {
	// Tell at-least-once 投递；ErrMailboxFull 时调用方可安全重试。
	Tell(ctx context.Context, msg Msg) error
	// Ask = Tell + MsgID 关联回执；超时仅放弃等待（ADR-3），不撤销消息。
	Ask(ctx context.Context, msg Msg, timeout time.Duration) (Msg, error)
}

// 预定义错误。
var (
	ErrMailboxFull  = fmt.Errorf("actor: mailbox full")
	ErrStopped      = fmt.Errorf("actor: system stopped")
	ErrNoProvider   = fmt.Errorf("actor: no provider registered for type")
	ErrAskTimeout   = fmt.Errorf("actor: ask timeout")
	ErrSuspended    = fmt.Errorf("actor: suspended, message parked")
	ErrDeadLettered = fmt.Errorf("actor: quarantined (dead letter)")
)

// sys.* 事件类型（spec §6.3，两段式 fold 的 runtime 段）。
const (
	sysInboxReceived   = "sys.inbox.received"
	sysInboxDone       = "sys.inbox.done"
	sysOutboxSent      = "sys.outbox.sent"
	sysOutboxDelivered = "sys.outbox.delivered"
	sysTimerRegistered = "sys.timer.registered"
	sysTimerFired      = "sys.timer.fired"
	sysSuspended       = "sys.suspended"
	sysResumed         = "sys.resumed"
	sysDeadLetter      = "sys.dead_letter"
)

// newMsgID 生成全局唯一消息 ID（128bit 随机 hex）。
func newMsgID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败不可静默：退化为纳秒时间戳仍保证进程内唯一，
		// 跨进程由 fsync 的时间窗兜底。
		return fmt.Sprintf("m-%d", time.Now().UnixNano())
	}
	return "m-" + hex.EncodeToString(b[:])
}

// msgJSON 是 Msg 的线上/落盘形态（es 要求 payload 为 JSON 对象）。
type msgJSON struct {
	MsgID      string          `json:"msg_id"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Durability uint8           `json:"durability,omitempty"`
}

func marshalMsg(m Msg) (msgJSON, error) {
	out := msgJSON{MsgID: m.MsgID, Kind: m.Kind, Durability: uint8(m.Durability)}
	if m.Payload != nil {
		raw, err := json.Marshal(m.Payload)
		if err != nil {
			return msgJSON{}, fmt.Errorf("actor: marshal msg payload: %w", err)
		}
		out.Payload = raw
	} else {
		out.Payload = json.RawMessage("null")
	}
	return out, nil
}
