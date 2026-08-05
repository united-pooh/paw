package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"paw/internal/message"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultSessionsDir = "sessions"

type Record struct {
	Seq        int64               `json:"seq"`
	Kind       JournalKind         `json:"kind,omitempty"`
	TurnID     string              `json:"turn_id,omitempty"`
	CallIndex  *int                `json:"call_index,omitempty"`
	Message    message.Message     `json:"message"`
	ToolResult *message.ToolResult `json:"tool_result,omitempty"`
	Error      string              `json:"error,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
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
	baseDir string
	nowFn   func() time.Time
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

// NewJSONLStoreInCwd 以当前工作目录作为 baseDir 创建存储，
// 会话数据存放在 .paw/ 子目录下。
// 调用方不需要感知路径细节。
func NewJSONLStoreInCwd() (*JSONLStore, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("获取当前目录失败: %w", err)
	}
	baseDir := filepath.Join(cwd, ".paw")
	return NewJSONLStore(baseDir)
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
		Subagent:  request.Subagent,
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
		Subagent:        request.Subagent,
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

	data, err := os.ReadFile(s.metaPath(sessionID))
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

	_, err := os.Stat(s.metaPath(sessionID))
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
		if err := enc.Encode(records[i]); err != nil {
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
		s.journal[sessionID] = journalState{nextSeq: lastSeq + 1, size: fi.Size()}
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

	if policy == SyncPolicyAlways || turnBoundary || !ok || time.Since(last) >= interval {
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

// journalNextSeq 返回会话下一次 append 应使用的 sequence。优先使用内存缓存；
// 仅当 transcript 文件大小与上次观察一致时才命中缓存。任何大小不匹配
// （进程重启、外部进程写入、首次 append）都会触发一次完整重扫，保证持久化
// 语义与每次扫描时逐字节一致。
func (s *JSONLStore) journalNextSeq(ctx context.Context, sessionID string) (int64, error) {
	if cached, ok := s.journal[sessionID]; ok {
		if fi, err := os.Stat(s.transcriptPath(sessionID)); err == nil && fi.Size() == cached.size {
			return cached.nextSeq, nil
		}
		// 文件不存在或大小变化：继续走重扫路径。
	}
	existing, err := s.readOwnRecords(ctx, sessionID)
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
	s.journal[sessionID] = journalState{nextSeq: nextSeq, size: size}
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
		Messages:      append([]message.Message(nil), messages...),
		ActiveHistory: append([]message.Message(nil), activeMessages...),
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
		active := append([]message.Message(nil), activeMessages[:prefixLen]...)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path := s.transcriptPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]Record, 0, 64)
	lines := bytes.Split(data, []byte{'\n'})
	for lineIndex, rawLine := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			if lineIndex == len(lines)-1 && !bytes.HasSuffix(data, []byte{'\n'}) {
				// The final line may have been torn during a process crash. All
				// preceding newline-terminated records are still durable.
				break
			}
			return nil, fmt.Errorf("解析 transcript 失败(%s): %w", path, err)
		}
		if rec.Kind == "" {
			rec.Kind = JournalMessage
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *JSONLStore) sessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, defaultSessionsDir, sessionID)
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

// TranscriptPath 返回指定 session 的 transcript 文件路径。
func (s *JSONLStore) TranscriptPath(sessionID string) string {
	return s.transcriptPath(sessionID)
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
	f, err := os.Open(s.turnMetadataPath(sessionID))
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
	indexPath := s.keyIndexPath(key)

	// 如果索引文件存在，说明该 key 已经绑定了一个 session。
	existingID, err := os.ReadFile(indexPath)
	if err == nil {
		sessionID := strings.TrimSpace(string(existingID))
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读取 session 索引失败: %w", err)
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
	if err := os.Chtimes(s.metaPath(sessionID), now, now); err != nil {
		return fmt.Errorf("更新 session 最近使用时间失败: %w", err)
	}
	return nil
}

// ListSessions 枚举所有可恢复的前台会话，按最近使用时间倒序返回。
// subagent 会话及读取失败的会话会被跳过。
func (s *JSONLStore) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sessionsDir := filepath.Join(s.baseDir, defaultSessionsDir)
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var summaries []SessionSummary
	for _, entry := range entries {
		if !entry.IsDir() {
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
		if meta.Subagent || s.isLegacySubagentSession(sessionID) {
			continue
		}

		lastUsedAt := meta.CreatedAt
		if info, statErr := os.Stat(s.metaPath(sessionID)); statErr == nil && info.ModTime().After(lastUsedAt) {
			lastUsedAt = info.ModTime()
		}
		summary := SessionSummary{
			SessionID:  meta.SessionID,
			CreatedAt:  meta.CreatedAt,
			LastUsedAt: lastUsedAt,
		}

		// 尝试读取第一条用户消息和 transcript 文件大小
		if fi, err := os.Stat(s.transcriptPath(sessionID)); err == nil {
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

	// LRU 顺序：最近使用的会话在前；时间相同时用 session ID 保证稳定顺序。
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].LastUsedAt.Equal(summaries[j].LastUsedAt) {
			return summaries[i].SessionID < summaries[j].SessionID
		}
		return summaries[i].LastUsedAt.After(summaries[j].LastUsedAt)
	})

	return summaries, nil
}

func (s *JSONLStore) isLegacySubagentSession(sessionID string) bool {
	_, err := os.Stat(filepath.Join(s.baseDir, "tasks", sessionID, "meta.json"))
	return err == nil
}
