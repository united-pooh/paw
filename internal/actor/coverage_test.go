package actor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/es"
)

func TestNewMailboxDefaultCap(t *testing.T) {
	m := newMailbox(0) // 非法容量回退默认
	for i := 0; i < defaultMailboxCap; i++ {
		if err := m.push(delivery{}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if err := m.push(delivery{}); err != ErrMailboxFull {
		t.Fatalf("want full, got %v", err)
	}
}

// 非 EventSourced actor 的钝化快照路径（提前返回，不写文件）。
func TestSnapshotSkipsNonEventSourced(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "sink", Key: "s1"}
	_ = sys.Tell(context.Background(), id, Msg{MsgID: "x", Kind: "note"})
	sys.Drain()
	c := sys.router.get(id)
	c.snapshot(context.Background()) // 不应 panic/写快照
	if _, ok, _ := sys.journal.readSnapshot(context.Background(), id); ok {
		t.Fatal("sink should not have snapshot")
	}
}

// 快照写失败（目录不可写）不致命：仅日志。
func TestSnapshotWriteFailureNonFatal(t *testing.T) {
	dir := t.TempDir()
	sys, vc := newTestSystem(t, dir)
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	tellN(t, sys, id, 2)
	sys.Drain()
	// 事后把流目录替换为文件，使写快照失败。
	streamDir := filepath.Join(dir, "counter")
	snapTarget := filepath.Join(streamDir, "c1.snapshot.json")
	_ = snapTarget
	os.RemoveAll(streamDir)
	if err := os.WriteFile(streamDir, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := sys.router.get(id)
	c.snapshot(context.Background()) // 应静默失败
	if _, err := os.Stat(streamDir); err != nil {
		t.Fatalf("dir should remain blocked file: %v", err)
	}
	_ = vc
}

// readSnapshot 损坏文件报错。
func TestReadSnapshotCorrupt(t *testing.T) {
	dir := t.TempDir()
	streamDir := filepath.Join(dir, "counter")
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "c1.snapshot.json"), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys, _ := newTestSystem(t, dir)
	defer sys.Stop()
	if _, ok, err := sys.journal.readSnapshot(context.Background(), ActorID{Type: "counter", Key: "c1"}); err == nil || ok {
		t.Fatalf("corrupt snapshot should error: ok=%v err=%v", ok, err)
	}
}

// 死信事件折叠为隔离标志 + 未知 sys 类型前跳过。
func TestFoldDeadLetterAndDuration(t *testing.T) {
	l := newRuntimeLedger()
	_ = l.foldRuntime(testEnvelope(sysDeadLetter, map[string]string{"msg_id": "m"}))
	if !l.quarantined {
		t.Fatal("dead letter should set quarantine flag")
	}
	if d := durationJSON(1500).Duration(); d != 1500 {
		t.Fatalf("duration = %d", d)
	}
}

// 发往非法地址：route 失败但 Outbox sent 已落盘、delivered 无 → pending 保留，
// 重激活时由恢复扫描再次尝试补发（at-least-once 容忍不可达目标）。
func TestSendToInvalidKeepsPendingOutbox(t *testing.T) {
	sys, _ := newTestSystem(t, t.TempDir())
	defer sys.Stop()
	id := ActorID{Type: "counter", Key: "c1"}
	if err := sys.Tell(context.Background(), id, Msg{MsgID: "warm", Kind: "inc"}); err != nil {
		t.Fatal(err)
	}
	sys.Drain()
	c := sys.router.get(id)
	bad := ActorID{Type: "counter", Key: "../escape"}
	if err := c.send(bad, Msg{MsgID: "to-bad", Kind: "note"}); err == nil {
		t.Fatal("send to invalid id should fail")
	}
	c.mu.Lock()
	pending := len(c.ledger.outbox)
	c.mu.Unlock()
	if pending != 1 {
		t.Fatalf("outbox pending = %d, want 1", pending)
	}
	envs, _ := sys.journal.load(context.Background(), id)
	sent, delivered := 0, 0
	for _, env := range envs {
		switch env.Type {
		case sysOutboxSent:
			sent++
		case sysOutboxDelivered:
			delivered++
		}
	}
	if sent != 1 || delivered != 0 {
		t.Fatalf("sent=%d delivered=%d, want 1/0", sent, delivered)
	}
}

// PendingTimers 混合取消/未取消。
func TestPendingTimersMixed(t *testing.T) {
	vc := NewVirtualClock()
	keep := vc.After(10*time.Millisecond, func() {})
	vc.After(20*time.Millisecond, func() {})
	cancel := vc.After(30*time.Millisecond, func() {})
	cancel()
	pending := vc.PendingTimers()
	if len(pending) != 2 || pending[0] != 10*time.Millisecond || pending[1] != 20*time.Millisecond {
		t.Fatalf("pending = %v", pending)
	}
	_ = keep
	vc.Advance(time.Hour)
	if len(vc.PendingTimers()) != 0 {
		t.Fatal("all fired")
	}
}

// journal append 编码失败（不可序列化 payload）报错而非 panic。
func TestJournalAppendEncodeError(t *testing.T) {
	j := newJournal(t.TempDir(), RealClock{})
	_, err := j.append(context.Background(), ActorID{Type: "counter", Key: "k"},
		es.KindRuntime, "sys.test", make(chan int))
	if err == nil {
		t.Fatal("chan payload should fail to encode")
	}
	// appendDomain 空组提交为 no-op。
	if first, last, err := j.appendDomain(context.Background(), ActorID{Type: "counter", Key: "k"}, nil); err != nil || first != 0 {
		t.Fatalf("empty appendDomain = %d %d %v", first, last, err)
	}
}

// cloneRaw nil 安全。
func TestCloneRawNil(t *testing.T) {
	if cloneRaw(nil) != nil {
		t.Fatal("nil clone should stay nil")
	}
	raw := json.RawMessage(`{"a":1}`)
	got := cloneRaw(raw)
	if string(got) != string(raw) {
		t.Fatalf("clone = %s", got)
	}
}
