package es

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *JSONLStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewJSONLStore(filepath.Join(dir, "events"), "goals")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func env(seq int64, typ, payload string) Envelope {
	return Envelope{
		Seq:           seq,
		Type:          typ,
		OccurredAt:    time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		SchemaVersion: 1,
		Payload:       json.RawMessage(payload),
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	evs := []Envelope{
		env(1, "goal.created", `{"goal_id":"g1"}`),
		env(2, "goal.started", `{}`),
		env(3, "goal.paused", `{"reason":"no_progress"}`),
	}
	first, last, err := s.Append(ctx, "g1", evs)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if first != 1 || last != 3 {
		t.Fatalf("seq range = %d..%d, want 1..3", first, last)
	}
	got, truncated, err := s.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for i, g := range got {
		if g.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, g.Seq, i+1)
		}
		if g.Type != evs[i].Type {
			t.Fatalf("event %d type = %q, want %q", i, g.Type, evs[i].Type)
		}
	}
}

func TestAppendSequentialAcrossCalls(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, err := s.Append(ctx, "g1", []Envelope{env(1, "goal.created", `{}`), env(2, "goal.started", `{}`)}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	first, last, err := s.Append(ctx, "g1", []Envelope{env(3, "goal.paused", `{"reason":"x"}`), env(4, "goal.resumed", `{}`), env(5, "goal.completed", `{}`)})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if first != 3 || last != 5 {
		t.Fatalf("seq range = %d..%d, want 3..5", first, last)
	}
	got, _, err := s.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 5 || got[4].Seq != 5 {
		t.Fatalf("loaded %d events, last seq %v", len(got), got[len(got)-1].Seq)
	}
}

func TestAppendRejectsOversizePayload(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	big := strings.Repeat("x", MaxPayloadBytes+1)
	if _, _, err := s.Append(ctx, "g1", []Envelope{env(1, "goal.paused", fmt.Sprintf(`{"reason":%q}`, big))}); err == nil {
		t.Fatal("oversize payload must be rejected")
	}
	got, _, err := s.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load after rejection: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rejected append must not write: got %d events", len(got))
	}
}

func TestAppendRejectsInvalidEnvelope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// seq 是占位符会被覆盖分配；空 type 在任何情况下都必须被拒绝。
	bad := Envelope{Seq: 0, Type: "", OccurredAt: time.Now(), SchemaVersion: 1, Payload: json.RawMessage(`{}`)}
	if _, _, err := s.Append(ctx, "g1", []Envelope{bad}); err == nil {
		t.Fatal("invalid envelope must be rejected")
	}
}

