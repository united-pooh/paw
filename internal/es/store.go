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
type JSONLStore struct {
	baseDir string
	kind    string

	mu      sync.Mutex
	lastSeq map[string]int64
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
		lastSeq: make(map[string]int64),
	}, nil
}

func (s *JSONLStore) streamPath(id string) string {
	return filepath.Join(s.baseDir, s.kind, id+".events.jsonl")
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

	next := s.nextSeqLocked(ctx, aggregateID)
	assigned := make([]Envelope, len(events))
	for i, e := range events {
		e.Seq = next + int64(i) + 1
		if err := e.Validate(); err != nil {
			return 0, 0, err
		}
		assigned[i] = e
	}

	path := s.streamPath(aggregateID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, 0, fmt.Errorf("es: create stream dir: %w", err)
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
	s.lastSeq[aggregateID] = assigned[len(assigned)-1].Seq
	return assigned[0].Seq, assigned[len(assigned)-1].Seq, nil
}

func (s *JSONLStore) nextSeqLocked(ctx context.Context, id string) int64 {
	if n, ok := s.lastSeq[id]; ok {
		return n
	}
	last, _, err := s.Load(ctx, id)
	if err != nil {
		return 0
	}
	if len(last) == 0 {
		return 0
	}
	n := last[len(last)-1].Seq
	s.lastSeq[id] = n
	return n
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
