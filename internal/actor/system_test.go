package actor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/es"
)

func TestTellActivatesAndProcesses(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 10)
	sys.Drain()
	c := sys.router.get(id)
	counter := c.actor.(*counterActor)
	if counter.count() != 10 {
		t.Fatalf("count = %d, want 10", counter.count())
	}
}

func TestEphemeralSkipsJournal(t *testing.T) {
	dir := t.TempDir()
	sys, _ := newTestSystem(t, dir)
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{Kind: "inc", MsgID: "e1", Durability: Ephemeral}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		// Ephemeral 消息跳过收件账本；但 actor 显式 Persist 的领域事件照常落盘。
		if env.Kind == es.KindRuntime {
			t.Fatalf("ephemeral message wrote runtime event: %+v", env)
		}
	}
	c := sys.router.get(id)
	if got := c.actor.(*counterActor).count(); got != 1 {
		t.Fatalf("processed = %d", got)
	}
}

func TestDuplicateTellDedups(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	for i := 0; i < 5; i++ {
		if err := sys.Tell(context.Background(), id, Msg{MsgID: "same", Kind: "inc"}); err != nil {
			t.Fatal(err)
		}
	}
	sys.Drain()
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 1 {
		t.Fatalf("count = %d, want 1 (dedup)", got)
	}
}

func TestAskReply(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 3)
	sys.Drain()
	reply, err := sys.Ref(id).Ask(context.Background(), Msg{Kind: "ask"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Payload != 3 {
		t.Fatalf("reply payload = %v", reply.Payload)
	}
}

func TestAskTimeoutAbandonsOnly(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"} // counter 不回复未知 kind
	_, err := sys.Ref(id).Ask(context.Background(), Msg{Kind: "silence"}, 20*time.Millisecond)
	if err != ErrAskTimeout {
		t.Fatalf("err = %v, want ErrAskTimeout", err)
	}
}

func TestSuspendResume(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	// 挂起后投递：滞留邮箱，Resume 后处理。
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "s0", Kind: "suspend"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "s1", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 0 {
		t.Fatalf("processed while suspended = %d", got)
	}
	if err := sys.Resume(id); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 1 {
		t.Fatalf("after resume = %d", got)
	}
}

func TestSupervisorRestartsThenQuarantines(t *testing.T) {
	vc := NewVirtualClock()
	sys := NewSystem(t.TempDir(), WithShards(1), WithClock(vc), WithPassivation(0),
		WithSupervision(time.Minute, 3), WithLogger(func(string, ...any) {}))
	sys.Register("panic", func(id ActorID) Actor { return &panicActor{id: id} })
	id := ActorID{Type: "panic", Key: "p1"}

	// 毒消息 b1：panic 后未完结，退避后重投（at-least-once）。
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "b1", Kind: "boom"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	vc.Advance(200 * time.Millisecond) // 退避 100ms 后重启
	sys.Drain()
	if sys.router.get(id) == nil {
		t.Fatal("cell vanished after restart")
	}
	// 窗口内第 3 次 panic（重投 + 新消息叠加）→ 隔离。
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "b2", Kind: "boom"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	dead := sys.DeadLetters()
	if len(dead) != 1 || dead[0].Actor != id {
		t.Fatalf("dead letters = %+v", dead)
	}
	// 隔离后拒绝新投递；同型其他实例不受影响。
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "after", Kind: "ok"}); err != ErrDeadLettered {
		t.Fatalf("quarantined tell err = %v", err)
	}
}

func TestPanicWindowResets(t *testing.T) {
	vc := NewVirtualClock()
	sys := NewSystem(t.TempDir(), WithShards(1), WithClock(vc), WithPassivation(0),
		WithSupervision(50*time.Millisecond, 3), WithLogger(func(string, ...any) {}))
	sys.Register("panic", func(id ActorID) Actor { return &panicActor{id: id} })
	id := ActorID{Type: "panic", Key: "p1"}
	// Ephemeral boom：不入收件账本，无重投——纯窗口语义验证。
	for round := 0; round < 3; round++ {
		_ = sys.Tell(context.Background(), id, Msg{MsgID: "b", Kind: "boom", Durability: Ephemeral})
		sys.Drain()
		vc.Advance(200 * time.Millisecond) // 越过退避与窗口
		sys.Drain()
	}
	if dead := sys.DeadLetters(); len(dead) != 0 {
		t.Fatalf("panics outside window should not quarantine: %+v", dead)
	}
}

