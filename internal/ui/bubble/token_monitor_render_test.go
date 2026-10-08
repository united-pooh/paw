package bubble

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
	"paw/internal/tokentracer"
)

func tokenMonitorFixture() tokenMonitorView {
	now := time.Date(2026, 10, 4, 12, 3, 27, 0, time.UTC)
	return tokenMonitorView{
		Snapshot: tokentracer.MonitorSnapshot{
			SessionID: "main-session", GeneratedAt: now,
			Summary:            tokentracer.RequestSummary{Requests: 4, Attempts: 5, Input: 124800, InputKnown: 5, Output: 6430, OutputKnown: 5, CacheRead: 93600, CacheKnown: 5, CacheCreation: 800, Reasoning: 2100, Reported: 5, Running: 1},
			CacheCreationKnown: 5, ReasoningKnown: 5, ToolsKnown: 5,
			ToolTotal: tokentracer.LedgerTool{Occurrences: 27, Definitions: 2000, Arguments: 465, Results: 35600},
			Tools:     []tokentracer.LedgerTool{{Name: "exec_command", Occurrences: 19, Definitions: 1400, Arguments: 340, Results: 26400}, {Name: "read_file", Occurrences: 8, Definitions: 600, Arguments: 125, Results: 9200}},
			Requests:  []tokentracer.MonitorRequest{{LedgerRequestRow: tokentracer.LedgerRequestRow{ID: "request-active", Model: "test-model", StartedAt: now.Add(-12 * time.Second), Status: "running", Summary: tokentracer.RequestSummary{Attempts: 1, Input: 18400, InputKnown: 1}}, SessionID: "main-session"}, {LedgerRequestRow: tokentracer.LedgerRequestRow{ID: "request-complete", Model: "worker-model", StartedAt: now.Add(-40 * time.Second), Status: "completed", Summary: tokentracer.RequestSummary{Attempts: 1, Input: 12800, InputKnown: 1, Output: 1400, OutputKnown: 1}}, TaskID: "inspect-source", SessionID: "child-session"}},
		},
		Context: loop.ContextStats{Known: true, TotalKnown: true, UsedTokens: 48200, CacheTokens: 33600, LimitTokens: 128000},
		Rates:   tokenRateView{TurnAverage: 12.5, TurnTokens: 1000, TurnElapsed: 80 * time.Second, TurnObserved: true, TurnActive: true, Current: 48.2, Average: 42.6, FirstText: 820 * time.Millisecond, FirstTextKnown: true, Active: true, Observed: true, RequestID: "request-active", Samples: []float64{0, 0, 12, 24, 38, 42, 55, 40, 46, 52, 48, 60, 50, 38, 46, 51, 44, 48}, ToolCalls: 6, ToolDuration: 4200 * time.Millisecond},
	}
}

func TestTokenMonitorFitsTerminalCells(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {20, 10}, {38, 24}, {46, 36}, {80, 24}, {120, 36}, {160, 50}} {
		v := tokenMonitorFixture()
		v.Snapshot.Tools[0].Name = "工具与路径👩‍💻\nmalformed\tvalue"
		view := renderTokenMonitor(v, size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v height = %d", size, len(lines))
		}
		for i, line := range lines {
			if got := terminalCellWidth(line); got != size[0] {
				t.Fatalf("%v line %d width = %d: %q", size, i, got, ansi.Strip(line))
			}
		}
	}
	if got := renderTokenMonitor(tokenMonitorView{}, 0, 4); got != "" {
		t.Fatalf("zero width = %q", got)
	}
}

func TestTokenMonitorReportsCoverageAndSubsetSemantics(t *testing.T) {
	v := tokenMonitorFixture()
	v.Snapshot.Summary.CacheKnown = 2
	v.Snapshot.Summary.OutputKnown = 0
	v.Snapshot.CacheCreationKnown = 0
	v.Snapshot.ReasoningKnown = 0
	got := ansi.Strip(renderTokenMonitor(v, 120, 40))
	for _, want := range []string{"cache ⊂ input", "reasoning ⊂ output", "2/5", "Output", "—", "partial", "≈", "history", "6 calls"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "75.0%") {
		t.Error("partial cache coverage must not claim a whole-session cache-hit ratio")
	}
	if strings.Contains(got, "19 calls") {
		t.Error("tool exposures are not executions")
	}
}

func TestTokenMonitorSelectedRequestDetailAndStates(t *testing.T) {
	v := tokenMonitorFixture()
	v.Selected = 1
	v.Detail = true
	got := ansi.Strip(renderTokenMonitor(v, 120, 36))
	for _, want := range []string{"request-complete", "inspect-source", "worker-model"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing selected detail %q", want)
		}
	}
	for _, tc := range []struct {
		view tokenMonitorView
		want string
	}{
		{tokenMonitorView{Loading: true}, "Loading"},
		{tokenMonitorView{Err: "ledger unavailable"}, "ledger unavailable"},
		{tokenMonitorView{}, "No recorded requests"},
	} {
		got = ansi.Strip(renderTokenMonitor(tc.view, 42, 27))
		if !strings.Contains(got, tc.want) {
			t.Errorf("missing state %q in\n%s", tc.want, got)
		}
	}
}

func TestTokenMonitorGraphHandlesMissingAndInvalidSamples(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates.Samples = []float64{-1, math.Inf(1), math.NaN(), 10, 0, 20}
	v.Rates.Current = math.NaN()
	got := renderTokenMonitor(v, 80, 24)
	if strings.Contains(got, "NaN") || strings.Contains(got, "+Inf") {
		t.Fatal("invalid sample leaked into visible metrics")
	}
}

func TestTokenMonitorPreservesUnknownRatesAndToolTotals(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates = tokenRateView{Samples: make([]float64, 60), ToolActive: true}
	v.Snapshot.ToolTotal.Results = 900000
	got := ansi.Strip(renderTokenMonitor(v, 120, 34))
	if strings.Contains(got, "≈0.0 tok/s") {
		t.Fatal("empty rate history must remain unknown before the first request")
	}
	for _, want := range []string{"— tok/s", "tools", "≈900k"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestTokenMonitorNarrowDetailAndKeySectionsRemainReachable(t *testing.T) {
	v := tokenMonitorFixture()
	for _, width := range []int{32, 52} {
		got := ansi.Strip(renderTokenMonitor(v, width, 24))
		for _, want := range []string{"FLOW", "CONTEXT", "TOKEN LEDGER", "TOOLS", "exec_command", "REQUESTS"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q in\n%s", width, want, got)
			}
		}
		v.Detail, v.Selected = true, 1
		got = ansi.Strip(renderTokenMonitor(v, width, 24))
		for _, want := range []string{"REQUEST DETAIL", "request-complete", "worker-model", "Input", "Output"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d detail missing %q in\n%s", width, want, got)
			}
		}
		v.Detail = false
	}
}
