package actor

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/es"
)

func TestLedgerFoldBadPayload(t *testing.T) {
	l := newRuntimeLedger()
	bad := es.Envelope{Seq: 1, Type: sysInboxReceived, Kind: es.KindRuntime,
		OccurredAt: time.Unix(0, 1), SchemaVersion: 1, Payload: json.RawMessage("not-an-object")}
	if err := l.foldRuntime(bad); err == nil {
		t.Fatal("expected decode error")
	}
	bad2 := es.Envelope{Seq: 2, Type: sysTimerRegistered, Kind: es.KindRuntime,
		OccurredAt: time.Unix(0, 1), SchemaVersion: 1, Payload: json.RawMessage("[]")}
	if err := l.foldRuntime(bad2); err == nil {
		t.Fatal("expected decode error for timer")
	}
	// 空 MsgID 的 received/timer 事件被忽略（防重投无主消息）。
	emptyMsg := es.Envelope{Seq: 3, Type: sysInboxReceived, Kind: es.KindRuntime,
		OccurredAt: time.Unix(0, 1), SchemaVersion: 1, Payload: mustJSON(inboxReceivedPayload{MsgID: ""})}
	if err := l.foldRuntime(emptyMsg); err != nil || len(l.pendingInbox()) != 0 {
		t.Fatalf("empty msgID should be ignored: %v %v", err, l.pendingInbox())
	}
}

func TestSendUnserializablePayloadFallsBack(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	// chan 不可 JSON 序列化：mustMarshalMsg 回退为 null 载荷，投递不中断。
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "r-bad", Kind: "relay"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sawSent := false
	for _, env := range envs {
		if env.Type == sysOutboxSent {
			sawSent = true
		}
	}
	if !sawSent {
		t.Fatal("outbox sent missing")
	}
}

// 定时器跨重启重武装：注册 → 丢弃系统（模拟崩溃）→ 重建 → 推进时钟 → 触发恰一次。
func TestTimerRearmsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	sysA, _ := newTestSystem(t, dir)
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sysA.Tell(context.Background(), id, Msg{MsgID: "ts", Kind: "tick-setup"}); err != nil {
		t.Fatal(err)
	}
	sysA.Drain()
	// 不推进时钟即丢弃：sys.timer.registered 已落盘、未 fired。

	sysB, vcB := newTestSystem(t, dir)
	defer sysB.Stop()
	sysB.Drain()
	if err := sysB.Tell(context.Background(), id, Msg{MsgID: "nudge", Kind: "noop"}); err != nil {
		t.Fatal(err)
	}
	sysB.Drain()
	if got := len(vcB.PendingTimers()); got != 1 {
		t.Fatalf("pending timers after restart = %d, want 1", got)
	}
	vcB.Advance(10 * time.Millisecond)
	sysB.Drain()
	envs, err := sysB.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	fired := 0
	for _, env := range envs {
		if env.Type == sysTimerFired {
			fired++
		}
	}
	if fired != 1 {
		t.Fatalf("timer fired %d times, want 1", fired)
	}
}

func TestContextStateNilActor(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	c := newCell(sys, ActorID{Type: "counter", Key: "c9"})
	ctx := &Context{sys: sys, cell: c}
	if ctx.State() != nil {
		t.Fatal("state of unactivated cell should be nil")
	}
}

// exerciseActor 触发 Context 全部能力与错误分支（覆盖率补全）。
type exerciseActor struct {
	id       ActorID
	state    atomic.Int64
	results  map[string]string
	schedule string
}

func (a *exerciseActor) ID() ActorID { return a.id }

func (a *exerciseActor) Receive(ctx *Context, msg Msg) {
	switch msg.Kind {
	case "exercise":
		a.results["self"] = ctx.Self().(sysRef).id.String()
		a.results["selfID"] = ctx.SelfID().String()
		a.results["message"] = ctx.Message().MsgID
		a.results["onceFirst"] = boolStr(ctx.Once("k"))
		a.results["onceSecond"] = boolStr(ctx.Once("k"))
		a.results["persistDurable"] = errStr(ctx.Persist("ex.durable", map[string]int{"n": 1}, Durable))
		a.results["persistEphemeral"] = errStr(ctx.Persist("ex.ephemeral", nil, Ephemeral))
		a.results["persistNoType"] = errStr(ctx.Persist("", nil, Durable))
		if _, err := ctx.Schedule(-time.Second, Msg{}); err != nil {
			a.results["scheduleNeg"] = "err"
		}
		timerID, err := ctx.Schedule(5*time.Millisecond, Msg{Kind: "fired"})
		a.schedule = timerID
		a.results["schedule"] = errStr(err)
		_ = ctx.Send(ctx.SelfID(), Msg{MsgID: "self-send", Kind: "noop"})
	case "fired":
		a.results["timerFired"] = "yes"
	case "state":
		a.state.Add(1)
		_ = ctx.State()
	}
}

