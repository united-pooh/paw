package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paw/internal/runtime/goal"
	"paw/internal/storage/session"
)

func TestStateContextKeepsQuantitativeGoal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	journal, err := session.NewJSONLStore(root)
	if err != nil {
		t.Fatal(err)
	}
	goals, err := goal.NewEventStore(journal.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []goal.Goal{
		{ID: "target", SessionID: "current", Objective: "延迟不超过 50 毫秒，方法待探索", Status: goal.GoalPaused,
			Metric:    &goal.MetricSpec{Name: "p95 latency", Comparison: "lte", Target: 50, Unit: "ms", Measurement: goal.MetricMeasurement{Tool: "Bash", Command: "./bench --json", CWD: "", JSONPointer: "/p95"}},
			UpdatedAt: time.Now()},
		{ID: "foreign", SessionID: "another", Objective: "PRIVATE OTHER SESSION", Status: goal.GoalRunning},
	} {
		if err := goals.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	saved, _, err := goals.Get(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	saved.Measurements = []goal.MetricResult{{ToolCallID: "measure-1", Value: 73, Digest: strings.Repeat("a", 64), RecordedAt: time.Now()}}
	if err := goals.Update(ctx, saved); err != nil {
		t.Fatal(err)
	}
	provider := stateBlockProviderFor("current", journal, nil, filepath.Join(root, "plans"))
	out, err := provider.BuildStateContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 目标（goal）", "方法待探索", "p95 latency", "lte", "50", "ms", "./bench --json", "/p95", "measure-1", "73", "paused"} {
		if !strings.Contains(out, want) {
			t.Errorf("state context lost %q: %s", want, out)
		}
	}
	if strings.Contains(out, "PRIVATE OTHER SESSION") {
		t.Fatal("goal context leaked another session")
	}
	unbound := stateBlockProviderFor("", journal, nil, filepath.Join(root, "plans"))
	out, err = unbound.BuildStateContext(ctx)
	if err != nil || strings.Contains(out, "## 目标（goal）") {
		t.Fatalf("unbound context must not include goals from any session: %q, %v", out, err)
	}
}
