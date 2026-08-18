// crashprobe 是崩溃矩阵的子进程载体（spec ADR-8：协议 ①-④ 每步 SIGKILL）。
//
// 环境变量：
//
//	PROBE_DIR  - journal 目录
//	PROBE_STAGE - 崩溃位点（journal stage 名；空 = 不崩溃）
//	PROBE_HIT  - 第 N 次命中该位点时 SIGKILL 自身
//	PROBE_MODE - run | verify
//
// run：注册 counter/sink，投递 m0..m7 inc + relay，完成后写 DONE 标记退出 0。
// verify：直接读 journal 断言恢复不变量，打印 VERIFY-OK。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"paw/internal/actor"
	"paw/internal/es"
)

var lastCounter *probeCounter

type probeCounter struct {
	id        actor.ActorID
	processed map[string]int
}

func (a *probeCounter) ID() actor.ActorID { return a.id }

func (a *probeCounter) Receive(ctx *actor.Context, msg actor.Msg) {
	switch msg.Kind {
	case "inc":
		if _, done := a.processed[msg.MsgID]; done {
			return
		}
		_ = ctx.Persist("incremented", map[string]any{"msg_id": msg.MsgID}, actor.Buffered)
		a.processed[msg.MsgID] = 1
	case "relay":
		_ = ctx.Send(actor.ActorID{Type: "sink", Key: "s1"},
			actor.Msg{MsgID: "relay-relay", Kind: "note", Payload: msg.MsgID})
	}
}

func (a *probeCounter) Fold(env es.Envelope) error {
	if env.Type != "incremented" {
		return nil
	}
	var p struct {
		MsgID string `json:"msg_id"`
	}
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	a.processed[p.MsgID] = 1
	return nil
}

func (a *probeCounter) Snapshot() (json.RawMessage, error) {
	return json.Marshal(map[string]any{"processed": a.processed})
}

func (a *probeCounter) Restore(state json.RawMessage) error {
	var p struct {
		Processed map[string]int `json:"processed"`
	}
	if err := json.Unmarshal(state, &p); err != nil {
		return err
	}
	if p.Processed != nil {
		a.processed = p.Processed
	}
	return nil
}

type probeSink struct {
	id actor.ActorID
}

func (a *probeSink) ID() actor.ActorID                     { return a.id }
func (a *probeSink) Receive(_ *actor.Context, _ actor.Msg) {}

