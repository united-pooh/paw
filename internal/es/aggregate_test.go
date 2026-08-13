package es

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testState is a minimal aggregate state for loader tests: it records the
// applied event types and its own seq, and snapshots/restores itself.
type testState struct {
	status string
	seq    int64
}

func (s *testState) Apply(payload Payload, env Envelope) error {
	s.seq = env.Seq
	if p, ok := payload.(map[string]any); ok {
		if r, ok := p["reason"].(string); ok {
			s.status = "paused:" + r
			return nil
		}
	}
	s.status = env.Type
	return nil
}

func (s *testState) Snapshot() (json.RawMessage, error) {
	return json.Marshal(s)
}

func (s *testState) Restore(data json.RawMessage) error {
	return json.Unmarshal(data, s)
}

// testCommand emits a fixed set of events when the state is not terminal.
type testCommand struct {
	events   []Envelope
	reject   bool
	terminal bool
}

func (c testCommand) Execute(state State) ([]Envelope, error) {
	if c.reject {
		return nil, errors.New("command rejected")
	}
	if c.terminal {
		return nil, nil
	}
	return c.events, nil
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	openSpec := TypeSpec{
		Type: "goal.started",
		Decode: func(raw json.RawMessage) (Payload, error) {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			return m, nil
		},
	}
	pauseSpec := TypeSpec{
		Type: "goal.paused",
		Decode: func(raw json.RawMessage) (Payload, error) {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			return m, nil
		},
		Validate: func(p Payload) error {
			m, ok := p.(map[string]any)
			if !ok {
				return errors.New("not a map")
			}
			if m["reason"] == "" {
				return errors.New("reason required")
			}
			return nil
		},
	}
	if err := r.Register(openSpec); err != nil {
		t.Fatalf("register open: %v", err)
	}
	if err := r.Register(pauseSpec); err != nil {
		t.Fatalf("register pause: %v", err)
	}
	return r
}

func newTestLoader(t *testing.T, interval int) (*JSONLStore, *Loader) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONLStore(dir, "goals")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	l := &Loader{Store: store, Registry: newTestRegistry(t)}
	if interval > 0 {
		l.SnapshotInterval = interval
	}
	return store, l
}

func TestLoadEmptyAggregate(t *testing.T) {
	_, l := newTestLoader(t, 0)
	st := &testState{}
	next, err := l.Load(context.Background(), "g1", st)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if next != 1 {
		t.Fatalf("nextSeq = %d, want 1", next)
	}
	if st.status != "" || st.seq != 0 {
		t.Fatalf("state must stay default: %+v", st)
	}
}

