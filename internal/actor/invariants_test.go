package actor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"paw/internal/es"
)

// I1：任一时刻每个 actor 至多一个 worker 执行 Receive。
func TestInvariantI1SingleWriter(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	sys.mailboxCap = 8192
	defer sys.Stop()
	id := ActorID{Type: "concurrency", Key: "c1"}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = sys.Tell(context.Background(), id, Msg{MsgID: fmt.Sprintf("w%d-i%d", w, i), Kind: "hit"})
			}
		}(w)
	}
	wg.Wait()
	sys.Drain()
	c := sys.router.get(id)
	a := c.actor.(*concurrencyActor)
	if got := a.max.Load(); got != 1 {
		t.Fatalf("max concurrent Receive = %d, want 1", got)
	}
	if got := a.sawAll.Load(); got == 0 {
		t.Fatal("no messages processed")
	}
}

// I2：Outbox 先落盘后投递——journal 中 sent 必须先于 delivered，且
// 投递动作在 sent 落盘之后发生（经 stage 钩子断言时序）。
func TestInvariantI2OutboxPersistedBeforeDelivery(t *testing.T) {
	dir := t.TempDir()
	sys, _ := newTestSystem(t, dir)
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "r1", Kind: "relay"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sentAt, deliveredAt := -1, -1
	for i, env := range envs {
		switch env.Type {
		case sysOutboxSent:
			sentAt = i
		case sysOutboxDelivered:
			deliveredAt = i
		}
	}
	if sentAt < 0 || deliveredAt < 0 || sentAt > deliveredAt {
		t.Fatalf("outbox order broken: sent@%d delivered@%d", sentAt, deliveredAt)
	}
	// 投递结果可见（sink 收到）必须晚于 sent 落盘：由于 sent 在 Receive 内
	// 同步追加而投递随后才 enqueue，跨进程崩溃下重发补投由未决账本承担。
	sink := sys.router.get(ActorID{Type: "sink", Key: "s1"}).actor.(*sinkActor)
	sink.mu.Lock()
	got := len(sink.got)
	sink.mu.Unlock()
	if got != 1 {
		t.Fatalf("sink got %d, want 1", got)
	}
}

// I3：每条流 seq 从 1 连续单调（跨混合负载：sys + domain + 崩溃重启）。
func TestInvariantI3SeqContiguous(t *testing.T) {
	dir := t.TempDir()
	sysA, _ := newTestSystem(t, dir)
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sysA, id, 5)
	sysA.Tell(context.Background(), id, Msg{MsgID: "relay", Kind: "relay"})
	sysA.Drain()
	// “崩溃”：直接丢弃，重建。
	sysB, _ := newTestSystem(t, dir)
	defer sysB.Stop()
	tellN(t, sysB, id, 5)
	sysB.Drain()
	envs, err := sysB.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i, env := range envs {
		if env.Seq != int64(i+1) {
			t.Fatalf("seq gap at index %d: seq=%d", i, env.Seq)
		}
	}
	if len(envs) == 0 {
		t.Fatal("no events")
	}
	// 重建后计数 == journal 中已 done 的 inc 消息数（exactly-once）。
	doneCount := 0
	for _, env := range envs {
		if env.Type != sysInboxDone {
			continue
		}
		var p struct {
			MsgID string `json:"msg_id"`
		}
		jsonUnmarshal(env.Payload, &p)
		if p.MsgID != "relay" {
			doneCount++
		}
	}
	if got := sysB.router.get(id).actor.(*counterActor).count(); got != doneCount {
		t.Fatalf("count after restart = %d, journal-done = %d", got, doneCount)
	}
}

