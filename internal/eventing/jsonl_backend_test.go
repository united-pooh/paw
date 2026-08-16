package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testEvent(kind, value string) Event {
	return Event{Type: kind, SchemaVersion: 1, Payload: json.RawMessage(value)}
}

func appendOne(t *testing.T, backend *JSONLBackend, ref StreamRef, command string, expected int64, event Event) Commit {
	t.Helper()
	commit, err := backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: command, ExpectedVersion: expected, Events: []Event{event}})
	if err != nil {
		t.Fatalf("append %s: %v", command, err)
	}
	return commit
}

func TestJSONLBackendAtomicBatchAndReplay(t *testing.T) {
	backend := NewJSONLBackend(t.TempDir())
	ref := StreamRef{StreamType: "session", StreamID: "one/two"}
	commit, err := backend.Append(context.Background(), AppendRequest{
		Stream: ref, CommandID: "cmd-1", ExpectedVersion: 0,
		Events: []Event{testEvent("TurnStarted", `{"turn":1}`), testEvent("TurnFinished", `{"turn":1}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.FirstVersion != 1 || commit.LastVersion != 2 || len(commit.Events) != 2 {
		t.Fatalf("unexpected commit: %#v", commit)
	}
	path, err := backend.StreamPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "one%2ftwo.jsonl" {
		t.Fatalf("unsafe stream filename: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []json.RawMessage
	for _, line := range bytesLines(raw) {
		lines = append(lines, line)
	}
	if len(lines) != 1 {
		t.Fatalf("one command must occupy one line, got %d", len(lines))
	}
	events, err := backend.Replay(context.Background(), ref)
	if err != nil || len(events) != 2 || events[1].StreamVersion != 2 {
		t.Fatalf("replay events=%#v err=%v", events, err)
	}
}

func TestJSONLBackendVersionAndIdempotencyConflicts(t *testing.T) {
	backend := NewJSONLBackend(t.TempDir())
	ref := StreamRef{StreamType: "goal", StreamID: "g1"}
	event := Event{EventID: "event-1", Type: "Started", SchemaVersion: 1, Payload: json.RawMessage(`{"x":1}`)}
	first := appendOne(t, backend, ref, "command-1", 0, event)

	retry, err := backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: "command-1", ExpectedVersion: 0, Events: []Event{event}})
	if err != nil || !retry.Idempotent || retry.LastVersion != first.LastVersion {
		t.Fatalf("idempotent retry=%#v err=%v", retry, err)
	}
	_, err = backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: "command-1", ExpectedVersion: 1, Events: []Event{testEvent("Different", `{}`)}})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected command conflict, got %v", err)
	}
	_, err = backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: "command-2", ExpectedVersion: 0, Events: []Event{testEvent("Next", `{}`)}})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	_, err = backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: "command-3", ExpectedVersion: 1, Events: []Event{event}})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected event id conflict, got %v", err)
	}
}

func TestJSONLBackendTwoInstancesRereadVersionUnderAdvisoryLock(t *testing.T) {
	root := t.TempDir()
	left, right := NewJSONLBackend(root), NewJSONLBackend(root)
	ref := StreamRef{StreamType: "todo", StreamID: "same"}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, backend := range []*JSONLBackend{left, right} {
		wg.Add(1)
		go func(i int, backend *JSONLBackend) {
			defer wg.Done()
			<-start
			_, err := backend.Append(context.Background(), AppendRequest{Stream: ref, CommandID: string(rune('a' + i)), ExpectedVersion: 0, Events: []Event{testEvent("Added", `{}`)}})
			errs <- err
		}(i, backend)
	}
	close(start)
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected append error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestJSONLBackendToleratesAndRepairsOnlyTornTail(t *testing.T) {
	backend := NewJSONLBackend(t.TempDir())
	ref := StreamRef{StreamType: "plan", StreamID: "p1"}
	appendOne(t, backend, ref, "one", 0, testEvent("Created", `{}`))
	path, _ := backend.StreamPath(ref)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"format_version":1,"events":[`)
	_ = file.Close()
	events, err := backend.Load(context.Background(), ref)
	if err != nil || len(events) != 1 {
		t.Fatalf("torn replay events=%d err=%v", len(events), err)
	}
	appendOne(t, backend, ref, "two", 1, testEvent("Updated", `{}`))
	events, err = backend.Load(context.Background(), ref)
	if err != nil || len(events) != 2 {
		t.Fatalf("tail was not repaired events=%d err=%v", len(events), err)
	}

	raw, _ := os.ReadFile(path)
	firstNewline := len(bytesLines(raw)[0])
	corruptRaw := append([]byte(nil), raw[:firstNewline]...)
	corruptRaw = append(corruptRaw, []byte("\nnot-json\n")...)
	corruptRaw = append(corruptRaw, raw[firstNewline+1:]...)
	if err := os.WriteFile(path, corruptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = backend.Load(context.Background(), ref)
	if !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("expected middle corruption, got %v", err)
	}
}

func TestJSONLBackendAppendsAfterValidUnterminatedRecord(t *testing.T) {
	backend := NewJSONLBackend(t.TempDir())
	ref := StreamRef{StreamType: "session", StreamID: "unterminated"}
	appendOne(t, backend, ref, "one", 0, testEvent("Created", `{}`))
	path, _ := backend.StreamPath(ref)
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, raw[:len(raw)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	appendOne(t, backend, ref, "two", 1, testEvent("Updated", `{}`))
	events, err := backend.Load(context.Background(), ref)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
}

func TestJSONLBackendRejectsVersionDiscontinuity(t *testing.T) {
	backend := NewJSONLBackend(t.TempDir())
	ref := StreamRef{StreamType: "task", StreamID: "task"}
	commit := appendOne(t, backend, ref, "one", 0, testEvent("Started", `{}`))
	path, _ := backend.StreamPath(ref)
	raw, _ := os.ReadFile(path)
	var batch diskBatch
	if err := json.Unmarshal(bytesLines(raw)[0], &batch); err != nil {
		t.Fatal(err)
	}
	batch.CommandID = "two"
	batch.RequestHash = "other"
	batch.FirstVersion = 3
	batch.LastVersion = 3
	batch.Events = []Event{commit.Events[0]}
	batch.Events[0].EventID = "event-two"
	batch.Events[0].StreamVersion = 3
	line, _ := json.Marshal(batch)
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = file.Write(append(line, '\n'))
	_ = file.Close()
	_, err := backend.Load(context.Background(), ref)
	if !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("expected version corruption, got %v", err)
	}
}

func TestJSONLBackendUnknownSchemaAndUpcaster(t *testing.T) {
	root := t.TempDir()
	writer := NewJSONLBackend(root)
	ref := StreamRef{StreamType: "streamma", StreamID: "run"}
	appendOne(t, writer, ref, "one", 0, testEvent("StepCommitted", `{"old":"value"}`))

	strict := NewSchemaRegistry()
	if err := strict.Register("StepCommitted", 2); err != nil {
		t.Fatal(err)
	}
	reader := NewJSONLBackend(root, WithSchemaRegistry(strict))
	_, err := reader.Load(context.Background(), ref)
	if !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("missing upcaster should be unknown schema, got %v", err)
	}
	if err := strict.RegisterUpcaster("StepCommitted", 1, func(event Event) (Event, error) {
		var old map[string]string
		if err := json.Unmarshal(event.Payload, &old); err != nil {
			return Event{}, err
		}
		event.Payload = json.RawMessage(`{"new":"value"}`)
		event.SchemaVersion = 2
		return event, nil
	}); err != nil {
		t.Fatal(err)
	}
	events, err := reader.Load(context.Background(), ref)
	if err != nil || len(events) != 1 || events[0].SchemaVersion != 2 || string(events[0].Payload) != `{"new":"value"}` {
		t.Fatalf("upcast events=%#v err=%v", events, err)
	}
}

func TestAppendOrdersFsyncProjectionThenRuntimeBus(t *testing.T) {
	bus := NewRuntimeBus()
	updates, cancel := bus.Subscribe(1)
	defer cancel()
	backend := NewJSONLBackend(t.TempDir(), WithRuntimeBus(bus))
	ref := StreamRef{StreamType: "session", StreamID: "order"}
	projected := false
	commit, err := backend.Append(context.Background(), AppendRequest{
		Stream: ref, CommandID: "one", ExpectedVersion: 0, Events: []Event{testEvent("Done", `{}`)},
		Projection: func(_ context.Context, got Commit) error {
			if _, err := os.Stat(mustStreamPath(t, backend, ref)); err != nil {
				t.Fatalf("projection ran before durable stream existed: %v", err)
			}
			projected = got.LastVersion == 1
			return nil
		},
	})
	if err != nil || !projected {
		t.Fatalf("commit=%#v projected=%v err=%v", commit, projected, err)
	}
	got := <-updates
	if !projected || got.LastVersion != 1 {
		t.Fatalf("bus published before projection: %#v", got)
	}
}

func TestProjectionFailureReturnsCommittedLagAndDoesNotPublish(t *testing.T) {
	bus := NewRuntimeBus()
	updates, cancel := bus.Subscribe(1)
	defer cancel()
	backend := NewJSONLBackend(t.TempDir(), WithRuntimeBus(bus))
	ref := StreamRef{StreamType: "goal", StreamID: "projection"}
	want := errors.New("projection unavailable")
	commit, err := backend.Append(context.Background(), AppendRequest{
		Stream: ref, CommandID: "one", ExpectedVersion: 0, Events: []Event{testEvent("Started", `{}`)},
		Projection: func([]Event) error { return want },
	})
	var committed *CommittedProjectionError
	if !errors.As(err, &committed) || !errors.Is(err, ErrProjectionLag) || !errors.Is(err, want) || committed.Version() != 1 || commit.LastVersion != 1 {
		t.Fatalf("commit=%#v error=%v", commit, err)
	}
	if version, loadErr := backend.CurrentVersion(context.Background(), ref); loadErr != nil || version != 1 {
		t.Fatalf("durable version=%d err=%v", version, loadErr)
	}
	select {
	case got := <-updates:
		t.Fatalf("published projection-lagged commit: %#v", got)
	default:
	}
}

func TestSnapshotStoreReplaceDeleteAndRebuild(t *testing.T) {
	root := t.TempDir()
	store := NewSnapshotStore(root)
	ref := StreamRef{StreamType: "todo", StreamID: "list/1"}
	if err := store.Save(context.Background(), Snapshot{Stream: ref, Version: 2, State: json.RawMessage(`{"count":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), Snapshot{Stream: ref, Version: 3, State: json.RawMessage(`{"count":3}`)}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), ref)
	if err != nil || loaded.Version != 3 || string(loaded.State) != `{"count":3}` {
		t.Fatalf("snapshot=%#v err=%v", loaded, err)
	}
	path, _ := store.Path(ref)
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".snapshot-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("temporary snapshots leaked: %v", matches)
	}
	if err := store.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), ref); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted snapshot should be rebuildable/missing, got %v", err)
	}
}

func bytesLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			lines = append(lines, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

func mustStreamPath(t *testing.T, backend *JSONLBackend, ref StreamRef) string {
	t.Helper()
	path, err := backend.StreamPath(ref)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
