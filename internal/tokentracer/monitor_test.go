package tokentracer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/capability/model"
)

func monitorFixture(t *testing.T) (*LedgerReader, string) {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewLedgerReader(t.TempDir())
	r.instances["main"] = InstanceMetadata{ID: "main", Workspace: workspace, ProjectID: "project"}
	return r, workspace
}

func addMonitorRequest(r *LedgerReader, instance, id string, scope model.RequestScope) *LedgerRequest {
	now := time.Now().UTC()
	request := &LedgerRequest{
		ID: id, InstanceID: instance, ProjectID: r.instances[instance].ProjectID, Scope: scope,
		Provider: "fixture", Model: "model", StartedAt: now, UpdatedAt: now, Status: "completed",
		Attempts: []LedgerAttempt{{Number: 1, UsageFinality: "final", Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5}}},
	}
	r.requests[instance+"/"+id] = request
	return request
}

func monitorUsage(t *testing.T, raw string) *model.Usage {
	t.Helper()
	var usage model.Usage
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		t.Fatal(err)
	}
	return &usage
}

func TestMonitorScopesCanonicalWorkspaceAndRecursiveDescendants(t *testing.T) {
	r, workspace := monitorFixture(t)
	r.instances["worker"] = InstanceMetadata{ID: "worker", Workspace: workspace, ProjectID: "project"}
	r.instances["other-project"] = InstanceMetadata{ID: "other-project", Workspace: t.TempDir(), ProjectID: "other"}
	addMonitorRequest(r, "main", "root", model.RequestScope{SessionID: "root"})
	addMonitorRequest(r, "worker", "child", model.RequestScope{SessionID: "child-session", ParentSessionID: "root", TaskID: "child-task"})
	addMonitorRequest(r, "worker", "child-followup", model.RequestScope{SessionID: "child-session"})
	addMonitorRequest(r, "worker", "nested-session", model.RequestScope{SessionID: "nested-session", ParentSessionID: "child-session", TaskID: "nested-task"})
	addMonitorRequest(r, "worker", "nested-task", model.RequestScope{SessionID: "deep-session", TaskID: "deep-task", ParentTaskID: "nested-task"})
	addMonitorRequest(r, "worker", "task-followup", model.RequestScope{TaskID: "deep-task"})
	addMonitorRequest(r, "main", "other-session", model.RequestScope{SessionID: "unrelated"})
	addMonitorRequest(r, "main", "unanchored-cycle-a", model.RequestScope{SessionID: "cycle-a", ParentSessionID: "cycle-b"})
	addMonitorRequest(r, "main", "unanchored-cycle-b", model.RequestScope{SessionID: "cycle-b", ParentSessionID: "cycle-a"})
	addMonitorRequest(r, "main", "empty", model.RequestScope{})
	addMonitorRequest(r, "other-project", "foreign-root", model.RequestScope{SessionID: "root"})
	addMonitorRequest(r, "other-project", "foreign-child", model.RequestScope{ParentSessionID: "root", ParentTaskID: "deep-task"})
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	got, err := r.Monitor(alias, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "root" || got.GeneratedAt.IsZero() || got.Summary.Requests != 6 || got.Summary.Total != 630 || len(got.Requests) != 6 {
		t.Fatalf("scope lost or widened: %+v", got)
	}
	want := map[string]bool{"root": true, "child": true, "child-followup": true, "nested-session": true, "nested-task": true, "task-followup": true}
	for _, request := range got.Requests {
		if !want[request.ID] || request.ProjectID != "project" || request.UpdatedAt.IsZero() {
			t.Fatalf("unexpected request: %+v", request)
		}
		delete(want, request.ID)
		if request.ID == "nested-task" && (request.SessionID != "deep-session" || request.TaskID != "deep-task") {
			t.Fatalf("request scope missing: %+v", request)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing descendants: %v", want)
	}
}

func TestMonitorEmptyScopeAndScanErrorsDoNotReturnGlobalData(t *testing.T) {
	r, workspace := monitorFixture(t)
	addMonitorRequest(r, "main", "root", model.RequestScope{SessionID: "root"})
	for _, scope := range [][2]string{{"", "root"}, {workspace, ""}, {workspace, " \t "}, {filepath.Join(workspace, "missing"), "root"}} {
		got, err := r.Monitor(scope[0], scope[1])
		if err == nil || got.Summary.Requests != 0 || len(got.Requests) != 0 {
			t.Fatalf("invalid scope returned data: %+v err=%v", got, err)
		}
	}
	r.root = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(r.root, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Monitor(workspace, "root"); err == nil || got.Summary.Requests != 0 {
		t.Fatalf("scan failure hidden: %+v err=%v", got, err)
	}
}

func TestMonitorRetryIdentityAndAttemptCoverageMatchLedger(t *testing.T) {
	for _, tc := range []struct {
		name, response, transport string
		wantTotal                 int
	}{
		{"same response", "response", "responses", 83},
		{"different response", "other", "responses", 188},
		{"missing response", "", "responses", 188},
		{"different transport", "response", "chat", 188},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, workspace := monitorFixture(t)
			request := addMonitorRequest(r, "main", "root", model.RequestScope{SessionID: "root"})
			request.Attempts = []LedgerAttempt{
				{Number: 1, Transport: "responses", ProviderResponseID: "response", UsageFinality: "partial", Usage: monitorUsage(t, `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":40,"cache_creation_tokens":10},"completion_tokens_details":{"reasoning_tokens":2}}`)},
				{Number: 2, Transport: tc.transport, ProviderResponseID: tc.response, UsageFinality: "final", Usage: monitorUsage(t, `{"prompt_tokens":80,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":0,"cache_creation_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`)},
				{Number: 3, Transport: "responses"},
			}
			got, err := r.Monitor(workspace, "root")
			if err != nil {
				t.Fatal(err)
			}
			want := summarizeRequest(request, "completed")
			if got.Summary != want || got.Requests[0].Summary != want || got.Summary.Total != tc.wantTotal {
				t.Fatalf("retry totals diverged: %+v want=%+v", got, want)
			}
			if got.Summary.Attempts != 3 || got.Summary.Unknown != 2 || got.Summary.Reported != 2 || got.CacheCreationKnown != 2 || got.ReasoningKnown != 2 {
				t.Fatalf("attempt coverage lost: %+v", got)
			}
		})
	}
}

func TestMonitorMissingZeroAndInvalidSubsetRemainDistinct(t *testing.T) {
	r, workspace := monitorFixture(t)
	for i, raw := range []string{
		`{"prompt_tokens":100,"completion_tokens":5}`,
		`{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0,"cache_creation_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`,
		`{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":130,"cache_creation_tokens":20},"completion_tokens_details":{"reasoning_tokens":8}}`,
	} {
		request := addMonitorRequest(r, "main", fmt.Sprint(i), model.RequestScope{SessionID: "root"})
		request.Attempts[0].Usage = monitorUsage(t, raw)
	}
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Total != 315 || got.Summary.CacheKnown != 1 || got.CacheCreationKnown != 1 || got.ReasoningKnown != 1 || got.Summary.Reasoning != 0 {
		t.Fatalf("missing or invalid fields counted as known zero: %+v", got)
	}
}

func TestMonitorBoundsRowsButAggregatesAllInputExposures(t *testing.T) {
	r, workspace := monitorFixture(t)
	base := time.Now().UTC()
	for i := 0; i < 20; i++ {
		request := addMonitorRequest(r, "main", fmt.Sprintf("request-%02d", i), model.RequestScope{SessionID: "root"})
		request.StartedAt = base.Add(time.Duration(i) * time.Second)
		estimate := i + 1
		request.Attempts[0].Atoms = []model.InputAtom{{ToolName: fmt.Sprintf("Tool-%02d", i), Category: "tool_result", EstimatedTokens: &estimate}}
	}
	request := r.requests["main/request-19"]
	definition, arguments, result := 2, 3, 4
	request.Attempts[0].Atoms = append(request.Attempts[0].Atoms,
		model.InputAtom{ToolName: "Read", Category: "tool_definition", EstimatedTokens: &definition},
		model.InputAtom{ToolName: "Read", Category: "tool_arguments", EstimatedTokens: &arguments},
		model.InputAtom{ToolName: "Read", Category: "tool_result", EstimatedTokens: &result},
		model.InputAtom{ToolName: "Read", Category: "multimodal"},
	)
	request.Attempts = append(request.Attempts, request.Attempts[0])
	request.Attempts[0].ProviderResponseID, request.Attempts[1].ProviderResponseID = "replay", "replay"
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Requests != 20 || got.Summary.Total != 2100 || len(got.Requests) != 12 || len(got.Tools) != 12 {
		t.Fatalf("bounded rows changed totals or tool estimate was billed twice: %+v", got)
	}
	if got.Requests[0].ID != "request-19" || got.Requests[11].ID != "request-08" || got.Tools[0].Name != "Tool-19" || got.Tools[0].Results != 40 {
		t.Fatalf("ordering or repeated input exposures lost: %+v", got)
	}
	var read LedgerTool
	for _, tool := range got.Tools {
		if tool.Name == "Read" {
			read = tool
		}
	}
	if read != (LedgerTool{Name: "Read", Occurrences: 8, Definitions: 4, Arguments: 6, Results: 8, Unknown: 2}) {
		t.Fatalf("tool input buckets differ: %+v", read)
	}
	if got.ToolTotal != (LedgerTool{Occurrences: 29, Definitions: 4, Arguments: 6, Results: 238, Unknown: 2}) {
		t.Fatalf("tool totals were truncated with visible rows: %+v", got.ToolTotal)
	}
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"atoms"`, `"usage"`, `"attempts":[`, `"request_body_bytes"`} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("monitor leaks raw details: %s", forbidden)
		}
	}
	got.Tools[0].Results = 999
	again, err := r.Monitor(workspace, "root")
	if err != nil || again.Tools[0].Results != 40 {
		t.Fatalf("monitor result aliases reader state: %+v %v", again, err)
	}
}

func TestMonitorScopesIssuesAndReportsScanBacklog(t *testing.T) {
	r, workspace := monitorFixture(t)
	addMonitorRequest(r, "main", "root", model.RequestScope{SessionID: "root"})
	r.instances["foreign"] = InstanceMetadata{ID: "foreign", Workspace: t.TempDir(), ProjectID: "foreign"}
	r.issue("main", "usage_inconsistent")
	r.issue("foreign", "private-project-issue")
	r.issue("", "instance_limit")
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	want := []LedgerIssue{{Kind: "instance_limit"}, {InstanceID: "main", Kind: "usage_inconsistent"}}
	if !reflect.DeepEqual(got.Issues, want) {
		t.Fatalf("issues lost or crossed workspace: %+v", got.Issues)
	}
	instanceDir := filepath.Join(r.root, "main")
	if err := os.MkdirAll(instanceDir, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := r.instances["main"]
	metadata.Version = ledgerVersion
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instanceDir, "meta.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		file, err := os.Create(filepath.Join(instanceDir, fmt.Sprintf("events-%06d.jsonl", i)))
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(maxLedgerSegmentBytes)
		if err == nil {
			_, err = file.WriteAt([]byte{'\n'}, maxLedgerSegmentBytes-1)
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("create sparse backlog: %v %v", err, closeErr)
		}
	}
	got, err = r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range got.Issues {
		if issue.Kind == "scan_backlog" {
			return
		}
	}
	t.Fatalf("scan backlog hidden: %+v", got.Issues)
}

func TestMonitorReadsLiveLedgerSnapshotsAndInterruptedRequests(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	r := NewLedgerReader(home)
	base := time.Now().UTC()
	emit := func(event model.RequestEvent) {
		t.Helper()
		event.Scope = model.RequestScope{SessionID: "root", Purpose: "conversation"}
		if event.Timestamp.IsZero() {
			event.Timestamp = base
		}
		if err := recorder.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	emit(model.RequestEvent{Kind: "request_start", RequestID: "live"})
	emit(model.RequestEvent{Kind: "attempt_start", RequestID: "live", Attempt: 1})
	emit(model.RequestEvent{Kind: "attempt_usage", RequestID: "live", Attempt: 1, UsageFinality: "partial", Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5}})
	got, err := r.Monitor(workspace, "root")
	if err != nil || got.Summary.Total != 105 || got.Summary.Running != 1 || got.Summary.Unknown != 1 {
		t.Fatalf("live snapshot lost: %+v %v", got, err)
	}
	emit(model.RequestEvent{Kind: "attempt_usage", RequestID: "live", Attempt: 1, UsageFinality: "final", Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 9}})
	emit(model.RequestEvent{Kind: "request_end", RequestID: "live", Status: "completed", Timestamp: base.Add(time.Second)})
	got, err = r.Monitor(workspace, "root")
	if err != nil || got.Summary.Total != 109 || got.Summary.Running != 0 || got.Summary.Unknown != 0 || got.Requests[0].UpdatedAt != base.Add(time.Second) {
		t.Fatalf("incremental ledger snapshot was summed or remained partial: %+v %v", got, err)
	}
	emit(model.RequestEvent{Kind: "request_start", RequestID: "abandoned", Timestamp: base.Add(2 * time.Second)})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = r.Monitor(workspace, "root")
	if err != nil || got.Summary.Running != 0 || got.Requests[0].Status != "interrupted" {
		t.Fatalf("stopped instance advertised a live request: %+v %v", got, err)
	}
}
