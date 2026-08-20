package actor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/es"
)

// logCapture 收集系统日志（分片线程与测试线程共享，需互斥）。
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// writeSeqLine 向流文件追加一条合法 envelope（指定 seq，用于制造违例尾部）。
func writeSeqLine(t *testing.T, path string, seq int64) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"corrupt": "tail"})
	env := es.Envelope{
		Seq:           seq,
		Type:          "incremented",
		OccurredAt:    time.Unix(1, 0).UTC(),
		SchemaVersion: 1,
		Kind:          es.KindDomain,
		Payload:       payload,
	}
	line, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestSeqGapJournalSelfHealsOnActivation 验证多实例双写者留下的 seq 违例尾部
// 在激活时被截除：合法前缀的状态完整恢复，后续消息正常处理，恢复动作有日志。
// 回归：旧实现激活失败 → pump 以 nil ledger 消费 → nil pointer panic → 监督
// 退避重启 → 激活再次失败的重启风暴，actor 永久不可用。
func TestSeqGapJournalSelfHealsOnActivation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	id := ActorID{Type: "counter", Key: "c1"}

	sys1, _ := newTestSystem(t, dir)
	tellN(t, sys1, id, 3)
	sys1.Drain()
	sys1.Stop()

	// 模拟双写者交错：尾部追加一条回退 seq（合法前缀 9 条事件，尾部 seq=5）。
	streamPath := filepath.Join(dir, "counter", "c1.events.jsonl")
	writeSeqLine(t, streamPath, 5)

	logs := &logCapture{}
	vc := NewVirtualClock()
	sys2 := NewSystem(dir,
		WithShards(1),
		WithClock(vc),
		WithPassivation(0),
		WithLogger(logs.log),
	)
	sys2.Register("counter", func(id ActorID) Actor { return newCounter(id.Key) })
	defer sys2.Stop()

	// 激活应自愈并处理新消息：3 条前缀事件恢复 + 1 条新事件 = 4。
	if err := sys2.Tell(ctx, id, Msg{MsgID: "m100", Kind: "inc"}); err != nil {
		t.Fatalf("tell after corruption: %v", err)
	}
	sys2.Drain()
	reply, err := sys2.Ref(id).Ask(ctx, Msg{Kind: "ask"}, time.Second)
	if err != nil {
		t.Fatalf("ask after self-heal: %v", err)
	}
	if reply.Payload != 4 {
		t.Fatalf("counter = %v, want 4 (prefix restored + new event)", reply.Payload)
	}
	if !logs.contains("seq gap") || !logs.contains("dropped 1 tail events") {
		t.Fatalf("recovery log missing; logs: %v", logs.lines)
	}
	if logs.contains("panic") {
		t.Fatalf("self-heal must not panic; logs: %v", logs.lines)
	}
}

// failingLoadStore 的 Load 永远失败（非 seq gap，不可自愈），用于构造持续
// 激活失败场景。
type failingLoadStore struct {
	err error
}

func (s *failingLoadStore) Append(context.Context, string, []es.Envelope) (int64, int64, error) {
	return 0, 0, nil
}
func (s *failingLoadStore) Load(context.Context, string) ([]es.Envelope, bool, error) {
	return nil, false, s.err
}
func (s *failingLoadStore) WriteSnapshot(context.Context, string, int64, json.RawMessage) error {
	return nil
}
func (s *failingLoadStore) ReadSnapshot(context.Context, string) (es.Snapshot, bool, error) {
	return es.Snapshot{}, false, nil
}

// TestFailedActivationParksMessagesWithoutPanic 验证激活持续失败时消息驻留
// 邮箱而不是被 nil actor/ledger 消费：无 panic、无监督重启、无死信。
func TestFailedActivationParksMessagesWithoutPanic(t *testing.T) {
	logs := &logCapture{}
	vc := NewVirtualClock()
	sys := NewSystem(t.TempDir(),
		WithShards(1),
		WithClock(vc),
		WithPassivation(0),
		WithLogger(logs.log),
		WithStreamStore("counter", &failingLoadStore{err: errors.New("load exploded")}),
	)
	sys.Register("counter", func(id ActorID) Actor { return newCounter(id.Key) })
	defer sys.Stop()

	id := ActorID{Type: "counter", Key: "c1"}
	for _, msgID := range []string{"m1", "m2"} {
		if err := sys.Tell(context.Background(), id, Msg{MsgID: msgID, Kind: "inc"}); err != nil {
			t.Fatalf("tell %s: %v", msgID, err)
		}
	}
	sys.Drain()
	// 重激活同样失败（Load 仍报错），同样不得 panic。
	if c := sys.router.ensure(sys, id); c != nil {
		c.reactivate()
	}
	sys.Drain()

	if logs.contains("panic") {
		t.Fatalf("failed activation must not panic; logs: %v", logs.lines)
	}
	if dead := sys.DeadLetters(); len(dead) != 0 {
		t.Fatalf("no dead letters expected, got %v", dead)
	}
	if !logs.contains("activate counter/c1 failed") {
		t.Fatalf("activation failure should be logged; logs: %v", logs.lines)
	}
}
