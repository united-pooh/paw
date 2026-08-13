package goal

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCrashRecoveryTornTail 模拟进程在追加最后一个事件时崩溃（尾部行被截断）：
// 从事件流重建时必须截断到完好前缀，状态与崩溃前一致。
func TestCrashRecoveryTornTail(t *testing.T) {
	dir := t.TempDir()
	store, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := store.Get(ctx, "g1")
	got.Status = GoalPaused
	got.PauseReason = PauseNoProgress
	got.TurnsUsed = 3
	got.UpdatedAt = time.Now()
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 模拟崩溃：追加一个被截断的 stats 事件（无换行结尾）
	stream, err := store.events.StreamPath("g1")
	if err != nil {
		t.Fatalf("stream path: %v", err)
	}
	f, err := os.OpenFile(stream, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := f.WriteString(`{"seq":9,"type":"goal.stats.updated","paylo`); err != nil {
		f.Close()
		t.Fatalf("write torn tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 新实例（模拟重启）：必须从完好前缀恢复
	store2, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	recovered, ok, err := store2.Get(ctx, "g1")
	if err != nil || !ok {
		t.Fatalf("recover: ok=%v err=%v", ok, err)
	}
	if recovered.Status != GoalPaused || recovered.PauseReason != PauseNoProgress {
		t.Fatalf("recovered state mismatch: %+v", recovered)
	}
	if recovered.TurnsUsed != 3 {
		t.Fatalf("recovered turns = %d, want 3", recovered.TurnsUsed)
	}
	// 崩溃后继续写入：seq 从完好前缀继续
	recovered.TurnsUsed = 4
	recovered.UpdatedAt = time.Now()
	if err := store2.Update(ctx, recovered); err != nil {
		t.Fatalf("update after recovery: %v", err)
	}
	// 追加的新事件必须可重建（且会覆盖 torn 之前的统计）
	final, _, _ := store2.Get(ctx, "g1")
	if final.TurnsUsed != 4 {
		t.Fatalf("final turns = %d, want 4", final.TurnsUsed)
	}
}

// TestCrashRecoveryMidStreamCorruption 模拟事件流中部损坏（非尾部）：
// 必须报错而不是静默截断。
func TestCrashRecoveryMidStreamCorruption(t *testing.T) {
	dir := t.TempDir()
	store, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := store.Get(ctx, "g1")
	got.Status = GoalPaused
	got.PauseReason = PauseBlocked
	got.UpdatedAt = time.Now()
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	// 在流中部插入一行垃圾（带换行结尾 → 非尾部损坏）
	stream, err := store.events.StreamPath("g1")
	if err != nil {
		t.Fatalf("stream path: %v", err)
	}
	data, err := os.ReadFile(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := splitLines(string(data))
	if len(lines) < 2 {
		t.Fatalf("stream too short: %d lines", len(lines))
	}
	corrupted := lines[0] + "\n" + "garbage line\n" + lines[1] + "\n"
	if len(lines) > 2 {
		corrupted += lines[2] + "\n"
	}
	if err := os.WriteFile(stream, []byte(corrupted), 0o600); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}
	store2, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	if _, _, err := store2.Get(ctx, "g1"); err == nil {
		t.Fatal("mid-stream corruption must fail loudly")
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// TestCrashRecoveryNoSnapshotFullReplay 无快照时全量重放必须得到相同状态
// （等价于带快照路径）。
func TestCrashRecoveryNoSnapshotFullReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 多次更新，但 interval 保持默认（不触发快照）
	for i := 0; i < 5; i++ {
		got, _, _ := store.Get(ctx, "g1")
		got.TurnsUsed = i + 1
		got.UpdatedAt = time.Now()
		if err := store.Update(ctx, got); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	if _, ok, err := store.events.ReadSnapshot(ctx, "g1"); err != nil || ok {
		t.Fatalf("unexpected snapshot: ok=%v err=%v", ok, err)
	}
	// 全量重放（新实例）
	store2, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	back, _, _ := store2.Get(ctx, "g1")
	if back.TurnsUsed != 5 {
		t.Fatalf("replayed turns = %d, want 5", back.TurnsUsed)
	}
}

// TestCrashRecoveryRejectsStaleRevision 崩溃后基于旧 revision 的更新必须冲突。
func TestCrashRecoveryRejectsStaleRevision(t *testing.T) {
	dir := t.TempDir()
	store, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _, _ := store.Get(ctx, "g1")
	got.Status = GoalPaused
	got.PauseReason = PauseNoProgress
	got.UpdatedAt = time.Now()
	if err := store.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	// 旧 revision 的并发更新（模拟崩溃前读、崩溃后写）
	stale := got
	stale.Status = GoalCancelled
	stale.UpdatedAt = time.Now()
	if err := store.Update(ctx, stale); err == nil {
		t.Fatal("stale revision update must conflict")
	}
}
