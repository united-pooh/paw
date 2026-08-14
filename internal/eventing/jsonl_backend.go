package eventing

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const diskFormatVersion = 1

// Upcaster upgrades one event by exactly one schema version.
type Upcaster func(Event) (Event, error)

// SchemaRegistry describes the current schema for registered event types.
// A nil registry accepts schema version 1 for every type. A non-nil registry is
// strict: unregistered types and incomplete upcast chains are rejected.
type SchemaRegistry struct {
	mu       sync.RWMutex
	current  map[string]int
	upcaster map[string]map[int]Upcaster
}

func NewSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{current: make(map[string]int), upcaster: make(map[string]map[int]Upcaster)}
}

func (r *SchemaRegistry) Register(eventType string, currentVersion int) error {
	if r == nil {
		return errors.New("schema registry is nil")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || currentVersion < 1 {
		return errors.New("event type and positive current schema version are required")
	}
	r.mu.Lock()
	r.current[eventType] = currentVersion
	r.mu.Unlock()
	return nil
}

func (r *SchemaRegistry) RegisterUpcaster(eventType string, fromVersion int, upcaster Upcaster) error {
	if r == nil {
		return errors.New("schema registry is nil")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || fromVersion < 1 || upcaster == nil {
		return errors.New("event type, positive source version, and upcaster are required")
	}
	r.mu.Lock()
	if r.upcaster[eventType] == nil {
		r.upcaster[eventType] = make(map[int]Upcaster)
	}
	r.upcaster[eventType][fromVersion] = upcaster
	r.mu.Unlock()
	return nil
}

func (r *SchemaRegistry) upcast(event Event) (Event, error) {
	if event.SchemaVersion < 1 {
		return Event{}, fmt.Errorf("%w: %s has invalid version %d", ErrUnknownSchema, event.Type, event.SchemaVersion)
	}
	if r == nil {
		if event.SchemaVersion != 1 {
			return Event{}, fmt.Errorf("%w: %s version %d", ErrUnknownSchema, event.Type, event.SchemaVersion)
		}
		return event, nil
	}
	r.mu.RLock()
	current, ok := r.current[event.Type]
	registered := r.upcaster[event.Type]
	chain := make(map[int]Upcaster, len(registered))
	for version, fn := range registered {
		chain[version] = fn
	}
	r.mu.RUnlock()
	if !ok || event.SchemaVersion > current {
		return Event{}, fmt.Errorf("%w: %s version %d", ErrUnknownSchema, event.Type, event.SchemaVersion)
	}
	for event.SchemaVersion < current {
		fn := chain[event.SchemaVersion]
		if fn == nil {
			return Event{}, fmt.Errorf("%w: %s version %d has no upcaster", ErrUnknownSchema, event.Type, event.SchemaVersion)
		}
		before := event.SchemaVersion
		var err error
		event, err = fn(cloneEvent(event))
		if err != nil {
			return Event{}, fmt.Errorf("upcast %s version %d: %w", event.Type, before, err)
		}
		if event.SchemaVersion != before+1 {
			return Event{}, fmt.Errorf("%w: %s upcaster changed version %d to %d", ErrUnknownSchema, event.Type, before, event.SchemaVersion)
		}
	}
	return event, nil
}

type BackendOption func(*JSONLBackend)

func WithSchemaRegistry(registry *SchemaRegistry) BackendOption {
	return func(b *JSONLBackend) { b.schemas = registry }
}

func WithRuntimeBus(bus *RuntimeBus) BackendOption {
	return func(b *JSONLBackend) { b.bus = bus }
}

// JSONLBackend stores one command batch per JSONL line below
// <root>/event-streams/<stream-type>/<safe-id>.jsonl.
type JSONLBackend struct {
	root    string
	schemas *SchemaRegistry
	bus     *RuntimeBus
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
	now     func() time.Time
}

func NewJSONLBackend(root string, options ...BackendOption) *JSONLBackend {
	b := &JSONLBackend{root: root, locks: make(map[string]*sync.Mutex), now: time.Now}
	for _, option := range options {
		if option != nil {
			option(b)
		}
	}
	return b
}

func (b *JSONLBackend) SetRuntimeBus(bus *RuntimeBus) { b.bus = bus }

func (b *JSONLBackend) StreamPath(ref StreamRef) (string, error) {
	ref, err := normalizeRef(ref)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(b.root) == "" {
		return "", errors.New("event backend root is required")
	}
	return filepath.Join(b.root, "event-streams", safeComponent(ref.StreamType), safeComponent(ref.StreamID)+".jsonl"), nil
}

type diskBatch struct {
	FormatVersion int       `json:"format_version"`
	Stream        StreamRef `json:"stream"`
	CommandID     string    `json:"command_id"`
	RequestHash   string    `json:"request_hash"`
	FirstVersion  int64     `json:"first_version"`
	LastVersion   int64     `json:"last_version"`
	Events        []Event   `json:"events"`
}

type loadedStream struct {
	events       []Event
	batches      map[string]diskBatch
	eventIDs     map[string]string
	version      int64
	validSize    int64
	torn         bool
	unterminated bool
}

func (b *JSONLBackend) Append(ctx context.Context, request AppendRequest) (Commit, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ref, err := normalizeRef(request.Stream)
	if err != nil {
		return Commit{}, err
	}
	request.CommandID = strings.TrimSpace(request.CommandID)
	if request.CommandID == "" {
		return Commit{}, errors.New("command id is required")
	}
	incoming := request.eventBatch()
	if len(incoming) == 0 {
		return Commit{}, ErrEmptyBatch
	}
	requestHash, err := hashRequestEvents(incoming)
	if err != nil {
		return Commit{}, err
	}
	path, err := b.StreamPath(ref)
	if err != nil {
		return Commit{}, err
	}
	lock := b.streamLock(path)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Commit{}, fmt.Errorf("create event stream directory: %w", err)
	}
	lockFile, err := lockAdvisory(path + ".lock")
	if err != nil {
		return Commit{}, err
	}
	defer unlockAdvisory(lockFile)

	loaded, err := b.readStream(path, ref)
	if err != nil {
		return Commit{}, err
	}
	if previous, ok := loaded.batches[request.CommandID]; ok {
		if previous.RequestHash != requestHash {
			return Commit{}, fmt.Errorf("%w: command %q was already committed with different events", ErrIdempotencyConflict, request.CommandID)
		}
		return commitFromBatch(previous, true), nil
	}
	if request.ExpectedVersion != loaded.version {
		return Commit{}, fmt.Errorf("%w: expected %d, current %d", ErrVersionConflict, request.ExpectedVersion, loaded.version)
	}
	if loaded.torn {
		if err := os.Truncate(path, loaded.validSize); err != nil {
			return Commit{}, fmt.Errorf("discard torn event stream tail: %w", err)
		}
	}

	events := make([]Event, len(incoming))
	seen := make(map[string]struct{}, len(incoming))
	for i := range incoming {
		event := cloneEvent(incoming[i])
		if strings.TrimSpace(event.Type) == "" {
			return Commit{}, fmt.Errorf("event %d type is required", i)
		}
		if len(event.Payload) == 0 {
			event.Payload = json.RawMessage("null")
		} else if !json.Valid(event.Payload) {
			return Commit{}, fmt.Errorf("event %d payload is not valid JSON", i)
		}
		if event.SchemaVersion == 0 {
			event.SchemaVersion = 1
		}
		if _, err := b.schemas.upcast(event); err != nil {
			return Commit{}, err
		}
		if event.StreamType != "" && event.StreamType != ref.StreamType || event.StreamID != "" && event.StreamID != ref.StreamID {
			return Commit{}, fmt.Errorf("event %d stream does not match append stream", i)
		}
		event.StreamType, event.StreamID = ref.StreamType, ref.StreamID
		event.StreamVersion = loaded.version + int64(i) + 1
		if event.EventID == "" {
			event.EventID, err = newEventID()
			if err != nil {
				return Commit{}, err
			}
		}
		if _, duplicate := seen[event.EventID]; duplicate {
			return Commit{}, fmt.Errorf("%w: duplicate event id %q in batch", ErrIdempotencyConflict, event.EventID)
		}
		seen[event.EventID] = struct{}{}
		if command, duplicate := loaded.eventIDs[event.EventID]; duplicate {
			return Commit{}, fmt.Errorf("%w: event id %q already belongs to command %q", ErrIdempotencyConflict, event.EventID, command)
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = b.now().UTC()
		} else {
			event.Timestamp = event.Timestamp.UTC()
		}
		events[i] = event
	}
	batch := diskBatch{FormatVersion: diskFormatVersion, Stream: ref, CommandID: request.CommandID, RequestHash: requestHash, FirstVersion: events[0].StreamVersion, LastVersion: events[len(events)-1].StreamVersion, Events: events}
	line, err := json.Marshal(batch)
	if err != nil {
		return Commit{}, fmt.Errorf("encode event batch: %w", err)
	}
	if loaded.unterminated {
		line = append([]byte{'\n'}, line...)
	}
	line = append(line, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Commit{}, fmt.Errorf("open event stream: %w", err)
	}
	if err = writeFull(file, line); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return Commit{}, fmt.Errorf("durably append event batch: %w", err)
	}
	if closeErr != nil {
		return Commit{}, fmt.Errorf("close event stream: %w", closeErr)
	}
	commit := commitFromBatch(batch, false)
	if err := applyProjection(ctx, request.Projection, commit); err != nil {
		return commit, &CommittedProjectionError{Stream: ref, CommittedVersion: commit.LastVersion, Err: err}
	}
	if b.bus != nil {
		if err := b.bus.Publish(ctx, commit); err != nil {
			return commit, fmt.Errorf("event batch committed at version %d but runtime publish failed: %w", commit.LastVersion, err)
		}
	}
	return commit, nil
}

