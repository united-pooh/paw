package bubble

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"paw/internal/session"
)

const (
	localScrollSessionEnv          = "PAW_SCROLL_BENCH_SESSION"
	localScrollSessionMinimumBytes = 10_000_000
)

var localScrollViewSink string

type localScrollSessionStats struct {
	fileBytes       int64
	records         int
	entries         int
	renderedLines   int
	forwardScrolls  int
	hoverScrolls    int
	mouseWheelDelta int
}

type localHoverPoint struct {
	x int
	y int
}

type localHoverSweepStats struct {
	totalMotions      int
	filteredMotions   int
	passedMotions     int
	toolEnters        int
	toolLeaves        int
	noticeEnters      int
	noticeLeaves      int
	uniqueTools       int
	expectedTools     map[int]struct{}
	seenTools         map[int]struct{}
	transcriptRefresh uint64
	transcriptRenders int
	elapsed           time.Duration
	motionDurations   []time.Duration
	passedDurations   []time.Duration
	firstToolPoint    localHoverPoint
	hasToolPoint      bool
}

func TestLocalSessionScrollReversal(t *testing.T) {
	model, stats := loadLocalScrollSessionModel(t)
	up := localScrollMouseMsg(tea.MouseButtonWheelUp)
	down := localScrollMouseMsg(tea.MouseButtonWheelDown)
	durations := make([]time.Duration, 0, 31)

	refreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	for range cap(durations) {
		model.viewport.GotoBottom()
		for range stats.forwardScrolls {
			updateLocalScrollModel(t, &model, up, true)
		}
		beforeReverse := model.viewport.YOffset
		started := time.Now()
		updateLocalScrollModel(t, &model, down, true)
		durations = append(durations, time.Since(started))
		if model.viewport.YOffset <= beforeReverse {
			t.Fatalf("reverse wheel did not move down: YOffset %d -> %d", beforeReverse, model.viewport.YOffset)
		}
	}

	if got := model.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("scrolling refreshed transcript %d times, want 0", got)
	}
	if got := model.transcriptRenderVisits; got != 0 {
		t.Fatalf("scrolling rendered %d transcript entries, want 0", got)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf(
		"session=%.2fMiB records=%d entries=%d rendered_lines=%d forward_events=%d wheel_delta=%d reverse_update_view_p50=%s p95=%s max=%s",
		float64(stats.fileBytes)/(1024*1024),
		stats.records,
		stats.entries,
		stats.renderedLines,
		stats.forwardScrolls,
		stats.mouseWheelDelta,
		durations[len(durations)/2],
		durations[percentileIndex(len(durations), 95)],
		durations[len(durations)-1],
	)
}

