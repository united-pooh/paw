package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/plan"
	"paw/internal/todo"
)

// newTestProvider 构造带临时目录的 provider。
func newTestProvider(t *testing.T) (*stateBlockProvider, string, *todo.Broker) {
	t.Helper()
	root := t.TempDir()
	broker := todo.NewBroker()
	plansDir := filepath.Join(root, "plans")
	store := plan.NewFileStore(plansDir)
	p := newStateBlockProvider(store, broker, "test-session", root, filepath.Join(root, "memory.md"))
	return p, root, broker
}

// TestStateBlockProviderFull 覆盖：plan + todo + memory + ariadne 全部
// 存在时组装完整状态块，含时间戳标注。
func TestStateBlockProviderFull(t *testing.T) {
	p, root, broker := newTestProvider(t)
	ctx := context.Background()

	doc := plan.PlanDoc{ID: "2026-08-13-test-plan", Title: "完成状态压缩", Status: plan.PlanApproved}
	if err := p.planStore.(*plan.FileStore).Create(ctx, doc); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	broker.Publish(todo.Snapshot{
		Explanation: "进度",
		Items: []todo.Item{
			{ID: "1", Content: "T1 存储层", Status: todo.StatusCompleted},
			{ID: "2", Content: "T2 注入器", Status: todo.StatusInProgress},
		},
		UpdatedAt: time.Now(),
	})
	memory := "用户偏好：中文回复，简洁"
	ariadne := "## 方向\n状态压缩实施\n\n## 下一步\n1. 切片 2"
	if err := os.WriteFile(filepath.Join(root, "memory.md"), []byte(memory), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sessions", "test-session"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions", "test-session", "ariadne.md"), []byte(ariadne), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := p.BuildStateContext(ctx)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"## 目标（plan）", "完成状态压缩", "## 进度（todo）", "T1 存储层", "## 长期习惯（memory）", "用户偏好", "## 方向记忆（ariadne）", "状态压缩实施", "updated"} {
		if !strings.Contains(out, want) {
			t.Fatalf("state block missing %q:\n%s", want, out)
		}
	}
}

// TestStateBlockProviderEmptyComponents 覆盖：组件缺失时跳过，不报错；
// 全部缺失返回空串。
func TestStateBlockProviderEmptyComponents(t *testing.T) {
	p, root, broker := newTestProvider(t)
	ctx := context.Background()

	// 只有 memory 存在。
	if err := os.WriteFile(filepath.Join(root, "memory.md"), []byte("习惯"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := p.BuildStateContext(ctx)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(out, "## 长期习惯（memory）") || strings.Contains(out, "## 目标") {
		t.Fatalf("partial block mismatch:\n%s", out)
	}

	// 全空。
	p2 := newStateBlockProvider(nil, broker, "", "", "")
	empty, err := p2.BuildStateContext(ctx)
	if err != nil || empty != "" {
		t.Fatalf("all-empty must return empty: %q err=%v", empty, err)
	}
}

// TestStateBlockProviderOneLine 覆盖：标题换行截断与长度限制。
func TestStateBlockProviderOneLine(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := oneLine(long + "\nsecond line")
	if len([]rune(got)) > 121 {
		t.Fatalf("oneLine too long: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("oneLine must truncate with ellipsis: %q", got)
	}
	if oneLine("a\nb") != "a" {
		t.Fatal("oneLine must cut at newline")
	}
}
