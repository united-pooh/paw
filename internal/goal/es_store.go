package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"paw/internal/es"
)

// EventStore 是 goal 聚合的事件溯源存储，实现 GoalStore 接口。事件流为唯一
// 事实来源；Goal 状态由 es.Loader 从快照 + 事件重放重建。Update 通过字段
// diff 产出领域事件，禁止裸状态覆盖；不可变字段变更被拒绝。
// evidence 与 checkpoint 是 goal 聚合的子状态，同样由事件流重建（经
// EvidenceStore()/CheckpointStore() 适配接口，方法名冲突故不直接实现）。
type EventStore struct {
	events   *es.JSONLStore
	registry *es.Registry
	loader   *es.Loader
}

var _ GoalStore = (*EventStore)(nil)
var _ EvidenceStore = evidenceStoreAdapter{}
var _ CheckpointStore = checkpointStoreAdapter{}

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

// loadState 重建聚合状态（快照 + 事件重放）。旧格式快照（不含
// evidence/checkpoints）被检测后删除缓存并全量重放——快照是缓存，不是事实。
func (s *EventStore) loadState(ctx context.Context, id GoalID) (*goalESState, bool, error) {
	exists, err := s.streamExists(id)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	st := &goalESState{g: &Goal{}, evidence: map[string]Evidence{}}
	if _, err := s.loader.Load(ctx, string(id), st); err != nil {
		if !errors.Is(err, errLegacySnapshot) {
			return nil, false, err
		}
		if err := s.dropSnapshot(string(id)); err != nil {
			return nil, false, err
		}
		st = &goalESState{g: &Goal{}, evidence: map[string]Evidence{}}
		if _, err := s.loader.Load(ctx, string(id), st); err != nil {
			return nil, false, err
		}
	}
	return st, true, nil
}

func (s *EventStore) dropSnapshot(id string) error {
	path := filepath.Join(s.events.Dir(), "goals", id+".snapshot.json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("goal: drop legacy snapshot %s: %w", path, err)
	}
	return nil
}

