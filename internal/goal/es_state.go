package goal

import (
	"encoding/json"
	"fmt"

	"paw/internal/es"
)

// goalESState 适配 Goal 为 es.State。Goal.Snapshot() 已占用（返回 GoalSnapshot），
// 故快照序列化经由本适配器。
type goalESState struct {
	g *Goal
}

func (s *goalESState) Apply(payload es.Payload, env es.Envelope) error {
	return s.g.Apply(payload, env)
}

func (s *goalESState) Snapshot() (json.RawMessage, error) {
	return json.Marshal(s.g)
}

func (s *goalESState) Restore(data json.RawMessage) error {
	return json.Unmarshal(data, s.g)
}

// Apply 将已解码的 goal 域事件应用到状态。事件顺序由加载管道保证。
func (g *Goal) Apply(payload es.Payload, env es.Envelope) error {
	if g == nil {
		return fmt.Errorf("goal is nil")
	}
	switch env.Type {
	case evCreated:
		p, ok := payload.(createdPayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		g.ID = GoalID(p.GoalID)
		g.SessionID = p.SessionID
		g.Objective = p.Objective
		g.AcceptanceCriteria = append([]string(nil), p.AcceptanceCriteria...)
		g.Verification = append([]VerificationSpec(nil), p.Verification...)
		g.Budget = p.Budget
		g.Status = p.Status
		g.CreatedAt = p.CreatedAt
		g.UpdatedAt = env.OccurredAt
		g.Revision = 1
	case evStarted:
		g.Status = GoalRunning
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evPaused, evBlocked:
		p, ok := payload.(reasonPayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		if env.Type == evPaused {
			g.Status = GoalPaused
		} else {
			g.Status = GoalBlocked
		}
		g.PauseReason = p.Reason
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evResumed:
		g.Status = GoalRunning
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evReplanned:
		g.Status = GoalReplanning
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evCompleted:
		g.Status = GoalCompleted
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evFailed:
		g.Status = GoalFailed
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evCancelled:
		g.Status = GoalCancelled
		g.PauseReason = ""
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	case evDeleted:
		g.deleted = true
	case evStatsUpdate:
		p, ok := payload.(statsPayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		g.TurnsUsed = p.TurnsUsed
		g.ToolCallsUsed = p.ToolCallsUsed
		g.NoProgressCount = p.NoProgressCount
		g.ContinuationUsed = p.ContinuationUsed
		g.CurrentTaskID = p.CurrentTaskID
		g.LastDecision = p.LastDecision
		g.UpdatedAt = env.OccurredAt
		g.Revision++
	default:
		return fmt.Errorf("goal: unknown event type %q", env.Type)
	}
	return nil
}

// IsDeleted 报告该聚合是否已收到 goal.deleted 墓碑事件。
func (g *Goal) IsDeleted() bool {
	return g != nil && g.deleted
}
