package bubble

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/loop"
)

func TestStatusLineExactWidthAcrossWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)
	for _, width := range []int{8, 12, 20, 32, 60, 80, 100} {
		line := model.renderDockStatusLine(width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d status cell width=%d line=%q", width, got, ansi.Strip(line))
		}
	}
}

func TestStatusLineModeIndicator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Now()
	if dock := ansi.Strip(model.renderDockStatusLine(80)); !strings.Contains(dock, "chat") {
		t.Fatalf("default mode = %q, want chat", dock)
	}
	model.input.SetValue("!ls")
	if dock := ansi.Strip(model.renderDockStatusLine(80)); !strings.Contains(dock, "!shell") {
		t.Fatalf("bang mode = %q, want !shell", dock)
	}
}

func TestStatusLineUsesTokenCountAndReady(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	dock := ansi.Strip(model.renderDockStatusLine(100))
	if !strings.Contains(dock, "ready") || !strings.Contains(dock, "12k / 128k") {
		t.Fatalf("idle dock = %q, want ready and token count", dock)
	}
	if strings.Contains(dock, "cache") || strings.Contains(dock, "free") {
		t.Fatalf("idle dock still contains retired telemetry: %q", dock)
	}
}

func TestTokenFrontierRippleStartsAtUsedProgressAndMoves(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
	model.isGenerating = true
	model.cursorFrameAt = time.Unix(0, int64(300*time.Millisecond))
	first := ansi.Strip(model.renderDockStatusLine(100))
	model.cursorFrameAt = time.Unix(0, int64(700*time.Millisecond))
	second := ansi.Strip(model.renderDockStatusLine(100))
	if first == second {
		t.Fatalf("ripple did not move: %q", first)
	}
	if !strings.Contains(first, "░") || !strings.Contains(first, "█") {
		t.Fatalf("ripple lacks gradient head/tail: %q", first)
	}
}

func TestTokenRippleNarrowSpanWaitsForTailBeforeNextHead(t *testing.T) {
	tests := []struct {
		head int
		want []int
	}{
		{head: 4, want: []int{4, 3, 2, 1, 0}},
		{head: 5, want: []int{5, 4, 3, 2, 1}},
		{head: 13, want: []int{13, 12, 11, 10, 9}},
		{head: 14, want: []int{0, 13, 12, 11, 10}},
	}
	for _, test := range tests {
		got := tokenRippleNarrowDistancesAtHead(5, test.head)
		if !slices.Equal(got, test.want) {
			t.Fatalf("head=%d distances=%v, want %v", test.head, got, test.want)
		}
	}
}

func TestTokenRippleNarrowSpanPreservesUncompressedCyclicOrder(t *testing.T) {
	for freeCells := 1; freeCells < tokenRippleTail; freeCells++ {
		for head := 0; head < tokenRippleTail*2; head++ {
			distances := tokenRippleNarrowDistancesAtHead(freeCells, head)
			if len(distances) != freeCells {
				t.Fatalf("free=%d head=%d len=%d", freeCells, head, len(distances))
			}
			for i, distance := range distances {
				if distance < 0 || distance >= tokenRippleTail {
					t.Fatalf("free=%d head=%d distance[%d]=%d", freeCells, head, i, distance)
				}
				if i > 0 {
					want := positiveModulo(distances[i-1]-1, tokenRippleTail)
					if distance != want {
						t.Fatalf("free=%d head=%d distances=%v: distance[%d]=%d, want %d", freeCells, head, distances, i, distance, want)
					}
				}
			}
		}
	}
}

func TestTokenRippleNarrowSpanDoesNotPinHead(t *testing.T) {
	var observed []int
	for head := 0; head < tokenRippleTail; head++ {
		observed = append(observed, tokenRippleNarrowDistancesAtHead(1, head)[0])
	}
	want := make([]int, tokenRippleTail)
	for i := range want {
		want[i] = i
	}
	if !slices.Equal(observed, want) {
		t.Fatalf("single-cell ripple distances=%v, want %v", observed, want)
	}
}

func TestTokenFrontierRippleFadesAtRightEdge(t *testing.T) {
	if got := tokenRippleFade(tokenRippleTravel); got != 1 {
		t.Fatalf("ripple fade before end = %v, want 1", got)
	}
	if got := tokenRippleFade(tokenRippleTravel + tokenRippleExit/2); got >= 1 || got <= 0 {
		t.Fatalf("ripple fade at end = %v, want between 0 and 1", got)
	}
}

func TestTokenFrontierRippleContinuesPastRightEdge(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
	model.isGenerating = true
	model.cursorFrameAt = time.Unix(0, int64(tokenRippleTravel))
	atEdge := ansi.Strip(model.renderDockStatusLine(100))
	model.cursorFrameAt = time.Unix(0, int64(tokenRippleTravel+tokenRippleExit/2))
	midExit := ansi.Strip(model.renderDockStatusLine(100))
	model.cursorFrameAt = time.Unix(0, int64(tokenRippleCycle-time.Millisecond))
	end := ansi.Strip(model.renderDockStatusLine(100))
	if atEdge == midExit || midExit == end {
		t.Fatalf("ripple did not progress through exit phase: edge=%q mid=%q end=%q", atEdge, midExit, end)
	}
	if tokenRippleFade(tokenRippleTravel+tokenRippleExit/2) <= 0 || tokenRippleFade(tokenRippleCycle-time.Millisecond) >= tokenRippleFade(tokenRippleTravel+tokenRippleExit/2) {
		t.Fatalf("ripple fade does not decrease across exit phase")
	}
}

func TestTokenFrontierRippleSurvivesTurnCompletion(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	model.isGenerating = true
	completedAt := time.Unix(0, int64(tokenRippleTravel))
	model.cursorFrameAt = completedAt

	next, _ := model.Update(turnFinishedMsg{})
	model = next.(appModel)
	if model.isAgentWorking() {
		t.Fatal("model should be idle after turn completion")
	}

	model.cursorFrameAt = completedAt.Add(tokenRippleExit / 2)
	exiting := ansi.Strip(model.renderTokenFrontier(80, 40000, 100000))
	if !strings.ContainsAny(exiting, "░▒▓") {
		t.Fatalf("completed-turn ripple disappeared during exit: %q", exiting)
	}

	model.cursorFrameAt = completedAt.Add(tokenRippleExit + time.Millisecond)
	settled := ansi.Strip(model.renderTokenFrontier(80, 40000, 100000))
	if strings.ContainsAny(settled, "░▒▓█") {
		t.Fatalf("completed-turn ripple remained after exit: %q", settled)
	}
}

func TestStatusLineWorkingDuringToolCall(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	dock := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.Contains(dock, "working") || strings.Contains(dock, "ready") {
		t.Fatalf("tool-call dock = %q, want working only", dock)
	}
}