func (b *JSONLBackend) Load(ctx context.Context, ref StreamRef) ([]Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ref, err := normalizeRef(ref)
	if err != nil {
		return nil, err
	}
	path, err := b.StreamPath(ref)
	if err != nil {
		return nil, err
	}
	lock := b.streamLock(path)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat event stream: %w", err)
	}
	lockFile, err := lockAdvisory(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer unlockAdvisory(lockFile)
	loaded, err := b.readStream(path, ref)
	if err != nil {
		return nil, err
	}
	return cloneEvents(loaded.events), nil
}

func (b *JSONLBackend) Replay(ctx context.Context, ref StreamRef) ([]Event, error) {
	return b.Load(ctx, ref)
}

func (b *JSONLBackend) CurrentVersion(ctx context.Context, ref StreamRef) (int64, error) {
	events, err := b.Load(ctx, ref)
	if err != nil || len(events) == 0 {
		return 0, err
	}
	return events[len(events)-1].StreamVersion, nil
}

func (b *JSONLBackend) readStream(path string, ref StreamRef) (loadedStream, error) {
	result := loadedStream{batches: make(map[string]diskBatch), eventIDs: make(map[string]string)}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("open event stream: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	lineNumber := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		lineNumber++
		complete := len(line) > 0 && line[len(line)-1] == '\n'
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if readErr == nil {
				return result, corrupt(lineNumber, "blank record")
			}
			result.torn = true
			break
		}
		var batch diskBatch
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			if errors.Is(readErr, io.EOF) && !complete {
				result.torn = true
				break // only a final, unterminated malformed record is a tolerated torn tail
			}
			return result, corrupt(lineNumber, "invalid JSON: %v", err)
		}
		if err := b.validateBatch(ref, &result, batch, lineNumber); err != nil {
			return result, err
		}
		result.validSize += int64(len(line))
		if errors.Is(readErr, io.EOF) && !complete {
			result.unterminated = true
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, fmt.Errorf("read event stream: %w", readErr)
		}
	}
	return result, nil
}

