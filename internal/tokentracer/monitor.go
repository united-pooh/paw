package tokentracer

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MonitorSnapshot struct {
	SessionID          string           `json:"session_id"`
	GeneratedAt        time.Time        `json:"generated_at"`
	Summary            RequestSummary   `json:"summary"`
	CacheCreationKnown int              `json:"cache_creation_known"`
	ReasoningKnown     int              `json:"reasoning_known"`
	ToolTotal          LedgerTool       `json:"tool_total"`
	ToolsKnown         int              `json:"tools_known"`
	Tools              []LedgerTool     `json:"tools"`
	Requests           []MonitorRequest `json:"requests"`
	Issues             []LedgerIssue    `json:"issues"`
}

type MonitorRequest struct {
	LedgerRequestRow
	SessionID string    `json:"session_id"`
	TaskID    string    `json:"task_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *LedgerReader) Monitor(workspace, sessionID string) (MonitorSnapshot, error) {
	if r == nil || workspace == "" || strings.TrimSpace(sessionID) == "" {
		return MonitorSnapshot{}, fmt.Errorf("monitor workspace and session are required")
	}
	canonical, err := filepath.Abs(workspace)
	if err == nil {
		canonical, err = filepath.EvalSymlinks(canonical)
	}
	if err != nil {
		return MonitorSnapshot{}, fmt.Errorf("resolve monitor workspace: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, err := r.scanLocked()
	if err != nil {
		return MonitorSnapshot{}, err
	}
	instances := make(map[string]bool)
	for _, instance := range meta.Instances {
		if instance.Workspace == canonical {
			instances[instance.ID] = true
		}
	}
	snapshot := MonitorSnapshot{
		SessionID: sessionID, GeneratedAt: meta.GeneratedAt,
		Tools: []LedgerTool{}, Requests: []MonitorRequest{}, Issues: []LedgerIssue{},
	}
	items := r.monitorRequests(instances, sessionID)
	sortRequests(items)
	states := requestStates(meta.Instances)
	tools := make(map[string]*LedgerTool)
	limitedDetails := make(map[string]bool)
	for i, request := range items {
		status := requestStatus(request, states)
		summary := summarizeRequest(request, status)
		snapshot.Summary = snapshot.Summary.add(summary)
		if i < 12 {
			snapshot.Requests = append(snapshot.Requests, MonitorRequest{
				LedgerRequestRow: LedgerRequestRow{ID: request.ID, InstanceID: request.InstanceID,
					ProjectID: request.ProjectID, Model: request.Model, StartedAt: request.StartedAt,
					Status: status, Purpose: request.Scope.Purpose, Summary: summary},
				SessionID: request.Scope.SessionID, TaskID: request.Scope.TaskID, UpdatedAt: request.UpdatedAt,
			})
		}
		for _, attempt := range request.Attempts {
			if attempt.AtomsState == "retention_limit" {
				limitedDetails[request.InstanceID] = true
			}
			total, complete := attempt.toolTotal, attempt.toolSummaryComplete
			if !attempt.toolSummarySet {
				total, complete = summarizeToolAtoms(attempt.Atoms), attempt.AtomsState == "complete"
			}
			if complete {
				snapshot.ToolsKnown++
			}
			snapshot.ToolTotal.Occurrences += total.Occurrences
			snapshot.ToolTotal.Definitions += total.Definitions
			snapshot.ToolTotal.Arguments += total.Arguments
			snapshot.ToolTotal.Results += total.Results
			snapshot.ToolTotal.Unknown += total.Unknown
			// Coverage counts raw attempts, like RequestSummary; token totals deduplicate response snapshots.
			if attempt.Usage != nil {
				known := attempt.Usage.Knowledge()
				if known.CacheCreation {
					snapshot.CacheCreationKnown++
				}
				if known.Reasoning {
					snapshot.ReasoningKnown++
				}
			}
			for _, atom := range attempt.Atoms {
				if atom.ToolName == "" {
					continue
				}
				row := tools[atom.ToolName]
				if row == nil {
					row = &LedgerTool{Name: atom.ToolName}
					tools[atom.ToolName] = row
				}
				row.Occurrences++
				if atom.EstimatedTokens == nil {
					row.Unknown++
					continue
				}
				switch atom.Category {
				case "tool_definition":
					row.Definitions += *atom.EstimatedTokens
				case "tool_arguments":
					row.Arguments += *atom.EstimatedTokens
				case "tool_result":
					row.Results += *atom.EstimatedTokens
				}
			}
		}
	}
	for _, issue := range meta.Issues {
		if issue.InstanceID != "" && !instances[issue.InstanceID] {
			continue
		}
		if issue.Kind == "atom_retention_limit" && !limitedDetails[issue.InstanceID] {
			continue
		}
		snapshot.Issues = append(snapshot.Issues, issue)
	}
	for _, tool := range tools {
		snapshot.Tools = append(snapshot.Tools, *tool)
	}
	sort.Slice(snapshot.Tools, func(i, j int) bool {
		a, b := snapshot.Tools[i], snapshot.Tools[j]
		x, y := a.Definitions+a.Arguments+a.Results, b.Definitions+b.Arguments+b.Results
		if x == y {
			return a.Name < b.Name
		}
		return x > y
	})
	if len(snapshot.Tools) > 12 {
		snapshot.Tools = snapshot.Tools[:12:12]
	}
	return snapshot, nil
}

func (r *LedgerReader) monitorRequests(instances map[string]bool, sessionID string) []*LedgerRequest {
	type identity struct {
		id   string
		task bool
	}
	index := make(map[identity][]*LedgerRequest)
	for _, request := range r.requests {
		if !instances[request.InstanceID] {
			continue
		}
		scope := request.Scope
		for _, key := range []identity{{id: scope.SessionID}, {id: scope.ParentSessionID}, {id: scope.TaskID, task: true}, {id: scope.ParentTaskID, task: true}} {
			if key.id != "" {
				index[key] = append(index[key], request)
			}
		}
	}
	root := identity{id: sessionID}
	queue := []identity{root}
	visited := map[identity]bool{root: true}
	selected := make(map[*LedgerRequest]bool)
	items := make([]*LedgerRequest, 0)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, request := range index[key] {
			if selected[request] {
				continue
			}
			selected[request] = true
			items = append(items, request)
			for _, child := range []identity{{id: request.Scope.SessionID}, {id: request.Scope.TaskID, task: true}} {
				if child.id != "" && !visited[child] {
					visited[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
	return items
}