// listGoalIDs 枚举事件流目录中的 goal id（流文件即聚合存在性）。
func (s *EventStore) listGoalIDs() ([]GoalID, error) {
	dir := filepath.Join(s.events.Dir(), "goals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []GoalID
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".events.jsonl") {
			continue
		}
		ids = append(ids, GoalID(strings.TrimSuffix(entry.Name(), ".events.jsonl")))
	}
	return ids, nil
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
	st, ok, err := s.loadState(ctx, id)
	if err != nil || !ok || st.g.IsDeleted() {
		return
	}
	raw, err := st.Snapshot()
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
	st, ok, err := s.loadState(ctx, id)
	if err != nil {
		return Goal{}, false, err
	}
	if !ok {
		return Goal{}, false, nil
	}
	if st.g.IsDeleted() {
		return Goal{}, false, nil
	}
	return *st.g, true, nil
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
	ids, err := s.listGoalIDs()
	if err != nil {
		return nil, err
	}
	out := make([]GoalSnapshot, 0)
	for _, id := range ids {
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

// AddEvidence 追加 goal.evidence.added 事件。evidence 属于 goal 聚合：
// goal 不存在时拒绝；同 goal 内重复 evidence id 拒绝（与 MemoryEvidenceStore
// 语义一致）。
func (s *EventStore) AddEvidence(ctx context.Context, e Evidence) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if e.ID == "" {
		return errors.New("evidence id is empty")
	}
	if e.GoalID == "" {
		return errors.New("evidence goal id is empty")
	}
	if e.Kind == "" {
		return errors.New("evidence kind is empty")
	}
	if e.Status == "" {
		return errors.New("evidence status is empty")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	st, ok, err := s.loadState(ctx, e.GoalID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("goal %q not found", e.GoalID)
	}
	if _, dup := st.evidence[e.ID]; dup {
		return errors.New("evidence already exists")
	}
	raw, err := json.Marshal(evidencePayload{
		EvidenceID: e.ID,
		GoalID:     string(e.GoalID),
		StepID:     e.StepID,
		Kind:       e.Kind,
		Command:    e.Command,
		Status:     e.Status,
		Summary:    e.Summary,
		Scope:      append([]string(nil), e.Scope...),
		Digest:     e.Digest,
		CreatedAt:  e.CreatedAt,
		Stale:      e.Stale,
	})
	if err != nil {
		return fmt.Errorf("goal: encode evidence payload: %w", err)
	}
	env := es.Envelope{Type: evEvidenceAdded, OccurredAt: e.CreatedAt, SchemaVersion: 1, Payload: raw}
	if err := s.appendEvents(ctx, e.GoalID, []es.Envelope{env}); err != nil {
		return fmt.Errorf("goal: append evidence: %w", err)
	}
	return nil
}

// GetEvidence 按 evidence id 在所有 goal 流中查找（id 全局唯一约定）。
func (s *EventStore) GetEvidence(ctx context.Context, id string) (Evidence, bool, error) {
	if err := contextErr(ctx); err != nil {
		return Evidence{}, false, err
	}
	if s == nil {
		return Evidence{}, false, fmt.Errorf("goal store is nil")
	}
	if id == "" {
		return Evidence{}, false, errors.New("evidence id is empty")
	}
	ids, err := s.listGoalIDs()
	if err != nil {
		return Evidence{}, false, err
	}
	for _, gid := range ids {
		st, ok, err := s.loadState(ctx, gid)
		if err != nil {
			return Evidence{}, false, err
		}
		if !ok {
			continue
		}
		if e, found := st.evidence[id]; found {
			return e.Clone(), true, nil
		}
	}
	return Evidence{}, false, nil
}

// ListEvidenceByGoal 返回某 goal 的全部 evidence，按创建时间排序。
func (s *EventStore) ListEvidenceByGoal(ctx context.Context, gid GoalID) ([]Evidence, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("goal store is nil")
	}
	st, ok, err := s.loadState(ctx, gid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	out := make([]Evidence, 0, len(st.evidence))
	for _, e := range st.evidence {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// MarkEvidenceStaleByChangedFiles 对所有 goal 流中 scope 命中变更文件的
// evidence 追加 goal.evidence.stale_marked 事件（声明式：Apply 时按 scope
// 匹配，重放幂等）。部分流写入失败返回错误，已写入的保留（幂等可重试）。
func (s *EventStore) MarkEvidenceStaleByChangedFiles(ctx context.Context, changed []string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if len(changed) == 0 {
		return nil
	}
	ids, err := s.listGoalIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		st, ok, err := s.loadState(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		hit := false
		for _, e := range st.evidence {
			if scopeMatchesChanged(e.Scope, changed) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		raw, err := json.Marshal(staleMarkedPayload{ChangedFiles: append([]string(nil), changed...)})
		if err != nil {
			return fmt.Errorf("goal: encode stale marked payload: %w", err)
		}
		env := es.Envelope{Type: evEvidenceStaleMarked, OccurredAt: time.Now(), SchemaVersion: 1, Payload: raw}
		if err := s.appendEvents(ctx, id, []es.Envelope{env}); err != nil {
			return fmt.Errorf("goal: append stale marked: %w", err)
		}
	}
	return nil
}

// SaveCheckpoint 追加 goal.checkpoint.saved 事件。checkpoint 属于 goal 聚合：
// goal 不存在时拒绝。ID 缺省时按 goal id + 时间戳生成（与 MemoryCheckpointStore
// 一致）。
func (s *EventStore) SaveCheckpoint(ctx context.Context, c GoalCheckpoint) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if c.GoalID == "" {
		return errors.New("checkpoint goal id is empty")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.ID == "" {
		c.ID = string(c.GoalID) + "-" + c.CreatedAt.UTC().Format("20060102150405.000000000")
	}
	_, ok, err := s.loadState(ctx, c.GoalID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("goal %q not found", c.GoalID)
	}
	raw, err := json.Marshal(checkpointPayload{
		CheckpointID:     c.ID,
		GoalID:           string(c.GoalID),
		SessionID:        c.SessionID,
		Status:           c.Status,
		Objective:        c.Objective,
		TodoSnapshot:     c.TodoSnapshot,
		EvidenceIDs:      append([]string(nil), c.EvidenceIDs...),
		ContinuationUsed: c.ContinuationUsed,
		NoProgressCount:  c.NoProgressCount,
		LastDecision:     c.LastDecision,
		ProgressHash:     c.ProgressHash,
		PauseReason:      c.PauseReason,
		NextInput:        c.NextInput,
		CreatedAt:        c.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("goal: encode checkpoint payload: %w", err)
	}
	env := es.Envelope{Type: evCheckpointSaved, OccurredAt: c.CreatedAt, SchemaVersion: 1, Payload: raw}
	if err := s.appendEvents(ctx, c.GoalID, []es.Envelope{env}); err != nil {
		return fmt.Errorf("goal: append checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint 与 LatestCheckpoint 等价（与 MemoryCheckpointStore 一致：
// Load 即最新 checkpoint）。
func (s *EventStore) LoadCheckpoint(ctx context.Context, gid GoalID) (GoalCheckpoint, bool, error) {
	return s.LatestCheckpoint(ctx, gid)
}

// LatestCheckpoint 返回 goal 最新的 checkpoint（goal.checkpoint.deleted 清空
// 后重新 saved 的 checkpoint 为最新）。
func (s *EventStore) LatestCheckpoint(ctx context.Context, gid GoalID) (GoalCheckpoint, bool, error) {
	if err := contextErr(ctx); err != nil {
		return GoalCheckpoint{}, false, err
	}
	if s == nil {
		return GoalCheckpoint{}, false, fmt.Errorf("goal store is nil")
	}
	if gid == "" {
		return GoalCheckpoint{}, false, errors.New("checkpoint goal id is empty")
	}
	st, ok, err := s.loadState(ctx, gid)
	if err != nil {
		return GoalCheckpoint{}, false, err
	}
	if !ok || len(st.checkpoints) == 0 {
		return GoalCheckpoint{}, false, nil
	}
	return st.checkpoints[len(st.checkpoints)-1].Clone(), true, nil
}

// DeleteCheckpoints 追加 goal.checkpoint.deleted 事件（清空该 goal 的全部
// checkpoint；后续 SaveCheckpoint 重新累积，与 MemoryCheckpointStore 一致）。
func (s *EventStore) DeleteCheckpoints(ctx context.Context, gid GoalID) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if gid == "" {
		return errors.New("checkpoint goal id is empty")
	}
	_, ok, err := s.loadState(ctx, gid)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("goal %q not found", gid)
	}
	env := es.Envelope{Type: evCheckpointDeleted, OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}
	if err := s.appendEvents(ctx, gid, []es.Envelope{env}); err != nil {
		return fmt.Errorf("goal: append checkpoint deleted: %w", err)
	}
	return nil
}

// evidenceStoreAdapter / checkpointStoreAdapter 把 EventStore 适配为
// EvidenceStore / CheckpointStore 接口（EventStore 已有 Get/Delete 等方法名，
// 不能直接实现接口）。
type evidenceStoreAdapter struct{ s *EventStore }

func (a evidenceStoreAdapter) Add(ctx context.Context, e Evidence) error {
	return a.s.AddEvidence(ctx, e)
}
func (a evidenceStoreAdapter) Get(ctx context.Context, id string) (Evidence, bool, error) {
	return a.s.GetEvidence(ctx, id)
}
func (a evidenceStoreAdapter) ListByGoal(ctx context.Context, gid GoalID) ([]Evidence, error) {
	return a.s.ListEvidenceByGoal(ctx, gid)
}
func (a evidenceStoreAdapter) MarkStaleByChangedFiles(ctx context.Context, changed []string) error {
	return a.s.MarkEvidenceStaleByChangedFiles(ctx, changed)
}

type checkpointStoreAdapter struct{ s *EventStore }

func (a checkpointStoreAdapter) Save(ctx context.Context, c GoalCheckpoint) error {
	return a.s.SaveCheckpoint(ctx, c)
}
func (a checkpointStoreAdapter) Load(ctx context.Context, gid GoalID) (GoalCheckpoint, bool, error) {
	return a.s.LoadCheckpoint(ctx, gid)
}
func (a checkpointStoreAdapter) Latest(ctx context.Context, gid GoalID) (GoalCheckpoint, bool, error) {
	return a.s.LatestCheckpoint(ctx, gid)
}
func (a checkpointStoreAdapter) Delete(ctx context.Context, gid GoalID) error {
	return a.s.DeleteCheckpoints(ctx, gid)
}

// EvidenceStore 返回事件化的 evidence 存储接口。
func (s *EventStore) EvidenceStore() EvidenceStore { return evidenceStoreAdapter{s: s} }

// CheckpointStore 返回事件化的 checkpoint 存储接口。
func (s *EventStore) CheckpointStore() CheckpointStore { return checkpointStoreAdapter{s: s} }

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
