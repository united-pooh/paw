package bubble

import (
	"fmt"
	"strings"
	"testing"
)

func TestTranscriptInvalidationKeepsEarliestDirtyIndex(t *testing.T) {
	var invalidation transcriptInvalidation
	invalidation.markFrom(8)
	invalidation.markFrom(3)
	invalidation.markFrom(5)

	if !invalidation.dirty || invalidation.full || invalidation.from != 3 {
		t.Fatalf("invalidation = %#v, want dirty suffix from 3", invalidation)
	}
}

func TestTranscriptInvalidationFullDominatesEntryChanges(t *testing.T) {
	var invalidation transcriptInvalidation
	invalidation.markFrom(8)
	invalidation.markFull()
	invalidation.markFrom(12)

	if !invalidation.dirty || !invalidation.full || invalidation.from != 0 {
		t.Fatalf("invalidation = %#v, want full invalidation", invalidation)
	}
}

func TestTranscriptDirtyToolEntryNormalizesToReadyGroupStart(t *testing.T) {
	m := appModel{transcript: []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "before"},
		{kind: entryTool, title: "tool", toolName: "Read", toolStatus: "ok"},
		{kind: entryTool, title: "tool", toolName: "Bash", toolStatus: "running"},
	}}

	if got := m.normalizeTranscriptDirtyIndex(2); got != 1 {
		t.Fatalf("normalized dirty index = %d, want tool group start 1", got)
	}
}

func TestTranscriptRenderConfigChangeForcesFullReplacement(t *testing.T) {
	m := appModel{transcript: []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "hello"}}}
	changed, start, _ := m.ensureTranscriptLinesAt(80, true, m.animationNow())
	if !changed || start != -1 {
		t.Fatalf("initial render = changed %v start %d, want full replacement", changed, start)
	}

	changed, start, _ = m.ensureTranscriptLinesAt(81, true, m.animationNow())
	if !changed || start != -1 {
		t.Fatalf("config render = changed %v start %d, want full replacement", changed, start)
	}
}

func TestTranscriptAppendPreservesHistoricalRenderCache(t *testing.T) {
	m := appModel{transcript: []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "first"},
		{kind: entryAssistant, title: "assistant", body: "second"},
	}}
	m.ensureTranscriptLinesAt(80, true, m.animationNow())
	first := m.transcriptRenderCache[0]
	second := m.transcriptRenderCache[1]

	m.appendTranscriptEntry(transcriptEntry{kind: entryAssistant, title: "assistant", body: "third"})
	changed, start, _ := m.ensureTranscriptLinesAt(80, true, m.animationNow())
	if !changed || start < 0 {
		t.Fatalf("append render = changed %v start %d, want suffix replacement", changed, start)
	}
	if len(m.transcriptRenderCache) != 3 {
		t.Fatalf("cache length = %d, want 3", len(m.transcriptRenderCache))
	}
	if m.transcriptRenderCache[0] != first || m.transcriptRenderCache[1] != second {
		t.Fatal("append discarded historical render cache entries")
	}
}

func TestTranscriptWarmCacheDoesNotVisitEntries(t *testing.T) {
	entries := make([]transcriptEntry, 100)
	for index := range entries {
		entries[index] = transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"}
	}
	m := appModel{transcript: entries}
	m.ensureTranscriptLinesAt(80, true, m.animationNow())

	m.ensureTranscriptLinesAt(80, true, m.animationNow())

	if got := m.transcriptRenderVisits; got != 0 {
		t.Fatalf("warm cache visited %d transcript entries, want 0", got)
	}
}

func TestTranscriptTailMutationVisitsOnlyDirtySuffix(t *testing.T) {
	entries := make([]transcriptEntry, 100)
	for index := range entries {
		entries[index] = transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"}
	}
	m := appModel{transcript: entries}
	m.ensureTranscriptLinesAt(80, true, m.animationNow())
	m.transcript[98].body = "changed"
	m.touchTranscriptEntryAt(98)

	m.ensureTranscriptLinesAt(80, true, m.animationNow())

	if got := m.transcriptRenderVisits; got != 2 {
		t.Fatalf("tail mutation visited %d transcript entries, want dirty suffix size 2", got)
	}
}

