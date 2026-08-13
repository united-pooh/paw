package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"paw/internal/es"
)

// EventStore 是 goal 聚合的事件溯源存储，实现 GoalStore 接口。事件流为唯一
// 事实来源；Goal 状态由 es.Loader 从快照 + 事件重放重建。Update 通过字段
// diff 产出领域事件，禁止裸状态覆盖；不可变字段变更被拒绝。
type EventStore struct {
	events   *es.JSONLStore
	registry *es.Registry
	loader   *es.Loader
}

var _ GoalStore = (*EventStore)(nil)

func NewEventStore(baseDir string) (*EventStore, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("goal: baseDir is required")
	}
	events, err := es.NewJSONLStore(baseDir, "goals")
	if err != nil {
		return nil, err
	}
	registry := es.NewRegistry()
	if err := RegisterEvents(registry); err != nil {
		return nil, err
	}
	return &EventStore{
		events:   events,
		registry: registry,
		loader:   &es.Loader{Store: events, Registry: registry},
	}, nil
}

func (s *EventStore) streamExists(id GoalID) (bool, error) {
	path, err := s.events.StreamPath(string(id))
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// appendEvents 追加事件并在达到快照间隔时写快照（快照为缓存，失败不阻断）。
func (s *EventStore) appendEvents(ctx context.Context, id GoalID, events []es.Envelope) error {
	_, last, err := s.events.Append(ctx, string(id), events)
	if err != nil {
		return err
	}
	s.maybeSnapshot(ctx, id, last)
	return nil
}

func (s *EventStore) maybeSnapshot(ctx context.Context, id GoalID, last int64) {
	interval := s.loader.SnapshotInterval
	if interval <= 0 {
		interval = es.DefaultSnapshotInterval
	}
	if last <= 0 || last%int64(interval) != 0 {
		return
	}
	var g Goal
	if _, err := s.loader.Load(ctx, string(id), &goalESState{g: &g}); err != nil || g.IsDeleted() {
		return
	}
	raw, err := json.Marshal(&g)
	if err != nil {
		return
	}
	_ = s.events.WriteSnapshot(ctx, string(id), last, raw)
}

func (s *EventStore) Create(ctx context.Context, g Goal) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if g.ID == "" {
		return fmt.Errorf("goal id is empty")
	}
	exists, err := s.streamExists(g.ID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("goal %q already exists", g.ID)
	}
	if g.Status == "" {
		g.Status = GoalDraft
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	g.Budget = g.Budget.Normalize()
	payload := createdPayload{
		GoalID:             string(g.ID),
		SessionID:          g.SessionID,
		Objective:          g.Objective,
		AcceptanceCriteria: append([]string(nil), g.AcceptanceCriteria...),
		Verification:       append([]VerificationSpec(nil), g.Verification...),
		Budget:             g.Budget,
		Status:             g.Status,
		CreatedAt:          g.CreatedAt,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("goal: encode created payload: %w", err)
	}
	env := es.Envelope{Type: EventCreated, OccurredAt: g.CreatedAt, SchemaVersion: 1, Payload: raw}
	if err := s.appendEvents(ctx, g.ID, []es.Envelope{env}); err != nil {
		return fmt.Errorf("goal: append created: %w", err)
	}
	return nil
}

func (s *EventStore) Get(ctx context.Context, id GoalID) (Goal, bool, error) {
	if err := contextErr(ctx); err != nil {
		return Goal{}, false, err
	}
	if s == nil {
		return Goal{}, false, fmt.Errorf("goal store is nil")
	}
	if id == "" {
		return Goal{}, false, fmt.Errorf("goal id is empty")
	}
	exists, err := s.streamExists(id)
	if err != nil {
		return Goal{}, false, err
	}
	if !exists {
		return Goal{}, false, nil
	}
	var g Goal
	if _, err := s.loader.Load(ctx, string(id), &goalESState{g: &g}); err != nil {
		return Goal{}, false, err
	}
	if g.IsDeleted() {
		return Goal{}, false, nil
	}
	return g, true, nil
}

func (s *EventStore) Update(ctx context.Context, g Goal) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if g.ID == "" {
		return fmt.Errorf("goal id is empty")
	}
	current, ok, err := s.Get(ctx, g.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("goal %q not found", g.ID)
	}
	if g.Revision == 0 {
		return fmt.Errorf("%w: goal %q", ErrRevisionConflict, g.ID)
	}
	if g.Revision != current.Revision && g.Revision != current.Revision+1 {
		return fmt.Errorf("%w: goal %q", ErrRevisionConflict, g.ID)
	}
	events, err := diffGoal(current, g)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if err := s.appendEvents(ctx, g.ID, events); err != nil {
		return fmt.Errorf("goal: append update events: %w", err)
	}
	return nil
}

func (s *EventStore) List(ctx context.Context, sessionID string) ([]GoalSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("goal store is nil")
	}
	dir := filepath.Join(s.events.Dir(), "goals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]GoalSnapshot, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".events.jsonl") {
			continue
		}
		id := GoalID(strings.TrimSuffix(entry.Name(), ".events.jsonl"))
		g, ok, err := s.Get(ctx, id)
		if err != nil || !ok {
			continue
		}
		if sessionID == "" || g.SessionID == sessionID {
			out = append(out, g.Snapshot())
		}
	}
	return out, nil
}

