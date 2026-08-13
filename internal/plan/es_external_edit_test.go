package plan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPlanEventStoreAt(t *testing.T, dir string) *EventStore {
	t.Helper()
	s, err := NewEventStore(filepath.Join(dir, "plans"), filepath.Join(dir, "events"))
	if err != nil {
		t.Fatalf("new event store: %v", err)
	}
	return s
}

// TestPlanEventStoreUpdateAdoptsExternalEdit 覆盖：用户外部编辑投影文件后，
// 调用方基于磁盘最新内容（FileStore.Get 后 Update）时，用户编辑被采纳进
// 事件流，不丢失、不报错。注意 EventStore.Get 读事件流投影（不读磁盘），
// 读取用户编辑必须走 FileStore.Get。
func TestPlanEventStoreUpdateAdoptsExternalEdit(t *testing.T) {
	dir := t.TempDir()
	s := newPlanEventStoreAt(t, dir)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-external-edit-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	projected, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	edited := projected
	edited.Content = "# DDD plan\n\nuser edited body"
	edited.UpdatedAt = time.Now()
	if err := os.WriteFile(projected.Path, []byte(encodeDoc(edited)), 0o644); err != nil {
		t.Fatalf("simulate external edit: %v", err)
	}

	fresh, ok, err := s.FileStore.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("file get after external edit: ok=%v err=%v", ok, err)
	}
	if strings.TrimRight(fresh.Content, "\n") != edited.Content {
		t.Fatalf("file get must return external edit, got %q", fresh.Content)
	}
	if err := s.Update(ctx, fresh); err != nil {
		t.Fatalf("update from fresh doc must adopt external edit: %v", err)
	}

	reopened := newPlanEventStoreAt(t, dir)
	got, ok, err := reopened.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("reopen get: ok=%v err=%v", ok, err)
	}
	if strings.TrimRight(got.Content, "\n") != edited.Content {
		t.Fatalf("external edit lost after replay: got %q want %q", got.Content, edited.Content)
	}
}

// TestPlanEventStoreUpdateRejectsStaleDoc 覆盖：用户外部编辑投影文件后，
// 调用方仍基于旧状态 Update 时返回 ErrExternalEditConflict，且不覆盖
// 用户编辑、不产生事件。
func TestPlanEventStoreUpdateRejectsStaleDoc(t *testing.T) {
	dir := t.TempDir()
	s := newPlanEventStoreAt(t, dir)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-external-edit-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	projected, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	edited := projected
	edited.Content = "# DDD plan\n\nuser edited body"
	edited.UpdatedAt = time.Now()
	if err := os.WriteFile(projected.Path, []byte(encodeDoc(edited)), 0o644); err != nil {
		t.Fatalf("simulate external edit: %v", err)
	}

	// 调用方持有创建时的旧 doc（过期状态）。
	err = s.Update(ctx, doc)
	if !errors.Is(err, ErrExternalEditConflict) {
		t.Fatalf("stale update must return ErrExternalEditConflict, got %v", err)
	}
	data, readErr := os.ReadFile(projected.Path)
	if readErr != nil {
		t.Fatalf("read projected file: %v", readErr)
	}
	if !strings.Contains(string(data), "user edited body") {
		t.Fatal("external edit must be preserved after conflict")
	}

	reopened := newPlanEventStoreAt(t, dir)
	got, ok, err := reopened.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("reopen get: ok=%v err=%v", ok, err)
	}
	if got.Content != doc.Content {
		t.Fatalf("no event must be appended on conflict, got %q", got.Content)
	}
}

// TestPlanEventStoreUpdateUnchangedFile 覆盖：投影文件与事件流一致（无外部
// 编辑）时，正常 diff 更新不受影响。
func TestPlanEventStoreUpdateUnchangedFile(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-normal-update-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	next := doc
	next.Content = "# DDD plan\n\nsecond revision"
	next.UpdatedAt = time.Now()
	if err := s.Update(ctx, next); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != next.Content {
		t.Fatalf("content mismatch: %q", got.Content)
	}
}

