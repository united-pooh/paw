package actor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/es"
)

// counterActor 是事件溯源计数器（矩阵用）：Persist "incremented{msg_id,n}"，
// Fold 重建 processed 集合实现跨重启幂等（ADR-5 防御性幂等的标准姿势）。
type counterActor struct {
	id        ActorID
	mu        sync.Mutex // State()/测试读取与分片线程互斥
	processed map[string]int
	order     []string
	sawSys    *atomic.Bool // I5 断言：Fold 是否见到 sys.*
}

func newCounter(key string) *counterActor {
	return &counterActor{id: ActorID{Type: "counter", Key: key}, processed: make(map[string]int), sawSys: &atomic.Bool{}}
}

func (a *counterActor) ID() ActorID { return a.id }

func (a *counterActor) Receive(ctx *Context, msg Msg) {
	switch msg.Kind {
	case "inc":
		a.mu.Lock()
		_, done := a.processed[msg.MsgID]
		a.mu.Unlock()
		if done {
			return // 事件溯源幂等：重投跳过
		}
		_ = ctx.Persist("incremented", map[string]any{"msg_id": msg.MsgID}, Buffered)
		a.mu.Lock()
		a.processed[msg.MsgID] = 1
		a.order = append(a.order, msg.MsgID)
		a.mu.Unlock()
	case "relay":
		_ = ctx.Send(ActorID{Type: "sink", Key: "s1"}, Msg{MsgID: "relay-" + msg.MsgID, Kind: "note", Payload: msg.MsgID})
	case "ask":
		a.mu.Lock()
		n := len(a.processed)
		a.mu.Unlock()
		ctx.Reply(Msg{Kind: "count", Payload: n})
	case "tick-setup":
		_, _ = ctx.Schedule(5*time.Millisecond, Msg{Kind: "tick", Payload: "fired"})
	case "suspend":
		_ = ctx.Suspend("await decision")
	}
}

func (a *counterActor) Fold(env es.Envelope) error {
	if env.Kind == es.KindRuntime {
		a.sawSys.Store(true)
	}
	if env.Type != "incremented" {
		return nil
	}
	var p struct {
		MsgID string `json:"msg_id"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	a.mu.Lock()
	a.processed[p.MsgID] = 1
	a.order = append(a.order, p.MsgID)
	a.mu.Unlock()
	return nil
}

func (a *counterActor) Snapshot() (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return json.Marshal(map[string]any{"processed": a.processed})
}

func (a *counterActor) Restore(state json.RawMessage) error {
	var p struct {
		Processed map[string]int `json:"processed"`
	}
	if err := json.Unmarshal(state, &p); err != nil {
		return err
	}
	a.mu.Lock()
	a.processed = p.Processed
	a.mu.Unlock()
	return nil
}

func (a *counterActor) State() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.processed)
}

func (a *counterActor) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.processed)
}

// sinkActor 收集 relay 投递（崩溃矩阵的 Outbox 验证端）。
type sinkActor struct {
	id  ActorID
	mu  sync.Mutex
	got map[string]int
}

func newSink(key string) *sinkActor {
	return &sinkActor{id: ActorID{Type: "sink", Key: key}, got: make(map[string]int)}
}

func (a *sinkActor) ID() ActorID { return a.id }
func (a *sinkActor) Receive(_ *Context, msg Msg) {
	a.mu.Lock()
	a.got[msg.MsgID]++
	a.mu.Unlock()
}

// concurrencyActor 记录 Receive 的最大并发数（I1 属性断言）。
type concurrencyActor struct {
	id     ActorID
	cur    atomic.Int64
	max    atomic.Int64
	sawAll atomic.Int64
}

func (a *concurrencyActor) ID() ActorID { return a.id }
func (a *concurrencyActor) Receive(_ *Context, _ Msg) {
	cur := a.cur.Add(1)
	for {
		old := a.max.Load()
		if cur <= old || a.max.CompareAndSwap(old, cur) {
			break
		}
	}
	a.sawAll.Add(1)
	a.cur.Add(-1)
}

// panicActor 按消息载荷决定 panic（监督测试）。
type panicActor struct {
	id     ActorID
	hits   atomic.Int64
	panics atomic.Int64
}

func (a *panicActor) ID() ActorID { return a.id }
func (a *panicActor) Receive(_ *Context, msg Msg) {
	a.hits.Add(1)
	if msg.Kind == "boom" {
		a.panics.Add(1)
		panic("boom: " + fmt.Sprint(msg.Payload))
	}
}

// newTestSystem 构建单分片确定性系统（虚拟时钟、不钝化）。
func newTestSystem(t *testing.T, dir string) (*System, *VirtualClock) {
	t.Helper()
	vc := NewVirtualClock()
	sys := NewSystem(dir,
		WithShards(1),
		WithClock(vc),
		WithPassivation(0),
		WithLogger(func(string, ...any) {}),
	)
	sys.Register("counter", func(id ActorID) Actor { return newCounter(id.Key) })
	sys.Register("sink", func(id ActorID) Actor { return newSink(id.Key) })
	sys.Register("concurrency", func(id ActorID) Actor { return &concurrencyActor{id: id} })
	sys.Register("panic", func(id ActorID) Actor { return &panicActor{id: id} })
	return sys, vc
}

func tellN(t *testing.T, sys *System, id ActorID, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := sys.Tell(context.Background(), id, Msg{MsgID: fmt.Sprintf("m%03d", i), Kind: "inc"}); err != nil {
			t.Fatalf("tell %d: %v", i, err)
		}
	}
}

// waitUntil 轮询直至条件满足（真实时钟测试的同步点）。
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}
