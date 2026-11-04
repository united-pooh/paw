package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/todo"
	"paw/internal/tokentracer"
)

func TestActivityPaneAlwaysSplitsLatestActivityAndTokenHistory(t *testing.T) {
	for _, width := range []int{32, 52} {
		for _, height := range []int{22, 34, 38} {
			for _, tab := range []activityTab{activityTabTasks, activityTabTodo} {
				model := newActivityVisualModel(120, height+2)
				model.activity.tab = tab
				fixture := tokenMonitorFixture()
				model.tokenMonitor.snapshot = fixture.Snapshot
				now := time.Now()
				model.tokenMonitor.live.start("visible", now.Add(-20*time.Second))
				model.tokenMonitor.live.text(strings.Repeat("a", 200), now)
				model.hasCurrentTodo = true
				model.currentTodo = todo.Snapshot{Items: []todo.Item{{Content: "Keep the input available", Status: todo.StatusInProgress}}}
				view := model.renderActivityPane(width, height)
				lines := strings.Split(view, "\n")
				if len(lines) != height {
					t.Fatalf("height %d got %d", height, len(lines))
				}
				for _, line := range lines {
					if terminalCellWidth(line) != width {
						t.Fatalf("width %d got %d", width, terminalCellWidth(line))
					}
				}
				plain := ansi.Strip(view)
				for _, bad := range []string{"FOCUSED", "Tab page", "↑↓ scroll", "Enter preview", "f expand"} {
					if strings.Contains(plain, bad) {
						t.Errorf("passive pane retained %q", bad)
					}
				}
				lower := strings.Join(lines[height/2:], "\n")
				for _, want := range []string{copyTokenHistory, "累计输入", "累计输出", "缓存命中", "其中工具"} {
					if !strings.Contains(ansi.Strip(lower), want) {
						t.Errorf("%dx%d lower missing %q\n%s", width, height, want, ansi.Strip(lower))
					}
				}
				chartRows := 0
				for _, line := range lines[height/2:] {
					for _, r := range ansi.Strip(line) {
						if r > 0x2800 && r <= 0x28ff {
							chartRows++
							break
						}
					}
				}
				if height >= 34 && chartRows < 5 {
					t.Errorf("%dx%d token history has only %d plotted rows", width, height, chartRows)
				}
			}
		}
	}
}

func TestTokenHistoryLabelsReceivedStreamAndKeepsCumulativeScopeWithIssues(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates.FirstTextKnown = false
	v.Snapshot.Issues = []tokentracer.LedgerIssue{{Kind: "atom_retention_limit"}}
	got := ansi.Strip(renderTokenHistory(v, 32, 20))
	for _, want := range []string{"均速", "近60秒", "累计输入（含子任务）", "工具明细受限"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "visible text") || strings.Contains(got, "atom_retention_limit") {
		t.Fatalf("obsolete scope or raw internal issue: %s", got)
	}
	flow := ansi.Strip(strings.Join(tokenMonitorFlow(v, 48, 10), "\n"))
	if strings.Contains(flow, "waiting") || !strings.Contains(flow, "receiving") {
		t.Fatalf("reasoning-only stream called waiting: %s", flow)
	}
}

func TestTokenHistoryNoChunksAndIncompleteToolCoverage(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates.Samples = make([]float64, 60)
	v.Rates.Current = 0
	got := ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "暂无流片段") {
		t.Fatalf("empty stream has no explanation:\n%s", got)
	}
	v.Snapshot.ToolTotal = tokentracer.LedgerTool{}
	v.Snapshot.ToolsKnown = 0
	got = ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "其中工具 —") {
		t.Fatalf("missing tool source presented as zero:\n%s", got)
	}
	v.Snapshot.ToolsKnown = v.Snapshot.Summary.Attempts
	got = ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "其中工具 约0") {
		t.Fatalf("known empty tool source presented as unknown:\n%s", got)
	}
	v.Snapshot.ToolTotal = tokentracer.LedgerTool{Results: 1000, Occurrences: 1}
	v.Snapshot.ToolsKnown = 2
	got = ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "其中工具 约1000 部分 2/5") {
		t.Fatalf("incomplete tool attribution silently complete:\n%s", got)
	}
}

func TestTokenHistoryPriorRequestSamplesDoNotMakeCurrentRateKnown(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates.Observed = false
	v.Rates.TurnObserved = false
	got := ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "均速 —") || strings.Contains(got, "暂无流片段") {
		t.Fatalf("historical samples changed current-request rate knowledge or were hidden:\n%s", got)
	}
}

func TestActivityTodoPassiveTruncationHasNoScrollInstruction(t *testing.T) {
	model := newActivityVisualModel(120, 24)
	model.hasCurrentTodo = true
	for i := 0; i < 12; i++ {
		model.currentTodo.Items = append(model.currentTodo.Items, todo.Item{Content: "Task", Status: todo.StatusPending})
	}
	got := ansi.Strip(model.renderActivityTodo(32, 5))
	if strings.Contains(got, "scroll") || strings.Contains(got, "↑") {
		t.Fatalf("passive Todo retained navigation: %s", got)
	}
	if !strings.Contains(got, "另8行") {
		t.Fatalf("missing static overflow count: %s", got)
	}
}

func TestTokenHistoryRetainsPartialCoverageOnNarrowPane(t *testing.T) {
	v := tokenMonitorFixture()
	v.Snapshot.Summary.InputKnown = 3
	v.Snapshot.Summary.OutputKnown = 2
	v.Snapshot.Summary.CacheKnown = 1
	v.Snapshot.ToolTotal.Unknown = 1
	got := ansi.Strip(renderTokenHistory(v, 32, 18))
	for _, want := range []string{"12.5万 部分 3/5", "6430 部分 2/5", "缓存命中", "其中工具 约3.81万 部分"} {
		if !strings.Contains(got, want) {
			t.Errorf("partial coverage lost %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "75.0%") {
		t.Fatal("partial usage produced a cache hit rate")
	}
	if !strings.Contains(got, "9.36万 部分 1/5") {
		t.Fatal("partial cached-token amount lost its coverage")
	}
}

func TestTokenHistoryRetainsAbsoluteCacheReadAndRatio(t *testing.T) {
	v := tokenMonitorFixture()
	got := ansi.Strip(renderTokenHistory(v, 32, 18))
	if !strings.Contains(got, "9.36万 75.0%") {
		t.Fatalf("cache quantity or hit rate missing:\n%s", got)
	}
}