func TestLocalSessionHoverSweepAndScrollReversal(t *testing.T) {
	model, sessionStats := loadLocalScrollSessionModel(t)
	up := localScrollMouseMsg(tea.MouseButtonWheelUp)
	for range sessionStats.hoverScrolls {
		updateLocalScrollModel(t, &model, up, true)
	}
	model.newMessageNoticeCount = 1
	localScrollViewSink = model.View()

	var current tea.Model = model
	current, hoverStats := sweepLocalSessionHovers(t, current, true, true)
	assertLocalHoverSweep(t, hoverStats)
	model = current.(appModel)
	if model.toolHoverIndex != -1 || model.newMessageNoticeHovered {
		t.Fatalf("hover sweep ended active: tool=%d notice=%v", model.toolHoverIndex, model.newMessageNoticeHovered)
	}

	motion := tea.MouseMsg{
		X:      hoverStats.firstToolPoint.x,
		Y:      hoverStats.firstToolPoint.y,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	}
	filtered := filterIdleMouseMotion(current, motion)
	if filtered == nil {
		t.Fatal("motion into a visible tool was filtered")
	}
	next, _ := current.Update(filtered)
	current = next
	localScrollViewSink = current.View()
	model = current.(appModel)
	if model.toolHoverIndex < 0 {
		t.Fatal("tool hover did not become active before reverse scroll")
	}

	beforeOffset := model.viewport.YOffset
	refreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	current = model
	started := time.Now()
	reverse := localScrollMouseMsg(tea.MouseButtonWheelDown)
	reverse.X = hoverStats.firstToolPoint.x
	reverse.Y = hoverStats.firstToolPoint.y
	next, _ = current.Update(reverse)
	current = next
	localScrollViewSink = current.View()
	reverseDuration := time.Since(started)
	model = current.(appModel)
	if model.viewport.YOffset <= beforeOffset {
		t.Fatalf("hovered reverse wheel did not move down: YOffset %d -> %d", beforeOffset, model.viewport.YOffset)
	}
	if got := model.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("hovered reverse wheel refreshed transcript %d times, want 0", got)
	}
	if got := model.transcriptRenderVisits; got != 0 {
		t.Fatalf("hovered reverse wheel rendered %d transcript entries, want 0", got)
	}
	sort.Slice(hoverStats.motionDurations, func(i, j int) bool {
		return hoverStats.motionDurations[i] < hoverStats.motionDurations[j]
	})
	sort.Slice(hoverStats.passedDurations, func(i, j int) bool {
		return hoverStats.passedDurations[i] < hoverStats.passedDurations[j]
	})
	t.Logf(
		"session=%.2fMiB forward_events=%d motions=%d filtered=%d passed=%d visible_tools=%d/%d tool_enter_leave=%d/%d notice_enter_leave=%d/%d refreshes=%d render_visits=%d sweep=%s motion_p50=%s motion_p95=%s motion_max=%s passed_p50=%s passed_p95=%s passed_max=%s hovered_reverse_update_view=%s",
		float64(sessionStats.fileBytes)/(1024*1024),
		sessionStats.hoverScrolls,
		hoverStats.totalMotions,
		hoverStats.filteredMotions,
		hoverStats.passedMotions,
		hoverStats.uniqueTools,
		len(hoverStats.expectedTools),
		hoverStats.toolEnters,
		hoverStats.toolLeaves,
		hoverStats.noticeEnters,
		hoverStats.noticeLeaves,
		hoverStats.transcriptRefresh,
		hoverStats.transcriptRenders,
		hoverStats.elapsed,
		hoverStats.motionDurations[len(hoverStats.motionDurations)/2],
		hoverStats.motionDurations[percentileIndex(len(hoverStats.motionDurations), 95)],
		hoverStats.motionDurations[len(hoverStats.motionDurations)-1],
		hoverStats.passedDurations[len(hoverStats.passedDurations)/2],
		hoverStats.passedDurations[percentileIndex(len(hoverStats.passedDurations), 95)],
		hoverStats.passedDurations[len(hoverStats.passedDurations)-1],
		reverseDuration,
	)
}