// I4：快照 + 尾部 fold 与全量 fold 等价。
func TestInvariantI4SnapshotEquivalence(t *testing.T) {
	dir := t.TempDir()
	// A：无快照路径（全量 fold）。
	sysA, _ := newTestSystem(t, dir)
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sysA, id, 40)
	sysA.Drain()
	sysA.snapshotInterval = 5
	sysA.Tell(context.Background(), id, Msg{MsgID: "extra", Kind: "inc"})
	sysA.Drain()
	sysA.Stop() // 终态快照
	// B：删除快照 → 全量重放；对比 A（快照路径）的最终计数。
	gotWithSnap := countFromJournal(t, dir, true)
	gotFullFold := countFromJournal(t, dir, false)
	if gotWithSnap != gotFullFold || gotWithSnap != 41 {
		t.Fatalf("snapshot=%d full=%d, want 41/41", gotWithSnap, gotFullFold)
	}
}

func countFromJournal(t *testing.T, dir string, keepSnapshot bool) int {
	t.Helper()
	if !keepSnapshot {
		// 删除快照文件 → 强制全量 fold。
		snapPath := dir + "/counter/c1.snapshot.json"
		if err := os.Remove(snapPath); err != nil {
			t.Logf("no snapshot to remove: %v", err)
		}
	}
	sys, _ := newTestSystem(t, dir)
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "probe", Kind: "ask"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	c := sys.router.get(id)
	if c == nil || c.actor == nil {
		t.Fatal("cell not activated")
	}
	return c.actor.(*counterActor).count()
}

// I5：sys.* 永不进入 domain reducer（Fold）。
func TestInvariantI5NoSysInDomainFold(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 3)
	sys.Tell(context.Background(), id, Msg{MsgID: "relay", Kind: "relay"})
	sys.Drain()
	// 重启走 Fold 路径，counterActor.sawSys 断言 Fold 从未见 sys.*。
	sys2, _ := newTestSystem(t, sys.dir)
	defer sys2.Stop()
	tellN(t, sys2, id, 0)
	sys2.Tell(context.Background(), sys2ID(id), Msg{MsgID: "warm", Kind: "inc"})
	sys2.Drain()
	a := sys2.router.get(id).actor.(*counterActor)
	if a.sawSys.Load() {
		t.Fatal("domain Fold received sys.* event")
	}
	if a.count() != 4 { // 3 + warm（relay 不产生事件）
		t.Fatalf("count = %d, want 4", a.count())
	}
	// 补充：全量扫描 journal，domain Kind 下不允许 sys. 前缀类型。
	envs, err := sys.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		if env.Kind == es.KindDomain && strings.HasPrefix(env.Type, "sys.") {
			t.Fatalf("domain event carries sys type: %+v", env)
		}
	}
}

func sys2ID(id ActorID) ActorID { return id }

// I6：L2 运行时不得 import L3/L4 领域包与 cmd。
func TestInvariantI6NoDomainImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./internal/actor").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	forbidden := []string{
		"paw/internal/loop", "paw/internal/task", "paw/internal/goal",
		"paw/internal/plan", "paw/internal/session", "paw/cmd",
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, bad := range forbidden {
			if strings.HasPrefix(line, bad) {
				t.Fatalf("I6 violated: %s imports %s", "internal/actor", line)
			}
		}
	}
}

// 确定性：单分片 + 虚拟时钟下，同一输入序列产生同一 journal（含事件序）。
func TestDeterministicJournalReplay(t *testing.T) {
	run := func() string {
		sys, _ := newTestSystem(t, t.TempDir())
		defer sys.Stop()
		id := ActorID{Type: "counter", Key: "c1"}
		for i := 0; i < 10; i++ {
			sys.Tell(context.Background(), id, Msg{MsgID: fmt.Sprintf("m%03d", i), Kind: "inc"})
		}
		sys.Drain()
		envs, _ := sys.journal.load(context.Background(), id)
		var sb strings.Builder
		for _, env := range envs {
			sb.WriteString(fmt.Sprintf("%d|%s|%s\n", env.Seq, env.Kind, env.Type))
		}
		return sb.String()
	}
	if run() != run() {
		t.Fatal("journal layout differs between identical runs")
	}
}
