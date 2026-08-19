package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"paw/internal/es"
)

// AppendEnvelopes appends runtime or domain control events to the same
// transcript ledger used by conversation records. Actor events are always
// synced because they contain inbox/outbox and human-decision facts.
func (s *JSONLStore) AppendEnvelopes(ctx context.Context, sessionID string, events []es.Envelope) (firstSeq, lastSeq int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, nil
	}
	if strings.TrimSpace(sessionID) == "" {
		return 0, 0, fmt.Errorf("sessionID 不能为空")
	}

	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := s.ensureWritableSession(sessionID); err != nil {
		return 0, 0, err
	}
	exists, err := s.Exists(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}
	if !exists {
		if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
			return 0, 0, err
		}
	} else if _, err := s.GetMeta(ctx, sessionID); err != nil {
		return 0, 0, err
	}
	if err := s.repairTornTail(sessionID); err != nil {
		return 0, 0, err
	}
	nextSeq, err := s.journalNextSeq(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}
	assigned := make([]es.Envelope, len(events))
	for i := range events {
		event := events[i]
		event.Seq = nextSeq + int64(i)
		if err := event.Validate(); err != nil {
			return 0, 0, err
		}
		assigned[i] = event
	}
	if err := os.MkdirAll(s.sessionDir(sessionID), 0o755); err != nil {
		return 0, 0, err
	}
	f, err := os.OpenFile(s.transcriptPath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range assigned {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if err := enc.Encode(assigned[i]); err != nil {
			return 0, 0, err
		}
	}
	if err := s.syncFile(f); err != nil {
		return 0, 0, fmt.Errorf("同步 transcript 失败: %w", err)
	}
	firstSeq, lastSeq = assigned[0].Seq, assigned[len(assigned)-1].Seq
	if fi, statErr := f.Stat(); statErr == nil {
		s.setJournalState(sessionID, journalState{nextSeq: lastSeq + 1, size: fi.Size()})
	}
	now := s.nowFn().UTC()
	if err := os.Chtimes(s.metaPath(sessionID), now, now); err != nil {
		return 0, 0, fmt.Errorf("更新 session 最近使用时间失败: %w", err)
	}
	return firstSeq, lastSeq, nil
}

// LoadEnvelopes returns every intact event, converting legacy Records to
// domain envelopes while preserving their original sequence numbers.
func (s *JSONLStore) LoadEnvelopes(ctx context.Context, sessionID string) ([]es.Envelope, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	path := s.readTranscriptPath(sessionID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	lines := bytes.Split(data, []byte{'\n'})
	out := make([]es.Envelope, 0, len(lines))
	for index, raw := range lines {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var env es.Envelope
		if isEnvelopeLine(line) {
			if err := json.Unmarshal(line, &env); err != nil || env.Validate() != nil {
				if index == len(lines)-1 && !bytes.HasSuffix(data, []byte{'\n'}) {
					return out, true, nil
				}
				if err == nil {
					err = env.Validate()
				}
				return nil, false, fmt.Errorf("解析 transcript 失败(%s): %w", path, err)
			}
		} else {
			var record Record
			if err := json.Unmarshal(line, &record); err != nil {
				if index == len(lines)-1 && !bytes.HasSuffix(data, []byte{'\n'}) {
					return out, true, nil
				}
				return nil, false, fmt.Errorf("解析 transcript 失败(%s): %w", path, err)
			}
			if record.Kind == "" {
				record.Kind = JournalMessage
			}
			converted, err := recordToEnvelope(record)
			if err != nil {
				return nil, false, fmt.Errorf("解析 transcript 失败(%s): %w", path, err)
			}
			converted.Kind = es.KindDomain
			env = converted
		}
		out = append(out, env)
	}
	return out, false, nil
}

const sessionActorSnapshotFile = "session-actor.snapshot.json"

func (s *JSONLStore) WriteActorSnapshot(ctx context.Context, sessionID string, seq int64, state json.RawMessage) error {
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
	if err := os.MkdirAll(s.sessionDir(sessionID), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(es.Snapshot{Seq: seq, State: append(json.RawMessage(nil), state...)})
	if err != nil {
		return err
	}
	path := filepath.Join(s.sessionDir(sessionID), sessionActorSnapshotFile)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-actor-snapshot-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmpPath, path)
}

func (s *JSONLStore) ReadActorSnapshot(ctx context.Context, sessionID string) (es.Snapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return es.Snapshot{}, false, err
	}
	path := filepath.Join(s.resolveSessionDir(sessionID), sessionActorSnapshotFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return es.Snapshot{}, false, nil
	}
	if err != nil {
		return es.Snapshot{}, false, err
	}
	var snapshot es.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return es.Snapshot{}, false, err
	}
	return snapshot, true, nil
}
