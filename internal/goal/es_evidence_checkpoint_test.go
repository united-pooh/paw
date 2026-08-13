package goal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/es"
	"paw/internal/message"
	"paw/internal/todo"
)

func newTestEventStoreDir(t *testing.T, dir string) *EventStore {
	t.Helper()
	s, err := NewEventStore(dir)
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	return s
}

func testEvidence(id string, gid GoalID) Evidence {
	return Evidence{
		ID:        id,
		GoalID:    gid,
		StepID:    "step-1",
		Kind:      EvidenceTestPassed,
		Command:   "go test ./...",
		Status:    EvidencePassed,
		Summary:   "all tests pass",
		Scope:     []string{"internal/foo/bar.go"},
		Digest:    "abc123",
		CreatedAt: time.Date(2026, 8, 13, 12, 30, 0, 0, time.UTC),
	}
}

func testCheckpoint(id string, gid GoalID) GoalCheckpoint {
	return GoalCheckpoint{
		ID:               id,
		GoalID:           gid,
		SessionID:        "s1",
		Status:           GoalRunning,
		Objective:        "build the thing",
		TodoSnapshot:     todo.Snapshot{Explanation: "todo", UpdatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)},
		EvidenceIDs:      []string{"ev1"},
		ContinuationUsed: 2,
		NoProgressCount:  1,
		LastDecision:     "keep going",
		ProgressHash:     "hash1",
		NextInput:        message.Message{Role: message.RoleUser, Content: "continue"},
		CreatedAt:        time.Date(2026, 8, 13, 12, 31, 0, 0, time.UTC),
	}
}

func TestEventStoreAddEvidenceListPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	// 重启：同目录新 store，evidence 必须从事件流重建。
	s2 := newTestEventStoreDir(t, dir)
	got, err := s2.ListEvidenceByGoal(ctx, "g1")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(got))
	}
	e := got[0]
	if e.ID != "ev1" || e.GoalID != "g1" || e.Kind != EvidenceTestPassed || e.Status != EvidencePassed {
		t.Fatalf("reconstructed evidence mismatch: %+v", e)
	}
	if e.Summary != "all tests pass" || e.Command != "go test ./..." || e.Digest != "abc123" {
		t.Fatalf("evidence fields lost: %+v", e)
	}
	if len(e.Scope) != 1 || e.Scope[0] != "internal/foo/bar.go" {
		t.Fatalf("evidence scope lost: %+v", e.Scope)
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("evidence created_at lost")
	}
}

func TestEventStoreAddEvidenceRequiresGoal(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.AddEvidence(ctx, testEvidence("ev1", "nope")); err == nil {
		t.Fatal("add evidence for unknown goal must fail")
	}
}

