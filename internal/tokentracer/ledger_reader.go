package tokentracer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"paw/internal/capability/model"
)

type ledgerFileCursor struct {
	offset    int64
	read      int64
	info      os.FileInfo
	pending   []byte
	dropped   bool
	oversized bool
	guard     [64]byte
	guardSize int
}

type LedgerReader struct {
	mu          sync.Mutex
	root        string
	files       map[string]*ledgerFileCursor
	sequences   map[string]uint64
	instances   map[string]InstanceMetadata
	requests    map[string]*LedgerRequest
	issues      map[string]LedgerIssue
	atoms       int
	pendingFile *ledgerFileCursor
}

func NewLedgerReader(home string) *LedgerReader {
	return &LedgerReader{root: filepath.Join(home, "tracer", "instances"), files: map[string]*ledgerFileCursor{}, sequences: map[string]uint64{}, instances: map[string]InstanceMetadata{}, requests: map[string]*LedgerRequest{}, issues: map[string]LedgerIssue{}}
}

func (r *LedgerReader) issue(id, kind string) {
	r.issues[id+":"+kind] = LedgerIssue{InstanceID: id, Kind: kind}
}

func (r *LedgerReader) Snapshot() (LedgerSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot, err := r.scanLocked()
	if err != nil {
		return LedgerSnapshot{}, err
	}
	requests := make([]*LedgerRequest, 0, len(r.requests))
	for _, request := range r.requests {
		requests = append(requests, request)
	}
	return snapshotRequests(snapshot, requests), nil
}

func (r *LedgerReader) scanLocked() (LedgerSnapshot, error) {
	entries, err := ledgerDirectory(r.root, maxLedgerInstances)
	if errors.Is(err, os.ErrNotExist) {
		entries, err = nil, nil
	}
	if err != nil {
		return LedgerSnapshot{}, err
	}
	if len(entries) > maxLedgerInstances {
		r.issue("", "instance_limit")
		entries = entries[:maxLedgerInstances]
	}
	for key, issue := range r.issues {
		if issue.Kind == "partial_tail" {
			delete(r.issues, key)
		}
	}
	seen := map[string]bool{}
	budget := int64(64 << 20)
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		seen[id] = true
		dir := filepath.Join(r.root, id)
		metaFile, err := openLedgerFile(filepath.Join(dir, "meta.json"), 64<<10)
		if err != nil {
			r.issue(id, "metadata_unavailable")
			continue
		}
		var meta InstanceMetadata
		err = json.NewDecoder(metaFile).Decode(&meta)
		_ = metaFile.Close()
		if err != nil || meta.Version != ledgerVersion || meta.ID != id || meta.ProjectID == "" || meta.Workspace == "" {
			r.issue(id, "invalid_metadata")
			continue
		}
		r.instances[id] = meta
		segments, err := ledgerDirectory(dir, maxLedgerSegments+4)
		if err != nil {
			r.issue(id, "segments_unavailable")
			continue
		}
		if len(segments) > maxLedgerSegments+4 {
			r.issue(id, "segment_limit")
			continue
		}
		for _, segment := range segments {
			name := segment.Name()
			if !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if budget <= 0 {
				r.issue("", "scan_backlog")
				break
			}
			if !r.scanFile(id, filepath.Join(dir, name), &budget) {
				break
			}
		}
	}
	if budget > 0 {
		delete(r.issues, ":scan_backlog")
	}
	now := time.Now().UTC()
	snapshot := LedgerSnapshot{Version: ledgerVersion, GeneratedAt: now, Instances: []LedgerInstance{}, Requests: []LedgerRequest{}, Issues: []LedgerIssue{}}
	for id, meta := range r.instances {
		status := "recent"
		if !seen[id] {
			status = "missing"
		} else if meta.StoppedAt != nil {
			status = "stopped"
		} else if now.Sub(meta.UpdatedAt) > 30*time.Second {
			status = "stale"
		}
		if meta.Error != "" {
			r.issue(id, meta.Error)
		}
		snapshot.Instances = append(snapshot.Instances, LedgerInstance{InstanceMetadata: meta, Status: status})
	}
	for _, issue := range r.issues {
		snapshot.Issues = append(snapshot.Issues, issue)
	}
	sort.Slice(snapshot.Instances, func(i, j int) bool { return snapshot.Instances[i].StartedAt.After(snapshot.Instances[j].StartedAt) })
	sort.Slice(snapshot.Issues, func(i, j int) bool {
		return snapshot.Issues[i].InstanceID+snapshot.Issues[i].Kind < snapshot.Issues[j].InstanceID+snapshot.Issues[j].Kind
	})
	return snapshot, nil
}

