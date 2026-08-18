package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"paw/internal/es"
)

// crashStage 是崩溃注入钩子的协议位点（spec §6.1 ①-④）。
// 值本身是对外契约：testdata/crashprobe 与崩溃矩阵测试按 stage 名断点。
const (
	StageInboxReceived   = "inbox.received.post" // ① 之后
	StageOutboxSent      = "outbox.sent.post"    // ② 内：Outbox 落盘之后（I2）
	StageDomainFlushed   = "domain.flushed.post" // ② 内：领域事件落盘之后
	StageInboxDone       = "inbox.done.post"     // ③ 之后
	StageSnapshotted     = "snapshot.post"       // ④ 之后
	StageTimerRegistered = "timer.registered.post"
)

// journal 按 actor.Type 分目录复用 es.JSONLStore（流路径 {dir}/{type}/{key}.events.jsonl）。
// 所有 sys.* 事件逐条 fsync（Durable）；领域事件按调用方 Durability：
// Durable 逐条落盘，Buffered 由 cell 批量一次 Append（组提交）。
type journal struct {
	dir   string
	clock Clock

	mu     sync.Mutex
	stores map[string]*es.JSONLStore
	hook   func(stage string, id ActorID)
}

func newJournal(dir string, clock Clock) *journal {
	return &journal{dir: dir, clock: clock, stores: make(map[string]*es.JSONLStore)}
}

// setHook 注册崩溃注入钩子（测试专用；nil 关闭）。
func (j *journal) setHook(hook func(stage string, id ActorID)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.hook = hook
}

func (j *journal) fire(stage string, id ActorID) {
	j.mu.Lock()
	hook := j.hook
	j.mu.Unlock()
	if hook != nil {
		hook(stage, id)
	}
}

func (j *journal) store(id ActorID) (*es.JSONLStore, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	store, ok := j.stores[id.Type]
	if !ok {
		var err error
		store, err = es.NewJSONLStore(j.dir, id.Type)
		if err != nil {
			return nil, err
		}
		j.stores[id.Type] = store
	}
	return store, nil
}

func (j *journal) append(ctx context.Context, id ActorID, kind, typ string, payload any) (es.Envelope, error) {
	store, err := j.store(id)
	if err != nil {
		return es.Envelope{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return es.Envelope{}, fmt.Errorf("actor: encode %s: %w", typ, err)
	}
	envs := []es.Envelope{{
		Type:          typ,
		OccurredAt:    j.clock.Now(),
		SchemaVersion: 1,
		Kind:          kind,
		Payload:       raw,
	}}
	first, _, err := store.Append(ctx, id.Key, envs)
	if err != nil {
		return es.Envelope{}, fmt.Errorf("actor: append %s: %w", typ, err)
	}
	envs[0].Seq = first
	return envs[0], nil
}

// now 由 clock 提供。

// appendSys 落一条运行时事件（总是 Durable：账本正确性先于吞吐），
// 返回带分配 seq 的事件。
func (j *journal) appendSys(ctx context.Context, id ActorID, typ string, payload any) (es.Envelope, error) {
	return j.append(ctx, id, es.KindRuntime, typ, payload)
}

// appendSysMsg 同 appendSys 但忽略返回事件（触发路径使用）。
func (j *journal) appendSysMsg(ctx context.Context, id ActorID, typ string, payload any) error {
	_, err := j.append(ctx, id, es.KindRuntime, typ, payload)
	return err
}

// appendDomain 批量落领域事件（一组一个 seq 段，组提交），返回首尾 seq。
func (j *journal) appendDomain(ctx context.Context, id ActorID, events []es.Envelope) (first, last int64, err error) {
	if len(events) == 0 {
		return 0, 0, nil
	}
	store, err := j.store(id)
	if err != nil {
		return 0, 0, err
	}
	prepared := make([]es.Envelope, len(events))
	for i, e := range events {
		e.Kind = es.KindDomain
		e.OccurredAt = j.clock.Now()
		e.SchemaVersion = 1
		prepared[i] = e
	}
	return store.Append(ctx, id.Key, prepared)
}

func (j *journal) load(ctx context.Context, id ActorID) ([]es.Envelope, error) {
	store, err := j.store(id)
	if err != nil {
		return nil, err
	}
	envs, _, err := store.Load(ctx, id.Key)
	return envs, err
}

func (j *journal) writeSnapshot(ctx context.Context, id ActorID, seq int64, state json.RawMessage) error {
	store, err := j.store(id)
	if err != nil {
		return err
	}
	return store.WriteSnapshot(ctx, id.Key, seq, state)
}

func (j *journal) readSnapshot(ctx context.Context, id ActorID) (es.Snapshot, bool, error) {
	store, err := j.store(id)
	if err != nil {
		return es.Snapshot{}, false, err
	}
	return store.ReadSnapshot(ctx, id.Key)
}
