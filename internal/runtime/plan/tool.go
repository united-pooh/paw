package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FinalizeHook validates and saves a ready plan without authorizing execution.
type FinalizeHook func(ctx context.Context, id PlanID, path string) (PlanDoc, error)

// FinalizeTool preserves the existing tool name for saving a usable plan.
// Execution approval is available only through the host's user action.
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
	return "Save the current plan document as ready for the user to review or execute later. This does not approve or start implementation. " +
		"Input: {\"plan_id\": \"<id>\", \"path\": \"<absolute path of the plan file>\"}. " +
		"The user may continue discussing or revising the plan; execution requires the explicit /plan execute user action."
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
	return fmt.Sprintf("plan saved and ready: %s (status=%s); implementation has not started", doc.Path, doc.Status), nil
}
