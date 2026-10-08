package bubble

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"paw/internal/capability/model"
)

func TestTokenMonitorLiveFragmentationAndWindow(t *testing.T) {
	start := time.Unix(100, 0)
	var whole, split tokenLiveState
	whole.start("request", start)
	split.start("request", start)
	whole.text("abcdefgh你好", start.Add(time.Second))
	for _, part := range []string{"a", "bc", "defgh", "你", "好"} {
		split.text(part, start.Add(time.Second))
	}
	if whole.total != 4 || whole.total != split.total {
		t.Fatalf("chunk-dependent token estimate: %v / %v", whole.total, split.total)
	}
	if got := whole.view(start.Add(time.Second)); got.Current <= 0 || got.FirstText != time.Second || !got.FirstTextKnown {
		t.Fatalf("arrival sample = %+v", got)
	}
	if got := whole.view(start.Add(5 * time.Second)); got.Current != 0 {
		t.Fatalf("stalled output still has speed: %+v", got)
	}
	for i := 0; i < 1000; i++ {
		whole.text("abcd", start.Add(time.Duration(i+6)*time.Second))
	}
	if got := whole.view(start.Add(1005 * time.Second)); len(got.Samples) != 60 || math.IsNaN(got.Average) {
		t.Fatalf("unbounded/invalid sample: %+v", got)
	}
}

func TestTokenMonitorLiveFinishAndNewRequest(t *testing.T) {
	now := time.Unix(100, 0)
	var live tokenLiveState
	live.start("one", now)
	live.text("abcd", now.Add(time.Second))
	live.text("efgh", now.Add(2*time.Second))
	live.finish(now.Add(3 * time.Second))
	finished := live.view(now.Add(50 * time.Second))
	if finished.Active || finished.Current != 0 || math.Abs(finished.Average-2.0/3) > 1e-9 || !finished.FirstTextKnown {
		t.Fatalf("finished = %+v", finished)
	}
	live.start("two", now.Add(time.Minute))
	if got := live.view(now.Add(time.Minute)); got.FirstTextKnown || got.Average != 0 || got.RequestID != "two" {
		t.Fatalf("request retained old timing: %+v", got)
	}
	live.text("", now.Add(time.Minute))
	if live.view(now.Add(time.Minute)).FirstTextKnown {
		t.Fatal("empty delta counted as visible text")
	}
}

func TestTokenMonitorUnobservedNewRequestKeepsPreviousHistory(t *testing.T) {
	start := time.Unix(100, 0)
	var live tokenLiveState
	live.start("streamed", start)
	live.text("abcd", start.Add(time.Second))
	if !live.view(start.Add(time.Second)).Observed {
		t.Fatal("actual stream activity was not marked observed")
	}
	live.requestEnd("streamed", start.Add(2*time.Second))
	live.start("completion-only", start.Add(3*time.Second))
	live.requestEnd("completion-only", start.Add(4*time.Second))
	got := live.view(start.Add(4 * time.Second))
	if got.Observed || got.Average != 0 || got.Samples[56] != 1 {
		t.Fatalf("new completion-only request inherited observed status or cleared historical samples: %+v", got)
	}
}

func TestTokenMonitorArrivalCountsReasoningAndArgumentsBeforeVisibleText(t *testing.T) {
	start := time.Unix(100, 0)
	var live tokenLiveState
	live.start("request", start)
	live.activity("request", model.StreamActivity{At: start.Add(time.Second), Reasoning: "thinking"})
	live.activity("request", model.StreamActivity{At: start.Add(2 * time.Second), ToolArguments: `{"q":"abcd"}`})
	got := live.view(start.Add(2 * time.Second))
	if got.Current <= 0 || got.Average <= 0 || got.FirstTextKnown || live.total != 5 {
		t.Fatalf("reasoning and native arguments were invisible to sampler: %+v total=%v", got, live.total)
	}
	live.activity("request", model.StreamActivity{At: start.Add(3 * time.Second), Text: "abcd"})
	got = live.view(start.Add(3 * time.Second))
	if !got.FirstTextKnown || got.FirstText != 3*time.Second || got.Samples[57] != 2 || got.Samples[58] != 3 || got.Samples[59] != 1 {
		t.Fatalf("arrival buckets or first visible text timestamp were changed: %+v", got)
	}
	live.finish(start.Add(time.Hour))
	live.requestEnd("request", start.Add(4*time.Second))
	live.requestEnd("request", start.Add(8*time.Second))
	got = live.view(start.Add(time.Hour))
	if got.Active || got.Average != 1.5 || got.Current != 0 {
		t.Fatalf("display completion overwrote transport end or duplicate finish changed average: %+v", got)
	}
	live.start("next", start.Add(time.Hour))
	live.activity("request", model.StreamActivity{At: start.Add(time.Hour), Text: "old"})
	live.requestEnd("request", start.Add(time.Hour))
	if !live.active || live.total != 0 || !live.finished.IsZero() {
		t.Fatal("late old-request arrival or finish changed the new request")
	}
}