func TestLocalSessionQueuedScrollReversal(t *testing.T) {
	base, stats := loadLocalScrollSessionModel(t)
	testCases := []struct {
		name          string
		forwardButton tea.MouseButton
		startAt       string
	}{
		{name: "up-to-down", forwardButton: tea.MouseButtonWheelUp, startAt: "middle"},
		{name: "down-to-up", forwardButton: tea.MouseButtonWheelDown, startAt: "middle"},
		{name: "top-clamp", forwardButton: tea.MouseButtonWheelUp, startAt: "top"},
		{name: "bottom-clamp", forwardButton: tea.MouseButtonWheelDown, startAt: "bottom"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			model := base
			switch tc.startAt {
			case "middle":
				model.viewport.GotoBottom()
				model.viewport.SetYOffset(model.viewport.YOffset / 2)
			case "top":
				model.viewport.GotoTop()
			case "bottom":
				model.viewport.GotoBottom()
			}
			startOffset := model.viewport.YOffset
			model.newMessageNoticeCount = 1
			refreshes := model.transcriptRefreshCount
			model.transcriptRenderVisits = 0
			filter := newProgramEventFilter(nil)
			var current tea.Model = model
			forward := localScrollMouseMsg(tc.forwardButton)
			reverse := localScrollMouseMsg(oppositeWheelButton(tc.forwardButton))
			accepted := 0
			views := 0
			started := time.Now()
			for range 3_001 {
				filtered := filter.Filter(current, forward)
				if filtered == nil {
					continue
				}
				next, _ := current.Update(filtered)
				current = next
				localScrollViewSink = current.View()
				accepted++
				views++
			}
			beforeReverse := current.(appModel).viewport.YOffset
			filtered := filter.Filter(current, reverse)
			if filtered == nil {
				t.Fatal("reverse wheel was filtered")
			}
			next, _ := current.Update(filtered)
			current = next
			localScrollViewSink = current.View()
			accepted++
			views++
			elapsed := time.Since(started)
			model = current.(appModel)

			if accepted != 2 || views != 2 {
				t.Fatalf("queued burst accepted=%d views=%d, want 2/2", accepted, views)
			}
			if tc.forwardButton == tea.MouseButtonWheelUp && model.viewport.YOffset <= beforeReverse {
				t.Fatalf("reverse down did not move viewport: %d -> %d", beforeReverse, model.viewport.YOffset)
			}
			if tc.forwardButton == tea.MouseButtonWheelDown && model.viewport.YOffset >= beforeReverse {
				t.Fatalf("reverse up did not move viewport: %d -> %d", beforeReverse, model.viewport.YOffset)
			}
			wantOffset := startOffset
			switch tc.startAt {
			case "top":
				wantOffset += stats.mouseWheelDelta
			case "bottom":
				wantOffset -= stats.mouseWheelDelta
			}
			if model.viewport.YOffset != wantOffset {
				t.Fatalf("final YOffset = %d, want %d", model.viewport.YOffset, wantOffset)
			}
			wantNotice := 1
			if tc.startAt == "bottom" {
				wantNotice = 0
			}
			if model.newMessageNoticeCount != wantNotice {
				t.Fatalf("notice count = %d, want %d", model.newMessageNoticeCount, wantNotice)
			}
			if got := model.transcriptRefreshCount - refreshes; got != 0 {
				t.Fatalf("queued scrolling refreshed transcript %d times, want 0", got)
			}
			if model.transcriptRenderVisits != 0 {
				t.Fatalf("queued scrolling rendered %d transcript entries, want 0", model.transcriptRenderVisits)
			}
			t.Logf(
				"session=%.2fMiB records=%d entries=%d rendered_lines=%d raw_events=%d accepted_batches=%d views=%d queued_reversal=%s",
				float64(stats.fileBytes)/(1024*1024), stats.records, stats.entries, stats.renderedLines,
				3_002, accepted, views, elapsed,
			)
		})
	}
}

