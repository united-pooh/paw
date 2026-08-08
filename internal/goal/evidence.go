package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type EvidenceKind string

const (
	EvidenceTestPassed       EvidenceKind = "test_passed"
	EvidenceBuildPassed      EvidenceKind = "build_passed"
	EvidenceLintPassed       EvidenceKind = "lint_passed"
	EvidenceCommandSucceeded EvidenceKind = "command_succeeded"
	EvidenceReviewPassed     EvidenceKind = "review_passed"
	EvidenceFileChanged      EvidenceKind = "file_changed"
	EvidenceUserApproved     EvidenceKind = "user_approved"
)

type EvidenceStatus string

const (
	EvidencePassed EvidenceStatus = "passed"
	EvidenceFailed EvidenceStatus = "failed"
	EvidenceStale  EvidenceStatus = "stale"
)

type VerificationSpec struct {
	ID       string
	Kind     EvidenceKind
	Command  string
	Required bool
	Scope    []string
}
type Evidence struct {
	ID        string
	GoalID    GoalID
	StepID    string
	Kind      EvidenceKind
	Command   string
	Status    EvidenceStatus
	Summary   string
	Scope     []string
	Digest    string
	CreatedAt time.Time
	Stale     bool
}

func (e Evidence) IsStale(currentDigest string) bool {
	return e.Stale || e.Status == EvidenceStale || (e.Digest != "" && currentDigest != "" && e.Digest != currentDigest)
}
func EvidenceDigest(scope []string, contents map[string]string) string {
	keys := append([]string(nil), scope...)
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(contents[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (e Evidence) Clone() Evidence { e.Scope = append([]string(nil), e.Scope...); return e }

type EvidenceStore interface {
	Add(context.Context, Evidence) error
	Get(context.Context, string) (Evidence, bool, error)
	ListByGoal(context.Context, GoalID) ([]Evidence, error)
	MarkStaleByChangedFiles(context.Context, []string) error
}
type MemoryEvidenceStore struct {
	mu    sync.RWMutex
	items map[string]Evidence
}

func NewMemoryEvidenceStore() *MemoryEvidenceStore {
	return &MemoryEvidenceStore{items: map[string]Evidence{}}
}
func (s *MemoryEvidenceStore) Add(ctx context.Context, e Evidence) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if e.ID == "" {
		return errors.New("evidence id is empty")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; ok {
		return errors.New("evidence already exists")
	}
	s.items[e.ID] = e.Clone()
	return nil
}
func (s *MemoryEvidenceStore) Get(ctx context.Context, id string) (Evidence, bool, error) {
	if err := contextErr(ctx); err != nil {
		return Evidence{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.items[id]
	return e.Clone(), ok, nil
}
func (s *MemoryEvidenceStore) ListByGoal(ctx context.Context, id GoalID) ([]Evidence, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Evidence{}
	for _, e := range s.items {
		if e.GoalID == id {
			out = append(out, e.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryEvidenceStore) MarkStaleByChangedFiles(ctx context.Context, changed []string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.items {
		for _, a := range changed {
			for _, b := range e.Scope {
				if strings.TrimSpace(a) == strings.TrimSpace(b) {
					e.Stale = true
					e.Status = EvidenceStale
					s.items[id] = e
					break
				}
			}
		}
	}
	return nil
}
func RequiredEvidenceComplete(specs []VerificationSpec, evidence []Evidence) bool {
	for _, s := range specs {
		if !s.Required {
			continue
		}
		found := false
		for _, e := range evidence {
			if e.Kind == s.Kind && e.Status == EvidencePassed && !e.IsStale("") {
				if s.ID == "" || e.ID == s.ID {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func AnyStaleEvidence(evidence []Evidence) bool {
	for _, e := range evidence {
		if e.Stale {
			return true
		}
	}
	return false
}

var _ EvidenceStore = (*MemoryEvidenceStore)(nil)
var _ = strings.Builder{}

type AcceptanceResult struct {
	Passed bool
	Reason string
}