func TestTokenMonitorToolStartDoesNotStopNativeArgumentSampling(t *testing.T) {
	start := time.Unix(100, 0)
	var live tokenLiveState
	live.start("request", start)
	live.toolStart("native-tool", start.Add(time.Second))
	live.activity("request", model.StreamActivity{At: start.Add(2 * time.Second), ToolArguments: `{"q":"abcd"}`})
	if got := live.view(start.Add(2 * time.Second)); !got.Active || got.Current != 1 || live.total != 3 {
		t.Fatalf("native tool start stopped argument stream sampling: %+v total=%v", got, live.total)
	}
	live.requestEnd("request", start.Add(3*time.Second))
	if got := live.view(start.Add(4 * time.Second)); got.Active || got.Current != 0 || !got.ToolActive {
		t.Fatalf("request completion did not separate output stream from tool lifecycle: %+v", got)
	}
}

func TestTokenMonitorRequestAverageIncludesTimeBeforeFirstText(t *testing.T) {
	start := time.Unix(100, 0)
	var live tokenLiveState
	live.start("buffered-request", start)
	live.text(strings.Repeat("a", 400), start.Add(20*time.Second))
	live.finish(start.Add(20*time.Second + 100*time.Millisecond))
	got := live.view(start.Add(time.Minute))
	want := 100 / 20.1
	if math.Abs(got.Average-want) > 1e-9 {
		t.Fatalf("request average = %v, want %v across the full request, not the final display flush", got.Average, want)
	}
}

func TestTokenMonitorToolCallsDeduplicateAndExcludeArgumentGeneration(t *testing.T) {
	now := time.Unix(100, 0)
	var live tokenLiveState
	live.toolStart("read", now)
	live.toolStart("read", now.Add(time.Second))
	live.toolEnd("read", now.Add(2*time.Second))
	live.toolEnd("read", now.Add(3*time.Second))
	got := live.view(now.Add(4 * time.Second))
	if got.ToolCalls != 1 || got.ToolDuration != 2*time.Second {
		t.Fatalf("tool lifecycle = %+v", got)
	}
	live.resetTools()
	if got := live.view(now); got.ToolCalls != 0 || got.ToolDuration != 0 {
		t.Fatalf("new turn kept tool statistics: %+v", got)
	}
}

func TestTokenMonitorSamplerDoesNotRetainText(t *testing.T) {
	var live tokenLiveState
	now := time.Now()
	live.start("r", now)
	live.text(strings.Repeat("a", 1<<20), now.Add(time.Second))
	if live.total != 1<<18 || len(live.buckets) != 60 {
		t.Fatalf("bad bounded counter: %v", live.total)
	}
}

func TestTokenMonitorToolStorageBoundAndEndTurn(t *testing.T) {
	var live tokenLiveState
	now := time.Now()
	for i := 0; i < 4200; i++ {
		live.toolStart(fmt.Sprint(i), now)
	}
	if len(live.tools) != 4096 || !live.view(now).ToolCallsPartial {
		t.Fatal("tool lifecycle memory must remain bounded and labelled partial")
	}
	if !live.view(now).ToolActive {
		t.Fatal("tool activity not shown")
	}
	live.endTurn(now.Add(time.Second))
	got := live.view(now.Add(time.Hour))
	if got.ToolActive || got.ToolDuration != 4096*time.Second {
		t.Fatalf("finished tool timing continued: %+v", got)
	}
}
