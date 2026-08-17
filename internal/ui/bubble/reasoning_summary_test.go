package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestReasoningSummaryTranscriptOmitsLegacyLabel(t *testing.T) {
	started := time.Unix(500, 0)
	finished := started.Add(2 * time.Second)
	entry := transcriptEntry{
		kind:                entryReasoning,
		title:               "reasoning",
		body:                "provider returned summary",
		reasoningStartedAt:  &started,
		reasoningFinishedAt: &finished,
		createdAt:           started,
	}

	collapsed := ansi.Strip(renderEntryAt(entry, 80, finished, false))
	if strings.Contains(collapsed, "reasoning >") {
		t.Fatalf("legacy reasoning label is still rendered: %q", collapsed)
	}
	if !strings.Contains(collapsed, "Thought for 2 s") || strings.Contains(collapsed, "provider returned summary") {
		t.Fatalf("collapsed reasoning = %q", collapsed)
	}

	expanded := ansi.Strip(renderEntryAt(entry, 80, finished, true))
	if strings.Contains(expanded, "reasoning >") || !strings.Contains(expanded, "provider returned summary") {
		t.Fatalf("expanded reasoning = %q", expanded)
	}
}
