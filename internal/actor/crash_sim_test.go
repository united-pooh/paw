package actor

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// 崩溃模拟矩阵（spec §6.2 / ADR-8）：在协议 ①②③④ 各位点注入“进程死亡”
// （内存态丢弃、journal 存活），重建后断言：
//   - 无消息丢失（计数收敛到精确值——exactly-once 经事件溯源收敛）；
//   - seq 连续（I3）；
//   - Outbox 未决补发（sink 收到 relay）。
//
// 真实 SIGKILL 语义由 crashproc_test.go 的子进程矩阵覆盖；本文件覆盖
// 全部协议路径的进程内等价形态（快、可测覆盖率）。
func TestSimulatedCrashMatrix(t *testing.T) {
	stages := []string{
		StageInboxReceived,
		StageDomainFlushed,
		StageOutboxSent,
		StageInboxDone,
		StageSnapshotted,
	}
	for _, stage := range stages {
		for hit := 1; hit <= 2; hit++ {
			name := fmt.Sprintf("%s/hit%d", stage, hit)
			t.Run(name, func(t *testing.T) {
				runCrashScenario(t, stage, hit)
			})
		}
	}
}

func runCrashScenario(t *testing.T, stage string, hit int) {
	t.Helper()
	dir := t.TempDir()
	send := func(sys *System) {
		t.Helper()
		id := ActorID{Type: "counter", Key: "c1"}
		for i := 0; i < 8; i++ {
			if err := sys.Tell(context.Background(), id, Msg{MsgID: fmt.Sprintf("m%d", i), Kind: "inc"}); err != nil {
				t.Fatalf("tell m%d: %v", i, err)
			}
		}
		if err := sys.Tell(context.Background(), id, Msg{MsgID: "relay", Kind: "relay"}); err != nil {
			t.Fatalf("tell relay: %v", err)
		}
		sys.Drain()
	}

	// 第一段：带崩溃钩子执行；无论是否触发崩溃，直接丢弃内存态。
	sysA, _ := newTestSystem(t, dir)
	var fired atomic.Int64
	sysA.journal.setHook(func(s string, id ActorID) {
		if s != stage {
			return
		}
		if fired.Add(1) == int64(hit) {
			panic(errSimCrash)
		}
	})
	sysA.snapshotInterval = 3 // 覆盖 ④ 快照位点
	send(sysA)
	sysA.Drain() // 等旧分片静默（poisoned cell 不再处理）

	// 第二段：同目录重建——激活扫描应重投未完结消息并补发未决 Outbox。
	sysB, _ := newTestSystem(t, dir)
	defer sysB.Stop()
	sysB.Drain()
	id := ActorID{Type: "counter", Key: "c1"}
	// 轻推一次触发激活（若尚未激活）。
	_ = sysB.Tell(context.Background(), id, Msg{MsgID: "noop", Kind: "noop"})
	sysB.Drain()

	// 期望值从 journal 推导：已落盘 received 的 inc 消息必须全部完成
	//（exactly-once）；尚未落盘（仅在内存邮箱）的丢失属 at-least-once
	// 边界语义（发送方责任）。
	envA, err := sysB.journal.load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	received, done := map[string]bool{}, map[string]bool{}
	for _, env := range envA {
		switch env.Type {
		case sysInboxReceived:
			var p struct {
				MsgID string `json:"msg_id"`
			}
			jsonUnmarshal(env.Payload, &p)
			received[p.MsgID] = true
		case sysInboxDone:
			var p struct {
				MsgID string `json:"msg_id"`
			}
			jsonUnmarshal(env.Payload, &p)
			done[p.MsgID] = true
		}
	}
	want := 0
	for i := 0; i < 8; i++ {
		if done[fmt.Sprintf("m%d", i)] {
			want++
		}
	}
	c := sysB.router.get(id)
	if c == nil || c.actor == nil {
		t.Fatal("cell not activated after recovery")
	}
	if got := c.actor.(*counterActor).count(); got != want {
		t.Fatalf("count after crash@%s/hit%d = %d, want %d (journal-derived)", stage, hit, got, want)
	}
	// 重投后无残留 pending：全部 received 均已 done。
	for mid := range received {
		if !done[mid] {
			t.Fatalf("message %s received but never completed after recovery", mid)
		}
	}
	// Outbox 补发：若 relay 已落盘 sent，则 sink 必须收到恰好一次。
	if received["relay"] {
		sinkID := ActorID{Type: "sink", Key: "s1"}
		// 轻推激活（若未决补发未触及 sink——如未发生崩溃）。
		_ = sysB.Tell(context.Background(), sinkID, Msg{MsgID: "noop", Kind: "noop"})
		sysB.Drain()
		sinkCell := sysB.router.get(sinkID)
		if sinkCell == nil || sinkCell.actor == nil {
			t.Logf("[debug] sink tell err check: cell=%v", sinkCell != nil)
			t.Fatal("sink not activated")
		}
		// 投递事实以 sink 流的收件账本为准（内存态不跨重建）。
		sinkEnvs, err := sysB.journal.load(context.Background(), sinkID)
		if err != nil {
			t.Fatal(err)
		}
		var sinkGot, sinkDone bool
		for _, env := range sinkEnvs {
			var p struct {
				MsgID string `json:"msg_id"`
			}
			if env.Type == sysInboxReceived || env.Type == sysInboxDone {
				jsonUnmarshal(env.Payload, &p)
				if p.MsgID == "relay-relay" {
					if env.Type == sysInboxReceived {
						sinkGot = true
					} else {
						sinkDone = true
					}
				}
			}
		}
		if !sinkGot || !sinkDone {
			t.Fatalf("sink journal: relay received=%v done=%v, want true/true", sinkGot, sinkDone)
		}
		sink := sinkCell.actor.(*sinkActor)
		sink.mu.Lock()
		relayCount := sink.got["relay-relay"]
		sink.mu.Unlock()
		if relayCount > 1 {
			t.Fatalf("sink relay count = %d, want ≤1 (dedup)", relayCount)
		}
	}
	// I3：seq 连续（es.Load 内建校验，能读出即无 gap）。
	if _, err := sysB.journal.load(context.Background(), id); err != nil {
		t.Fatalf("journal after crash: %v", err)
	}
}
