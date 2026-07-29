package session

import (
	"bufio"
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
	"time"
)

const defaultSessionsDir = "sessions"

type Record struct {
	Seq       int64           `json:"seq"`
	Message   message.Message `json:"message"`
	CreatedAt time.Time       `json:"created_at"`
}

type JSONLStore struct {
	baseDir string
	nowFn   func() time.Time
}

var _ Store = (*JSONLStore)(nil)

// NewJSONLStore 在指定目录下创建存储。
// baseDir 是存放所有会话数据的根目录，通常传项目 cwd。
func NewJSONLStore(baseDir string) (*JSONLStore, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, fmt.Errorf("baseDir 不能为空")
	}
	return &JSONLStore{baseDir: baseDir, nowFn: time.Now}, nil
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

	parentHistory, err := s.LoadResolvedHistory(ctx, parentID)
	if err != nil {
		return Meta{}, fmt.Errorf("读取父会话历史失败: %w", err)
	}

	forkFromSeq := request.ForkFromSeq
	switch {
	case forkFromSeq == -1:
		forkFromSeq = int64(len(parentHistory))
	case forkFromSeq < -1:
		return Meta{}, fmt.Errorf("ForkFromSeq 不能小于 -1: %d", forkFromSeq)
	}
	if forkFromSeq > int64(len(parentHistory)) {
		return Meta{}, fmt.Errorf("ForkFromSeq 超出父会话长度: %d > %d", forkFromSeq, len(parentHistory))
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sessionID 不能为空")
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

	records, err := s.readOwnRecords(ctx, sessionID)
	if err != nil {
		return err
	}

	nextSeq := int64(0)
	if len(records) > 0 {
		nextSeq = records[len(records)-1].Seq + 1
	}

	if err := os.MkdirAll(s.sessionDir(sessionID), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.transcriptPath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	now := s.nowFn().UTC()
	for i := range msgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec := Record{Seq: nextSeq, Message: msgs[i], CreatedAt: now}
		if err := enc.Encode(rec); err != nil {
			return err
		}
		nextSeq++
	}
	return nil
}

func (s *JSONLStore) LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
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
	ownHistory := make([]message.Message, 0, len(ownRecords))
	for i := range ownRecords {
		ownHistory = append(ownHistory, ownRecords[i].Message)
	}

	if meta.ParentSessionID == "" {
		return ownHistory, nil
	}

	parentHistory, err := s.LoadResolvedHistory(ctx, meta.ParentSessionID)
	if err != nil {
		return nil, err
	}
	if meta.ForkFromSeq < 0 {
		return nil, fmt.Errorf("非法 fork_from_seq: %d", meta.ForkFromSeq)
	}
	if meta.ForkFromSeq > int64(len(parentHistory)) {
		return nil, fmt.Errorf("fork_from_seq 超过父会话长度: %d > %d", meta.ForkFromSeq, len(parentHistory))
	}

	resolved := make([]message.Message, 0, int(meta.ForkFromSeq)+len(ownHistory))
	resolved = append(resolved, parentHistory[:meta.ForkFromSeq]...)
	resolved = append(resolved, ownHistory...)
	return resolved, nil
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
	return os.WriteFile(s.metaPath(meta.SessionID), data, 0o644)
}

func (s *JSONLStore) readOwnRecords(ctx context.Context, sessionID string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path := s.transcriptPath(sessionID)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	records := make([]Record, 0, 64)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("解析 transcript 失败(%s): %w", path, err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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

// TranscriptPath 返回指定 session 的 transcript 文件路径。
func (s *JSONLStore) TranscriptPath(sessionID string) string {
	return s.transcriptPath(sessionID)
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
	FirstMessage   string // 第一条用户消息的前 80 个字符，可能为空
	TranscriptSize int64  // transcript 文件大小（字节）
}

// ListSessions 枚举所有已存储的会话，按创建时间倒序返回。
// 读取失败的会话会被跳过。
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

		summary := SessionSummary{
			SessionID: meta.SessionID,
			CreatedAt: meta.CreatedAt,
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

	// 按创建时间倒序排列（最新的在前）
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}
