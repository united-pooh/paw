package actor

import (
	"context"
	"errors"
	"testing"
	"time"

	"paw/internal/es"
)

// TestSystemSuspendHoldsMailboxUntilResume 验证 System 级挂起语义：
// 挂起后新消息滞留邮箱（不处理），Resume 后恢复消费。
func TestSystemSuspendHoldsMailboxUntilResume(t *testing.T) {
	dir := t.TempDir()
	id := ActorID{Type: "counter", Key: "c1"}
	counter := newCounter("c1")
	system := NewSystem(dir, WithShards(1))
	defer system.Stop()
	system.Register("counter", func(ActorID) Actor { return counter })

	if err := system.Tell(context.Background(), id, Msg{MsgID: "m1", Kind: "inc", Durability: Durable}); err != nil {
		t.Fatalf("Tell: %v", err)
	}
	system.Drain()

	if err := system.Suspend(id, "await decision"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if err := system.Tell(context.Background(), id, Msg{MsgID: "m2", Kind: "inc", Durability: Durable}); err != nil {
		t.Fatalf("Tell while suspended: %v", err)
	}
	system.Drain()
	counter.mu.Lock()
	held := len(counter.processed)
	counter.mu.Unlock()
	if held != 1 {
		t.Fatalf("processed while suspended = %d, want 1 (message must be held)", held)
	}

	if err := system.Resume(id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	system.Drain()
	counter.mu.Lock()
	resumed := len(counter.processed)
	counter.mu.Unlock()
	if resumed != 2 {
		t.Fatalf("processed after resume = %d, want 2", resumed)
	}
}

// TestSystemActivateRebuildsStateWithoutMessages 验证 Activate 幂等且
// 重建后 actor 可应答（无需投递任何消息）。
func TestSystemActivateRebuildsStateWithoutMessages(t *testing.T) {
	dir := t.TempDir()
	id := ActorID{Type: "counter", Key: "c1"}
	counter := newCounter("c1")
	system := NewSystem(dir, WithShards(1))
	defer system.Stop()
	system.Register("counter", func(ActorID) Actor { return counter })

	if err := system.Activate(id); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	system.Drain()
	reply, err := system.Ref(id).Ask(context.Background(), Msg{Kind: "ask"}, time.Second)
	if err != nil {
		t.Fatalf("Ask after Activate: %v", err)
	}
	if n, ok := reply.Payload.(int); !ok || n != 0 {
		t.Fatalf("count reply = %#v, want 0", reply.Payload)
	}
	if err := system.Activate(id); err != nil {
		t.Fatalf("Activate (idempotent): %v", err)
	}
	system.Drain()
}

// TestSystemPersistDomainAppendsDurableEvent 验证 PersistDomain 契约：
// 事件进入 Durable 流但不改变活动 actor 内存态；重启激活后经 fold 重建。
func TestSystemPersistDomainAppendsDurableEvent(t *testing.T) {
	dir := t.TempDir()
	id := ActorID{Type: "counter", Key: "c1"}
	counter := newCounter("c1")
	system := NewSystem(dir, WithShards(1))
	defer system.Stop()
	system.Register("counter", func(ActorID) Actor { return counter })

	if err := system.Activate(id); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := system.PersistDomain(context.Background(), id, "incremented", map[string]any{"msg_id": "px-1"}); err != nil {
		t.Fatalf("PersistDomain: %v", err)
	}
	system.Drain()

	counter.mu.Lock()
	live := len(counter.processed)
	counter.mu.Unlock()
	if live != 0 {
		t.Fatalf("live state mutated by PersistDomain = %d, want 0 (changes must cross the mailbox)", live)
	}

	envs, err := system.journal.load(context.Background(), id)
	if err != nil {
		t.Fatalf("journal load: %v", err)
	}
	found := false
	for _, env := range envs {
		if env.Kind == es.KindDomain && env.Type == "incremented" {
			found = true
		}
	}
	if !found {
		t.Fatalf("durable domain event missing from journal: %+v", envs)
	}

	restarted := NewSystem(dir, WithShards(1))
	defer restarted.Stop()
	rebuilt := newCounter("c1")
	restarted.Register("counter", func(ActorID) Actor { return rebuilt })
	if err := restarted.Activate(id); err != nil {
		t.Fatalf("Activate after restart: %v", err)
	}
	restarted.Drain()
	rebuilt.mu.Lock()
	folded := len(rebuilt.processed)
	rebuilt.mu.Unlock()
	if folded != 1 {
		t.Fatalf("folded domain events after restart = %d, want 1", folded)
	}
}

// TestSystemAdminRejectsInvalidTargets 覆盖管理端口错误路径：
// 非法 ActorID、未注册类型、已停机。
func TestSystemAdminRejectsInvalidTargets(t *testing.T) {
	system := NewSystem(t.TempDir(), WithShards(1))
	defer system.Stop()
	system.Register("counter", func(ActorID) Actor { return newCounter("x") })

	bad := ActorID{Type: "counter"} // 空 key
	if err := system.Suspend(bad, "r"); err == nil {
		t.Fatal("Suspend on invalid id: want error")
	}
	if err := system.Activate(bad); err == nil {
		t.Fatal("Activate on invalid id: want error")
	}
	if err := system.PersistDomain(context.Background(), bad, "e", nil); err == nil {
		t.Fatal("PersistDomain on invalid id: want error")
	}

	unknown := ActorID{Type: "missing", Key: "k"}
	for name, fn := range map[string]func() error{
		"Suspend":  func() error { return system.Suspend(unknown, "r") },
		"Activate": func() error { return system.Activate(unknown) },
		"Resume":   func() error { return system.Resume(unknown) },
		"PersistDomain": func() error {
			return system.PersistDomain(context.Background(), unknown, "e", nil)
		},
	} {
		if err := fn(); !errors.Is(err, ErrNoProvider) {
			t.Fatalf("%s on unregistered type = %v, want ErrNoProvider", name, err)
		}
	}

	stopped := NewSystem(t.TempDir(), WithShards(1))
	stopped.Stop()
	stopped.Register("counter", func(ActorID) Actor { return newCounter("k") })
	id := ActorID{Type: "counter", Key: "k"}
	for name, fn := range map[string]func() error{
		"Suspend":  func() error { return stopped.Suspend(id, "r") },
		"Activate": func() error { return stopped.Activate(id) },
		"Resume":   func() error { return stopped.Resume(id) },
	} {
		if err := fn(); !errors.Is(err, ErrStopped) {
			t.Fatalf("%s after Stop = %v, want ErrStopped", name, err)
		}
	}
}