// TestPlanEventStoreUpdateProjectionDeleted 覆盖：事件流存在但投影文件被
// 删除时，Update 返回 ErrExternalEditConflict 且不追加事件（避免事件提交
// 后投影写入失败的半提交）。
func TestPlanEventStoreUpdateProjectionDeleted(t *testing.T) {
	dir := t.TempDir()
	s := newPlanEventStoreAt(t, dir)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-deleted-projection-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	projected, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := os.Remove(projected.Path); err != nil {
		t.Fatalf("remove projection: %v", err)
	}

	err = s.Update(ctx, projected)
	if !errors.Is(err, ErrExternalEditConflict) {
		t.Fatalf("update with deleted projection must conflict, got %v", err)
	}

	reopened := newPlanEventStoreAt(t, dir)
	got, ok, err := reopened.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("reopen get: ok=%v err=%v", ok, err)
	}
	if got.Content != doc.Content {
		t.Fatalf("no event must be appended on conflict, got %q", got.Content)
	}
}

// TestPlanEventStoreUpdateTrailingNewlineNoop 覆盖：仅 encodeDoc 往返引入的
// 尾部换行差异不产生 no-op doc_updated 事件（磁盘读取的 Content 带尾 \n，
// 事件流内不带，内容实质相同）。
func TestPlanEventStoreUpdateTrailingNewlineNoop(t *testing.T) {
	dir := t.TempDir()
	s := newPlanEventStoreAt(t, dir)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-trailing-newline-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 磁盘读取路径：decodeDoc 保留 encodeDoc 补的尾 \n。
	fresh, ok, err := s.FileStore.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("file get: ok=%v err=%v", ok, err)
	}
	if err := s.Update(ctx, fresh); err != nil {
		t.Fatalf("noop update must succeed: %v", err)
	}

	stream, err := os.ReadFile(filepath.Join(dir, "events", "plans", string(doc.ID)+".events.jsonl"))
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if n := strings.Count(string(stream), "\n"); n != 1 {
		t.Fatalf("no-op update must not append events, stream has %d lines", n)
	}
}

// TestPlanEventStoreUpdateFromDiskDoc 覆盖：磁盘来源的 doc（CreatedAt 恒为
// 零值，front matter 不含 created_at）修改内容后 Update 成功，CreatedAt
// 继承事件流值而不是触发不可变校验错误。
func TestPlanEventStoreUpdateFromDiskDoc(t *testing.T) {
	s := newTestPlanEventStore(t)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-disk-doc-update-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	fresh, ok, err := s.FileStore.Get(ctx, doc.ID)
	if err != nil || !ok {
		t.Fatalf("file get: ok=%v err=%v", ok, err)
	}
	if !fresh.CreatedAt.IsZero() {
		t.Fatalf("disk doc must have zero created_at, got %v", fresh.CreatedAt)
	}
	fresh.Content = "# DDD plan\n\nupdated from disk"
	if err := s.Update(ctx, fresh); err != nil {
		t.Fatalf("update from disk doc must succeed: %v", err)
	}
	got, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != fresh.Content {
		t.Fatalf("content mismatch: %q", got.Content)
	}
	if !got.CreatedAt.Equal(doc.CreatedAt) {
		t.Fatalf("created_at must be inherited from stream: got %v want %v", got.CreatedAt, doc.CreatedAt)
	}
}

// TestPlanEventStoreUpdateConcurrentProjectionEdit 覆盖：投影写入前的字节
// CAS 检测并发外部编辑（beforeProjectedWrite hook 模拟），返回
// ErrExternalEditConflict、不覆盖用户编辑、不追加事件。
func TestPlanEventStoreUpdateConcurrentProjectionEdit(t *testing.T) {
	dir := t.TempDir()
	s := newPlanEventStoreAt(t, dir)
	ctx := context.Background()
	doc := testPlanDoc("2026-08-13-concurrent-edit-plan")
	if err := s.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	projected, _, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	next := projected
	next.Content = "# DDD plan\n\nupdated content"
	s.beforeProjectedWrite = func() {
		if werr := os.WriteFile(projected.Path, []byte("concurrent user edit"), 0o644); werr != nil {
			t.Errorf("hook write: %v", werr)
		}
	}

	err = s.Update(ctx, next)
	if !errors.Is(err, ErrExternalEditConflict) {
		t.Fatalf("concurrent edit must conflict, got %v", err)
	}
	data, readErr := os.ReadFile(projected.Path)
	if readErr != nil {
		t.Fatalf("read projected file: %v", readErr)
	}
	if string(data) != "concurrent user edit" {
		t.Fatalf("concurrent edit must be preserved, got %q", string(data))
	}
	stream, readErr := os.ReadFile(filepath.Join(dir, "events", "plans", string(doc.ID)+".events.jsonl"))
	if readErr != nil {
		t.Fatalf("read stream: %v", readErr)
	}
	if n := strings.Count(string(stream), "\n"); n != 1 {
		t.Fatalf("conflict must not append events, stream has %d lines", n)
	}
}
