package plan

import (
	"bytes"
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
	// beforeProjectedWrite 仅测试用：原子投影写前回调（锁内），模拟并发外部编辑。
	beforeProjectedWrite func()
}

var _ DocStore = (*EventStore)(nil)
var _ SessionStatusRecorder = (*EventStore)(nil)

// ErrExternalEditConflict 表示 plan 投影文件被用户外部编辑，而调用方基于
// 过期状态发起 Update。重新 Get（读磁盘最新）后再 Update 即可采纳编辑。
var ErrExternalEditConflict = errors.New("plan: external edit conflict")

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
// 先写投影、后追加事件：投影写失败不会留下已提交事件；事件追加失败时
// 投影领先，下一次 Update 会按外部编辑采纳自愈。
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
	if _, statErr := os.Stat(path); statErr == nil {
		return fmt.Errorf("plan %q already exists", doc.ID)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	exists, err := s.streamExists(doc.ID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("plan %q already exists", doc.ID)
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = s.now()
	}
	doc.UpdatedAt = doc.CreatedAt
	doc.Path = path
	if err := s.projectedCreate(doc); err != nil {
		return err
	}
	return s.appendCreated(ctx, EventCreated, doc)
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
	exists, err := s.streamExists(doc.ID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("plan %q already exists", doc.ID)
	}
	if err := s.projectedCreate(doc); err != nil {
		return err
	}
	return s.appendCreated(ctx, EventBaseline, doc)
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
//
// 外部编辑冲突：磁盘投影与事件流不一致且调用方不是基于磁盘最新内容时
// 返回 ErrExternalEditConflict；基于磁盘最新（FileStore.Get 后 Update）
// 则采纳用户编辑进事件流。投影写入使用原子替换 + 字节 CAS + 跨进程锁，
// 先投影后事件：投影冲突或缺失时事件未提交，不产生部分提交。
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
	} else {
		diskDoc, diskOK, derr := s.FileStore.Get(ctx, doc.ID)
		if derr != nil {
			return derr
		}
		if !diskOK {
			// 投影缺失但事件流存在：拒绝更新，避免事件提交后投影写入失败
			// 造成事件流与文件分裂。
			return ErrExternalEditConflict
		}
		if !planDocContentEqual(diskDoc, current.Doc) && !planDocContentEqual(doc, diskDoc) {
			return ErrExternalEditConflict
		}
	}
	// created_at 是事件流事实，文件 front matter 不含它：磁盘来源的 doc
	// （decodeDoc）恒为零值，一律继承流值；非零必须与流一致，防止绕过
	// 不可变校验。
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = current.Doc.CreatedAt
	}
	if doc.CreatedAt != current.Doc.CreatedAt {
		return fmt.Errorf("plan: created_at is immutable")
	}
	now := doc.UpdatedAt
	if now.IsZero() {
		now = s.now()
	}
	var events []es.Envelope
	if doc.Title != current.Doc.Title || !planContentEqual(doc.Content, current.Doc.Content) {
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
	projected := current.Doc
	projected.Title = doc.Title
	projected.Content = doc.Content
	projected.Status = doc.Status
	projected.UpdatedAt = now
	// 先投影后事件：投影 CAS 失败（并发外部编辑）时事件未提交；事件追加
	// 失败时投影领先，下一次 Update 按外部编辑采纳自愈。
	if err := s.writeProjectedAtomic(projected); err != nil {
		return err
	}
	return s.appendEvents(ctx, doc.ID, events)
}

// planContentEqual 比较文档内容，容忍 encodeDoc 强制文件尾 \n 引入的差异
// （事件流内 Content 原样保存；文件往返会补/去一个尾部换行）。用于抑制
// 无实质变化的 no-op doc_updated 事件。
func planContentEqual(a, b string) bool {
	return a == b || a+"\n" == b || a == b+"\n"
}

// planDocContentEqual 比较投影文件内容级字段（用户可见部分）。CreatedAt/
// UpdatedAt/Path 等元数据不参与比较（文件 front matter 不含 created_at）。
func planDocContentEqual(a, b PlanDoc) bool {
	if a.Title != b.Title || a.Status != b.Status {
		return false
	}
	return planContentEqual(a.Content, b.Content)
}

// writeProjectedAtomic 原子写投影：temp + fsync + rename，rename 前重读
// 磁盘字节并与锁外读取的基线比较，不一致说明并发外部编辑，返回
// ErrExternalEditConflict 且不覆盖。跨进程写由 <doc>.md.lock 独占锁串行化
// （跟随 internal/config CAS writer 模式）；基线在锁外读取，保证先取得锁
// 的并发写入者被检测为冲突而不是被覆盖。
func (s *EventStore) writeProjectedAtomic(doc PlanDoc) error {
	path, err := s.pathFor(doc.ID)
	if err != nil {
		return err
	}
	baseline, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errPlanNotFound
		}
		return err
	}
	release, err := acquirePlanProjectionLock(path)
	if err != nil {
		return err
	}
	defer release()
	if s.beforeProjectedWrite != nil {
		s.beforeProjectedWrite()
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, baseline) {
		return ErrExternalEditConflict
	}
	doc.Path = path
	doc.UpdatedAt = s.now()
	tmp, err := os.CreateTemp(s.dir, ".plan-proj-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write([]byte(encodeDoc(doc))); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// MarkApproved 追加 status_changed(approved) 事件并投影 approved 文件。
// 与 Update 一致：先投影后事件，投影写失败不产生部分提交。
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
	st.Doc.Status = PlanApproved
	st.Doc.UpdatedAt = now
	if err := s.writeProjectedAtomic(st.Doc); err != nil {
		return PlanDoc{}, err
	}
	if err := s.appendEvents(ctx, id, []es.Envelope{env}); err != nil {
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