func TestTranscriptTailMutationDoesNotScanHiddenStablePrefix(t *testing.T) {
	entries := []transcriptEntry{{kind: entryAssistant, title: "assistant", body: "visible prefix"}}
	for range 1_000 {
		entries = append(entries, transcriptEntry{kind: entryThinking, title: "thinking", body: "hidden"})
	}
	entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: "tail"})
	m := appModel{transcript: entries}
	m.ensureTranscriptLinesAt(80, false, m.animationNow())
	m.transcriptPrefixAnchorVisits = 0
	tail := len(m.transcript) - 1
	m.transcript[tail].body = "changed"
	m.touchTranscriptEntryAt(tail)

	m.ensureTranscriptLinesAt(80, false, m.animationNow())

	if got := m.transcriptPrefixAnchorVisits; got != 2 {
		t.Fatalf("tail mutation accessed %d stable prefix anchors, want 2 constant-time lookups", got)
	}
}

func TestTranscriptEmptyRenderableEntryKeepsPrefixAnchorForTailMutation(t *testing.T) {
	m := appModel{transcript: []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "visible prefix"},
		{kind: entryTool, title: "terminal"},
		{kind: entryAssistant, title: "assistant", body: "tail"},
	}}
	m.ensureTranscriptLinesAt(80, true, m.animationNow())
	m.transcript[2].body = "changed tail"
	m.touchTranscriptEntryAt(2)

	m.ensureTranscriptLinesAt(80, true, m.animationNow())

	content := strings.Join(m.transcriptLines, "\n")
	if !strings.Contains(content, "visible prefix") || !strings.Contains(content, "changed tail") {
		t.Fatalf("tail mutation lost stable prefix around empty entry:\n%s", content)
	}
}

func TestTranscriptDirtyToolGroupRendersAsOneLinearUnit(t *testing.T) {
	entries := make([]transcriptEntry, 200)
	for index := range entries {
		entries[index] = transcriptEntry{
			kind: entryTool, title: "tool", toolUseID: fmt.Sprintf("tool-%03d", index),
			toolName: "Read", toolStatus: "ok", toolResult: "done",
		}
	}
	m := appModel{transcript: entries}
	m.ensureTranscriptLinesAt(80, true, m.animationNow())
	m.transcript[len(m.transcript)-1].toolResult = "changed"
	m.touchTranscriptEntryAt(len(m.transcript) - 1)

	m.ensureTranscriptLinesAt(80, true, m.animationNow())

	if got := m.transcriptRenderVisits; got != 1 {
		t.Fatalf("dirty tool group entered render loop %d times, want 1 group unit", got)
	}
	if got := m.transcriptInteractionVisits; got != 1 {
		t.Fatalf("dirty tool group entered interaction loop %d times, want 1 group unit", got)
	}
}

func TestToolGroupExpansionChangeVisitsOnlyRunningSuffix(t *testing.T) {
	entries := make([]transcriptEntry, 100)
	for index := range entries {
		entries[index] = transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"}
	}
	entries = append(entries, transcriptEntry{
		kind: entryTool, title: "tool", toolUseID: "tail-tool", toolName: "Read",
		toolStatus: "running", body: "running",
	})
	m := appModel{}
	m.replaceTranscript(entries)
	m.ensureTranscriptLinesAt(80, true, m.animationNow())
	m.toolGroupExpanded = true

	changed, start, _ := m.ensureTranscriptLinesAt(80, true, m.animationNow())

	if !changed || start < 0 {
		t.Fatalf("group expansion render = changed %v start %d, want suffix replacement", changed, start)
	}
	if got := m.transcriptRenderVisits; got != 1 {
		t.Fatalf("group expansion visited %d transcript entries, want running suffix size 1", got)
	}
}
