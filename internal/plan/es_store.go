package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"paw/internal/es"
)

// DocStore 是 plan 文档存储接口。FileStore 与 EventStore 均实现。
type DocStore interface {
	Dir() string
	NextID(ctx context.Context, title string) (PlanID, error)
	Create(ctx context.Context, doc PlanDoc) error
	Get(ctx context.Context, id PlanID) (PlanDoc, bool, error)
	Update(ctx context.Context, doc PlanDoc) error
	MarkApproved(ctx context.Context, id PlanID) (PlanDoc, error)
	List(ctx context.Context) ([]PlanDoc, error)
}

// SessionStatusRecorder 是可选能力：将会话状态变化记录为事件。
// FileStore 不实现（现状会话状态仅内存）；EventStore 实现（审计 + 可恢复）。
type SessionStatusRecorder interface {
	RecordSessionStatus(ctx context.Context, id PlanID, status SessionStatus, reason PauseReason) error
}

// EventStore 是 plan 聚合的事件溯源存储。事件流为唯一事实来源；
// 嵌入的 FileStore 作为投影写入端，把最新文档渲染为用户可见的 .md 文件
// （front matter 格式不变，保持 git-friendly 与外部工具兼容）。
type EventStore struct {
	*FileStore
	events   *es.JSONLStore
	registry *es.Registry
	loader   *es.Loader
}

var _ DocStore = (*EventStore)(nil)
var _ SessionStatusRecorder = (*EventStore)(nil)

// NewEventStore 构造 plan 事件溯源存储。docDir 是 .md 投影文件目录；
// eventsDir 是事件库根目录（plan 流位于 <eventsDir>/plans/）。
func NewEventStore(docDir, eventsDir string) (*EventStore, error) {
	if docDir == "" || eventsDir == "" {
		return nil, fmt.Errorf("plan: docDir and eventsDir are required")
	}
	events, err := es.NewJSONLStore(eventsDir, "plans")
	if err != nil {
		return nil, err
	}
	registry := es.NewRegistry()
	if err := RegisterEvents(registry); err != nil {
		return nil, err
	}
	return &EventStore{
		FileStore: NewFileStore(docDir),
		events:    events,
		registry:  registry,
		loader:    &es.Loader{Store: events, Registry: registry},
	}, nil
}

func (s *EventStore) streamExists(id PlanID) (bool, error) {
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

func (s *EventStore) loadState(ctx context.Context, id PlanID) (esState, bool, error) {
	exists, err := s.streamExists(id)
	if err != nil {
		return esState{}, false, err
	}
	if !exists {
		return esState{}, false, nil
	}
	st := &esState{}
	if _, err := s.loader.Load(ctx, string(id), st); err != nil {
		return esState{}, false, err
	}
	return *st, true, nil
}

// Create 追加 plan.created 事件并将文档投影为 .md 文件。
func (s *EventStore) Create(ctx context.Context, doc PlanDoc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if doc.ID == "" {
		return errors.New("plan id is empty")
	}
	if doc.Status == "" {
		doc.Status = PlanDraft
	}
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = s.now()
	}
	doc.UpdatedAt = doc.CreatedAt
	doc.Path = path
	if err := s.appendCreated(ctx, EventCreated, doc); err != nil {
		return err
	}
	return s.projectedCreate(doc)
}

// ImportBaseline 迁移导入：把已有文档写为 plan.baseline 事件并投影文件。
// 仅用于一次性迁移；重复导入同一 id 报错。
func (s *EventStore) ImportBaseline(ctx context.Context, doc PlanDoc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if doc.ID == "" {
		return errors.New("plan id is empty")
	}
	if doc.Status == "" {
		doc.Status = PlanDraft
	}
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	doc.Path = path
	if err := s.appendCreated(ctx, EventBaseline, doc); err != nil {
		return err
	}
	return s.projectedCreate(doc)
}

func (s *EventStore) appendCreated(ctx context.Context, typ string, doc PlanDoc) error {
	exists, err := s.streamExists(doc.ID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("plan %q already exists", doc.ID)
	}
	// 旧文件 front matter 不含 created_at：导入时用投影文件 mtime 兜底。
	if doc.CreatedAt.IsZero() {
		if fi, statErr := os.Stat(doc.Path); statErr == nil {
			doc.CreatedAt = fi.ModTime()
		} else {
			doc.CreatedAt = s.now()
		}
		doc.UpdatedAt = doc.CreatedAt
	}
	raw, err := json.Marshal(createdPayload{
		PlanID:    string(doc.ID),
		Title:     doc.Title,
		Path:      doc.Path,
		Content:   doc.Content,
		Status:    doc.Status,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("plan: encode created payload: %w", err)
	}
	env := es.Envelope{Type: typ, OccurredAt: doc.CreatedAt, SchemaVersion: 1, Payload: raw}
	if _, _, err := s.events.Append(ctx, string(doc.ID), []es.Envelope{env}); err != nil {
		return fmt.Errorf("plan: append %s: %w", typ, err)
	}
	return nil
}

// projectedCreate 把文档写入投影文件（目录创建 + 0644）。
func (s *EventStore) projectedCreate(doc PlanDoc) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(doc.Path, []byte(encodeDoc(doc)), 0o644)
}

