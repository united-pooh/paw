package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"paw/internal/runtime/goal"
	"paw/internal/runtime/plan"
	"paw/internal/todo"
	"strings"
	"time"
)

// stateBlockProvider 组装模式 B（状态压缩）恢复用的结构化状态块
// （设计文档 D9 字节稳定 / D12 时间戳标注）。
// 习惯与进度可缺失；Goal 验收状态读取失败必须返回错误。
type stateBlockProvider struct {
	goalStore  goal.GoalStore
	planStore  plan.DocStore
	todoBroker *todo.Broker
	todoStore  interface {
		LoadLatestTodoSnapshot(context.Context, string) (todo.Snapshot, bool, error)
	}
	sessionID   string
	sessionBase string // session store 根目录（~/.paw/projects/<项目>）
	memoryPath  string // ~/.paw/memory.md
}

func newStateBlockProvider(planStore plan.DocStore, broker *todo.Broker, sessionID, sessionBase, memoryPath string) *stateBlockProvider {
	return &stateBlockProvider{
		planStore:   planStore,
		todoBroker:  broker,
		sessionID:   sessionID,
		sessionBase: sessionBase,
		memoryPath:  memoryPath,
	}
}

// BuildStateContext 组装状态块。返回空字符串表示无任何可用状态。
func (p *stateBlockProvider) BuildStateContext(ctx context.Context) (string, error) {
	if p == nil {
		return "", nil
	}
	var b strings.Builder
	any := false
	if p.goalStore != nil && strings.TrimSpace(p.sessionID) != "" {
		goals, err := p.goalStore.List(ctx, p.sessionID)
		if err != nil {
			return "", fmt.Errorf("load goal state: %w", err)
		}
		var current *goal.GoalSnapshot
		for i := range goals {
			candidate := &goals[i]
			if current == nil || (current.Status.Terminal() && !candidate.Status.Terminal()) ||
				(current.Status.Terminal() == candidate.Status.Terminal() && candidate.UpdatedAt.After(current.UpdatedAt)) {
				current = candidate
			}
		}
		if current != nil {
			if len(current.Measurements) > 5 {
				current.Measurements = current.Measurements[len(current.Measurements)-5:]
			}
			raw, err := json.Marshal(current)
			if err != nil {
				return "", fmt.Errorf("encode goal state: %w", err)
			}
			fmt.Fprintf(&b, "## 目标（goal）\n验收指标固定；TODO 完成不代表达标。以下为持久化状态（最多保留最近五次测量）：\n%s\n", raw)
			any = true
		}
	}

	if p.planStore != nil {
		if docs, err := p.planStore.List(ctx); err == nil {
			var planLines []string
			for _, doc := range docs {
				status := strings.ToLower(string(doc.Status))
				ts := doc.UpdatedAt
				planLines = append(planLines, fmt.Sprintf("- [%s] %s (updated %s)", status, oneLine(doc.Title), ts.Format(time.RFC3339)))
			}
			if len(planLines) > 0 {
				fmt.Fprintf(&b, "## 计划（plan）\n%s\n", strings.Join(planLines, "\n"))
				any = true
			}
		}
	}

	if p.todoBroker != nil || p.todoStore != nil {
		snap, ok := p.latestTodo(ctx)
		if ok {
			var todoLines []string
			if explanation := oneLine(snap.Explanation); explanation != "" {
				fmt.Fprintf(&b, "## 进度（todo）\n说明：%s\n", explanation)
			} else {
				b.WriteString("## 进度（todo）\n")
			}
			for _, item := range snap.Items {
				status := string(item.Status)
				todoLines = append(todoLines, fmt.Sprintf("- [%s] %s (id: %s)", status, oneLine(item.Content), oneLine(item.ID)))
				if len(item.BlockedBy) > 0 {
					todoLines = append(todoLines, fmt.Sprintf("  id: %q; blocked_by: %q; unresolved_blockers: %q", item.ID, item.BlockedBy, snap.UnresolvedBlockers(item.ID)))
				}
			}
			if len(todoLines) > 0 {
				fmt.Fprintf(&b, "%s\n", strings.Join(todoLines, "\n"))
			}
			fmt.Fprintf(&b, "权威快照更新时间（updated %s）\n", snap.UpdatedAt.Format(time.RFC3339))
			any = true
		}
	}

	if text, ts, ok := readStateFile(p.memoryPath); ok && strings.TrimSpace(text) != "" {
		fmt.Fprintf(&b, "## 长期习惯（memory）\n%s\n(memory.md updated %s)\n", strings.TrimSpace(text), ts)
		any = true
	}

	if p.sessionID != "" && p.sessionBase != "" {
		ariadnePath := filepath.Join(p.sessionBase, "sessions", p.sessionID, "ariadne.md")
		if text, ts, ok := readStateFile(ariadnePath); ok && strings.TrimSpace(text) != "" {
			fmt.Fprintf(&b, "## 方向记忆（ariadne）\n%s\n(ariadne.md updated %s)\n", strings.TrimSpace(text), ts)
			any = true
		}
	}

	if !any {
		return "", nil
	}
	return b.String(), nil
}

func (p *stateBlockProvider) latestTodo(ctx context.Context) (todo.Snapshot, bool) {
	if p == nil {
		return todo.Snapshot{}, false
	}
	if p.todoBroker != nil {
		if snapshot, ok := p.todoBroker.Latest(); ok {
			return snapshot, true
		}
	}
	if p.todoStore != nil {
		snapshot, ok, err := p.todoStore.LoadLatestTodoSnapshot(ctx, p.sessionID)
		if err == nil && ok {
			return snapshot, true
		}
	}
	return todo.Snapshot{}, false
}

func readStateFile(path string) (string, string, bool) {
	if path == "" {
		return "", "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	ts := ""
	if fi, err := os.Stat(path); err == nil {
		ts = fi.ModTime().Format(time.RFC3339)
	}
	return string(data), ts, true
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	runes := []rune(s)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return s
}