func TestLocalSessionQueuedScrollReversalWithActiveToolHover(t *testing.T) {
	model, stats := loadLocalScrollSessionModel(t)
	model.viewport.GotoBottom()
	model.viewport.ScrollUp(stats.hoverScrolls * stats.mouseWheelDelta)
	model.newMessageNoticeCount = 1
	localScrollViewSink = model.View()

	var current tea.Model = model
	current, hoverStats := sweepLocalSessionHovers(t, current, false, false)
	assertLocalHoverSweep(t, hoverStats)
	motion := tea.MouseMsg{
		X:      hoverStats.firstToolPoint.x,
		Y:      hoverStats.firstToolPoint.y,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionMotion,
	}
	filtered := filterIdleMouseMotion(current, motion)
	if filtered == nil {
		t.Fatal("motion into a visible tool was filtered")
	}
	next, _ := current.Update(filtered)
	current = next
	model = current.(appModel)
	if model.toolHoverIndex < 0 {
		t.Fatal("tool hover did not become active before queued scroll")
	}
	startHover := model.toolHoverIndex
	startOffset := model.viewport.YOffset
	refreshes := model.transcriptRefreshCount
	model.transcriptRenderVisits = 0
	hoverRenders := 0
	model.transcriptHoverPatchRenderSpy = &hoverRenders
	current = model

	filter := newProgramEventFilter(nil)
	forward := localScrollMouseMsg(tea.MouseButtonWheelUp)
	forward.X = motion.X
	forward.Y = motion.Y
	reverse := localScrollMouseMsg(tea.MouseButtonWheelDown)
	reverse.X = motion.X
	reverse.Y = motion.Y
	accepted := 0
	views := 0
	started := time.Now()
	for range 3_001 {
		filtered := filter.Filter(current, forward)
		if filtered == nil {
			continue
		}
		next, _ = current.Update(filtered)
		current = next
		localScrollViewSink = current.View()
		accepted++
		views++
	}
	filtered = filter.Filter(current, reverse)
	if filtered == nil {
		t.Fatal("reverse wheel was filtered")
	}
	next, _ = current.Update(filtered)
	current = next
	localScrollViewSink = current.View()
	accepted++
	views++
	elapsed := time.Since(started)
	model = current.(appModel)

	if accepted != 2 || views != 2 {
		t.Fatalf("queued hovered burst accepted=%d views=%d, want 2/2", accepted, views)
	}
	if model.viewport.YOffset != startOffset {
		t.Fatalf("final hovered YOffset = %d, want restored %d", model.viewport.YOffset, startOffset)
	}
	wantHover, interactionValid := model.toolHoverIndexAtMouse(motion.X, motion.Y)
	if !interactionValid {
		t.Fatal("interaction index became invalid after queued scroll")
	}
	if model.toolHoverIndex != wantHover || model.toolHoverIndex != startHover {
		t.Fatalf("final hover = %d, want hit-tested/restored %d/%d", model.toolHoverIndex, wantHover, startHover)
	}
	if hoverRenders > 2 {
		t.Fatalf("hover patch renders = %d, want at most one per visible batch", hoverRenders)
	}
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("notice count = %d, want preserved away from bottom", model.newMessageNoticeCount)
	}
	if got := model.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("queued hovered scrolling refreshed transcript %d times, want 0", got)
	}
	if model.transcriptRenderVisits != 0 {
		t.Fatalf("queued hovered scrolling rendered %d transcript entries, want 0", model.transcriptRenderVisits)
	}
	t.Logf(
		"session=%.2fMiB records=%d entries=%d rendered_lines=%d raw_events=%d accepted_batches=%d views=%d hover_patch_renders=%d queued_hovered_reversal=%s",
		float64(stats.fileBytes)/(1024*1024), stats.records, stats.entries, stats.renderedLines,
		3_002, accepted, views, hoverRenders, elapsed,
	)
}

func BenchmarkLocalSessionScrollReversal(b *testing.B) {
	for _, tc := range []struct {
		name       string
		renderView bool
	}{
		{name: "update", renderView: false},
		{name: "update+view", renderView: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			model, stats := loadLocalScrollSessionModel(b)
			up := localScrollMouseMsg(tea.MouseButtonWheelUp)
			down := localScrollMouseMsg(tea.MouseButtonWheelDown)
			for range stats.forwardScrolls {
				updateLocalScrollModel(b, &model, up, tc.renderView)
			}

			refreshes := model.transcriptRefreshCount
			model.transcriptRenderVisits = 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msg := down
				if i%2 == 1 {
					msg = up
				}
				updateLocalScrollModel(b, &model, msg, tc.renderView)
			}
			b.StopTimer()
			b.ReportMetric(float64(stats.fileBytes)/(1024*1024), "session-MiB")
			b.ReportMetric(float64(stats.records), "records")
			b.ReportMetric(float64(stats.entries), "entries")
			b.ReportMetric(float64(stats.renderedLines), "rendered-lines")
			b.ReportMetric(float64(stats.forwardScrolls), "forward-events")

			if got := model.transcriptRefreshCount - refreshes; got != 0 {
				b.Fatalf("scrolling refreshed transcript %d times, want 0", got)
			}
			if got := model.transcriptRenderVisits; got != 0 {
				b.Fatalf("scrolling rendered %d transcript entries, want 0", got)
			}
		})
	}
}