// Update diff 文档内容变化产出 plan.doc_updated 事件，再投影覆盖文件。
// 文档可能由 agent 直接写文件、首次经 Finalize 持久化：事件流不存在但
// 投影文件存在时自动基线导入（plan.baseline），保证外部写入兼容。
func (s *EventStore) Update(ctx context.Context, doc PlanDoc) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if doc.ID == "" {
		return errors.New("plan id is empty")
	}
	current, ok, err := s.loadState(ctx, doc.ID)
	if err != nil {
		return err
	}
	if !ok {
		fileDoc, fileOK, ferr := s.FileStore.Get(ctx, doc.ID)
		if ferr != nil {
			return ferr
		}
		if !fileOK {
			return errPlanNotFound
		}
		if err := s.appendCreated(ctx, EventBaseline, fileDoc); err != nil {
			return err
		}
		current, _, err = s.loadState(ctx, doc.ID)
		if err != nil {
			return err
		}
	}
	if doc.CreatedAt != current.Doc.CreatedAt {
		return fmt.Errorf("plan: created_at is immutable")
	}
	now := doc.UpdatedAt
	if now.IsZero() {
		now = s.now()
	}
	var events []es.Envelope
	if doc.Title != current.Doc.Title || doc.Content != current.Doc.Content {
		raw, err := json.Marshal(docUpdatedPayload{
			Title:     doc.Title,
			Path:      current.Doc.Path,
			Content:   doc.Content,
			UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("plan: encode doc payload: %w", err)
		}
		events = append(events, es.Envelope{Type: EventDocUpdated, OccurredAt: now, SchemaVersion: 1, Payload: raw})
	}
	if doc.Status != current.Doc.Status {
		if doc.Status == PlanApproved {
			raw, err := json.Marshal(statusChangedPayload{Status: SessionApproved, UpdatedAt: now})
			if err != nil {
				return fmt.Errorf("plan: encode status payload: %w", err)
			}
			events = append(events, es.Envelope{Type: EventStatusChanged, OccurredAt: now, SchemaVersion: 1, Payload: raw})
		} else {
			return fmt.Errorf("plan: cannot move status back from %s to %s", current.Doc.Status, doc.Status)
		}
	}
	if len(events) == 0 {
		return nil
	}
	if err := s.appendEvents(ctx, doc.ID, events); err != nil {
		return err
	}
	projected := current.Doc
	projected.Title = doc.Title
	projected.Content = doc.Content
	projected.Status = doc.Status
	projected.UpdatedAt = now
	return s.projectedUpdate(projected)
}

// projectedUpdate 覆盖投影文件（与 FileStore.Update 一致：保留 front matter id）。
func (s *EventStore) projectedUpdate(doc PlanDoc) error {
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return errPlanNotFound
	}
	doc.UpdatedAt = s.now()
	doc.Path = path
	return os.WriteFile(path, []byte(encodeDoc(doc)), 0o644)
}

// MarkApproved 追加 status_changed(approved) 事件并投影 approved 文件。
func (s *EventStore) MarkApproved(ctx context.Context, id PlanID) (PlanDoc, error) {
	st, ok, err := s.loadState(ctx, id)
	if err != nil {
		return PlanDoc{}, err
	}
	if !ok {
		return PlanDoc{}, errPlanNotFound
	}
	if st.SessionStatus == SessionApproved {
		return st.Doc, nil
	}
	now := s.now()
	raw, err := json.Marshal(statusChangedPayload{Status: SessionApproved, UpdatedAt: now})
	if err != nil {
		return PlanDoc{}, fmt.Errorf("plan: encode status payload: %w", err)
	}
	env := es.Envelope{Type: EventStatusChanged, OccurredAt: now, SchemaVersion: 1, Payload: raw}
	if err := s.appendEvents(ctx, id, []es.Envelope{env}); err != nil {
		return PlanDoc{}, err
	}
	st.Doc.Status = PlanApproved
	st.Doc.UpdatedAt = now
	if err := s.projectedUpdate(st.Doc); err != nil {
		return PlanDoc{}, err
	}
	return st.Doc, nil
}

// RecordSessionStatus 记录会话生命周期状态变化（审计 + 可恢复）。
// 不修改投影文件（文档内容与会话状态解耦）。
func (s *EventStore) RecordSessionStatus(ctx context.Context, id PlanID, status SessionStatus, reason PauseReason) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if _, ok, err := s.loadState(ctx, id); err != nil {
		return err
	} else if !ok {
		return errPlanNotFound
	}
	now := s.now()
	raw, err := json.Marshal(statusChangedPayload{Status: status, Reason: reason, UpdatedAt: now})
	if err != nil {
		return fmt.Errorf("plan: encode status payload: %w", err)
	}
	env := es.Envelope{Type: EventStatusChanged, OccurredAt: now, SchemaVersion: 1, Payload: raw}
	if err := s.appendEvents(ctx, id, []es.Envelope{env}); err != nil {
		return err
	}
	return nil
}

// Get 从事件流重建文档（文件是投影，可能被外部编辑，不作为事实来源）。
func (s *EventStore) Get(ctx context.Context, id PlanID) (PlanDoc, bool, error) {
	if err := contextErr(ctx); err != nil {
		return PlanDoc{}, false, err
	}
	st, ok, err := s.loadState(ctx, id)
	if err != nil {
		return PlanDoc{}, false, err
	}
	if !ok {
		return PlanDoc{}, false, nil
	}
	return st.Doc, true, nil
}

// List 从事件流枚举所有文档，按创建时间排序。
func (s *EventStore) List(ctx context.Context) ([]PlanDoc, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.events.Dir(), "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]PlanDoc, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".events.jsonl") {
			continue
		}
		id := PlanID(strings.TrimSuffix(entry.Name(), ".events.jsonl"))
		doc, ok, err := s.Get(ctx, id)
		if err != nil || !ok {
			continue
		}
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *EventStore) appendEvents(ctx context.Context, id PlanID, events []es.Envelope) error {
	if _, _, err := s.events.Append(ctx, string(id), events); err != nil {
		return fmt.Errorf("plan: append events: %w", err)
	}
	return nil
}
