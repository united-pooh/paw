package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/session"
)

func inputFor(content string) []byte {
	b, _ := json.Marshal(map[string]string{"content": content})
	return b
}

const validAriadne = `## 方向
完成状态压缩

## 进度
- [x] T1
- [ ] T2

## 关键决策
- **双列方案**：理由

## 下一步
1. T2

## 教训
无
`

func TestUpdateMemoryTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.md")
	var events []session.StateEventKind
	tool := NewUpdateMemory(path, func(_ context.Context, kind session.StateEventKind, _ string) error {
		events = append(events, kind)
		return nil
	})

	out, err := tool.Run(context.Background(), inputFor("用户偏好中文"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "已更新") {
		t.Fatalf("output: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "用户偏好中文" {
		t.Fatalf("file content: %q", data)
	}
	if len(events) != 1 || events[0] != session.StateEventMemory {
		t.Fatalf("event not recorded: %+v", events)
	}

	// 空内容拒绝。
	if _, err := tool.Run(context.Background(), inputFor("  ")); err == nil {
		t.Fatal("empty content must error")
	}
	// 超限拒绝。
	tooLong := strings.Repeat("x", MaxMemoryRunes+1)
	if _, err := tool.Run(context.Background(), inputFor(tooLong)); err == nil {
		t.Fatal("oversize must error")
	}
}

func TestUpdateAriadneTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "s1", "ariadne.md")
	var events []session.StateEventKind
	tool := NewUpdateAriadne(path, func(_ context.Context, kind session.StateEventKind, _ string) error {
		events = append(events, kind)
		return nil
	})

	out, err := tool.Run(context.Background(), inputFor(validAriadne))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "已更新") {
		t.Fatalf("output: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != strings.TrimSpace(validAriadne) {
		t.Fatalf("file mismatch:\n%s", data)
	}
	if len(events) != 1 || events[0] != session.StateEventAriadne {
		t.Fatalf("event not recorded: %+v", events)
	}

	// 缺 section 拒绝。
	incomplete := "## 方向\nxxx\n"
	if _, err := tool.Run(context.Background(), inputFor(incomplete)); err == nil {
		t.Fatal("incomplete ariadne must error")
	}
}

func TestValidateAriadne(t *testing.T) {
	if err := validateAriadne(validAriadne); err != nil {
		t.Fatalf("valid ariadne rejected: %v", err)
	}
	for _, missing := range []string{"## 方向", "## 进度", "## 关键决策", "## 下一步", "## 教训"} {
		content := strings.ReplaceAll(validAriadne, missing, "## 缺失")
		if err := validateAriadne(content); err == nil {
			t.Fatalf("missing %q must be rejected", missing)
		}
	}
}

func TestAtomicWriteCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "memory.md")
	if err := atomicWrite(path, "x"); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "x" {
		t.Fatalf("read back: %q err=%v", data, err)
	}
}