func BenchmarkLocalSessionQueuedScrollReversal(b *testing.B) {
	base, stats := loadLocalScrollSessionModel(b)
	const burst = 3_000
	for _, forwardButton := range []tea.MouseButton{tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		name := "up-to-down"
		if forwardButton == tea.MouseButtonWheelDown {
			name = "down-to-up"
		}
		b.Run(name, func(b *testing.B) {
			forward := localScrollMouseMsg(forwardButton)
			reverse := localScrollMouseMsg(oppositeWheelButton(forwardButton))
			b.ReportAllocs()
			for b.Loop() {
				model := base
				model.viewport.GotoBottom()
				model.viewport.SetYOffset(model.viewport.YOffset / 2)
				startOffset := model.viewport.YOffset
				refreshes := model.transcriptRefreshCount
				model.transcriptRenderVisits = 0
				filter := newProgramEventFilter(nil)
				var current tea.Model = model
				accepted := 0
				for range burst + 1 {
					filtered := filter.Filter(current, forward)
					if filtered == nil {
						continue
					}
					next, _ := current.Update(filtered)
					current = next
					localScrollViewSink = current.View()
					accepted++
				}
				filtered := filter.Filter(current, reverse)
				if filtered == nil {
					b.Fatal("reverse wheel was filtered")
				}
				next, _ := current.Update(filtered)
				current = next
				localScrollViewSink = current.View()
				accepted++
				model = current.(appModel)
				if accepted != 2 || model.viewport.YOffset != startOffset {
					b.Fatalf("accepted=%d final YOffset=%d, want 2/%d", accepted, model.viewport.YOffset, startOffset)
				}
				if got := model.transcriptRefreshCount - refreshes; got != 0 {
					b.Fatalf("queued scrolling refreshed transcript %d times, want 0", got)
				}
				if model.transcriptRenderVisits != 0 {
					b.Fatalf("queued scrolling rendered %d transcript entries, want 0", model.transcriptRenderVisits)
				}
			}
			b.ReportMetric(burst+2, "raw-events/op")
			b.ReportMetric(2, "batch-updates/op")
			b.ReportMetric(2, "views/op")
			b.ReportMetric(burst+2, "reverse-position/op")
			b.ReportMetric(float64(stats.fileBytes)/(1024*1024), "session-MiB")
			b.ReportMetric(float64(stats.records), "records")
			b.ReportMetric(float64(stats.entries), "entries")
			b.ReportMetric(float64(stats.renderedLines), "rendered-lines")
		})
	}
}

func BenchmarkLocalSessionScheduledRawWheelReversal(b *testing.B) {
	base, stats := loadLocalScrollSessionModel(b)
	const burst = 3_000
	for _, forwardButton := range []tea.MouseButton{tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		name := "up-to-down"
		if forwardButton == tea.MouseButtonWheelDown {
			name = "down-to-up"
		}
		b.Run(name, func(b *testing.B) {
			input := strings.Repeat(wheelSGR(forwardButton), burst+1) + wheelSGR(oppositeWheelButton(forwardButton)) + "q"
			lastTrace := wheelProgramTrace{}
			b.ReportAllocs()
			for b.Loop() {
				model := base
				model.viewport.GotoBottom()
				model.viewport.SetYOffset(model.viewport.YOffset / 2)
				trace := &wheelProgramTrace{}
				filter := newProgramEventFilter(scheduleTranscriptWheelFlush)
				program := tea.NewProgram(
					wheelProgramModel{app: model, trace: trace},
					tea.WithInput(&wheelEventReader{source: strings.NewReader(input)}),
					tea.WithoutRenderer(),
					tea.WithoutSignalHandler(),
					tea.WithFilter(filter.Filter),
				)
				if _, err := program.Run(); err != nil {
					b.Fatalf("run scheduled local wheel program: %v", err)
				}
				if trace.rawWheelUpdates != 0 || trace.batchUpdates < 2 || trace.batchUpdates > burst/100 {
					b.Fatalf("raw=%d batches=%d, want 0 and 2..%d", trace.rawWheelUpdates, trace.batchUpdates, burst/100)
				}
				if !trace.reverseSeen {
					b.Fatal("reverse batch never reached Update")
				}
				lastTrace = *trace
			}
			b.ReportMetric(burst+2, "raw-events/op")
			b.ReportMetric(float64(lastTrace.batchUpdates), "batch-updates/op")
			b.ReportMetric(float64(lastTrace.views), "views/op")
			b.ReportMetric(burst+2, "reverse-position/op")
			b.ReportMetric(float64(stats.fileBytes)/(1024*1024), "session-MiB")
			b.ReportMetric(float64(stats.entries), "entries")
			b.ReportMetric(float64(stats.renderedLines), "rendered-lines")
		})
	}
}