func TestLoadTailTruncation(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "events", "goals", "g1.events.jsonl")
	if err := os.MkdirAll(filepath.Dir(stream), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	good1, _ := json.Marshal(env(1, "goal.created", `{}`))
	good2, _ := json.Marshal(env(2, "goal.started", `{}`))
	content := string(good1) + "\n" + string(good2) + "\n" + `{"seq":3,"type":"goal.paused","occ` // truncated line
	if err := os.WriteFile(stream, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, err := NewJSONLStore(filepath.Join(dir, "events"), "goals")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	got, truncated, err := s.Load(context.Background(), "g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !truncated {
		t.Fatal("tail truncation must be reported")
	}
	if len(got) != 2 || got[1].Seq != 2 {
		t.Fatalf("loaded %d events (want 2 intact), last seq %v", len(got), got[len(got)-1].Seq)
	}
}

func TestLoadMidGapRejected(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "events", "goals", "g1.events.jsonl")
	if err := os.MkdirAll(filepath.Dir(stream), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b1, _ := json.Marshal(env(1, "goal.created", `{}`))
	b2, _ := json.Marshal(env(2, "goal.started", `{}`))
	b4, _ := json.Marshal(env(4, "goal.paused", `{"reason":"x"}`))
	content := string(b1) + "\n" + string(b2) + "\n" + string(b4) + "\n"
	if err := os.WriteFile(stream, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, _ := NewJSONLStore(filepath.Join(dir, "events"), "goals")
	if _, _, err := s.Load(context.Background(), "g1"); err == nil {
		t.Fatal("mid-stream seq gap must be rejected")
	}
}

func TestLoadDuplicateSeqRejected(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "events", "goals", "g1.events.jsonl")
	if err := os.MkdirAll(filepath.Dir(stream), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b1, _ := json.Marshal(env(1, "goal.created", `{}`))
	content := string(b1) + "\n" + string(b1) + "\n"
	if err := os.WriteFile(stream, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, _ := NewJSONLStore(filepath.Join(dir, "events"), "goals")
	if _, _, err := s.Load(context.Background(), "g1"); err == nil {
		t.Fatal("duplicate seq must be rejected")
	}
}

func TestLoadUnknownLineRejected(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "events", "goals", "g1.events.jsonl")
	if err := os.MkdirAll(filepath.Dir(stream), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b1, _ := json.Marshal(env(1, "goal.created", `{}`))
	content := string(b1) + "\n" + `not json at all` + "\n"
	if err := os.WriteFile(stream, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s, _ := NewJSONLStore(filepath.Join(dir, "events"), "goals")
	got, truncated, err := s.Load(context.Background(), "g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !truncated || len(got) != 1 {
		t.Fatalf("non-json tail must truncate: %d events, truncated=%v", len(got), truncated)
	}
}

func TestLoadEmptyStream(t *testing.T) {
	s := newTestStore(t)
	got, truncated, err := s.Load(context.Background(), "missing")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if truncated || len(got) != 0 {
		t.Fatalf("empty stream: got %d events, truncated=%v", len(got), truncated)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	state := json.RawMessage(`{"status":"paused","reason":"no_progress"}`)
	if err := s.WriteSnapshot(ctx, "g1", 7, state); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	snap, ok, err := s.ReadSnapshot(ctx, "g1")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !ok {
		t.Fatal("snapshot must exist")
	}
	if snap.Seq != 7 {
		t.Fatalf("snapshot seq = %d, want 7", snap.Seq)
	}
	if string(snap.State) != string(state) {
		t.Fatalf("snapshot state mismatch: %s vs %s", snap.State, state)
	}
}

func TestSnapshotMissing(t *testing.T) {
	s := newTestStore(t)
	_, ok, err := s.ReadSnapshot(context.Background(), "g1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ok {
		t.Fatal("missing snapshot must report ok=false")
	}
}

func TestSnapshotOverwrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.WriteSnapshot(ctx, "g1", 3, json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := s.WriteSnapshot(ctx, "g1", 9, json.RawMessage(`{"b":2}`)); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	snap, _, err := s.ReadSnapshot(ctx, "g1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snap.Seq != 9 || string(snap.State) != `{"b":2}` {
		t.Fatalf("snapshot not overwritten: %+v", snap)
	}
}

func TestAggregateIDPathSafety(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"", "../escape", "a/b", "a\\b", "a\x00b", ".."} {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			if _, _, err := s.Append(ctx, id, []Envelope{env(1, "goal.created", `{}`)}); err == nil {
				t.Fatalf("unsafe aggregate id %q must be rejected", id)
			}
			if err := s.WriteSnapshot(ctx, id, 1, json.RawMessage(`{}`)); err == nil {
				t.Fatalf("unsafe aggregate id %q must be rejected for snapshot", id)
			}
		})
	}
}

func TestConcurrentAppendSameStream(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const writers = 8
	const perWriter = 25
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				evs := []Envelope{env(0, "goal.task.started", fmt.Sprintf(`{"task_id":"w%d-%d"}`, w, i))}
				// seq 由 store 分配，调用方传 0 占位
				if _, _, err := s.Append(ctx, "g1", evs); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}
	got, _, err := s.Load(ctx, "g1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != writers*perWriter {
		t.Fatalf("got %d events, want %d", len(got), writers*perWriter)
	}
	for i, g := range got {
		if g.Seq != int64(i+1) {
			t.Fatalf("seq not contiguous at %d: %d", i, g.Seq)
		}
	}
}

func TestAppendAssignsSequences(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	evs := []Envelope{env(0, "goal.created", `{}`), env(0, "goal.started", `{}`)}
	first, last, err := s.Append(ctx, "g1", evs)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if first != 1 || last != 2 {
		t.Fatalf("assigned seq = %d..%d, want 1..2", first, last)
	}
	// caller-supplied seq 冲突时以分配为准，后续追加继续递增
	first2, last2, err := s.Append(ctx, "g1", []Envelope{env(99, "goal.paused", `{"reason":"x"}`)})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if first2 != 3 || last2 != 3 {
		t.Fatalf("assigned seq 2 = %d..%d, want 3..3", first2, last2)
	}
}

func TestFilesHaveExpectedPermissions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, err := s.Append(ctx, "g1", []Envelope{env(1, "goal.created", `{}`)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	info, err := os.Stat(s.streamPath("g1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("stream mode = %o, want 600", info.Mode().Perm())
	}
}
