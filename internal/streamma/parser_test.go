package streamma

import (
	"context"
	"errors"
	"gocode/internal/model"
	"strings"
	"testing"
	"time"
)

func parseTextForTest(t *testing.T, text string, config ParserConfig) ([]StepPacket, error) {
	t.Helper()
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: text}
	events <- model.StreamEvent{Done: true}
	close(events)
	return ParseStream(context.Background(), events, config)
}

func parseChunksForTest(t *testing.T, config ParserConfig, chunks ...string) ([]StepPacket, error) {
	t.Helper()
	events := make(chan model.StreamEvent, len(chunks)+1)
	for _, chunk := range chunks {
		events <- model.StreamEvent{Delta: chunk}
	}
	events <- model.StreamEvent{Done: true}
	close(events)
	return ParseStream(context.Background(), events, config)
}

func TestParserEmitsMultipleStepsFromOneResponse(t *testing.T) {
	steps, err := parseTextForTest(t, "first\nEND_STEP\nsecond\nEND_STEP\nthird\nEND_STEP\n", ParserConfig{RunID: "run", AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("len(steps) = %d, want 3", len(steps))
	}
	if steps[0].Content.Text != "first\n" || steps[1].Content.Text != "second\n" || steps[2].Content.Text != "third\n" {
		t.Fatalf("unexpected step text: %#v", steps)
	}
}

func TestParserOnlyStandaloneSentinelOutsideCodeBlockClosesStep(t *testing.T) {
	text := strings.Join([]string{
		"inline END_STEP text stays",
		"```txt",
		"END_STEP",
		"```",
		"real content",
		"END_STEP",
		"",
	}, "\n")
	steps, err := parseTextForTest(t, text, ParserConfig{RunID: "run", AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1: %#v", len(steps), steps)
	}
	if !strings.Contains(steps[0].Content.Text, "inline END_STEP text stays") {
		t.Fatalf("inline sentinel was not preserved: %q", steps[0].Content.Text)
	}
	if !strings.Contains(steps[0].Content.Text, "```txt\nEND_STEP\n```") {
		t.Fatalf("code-block sentinel was not preserved: %q", steps[0].Content.Text)
	}
}

func TestParserPreservesStepTextExactlyExceptSentinelLine(t *testing.T) {
	steps, err := parseTextForTest(t, "  first line  \n\nEND_STEP\n  END_STEP  \nreal close\nEND_STEP\n", ParserConfig{RunID: "run", AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2: %#v", len(steps), steps)
	}
	if steps[0].Content.Text != "  first line  \n\n" {
		t.Fatalf("step 1 text = %q", steps[0].Content.Text)
	}
	if steps[1].Content.Text != "  END_STEP  \nreal close\n" {
		t.Fatalf("step 2 text = %q", steps[1].Content.Text)
	}
}

func TestParserHandlesChunkBoundariesAcrossSentinel(t *testing.T) {
	steps, err := parseChunksForTest(t, ParserConfig{RunID: "run", AgentID: "a"},
		"alpha\nEN",
		"D_STEP\nbe",
		"ta\nEND",
		"_STEP\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2: %#v", len(steps), steps)
	}
	if steps[0].Content.Text != "alpha\n" || steps[1].Content.Text != "beta\n" {
		t.Fatalf("unexpected chunked parse: %#v", steps)
	}
}

func TestStreamStepsEmitsStepBeforeDone(t *testing.T) {
	events := make(chan model.StreamEvent)
	emitted := make(chan StepPacket, 1)
	errs := make(chan error, 1)

	go func() {
		errs <- StreamSteps(context.Background(), events, ParserConfig{RunID: "run", AgentID: "a"}, func(step StepPacket) error {
			emitted <- step
			return nil
		})
	}()

	events <- model.StreamEvent{Delta: "early\nEND_STEP\n"}
	select {
	case step := <-emitted:
		if step.Content.Text != "early\n" || step.Boundary.BoundaryRecovered {
			t.Fatalf("unexpected emitted step: %#v", step)
		}
	case <-time.After(time.Second):
		t.Fatal("StreamSteps did not emit a step before stream Done")
	}

	select {
	case err := <-errs:
		t.Fatalf("StreamSteps returned before Done: %v", err)
	default:
	}

	events <- model.StreamEvent{Done: true}
	close(events)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

func TestParserReturnsStreamEventErrors(t *testing.T) {
	want := errors.New("stream interrupted")
	events := make(chan model.StreamEvent, 1)
	events <- model.StreamEvent{Err: want}
	close(events)

	_, err := ParseStream(context.Background(), events, ParserConfig{RunID: "run", AgentID: "a"})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestParserForcedClosesTrailingContentWithoutFinalSentinel(t *testing.T) {
	steps, err := parseTextForTest(t, "tail without sentinel", ParserConfig{RunID: "run", AgentID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(steps))
	}
	if !steps[0].Boundary.BoundaryRecovered {
		t.Fatalf("BoundaryRecovered = false, want true")
	}
}

func TestParserRequireBoundaryRejectsTrailingContentWithoutFinalSentinel(t *testing.T) {
	_, err := parseTextForTest(t, "tail without sentinel", ParserConfig{RunID: "run", AgentID: "a", RequireBoundary: true})
	if err == nil || !IsParserFatal(err) || !strings.Contains(err.Error(), "stream ended before exact boundary") {
		t.Fatalf("ParseStream() error = %v, want parser fatal missing boundary", err)
	}
}

func TestParserAcceptsFinalBoundaryWithoutTrailingNewline(t *testing.T) {
	steps, err := parseTextForTest(t, "final step\nEND_STEP", ParserConfig{
		RunID:           "run",
		AgentID:         "a",
		RequireBoundary: true,
	})
	if err != nil {
		t.Fatalf("ParseStream() error = %v", err)
	}
	if len(steps) != 1 || steps[0].Content.Text != "final step\n" || !steps[0].Boundary.Closed {
		t.Fatalf("steps = %#v, want one closed final step", steps)
	}
}

func TestParserFatalOnMaxByteViolation(t *testing.T) {
	_, err := parseTextForTest(t, "abcd", ParserConfig{RunID: "run", AgentID: "a", MaxStepBytes: 3})
	if err == nil {
		t.Fatal("expected error")
	}
	var fatal *ParserFatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("error = %T %v, want ParserFatalError", err, err)
	}
}