func BenchmarkLocalSessionHoverSweep(b *testing.B) {
	model, sessionStats := loadLocalScrollSessionModel(b)
	up := localScrollMouseMsg(tea.MouseButtonWheelUp)
	for range sessionStats.hoverScrolls {
		updateLocalScrollModel(b, &model, up, true)
	}
	model.newMessageNoticeCount = 1
	localScrollViewSink = model.View()
	var current tea.Model = model
	var hoverStats localHoverSweepStats

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		current, hoverStats = sweepLocalSessionHovers(b, current, true, false)
	}
	b.StopTimer()
	assertLocalHoverSweep(b, hoverStats)
	b.ReportMetric(float64(sessionStats.fileBytes)/(1024*1024), "session-MiB")
	b.ReportMetric(float64(hoverStats.totalMotions), "motions")
	b.ReportMetric(float64(hoverStats.filteredMotions), "filtered-motions")
	b.ReportMetric(float64(hoverStats.passedMotions), "passed-motions")
	b.ReportMetric(float64(hoverStats.uniqueTools), "visible-tools")
	b.ReportMetric(float64(len(hoverStats.expectedTools)), "expected-visible-tools")
	b.ReportMetric(float64(hoverStats.transcriptRefresh), "refreshes")
	b.ReportMetric(float64(hoverStats.transcriptRenders), "render-visits")
	b.ReportMetric(float64(sessionStats.hoverScrolls), "forward-events")
}

func sweepLocalSessionHovers(tb testing.TB, current tea.Model, renderView, collectDurations bool) (tea.Model, localHoverSweepStats) {
	tb.Helper()
	model, ok := current.(appModel)
	if !ok {
		tb.Fatalf("hover sweep model = %T, want appModel", current)
	}
	stats := localHoverSweepStats{
		totalMotions:  model.width * model.height,
		expectedTools: localVisibleToolHoverIndices(model),
	}
	if collectDurations {
		stats.motionDurations = make([]time.Duration, 0, stats.totalMotions)
		stats.passedDurations = make([]time.Duration, 0, model.height)
	}
	seenTools := make(map[int]struct{})
	refreshes := model.transcriptRefreshCount
	started := time.Now()
	for y := 0; y < model.height; y++ {
		for column := 0; column < model.width; column++ {
			x := column
			if y%2 == 1 {
				x = model.width - 1 - column
			}
			motion := tea.MouseMsg{
				X:      x,
				Y:      y,
				Button: tea.MouseButtonNone,
				Action: tea.MouseActionMotion,
			}
			motionStarted := time.Now()
			filtered := filterIdleMouseMotion(current, motion)
			if filtered == nil {
				stats.filteredMotions++
				if collectDurations {
					stats.motionDurations = append(stats.motionDurations, time.Since(motionStarted))
				}
				continue
			}

			before := current.(appModel)
			next, _ := current.Update(filtered)
			current = next
			if renderView {
				localScrollViewSink = current.View()
			}
			after, ok := current.(appModel)
			if !ok {
				tb.Fatalf("hover update returned %T, want appModel", current)
			}
			duration := time.Since(motionStarted)
			stats.passedMotions++
			if collectDurations {
				stats.motionDurations = append(stats.motionDurations, duration)
				stats.passedDurations = append(stats.passedDurations, duration)
			}
			if before.toolHoverIndex != after.toolHoverIndex {
				if before.toolHoverIndex >= 0 {
					stats.toolLeaves++
				}
				if after.toolHoverIndex >= 0 {
					stats.toolEnters++
					seenTools[after.toolHoverIndex] = struct{}{}
					if !stats.hasToolPoint {
						stats.firstToolPoint = localHoverPoint{x: x, y: y}
						stats.hasToolPoint = true
					}
				}
			}
			if before.newMessageNoticeHovered != after.newMessageNoticeHovered {
				if after.newMessageNoticeHovered {
					stats.noticeEnters++
				} else {
					stats.noticeLeaves++
				}
			}
			if delta := after.transcriptRefreshCount - before.transcriptRefreshCount; delta > 0 {
				stats.transcriptRenders += after.transcriptRenderVisits
			}
		}
	}
	stats.elapsed = time.Since(started)
	stats.uniqueTools = len(seenTools)
	stats.seenTools = seenTools
	model = current.(appModel)
	stats.transcriptRefresh = model.transcriptRefreshCount - refreshes
	return current, stats
}

