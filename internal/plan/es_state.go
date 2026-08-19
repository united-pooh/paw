package plan

import (
	"encoding/json"
	"fmt"

	"paw/internal/es"
)

// esState 是 plan 聚合的投影状态：文档字段 + 会话生命周期状态。
// 文档文件是投影产物（用户可见），事件流是事实来源。
type esState struct {
	Doc           PlanDoc
	SessionStatus SessionStatus
	PauseReason   PauseReason
}

func (s *esState) Apply(payload es.Payload, env es.Envelope) error {
	if s == nil {
		return fmt.Errorf("plan: state is nil")
	}
	switch env.Type {
	case EventCreated, EventBaseline:
		p, ok := payload.(createdPayload)
		if !ok {
			return fmt.Errorf("plan: payload type mismatch for %s", env.Type)
		}
		s.Doc = PlanDoc{
			ID:        PlanID(p.PlanID),
			SessionID: p.SessionID,
			Title:     p.Title,
			Path:      p.Path,
			Content:   p.Content,
			Status:    p.Status,
			CreatedAt: p.CreatedAt,
			UpdatedAt: p.UpdatedAt,
		}
		s.SessionStatus = sessionStatusFromPlanStatus(p.Status)
	case EventDocUpdated:
		p, ok := payload.(docUpdatedPayload)
		if !ok {
			return fmt.Errorf("plan: payload type mismatch for %s", env.Type)
		}
		s.Doc.Title = p.Title
		s.Doc.Path = p.Path
		s.Doc.Content = p.Content
		s.Doc.UpdatedAt = p.UpdatedAt
	case EventStatusChanged:
		p, ok := payload.(statusChangedPayload)
		if !ok {
			return fmt.Errorf("plan: payload type mismatch for %s", env.Type)
		}
		s.SessionStatus = p.Status
		s.PauseReason = p.Reason
		if p.Status == SessionApproved {
			s.Doc.Status = PlanApproved
		} else if s.Doc.Status == PlanApproved && p.Status != SessionApproved {
			s.Doc.Status = PlanDraft
		}
		s.Doc.UpdatedAt = p.UpdatedAt
	default:
		return fmt.Errorf("plan: unknown event type %q", env.Type)
	}
	return nil
}

func sessionStatusFromPlanStatus(status PlanStatus) SessionStatus {
	if status == PlanApproved {
		return SessionApproved
	}
	return SessionClarifying
}

func (s *esState) Snapshot() (json.RawMessage, error) {
	return json.Marshal(s)
}

func (s *esState) Restore(data json.RawMessage) error {
	return json.Unmarshal(data, s)
}
