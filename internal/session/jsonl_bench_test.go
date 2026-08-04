package session

import (
	"context"
	"fmt"
	"testing"

	"paw/internal/message"
)

// BenchmarkJSONLAppendGrowth 度量"向已有 N 条历史的 session 追加一批记录"的延迟。
// 该场景对应 docs/performance-deferred-todos.md P0：appendRecords 每次都会
// 重新 readOwnRecords 从头解析整份 transcript.jsonl 来计算下一个 sequence。
func BenchmarkJSONLAppendGrowth(b *testing.B) {
	for _, historySize := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("history=%d", historySize), func(b *testing.B) {
			store := newBenchStore(b)
			ctx := context.Background()
			sessionID := benchSessionID(b)
			if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
				b.Fatal(err)
			}
			seed := make([]message.Message, historySize)
			for i := range seed {
				seed[i] = message.Message{Role: message.RoleUser, Content: fmt.Sprintf("seed message %d with some padding text", i)}
			}
			// 分小批写入，避免单次 append 的时间掩盖"重复读取"成本。
			const seedBatch = 200
			for start := 0; start < len(seed); start += seedBatch {
				end := start + seedBatch
				if end > len(seed) {
					end = len(seed)
				}
				if err := store.Append(ctx, sessionID, seed[start:end]...); err != nil {
					b.Fatal(err)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "benchmark append payload"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONLAppendBatch 度量一次追加较大批次（如一轮完整对话的
// BeginTurn/Commit 合并写入）的延迟。
func BenchmarkJSONLAppendBatch(b *testing.B) {
	for _, batchSize := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
			store := newBenchStore(b)
			ctx := context.Background()
			sessionID := benchSessionID(b)
			if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
				b.Fatal(err)
			}
			msgs := make([]message.Message, batchSize)
			for i := range msgs {
				msgs[i] = message.Message{Role: message.RoleUser, Content: "batch payload message"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := store.Append(ctx, sessionID, msgs...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONLAppendParallelSessions 度量 N 个 session 并行追加时的吞吐。
// 对比单一全局锁（不同 session 串行化）与 session 粒度锁（并行写入）。
func BenchmarkJSONLAppendParallelSessions(b *testing.B) {
	for _, sessions := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("sessions=%d", sessions), func(b *testing.B) {
			store := newBenchStore(b)
			ctx := context.Background()
			ids := make([]string, sessions)
			for i := range ids {
				ids[i] = benchSessionID(b)
				if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: ids[i]}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				// 每个 worker 固定写一个 session，衡量跨 session 并行度。
				for pb.Next() {
					if err := store.Append(ctx, ids[b.N%len(ids)], message.Message{Role: message.RoleUser, Content: "parallel payload"}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func newBenchStore(b *testing.B) *JSONLStore {
	b.Helper()
	store, err := NewJSONLStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	return store
}

func benchSessionID(b *testing.B) string {
	b.Helper()
	id, err := GenerateSessionID()
	if err != nil {
		b.Fatal(err)
	}
	return id
}