func assertLocalHoverSweep(tb testing.TB, stats localHoverSweepStats) {
	tb.Helper()
	if stats.totalMotions == 0 || stats.filteredMotions+stats.passedMotions != stats.totalMotions {
		tb.Fatalf("hover motions total=%d filtered=%d passed=%d", stats.totalMotions, stats.filteredMotions, stats.passedMotions)
	}
	if stats.filteredMotions == 0 {
		tb.Fatal("hover sweep did not exercise idle-motion filtering")
	}
	if stats.toolEnters == 0 || stats.toolLeaves == 0 || stats.uniqueTools == 0 || !stats.hasToolPoint {
		tb.Fatalf("hover sweep missed tool transitions: enter=%d leave=%d unique=%d", stats.toolEnters, stats.toolLeaves, stats.uniqueTools)
	}
	missing := make([]int, 0)
	for index := range stats.expectedTools {
		if _, ok := stats.seenTools[index]; !ok {
			missing = append(missing, index)
		}
	}
	sort.Ints(missing)
	if len(missing) > 0 || len(stats.seenTools) != len(stats.expectedTools) {
		tb.Fatalf("hover sweep visited tools=%v, expected every visible tool=%v, missing=%v", stats.seenTools, stats.expectedTools, missing)
	}
	if stats.noticeEnters == 0 || stats.noticeLeaves == 0 {
		tb.Fatalf("hover sweep missed notice transitions: enter=%d leave=%d", stats.noticeEnters, stats.noticeLeaves)
	}
	if stats.transcriptRefresh != 0 || stats.transcriptRenders != 0 {
		tb.Fatalf("tool hovers refreshed/rendered transcript: refreshes=%d render_visits=%d", stats.transcriptRefresh, stats.transcriptRenders)
	}
}