func (s *EventStore) Delete(ctx context.Context, id GoalID) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if _, ok, err := s.Get(ctx, id); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("goal %q not found", id)
	}
	env := es.Envelope{Type: EventDeleted, OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}
	if err := s.appendEvents(ctx, id, []es.Envelope{env}); err != nil {
		return fmt.Errorf("goal: append deleted: %w", err)
	}
	return nil
}

// diffGoal 比较当前与传入状态，产出领域事件。状态转移经 CanTransition
// 校验；统计字段变化产出 goal.stats.updated；不可变字段变更报错。
func diffGoal(current, next Goal) ([]es.Envelope, error) {
	if !goalsEqualImmutable(current, next) {
		return nil, fmt.Errorf("goal: immutable fields (objective/acceptance criteria/verification/budget/session) must not change")
	}
	var events []es.Envelope
	now := next.UpdatedAt
	if now.IsZero() {
		now = time.Now()
	}
	if next.Status != current.Status {
		if !CanTransition(current.Status, next.Status) {
			return nil, fmt.Errorf("invalid goal transition: %s -> %s", current.Status, next.Status)
		}
		env, err := transitionEvent(next, now)
		if err != nil {
			return nil, err
		}
		events = append(events, env)
	}
	if statsChanged(current, next) {
		raw, err := json.Marshal(statsPayload{
			TurnsUsed:        next.TurnsUsed,
			ToolCallsUsed:    next.ToolCallsUsed,
			NoProgressCount:  next.NoProgressCount,
			ContinuationUsed: next.ContinuationUsed,
			CurrentTaskID:    next.CurrentTaskID,
			LastDecision:     next.LastDecision,
		})
		if err != nil {
			return nil, fmt.Errorf("goal: encode stats payload: %w", err)
		}
		events = append(events, es.Envelope{
			Type:          EventStatsUpdate,
			OccurredAt:    now,
			SchemaVersion: 1,
			Payload:       raw,
		})
	}
	return events, nil
}

func transitionEvent(next Goal, now time.Time) (es.Envelope, error) {
	makeEnv := func(typ string, payload any) (es.Envelope, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return es.Envelope{}, fmt.Errorf("goal: encode %s payload: %w", typ, err)
		}
		return es.Envelope{Type: typ, OccurredAt: now, SchemaVersion: 1, Payload: raw}, nil
	}
	switch next.Status {
	case GoalRunning:
		if next.PauseReason != "" {
			return es.Envelope{}, fmt.Errorf("goal: running state must not carry a pause reason")
		}
		return makeEnv(evStarted, struct{}{})
	case GoalPaused:
		if next.PauseReason == "" {
			return es.Envelope{}, fmt.Errorf("goal: paused state requires a pause reason")
		}
		return makeEnv(evPaused, reasonPayload{Reason: next.PauseReason})
	case GoalBlocked:
		if next.PauseReason == "" {
			return es.Envelope{}, fmt.Errorf("goal: blocked state requires a pause reason")
		}
		return makeEnv(evBlocked, reasonPayload{Reason: next.PauseReason})
	case GoalReplanning:
		return makeEnv(evReplanned, struct{}{})
	case GoalCompleted:
		return makeEnv(evCompleted, struct{}{})
	case GoalFailed:
		return makeEnv(evFailed, failedPayload{Reason: string(next.PauseReason)})
	case GoalCancelled:
		return makeEnv(evCancelled, struct{}{})
	default:
		return es.Envelope{}, fmt.Errorf("goal: cannot transition to %s", next.Status)
	}
}

func goalsEqualImmutable(a, b Goal) bool {
	if a.SessionID != b.SessionID || a.Objective != b.Objective {
		return false
	}
	if len(a.AcceptanceCriteria) != len(b.AcceptanceCriteria) {
		return false
	}
	for i := range a.AcceptanceCriteria {
		if a.AcceptanceCriteria[i] != b.AcceptanceCriteria[i] {
			return false
		}
	}
	if len(a.Verification) != len(b.Verification) {
		return false
	}
	for i := range a.Verification {
		if !verificationEqual(a.Verification[i], b.Verification[i]) {
			return false
		}
	}
	return a.Budget == b.Budget
}

func verificationEqual(a, b VerificationSpec) bool {
	if a.ID != b.ID || a.Kind != b.Kind || a.Command != b.Command || a.Required != b.Required {
		return false
	}
	if len(a.Scope) != len(b.Scope) {
		return false
	}
	for i := range a.Scope {
		if a.Scope[i] != b.Scope[i] {
			return false
		}
	}
	return true
}

func statsChanged(a, b Goal) bool {
	return a.TurnsUsed != b.TurnsUsed ||
		a.ToolCallsUsed != b.ToolCallsUsed ||
		a.NoProgressCount != b.NoProgressCount ||
		a.ContinuationUsed != b.ContinuationUsed ||
		a.CurrentTaskID != b.CurrentTaskID ||
		a.LastDecision != b.LastDecision
}
