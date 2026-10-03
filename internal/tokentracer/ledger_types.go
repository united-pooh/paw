package tokentracer

import (
	"paw/internal/capability/model"
	"time"
)

const ledgerVersion = 1
const maxLedgerRecordBytes = 2 << 20
const maxLedgerSegmentBytes = 16 << 20
const maxLedgerSegments = 128
const maxLedgerInstances = 4096
const maxLedgerRequests = 100000
const maxLedgerAtoms = 100000

type InstanceMetadata struct {
	Version          int        `json:"version"`
	ID               string     `json:"id"`
	ProjectID        string     `json:"project_id"`
	ProjectName      string     `json:"project_name"`
	Workspace        string     `json:"workspace"`
	ParentInstanceID string     `json:"parent_instance_id,omitempty"`
	PID              int        `json:"pid"`
	StartedAt        time.Time  `json:"started_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StoppedAt        *time.Time `json:"stopped_at,omitempty"`
	Error            string     `json:"error,omitempty"`
}

type LedgerInstance struct {
	InstanceMetadata
	Status string `json:"status"`
}

type ledgerRecord struct {
	Version    int                `json:"version"`
	InstanceID string             `json:"instance_id"`
	Seq        uint64             `json:"seq"`
	Event      model.RequestEvent `json:"event"`
}

type LedgerAttempt struct {
	ProviderResponseID  string                            `json:"provider_response_id,omitempty"`
	UsageFinality       string                            `json:"usage_finality,omitempty"`
	Number              int                               `json:"number"`
	Transport           string                            `json:"transport"`
	StartedAt           time.Time                         `json:"started_at"`
	Status              string                            `json:"status"`
	HTTPStatus          int                               `json:"http_status,omitempty"`
	Usage               *model.Usage                      `json:"usage,omitempty"`
	Tokens              *model.UsageBreakdown             `json:"tokens,omitempty"`
	Known               model.UsageKnowledge              `json:"known"`
	UsageSource         string                            `json:"usage_source,omitempty"`
	Precision           string                            `json:"precision,omitempty"`
	Atoms               []model.InputAtom                 `json:"atoms,omitempty"`
	AtomsState          string                            `json:"atoms_state,omitempty"`
	RequestBodyBytes    int64                             `json:"request_body_bytes,omitempty"`
	StreamDiagnostics   *model.ResponsesStreamDiagnostics `json:"stream_diagnostics,omitempty"`
	toolTotal           LedgerTool
	toolSummarySet      bool
	toolSummaryComplete bool
}

type LedgerRequest struct {
	Summary    RequestSummary     `json:"summary"`
	ID         string             `json:"id"`
	InstanceID string             `json:"instance_id"`
	ProjectID  string             `json:"project_id"`
	Scope      model.RequestScope `json:"scope"`
	Provider   string             `json:"provider"`
	Model      string             `json:"model"`
	StartedAt  time.Time          `json:"started_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Status     string             `json:"status"`
	Attempts   []LedgerAttempt    `json:"attempts"`
}

type LedgerCoverage struct {
	Requests         int `json:"requests"`
	Attempts         int `json:"attempts"`
	ReportedAttempts int `json:"reported_attempts"`
}

type LedgerIssue struct {
	InstanceID string `json:"instance_id,omitempty"`
	Kind       string `json:"kind"`
}

type LedgerSnapshot struct {
	Version     int                  `json:"version"`
	GeneratedAt time.Time            `json:"generated_at"`
	Instances   []LedgerInstance     `json:"instances"`
	Requests    []LedgerRequest      `json:"requests"`
	Total       model.UsageBreakdown `json:"total"`
	Coverage    LedgerCoverage       `json:"coverage"`
	Issues      []LedgerIssue        `json:"issues"`
}