func main() {
	dir := os.Getenv("PROBE_DIR")
	stage := os.Getenv("PROBE_STAGE")
	mode := os.Getenv("PROBE_MODE")
	var hit int
	fmt.Sscanf(os.Getenv("PROBE_HIT"), "%d", &hit)

	if mode == "verify" {
		verify(dir)
		return
	}
	if mode == "debug" {
		debugFold(dir)
		return
	}

	sys := actor.NewSystem(dir, actor.WithShards(1), actor.WithPassivation(0),
		actor.WithSnapshotInterval(3), actor.WithLogger(func(string, ...any) {}))
	sys.Register("counter", func(id actor.ActorID) actor.Actor {
		return &probeCounter{id: id, processed: map[string]int{}}
	})
	sys.Register("sink", func(id actor.ActorID) actor.Actor { return &probeSink{id: id} })

	if stage != "" {
		count := 0
		sys.SetJournalHook(func(s string, _ actor.ActorID) {
			if s != stage {
				return
			}
			count++
			if count == hit {
				// 真实进程死亡：不给任何清理机会。
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
		})
	}

	id := actor.ActorID{Type: "counter", Key: "c1"}
	for i := 0; i < 8; i++ {
		if err := sys.Tell(context.Background(), id, actor.Msg{MsgID: fmt.Sprintf("m%d", i), Kind: "inc"}); err != nil {
			fmt.Fprintln(os.Stderr, "tell:", err)
			os.Exit(3)
		}
	}
	if err := sys.Tell(context.Background(), id, actor.Msg{MsgID: "relay", Kind: "relay"}); err != nil {
		fmt.Fprintln(os.Stderr, "tell relay:", err)
		os.Exit(3)
	}
	sys.Drain()
	sys.Stop()
	if err := os.WriteFile(filepath.Join(dir, "DONE"), []byte("ok"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write done:", err)
		os.Exit(3)
	}
	fmt.Println("COMPLETE")
}

// debugFold 用运行时激活路径重建状态，打印 fold 后 processed 与快照对照。
func debugFold(dir string) {
	sys := actor.NewSystem(dir, actor.WithShards(1), actor.WithPassivation(0),
		actor.WithLogger(func(string, ...any) {}))
	sys.Register("counter", func(id actor.ActorID) actor.Actor {
		c := &probeCounter{id: id, processed: map[string]int{}}
		lastCounter = c
		return c
	})
	id := actor.ActorID{Type: "counter", Key: "c1"}
	_ = sys.Tell(context.Background(), id, actor.Msg{MsgID: "probe-debug", Kind: "noop"})
	sys.Drain()
	keys := make([]string, 0, len(lastCounter.processed))
	for k := range lastCounter.processed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("DEBUG-FOLD processed=%v (n=%d)\n", keys, len(keys))
	sys.Stop()
}
func verify(dir string) {
	store, err := es.NewJSONLStore(dir, "counter")
	must(err)
	envs, _, err := store.Load(context.Background(), "c1")
	must(err)

	incremented := map[string]bool{}
	received := map[string]int{}
	done := map[string]int{}
	var seqOK = true
	for i, env := range envs {
		if env.Seq != int64(i+1) {
			seqOK = false
		}
		switch env.Type {
		case "incremented":
			var p struct {
				MsgID string `json:"msg_id"`
			}
			must(json.Unmarshal(env.Payload, &p))
			incremented[p.MsgID] = true
		case "sys.inbox.received", "sys.inbox.done":
			var p struct {
				MsgID string `json:"msg_id"`
			}
			must(json.Unmarshal(env.Payload, &p))
			if env.Type == "sys.inbox.received" {
				received[p.MsgID]++
			} else {
				done[p.MsgID]++
			}
		}
	}
	if !seqOK {
		fatal("seq gap in counter stream")
	}
	// done 允许多条（崩溃重投合法，账本是幂等集合非计数器）；
	// exactly-once 的事实证据是每消息恰一条 incremented。
	for msgID := range incremented {
		if done[msgID] < 1 {
			fatal(fmt.Sprintf("incremented %s never done", msgID))
		}
		if countType(envs, "incremented", msgID) != 1 {
			fatal(fmt.Sprintf("incremented %s duplicated", msgID))
		}
	}
	// 落盘 received 的消息必已闭合（无 pending 残留）。
	for msgID, n := range received {
		if done[msgID] == 0 {
			fatal(fmt.Sprintf("message %s received=%d never done", msgID, n))
		}
	}
	// sink 流：relay 若已投递必闭合。
	sinkStore, err := es.NewJSONLStore(dir, "sink")
	must(err)
	sinkEnvs, _, err := sinkStore.Load(context.Background(), "s1")
	must(err)
	sinkRecv, sinkDone := 0, 0
	for _, env := range sinkEnvs {
		var p struct {
			MsgID string `json:"msg_id"`
		}
		if env.Type == "sys.inbox.received" || env.Type == "sys.inbox.done" {
			must(json.Unmarshal(env.Payload, &p))
			if p.MsgID == "relay-relay" {
				if env.Type == "sys.inbox.received" {
					sinkRecv++
				} else {
					sinkDone++
				}
			}
		}
	}
	if sinkRecv > 0 && sinkDone != 1 {
		fatal(fmt.Sprintf("relay relay-relay recv=%d done=%d", sinkRecv, sinkDone))
	}
	if _, err := os.Stat(filepath.Join(dir, "DONE")); err != nil {
		fatal("DONE marker missing")
	}
	fmt.Println("VERIFY-OK")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(3)
	}
}

// countType 统计某消息在指定事件类型下的出现次数。
func countType(envs []es.Envelope, typ, msgID string) int {
	n := 0
	for _, env := range envs {
		if env.Type != typ {
			continue
		}
		var p struct {
			MsgID string `json:"msg_id"`
		}
		if err := json.Unmarshal(env.Payload, &p); err == nil && p.MsgID == msgID {
			n++
		}
	}
	return n
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "verify failed:", msg)
	os.Exit(4)
}

var _ = time.Second