func TestEventStoreAddEvidenceDuplicateRejected(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err != nil {
		t.Fatalf("add evidence 1: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err == nil {
		t.Fatal("duplicate evidence id must fail")
	}
}

func TestEventStoreGetEvidenceFindsById(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	s2 := newTestEventStoreDir(t, dir)
	e, ok, err := s2.GetEvidence(ctx, "ev1")
	if err != nil {
		t.Fatalf("get evidence: %v", err)
	}
	if !ok || e.ID != "ev1" {
		t.Fatalf("get evidence: ok=%v e=%+v", ok, e)
	}
	if _, ok, err := s2.GetEvidence(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing evidence: ok=%v err=%v", ok, err)
	}
}

func TestEventStoreMarkStaleByChangedFiles(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	hit := testEvidence("ev1", "g1")
	hit.Scope = []string{"internal/foo/bar.go", "internal/foo/baz.go"}
	miss := testEvidence("ev2", "g1")
	miss.Scope = []string{"internal/other/x.go"}
	if err := s.AddEvidence(ctx, hit); err != nil {
		t.Fatalf("add evidence 1: %v", err)
	}
	if err := s.AddEvidence(ctx, miss); err != nil {
		t.Fatalf("add evidence 2: %v", err)
	}
	if err := s.MarkEvidenceStaleByChangedFiles(ctx, []string{"internal/foo/bar.go"}); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	got, err := s.ListEvidenceByGoal(ctx, "g1")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	byID := map[string]Evidence{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if !byID["ev1"].Stale || byID["ev1"].Status != EvidenceStale {
		t.Fatalf("ev1 must be stale: %+v", byID["ev1"])
	}
	if byID["ev2"].Stale || byID["ev2"].Status != EvidencePassed {
		t.Fatalf("ev2 must stay fresh: %+v", byID["ev2"])
	}
	// 重启后 stale 标记必须保留（事件化声明式重放）。
	s2 := newTestEventStoreDir(t, dir)
	got2, err := s2.ListEvidenceByGoal(ctx, "g1")
	if err != nil {
		t.Fatalf("list evidence after restart: %v", err)
	}
	byID2 := map[string]Evidence{}
	for _, e := range got2 {
		byID2[e.ID] = e
	}
	if !byID2["ev1"].Stale || byID2["ev1"].Status != EvidenceStale {
		t.Fatalf("ev1 must stay stale after restart: %+v", byID2["ev1"])
	}
}

func TestEventStoreMarkStaleAcrossGoals(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	for _, id := range []GoalID{"g1", "g2"} {
		if err := s.Create(ctx, testGoal(id, GoalRunning)); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	hit1 := testEvidence("ev1", "g1")
	hit1.Scope = []string{"shared/file.go"}
	hit2 := testEvidence("ev2", "g2")
	hit2.Scope = []string{"shared/file.go"}
	if err := s.AddEvidence(ctx, hit1); err != nil {
		t.Fatalf("add ev1: %v", err)
	}
	if err := s.AddEvidence(ctx, hit2); err != nil {
		t.Fatalf("add ev2: %v", err)
	}
	if err := s.MarkEvidenceStaleByChangedFiles(ctx, []string{"shared/file.go"}); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	for _, gid := range []GoalID{"g1", "g2"} {
		got, err := s.ListEvidenceByGoal(ctx, gid)
		if err != nil {
			t.Fatalf("list %s: %v", gid, err)
		}
		if len(got) != 1 || !got[0].Stale {
			t.Fatalf("evidence in %s must be stale: %+v", gid, got)
		}
	}
}

func TestEventStoreMarkStaleEmptyNoop(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.MarkEvidenceStaleByChangedFiles(ctx, nil); err != nil {
		t.Fatalf("empty mark stale: %v", err)
	}
}

func TestEventStoreSaveCheckpointLatestPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	first := testCheckpoint("cp1", "g1")
	second := testCheckpoint("cp2", "g1")
	second.CreatedAt = first.CreatedAt.Add(time.Minute)
	second.ContinuationUsed = 5
	if err := s.SaveCheckpoint(ctx, first); err != nil {
		t.Fatalf("save checkpoint 1: %v", err)
	}
	if err := s.SaveCheckpoint(ctx, second); err != nil {
		t.Fatalf("save checkpoint 2: %v", err)
	}
	s2 := newTestEventStoreDir(t, dir)
	cp, ok, err := s2.LatestCheckpoint(ctx, "g1")
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}
	if !ok {
		t.Fatal("checkpoint must exist after restart")
	}
	if cp.ID != "cp2" || cp.ContinuationUsed != 5 {
		t.Fatalf("latest checkpoint mismatch: %+v", cp)
	}
	if cp.Objective != "build the thing" || cp.LastDecision != "keep going" || cp.ProgressHash != "hash1" {
		t.Fatalf("checkpoint fields lost: %+v", cp)
	}
	if len(cp.EvidenceIDs) != 1 || cp.EvidenceIDs[0] != "ev1" {
		t.Fatalf("checkpoint evidence ids lost: %+v", cp.EvidenceIDs)
	}
	if cp.TodoSnapshot.Explanation != "todo" || cp.TodoSnapshot.UpdatedAt.IsZero() {
		t.Fatalf("checkpoint todo snapshot lost: %+v", cp.TodoSnapshot)
	}
	if cp.NextInput.Content != "continue" || cp.NextInput.Role != message.RoleUser {
		t.Fatalf("checkpoint next input lost: %+v", cp.NextInput)
	}
	if cp.SessionID != "s1" || cp.Status != GoalRunning {
		t.Fatalf("checkpoint identity lost: %+v", cp)
	}
	// Load == Latest（与 MemoryCheckpointStore 语义一致）。
	loaded, ok, err := s2.LoadCheckpoint(ctx, "g1")
	if err != nil || !ok || loaded.ID != "cp2" {
		t.Fatalf("load checkpoint: ok=%v err=%v cp=%+v", ok, err, loaded)
	}
}

func TestEventStoreCheckpointDeleteThenSave(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SaveCheckpoint(ctx, testCheckpoint("cp1", "g1")); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if err := s.DeleteCheckpoints(ctx, "g1"); err != nil {
		t.Fatalf("delete checkpoints: %v", err)
	}
	s2 := newTestEventStoreDir(t, dir)
	if _, ok, err := s2.LatestCheckpoint(ctx, "g1"); err != nil || ok {
		t.Fatalf("checkpoint must be gone after delete: ok=%v err=%v", ok, err)
	}
	// 删除后重新保存：新 checkpoint 成为最新（重放语义：deleted 清空后重新累积）。
	reborn := testCheckpoint("cp2", "g1")
	reborn.CreatedAt = time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	if err := s2.SaveCheckpoint(ctx, reborn); err != nil {
		t.Fatalf("save checkpoint after delete: %v", err)
	}
	cp, ok, err := s2.LatestCheckpoint(ctx, "g1")
	if err != nil || !ok || cp.ID != "cp2" {
		t.Fatalf("latest after reborn: ok=%v err=%v cp=%+v", ok, err, cp)
	}
}

func TestEventStoreSaveCheckpointRequiresGoal(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	if err := s.SaveCheckpoint(ctx, testCheckpoint("cp1", "nope")); err == nil {
		t.Fatal("save checkpoint for unknown goal must fail")
	}
}

func TestEventStoreSnapshotRoundTripWithEvidence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	s.loader.SnapshotInterval = 2
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil { // seq 1
		t.Fatalf("create: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err != nil { // seq 2 -> snapshot
		t.Fatalf("add evidence: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev2", "g1")); err != nil { // seq 3
		t.Fatalf("add evidence 2: %v", err)
	}
	if err := s.SaveCheckpoint(ctx, testCheckpoint("cp1", "g1")); err != nil { // seq 4 -> snapshot
		t.Fatalf("save checkpoint: %v", err)
	}
	// 重启后从新格式快照 + 尾部重放，evidence/checkpoint 必须完整。
	s2 := newTestEventStoreDir(t, dir)
	s2.loader.SnapshotInterval = 2
	got, err := s2.ListEvidenceByGoal(ctx, "g1")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(got))
	}
	cp, ok, err := s2.LatestCheckpoint(ctx, "g1")
	if err != nil || !ok {
		t.Fatalf("latest checkpoint: ok=%v err=%v", ok, err)
	}
	if cp.ID != "cp1" {
		t.Fatalf("checkpoint mismatch: %+v", cp)
	}
	// 快照文件存在且是新格式（含 goal 键）。
	snapPath := filepath.Join(s.events.Dir(), "goals", "g1.snapshot.json")
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap es.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	var sp goalSnapshotPayload
	if err := json.Unmarshal(snap.State, &sp); err != nil {
		t.Fatalf("decode snapshot state: %v", err)
	}
	if sp.Goal == nil || len(sp.Evidence) != 2 || len(sp.Checkpoints) != 1 {
		t.Fatalf("snapshot state incomplete: goal=%v evidence=%d checkpoints=%d", sp.Goal != nil, len(sp.Evidence), len(sp.Checkpoints))
	}
}

