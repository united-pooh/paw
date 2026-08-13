package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestPlanEventStore(t *testing.T) *EventStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewEventStore(filepath.Join(dir, "plans"), filepath.Join(dir, "events"))
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	return s
}

func testPlanDoc(id PlanID) PlanDoc {
	return PlanDoc{
		ID:        id,
		Title:     "DDD plan",
		Path:      "",
		Content:   "# DDD plan\n\nbody",
		Status:    PlanDraft,
		CreatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
	}
}

func TestPlanEventStoreCreateGet(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-ddd-plan-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("plan must exist")
	}
	if got.Title != doc.Title || got.Content != doc.Content || got.Status != PlanDraft {
		t.Fatalf("reconstructed doc mismatch: %+v", got)
	}
	if got.Path == "" || !strings.HasSuffix(got.Path, string(doc.ID)+".md") {
		t.Fatalf("path not projected: %q", got.Path)
	}
	// 投影文件必须真实存在且可读
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("projected file missing: %v", err)
	}
	if !strings.Contains(string(data), doc.Content) {
		t.Fatalf("projected file content mismatch")
	}
}

func TestPlanEventStoreCreateDuplicate(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, testPlanDoc("p1")); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if err := s.Create(ctx, testPlanDoc("p1")); err == nil {
		t.Fatal("duplicate create must fail")
	}
}

func TestPlanEventStoreGetMissing(t *testing.T) {
	s := newTestPlanEventStore(t)
	_, ok, err := s.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatal("missing plan must report not found")
	}
}

func TestPlanEventStoreUpdateContent(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	doc.Content = "# DDD plan\n\nupdated body"
	doc.UpdatedAt = time.Now()
	if err := s.Update(ctx, doc); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ := s.Get(ctx, doc.ID)
	if got.Content != doc.Content {
		t.Fatalf("content mismatch after update: %q", got.Content)
	}
	// 投影文件同步更新
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("projected file missing: %v", err)
	}
	if !strings.Contains(string(data), "updated body") {
		t.Fatalf("projected file not updated")
	}
}

func TestPlanEventStoreUpdateNoop(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Update(ctx, doc); err != nil {
		t.Fatalf("noop update: %v", err)
	}
	// 事件流只有 created
	envs, _, err := s.events.Load(ctx, "p1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("noop update must not append events: %d", len(envs))
	}
}

func TestPlanEventStoreMarkApproved(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.MarkApproved(ctx, doc.ID)
	if err != nil {
		t.Fatalf("mark approved: %v", err)
	}
	if got.Status != PlanApproved {
		t.Fatalf("status = %s, want approved", got.Status)
	}
	// 重建后仍 approved
	back, _, _ := s.Get(ctx, doc.ID)
	if back.Status != PlanApproved {
		t.Fatalf("reconstructed status = %s, want approved", back.Status)
	}
	// 投影文件 front matter 更新
	data, err := os.ReadFile(back.Path)
	if err != nil {
		t.Fatalf("projected file: %v", err)
	}
	if !strings.Contains(string(data), "status=approved") {
		t.Fatalf("front matter not updated: %s", data)
	}
}

func TestPlanEventStoreRecordSessionStatus(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RecordSessionStatus(ctx, doc.ID, SessionAwaitingApprov, ""); err != nil {
		t.Fatalf("record status: %v", err)
	}
	if err := s.RecordSessionStatus(ctx, doc.ID, SessionPaused, PauseNoProgress); err != nil {
		t.Fatalf("record paused: %v", err)
	}
	// 聚合重建可恢复会话状态
	st := &esState{}
	if _, err := s.loader.Load(ctx, "p1", st); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.SessionStatus != SessionPaused || st.PauseReason != PauseNoProgress {
		t.Fatalf("session status mismatch: %+v", st)
	}
}

func TestPlanEventStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewEventStore(filepath.Join(dir, "plans"), filepath.Join(dir, "events"))
	if err != nil {
		t.Fatalf("store 1: %v", err)
	}
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s1.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	doc.Content = "# updated"
	doc.UpdatedAt = time.Now()
	if err := s1.Update(ctx, doc); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := s1.MarkApproved(ctx, doc.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	s2, err := NewEventStore(filepath.Join(dir, "plans"), filepath.Join(dir, "events"))
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	back, ok, err := s2.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("get after restart: ok=%v err=%v", ok, err)
	}
	if back.Status != PlanApproved || back.Content != "# updated" {
		t.Fatalf("reconstructed after restart: %+v", back)
	}
}

func TestPlanEventStoreBaselineImport(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-01-legacy-plan-plan")
	if err := s.ImportBaseline(ctx, doc); err != nil {
		t.Fatalf("import baseline: %v", err)
	}
	got, ok, err := s.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("get after baseline: ok=%v err=%v", ok, err)
	}
	if got.Title != doc.Title || got.Content != doc.Content || got.CreatedAt != doc.CreatedAt {
		t.Fatalf("baseline reconstruction mismatch: %+v", got)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatalf("baseline must project file: %v", err)
	}
}

func TestPlanEventStoreUpdateImmutableRejected(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("p1")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	doc.ID = "p2"
	if err := s.Update(ctx, doc); err == nil {
		t.Fatal("mutating id must be rejected")
	}
}

func TestPlanEventStoreList(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	for _, id := range []PlanID{"p1", "p2"} {
		if err := s.Create(ctx, testPlanDoc(id)); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %d plans, want 2", len(list))
	}
}

func TestPlanEventStoreInterfaceCompatibility(t *testing.T) {
	var _ DocStore = (*FileStore)(nil)
	var _ DocStore = (*EventStore)(nil)
}
