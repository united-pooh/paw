package tokentracer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"paw/internal/capability/model"
)

type RecorderConfig struct {
	Home             string
	Workspace        string
	ParentInstanceID string
}

type Recorder struct {
	mu      sync.Mutex
	dir     string
	meta    InstanceMetadata
	file    *os.File
	segment int
	size    int64
	seq     uint64
	err     error
	done    chan struct{}
	closed  bool
}

func NewRecorder(cfg RecorderConfig) (*Recorder, error) {
	if cfg.Home == "" || cfg.Workspace == "" {
		return nil, fmt.Errorf("telemetry home and workspace required")
	}
	workspace, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	home, err := filepath.Abs(cfg.Home)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, "tracer", "instances")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	id := rand.Text()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	projectHash := sha256.Sum256([]byte(workspace))
	r := &Recorder{dir: dir, done: make(chan struct{}), meta: InstanceMetadata{Version: ledgerVersion, ID: id, ProjectID: fmt.Sprintf("%x", projectHash[:16]), ProjectName: filepath.Base(workspace), Workspace: workspace, ParentInstanceID: cfg.ParentInstanceID, PID: os.Getpid(), StartedAt: now, UpdatedAt: now}}
	if err := r.writeMetadataLocked(); err != nil {
		return nil, err
	}
	if err := r.rotateLocked(); err != nil {
		return nil, err
	}
	go r.heartbeat()
	return r, nil
}

func (r *Recorder) InstanceID() string { return r.meta.ID }

func (r *Recorder) Record(event model.RequestEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.closed {
		return fmt.Errorf("telemetry recorder closed")
	}
	if !validLedgerEvent(event) {
		return r.failLocked(fmt.Errorf("invalid telemetry event"))
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	record := ledgerRecord{Version: ledgerVersion, InstanceID: r.meta.ID, Seq: r.seq + 1, Event: event}
	data, err := json.Marshal(record)
	if err != nil {
		return r.failLocked(err)
	}
	if len(data)+1 > maxLedgerRecordBytes {
		return r.failLocked(fmt.Errorf("telemetry record limit reached"))
	}
	if r.size+int64(len(data)+1) > maxLedgerSegmentBytes {
		if err := r.rotateLocked(); err != nil {
			return r.failLocked(err)
		}
	}
	data = append(data, '\n')
	n, err := r.file.Write(data)
	r.size += int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return r.failLocked(err)
	}
	r.seq++
	if event.Kind == "request_end" || event.Kind == "attempt_stream_end" {
		if err := r.file.Sync(); err != nil {
			return r.failLocked(err)
		}
	}
	return nil
}

func validLedgerEvent(event model.RequestEvent) bool {
	if event.RequestID == "" || len(event.RequestID) > 256 || event.Attempt < 0 || event.Attempt > 10000 {
		return false
	}
	switch event.Kind {
	case "request_start", "request_end":
		return true
	case "attempt_start", "attempt_headers", "attempt_usage", "attempt_response", "attempt_stream_end":
		return event.Attempt > 0
	default:
		return false
	}
}

func (r *Recorder) rotateLocked() error {
	if r.segment >= maxLedgerSegments {
		return fmt.Errorf("telemetry segment limit reached")
	}
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			return err
		}
	}
	r.segment++
	file, err := os.OpenFile(filepath.Join(r.dir, fmt.Sprintf("events-%06d.jsonl", r.segment)), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	r.file, r.size = file, 0
	return nil
}

func (r *Recorder) failLocked(err error) error {
	if r.err == nil {
		r.err = err
		r.meta.Error = "telemetry_write_failed"
		_ = r.writeMetadataLocked()
		log.Printf("Token Tracer telemetry stopped: %v", err)
	}
	return r.err
}

func (r *Recorder) heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.mu.Lock()
			if !r.closed {
				r.meta.UpdatedAt = time.Now().UTC()
				if err := r.writeMetadataLocked(); err != nil {
					r.failLocked(err)
				}
			}
			r.mu.Unlock()
		}
	}
}

func (r *Recorder) writeMetadataLocked() error {
	data, err := json.Marshal(r.meta)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(r.dir, ".meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(r.dir, "meta.json"))
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.closed = true
	close(r.done)
	now := time.Now().UTC()
	r.meta.UpdatedAt, r.meta.StoppedAt = now, &now
	var fileErr error
	if r.file != nil {
		fileErr = errors.Join(r.file.Sync(), r.file.Close())
	}
	r.err = errors.Join(r.err, fileErr, r.writeMetadataLocked())
	return r.err
}