func TestEventStoreLegacySnapshotFullReplay(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := newTestEventStoreDir(t, dir)
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AddEvidence(ctx, testEvidence("ev1", "g1")); err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	// 模拟旧版本进程留下的快照：裸 Goal JSON（不含 evidence/checkpoints）。
	g, _, err := s.Get(ctx, "g1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rawGoal, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal goal: %v", err)
	}
	snapRaw, err := json.Marshal(es.Snapshot{Seq: 1, State: rawGoal})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	snapPath := filepath.Join(s.events.Dir(), "goals", "g1.snapshot.json")
	if err := os.WriteFile(snapPath, snapRaw, 0o600); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}
	// 重启：loadState 必须检测 legacy 快照、删除缓存并全量重放。
	s2 := newTestEventStoreDir(t, dir)
	got, err := s2.ListEvidenceByGoal(ctx, "g1")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ev1" {
		t.Fatalf("evidence lost after legacy snapshot replay: %+v", got)
	}
	if _, statErr := os.Stat(snapPath); !os.IsNotExist(statErr) {
		t.Fatalf("legacy snapshot must be dropped, stat err = %v", statErr)
	}
	// goal 状态本身也必须完整（全量重放而非残缺快照）。
	if _, ok, err := s2.Get(ctx, "g1"); err != nil || !ok {
		t.Fatalf("goal after replay: ok=%v err=%v", ok, err)
	}
}

func TestEventStoreAdaptersImplementInterfaces(t *testing.T) {
	s := newTestEventStore(t)
	ctx := context.Background()
	var evStore EvidenceStore = s.EvidenceStore()
	var cpStore CheckpointStore = s.CheckpointStore()
	if evStore == nil || cpStore == nil {
		t.Fatal("adapters must be non-nil")
	}
	if err := s.Create(ctx, testGoal("g1", GoalRunning)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := evStore.Add(ctx, testEvidence("ev1", "g1")); err != nil {
		t.Fatalf("adapter add evidence: %v", err)
	}
	if err := cpStore.Save(ctx, testCheckpoint("cp1", "g1")); err != nil {
		t.Fatalf("adapter save checkpoint: %v", err)
	}
	if err := evStore.MarkStaleByChangedFiles(ctx, []string{"internal/foo/bar.go"}); err != nil {
		t.Fatalf("adapter mark stale: %v", err)
	}
	if err := cpStore.Delete(ctx, "g1"); err != nil {
		t.Fatalf("adapter delete checkpoints: %v", err)
	}
	e, ok, err := evStore.Get(ctx, "ev1")
	if err != nil || !ok {
		t.Fatalf("adapter get evidence: ok=%v err=%v", ok, err)
	}
	if !e.Stale || e.Status != EvidenceStale {
		t.Fatalf("adapter stale evidence mismatch: %+v", e)
	}
}
