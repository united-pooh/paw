package tokentracer

import (
	"fmt"
	"paw/internal/capability/model"
	"sort"
	"strings"
	"time"
)

func (q LedgerQuery) normalized() (LedgerQuery, error) {
	if q.View == "" {
		q.View = "overview"
	}
	if q.Period == "" {
		q.Period = "24h"
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	switch q.View {
	case "overview", "projects", "sessions", "models", "tools", "requests":
	default:
		return q, fmt.Errorf("invalid view")
	}
	switch q.Period {
	case "24h", "7d", "30d", "all":
	default:
		return q, fmt.Errorf("invalid period")
	}
	if q.Offset < 0 || q.Offset > maxLedgerRequests || q.Limit < 1 || q.Limit > 250 {
		return q, fmt.Errorf("invalid pagination")
	}
	for _, value := range []string{q.Project, q.Session, q.Model, q.Tool, q.Search} {
		if len(value) > 512 {
			return q, fmt.Errorf("filter too long")
		}
	}
	return q, nil
}

func (q LedgerQuery) duration() time.Duration {
	switch q.Period {
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func (r *LedgerReader) matching(q LedgerQuery, now time.Time) []*LedgerRequest {
	items := make([]*LedgerRequest, 0, len(r.requests))
	search := strings.ToLower(q.Search)
	cutoff := now.Add(-q.duration())
	for _, request := range r.requests {
		if q.Project != "" && request.ProjectID != q.Project || q.Model != "" && request.Model != q.Model || q.Session != "" && request.ProjectID+"/"+request.Scope.SessionID != q.Session || q.Period != "all" && request.StartedAt.Before(cutoff) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(request.ID+" "+request.Scope.SessionID+" "+request.Model+" "+request.Provider+" "+request.Scope.TaskID), search) {
			continue
		}
		if q.Tool != "" && !requestHasTool(request, q.Tool) {
			continue
		}
		items = append(items, request)
	}
	return items
}

func requestHasTool(request *LedgerRequest, name string) bool {
	for _, attempt := range request.Attempts {
		for _, atom := range attempt.Atoms {
			if atom.ToolName == name {
				return true
			}
		}
	}
	return false
}

func requestStates(instances []LedgerInstance) map[string]string {
	states := make(map[string]string, len(instances))
	for _, instance := range instances {
		states[instance.ID] = instance.Status
	}
	return states
}

func requestStatus(request *LedgerRequest, states map[string]string) string {
	if request.Status == "running" && states[request.InstanceID] != "recent" {
		return "interrupted"
	}
	return request.Status
}

func summarizeRequest(request *LedgerRequest, status string) RequestSummary {
	s := RequestSummary{Requests: 1, Attempts: len(request.Attempts)}
	if status == "running" {
		s.Running = 1
	}
	for _, attempt := range request.Attempts {
		if attempt.Usage == nil {
			s.Unknown++
			continue
		}
		known := attempt.Usage.Knowledge()
		if !known.Input || !known.Output || attempt.UsageFinality != "final" {
			s.Unknown++
		}
		if known.Input {
			s.InputKnown++
		}
		if known.Output {
			s.OutputKnown++
		}
		if known.CacheRead {
			s.CacheKnown++
		}
		if known.Input || known.Output || known.Total || known.CacheRead || known.CacheCreation || known.Reasoning {
			s.Reported++
		}
	}
	tokens, input := requestUsageTotal(request)
	s.Total, s.Input, s.Output = tokens.Total(), input, tokens.Output
	s.CacheRead, s.CacheCreation, s.Reasoning = tokens.CacheRead, tokens.CacheCreation, tokens.Reasoning
	return s
}

// Response IDs only join snapshots inside one logical request and transport.
// Raw attempts and repeated input exposures remain intact for inspection.
func requestUsageTotal(request *LedgerRequest) (model.UsageBreakdown, int) {
	var total model.UsageBreakdown
	input := 0
	for i, attempt := range request.Attempts {
		if attempt.Usage == nil {
			continue
		}
		usage := *attempt.Usage
		if attempt.ProviderResponseID != "" {
			later := false
			for _, next := range request.Attempts[i+1:] {
				if next.Usage != nil && next.ProviderResponseID == attempt.ProviderResponseID && next.Transport == attempt.Transport {
					later = true
					break
				}
			}
			if later {
				continue
			}
			usage = model.Usage{}
			for _, earlier := range request.Attempts[:i+1] {
				if earlier.Usage != nil && earlier.ProviderResponseID == attempt.ProviderResponseID && earlier.Transport == attempt.Transport {
					usage = model.MergeUsageSnapshot(usage, *earlier.Usage)
				}
			}
		}
		tokens := usage.Breakdown()
		total = total.Add(tokens)
		if usage.Knowledge().Input {
			input += tokens.Input + tokens.CacheRead + tokens.CacheCreation
		}
	}
	return total, input
}

func (r *LedgerReader) Query(query LedgerQuery) (LedgerPage, error) {
	q, err := query.normalized()
	if err != nil {
		return LedgerPage{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, err := r.scanLocked()
	if err != nil {
		return LedgerPage{}, err
	}
	items := r.matching(q, meta.GeneratedAt)
	states := requestStates(meta.Instances)
	page := LedgerPage{Version: globalQueryVersion, GeneratedAt: meta.GeneratedAt, Instances: meta.Instances, Issues: meta.Issues, Query: q, StoredRequests: len(r.requests), Requests: []LedgerRequestRow{}, Groups: []LedgerGroup{}, Tools: []LedgerTool{}, Trend: make([]LedgerTrendBin, 24)}
	span := q.duration()
	if q.Period == "all" {
		for _, item := range items {
			span = max(span, meta.GeneratedAt.Sub(item.StartedAt))
		}
	}
	start := meta.GeneratedAt.Add(-span).UnixMilli()
	for i := range page.Trend {
		page.Trend[i].Time = start + int64(i)*span.Milliseconds()/24
	}
	projects := map[string]bool{}
	groups := map[string]*LedgerGroup{}
	toolRows := map[string]*LedgerTool{}
	for _, item := range items {
		summary := summarizeRequest(item, requestStatus(item, states))
		page.Summary = page.Summary.add(summary)
		projects[item.ProjectID] = true
		index := min(23, max(0, int((item.StartedAt.UnixMilli()-start)*24/span.Milliseconds())))
		page.Trend[index].Total += summary.Total
		page.Trend[index].Count++
		switch q.View {
		case "overview", "projects", "sessions", "models":
			id, label := item.ProjectID, item.ProjectID
			if q.View == "sessions" {
				id, label = item.ProjectID+"/"+item.Scope.SessionID, item.Scope.SessionID
			}
			if q.View == "models" {
				id, label = item.Model, item.Model
			}
			group := groups[id]
			if group == nil {
				group = &LedgerGroup{ID: id, Label: label, ProjectID: item.ProjectID}
				groups[id] = group
			}
			group.Summary = group.Summary.add(summary)
		case "tools":
			for _, attempt := range item.Attempts {
				for _, atom := range attempt.Atoms {
					if atom.ToolName == "" {
						continue
					}
					row := toolRows[atom.ToolName]
					if row == nil {
						row = &LedgerTool{Name: atom.ToolName}
						toolRows[atom.ToolName] = row
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
	}
	page.ProjectCount = len(projects)
	for _, instance := range meta.Instances {
		if projects[instance.ProjectID] && instance.Status == "recent" {
			page.ActiveInstances++
		}
	}
	switch q.View {
	case "requests":
		sortRequests(items)
		page.Pagination = pagination(q, len(items))
		for _, item := range items[page.Pagination.Offset:min(page.Pagination.Offset+q.Limit, len(items))] {
			status := requestStatus(item, states)
			page.Requests = append(page.Requests, LedgerRequestRow{ID: item.ID, InstanceID: item.InstanceID, ProjectID: item.ProjectID, Model: item.Model, StartedAt: item.StartedAt, Status: status, Purpose: item.Scope.Purpose, Summary: summarizeRequest(item, status)})
		}
	case "tools":
		rows := make([]LedgerTool, 0, len(toolRows))
		for _, row := range toolRows {
			rows = append(rows, *row)
		}
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			x, y := a.Definitions+a.Arguments+a.Results, b.Definitions+b.Arguments+b.Results
			if x == y {
				return a.Name < b.Name
			}
			return x > y
		})
		page.Pagination = pagination(q, len(rows))
		page.Tools = rows[page.Pagination.Offset:min(page.Pagination.Offset+q.Limit, len(rows))]
	default:
		rows := make([]LedgerGroup, 0, len(groups))
		for _, row := range groups {
			rows = append(rows, *row)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Summary.Total == rows[j].Summary.Total {
				return rows[i].ID < rows[j].ID
			}
			return rows[i].Summary.Total > rows[j].Summary.Total
		})
		page.Pagination = pagination(q, len(rows))
		page.Groups = rows[page.Pagination.Offset:min(page.Pagination.Offset+q.Limit, len(rows))]
	}
	return page, nil
}

func pagination(q LedgerQuery, count int) LedgerPagination {
	offset := q.Offset
	if count == 0 {
		offset = 0
	} else if offset >= count {
		offset = (count - 1) / q.Limit * q.Limit
	}
	return LedgerPagination{Offset: offset, Limit: q.Limit, Total: count}
}

func sortRequests(items []*LedgerRequest) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.StartedAt.Equal(b.StartedAt) {
			if a.InstanceID == b.InstanceID {
				return a.ID < b.ID
			}
			return a.InstanceID < b.InstanceID
		}
		return a.StartedAt.After(b.StartedAt)
	})
}

func cloneRequest(request *LedgerRequest, states map[string]string) LedgerRequest {
	result := *request
	result.Status = requestStatus(request, states)
	result.Summary = summarizeRequest(request, result.Status)
	result.Attempts = make([]LedgerAttempt, len(request.Attempts))
	for i, attempt := range request.Attempts {
		result.Attempts[i] = attempt
		if attempt.StreamDiagnostics != nil {
			diagnostics := *attempt.StreamDiagnostics
			result.Attempts[i].StreamDiagnostics = &diagnostics
		}
		result.Attempts[i].Atoms = cloneInputAtoms(attempt.Atoms)
		if attempt.Usage != nil {
			usage := *attempt.Usage
			tokens, known := usage.Breakdown(), usage.Knowledge()
			result.Attempts[i].Usage, result.Attempts[i].Tokens, result.Attempts[i].Known = &usage, &tokens, known
			result.Attempts[i].UsageSource, result.Attempts[i].Precision = "provider_reported", "provider_defined"
		}
	}
	return result
}

func snapshotRequests(snapshot LedgerSnapshot, items []*LedgerRequest) LedgerSnapshot {
	states := requestStates(snapshot.Instances)
	sortRequests(items)
	for _, item := range items {
		request := cloneRequest(item, states)
		total, _ := requestUsageTotal(item)
		snapshot.Total = snapshot.Total.Add(total)
		for _, attempt := range request.Attempts {
			snapshot.Coverage.Attempts++
			if attempt.Usage != nil {
				if attempt.Known.Input || attempt.Known.Output || attempt.Known.Total {
					snapshot.Coverage.ReportedAttempts++
				}
			}
		}
		snapshot.Requests = append(snapshot.Requests, request)
	}
	snapshot.Coverage.Requests = len(items)
	return snapshot
}

func (r *LedgerReader) Request(instanceID, requestID string) (*LedgerRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, err := r.scanLocked()
	if err != nil {
		return nil, err
	}
	item := r.requests[instanceID+"/"+requestID]
	if item == nil || item.InstanceID != instanceID || item.ID != requestID {
		return nil, nil
	}
	result := cloneRequest(item, requestStates(meta.Instances))
	return &result, nil
}

func (r *LedgerReader) Export(query LedgerQuery) (LedgerSnapshot, error) {
	q, err := query.normalized()
	if err != nil {
		return LedgerSnapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	meta, err := r.scanLocked()
	if err != nil {
		return LedgerSnapshot{}, err
	}
	items := r.matching(q, meta.GeneratedAt)
	ids := make(map[string]bool)
	for _, item := range items {
		ids[item.InstanceID] = true
	}
	instances := meta.Instances[:0]
	for _, instance := range meta.Instances {
		if ids[instance.ID] {
			instances = append(instances, instance)
		}
	}
	meta.Instances = instances
	issues := meta.Issues[:0]
	for _, issue := range meta.Issues {
		if issue.InstanceID == "" || ids[issue.InstanceID] {
			issues = append(issues, issue)
		}
	}
	meta.Issues = issues
	return snapshotRequests(meta, items), nil
}