func (b *JSONLBackend) validateBatch(ref StreamRef, result *loadedStream, batch diskBatch, line int) error {
	if batch.FormatVersion != diskFormatVersion {
		return corrupt(line, "unknown batch format %d", batch.FormatVersion)
	}
	if batch.Stream != ref || batch.CommandID == "" || len(batch.Events) == 0 {
		return corrupt(line, "invalid stream, command id, or empty events")
	}
	if _, duplicate := result.batches[batch.CommandID]; duplicate {
		return corrupt(line, "duplicate command id %q", batch.CommandID)
	}
	wantFirst := result.version + 1
	if batch.FirstVersion != wantFirst || batch.LastVersion != wantFirst+int64(len(batch.Events))-1 {
		return corrupt(line, "version discontinuity: expected %d..%d, got %d..%d", wantFirst, wantFirst+int64(len(batch.Events))-1, batch.FirstVersion, batch.LastVersion)
	}
	upcasted := make([]Event, len(batch.Events))
	for i, stored := range batch.Events {
		want := wantFirst + int64(i)
		if stored.StreamType != ref.StreamType || stored.StreamID != ref.StreamID || stored.StreamVersion != want || stored.EventID == "" || stored.Type == "" {
			return corrupt(line, "invalid event %d envelope or version (want %d)", i, want)
		}
		if _, duplicate := result.eventIDs[stored.EventID]; duplicate {
			return corrupt(line, "duplicate event id %q", stored.EventID)
		}
		if !json.Valid(stored.Payload) {
			return corrupt(line, "event %d has invalid payload", i)
		}
		event, err := b.schemas.upcast(stored)
		if err != nil {
			return err
		}
		// Ordering identity belongs to the stored envelope and may not be changed
		// by an upcaster.
		event.EventID, event.StreamType, event.StreamID, event.StreamVersion = stored.EventID, stored.StreamType, stored.StreamID, stored.StreamVersion
		upcasted[i] = event
		result.eventIDs[stored.EventID] = batch.CommandID
	}
	result.events = append(result.events, upcasted...)
	result.version = batch.LastVersion
	batch.Events = cloneEvents(batch.Events)
	result.batches[batch.CommandID] = batch
	return nil
}