func (r *LedgerReader) scanFile(id, path string, budget *int64) bool {
	file, err := openLedgerFile(path, maxLedgerSegmentBytes+maxLedgerRecordBytes)
	if err != nil {
		r.issue(id, "segment_unavailable")
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		r.issue(id, "segment_unavailable")
		return false
	}
	cursor := r.files[path]
	if cursor == nil {
		cursor = &ledgerFileCursor{info: info}
		r.files[path] = cursor
	}
	if !os.SameFile(info, cursor.info) || info.Size() < cursor.read {
		r.issue(id, "segment_replaced_or_truncated")
		return false
	}
	if cursor.guardSize > 0 && (info.Size() != cursor.info.Size() || !info.ModTime().Equal(cursor.info.ModTime())) {
		var guard [64]byte
		if _, err := file.ReadAt(guard[:cursor.guardSize], cursor.read-int64(cursor.guardSize)); err != nil || !bytes.Equal(guard[:cursor.guardSize], cursor.guard[:cursor.guardSize]) {
			r.issue(id, "segment_replaced_or_truncated")
			return false
		}
	}
	cursor.info = info
	// Released partial records must be reread, but unchanged EOF tails cost no payload I/O.
	if cursor.dropped && cursor.read < info.Size() {
		cursor.read, cursor.guardSize, cursor.dropped = cursor.offset, 0, false
	}
	if _, err := file.Seek(cursor.read, io.SeekStart); err != nil {
		r.issue(id, "segment_unavailable")
		return false
	}
	reader := bufio.NewReader(io.LimitReader(file, min(info.Size()-cursor.read, *budget)))
	for {
		if cursor.read == info.Size() {
			if cursor.read != cursor.offset {
				cursor.releasePending()
				r.issue(id, "partial_tail")
				return false
			}
			return true
		}
		if *budget <= 0 {
			r.issue("", "scan_backlog")
			return false
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) == 0 || err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			r.issue(id, "segment_unavailable")
			return false
		}
		*budget -= int64(len(fragment))
		if r.pendingFile != cursor {
			if r.pendingFile != nil {
				r.pendingFile.releasePending()
			}
			r.pendingFile = cursor
		}
		cursor.consume(fragment)
		if fragment[len(fragment)-1] != '\n' {
			continue
		}
		line, oversized := cursor.pending, cursor.oversized
		cursor.offset, cursor.pending, cursor.oversized = cursor.read, nil, false
		var record ledgerRecord
		if oversized || json.Unmarshal(line, &record) != nil || record.Version != ledgerVersion || record.InstanceID != id || record.Seq == 0 || !validLedgerEvent(record.Event) {
			r.issue(id, "invalid_record")
			continue
		}
		last := r.sequences[id]
		if record.Seq <= last {
			continue
		}
		if record.Seq != last+1 {
			r.issue(id, "sequence_gap")
		}
		r.sequences[id] = record.Seq
		r.apply(id, record.Event)
	}
}

func (c *ledgerFileCursor) consume(fragment []byte) {
	c.read += int64(len(fragment))
	if len(fragment) >= len(c.guard) {
		copy(c.guard[:], fragment[len(fragment)-len(c.guard):])
		c.guardSize = len(c.guard)
	} else {
		keep := min(c.guardSize, len(c.guard)-len(fragment))
		copy(c.guard[:keep], c.guard[c.guardSize-keep:c.guardSize])
		copy(c.guard[keep:], fragment)
		c.guardSize = keep + len(fragment)
	}
	if c.read-c.offset > maxLedgerRecordBytes {
		c.pending, c.oversized = nil, true
	} else if !c.oversized {
		needed := len(c.pending) + len(fragment)
		if needed > cap(c.pending) {
			pending := make([]byte, len(c.pending), min(maxLedgerRecordBytes, max(needed, 2*cap(c.pending))))
			copy(pending, c.pending)
			c.pending = pending
		}
		c.pending = append(c.pending, fragment...)
	}
}

