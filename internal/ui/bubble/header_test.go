package bubble

import (
	"strings"
	"testing"
	"time"
)

func TestRenderHeaderExactWidth(t *testing.T) {
	s := headerSnapshot{
		modelLabel:  "gpt-5.6-sol",
		statusLabel: "⠋ generating",
		now:         time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}
	line := renderHeader(s, 80)
	if got := terminalCellWidth(line); got != 80 {
		t.Fatalf("header width = %d, want 80", got)
	}
	for _, want := range []string{"gpt-5.6-sol", "generating", "15:42"} {
		if !strings.Contains(line, want) {
			t.Errorf("header %q missing %q", line, want)
		}
	}
}

func TestRenderHeaderUsesReadyWithoutGitSymbols(t *testing.T) {
	line := renderHeader(headerSnapshot{
		modelLabel:  "opus-4.8",
		statusLabel: "ready",
		now:         time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}, 80)
	if !strings.Contains(line, "ready") || strings.Contains(line, "Σ") {
		t.Fatalf("header = %q, want ready status without session total", line)
	}
}

func TestRenderHeaderLongModelTruncated(t *testing.T) {
	line := renderHeader(headerSnapshot{
		modelLabel:  strings.Repeat("x", 60),
		statusLabel: "ready",
		now:         time.Date(2026, 7, 28, 9, 1, 0, 0, time.UTC),
	}, 80)
	if got := terminalCellWidth(line); got != 80 {
		t.Fatalf("header width = %d, want 80", got)
	}
}

func TestRenderHeaderNarrowKeepsModelAndStatus(t *testing.T) {
	s := headerSnapshot{modelLabel: "opus-4.8", statusLabel: "ready", now: time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)}
	for _, width := range []int{30, 20, 12} {
		line := renderHeader(s, width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d header width=%d", width, got)
		}
		if !strings.Contains(line, "opus") {
			t.Errorf("width=%d lost model: %q", width, line)
		}
	}
}

func TestRenderHeaderRightAlignsTime(t *testing.T) {
	line := renderHeader(headerSnapshot{
		modelLabel:  "opus-4.8",
		statusLabel: "ready",
		now:         time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC),
	}, 80)
	if !strings.HasSuffix(strings.TrimRight(line, " "), "15:42") {
		t.Fatalf("time is not right aligned: %q", line)
	}
}

func TestHeaderShowsTurnElapsedWhileWorking(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	started := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	model.turnStartedAt = started
	model.isGenerating = true
	model.spinnerFrameIdx = 0
	snapshot := model.collectHeaderData(started.Add(22 * time.Second))
	if !strings.Contains(snapshot.statusLabel, "22s working") {
		t.Fatalf("header status = %q, want 22s working", snapshot.statusLabel)
	}
	if got := formatTurnTimer(started, started.Add(90*time.Second+3*time.Second)); got != "1m33s" {
		t.Fatalf("formatTurnTimer = %q, want 1m33s", got)
	}

	snapshot = model.collectHeaderData(started.Add(9 * time.Hour))
	if !strings.Contains(snapshot.statusLabel, "9h00m00s working") {
		t.Fatalf("header status = %q, want 9h00m00s working", snapshot.statusLabel)
	}
}

func TestFormatTurnTimerBoundaries(t *testing.T) {
	started := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{name: "seconds", elapsed: 59 * time.Second, want: "59s"},
		{name: "minutes", elapsed: time.Minute + 5*time.Second, want: "1m05s"},
		{name: "before hour", elapsed: 59*time.Minute + 59*time.Second, want: "59m59s"},
		{name: "one hour", elapsed: time.Hour, want: "1h00m00s"},
		{name: "nine hours", elapsed: 9*time.Hour + 5*time.Minute + 7*time.Second, want: "9h05m07s"},
		{name: "beyond one day", elapsed: 27*time.Hour + 5*time.Minute + 7*time.Second, want: "27h05m07s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatTurnTimer(started, started.Add(test.elapsed)); got != test.want {
				t.Fatalf("formatTurnTimer() = %q, want %q", got, test.want)
			}
		})
	}
}
