package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/todo"
	"paw/internal/tool"
	"paw/internal/ui/headless"
)

// resumeTestModel 最小 model stub（LoadSession 不触发模型调用）。
type resumeTestModel struct{}

func (m *resumeTestModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	ch := make(chan model.StreamEvent, 1)
	ch <- model.StreamEvent{Delta: "ok"}
	ch <- model.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func TestResumeRebindsStateTools(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "session-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "session-b"}); err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	broker := todo.NewBroker()
	defer broker.Close()
	if err := registerMainAgentTools(registry, broker); err != nil {
		t.Fatal(err)
	}

	runner := loop.NewRunnerWithInstructionRoot(&resumeTestModel{}, headless.New(io.Discard), registry, store, "session-a", root)
	wireSessionTools(runner, store, broker, "session-a")
	runner.SetSessionLoadedHook(func(sid string) {
		wireSessionTools(runner, store, broker, sid)
	})

	runAriadne := func() {
		t.Helper()
		in, _ := json.Marshal(map[string]string{"content": "## 方向\nx\n\n## 进度\nx\n\n## 关键决策\nx\n\n## 下一步\nx\n\n## 教训\nx\n"})
		if out, err := mainAriadneTool.Run(ctx, in); err != nil {
			t.Fatalf("update_ariadne: %v", err)
		} else if out == "" {
			t.Fatal("empty output")
		}
	}

	// 启动绑定 session-a：事件落 A。
	runAriadne()
	recsA, err := store.LoadResolvedJournalRecords(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(recsA) != 1 || recsA[0].Kind != session.JournalAriadneUpdated {
		t.Fatalf("session-a records = %+v, want 1 ariadne_updated", recsA)
	}

	// /resume 切换到 session-b：hook 重绑。
	if _, err := runner.LoadSession(ctx, "session-b"); err != nil {
		t.Fatal(err)
	}
	runAriadne()

	// 事件必须落 B；A 不再新增。
	recsA2, _ := store.LoadResolvedJournalRecords(ctx, "session-a")
	if len(recsA2) != 1 {
		t.Fatalf("session-a got %d records after resume (want 1, no new events)", len(recsA2))
	}
	recsB, err := store.LoadResolvedJournalRecords(ctx, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recsB {
		if r.Kind == session.JournalAriadneUpdated {
			found = true
		}
	}
	if !found {
		t.Fatalf("session-b must contain ariadne_updated after resume, got %+v", recsB)
	}
}

func TestTodoArchiveWritesProgressFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "session-a"}); err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	broker := todo.NewBroker()
	defer broker.Close()
	if err := registerMainAgentTools(registry, broker); err != nil {
		t.Fatal(err)
	}
	runner := loop.NewRunnerWithInstructionRoot(&resumeTestModel{}, headless.New(io.Discard), registry, store, "session-a", root)
	wireSessionTools(runner, store, broker, "session-a")

	snap := `{"explanation":"archive test","items":[{"id":"1","content":"done item","status":"completed"},{"id":"2","content":"pending item","status":"pending"}]}`
	if out, err := mainTodoTool.Run(ctx, json.RawMessage(snap)); err != nil {
		t.Fatalf("update_todo: %v", err)
	} else if !strings.Contains(out, `"accepted":true`) {
		t.Fatalf("unexpected result: %s", out)
	}

	progress := filepath.Join(root, "memory", "progress.md")
	data, err := os.ReadFile(progress)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "- [x] done item <!-- todo:1 -->") {
		t.Fatalf("progress.md missing archived line: %q", got)
	}
	if strings.Contains(got, "pending item") {
		t.Fatalf("progress.md must not contain pending item: %q", got)
	}

	// 同一快照重复提交：幂等，不重复追加。
	if _, err := mainTodoTool.Run(ctx, json.RawMessage(snap)); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(progress)
	if strings.Count(string(data2), "<!-- todo:1 -->") != 1 {
		t.Fatalf("archived twice after re-submit: %q", data2)
	}
}