func TestPassivationEvictsAndSnapshots(t *testing.T) {
	dir := t.TempDir()
	vc := NewVirtualClock()
	sys := NewSystem(dir, WithShards(1), WithClock(vc), WithPassivation(time.Minute),
		WithSnapshotInterval(3), WithLogger(func(string, ...any) {}))
	sys.Register("counter", func(id ActorID) Actor { return newCounter(id.Key) })
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 3)
	sys.Drain()
	vc.Advance(2 * time.Minute) // 触发钝化
	sys.Drain()
	if sys.router.get(id) != nil {
		t.Fatal("cell should be evicted after passivation")
	}
	// 钝化写快照：重激活走快照路径。
	tellN(t, sys, id, 0)
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "again", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 4 {
		t.Fatalf("after reactivation = %d, want 4", got)
	}
	// 第二轮钝化后重读快照（需推进虚拟时钟触发钝化写入）。
	vc.Advance(2 * time.Minute)
	sys.Drain()
	snap, ok, err := sys.journal.readSnapshot(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("snapshot missing: ok=%v err=%v", ok, err)
	}
	var state struct {
		Processed map[string]int `json:"processed"`
	}
	if err := json.Unmarshal(snap.State, &state); err != nil || len(state.Processed) != 4 {
		t.Fatalf("snapshot state = %s", snap.State)
	}
}

func TestSnapshotIntervalDuringProcessing(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	sys.snapshotInterval = 5
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 5)
	sys.Drain()
	if _, ok, err := sys.journal.readSnapshot(context.Background(), id); err != nil || !ok {
		t.Fatalf("interval snapshot missing: ok=%v err=%v", ok, err)
	}
}

func TestMailboxFullBackpressure(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	sys.mailboxCap = 4
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	var err error
	for i := 0; i < 8; i++ {
		err = sys.Tell(context.Background(), id, Msg{MsgID: "m", Kind: "inc"})
		if err != nil {
			break
		}
	}
	if err != ErrMailboxFull {
		t.Fatalf("expected backpressure, got %v", err)
	}
	sys.Drain()
}

func TestNoProvider(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	err := sys.Tell(context.Background(), ActorID{Type: "missing", Key: "x"}, Msg{})
	if err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	sysA, _ := newTestSystem(t, dir)
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sysA, id, 7)
	sysA.Drain()
	// 模拟进程死亡：不调用 Stop，直接丢弃。
	sysB, _ := newTestSystem(t, dir)
	defer sysB.Stop()
	tellN(t, sysB, id, 0)
	if err := sysB.Tell(context.Background(), id, Msg{MsgID: "more", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sysB.Drain()
	if got := sysB.router.get(id).actor.(*counterActor).count(); got != 8 {
		t.Fatalf("after restart = %d, want 8", got)
	}
}

func TestOutboxDeliveryToSink(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "r1", Kind: "relay"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	sink := sys.router.get(ActorID{Type: "sink", Key: "s1"}).actor.(*sinkActor)
	sink.mu.Lock()
	got := len(sink.got)
	sink.mu.Unlock()
	if got != 1 {
		t.Fatalf("sink got %d", got)
	}
}

func TestTimerFiresOnce(t *testing.T) {
	sys, vc := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "ts", Kind: "tick-setup"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	vc.Advance(10 * time.Millisecond)
	sys.Drain()
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	fired, registered := 0, 0
	for _, env := range envs {
		switch env.Type {
		case sysTimerFired:
			fired++
		case sysTimerRegistered:
			registered++
		}
	}
	if registered != 1 || fired != 1 {
		t.Fatalf("registered=%d fired=%d", registered, fired)
	}
}

func TestOnceSemantics(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "once"}
	// 用 relay 触发一次性副作用？直接构造：counter 内不使用 Once；
	// 此处校验原语本身经 cell。
	sys.Tell(context.Background(), id, Msg{MsgID: "warm", Kind: "inc"})
	sys.Drain()
	c := sys.router.get(id)
	if !c.once("k1") || c.once("k1") {
		t.Fatal("once semantics broken")
	}
}

func TestStopIdempotent(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	sys.Stop()
	sys.Stop()
	if err := sys.Tell(context.Background(), ActorID{Type: "counter", Key: "c"}, Msg{}); err != ErrStopped {
		t.Fatalf("tell after stop = %v", err)
	}
}

func TestConcurrentTellUnderRace(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	sys.mailboxCap = 4096
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = sys.Tell(context.Background(), id, Msg{MsgID: "hit", Kind: "inc"})
			}
		}(w)
	}
	wg.Wait()
	sys.Drain()
	// 同 MsgID 只处理一次（去重），但不应崩溃/死锁。
	if got := sys.router.get(id).actor.(*counterActor).count(); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestRefTellAndAsk(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	ref := sys.Ref(ActorID{Type: "counter", Key: "c1"})
	if err := ref.Tell(context.Background(), Msg{MsgID: "t1", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	if _, err := ref.Ask(context.Background(), Msg{Kind: "ask"}, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestContextStateNilWithoutStater(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "sink", Key: "s1"}
	sys.Tell(context.Background(), id, Msg{MsgID: "x", Kind: "note"})
	sys.Drain()
	c := sys.router.get(id)
	ctx := &Context{sys: sys, cell: c, msg: Msg{}}
	if ctx.State() != nil {
		t.Fatal("sink has no Stater")
	}
}

func TestJournalSeqContiguousInScenario(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 4)
	sys.Tell(context.Background(), id, Msg{MsgID: "r", Kind: "relay"})
	sys.Drain()
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i, env := range envs {
		if env.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %+v", i, env)
		}
	}
	if len(envs) == 0 {
		t.Fatal("no events")
	}
	var _ = es.KindDomain
}
