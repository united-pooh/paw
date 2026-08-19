package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"paw/internal/es"
	"paw/internal/message"
	"paw/internal/todo"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultSessionsDir = "sessions"

type Record struct {
	Seq          int64               `json:"seq"`
	Kind         JournalKind         `json:"kind,omitempty"`
	TurnID       string              `json:"turn_id,omitempty"`
	CallIndex    *int                `json:"call_index,omitempty"`
	Message      message.Message     `json:"message"`
	ToolResult   *message.ToolResult `json:"tool_result,omitempty"`
	Error        string              `json:"error,omitempty"`
	TodoSnapshot *todo.Snapshot      `json:"todo_snapshot,omitempty"`
	StateEvent   *StateEventRecord   `json:"state_event,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
}

// journalState 缓存单个 session 的追加序列状态，避免每次 append 都重新扫描
// 整份 transcript.jsonl。nextSeq 是下一次 append 应使用的 sequence；size 是
// 上次扫描时 transcript 文件的字节大小。进程重启后（或外部进程写入时）size
// 不匹配会触发重新扫描，因此持久化语义保持不变。
type journalState struct {
	nextSeq int64
	size    int64
}

// SyncPolicy 控制 transcript.jsonl 追加后的 f.Sync() 调用频率。
type SyncPolicy int

const (
	// SyncPolicyAlways 每次追加后都同步文件（默认，可靠性优先）。这是唯一
	// 保证任何一次 append 返回后记录已落盘的策略。
	SyncPolicyAlways SyncPolicy = iota
	// SyncPolicyInterval 按时间间隔批量同步；含 turn 完成/失败边界的批次仍
	// 立即同步，因此已完成 turn 的持久性不弱于 always 模式，只有高频中间
	// 增量（assistant delta、tool result）允许短暂停留在 page cache。
	SyncPolicyInterval
)

type JSONLStore struct {
	// baseDir 是全局项目布局根目录（~/.paw/projects/<项目名>），
	// 会话数据位于 <baseDir>/sessions/<sessionID>/。
	baseDir string
	// legacyBaseDir 是旧工作区布局根目录（<cwd>/.paw），只读 fallback：
	// 未迁移的旧会话从这里读取；新数据一律写全局（baseDir）。
	legacyBaseDir string
	nowFn         func() time.Time
	// mu 只保护 session state 元数据（journal 缓存、sessionLocks 与 lastSync），
	// 不再覆盖文件 I/O、JSON 编码和 f.Sync()。不同 session 的追加可以并行，
	// 同一 session 的追加由 sessionLock 串行化，保证 sequence 连续。
	mu           sync.Mutex
	journal      map[string]journalState // sessionID -> 缓存状态（受 mu 保护）
	sessionLocks map[string]*sync.Mutex  // sessionID -> 每 session 独立锁（受 mu 保护）
	// syncPolicy/syncInterval 只能在并发使用前配置（构造或 SetSyncPolicy）。
	syncPolicy   SyncPolicy
	syncInterval time.Duration
	lastSync     map[string]time.Time // sessionID -> 上次成功 sync 时刻（受 mu 保护）
	syncFile     func(*os.File) error // 测试可替换；默认 (*os.File).Sync
}

var _ Store = (*JSONLStore)(nil)
var _ TurnMetadataStore = (*JSONLStore)(nil)
var _ TurnJournal = (*JSONLStore)(nil)
var _ PartialAssistantJournal = (*JSONLStore)(nil)

// NewJSONLStore 在指定目录下创建存储。
// baseDir 是存放所有会话数据的根目录，通常传项目 cwd。
func NewJSONLStore(baseDir string) (*JSONLStore, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, fmt.Errorf("baseDir 不能为空")
	}
	return &JSONLStore{
		baseDir:      baseDir,
		nowFn:        time.Now,
		journal:      make(map[string]journalState),
		sessionLocks: make(map[string]*sync.Mutex),
		lastSync:     make(map[string]time.Time),
		syncPolicy:   SyncPolicyAlways,
		syncInterval: 5 * time.Second,
		syncFile:     func(f *os.File) error { return f.Sync() },
	}, nil
}

// SetSyncPolicy 切换持久化同步策略。仅在并发使用前调用（通常在构造后立即设置）。
// interval 仅对 SyncPolicyInterval 生效，默认 5 秒。
func (s *JSONLStore) SetSyncPolicy(policy SyncPolicy, interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncPolicy = policy
	if interval > 0 {
		s.syncInterval = interval
	}
}

// sessionLock 返回 session 粒度的互斥锁。不同 session 并发追加时互不阻塞；
// 同一 session 的追加（sequence 分配、文件写入、sync）保持串行。
// 调用方持有返回的锁期间不得再获取 s.mu（锁顺序：sessionLock -> mu）。
func (s *JSONLStore) sessionLock(sessionID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.sessionLocks[sessionID]; ok {
		return l
	}
	l := &sync.Mutex{}
	s.sessionLocks[sessionID] = l
	return l
}

// NewJSONLStoreInCwd 创建全局项目布局的会话存储：
// 新数据写入 ~/.paw/projects/<项目名>/sessions/，旧工作区
// <cwd>/.paw/sessions/ 作为只读 fallback（未迁移会话懒迁移到全局）。
func NewJSONLStoreInCwd() (*JSONLStore, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("获取当前目录失败: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户主目录失败: %w", err)
	}
	projectDir := filepath.Join(home, ".paw", "projects", projectNameFor(cwd))
	store, err := NewJSONLStore(projectDir)
	if err != nil {
		return nil, err
	}
	store.legacyBaseDir = filepath.Join(cwd, ".paw")
	return store, nil
}

// projectNameFor 生成稳定的项目目录名：cwd basename（slug 化）+ 路径哈希
// 前 8 位，避免同名目录冲突。同一 cwd 每次启动生成相同名称。
func projectNameFor(cwd string) string {
	base := filepath.Base(cwd)
	var slug strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			slug.WriteRune(r)
		default:
			slug.WriteByte('-')
		}
	}
	name := strings.Trim(slug.String(), "-")
	if name == "" {
		name = "project"
	}
	sum := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("%s-%x", name, sum[:4])
}

func (s *JSONLStore) CreateRoot(ctx context.Context, request CreateRootRequest) (Meta, error) {
	if err := ctx.Err(); err != nil {
		return Meta{}, err
	}

	sessionID := request.SessionID

	exists, err := s.Exists(ctx, sessionID)
	if err != nil {
		return Meta{}, err
	}
	if exists {
		return Meta{}, fmt.Errorf("session 已存在: %s", sessionID)
	}

	meta := Meta{
		SessionID: sessionID,
		CreatedAt: s.nowFn().UTC(),
		Task:      request.Task,
	}
	if err := s.writeMeta(ctx, meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func (s *JSONLStore) Fork(ctx context.Context, request ForkRequest) (Meta, error) {
	if err := ctx.Err(); err != nil {
		return Meta{}, err
	}

	parentID := strings.TrimSpace(request.ParentSessionID)
	if parentID == "" {
		return Meta{}, fmt.Errorf("ParentSessionID 不能为空")
	}
	if _, err := s.GetMeta(ctx, parentID); err != nil {
		return Meta{}, fmt.Errorf("读取父会话元数据失败: %w", err)
	}

	parentRecords, err := s.LoadResolvedRecords(ctx, parentID)
	if err != nil {
		return Meta{}, fmt.Errorf("读取父会话历史失败: %w", err)
	}

	forkFromSeq := request.ForkFromSeq
	switch {
	case forkFromSeq == -1:
		forkFromSeq = int64(len(parentRecords))
	case forkFromSeq < -1:
		return Meta{}, fmt.Errorf("ForkFromSeq 不能小于 -1: %d", forkFromSeq)
	}
	if forkFromSeq > int64(len(parentRecords)) {
		return Meta{}, fmt.Errorf("ForkFromSeq 超出父会话长度: %d > %d", forkFromSeq, len(parentRecords))
	}

	sessionID := request.SessionID

	exists, err := s.Exists(ctx, sessionID)
	if err != nil {
		return Meta{}, err
	}
	if exists {
		return Meta{}, fmt.Errorf("session 已存在: %s", sessionID)
	}

	meta := Meta{
		SessionID:       sessionID,
		ParentSessionID: parentID,
		ForkFromSeq:     forkFromSeq,
		CreatedAt:       s.nowFn().UTC(),
		Task:            request.Task,
	}
	if err := s.writeMeta(ctx, meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

func (s *JSONLStore) GetMeta(ctx context.Context, sessionID string) (Meta, error) {
	if err := ctx.Err(); err != nil {
		return Meta{}, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return Meta{}, fmt.Errorf("sessionID 不能为空")
	}

	data, err := os.ReadFile(s.readMetaPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Meta{}, fmt.Errorf("session 不存在: %s", sessionID)
		}
		return Meta{}, err
	}

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("解析 meta.json 失败: %w", err)
	}
	if meta.SessionID == "" {
		meta.SessionID = sessionID
	}
	return meta, nil
}

func (s *JSONLStore) Exists(ctx context.Context, sessionID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return false, fmt.Errorf("sessionID 不能为空")
	}

	_, err := os.Stat(s.readMetaPath(sessionID))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (s *JSONLStore) Append(ctx context.Context, sessionID string, msgs ...message.Message) error {
	_, _, err := s.AppendWithSequences(ctx, sessionID, msgs...)
	return err
}

// AppendWithSequences appends messages and returns the first and last
// transcript sequence assigned to this append operation.
func (s *JSONLStore) AppendWithSequences(ctx context.Context, sessionID string, msgs ...message.Message) (firstSeq, lastSeq int64, err error) {
	records := make([]Record, 0, len(msgs))
	for _, msg := range msgs {
		records = append(records, Record{Kind: JournalMessage, Message: msg})
	}
	return s.appendRecords(ctx, sessionID, records)
}

func (s *JSONLStore) appendRecords(ctx context.Context, sessionID string, records []Record) (firstSeq, lastSeq int64, err error) {
	firstSeq, lastSeq = -1, -1
	if err := ctx.Err(); err != nil {
		return firstSeq, lastSeq, err
	}
	if len(records) == 0 {
		return firstSeq, lastSeq, nil
	}
	if strings.TrimSpace(sessionID) == "" {
		return firstSeq, lastSeq, fmt.Errorf("sessionID 不能为空")
	}

	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	// 懒迁移：legacy 会话首次写入前复制到全局，避免新旧数据分裂。
	if _, err := s.ensureWritableSession(sessionID); err != nil {
		return firstSeq, lastSeq, err
	}

	exists, err := s.Exists(ctx, sessionID)
	if err != nil {
		return firstSeq, lastSeq, err
	}
	if !exists {
		if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
			return firstSeq, lastSeq, err
		}
	} else if _, err := s.GetMeta(ctx, sessionID); err != nil {
		return firstSeq, lastSeq, err
	}

	nextSeq, err := s.journalNextSeq(ctx, sessionID)
	if err != nil {
		return firstSeq, lastSeq, err
	}
	// 崩溃可能留下无换行结尾的 torn 行：物理截掉，否则 O_APPEND 会把新
	// 记录拼进损坏行（该行将永远无法解析，后续记录全部丢失）。
	if err := s.repairTornTail(sessionID); err != nil {
		return -1, -1, err
	}
	firstSeq = nextSeq
	lastSeq = nextSeq + int64(len(records)) - 1

	if err := os.MkdirAll(s.sessionDir(sessionID), 0o755); err != nil {
		return -1, -1, err
	}
	f, err := os.OpenFile(s.transcriptPath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return -1, -1, err
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	now := s.nowFn().UTC()
	for i := range records {
		if err := ctx.Err(); err != nil {
			return -1, -1, err
		}
		records[i].Seq = nextSeq
		records[i].CreatedAt = now
		env, err := recordToEnvelope(records[i])
		if err != nil {
			return -1, -1, err
		}
		if err := enc.Encode(env); err != nil {
			return -1, -1, err
		}
		nextSeq++
	}
	// turnBoundary 为 true 时，即使处于 interval 策略也必须立即同步：
	// 已完成/失败的 turn 是用户可感知的持久化边界，不能停留在 page cache。
	turnBoundary := false
	for i := range records {
		if records[i].Kind == JournalTurnCompleted || records[i].Kind == JournalTurnFailed {
			turnBoundary = true
			break
		}
	}
	if err := s.syncAfterAppend(f, sessionID, turnBoundary); err != nil {
		return -1, -1, fmt.Errorf("同步 transcript 失败: %w", err)
	}
	// 用追加后的真实文件大小更新内存缓存，使下一次 append 无需重扫。
	if fi, statErr := f.Stat(); statErr == nil {
		s.setJournalState(sessionID, journalState{nextSeq: lastSeq + 1, size: fi.Size()})
	}
	if err := os.Chtimes(s.metaPath(sessionID), now, now); err != nil {
		return -1, -1, fmt.Errorf("更新 session 最近使用时间失败: %w", err)
	}
	return firstSeq, lastSeq, nil
}

// syncAfterAppend 按策略同步文件。always 策略每次都同步；interval 策略在
// 距离上次同步超过 syncInterval 或遇到 turn 完成/失败边界时同步。调用方
// 必须已持有该 session 的 sessionLock，保证同一 session 的 lastSync 读取与
// 更新不与其他追加交错。
func (s *JSONLStore) syncAfterAppend(f *os.File, sessionID string, turnBoundary bool) error {
	s.mu.Lock()
	policy := s.syncPolicy
	interval := s.syncInterval
	last, ok := s.lastSync[sessionID]
	s.mu.Unlock()

	// 间隔判定用 nowFn 而非 time.Since：默认两者等价（真实时钟），
	// 但测试可注入受控时钟，使 interval 到期判定确定性可复现。
	if policy == SyncPolicyAlways || turnBoundary || !ok || s.nowFn().Sub(last) >= interval {
		if err := s.syncFile(f); err != nil {
			return err
		}
		now := s.nowFn()
		s.mu.Lock()
		s.lastSync[sessionID] = now
		s.mu.Unlock()
	}
	return nil
}

func (s *JSONLStore) setJournalState(sessionID string, state journalState) {
	s.mu.Lock()
	s.journal[sessionID] = state
	s.mu.Unlock()
}

func (s *JSONLStore) getJournalState(sessionID string) (journalState, bool) {
	s.mu.Lock()
	state, ok := s.journal[sessionID]
	s.mu.Unlock()
	return state, ok
}

// 仅当 transcript 文件大小与上次观察一致时才命中缓存。任何大小不匹配
// （进程重启、外部进程写入、首次 append）都会触发一次完整重扫，保证持久化
// 语义与每次扫描时逐字节一致。
func (s *JSONLStore) journalNextSeq(ctx context.Context, sessionID string) (int64, error) {
	if cached, ok := s.getJournalState(sessionID); ok {
		if fi, err := os.Stat(s.transcriptPath(sessionID)); err == nil && fi.Size() == cached.size {
			return cached.nextSeq, nil
		}
		// 文件不存在或大小变化：继续走重扫路径。
	}
	existing, _, err := s.LoadEnvelopes(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	nextSeq := int64(0)
	if len(existing) > 0 {
		nextSeq = existing[len(existing)-1].Seq + 1
	}
	size := int64(0)
	if fi, err := os.Stat(s.transcriptPath(sessionID)); err == nil {
		size = fi.Size()
	}
	s.setJournalState(sessionID, journalState{nextSeq: nextSeq, size: size})
	return nextSeq, nil
}

func (s *JSONLStore) BeginTurn(ctx context.Context, sessionID, turnID string, messages ...message.Message) error {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return err
	}
	records := []Record{journalTurn(JournalTurnStarted, turnID)}
	for _, msg := range messages {
		records = append(records, Record{Kind: JournalMessage, TurnID: turnID, Message: msg})
	}
	_, _, err := s.appendRecords(ctx, sessionID, records)
	return err
}

func (s *JSONLStore) AppendAssistant(ctx context.Context, sessionID, turnID string, msg message.Message) error {
	_, err := s.AppendAssistantWithSequence(ctx, sessionID, turnID, msg)
	return err
}

// AppendAssistantWithSequence appends an incremental assistant journal record
// and returns its transcript sequence for turn metadata association.
func (s *JSONLStore) AppendAssistantWithSequence(ctx context.Context, sessionID, turnID string, msg message.Message) (int64, error) {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return -1, err
	}
	_, lastSeq, err := s.appendRecords(ctx, sessionID, []Record{{
		Kind:    JournalAssistant,
		TurnID:  strings.TrimSpace(turnID),
		Message: msg,
	}})
	return lastSeq, err
}

func (s *JSONLStore) AppendPartialAssistant(ctx context.Context, sessionID, turnID string, msg message.Message) error {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return err
	}
	msg.Role = message.RoleAssistant
	_, _, err := s.appendRecords(ctx, sessionID, []Record{{
		Kind:    JournalAssistantPartial,
		TurnID:  strings.TrimSpace(turnID),
		Message: msg,
	}})
	return err
}

func (s *JSONLStore) AppendToolResult(ctx context.Context, sessionID, turnID string, callIndex int, result message.ToolResult) error {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return err
	}
	if callIndex < 0 {
		return fmt.Errorf("callIndex 不能小于 0: %d", callIndex)
	}
	resultCopy := result
	index := callIndex
	_, _, err := s.appendRecords(ctx, sessionID, []Record{{
		Kind:       JournalToolResult,
		TurnID:     strings.TrimSpace(turnID),
		CallIndex:  &index,
		Message:    message.Message{Role: message.RoleUser, ToolResult: &resultCopy},
		ToolResult: &resultCopy,
	}})
	return err
}

func (s *JSONLStore) CompleteTurn(ctx context.Context, sessionID, turnID string) error {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return err
	}
	_, _, err := s.appendRecords(ctx, sessionID, []Record{journalTurn(JournalTurnCompleted, turnID)})
	return err
}

func (s *JSONLStore) FailTurn(ctx context.Context, sessionID, turnID string, turnErr error) error {
	if err := validateTurnArgs(sessionID, turnID); err != nil {
		return err
	}
	record := journalTurn(JournalTurnFailed, turnID)
	record.Error = journalError(turnErr)
	_, _, err := s.appendRecords(ctx, sessionID, []Record{record})
	return err
}

// AppendTodoSnapshot 将一次 todo 快照更新作为事件追加到 session 流。
// 返回分配的事件 seq。todo 记录不出现在消息投影中，但参与 seq 连续性。
func (s *JSONLStore) AppendTodoSnapshot(ctx context.Context, sessionID string, snapshot todo.Snapshot) (int64, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return -1, fmt.Errorf("sessionID 不能为空")
	}
	snap := snapshot.Clone()
	_, lastSeq, err := s.appendRecords(ctx, sessionID, []Record{{
		Kind:         JournalTodoSnapshot,
		TodoSnapshot: &snap,
	}})
	return lastSeq, err
}

// AppendStateEvent 追加状态文件更新事件（memory/ariadne 审计留痕，
// best-effort 语义与 AppendTodoSnapshot 一致）。
func (s *JSONLStore) AppendStateEvent(ctx context.Context, sessionID string, kind StateEventKind, summary string) (int64, error) {
	if kind != StateEventMemory && kind != StateEventAriadne && kind != StateEventCompacted {
		return 0, fmt.Errorf("unknown state event kind %q", kind)
	}
	if len(summary) > 600 {
		summary = string([]rune(summary)[:600])
	}
	now := s.nowFn().UTC()
	rec := Record{
		Kind:      JournalMemoryUpdated,
		CreatedAt: now,
		StateEvent: &StateEventRecord{
			Kind:      kind,
			Summary:   summary,
			UpdatedAt: now,
		},
	}
	switch kind {
	case StateEventAriadne:
		rec.Kind = JournalAriadneUpdated
	case StateEventCompacted:
		rec.Kind = JournalStateCompacted
	}
	// 新 kind 不匹配任何投影 switch 的 case（无 default），不影响消息派生。
	_, lastSeq, err := s.appendRecords(ctx, sessionID, []Record{rec})
	return lastSeq, err
}

func (s *JSONLStore) LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	records, err := s.LoadResolvedRecords(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	history := make([]message.Message, 0, len(records))
	for _, record := range records {
		if record.Kind == JournalAssistantPartial {
			continue
		}
		history = append(history, record.Message)
	}
	return history, nil
}

// LoadResolvedRecords returns message records visible in a session. Journal
// control records are projected back into assistant/tool-result messages so
// existing callers and fork sequence semantics remain message-based.
func (s *JSONLStore) LoadResolvedRecords(ctx context.Context, sessionID string) ([]Record, error) {
	raw, err := s.loadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return projectJournalRecords(raw), nil
}

func (s *JSONLStore) loadResolvedJournalRecords(ctx context.Context, sessionID string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	meta, err := s.GetMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	ownRecords, err := s.readOwnRecords(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if meta.ParentSessionID == "" {
		return ownRecords, nil
	}

	parentRecords, err := s.LoadResolvedJournalRecords(ctx, meta.ParentSessionID)
	if err != nil {
		return nil, err
	}
	parentRecords = projectJournalRecords(parentRecords)
	if meta.ForkFromSeq < 0 {
		return nil, fmt.Errorf("非法 fork_from_seq: %d", meta.ForkFromSeq)
	}
	if meta.ForkFromSeq > int64(len(parentRecords)) {
		return nil, fmt.Errorf("fork_from_seq 超过父会话长度: %d > %d", meta.ForkFromSeq, len(parentRecords))
	}

	resolved := make([]Record, 0, int(meta.ForkFromSeq)+len(ownRecords))
	resolved = append(resolved, parentRecords[:meta.ForkFromSeq]...)
	resolved = append(resolved, ownRecords...)
	return resolved, nil
}

// LoadResolvedJournalRecords returns the raw append-only records for internal
// recovery analysis. It intentionally is not part of the public Store API.
func (s *JSONLStore) LoadResolvedJournalRecords(ctx context.Context, sessionID string) ([]Record, error) {
	return s.loadResolvedJournalRecords(ctx, sessionID)
}

func (s *JSONLStore) LoadLatestTodoSnapshot(ctx context.Context, sessionID string) (todo.Snapshot, bool, error) {
	records, err := s.loadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		return todo.Snapshot{}, false, err
	}
	toolNames := make(map[string]string)
	var latest todo.Snapshot
	hasLatest := false
	for _, record := range records {
		for _, call := range toolCallsFromRecord(record) {
			if id := strings.TrimSpace(call.ID); id != "" {
				toolNames[id] = strings.TrimSpace(call.Name)
			}
		}
		if record.Kind == JournalTodoSnapshot && record.TodoSnapshot != nil {
			if snapshot, ok := validTodoSnapshot(*record.TodoSnapshot); ok {
				latest = snapshot
				hasLatest = true
			}
		}
		// Old sessions persisted update_todo only as an assistant tool call plus
		// its JSON result. Keep this fallback read-only so new snapshots remain
		// the canonical format while pre-event transcripts remain resumable.
		for _, result := range toolResultsFromRecord(record) {
			if toolNames[strings.TrimSpace(result.ToolUseID)] != "update_todo" || result.IsError {
				continue
			}
			if snapshot, ok := decodeLegacyTodoResult(result.Content); ok {
				latest = snapshot
				hasLatest = true
			}
		}
	}
	if hasLatest {
		return latest, true, nil
	}
	return todo.Snapshot{}, false, nil
}

func validTodoSnapshot(snapshot todo.Snapshot) (todo.Snapshot, bool) {
	normalized, err := todo.ValidateSnapshot(snapshot)
	if err != nil {
		return todo.Snapshot{}, false
	}
	return normalized.Clone(), true
}

func decodeLegacyTodoResult(content string) (todo.Snapshot, bool) {
	var result todo.UpdateResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || !result.Accepted {
		return todo.Snapshot{}, false
	}
	return validTodoSnapshot(result.Snapshot)
}

func toolResultsFromRecord(record Record) []message.ToolResult {
	if record.ToolResult != nil {
		return []message.ToolResult{*record.ToolResult}
	}
	if record.Message.ToolResult != nil {
		return []message.ToolResult{*record.Message.ToolResult}
	}
	return append([]message.ToolResult(nil), record.Message.ToolResults...)
}

func projectJournalRecords(records []Record) []Record {
	projected := make([]Record, 0, len(records))
	for _, record := range records {
		switch record.Kind {
		case "", JournalMessage, JournalAssistant:
			if record.Kind == JournalAssistant {
				record.Kind = JournalMessage
			}
			projected = append(projected, record)
		case JournalAssistantPartial:
			projected = append(projected, record)
		case JournalToolResult:
			if record.ToolResult == nil {
				continue
			}
			result := *record.ToolResult
			record.Kind = JournalMessage
			record.Message = message.Message{Role: message.RoleUser, ToolResult: &result}
			projected = append(projected, record)
		}
	}
	return projected
}

// LoadSnapshot loads both the display transcript and a model-safe history.
// The latter excludes an orphaned multi-tool call group from the latest
// unfinished turn while retaining all complete tool call/result pairs.
func (s *JSONLStore) LoadSnapshot(ctx context.Context, sessionID string) (SessionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SessionSnapshot{}, err
	}
	if _, err := s.GetMeta(ctx, sessionID); err != nil {
		return SessionSnapshot{}, err
	}

	displayRecords, err := s.LoadResolvedRecords(ctx, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	messages := messagesFromRecords(displayRecords)
	activeMessages := modelHistoryFromRecords(displayRecords)
	ownRecords, err := s.readOwnRecords(ctx, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}

	turnID, status, failure := latestTurnState(ownRecords)
	snapshot := SessionSnapshot{
		Messages:      message.CloneMessages(messages),
		ActiveHistory: message.CloneMessages(activeMessages),
	}
	if turnID == "" || status == JournalTurnCompleted {
		return snapshot, nil
	}

	recovery := &RecoveryState{
		TurnID:      turnID,
		Error:       failure,
		Interrupted: status != JournalTurnFailed,
	}
	if strings.TrimSpace(recovery.Error) == "" {
		recovery.Error = "previous turn ended before completion"
	}
	entries := entriesForTurn(ownRecords, turnID)
	safeOwn := safeTurnHistory(entries, recovery)
	if len(entries) <= len(activeMessages) {
		prefixLen := len(activeMessages) - len(entries)
		active := message.CloneMessages(activeMessages[:prefixLen])
		active = append(active, safeOwn...)
		snapshot.ActiveHistory = active
	}
	snapshot.Recovery = copyRecovery(recovery)
	return snapshot, nil
}

func messagesFromRecords(records []Record) []message.Message {
	messages := make([]message.Message, 0, len(records))
	for _, record := range records {
		messages = append(messages, record.Message)
	}
	return messages
}

func modelHistoryFromRecords(records []Record) []message.Message {
	history := make([]message.Message, 0, len(records))
	for _, record := range records {
		if record.Kind == JournalAssistantPartial {
			continue
		}
		history = append(history, record.Message)
	}
	return history
}

func latestTurnState(records []Record) (turnID string, status JournalKind, failure string) {
	for _, record := range records {
		switch record.Kind {
		case JournalTurnStarted:
			turnID = record.TurnID
			status = JournalTurnStarted
			failure = ""
		case JournalTurnCompleted:
			if record.TurnID == turnID {
				status = JournalTurnCompleted
			}
		case JournalTurnFailed:
			if record.TurnID == turnID {
				status = JournalTurnFailed
				failure = record.Error
			}
		}
	}
	return turnID, status, failure
}

func (s *JSONLStore) writeMeta(ctx context.Context, meta Meta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(meta.SessionID) == "" {
		return fmt.Errorf("SessionID 不能为空")
	}
	if err := os.MkdirAll(s.sessionDir(meta.SessionID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := s.metaPath(meta.SessionID)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if !meta.CreatedAt.IsZero() {
		createdAt := meta.CreatedAt.UTC()
		if err := os.Chtimes(path, createdAt, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *JSONLStore) readOwnRecords(ctx context.Context, sessionID string) ([]Record, error) {
	envelopes, _, err := s.LoadEnvelopes(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(envelopes))
	for _, env := range envelopes {
		if env.Kind == es.KindRuntime {
			continue
		}
		record, err := envelopeToRecord(env)
		if err != nil {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// repairTornTail 物理截掉崩溃留下的未完成尾部（无换行结尾且最后一行解析
// 失败）。完整但无换行的最后一行保留；中部损坏报错。
func (s *JSONLStore) repairTornTail(sessionID string) error {
	path := s.readTranscriptPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取 transcript 失败(%s): %w", path, err)
	}
	if len(data) == 0 || bytes.HasSuffix(data, []byte{'\n'}) {
		return nil
	}
	lines := bytes.Split(data, []byte{'\n'})
	offset := 0
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += len(line) + 1
			continue
		}
		parsed := false
		if isEnvelopeLine(trimmed) {
			var env es.Envelope
			if err := json.Unmarshal(trimmed, &env); err == nil && env.Validate() == nil {
				parsed = true
			}
		} else {
			var rec Record
			parsed = json.Unmarshal(trimmed, &rec) == nil
		}
		if !parsed {
			if i == len(lines)-1 {
				if err := os.Truncate(path, int64(offset)); err != nil {
					return fmt.Errorf("截断 transcript 失败(%s): %w", path, err)
				}
				return nil
			}
			return fmt.Errorf("解析 transcript 失败(%s): 中部损坏 offset %d", path, offset)
		}
		offset += len(line) + 1
	}
	return nil
}

// LoadRecentTurns 返回最近 n 个轮次的清洗消息（模式 B 恢复用，验证实验
// v2 结论）：工具调用省略 input 参数（保留工具名/ID/结果截断），文本
// 消息原样保留。清洗后恢复上下文小而信息密度高。
func (s *JSONLStore) LoadRecentTurns(ctx context.Context, sessionID string, n int) ([]message.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	records, err := s.readOwnRecords(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	// 按出现顺序取最后 n 个不同 turn。
	order := make([]string, 0, 8)
	for _, r := range records {
		if r.TurnID == "" {
			continue
		}
		seen := false
		for _, tid := range order {
			if tid == r.TurnID {
				seen = true
				break
			}
		}
		if !seen {
			order = append(order, r.TurnID)
		}
	}
	start := len(order) - n
	if start < 0 {
		start = 0
	}
	keep := make(map[string]bool, n)
	for _, tid := range order[start:] {
		keep[tid] = true
	}

	msgs := make([]message.Message, 0, 32)
	for _, r := range records {
		if r.TurnID != "" && !keep[r.TurnID] {
			continue
		}
		switch r.Kind {
		case "", JournalMessage, JournalAssistant:
			msgs = append(msgs, cleanMessageForRecovery(r.Message))
		case JournalToolResult:
			if r.ToolResult != nil {
				tr := *r.ToolResult
				tr.Content = truncateForRecovery(tr.Content)
				msgs = append(msgs, message.Message{Role: message.RoleUser, ToolResult: &tr})
			}
		}
	}
	return msgs, nil
}

// recoveryResultCap 是恢复上下文中单条工具结果的最大字符数（rune）。
const recoveryResultCap = 400

// cleanMessageForRecovery 清洗单条消息：附件细节（Parts）省略、工具调用
// input 置空、工具结果内容截断。
func cleanMessageForRecovery(msg message.Message) message.Message {
	msg.Parts = nil
	if msg.ToolUse != nil {
		c := *msg.ToolUse
		c.Input = json.RawMessage("{}")
		msg.ToolUse = &c
	}
	for i := range msg.ToolUses {
		msg.ToolUses[i].Input = json.RawMessage("{}")
	}
	if msg.ToolResult != nil {
		tr := *msg.ToolResult
		tr.Content = truncateForRecovery(tr.Content)
		msg.ToolResult = &tr
	}
	for i := range msg.ToolResults {
		msg.ToolResults[i].Content = truncateForRecovery(msg.ToolResults[i].Content)
	}
	return msg
}

func truncateForRecovery(s string) string {
	runes := []rune(s)
	if len(runes) <= recoveryResultCap {
		return s
	}
	return string(runes[:recoveryResultCap]) + "…[truncated]"
}

func (s *JSONLStore) sessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, defaultSessionsDir, sessionID)
}

func (s *JSONLStore) legacySessionDir(sessionID string) string {
	if s.legacyBaseDir == "" {
		return ""
	}
	return filepath.Join(s.legacyBaseDir, defaultSessionsDir, sessionID)
}

// resolveSessionDir 返回会话实际所在目录（读路径）：全局优先，
// 不存在则回退 legacy 工作区布局；两者都没有时返回全局（写路径将创建）。
func (s *JSONLStore) resolveSessionDir(sessionID string) string {
	global := s.sessionDir(sessionID)
	if _, err := os.Stat(filepath.Join(global, "meta.json")); err == nil {
		return global
	}
	if legacy := s.legacySessionDir(sessionID); legacy != "" {
		if _, err := os.Stat(filepath.Join(legacy, "meta.json")); err == nil {
			return legacy
		}
	}
	return global
}

// ensureWritableSession 保证会话可写（懒迁移）：全局存在则不动；
// 仅 legacy 存在则把 legacy 会话目录复制到全局（meta + transcript +
// turns 侧车），使后续写入落在全局、旧数据不丢。两者都不存在返回 false，
// 由调用方走新建流程。
func (s *JSONLStore) ensureWritableSession(sessionID string) (bool, error) {
	global := s.sessionDir(sessionID)
	if _, err := os.Stat(filepath.Join(global, "meta.json")); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	legacy := s.legacySessionDir(sessionID)
	if legacy == "" {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(legacy, "meta.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	// 复制到临时目录再 rename，避免半成品会话目录。
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		return false, err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(global), ".migrate-*")
	if err != nil {
		return false, fmt.Errorf("创建迁移临时目录失败: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	for _, name := range []string{"meta.json", "transcript.jsonl", "turns.jsonl", sessionActorSnapshotFile} {
		src := filepath.Join(legacy, name)
		data, err := os.ReadFile(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("读取 legacy %s 失败: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), data, 0o644); err != nil {
			return false, fmt.Errorf("复制 %s 失败: %w", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(tmpDir, global); err != nil {
		return false, fmt.Errorf("迁移会话 %s 失败: %w", sessionID, err)
	}
	return true, nil
}

func (s *JSONLStore) metaPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "meta.json")
}

func (s *JSONLStore) transcriptPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "transcript.jsonl")
}

func (s *JSONLStore) turnMetadataPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "turns.jsonl")
}

// readMetaPath / readTranscriptPath / readTurnMetadataPath 是读路径版本：
// 通过 resolveSessionDir 支持 legacy fallback。
func (s *JSONLStore) readMetaPath(sessionID string) string {
	return filepath.Join(s.resolveSessionDir(sessionID), "meta.json")
}

func (s *JSONLStore) readTranscriptPath(sessionID string) string {
	return filepath.Join(s.resolveSessionDir(sessionID), "transcript.jsonl")
}

func (s *JSONLStore) readTurnMetadataPath(sessionID string) string {
	return filepath.Join(s.resolveSessionDir(sessionID), "turns.jsonl")
}

// TranscriptPath 返回指定 session 的 transcript 文件路径。
func (s *JSONLStore) TranscriptPath(sessionID string) string {
	return s.transcriptPath(sessionID)
}

// Dir 返回存储根目录（sessions/ 等子目录位于其下）。
func (s *JSONLStore) Dir() string {
	return s.baseDir
}

// TurnMetadataPath returns the sidecar path used for persisted turn timing.
func (s *JSONLStore) TurnMetadataPath(sessionID string) string {
	return s.turnMetadataPath(sessionID)
}

// AppendTurnMetadata appends display-only timing data without changing the
// message transcript that is sent back to the model.
func (s *JSONLStore) AppendTurnMetadata(ctx context.Context, sessionID string, metadata TurnMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sessionID 不能为空")
	}
	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := s.ensureWritableSession(sessionID); err != nil {
		return err
	}
	exists, err := s.Exists(ctx, sessionID)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
			return err
		}
	} else if _, err := s.GetMeta(ctx, sessionID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessionDir(sessionID), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.turnMetadataPath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := json.NewEncoder(f).Encode(metadata); err != nil {
		return err
	}
	return f.Sync()
}

// LoadTurnMetadata loads valid sidecar records in append order. Missing or
// malformed lines are ignored so a damaged display sidecar cannot hide a
// usable message transcript.
func (s *JSONLStore) LoadTurnMetadata(ctx context.Context, sessionID string) ([]TurnMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("sessionID 不能为空")
	}
	f, err := os.Open(s.readTurnMetadataPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	metadata := make([]TurnMetadata, 0, 16)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item TurnMetadata
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		metadata = append(metadata, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return metadata, nil
}

// GenerateSessionID 生成一个随机的 session ID（16 字节 hex 字符串）。
func GenerateSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// OpenOrCreate 按 key（通常传 cwd）查找已有会话并返回其 sessionID；
// 若不存在则创建一个新会话。
// 这样调用方完全不需要关心 session ID 的生成和查找。
func (s *JSONLStore) OpenOrCreate(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("key 不能为空")
	}

	// 用 key 的哈希作为索引文件名，避免 key 中包含路径分隔符等特殊字符。
	// 全局索引 miss 时回退 legacy 索引（旧工作区会话恢复入口）。
	indexPath := s.keyIndexPath(key)
	existingID, err := s.readKeyIndex(key)
	if err != nil {
		return "", err
	}
	if existingID != "" {
		sessionID := existingID
		// 再确认 session 目录本身也存在（防止文件被手动删除的情况）。
		exists, existsErr := s.Exists(ctx, sessionID)
		if existsErr != nil {
			return "", existsErr
		}
		if exists {
			return sessionID, nil
		}
		// session 目录不存在了，索引失效，重新创建。
	}

	// 生成新 session ID 并创建会话。
	sessionID, err := GenerateSessionID()
	if err != nil {
		return "", err
	}
	if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		return "", err
	}

	// 将 key -> sessionID 的映射写入索引文件。
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(indexPath, []byte(sessionID), 0o644); err != nil {
		return "", fmt.Errorf("写入 session 索引失败: %w", err)
	}

	return sessionID, nil
}

// keyIndexPath 用 key 的内容（去掉非法字符后）生成一个索引文件路径。
// 存放在 baseDir/index/ 目录下。
func (s *JSONLStore) keyIndexPath(key string) string {
	// 将路径分隔符和特殊字符替换为 _，让 key 可以安全作为文件名。
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(key)
	if len(safe) > 200 {
		safe = safe[len(safe)-200:]
	}
	return filepath.Join(s.baseDir, "index", safe)
}

// readKeyIndex 读取 key 的会话索引：全局优先，legacy 索引 fallback。
// 返回 "" 表示无绑定。
func (s *JSONLStore) readKeyIndex(key string) (string, error) {
	indexPath := s.keyIndexPath(key)
	if data, err := os.ReadFile(indexPath); err == nil {
		return strings.TrimSpace(string(data)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读取 session 索引失败: %w", err)
	}
	if s.legacyBaseDir != "" {
		legacy := filepath.Join(s.legacyBaseDir, "index", filepath.Base(indexPath))
		if data, err := os.ReadFile(legacy); err == nil {
			return strings.TrimSpace(string(data)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("读取 legacy session 索引失败: %w", err)
		}
	}
	return "", nil
}

// SessionSummary 是 ListSessions 返回的会话摘要信息。
type SessionSummary struct {
	SessionID      string
	CreatedAt      time.Time
	LastUsedAt     time.Time
	FirstMessage   string // 第一条用户消息的前 80 个字符，可能为空
	TranscriptSize int64  // transcript 文件大小（字节）
}

// TouchSession marks a foreground session as recently used. The metadata file's
// modification time is deliberately used as a lightweight access marker so the
// persisted metadata schema remains backward compatible.
func (s *JSONLStore) TouchSession(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.GetMeta(ctx, sessionID); err != nil {
		return err
	}
	now := s.nowFn().UTC()
	if err := os.Chtimes(s.readMetaPath(sessionID), now, now); err != nil {
		return fmt.Errorf("更新 session 最近使用时间失败: %w", err)
	}
	return nil
}

// ListSessions 枚举所有可恢复的前台会话，按最近使用时间倒序返回。
// 全局布局与 legacy 工作区布局合并（同 ID 取全局）；task 会话及
// 读取失败的会话会被跳过。
func (s *JSONLStore) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var summaries []SessionSummary
	for _, dir := range s.sessionRoots() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sessionID := entry.Name()
			meta, err := s.GetMeta(ctx, sessionID)
			if err != nil {
				continue // 跳过读取失败的会话
			}
			if meta.Task || s.isLegacytaskSession(sessionID) {
				continue
			}
			seen[sessionID] = true

			lastUsedAt := meta.CreatedAt
			if info, statErr := os.Stat(s.readMetaPath(sessionID)); statErr == nil && info.ModTime().After(lastUsedAt) {
				lastUsedAt = info.ModTime()
			}
			summary := SessionSummary{
				SessionID:  meta.SessionID,
				CreatedAt:  meta.CreatedAt,
				LastUsedAt: lastUsedAt,
			}

			// 尝试读取第一条用户消息和 transcript 文件大小
			if fi, err := os.Stat(s.readTranscriptPath(sessionID)); err == nil {
				summary.TranscriptSize = fi.Size()
			}
			records, err := s.readOwnRecords(ctx, sessionID)
			if err == nil {
				for _, rec := range records {
					if rec.Message.Role == "user" && rec.Message.Content != "" {
						msg := rec.Message.Content
						if len(msg) > 80 {
							msg = msg[:80]
						}
						summary.FirstMessage = msg
						break
					}
				}
			}

			summaries = append(summaries, summary)
		}
	}

	// LRU 顺序：最近使用的会话在前；时间相同时用 session ID 保证稳定顺序。
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].LastUsedAt.Equal(summaries[j].LastUsedAt) {
			return summaries[i].SessionID < summaries[j].SessionID
		}
		return summaries[i].LastUsedAt.After(summaries[j].LastUsedAt)
	})

	return summaries, nil
}

// sessionRoots 返回会话枚举根目录列表（全局优先，legacy 其次）。
func (s *JSONLStore) sessionRoots() []string {
	roots := []string{filepath.Join(s.baseDir, defaultSessionsDir)}
	if s.legacyBaseDir != "" {
		roots = append(roots, filepath.Join(s.legacyBaseDir, defaultSessionsDir))
	}
	return roots
}

func (s *JSONLStore) isLegacytaskSession(sessionID string) bool {
	for _, root := range []string{s.baseDir, s.legacyBaseDir} {
		if root == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "tasks", sessionID, "meta.json")); err == nil {
			return true
		}
	}
	return false
}

// TranscriptHit 是 search_transcript 的单条命中（D11）。
type TranscriptHit struct {
	Seq     int64     `json:"seq"`
	TurnID  string    `json:"turn_id,omitempty"`
	Role    string    `json:"role,omitempty"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// SearchTranscript 在 transcript 中按关键字检索（大小写不敏感，顺序扫描）。
// 匹配范围：user/assistant 文本、工具调用名、工具结果内容。
// 返回命中摘要 + 可检索记录总数（D11 显式范围约定）。
func (s *JSONLStore) SearchTranscript(ctx context.Context, sessionID, query string, limit int) ([]TranscriptHit, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	records, err := s.readOwnRecords(ctx, sessionID)
	if err != nil {
		return nil, 0, err
	}
	searched := len(records)
	hits := make([]TranscriptHit, 0, limit)
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		var role string
		var text string
		switch r.Kind {
		case "", JournalMessage, JournalAssistant, JournalAssistantPartial:
			role = string(r.Message.Role)
			text = searchableMessageText(r.Message)
			for _, tu := range toolCallsFromRecord(r) {
				text += " " + tu.Name
			}
		case JournalToolResult:
			role = "tool_result"
			if r.ToolResult != nil {
				text = r.ToolResult.Content
			}
		case JournalTodoSnapshot:
			continue
		}
		if strings.Contains(strings.ToLower(text), q) {
			hits = append(hits, TranscriptHit{
				Seq:     r.Seq,
				TurnID:  r.TurnID,
				Role:    role,
				Content: truncateForRecovery(text),
				Time:    r.CreatedAt,
			})
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits, searched, nil
}

func toolCallsFromRecord(r Record) []message.ToolCall {
	var out []message.ToolCall
	if r.Message.ToolUse != nil {
		out = append(out, *r.Message.ToolUse)
	}
	out = append(out, r.Message.ToolUses...)
	return out
}
