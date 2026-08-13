package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"paw/internal/es"
)

// errLegacySnapshot 标记旧格式快照（裸 Goal JSON，不含 evidence/checkpoints）。
// 快照是缓存而非事实；调用方应删除旧快照并全量重放事件流。
var errLegacySnapshot = errors.New("goal: legacy snapshot without evidence/checkpoints")

// goalESState 适配 Goal 为 es.State。Goal.Snapshot() 已占用（返回 GoalSnapshot），
// 故快照序列化经由本适配器。evidence 与 checkpoint 是 goal 聚合的子状态，
// 随聚合流持久化并纳入快照。
type goalESState struct {
	g           *Goal
	evidence    map[string]Evidence
	checkpoints []GoalCheckpoint
}

// goalSnapshotPayload 是快照的当前格式。旧格式是裸 Goal JSON（无 goal 键），
// Restore 时检测并返回 errLegacySnapshot。
type goalSnapshotPayload struct {
	Goal        *Goal            `json:"goal"`
	Evidence    []Evidence       `json:"evidence,omitempty"`
	Checkpoints []GoalCheckpoint `json:"checkpoints,omitempty"`
}

func (s *goalESState) Apply(payload es.Payload, env es.Envelope) error {
	switch env.Type {
	case evEvidenceAdded:
		p, ok := payload.(evidencePayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		if s.evidence == nil {
			s.evidence = map[string]Evidence{}
		}
		s.evidence[p.EvidenceID] = Evidence{
			ID:        p.EvidenceID,
			GoalID:    GoalID(p.GoalID),
			StepID:    p.StepID,
			Kind:      p.Kind,
			Command:   p.Command,
			Status:    p.Status,
			Summary:   p.Summary,
			Scope:     append([]string(nil), p.Scope...),
			Digest:    p.Digest,
			CreatedAt: p.CreatedAt,
			Stale:     p.Stale,
		}
		return nil
	case evEvidenceStaleMarked:
		p, ok := payload.(staleMarkedPayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		for id, e := range s.evidence {
			if scopeMatchesChanged(e.Scope, p.ChangedFiles) {
				e.Stale = true
				e.Status = EvidenceStale
				s.evidence[id] = e
			}
		}
		return nil
	case evCheckpointSaved:
		p, ok := payload.(checkpointPayload)
		if !ok {
			return fmt.Errorf("goal: payload type mismatch for %s", env.Type)
		}
		s.checkpoints = append(s.checkpoints, checkpointFromPayload(p))
		return nil
	case evCheckpointDeleted:
		s.checkpoints = nil
		return nil
	default:
		return s.g.Apply(payload, env)
	}
}

func (s *goalESState) Snapshot() (json.RawMessage, error) {
	evidence := make([]Evidence, 0, len(s.evidence))
	for _, e := range s.evidence {
		evidence = append(evidence, e)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	sp := goalSnapshotPayload{Goal: s.g, Evidence: evidence, Checkpoints: s.checkpoints}
	return json.Marshal(&sp)
}

func (s *goalESState) Restore(data json.RawMessage) error {
	var sp goalSnapshotPayload
	if err := json.Unmarshal(data, &sp); err == nil && sp.Goal != nil {
		s.g = sp.Goal
		s.evidence = make(map[string]Evidence, len(sp.Evidence))
		for _, e := range sp.Evidence {
			s.evidence[e.ID] = e
		}
		s.checkpoints = append([]GoalCheckpoint(nil), sp.Checkpoints...)
		return nil
	}
	var g Goal
	if err := json.Unmarshal(data, &g); err != nil {
		return err
	}
	return errLegacySnapshot
}

// scopeMatchesChanged 与 MemoryEvidenceStore.MarkStaleByChangedFiles 的匹配
// 规则一致：文件路径 trim 后相等即视为命中。
func scopeMatchesChanged(scope, changed []string) bool {
	for _, a := range changed {
		for _, b := range scope {
			if strings.TrimSpace(a) == strings.TrimSpace(b) {
				return true
			}
		}
	}
	return false
}

func checkpointFromPayload(p checkpointPayload) GoalCheckpoint {
	return GoalCheckpoint{
		ID:               p.CheckpointID,
		GoalID:           GoalID(p.GoalID),
		SessionID:        p.SessionID,
		Status:           p.Status,
		Objective:        p.Objective,
		TodoSnapshot:     p.TodoSnapshot,
		EvidenceIDs:      append([]string(nil), p.EvidenceIDs...),
		ContinuationUsed: p.ContinuationUsed,
		NoProgressCount:  p.NoProgressCount,
		LastDecision:     p.LastDecision,
		ProgressHash:     p.ProgressHash,
		PauseReason:      p.PauseReason,
		NextInput:        p.NextInput,
		CreatedAt:        p.CreatedAt,
	}
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
