package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunStageRetriesAndSucceeds(t *testing.T) {
	recorder := &stageRecorder{}
	attempts := 0
	value, err := runStage(context.Background(), recorder, "retry-test", 2, func(int) (string, error) {
		attempts++
		if attempts == 1 {
			return "", os.ErrInvalid
		}
		return "ok", nil
	}, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if value != "ok" {
		t.Fatalf("value = %q, want ok", value)
	}
	if recorder.retryCount != 1 {
		t.Fatalf("retryCount = %d, want 1", recorder.retryCount)
	}
	if len(recorder.attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(recorder.attempts))
	}
	if recorder.attempts[0].Status != "failed" || recorder.attempts[1].Status != "passed" {
		t.Fatalf("attempt records = %#v", recorder.attempts)
	}
}

func TestWriteReviewArtifactsCreatesStrictPipelineWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(root, "README.md"), "cmd/agent\ninternal/foo\n")
	writeTestFile(t, filepath.Join(root, "cmd", "agent", "main.go"), "package main\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "cmd", "agent", "main_test.go"), "package main\nimport \"testing\"\nfunc TestMain(t *testing.T) {}\nfunc TestFlags(t *testing.T) {}\n")
	writeTestFile(t, filepath.Join(root, "internal", "foo", "logic.go"), "package foo\nfunc Do() {}\n")

	result, err := WriteReviewArtifacts(context.Background(), ReviewArtifactsOptions{
		Root:        root,
		Task:        "review当前项目",
		RunID:       "run-review",
		FinalText:   "strict summary",
		MaxAttempts: 2,
		Now:         time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionReport.Iteration != 1 {
		t.Fatalf("iteration = %d, want 1", result.ExecutionReport.Iteration)
	}

	for _, rel := range []string{
		".pipeline-workspace/spec.json",
		".pipeline-workspace/tree-classification.json",
		".pipeline-workspace/tree-rubrics.json",
		".pipeline-workspace/tree-rubric-verification.json",
		".pipeline-workspace/tree-rubrics-refined.json",
		".pipeline-workspace/tree-grading-individual.json",
		".pipeline-workspace/final-assessment.json",
		".pipeline-last-run-summary.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing artifact %s: %v", rel, err)
		}
	}

	var grading TreeGradingArtifact
	readJSONFile(t, filepath.Join(root, ".pipeline-workspace", "tree-grading-individual.json"), &grading)
	if len(grading.Modules) != 2 {
		t.Fatalf("graded modules = %d, want 2", len(grading.Modules))
	}
	grades := map[string]ModuleGrade{}
	for _, module := range grading.Modules {
		grades[module.Path] = module
	}
	if grades["cmd/agent"].Verdict != "pass" {
		t.Fatalf("cmd/agent verdict = %q, want pass", grades["cmd/agent"].Verdict)
	}
	if grades["internal/foo"].Verdict != "fail" {
		t.Fatalf("internal/foo verdict = %q, want fail", grades["internal/foo"].Verdict)
	}
	if grades["cmd/agent"].Score <= grades["internal/foo"].Score {
		t.Fatalf("cmd/agent score=%d internal/foo score=%d, want cmd/agent higher", grades["cmd/agent"].Score, grades["internal/foo"].Score)
	}

	var assessment FinalAssessmentArtifact
	readJSONFile(t, filepath.Join(root, ".pipeline-workspace", "final-assessment.json"), &assessment)
	if assessment.Verdict != "reject" {
		t.Fatalf("assessment verdict = %q, want reject", assessment.Verdict)
	}
	if assessment.ReviewFinal != "strict summary" {
		t.Fatalf("review final = %q, want strict summary", assessment.ReviewFinal)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