func (c *ledgerFileCursor) releasePending() {
	if len(c.pending) > 0 {
		c.pending, c.dropped = nil, true
	}
}

func (r *LedgerReader) apply(id string, event model.RequestEvent) {
	key := id + "/" + event.RequestID
	request := r.requests[key]
	if request == nil {
		if len(r.requests) >= maxLedgerRequests {
			r.issue(id, "request_retention_limit")
			return
		}
		request = &LedgerRequest{ID: event.RequestID, InstanceID: id, ProjectID: r.instances[id].ProjectID, Scope: event.Scope, Provider: event.Provider, Model: event.Model, StartedAt: event.Timestamp, Status: "running", Attempts: []LedgerAttempt{}}
		r.requests[key] = request
		if event.Kind != "request_start" {
			r.issue(id, "request_start_missing")
		}
	}
	request.UpdatedAt = event.Timestamp
	if event.Kind == "request_end" {
		request.Status = event.Status
		return
	}
	if event.Attempt == 0 {
		return
	}
	var attempt *LedgerAttempt
	for i := range request.Attempts {
		if request.Attempts[i].Number == event.Attempt {
			attempt = &request.Attempts[i]
			break
		}
	}
	if attempt == nil {
		if len(request.Attempts) >= 256 {
			r.issue(id, "attempt_retention_limit")
			return
		}
		request.Attempts = append(request.Attempts, LedgerAttempt{Number: event.Attempt, Transport: event.Transport, StartedAt: event.Timestamp, Status: "started"})
		attempt = &request.Attempts[len(request.Attempts)-1]
	}
	if event.ProviderResponseID != "" {
		attempt.ProviderResponseID = event.ProviderResponseID
	}
	if event.UsageFinality != "" {
		attempt.UsageFinality = event.UsageFinality
	}
	switch event.Kind {
	case "attempt_stream_end":
		if event.StreamDiagnostics != nil && attempt.StreamDiagnostics == nil {
			diagnostics := *event.StreamDiagnostics
			attempt.StreamDiagnostics = &diagnostics
		}
	case "attempt_response":
		if event.Status != "" {
			attempt.Status = event.Status
		}
	case "attempt_start":
		attempt.AtomsState, attempt.RequestBodyBytes = event.AtomsState, event.RequestBodyBytes
		attempt.toolTotal = summarizeToolAtoms(event.Atoms)
		attempt.toolSummarySet = true
		attempt.toolSummaryComplete = event.AtomsState == "complete"
		if r.atoms+len(event.Atoms) <= maxLedgerAtoms {
			attempt.Atoms = cloneInputAtoms(event.Atoms)
			r.atoms += len(event.Atoms)
		} else {
			attempt.AtomsState = "retention_limit"
			r.issue(id, "atom_retention_limit")
		}
	case "attempt_headers":
		attempt.HTTPStatus, attempt.Status = event.HTTPStatus, event.Status
	case "attempt_usage":
		if event.Usage != nil {
			previous := model.Usage{}
			if attempt.Usage != nil {
				previous = *attempt.Usage
			}
			usage := model.MergeUsageSnapshot(previous, *event.Usage)
			attempt.Usage = &usage
			if usage.Inconsistent() {
				r.issue(id, "usage_inconsistent")
			}
		}
	}
}

func summarizeToolAtoms(atoms []model.InputAtom) LedgerTool {
	var total LedgerTool
	for _, atom := range atoms {
		if atom.ToolName == "" {
			continue
		}
		total.Occurrences++
		if atom.EstimatedTokens == nil {
			total.Unknown++
			continue
		}
		switch atom.Category {
		case "tool_definition":
			total.Definitions += *atom.EstimatedTokens
		case "tool_arguments":
			total.Arguments += *atom.EstimatedTokens
		case "tool_result":
			total.Results += *atom.EstimatedTokens
		}
	}
	return total
}

func ledgerDirectory(path string, limit int) ([]os.DirEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func openLedgerFile(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid telemetry file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("telemetry file changed")
	}
	return file, nil
}

func cloneInputAtoms(atoms []model.InputAtom) []model.InputAtom {
	if atoms == nil {
		return nil
	}
	result := append([]model.InputAtom(nil), atoms...)
	for i := range result {
		if result[i].EstimatedTokens != nil {
			value := *result[i].EstimatedTokens
			result[i].EstimatedTokens = &value
		}
	}
	return result
}
