package actor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/es"
)

// probeActor 是覆盖测试专用探针：记录 Once/State/Reply 行为。
type probeActor struct {
	id      ActorID
	mu      sync.Mutex
	once    []bool
	states  []any
	replies int
}

func (a *probeActor) ID() ActorID { return a.id }
func (a *probeActor) Receive(ctx *Context, msg Msg) {
	switch msg.Kind {
	case "once":
		a.mu.Lock()
		a.once = append(a.once, ctx.Once("key"))
		a.mu.Unlock()
	case "state":
		a.mu.Lock()
		a.states = append(a.states, ctx.State())
		a.mu.Unlock()
	case "reply":
		ctx.Reply(Msg{Kind: "ack", Payload: "ok"})
		a.mu.Lock()
		a.replies++
		a.mu.Unlock()
	}
}
func (a *probeActor) State() any { return "projection" }

// 系统选项边界：非法值回退默认；journal 钩子注册与清除。
func TestSystemOptionEdges(t *testing.T) {
	dir := t.TempDir()
	vc := NewVirtualClock()
	sys := NewSystem(dir,
		WithShards(0),       // 非法 → 默认
		WithMailboxCap(-1),  // 非法 → 默认
		WithPassivation(-1), // 关闭钝化
		WithClock(vc),
		WithLogger(func(string, ...any) {}),
	)
	defer sys.Stop()
	fired := 0
	sys.SetJournalHook(func(string, ActorID) { fired++ })
	sys.SetJournalHook(nil)
	sys.Register("probe", func(id ActorID) Actor { return &probeActor{id: id} })
	_ = sys.Tell(context.Background(), ActorID{Type: "probe", Key: "p1"}, Msg{MsgID: "m1", Kind: "once"})
	sys.Drain()
	if fired != 0 {
		t.Fatalf("nil hook should disable, fired=%d", fired)
	}
}

// wheel.cancel 直接路径：武装后取消，到期不触发。
func TestWheelCancelPreventsFire(t *testing.T) {
	sys, vc := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	_ = sys.Tell(context.Background(), id, Msg{MsgID: "warm", Kind: "inc"})
	sys.Drain()
	sys.wheel.arm(id, "t1", 5, Msg{MsgID: "t1", Kind: "inc"})
	sys.wheel.cancel(id, "t1") // 取消
	// 同 timerID 重复武装再取消：旧回调被替换。
	sys.wheel.arm(id, "t1", 5, Msg{MsgID: "t1", Kind: "inc"})
	sys.wheel.cancel(id, "t1")
	vc.Advance(50 * time.Millisecond)
	sys.Drain()
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 1 {
		t.Fatalf("count = %d, want 1 (warm only; canceled timers must not fire)", got)
	}
}

// Ask 超时：仅放弃等待（ADR-3），返回 ErrAskTimeout；已取消 ctx 返回 ctx 错误。
func TestAskTimeoutAndContextCancel(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if _, err := sys.Ref(id).Ask(context.Background(), Msg{MsgID: "q1", Kind: "inc"}, 30*time.Millisecond); err != ErrAskTimeout {
		t.Fatalf("want ErrAskTimeout, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sys.Ref(id).Ask(ctx, Msg{MsgID: "q2", Kind: "inc"}, time.Second); err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	sys.Drain()
}

// Ask 投递失败（未注册类型）：promise 清理，错误上抛。
func TestAskTellFailureCleansPromise(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	_, err := sys.Ref(ActorID{Type: "ghost", Key: "g"}).Ask(context.Background(), Msg{MsgID: "q", Kind: "ask"}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("ask to unregistered should fail: %v", err)
	}
	sys.mu.Lock()
	n := len(sys.promises)
	sys.mu.Unlock()
	if n != 0 {
		t.Fatalf("promises leaked: %d", n)
	}
}

// Ask 成功路径回执后，迟到 deliverPromise 无副作用。
func TestDeliverPromiseAfterCompletion(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	sys.Register("probe", func(id ActorID) Actor { return &probeActor{id: id} })
	id := ActorID{Type: "probe", Key: "p1"}
	reply, err := sys.Ref(id).Ask(context.Background(), Msg{MsgID: "q1", Kind: "reply"}, time.Second)
	if err != nil || reply.Kind != "ack" {
		t.Fatalf("ask = %+v %v", reply, err)
	}
	sys.deliverPromise("q1", Msg{Kind: "late"}) // promise 已消费：静默
	sys.Drain()
}

// Once：同 key 首真余假（本激活周期内）。
func TestOnceKeyFirstTrueThenFalse(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	sys.Register("probe", func(id ActorID) Actor { return &probeActor{id: id} })
	id := ActorID{Type: "probe", Key: "p1"}
	for i := 0; i < 3; i++ {
		_ = sys.Tell(context.Background(), id, Msg{MsgID: "o", Kind: "once", Durability: Ephemeral})
	}
	sys.Drain()
	p := sys.router.get(id).actor.(*probeActor)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.once) != 3 || !p.once[0] || p.once[1] || p.once[2] {
		t.Fatalf("once = %v, want [true false false]", p.once)
	}
}

// State：Stater 投影可见；非 Stater（sink）路径经 processOne 不 panic。
func TestContextStateStater(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	sys.Register("probe", func(id ActorID) Actor { return &probeActor{id: id} })
	id := ActorID{Type: "probe", Key: "p1"}
	_ = sys.Tell(context.Background(), id, Msg{MsgID: "s", Kind: "state", Durability: Ephemeral})
	sys.Drain()
	p := sys.router.get(id).actor.(*probeActor)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.states) != 1 || p.states[0] != "projection" {
		t.Fatalf("states = %v", p.states)
	}
	// sink 无 Stater：ctx.State() nil 分支由 reply 消息覆盖（sink 不调 State）。
	// 直接构造 Context 调用覆盖 nil 分支。
	sinkID := ActorID{Type: "sink", Key: "s1"}
	_ = sys.Tell(context.Background(), sinkID, Msg{MsgID: "n", Kind: "note"})
	sys.Drain()
	c := sys.router.get(sinkID)
	ctx := &Context{sys: sys, cell: c}
	if ctx.State() != nil {
		t.Fatal("non-stater state should be nil")
	}
}

// Resume：未挂起 cell 为 no-op；不存在 cell 报错。
func TestResumeEdges(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	_ = sys.Tell(context.Background(), id, Msg{MsgID: "warm", Kind: "inc"})
	sys.Drain()
	if err := sys.Resume(id); err != nil {
		t.Fatalf("resume non-suspended should be no-op: %v", err)
	}
	if err := sys.Resume(ActorID{Type: "counter", Key: "ghost"}); err == nil {
		t.Fatal("resume inactive should error")
	}
}

// foldRuntime 损坏载荷报错（激活恢复遇脏数据显式失败，不静默吞）。
func TestFoldRuntimeCorruptPayloads(t *testing.T) {
	types := []string{
		sysInboxReceived, sysInboxDone, sysOutboxSent, sysOutboxDelivered,
		sysTimerRegistered, sysTimerFired, sysSuspended,
	}
	for _, typ := range types {
		l := newRuntimeLedger()
		env := es.Envelope{Type: typ, Payload: []byte(`"not-an-object"`)}
		if err := l.foldRuntime(env); err == nil {
			t.Fatalf("%s: corrupt payload should error", typ)
		}
	}
}

// mustJSON 不可序列化回退 "{}"。
func TestMustJSONFallback(t *testing.T) {
	if got := string(mustJSON(make(chan int))); got != "{}" {
		t.Fatalf("mustJSON = %s", got)
	}
}
