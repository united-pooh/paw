package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is a disposable aggregate checkpoint. Event streams remain the
// source of truth and Version tells a caller where replay must resume.
type Snapshot struct {
	Stream        StreamRef       `json:"stream"`
	Version       int64           `json:"version"`
	SchemaVersion int             `json:"schema_version"`
	Timestamp     time.Time       `json:"timestamp"`
	State         json.RawMessage `json:"state"`
}

type SnapshotStore struct {
	root string
	now  func() time.Time
}

func NewSnapshotStore(root string) *SnapshotStore {
	return &SnapshotStore{root: root, now: time.Now}
}

func (s *SnapshotStore) Path(ref StreamRef) (string, error) {
	ref, err := normalizeRef(ref)
	if err != nil {
		return "", err
	}
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", errors.New("snapshot root is required")
	}
	return filepath.Join(s.root, "snapshots", safeComponent(ref.StreamType), safeComponent(ref.StreamID)+".json"), nil
}

// Save writes and syncs a temporary file before atomically renaming it over the
// old snapshot. The containing directory is synced after rename.
func (s *SnapshotStore) Save(ctx context.Context, snapshot Snapshot) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ref, err := normalizeRef(snapshot.Stream)
	if err != nil {
		return err
	}
	if snapshot.Version < 0 {
		return errors.New("snapshot version cannot be negative")
	}
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = 1
	}
	if snapshot.SchemaVersion < 1 {
		return errors.New("snapshot schema version must be positive")
	}
	if len(snapshot.State) == 0 {
		snapshot.State = json.RawMessage("null")
	} else if !json.Valid(snapshot.State) {
		return errors.New("snapshot state is not valid JSON")
	}
	snapshot.Stream = ref
	if snapshot.Timestamp.IsZero() {
		snapshot.Timestamp = s.now().UTC()
	} else {
		snapshot.Timestamp = snapshot.Timestamp.UTC()
	}
	path, err := s.Path(ref)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	raw = append(raw, '\n')
	temp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create snapshot temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	err = temp.Chmod(0o600)
	if err == nil {
		err = writeFull(temp, raw)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close snapshot: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open snapshot directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil {
		return fmt.Errorf("sync snapshot directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close snapshot directory: %w", closeErr)
	}
	return nil
}

func (s *SnapshotStore) Load(ctx context.Context, ref StreamRef) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	ref, err := normalizeRef(ref)
	if err != nil {
		return Snapshot{}, err
	}
	path, err := s.Path(ref)
	if err != nil {
		return Snapshot{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	normalized, err := normalizeRef(snapshot.Stream)
	if err != nil || normalized != ref || snapshot.Version < 0 || snapshot.SchemaVersion < 1 || !json.Valid(snapshot.State) {
		return Snapshot{}, errors.New("invalid snapshot envelope")
	}
	snapshot.State = append(json.RawMessage(nil), snapshot.State...)
	return snapshot, nil
}

func (s *SnapshotStore) Delete(ctx context.Context, ref StreamRef) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.Path(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}
