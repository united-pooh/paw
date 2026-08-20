package es

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxPayloadBytes caps a single event payload. Oversize events are rejected
// before anything is written; the stream never contains partial events.
const MaxPayloadBytes = 8 << 20

var (
	ErrInvalidAggregateID = errors.New("es: invalid aggregate id")
	ErrSeqGap             = errors.New("es: event stream has a seq gap or duplicate")
	ErrOversizeEvent      = errors.New("es: event payload exceeds limit")
)

// Snapshot is a cached aggregate state at a stream position. It is derived
// data, never the source of truth: deleting it only costs a full replay.
type Snapshot struct {
	Seq   int64           `json:"seq"`
	State json.RawMessage `json:"state"`
}

// JSONLStore is an append-only per-aggregate event store. Each aggregate owns
// one <id>.events.jsonl stream; a <id>.snapshot.json holds the optional
// cached state. All files are written 0600.
//
// 并发模型：进程内由 mu 串行化；跨进程（多个 paw 实例指向同一工作区）由
// 每流一把 flock 串行化「读尾序 → 追加」临界区，尾序始终以磁盘文件为准——
// 旧实现的进程内 lastSeq 缓存在多实例交错追加时会产生重复/回退序号（seq
// gap），因此已移除。
type JSONLStore struct {
	baseDir string
	kind    string

	mu sync.Mutex
}

func NewJSONLStore(baseDir, kind string) (*JSONLStore, error) {
	if baseDir == "" {
		return nil, fmt.Errorf("es: baseDir is required")
	}
	if kind == "" {
		return nil, fmt.Errorf("es: kind is required")
	}
	return &JSONLStore{
		baseDir: baseDir,
		kind:    kind,
	}, nil
}

func (s *JSONLStore) streamPath(id string) string {
	return filepath.Join(s.baseDir, s.kind, id+".events.jsonl")
}

func (s *JSONLStore) lockPath(id string) string {
	return s.streamPath(id) + ".lock"
}

func (s *JSONLStore) snapshotPath(id string) string {
	return filepath.Join(s.baseDir, s.kind, id+".snapshot.json")
}

func validateAggregateID(id string) error {
	if id == "" || len(id) > 255 {
		return fmt.Errorf("%w: %q", ErrInvalidAggregateID, id)
	}
	if strings.ContainsAny(id, "/\\\x00") || id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("%w: %q", ErrInvalidAggregateID, id)
	}
	return nil
}

// Append assigns contiguous seqs starting after the stream tail, validates
// every envelope, then appends all lines and syncs once. Caller-supplied
// seq values are ignored and replaced by the assigned ones.
func (s *JSONLStore) Append(ctx context.Context, aggregateID string, events []Envelope) (firstSeq, lastSeq int64, err error) {
	if err := validateAggregateID(aggregateID); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, nil
	}
	for _, e := range events {
		if len(e.Payload) > MaxPayloadBytes {
			return 0, 0, fmt.Errorf("%w: %d bytes", ErrOversizeEvent, len(e.Payload))
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.streamPath(aggregateID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, 0, fmt.Errorf("es: create stream dir: %w", err)
	}

	// 跨进程互斥：尾序读取与追加必须在同一把流锁内完成，否则两个进程可能
	// 读到同一个尾序、写出重复 seq（seq gap 的典型成因）。
	lock, err := lockStreamFile(s.lockPath(aggregateID))
	if err != nil {
		return 0, 0, fmt.Errorf("es: lock stream: %w", err)
	}
	defer unlockStreamFile(lock)

	next, err := s.prepareAppendLocked(aggregateID)
	if err != nil {
		return 0, 0, err
	}
	assigned := make([]Envelope, len(events))
	for i, e := range events {
		e.Seq = next + int64(i) + 1
		if err := e.Validate(); err != nil {
			return 0, 0, err
		}
		assigned[i] = e
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, 0, fmt.Errorf("es: open stream: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	for i := range assigned {
		line, err := json.Marshal(assigned[i])
		if err != nil {
			return 0, 0, fmt.Errorf("es: encode event: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return 0, 0, fmt.Errorf("es: append stream: %w", err)
	}
	if err := f.Sync(); err != nil {
		return 0, 0, fmt.Errorf("es: sync stream: %w", err)
	}
	return assigned[0].Seq, assigned[len(assigned)-1].Seq, nil
}

// prepareAppendLocked 在持有流锁的前提下检查文件并返回当前尾序（无事件为 0）。
// 崩溃可能在行中间截断文件（无换行结尾）：物理截掉 torn 尾部，否则
// O_APPEND 会把新事件拼进损坏行；中部损坏报错。磁盘文件是唯一事实源——
// 不再使用进程内缓存序号，多进程交错追加也不会产生重复 seq。
func (s *JSONLStore) prepareAppendLocked(aggregateID string) (int64, error) {
	path := s.streamPath(aggregateID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("es: read stream for repair: %w", err)
	}
	var tail int64
	offset := 0
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += len(line) + 1
			continue
		}
		var env Envelope
		if err := json.Unmarshal(trimmed, &env); err != nil {
			if i == len(lines)-1 {
				// torn 尾部：截到该行起始
				if err := os.Truncate(path, int64(offset)); err != nil {
					return 0, fmt.Errorf("es: truncate torn tail: %w", err)
				}
				return tail, nil
			}
			return 0, fmt.Errorf("es: mid-stream corruption at offset %d: %w", offset, err)
		}
		tail = env.Seq
		offset += len(line) + 1
	}
	return tail, nil
}

// RepairSeqGaps 截掉首个 seq 违例（重复或回退）开始的尾部，保留合法前缀。
// 多进程双写者交错追加或崩溃交错会在流里留下重复/回退序号，Load 因此拒绝
// 整个流；截尾会丢弃尾部事件，但对「唯一事实源已不可读」的流而言是唯一
// 自愈路径。返回丢弃的非空行数；0 表示流合法。torn 尾部一并截除（与 Load
// 的容忍语义一致）；中部 JSON 损坏不是序号问题，仍然报错。
func (s *JSONLStore) RepairSeqGaps(ctx context.Context, aggregateID string) (int, error) {
	if err := validateAggregateID(aggregateID); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.streamPath(aggregateID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("es: read stream for seq repair: %w", err)
	}
	expect := int64(1)
	offset := 0
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += len(line) + 1
			continue
		}
		var env Envelope
		if err := json.Unmarshal(trimmed, &env); err != nil {
			if i == len(lines)-1 {
				if err := os.Truncate(path, int64(offset)); err != nil {
					return 0, fmt.Errorf("es: truncate torn tail: %w", err)
				}
				return 1, nil
			}
			return 0, fmt.Errorf("es: malformed event at line %d: %w", i+1, err)
		}
		if env.Seq != expect {
			dropped := 0
			for _, rest := range lines[i:] {
				if len(bytes.TrimSpace(rest)) != 0 {
					dropped++
				}
			}
			if err := os.Truncate(path, int64(offset)); err != nil {
				return 0, fmt.Errorf("es: truncate seq-violating tail: %w", err)
			}
			return dropped, nil
		}
		expect++
		offset += len(line) + 1
	}
	return 0, nil
}

