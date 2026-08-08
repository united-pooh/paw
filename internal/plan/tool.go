package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FinalizeHook approves a plan document. Implementations must validate the
// session and path, mark the document approved, and return the updated doc.
type FinalizeHook func(ctx context.Context, id PlanID, path string) (PlanDoc, error)

// FinalizeTool is the deterministic approval signal of a plan session: the
// agent calls it only after the user picks "执行" in the final Select. It is
// only visible (and only accepted) while a plan session is active.
type FinalizeTool struct {
	mu   sync.RWMutex
	hook FinalizeHook
}

func NewFinalizeTool(hook FinalizeHook) *FinalizeTool {
	return &FinalizeTool{hook: hook}
}

// SetHook installs or replaces the approval hook (wired once the session
// controller exists).
func (t *FinalizeTool) SetHook(hook FinalizeHook) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hook = hook
}

func (t *FinalizeTool) Name() string { return "plan_finalize" }

func (t *FinalizeTool) Description() string {
	return "Finalize the current plan document after the user approved it (chose 执行). " +
		"Input: {\"plan_id\": \"<id>\", \"path\": \"<absolute path of the plan file>\"}. " +
		"Only call this AFTER the user selected 执行; do not call it when the user chose 修改."
}

func (t *FinalizeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"plan_id":{"type":"string"},"path":{"type":"string"}},"required":["plan_id","path"],"additionalProperties":false}`)
}

func (t *FinalizeTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	t.mu.RLock()
	hook := t.hook
	t.mu.RUnlock()
	if hook == nil {
		return "", fmt.Errorf("plan finalize hook is unavailable")
	}
	var input struct {
		PlanID string `json:"plan_id"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid plan_finalize input: %w", err)
	}
	if input.PlanID == "" || input.Path == "" {
		return "", fmt.Errorf("plan_finalize requires plan_id and path")
	}
	doc, err := hook(ctx, PlanID(input.PlanID), input.Path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan finalized and approved: %s (status=%s)", doc.Path, doc.Status), nil
}
