package subagent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"paw/internal/model"
)

// BenchmarkStreamEventBackpressure 度量 sendModelStreamEvent 在消费者快/慢两种
// 速度下发送 N 个 delta 事件的吞吐。当前事件 channel 无缓冲，慢消费者会把
// 模型侧（生产者）阻塞在 channel 发送上（端到端反压）。
func BenchmarkStreamEventBackpressure(b *testing.B) {
	for _, bufSize := range []int{0, 64, 256} {
		for _, consumer := range []string{"fast", "slow"} {
			b.Run(fmt.Sprintf("buf=%d/%s", bufSize, consumer), func(b *testing.B) {
				ctx := context.Background()
				events := make(chan model.StreamEvent, bufSize)
				stop := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						case <-events:
						}
					}
				}()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = sendModelStreamEvent(ctx, events, model.StreamEvent{Delta: "x"})
					if consumer == "slow" {
						// 模拟慢消费者：每个事件都让出调度，放大约束。
						time.Sleep(10 * time.Microsecond)
					}
				}
				b.StopTimer()
				close(stop)
				wg.Wait()
			})
		}
	}
}

// BenchmarkStreamEventCancel 度量消费者提前退出（ctx 取消）时，发送者多久
// 能从阻塞的 channel 发送中返回。
func BenchmarkStreamEventCancel(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan model.StreamEvent)
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sendModelStreamEvent(ctx, events, model.StreamEvent{Delta: "x"})
	}
}