func (b *JSONLBackend) streamLock(path string) *sync.Mutex {
	b.locksMu.Lock()
	defer b.locksMu.Unlock()
	if lock := b.locks[path]; lock != nil {
		return lock
	}
	lock := new(sync.Mutex)
	b.locks[path] = lock
	return lock
}

func lockAdvisory(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open event stream lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock event stream: %w", err)
	}
	return file, nil
}

func unlockAdvisory(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func safeComponent(value string) string {
	const hexDigits = "0123456789abcdef"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' && value != "." && value != ".." {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hexDigits[c>>4])
		out.WriteByte(hexDigits[c&15])
	}
	return out.String()
}

func hashRequestEvents(events []Event) (string, error) {
	type intent struct {
		EventID       string          `json:"event_id,omitempty"`
		Type          string          `json:"type"`
		SchemaVersion int             `json:"schema_version"`
		SessionID     string          `json:"session_id,omitempty"`
		CorrelationID string          `json:"correlation_id,omitempty"`
		CausationID   string          `json:"causation_id,omitempty"`
		Timestamp     time.Time       `json:"timestamp,omitempty"`
		Payload       json.RawMessage `json:"payload"`
		Metadata      map[string]any  `json:"metadata,omitempty"`
	}
	values := make([]intent, len(events))
	for i, event := range events {
		schema := event.SchemaVersion
		if schema == 0 {
			schema = 1
		}
		payload := event.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		values[i] = intent{event.EventID, event.Type, schema, event.SessionID, event.CorrelationID, event.CausationID, event.Timestamp.UTC(), payload, event.Metadata}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode command fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func commitFromBatch(batch diskBatch, idempotent bool) Commit {
	return Commit{Stream: batch.Stream, CommandID: batch.CommandID, Events: cloneEvents(batch.Events), FirstVersion: batch.FirstVersion, LastVersion: batch.LastVersion, Version: batch.LastVersion, CommittedVersion: batch.LastVersion, Idempotent: idempotent}
}

func applyProjection(ctx context.Context, projection any, commit Commit) error {
	if projection == nil {
		return nil
	}
	switch p := projection.(type) {
	case interface {
		Project(context.Context, Commit) error
	}:
		return p.Project(ctx, cloneCommit(commit))
	case interface {
		Apply(context.Context, []Event) error
	}:
		return p.Apply(ctx, cloneEvents(commit.Events))
	case func(context.Context, Commit) error:
		return p(ctx, cloneCommit(commit))
	case func(context.Context, []Event) error:
		return p(ctx, cloneEvents(commit.Events))
	case func(Commit) error:
		return p(cloneCommit(commit))
	case func([]Event) error:
		return p(cloneEvents(commit.Events))
	default:
		return fmt.Errorf("unsupported projection type %T", projection)
	}
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func corrupt(line int, format string, args ...any) error {
	return fmt.Errorf("%w at line %d: %s", ErrCorruptStream, line, fmt.Sprintf(format, args...))
}
