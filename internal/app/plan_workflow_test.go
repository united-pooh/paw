package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	toolfile "paw/internal/capability/tool/file"
	selecttool "paw/internal/capability/tool/select"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/plan"
	"paw/internal/runtime/sessionactor"
	"paw/internal/storage/session"
	"paw/internal/ui/headless"
)

type planWorkflowModel struct {
	mu    sync.Mutex
	calls int
}

func (m *planWorkflowModel) StreamMessage(_ context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var planPath string
	for _, msg := range messages {
		if _, suffix, ok := strings.Cut(msg.Content, "计划文件必须写入："); ok {
			planPath = strings.SplitN(suffix, "\n", 2)[0]
		}
	}
	id := strings.TrimSuffix(filepath.Base(planPath), ".md")
	var response string
	switch m.calls {
	case 0:
		response = "你希望优先降低延迟还是节省内存？"
	case 1:
		response = goalWorkflowTool("write-plan", "Write", map[string]string{"file_path": planPath, "content": "# 缓存方案\n\n先测量命中率，再验证过期策略。\n\n验收：测试通过。"})
	case 2:
		response = goalWorkflowTool("save-plan", "plan_finalize", map[string]string{"plan_id": id, "path": planPath})
	case 3:
		response = "计划已保存，请继续修改或明确执行。"
	default:
		return nil, fmt.Errorf("plan unexpectedly started execution (request %d)", m.calls)
	}
	m.calls++
	ch := make(chan model.StreamEvent, 2)
	ch <- model.StreamEvent{Delta: response}
	ch <- model.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func TestPlanWorkflowWaitsThenSavesWithoutExecuting(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "plan-session"}); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.WriteTool{Root: root})
	selection := selecttool.NewBroker()
	defer selection.Close()
	toolset := NewToolset(nil)
	if err := toolset.RegisterInteractive(registry, selection); err != nil {
		t.Fatal(err)
	}
	model := &planWorkflowModel{}
	engine := loop.NewEngineWithInstructionRoot(model, headless.New(io.Discard), registry, store, "plan-session", root)
	host, err := sessionactor.NewHost(engine, store, "plan-session")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	controller := plan.NewSessionController(host, PlansDir(engine), store.Dir())
	toolset.Finalize().SetHook(controller.Finalize)
	stopped := make(chan string, 4)
	ready := make(chan plan.PlanDoc, 1)
	controller.SetStopped(func(reason string) { stopped <- reason })
	controller.SetNotify(func(doc plan.PlanDoc) { ready <- doc })
	id, err := controller.Submit("讨论缓存设计")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-stopped:
		if reason != string(plan.SessionAwaitingInput) {
			t.Fatalf("plan did not wait after clarification: %s", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plan did not return control")
	}
	continued, err := controller.Submit("先优化内存，请保存计划")
	submitDeadline := time.Now().Add(time.Second)
	for errors.Is(err, plan.ErrPlanActive) && time.Now().Before(submitDeadline) {
		time.Sleep(time.Millisecond)
		continued, err = controller.Submit("先优化内存，请保存计划")
	}
	if err != nil || continued != id {
		t.Fatalf("same conversation = %q, error %v", continued, err)
	}
	var doc plan.PlanDoc
	select {
	case doc = <-ready:
		if doc.Status != plan.PlanReady {
			t.Fatalf("save approved prematurely: %+v", doc)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("plan did not save: %s", controller.Status())
	}
	model.mu.Lock()
	count := model.calls
	model.mu.Unlock()
	if count != 4 {
		t.Fatalf("plan calls %d, want two conversational turns and no execution", count)
	}
	approved, err := controller.Execute()
	deadline := time.Now().Add(time.Second)
	for errors.Is(err, plan.ErrPlanNotReady) && time.Now().Before(deadline) {
		// Receipt of the ready notification can precede its callback returning.
		time.Sleep(time.Millisecond)
		approved, err = controller.Execute()
	}
	if err != nil || approved.Status != plan.PlanApproved || approved.Content != doc.Content {
		t.Fatalf("explicit execution = %+v, error %v", approved, err)
	}
	if _, err := controller.Execute(); err == nil {
		t.Fatal("already executed plan accepted again")
	}
}