func loadLocalScrollSessionModel(tb testing.TB) (appModel, localScrollSessionStats) {
	tb.Helper()
	path := strings.TrimSpace(os.Getenv(localScrollSessionEnv))
	if path == "" {
		tb.Skipf("set %s to a local session directory or transcript.jsonl", localScrollSessionEnv)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		tb.Fatalf("resolve local session path: %v", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		tb.Fatalf("stat local session path: %v", err)
	}
	if info.IsDir() {
		absPath = filepath.Join(absPath, "transcript.jsonl")
		info, err = os.Stat(absPath)
		if err != nil {
			tb.Fatalf("stat local transcript: %v", err)
		}
	}
	if filepath.Base(absPath) != "transcript.jsonl" {
		tb.Fatalf("%s must point to a session directory or transcript.jsonl", localScrollSessionEnv)
	}
	if info.Size() < localScrollSessionMinimumBytes {
		tb.Fatalf("local transcript is %d bytes, want at least %d bytes", info.Size(), localScrollSessionMinimumBytes)
	}
	sessionDir := filepath.Dir(absPath)
	sessionsDir := filepath.Dir(sessionDir)
	if filepath.Base(sessionsDir) != "sessions" {
		tb.Fatalf("local transcript must use <store>/sessions/<session-id>/transcript.jsonl layout")
	}

	storeBase := filepath.Dir(sessionsDir)
	store, err := session.NewJSONLStore(storeBase)
	if err != nil {
		tb.Fatalf("open local session store: %v", err)
	}
	records, err := store.LoadResolvedRecords(context.Background(), filepath.Base(sessionDir))
	if err != nil {
		tb.Fatalf("load local session records: %v", err)
	}
	workspaceRoot := ""
	if filepath.Base(storeBase) == ".paw" {
		workspaceRoot = filepath.Dir(storeBase)
	}
	entries := transcriptEntriesFromRecords(records, nil, workspaceRoot)
	if len(entries) == 0 {
		tb.Fatal("local session produced no transcript entries")
	}

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 40
	model.relayout()
	model.replaceTranscript(entries)
	model.refreshViewport()
	localScrollViewSink = model.View()

	delta := model.viewport.MouseWheelDelta
	if delta <= 0 {
		tb.Fatalf("mouse wheel delta = %d, want positive", delta)
	}
	availableScrolls := model.viewport.YOffset / delta
	forwardScrolls := min(2_048, availableScrolls/2)
	if forwardScrolls < 32 {
		tb.Fatalf("local session has only %d available wheel events, want at least 64", availableScrolls)
	}
	hoverScrolls := localHoverTargetScrolls(tb, model, forwardScrolls, delta)
	return model, localScrollSessionStats{
		fileBytes:       info.Size(),
		records:         len(records),
		entries:         len(entries),
		renderedLines:   model.viewport.TotalLineCount(),
		forwardScrolls:  forwardScrolls,
		hoverScrolls:    hoverScrolls,
		mouseWheelDelta: delta,
	}
}

func localVisibleToolHoverIndices(model appModel) map[int]struct{} {
	indices := make(map[int]struct{})
	if !model.transcriptInteraction.valid {
		return indices
	}
	top := maxInt(0, model.viewport.YOffset)
	bottom := minInt(len(model.transcriptInteraction.rows), top+model.viewport.Height)
	for row := top; row < bottom; row++ {
		if index := model.transcriptInteraction.rows[row].toolIndex; index >= 0 {
			indices[index] = struct{}{}
		}
	}
	return indices
}

func localHoverTargetScrolls(tb testing.TB, model appModel, preferred, delta int) int {
	tb.Helper()
	probe := model
	locations := probe.transcriptEntryLocationsAt()
	bottom := model.viewport.YOffset
	bestScrolls := -1
	bestDistance := int(^uint(0) >> 1)
	for _, location := range locations {
		if location.transcriptIndex < 0 || location.transcriptIndex >= len(model.transcript) ||
			!isToolTransaction(model.transcript[location.transcriptIndex]) {
			continue
		}
		targetOffset := max(0, min(bottom, location.startRow-model.viewport.Height/2))
		scrolls := (bottom - targetOffset + delta - 1) / delta
		finalOffset := max(0, bottom-scrolls*delta)
		if location.startRow < finalOffset || location.startRow >= finalOffset+model.viewport.Height {
			continue
		}
		distance := scrolls - preferred
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestScrolls = scrolls
			bestDistance = distance
		}
	}
	if bestScrolls < 0 {
		tb.Fatal("local session has no rendered tool row for hover testing")
	}
	return bestScrolls
}

func updateLocalScrollModel(tb testing.TB, model *appModel, msg tea.MouseMsg, renderView bool) {
	next, _ := model.Update(msg)
	updated, ok := next.(appModel)
	if !ok {
		tb.Fatalf("scroll update returned %T, want appModel", next)
	}
	*model = updated
	if renderView {
		localScrollViewSink = model.View()
	}
}

func localScrollMouseMsg(button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{
		X:      10,
		Y:      3,
		Type:   tea.MouseEventType(button),
		Button: button,
		Action: tea.MouseActionPress,
	}
}

func percentileIndex(length, percentile int) int {
	index := (length*percentile + 99) / 100
	return min(length-1, max(0, index-1))
}
