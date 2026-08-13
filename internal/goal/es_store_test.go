package goal

import (
	"context"
	"testing"
	"time"
)

func newTestEventStore(t *testing.T) *EventStore {
	t.Helper()
	s, err := NewEventStore(t.TempDir())
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	return s
}

func testGoal(id GoalID, status GoalStatus) Goal {
	return Goal{
		ID:        id,
		SessionID: "s1",
		Objective: "build the thing",
		Budget:    GoalBudget{MaxTurns: 5, MaxToolCalls: 10, MaxContinuations: 2, MaxNoProgress: 3},
		Status:    status,
		CreatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		Revision:  1,
	}
}

func TestEventStoreCreateGet(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	g := testGoal("g1", GoalRunning)
	if err := s.Create(ctx, g); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := s.Get(ctx, "g1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("goal must exist")
	}
	if got.Status != GoalRunning || got.Objective != "build the thing" || got.SessionID != "s1" {
		t.Fatalf("reconstructed goal mismatch: %+v", got)
	}
	if got.Revision != 1 {
		t.Fatalf("revision = %d, want 1", got.Revision)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps lost: %+v", got)
	}
}

func TestEventStoreCreateDuplicate(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err == nil {
		t.Fatal("duplicate create must fail")
	}
}

func TestEventStoreGetMissing(t *testing.T) {
	s := newTestEventStore(t)
	_, ok, err := s.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatal("missing goal must report not found")
	}
}

func TestEventStoreUpdateStatusTransition(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, err := s.Get(ctx, "g1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// running → paused，携带 reason
	got.Status = GoalPaused
	got.PauseReason = PauseNoProgress
	got.UpdatedAt = time.Now()
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	// paused → running（resume）
	got2, _, _ := s.Get(ctx, "g1")
	if got2.Status != GoalPaused || got2.PauseReason != PauseNoProgress {
		t.Fatalf("after pause: %+v", got2)
	}
	got2.Status = GoalRunning
	got2.PauseReason = ""
	got2.UpdatedAt = time.Now()
	if err := s.Update(ctx, got2); err != nil {
		t.Fatalf("resume update: %v", err)
	}
	got3, _, _ := s.Get(ctx, "g1")
	if got3.Status != GoalRunning || got3.PauseReason != "" {
		t.Fatalf("after resume: %+v", got3)
	}
}

func TestEventStoreUpdateStats(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := s.Get(ctx, "g1")
	got.TurnsUsed = 1
	got.ToolCallsUsed = 3
	got.NoProgressCount = 2
	got.ContinuationUsed = 1
	got.CurrentTaskID = "task-1"
	got.LastDecision = "continue"
	got.UpdatedAt = time.Now()
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("update stats: %v", err)
	}
	back, _, _ := s.Get(ctx, "g1")
	if back.TurnsUsed != 1 || back.ToolCallsUsed != 3 || back.NoProgressCount != 2 || back.ContinuationUsed != 1 {
		t.Fatalf("stats mismatch: %+v", back)
	}
	if back.CurrentTaskID != "task-1" || back.LastDecision != "continue" {
		t.Fatalf("task/decision mismatch: %+v", back)
	}
}

func TestEventStoreUpdateImmutableFieldsRejected(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := s.Get(ctx, "g1")
	got.Objective = "mutated"
	if err := s.Update(ctx, got); err == nil {
		t.Fatal("mutating immutable fields must be rejected")
	}
}

func TestEventStoreUpdateRevisionConflict(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := s.Get(ctx, "g1")
	got.Revision = 99
	if err := s.Update(ctx, got); err == nil {
		t.Fatal("stale revision must conflict")
	}
	got2, _, _ := s.Get(ctx, "g1")
	got2.Revision = 0
	if err := s.Update(ctx, got2); err == nil {
		t.Fatal("zero revision must conflict")
	}
}

func TestEventStoreUpdateNoop(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := s.Get(ctx, "g1")
	if err := s.Update(ctx, got); err != nil {
		t.Fatalf("noop update: %v", err)
	}
	// 无变化不追加事件：重建后 revision 不变
	back, _, _ := s.Get(ctx, "g1")
	if back.Revision != 1 {
		t.Fatalf("noop update must not append: revision = %d", back.Revision)
	}
}

func TestEventStoreDelete(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Delete(ctx, "g1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := s.Get(ctx, "g1"); err != nil || ok {
		t.Fatalf("deleted goal must be gone: ok=%v err=%v", ok, err)
	}
}

func TestEventStoreListFiltersSessionAndDeleted(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	g1 := testGoal("g1", GoalRunning)
	g2 := testGoal("g2", GoalPaused)
	g2.SessionID = "s2"
	if err := s.Create(ctx, g1); err != nil {
		t.Fatalf("create g1: %v", err)
	}
	if err := s.Create(ctx, g2); err != nil {
		t.Fatalf("create g2: %v", err)
	}
	g3 := testGoal("g3", GoalRunning)
	if err := s.Create(ctx, g3); err != nil {
		t.Fatalf("create g3: %v", err)
	}
	if err := s.Delete(ctx, "g3"); err != nil {
		t.Fatalf("delete g3: %v", err)
	}
	list, err := s.List(ctx, "s1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "g1" {
		t.Fatalf("list(s1) = %+v, want [g1]", list)
	}
}

func TestEventStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store 1: %v", err)
	}
	ctx := context.Background()
	if err := s1.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := s1.Get(ctx, "g1")
	got.Status = GoalCompleted
	got.UpdatedAt = time.Now()
	if err := s1.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	// 新实例（模拟重启）
	s2, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	back, ok, err := s2.Get(ctx, "g1")
	if err != nil || !ok {
		t.Fatalf("get after restart: ok=%v err=%v", ok, err)
	}
	if back.Status != GoalCompleted {
		t.Fatalf("status after restart = %s, want completed", back.Status)
	}
}

func TestEventStoreSnapshotReplayEquivalence(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 多次更新推进状态，触发快照（interval 调小）
	s.loader.SnapshotInterval = 3
	for i := 0; i < 5; i++ {
		got, _, _ := s.Get(ctx, "g1")
		got.TurnsUsed = i + 1
		got.UpdatedAt = time.Now()
		if i == 2 {
			got.Status = GoalPaused
			got.PauseReason = PauseNoProgress
		}
		if err := s.Update(ctx, got); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	// 删除快照后全量重放必须得到相同状态
	snap, ok, err := s.events.ReadSnapshot(ctx, "g1")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !ok {
		t.Fatal("snapshot must exist after interval")
	}
	if snap.Seq < 1 {
		t.Fatalf("snapshot seq = %d", snap.Seq)
	}
	// 直接读事件流全量重建
	envs, _, err := s.events.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	rebuilt := &Goal{}
	for _, env := range envs {
		payload, err := s.registry.Decode(env)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if err := rebuilt.Apply(payload, env); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	got, _, _ := s.Get(ctx, "g1")
	if got.Status != rebuilt.Status || got.TurnsUsed != rebuilt.TurnsUsed || got.NoProgressCount != rebuilt.NoProgressCount {
		t.Fatalf("snapshot replay != full replay: %+v vs %+v", got, rebuilt)
	}
}