// Load returns the intact event prefix of a stream. A malformed or truncated
// final line is dropped and reported via truncated=true; a malformed line in
// the middle, or any seq gap or duplicate, is an error.
func (s *JSONLStore) Load(ctx context.Context, aggregateID string) ([]Envelope, bool, error) {
	if err := validateAggregateID(aggregateID); err != nil {
		return nil, false, err
	}
	path := s.streamPath(aggregateID)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("es: open stream: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var out []Envelope
	expect := int64(1)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// A malformed final line is a torn write: truncate and keep the prefix.
			if !scanner.Scan() && scanner.Err() == nil {
				// This was the last line; nothing valid follows.
				return out, true, nil
			}
			return nil, false, fmt.Errorf("es: malformed event at line %d: %w", lineNo, err)
		}
		if err := env.Validate(); err != nil {
			return nil, false, fmt.Errorf("es: invalid event at line %d: %w", lineNo, err)
		}
		if env.Seq != expect {
			return nil, false, fmt.Errorf("%w: line %d has seq %d, want %d", ErrSeqGap, lineNo, env.Seq, expect)
		}
		out = append(out, env)
		expect++
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("es: read stream: %w", err)
	}
	return out, false, nil
}

// WriteSnapshot atomically writes the cached aggregate state at seq.
func (s *JSONLStore) WriteSnapshot(ctx context.Context, aggregateID string, seq int64, state json.RawMessage) error {
	if err := validateAggregateID(aggregateID); err != nil {
		return err
	}
	if seq < 1 {
		return fmt.Errorf("es: snapshot seq must be >= 1, got %d", seq)
	}
	if len(state) == 0 {
		return fmt.Errorf("es: snapshot state is required")
	}
	snap := Snapshot{Seq: seq, State: state}
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("es: encode snapshot: %w", err)
	}
	path := s.snapshotPath(aggregateID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("es: create snapshot dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("es: create snapshot temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("es: chmod snapshot temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("es: write snapshot temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("es: sync snapshot temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("es: close snapshot temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("es: install snapshot: %w", err)
	}
	return nil
}

// ReadSnapshot returns the cached state, or ok=false when none exists.
func (s *JSONLStore) ReadSnapshot(ctx context.Context, aggregateID string) (Snapshot, bool, error) {
	if err := validateAggregateID(aggregateID); err != nil {
		return Snapshot{}, false, err
	}
	data, err := os.ReadFile(s.snapshotPath(aggregateID))
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("es: read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, false, fmt.Errorf("es: decode snapshot: %w", err)
	}
	if snap.Seq < 1 || len(snap.State) == 0 {
		return Snapshot{}, false, fmt.Errorf("es: corrupt snapshot: %+v", snap)
	}
	return snap, true, nil
}

// StreamPath returns the on-disk path of an aggregate's event stream.
func (s *JSONLStore) StreamPath(aggregateID string) (string, error) {
	if err := validateAggregateID(aggregateID); err != nil {
		return "", err
	}
	return s.streamPath(aggregateID), nil
}

// Dir returns the store root directory; kind subdirectories live beneath it.
func (s *JSONLStore) Dir() string {
	return s.baseDir
}