func TestLoadReplaysEvents(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	if _, _, err := store.Append(ctx, "g1", []Envelope{
		env(0, "goal.started", `{}`),
		env(0, "goal.paused", `{"reason":"no_progress"}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	st := &testState{}
	next, err := l.Load(ctx, "g1", st)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if next != 3 {
		t.Fatalf("nextSeq = %d, want 3", next)
	}
	if st.status != "paused:no_progress" || st.seq != 2 {
		t.Fatalf("state mismatch: %+v", st)
	}
}

func TestLoadUsesSnapshot(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	if _, _, err := store.Append(ctx, "g1", []Envelope{
		env(0, "goal.started", `{}`),
		env(0, "goal.paused", `{"reason":"no_progress"}`),
		env(0, "goal.started", `{}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// 快照覆盖到 seq 2：status=paused:no_progress
	snapState, _ := (&testState{status: "paused:no_progress", seq: 2}).Snapshot()
	if err := store.WriteSnapshot(ctx, "g1", 2, snapState); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	st := &testState{}
	next, err := l.Load(ctx, "g1", st)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if next != 4 {
		t.Fatalf("nextSeq = %d, want 4", next)
	}
	// 快照恢复后只重放 seq 3（goal.started）
	if st.status != "goal.started" || st.seq != 3 {
		t.Fatalf("state mismatch: %+v", st)
	}
}

func TestLoadRejectsUnknownType(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	if _, _, err := store.Append(ctx, "g1", []Envelope{env(0, "goal.unknown", `{}`)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := l.Load(ctx, "g1", &testState{}); err == nil {
		t.Fatal("unknown type must be rejected at load")
	}
}

func TestLoadRejectsInvalidPayload(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	if _, _, err := store.Append(ctx, "g1", []Envelope{env(0, "goal.paused", `{"reason":""}`)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := l.Load(ctx, "g1", &testState{}); err == nil {
		t.Fatal("invalid payload must be rejected at load")
	}
}

func TestCommitAppendsAndApplies(t *testing.T) {
	_, l := newTestLoader(t, 0)
	ctx := context.Background()
	st := &testState{}
	cmd := testCommand{events: []Envelope{
		env(0, "goal.started", `{}`),
		env(0, "goal.paused", `{"reason":"blocked"}`),
	}}
	events, err := l.Commit(ctx, "g1", st, cmd)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("committed events: %+v", events)
	}
	if st.status != "paused:blocked" || st.seq != 2 {
		t.Fatalf("state mismatch: %+v", st)
	}
	// 持久化验证：重新加载
	st2 := &testState{}
	if _, err := l.Load(ctx, "g1", st2); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if st2.status != "paused:blocked" || st2.seq != 2 {
		t.Fatalf("reloaded state mismatch: %+v", st2)
	}
}

func TestCommitRejectsCommand(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	cmd := testCommand{reject: true, events: []Envelope{env(0, "goal.started", `{}`)}}
	if _, err := l.Commit(ctx, "g1", &testState{}, cmd); err == nil {
		t.Fatal("rejected command must fail")
	}
	got, _, err := store.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rejected command must not append: %d events", len(got))
	}
}

func TestCommitNoEvents(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	st := &testState{}
	cmd := testCommand{terminal: true}
	events, err := l.Commit(ctx, "g1", st, cmd)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("terminal command must produce no events: %+v", events)
	}
	got, _, err := store.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("no events must be appended: %d", len(got))
	}
}

func TestCommitTriggersSnapshot(t *testing.T) {
	store, l := newTestLoader(t, 2)
	ctx := context.Background()
	// 每次 commit 产出 1 个事件；interval=2 → seq 2、4 处写快照
	for i := 0; i < 4; i++ {
		cmd := testCommand{events: []Envelope{env(0, "goal.started", `{}`)}}
		if _, err := l.Commit(ctx, "g1", &testState{}, cmd); err != nil {
			t.Fatalf("commit %d: %v", i+1, err)
		}
	}
	snap, ok, err := store.ReadSnapshot(ctx, "g1")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !ok || snap.Seq != 4 {
		t.Fatalf("snapshot = %+v ok=%v, want seq 4", snap, ok)
	}
}

func TestSnapshotReplayEquivalence(t *testing.T) {
	store, l := newTestLoader(t, 3)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		typ := "goal.started"
		payload := `{}`
		if i == 2 || i == 5 {
			typ = "goal.paused"
			payload = `{"reason":"no_progress"}`
		}
		if _, err := l.Commit(ctx, "g1", &testState{}, testCommand{events: []Envelope{env(0, typ, payload)}}); err != nil {
			t.Fatalf("commit %d: %v", i+1, err)
		}
	}
	// 带快照加载（seq 3 处有快照）
	stSnap := &testState{}
	if _, err := l.Load(ctx, "g1", stSnap); err != nil {
		t.Fatalf("load with snapshot: %v", err)
	}
	// 全重放（无快照）
	dir2 := t.TempDir()
	store2, err := NewJSONLStore(dir2, "goals")
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	// 复制事件流（不含快照）
	raw, err := store.StreamPath("g1")
	if err != nil {
		t.Fatalf("stream path: %v", err)
	}
	data, err := readFileForTest(raw)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	stream2, err := store2.StreamPath("g1")
	if err != nil {
		t.Fatalf("stream path 2: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(stream2), 0o700); err != nil {
		t.Fatalf("mkdir stream 2: %v", err)
	}
	if err := writeFileForTest(stream2, data); err != nil {
		t.Fatalf("copy stream: %v", err)
	}
	l2 := &Loader{Store: store2, Registry: newTestRegistry(t), SnapshotInterval: 3}
	stFull := &testState{}
	if _, err := l2.Load(ctx, "g1", stFull); err != nil {
		t.Fatalf("full replay: %v", err)
	}
	if stSnap.status != stFull.status || stSnap.seq != stFull.seq {
		t.Fatalf("snapshot replay != full replay: %+v vs %+v", stSnap, stFull)
	}
}

func TestCommitUsesCurrentStreamTail(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	// 先直接 append 两条，再 commit，seq 必须从 3 继续
	if _, _, err := store.Append(ctx, "g1", []Envelope{env(0, "goal.started", `{}`), env(0, "goal.paused", `{"reason":"x"}`)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	st := &testState{}
	events, err := l.Commit(ctx, "g1", st, testCommand{events: []Envelope{env(0, "goal.started", `{}`)}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 3 {
		t.Fatalf("committed seq = %+v, want 3", events)
	}
	if st.seq != 3 {
		t.Fatalf("state seq = %d, want 3", st.seq)
	}
}

func TestLoaderIntervalZeroDefaults(t *testing.T) {
	l := &Loader{Store: nil}
	if l.interval() != DefaultSnapshotInterval {
		t.Fatalf("default interval = %d, want %d", l.interval(), DefaultSnapshotInterval)
	}
}

func TestLoaderSnapshotTiming(t *testing.T) {
	_, l := newTestLoader(t, 0)
	l.SnapshotInterval = 5
	if !l.shouldSnapshot(5) || !l.shouldSnapshot(10) || l.shouldSnapshot(4) || l.shouldSnapshot(0) {
		t.Fatalf("snapshot timing wrong: 5=%v 10=%v 4=%v 0=%v",
			l.shouldSnapshot(5), l.shouldSnapshot(10), l.shouldSnapshot(4), l.shouldSnapshot(0))
	}
}

func TestCommitRejectsOversizeFromCommand(t *testing.T) {
	store, l := newTestLoader(t, 0)
	ctx := context.Background()
	big := make([]byte, MaxPayloadBytes+1)
	cmd := testCommand{events: []Envelope{env(0, "goal.started", string(big))}}
	if _, err := l.Commit(ctx, "g1", &testState{}, cmd); err == nil {
		t.Fatal("oversize event from command must be rejected")
	}
	if _, _, err := store.Load(ctx, "g1"); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestApplyFailureStopsCommit(t *testing.T) {
	_, l := newTestLoader(t, 0)
	ctx := context.Background()
	st := &failingState{limit: 1}
	cmd := testCommand{events: []Envelope{env(0, "goal.started", `{}`), env(0, "goal.paused", `{"reason":"x"}`)}}
	if _, err := l.Commit(ctx, "g1", st, cmd); err == nil {
		t.Fatal("apply failure must fail commit")
	}
	// 事件已持久化（追加发生在 apply 前），但状态停在失败点；下次加载可重放
	st2 := &failingState{limit: 99}
	if _, err := l.Load(ctx, "g1", st2); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if st2.seq != 2 {
		t.Fatalf("reloaded seq = %d, want 2", st2.seq)
	}
}

type failingState struct {
	seq   int64
	limit int
}

func (s *failingState) Apply(payload Payload, env Envelope) error {
	if env.Seq > int64(s.limit) {
		return errors.New("apply failed")
	}
	s.seq = env.Seq
	return nil
}
func (s *failingState) Snapshot() (json.RawMessage, error) { return json.Marshal(s) }
func (s *failingState) Restore(data json.RawMessage) error { return json.Unmarshal(data, s) }

// 测试辅助：避免依赖 os 包重复导入的样板
func readFileForTest(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeFileForTest(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
