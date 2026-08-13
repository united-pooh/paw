package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/es"
)

// TestMigrationDryRunFileToEventsToFile 是迁移 dry-run：旧 FileStore 的文档
// → 基线导入事件流 → 重建 → 与旧文档逐字节一致，且后续 Update 正常工作。
func TestMigrationDryRunFileToEventsToFile(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old-plans")
	eventsDir := filepath.Join(root, "events")
	ctx := context.Background()

	// 1. 旧系统：FileStore 写入一个真实文档（front matter + content）
	oldStore := NewFileStore(oldDir)
	id, err := oldStore.NextID(ctx, "Migrate the legacy system")
	if err != nil {
		t.Fatalf("next id: %v", err)
	}
	legacyDoc := PlanDoc{
		ID:        id,
		Title:     "Migrate the legacy system",
		Content:   "# 迁移计划\n\n- 步骤 1\n- 步骤 2\n\n```go\nfmt.Println(\"ok\")\n```\n",
		Status:    PlanDraft,
		CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
	}
	if err := oldStore.Create(ctx, legacyDoc); err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	legacyFile, err := os.ReadFile(filepath.Join(oldDir, string(id)+".md"))
	if err != nil {
		t.Fatalf("read legacy file: %v", err)
	}

	// 2. 迁移：读旧文档 → 基线导入新事件库
	oldDoc, ok, err := oldStore.Get(ctx, id)
	if err != nil || !ok {
		t.Fatalf("legacy get: ok=%v err=%v", ok, err)
	}
	newStore, err := NewEventStore(oldDir, eventsDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := newStore.ImportBaseline(ctx, oldDoc); err != nil {
		t.Fatalf("import baseline: %v", err)
	}

	// 3. 重建：事件流 → 文档，内容与旧文档逐字节一致
	rebuilt, ok, err := newStore.Get(ctx, id)
	if err != nil || !ok {
		t.Fatalf("rebuilt get: ok=%v err=%v", ok, err)
	}
	if rebuilt.ID != oldDoc.ID || rebuilt.Title != oldDoc.Title || rebuilt.Status != oldDoc.Status {
		t.Fatalf("rebuilt header mismatch: %+v vs %+v", rebuilt, oldDoc)
	}
	if rebuilt.Content != oldDoc.Content {
		t.Fatalf("rebuilt content mismatch:\n%q\nvs\n%q", rebuilt.Content, oldDoc.Content)
	}
	projected, err := os.ReadFile(filepath.Join(oldDir, string(id)+".md"))
	if err != nil {
		t.Fatalf("read projected: %v", err)
	}
	if string(projected) != string(legacyFile) {
		t.Fatalf("projected file differs from legacy file:\n%s\nvs\n%s", projected, legacyFile)
	}

	// 4. 迁移后 Update 继续工作：内容变更 → 事件 + 投影同步
	rebuilt.Content = "# 迁移计划\n\n- 步骤 1（更新）\n"
	rebuilt.UpdatedAt = time.Now()
	if err := newStore.Update(ctx, rebuilt); err != nil {
		t.Fatalf("update after migration: %v", err)
	}
	final, _, _ := newStore.Get(ctx, id)
	if final.Content != rebuilt.Content {
		t.Fatalf("updated content mismatch: %q", final.Content)
	}
	projected2, err := os.ReadFile(filepath.Join(oldDir, string(id)+".md"))
	if err != nil {
		t.Fatalf("read projected 2: %v", err)
	}
	if !strings.Contains(string(projected2), "步骤 1（更新）") {
		t.Fatalf("projected file not updated after migration")
	}

	// 5. 事件流包含 baseline + doc_updated
	envs, _, err := newStore.events.Load(ctx, string(id))
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	if len(envs) != 2 || envs[0].Type != EventBaseline || envs[1].Type != EventDocUpdated {
		t.Fatalf("stream = %+v, want [baseline, doc_updated]", envTypes(envs))
	}
}

func envTypes(envs []es.Envelope) []string {
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		out = append(out, e.Type)
	}
	return out
}

// TestMigrationSkipsAlreadyImported 幂等性：已导入的 id 再次导入报错。
func TestMigrationSkipsAlreadyImported(t *testing.T) {
	root := t.TempDir()
	oldStore := NewFileStore(filepath.Join(root, "plans"))
	ctx := context.Background()
	id, err := oldStore.NextID(ctx, "Once only")
	if err != nil {
		t.Fatalf("next id: %v", err)
	}
	doc := PlanDoc{ID: id, Title: "Once only", Content: "# Once\n", Status: PlanDraft, CreatedAt: time.Now()}
	if err := oldStore.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	newStore, err := NewEventStore(filepath.Join(root, "plans"), filepath.Join(root, "events"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := newStore.ImportBaseline(ctx, doc); err != nil {
		t.Fatalf("import 1: %v", err)
	}
	if err := newStore.ImportBaseline(ctx, doc); err == nil {
		t.Fatal("duplicate baseline import must fail")
	}
}
