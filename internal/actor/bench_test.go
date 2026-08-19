package actor

import (
	"context"
	"testing"
	"time"
)

// spec §10 性能预算基准（scripts/bench_actor.sh 入口）：
//   - BenchmarkDurableAskRoundtrip：Durable 消息摊销开销（预算 < 1ms，
//     本地 NVMe + group commit；每次 Ask 含 inbox.received 落盘、
//     领域事件持久化与回执投递）。
//   - BenchmarkColdActivation：冷激活延迟（预算 < 50ms，快照 + ≤1k
//     尾事件；本基准用 1k 消息 ≈ 2k+ 事件、关闭快照，测纯 fold 路径）。

func BenchmarkDurableAskRoundtrip(b *testing.B) {
	dir := b.TempDir()
	system := NewSystem(dir, WithShards(1))
	defer system.Stop()
	system.Register("counter", func(ActorID) Actor { return newCounter("bench") })
	id := ActorID{Type: "counter", Key: "bench"}
	ctx := context.Background()
	// 预激活，排除首次激活噪声。
	if _, err := system.Ref(id).Ask(ctx, Msg{Kind: "ask"}, 10*time.Second); err != nil {
		b.Fatalf("warmup Ask: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := system.Ref(id).Ask(ctx, Msg{Kind: "ask"}, 10*time.Second); err != nil {
			b.Fatalf("Ask: %v", err)
		}
	}
}

func BenchmarkColdActivation(b *testing.B) {
	dir := b.TempDir()
	const messages = 1000
	seed := NewSystem(dir, WithShards(1), WithSnapshotInterval(1<<30))
	seed.Register("counter", func(ActorID) Actor { return newCounter("bench") })
	id := ActorID{Type: "counter", Key: "bench"}
	ctx := context.Background()
	for i := 0; i < messages; i++ {
		if err := seed.Tell(ctx, id, Msg{MsgID: "m" + itoa(i), Kind: "inc", Durability: Durable}); err != nil {
			b.Fatalf("seed Tell: %v", err)
		}
		if i%100 == 99 {
			seed.Drain() // 邮箱上限 256，分批排空避免溢出
		}
	}
	seed.Drain()
	seed.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		system := NewSystem(dir, WithShards(1), WithSnapshotInterval(1<<30))
		system.Register("counter", func(ActorID) Actor { return newCounter("bench") })
		if err := system.Activate(id); err != nil {
			b.Fatalf("Activate: %v", err)
		}
		system.Drain()
		reply, err := system.Ref(id).Ask(ctx, Msg{Kind: "ask"}, 10*time.Second)
		if err != nil {
			b.Fatalf("Ask: %v", err)
		}
		if n, ok := reply.Payload.(int); !ok || n != messages {
			b.Fatalf("replayed count = %#v, want %d (fold 丢失事件)", reply.Payload, messages)
		}
		system.Stop()
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
