package tokentracer

import (
	"reflect"
	"testing"

	"paw/internal/capability/model"
)

func TestMonitorPreservesToolTotalsWhenAtomDetailsAreLimited(t *testing.T) {
	r, workspace := monitorFixture(t)
	r.atoms = maxLedgerAtoms
	estimate := 822848
	r.apply("main", model.RequestEvent{Kind: "request_start", RequestID: "root", Scope: model.RequestScope{SessionID: "root"}})
	r.apply("main", model.RequestEvent{Kind: "attempt_start", RequestID: "root", Attempt: 1, AtomsState: "complete",
		Atoms: []model.InputAtom{{ToolName: "Read", Category: "tool_result", EstimatedTokens: &estimate}}})
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolTotal.Results != estimate || got.ToolTotal.Occurrences != 1 || got.ToolsKnown != 1 {
		t.Fatalf("detail retention lost complete tool aggregate: %+v", got.ToolTotal)
	}
	if r.atoms != maxLedgerAtoms || len(r.requests["main/root"].Attempts[0].Atoms) != 0 {
		t.Fatal("tool aggregate bypassed the full-atom retention bound")
	}
}

func TestMonitorToolCoverageDistinguishesZeroPartialAndMissing(t *testing.T) {
	r, workspace := monitorFixture(t)
	estimate := 20
	for i, state := range []string{"complete", "partial", "unavailable"} {
		id := []string{"zero", "partial", "missing"}[i]
		r.apply("main", model.RequestEvent{Kind: "request_start", RequestID: id, Scope: model.RequestScope{SessionID: "root"}})
		event := model.RequestEvent{Kind: "attempt_start", RequestID: id, Attempt: 1, AtomsState: state}
		if state == "partial" {
			event.Atoms = []model.InputAtom{{ToolName: "Read", Category: "tool_result", EstimatedTokens: &estimate}}
		}
		r.apply("main", event)
	}
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Attempts != 3 || got.ToolsKnown != 1 || got.ToolTotal.Results != 20 {
		t.Fatalf("partial or unavailable tool attribution treated as complete: %+v", got)
	}
}

func TestMonitorIssuesDoNotIncludeUnrelatedSessionSources(t *testing.T) {
	r, workspace := monitorFixture(t)
	r.instances["other"] = InstanceMetadata{ID: "other", Workspace: workspace, ProjectID: "project"}
	addMonitorRequest(r, "main", "root", model.RequestScope{SessionID: "root"})
	addMonitorRequest(r, "other", "unrelated", model.RequestScope{SessionID: "unrelated"})
	r.issue("other", "atom_retention_limit")
	r.issue("main", "atom_retention_limit")
	r.issue("main", "usage_inconsistent")
	r.issue("", "scan_backlog")
	got, err := r.Monitor(workspace, "root")
	if err != nil {
		t.Fatal(err)
	}
	// scanLocked clears a resolved scan_backlog; the current session has no retained-atom loss.
	want := []LedgerIssue{{InstanceID: "main", Kind: "usage_inconsistent"}}
	if !reflect.DeepEqual(got.Issues, want) {
		t.Fatalf("unrelated source issues attributed to session: %+v", got.Issues)
	}
}

func TestMonitorRetainsWorkspaceReadErrorsBeforeSessionCanBeLocated(t *testing.T) {
	r, workspace := monitorFixture(t)
	r.issue("main", "segment_unavailable")
	got, err := r.Monitor(workspace, "unread-session")
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Requests != 0 || len(got.Issues) != 1 || got.Issues[0].Kind != "segment_unavailable" {
		t.Fatalf("unreadable workspace source looked like a complete empty session: %+v", got)
	}
}