func (a *exerciseActor) State() any { return a.state.Load() }

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func errStr(err error) string {
	if err != nil {
		return "err"
	}
	return "ok"
}

func TestContextCapabilities(t *testing.T) {
	sys, vc := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	a := &exerciseActor{id: ActorID{Type: "exercise", Key: "e1"}, results: map[string]string{}}
	sys.Register("exercise", func(id ActorID) Actor { return a })
	id := ActorID{Type: "exercise", Key: "e1"}

	if err := sys.Tell(context.Background(), id, Msg{MsgID: "x1", Kind: "exercise"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	vc.Advance(10 * time.Millisecond)
	sys.Drain()

	want := map[string]string{
		"self":             "exercise/e1",
		"selfID":           "exercise/e1",
		"message":          "x1",
		"onceFirst":        "true",
		"onceSecond":       "false",
		"persistDurable":   "ok",
		"persistEphemeral": "err",
		"persistNoType":    "err",
		"scheduleNeg":      "err",
		"schedule":         "ok",
		"timerFired":       "yes",
	}
	for k, v := range want {
		if a.results[k] != v {
			t.Errorf("%s = %q, want %q", k, a.results[k], v)
		}
	}
	// Durable 事件与定时器事件均落盘。
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sawDurable, sawTimer := false, false
	for _, env := range envs {
		if env.Type == "ex.durable" && env.Kind == es.KindDomain {
			sawDurable = true
		}
		if env.Type == sysTimerRegistered {
			sawTimer = true
		}
	}
	if !sawDurable || !sawTimer {
		t.Fatalf("durable=%v timer=%v", sawDurable, sawTimer)
	}
}

// badRestoreActor 的 Restore 恒败：覆盖激活失败路径。
type badRestoreActor struct{ id ActorID }

func (a *badRestoreActor) ID() ActorID               { return a.id }
func (a *badRestoreActor) Receive(_ *Context, _ Msg) {}
func (a *badRestoreActor) Fold(es.Envelope) error    { return nil }
func (a *badRestoreActor) Snapshot() (json.RawMessage, error) {
	return json.RawMessage("{}"), nil
}
func (a *badRestoreActor) Restore(json.RawMessage) error { return errors.New("bad restore") }

func TestActivationFailureKeepsCellInactive(t *testing.T) {
	dir := t.TempDir()
	// 预置快照（好 actor 写入）。
	sysA, _ := newTestSystem(t, dir)
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sysA, id, 3)
	sysA.Drain()
	sysA.Stop()
	// 换坏 actor 重启：Restore 失败 → 激活失败 → cell 保持未激活，不崩。
	sysB, _ := newTestSystem(t, dir)
	sysB.Register("counter", func(id ActorID) Actor { return &badRestoreActor{id: id} })
	defer sysB.Stop()
	if err := sysB.Tell(context.Background(), id, Msg{MsgID: "nudge", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sysB.Drain()
	c := sysB.router.get(id)
	if c != nil && c.actor != nil {
		t.Fatalf("activation should have failed, got actor %T", c.actor)
	}
}

func TestRealClockAfter(t *testing.T) {
	rc := RealClock{}
	if rc.Now().IsZero() {
		t.Fatal("real clock now is zero")
	}
	fired := make(chan struct{})
	cancel := rc.After(5*time.Millisecond, func() { close(fired) })
	_ = cancel
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("real timer did not fire")
	}
	// 取消路径。
	canceled := make(chan struct{})
	cancel2 := rc.After(time.Hour, func() { close(canceled) })
	cancel2()
	time.Sleep(10 * time.Millisecond)
	select {
	case <-canceled:
		t.Fatal("cancelled timer fired")
	default:
	}
}

// mailbox 基础行为补充：FIFO 顺序。
func TestMailboxFIFO(t *testing.T) {
	m := newMailbox(4)
	for _, id := range []string{"a", "b", "c"} {
		if err := m.push(delivery{msg: Msg{MsgID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		item, ok := m.pop()
		if !ok || item.msg.MsgID != want {
			t.Fatalf("pop = %v %v, want %s", ok, item.msg.MsgID, want)
		}
	}
	if _, ok := m.pop(); ok {
		t.Fatal("mailbox should be empty")
	}
}
