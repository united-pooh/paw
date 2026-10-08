package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
)

func TestTokenMonitorContextDoesNotTurnMissingComponentsIntoZero(t *testing.T) {
	for _, stats := range []loop.ContextStats{
		{LimitTokens: 1000},
		{Known: true, LimitTokens: 1000},
		{Known: true, InputKnown: true, UsedTokens: 100, InputTokens: 100, LimitTokens: 1000},
		{Known: true, OutputKnown: true, UsedTokens: 100, OutputTokens: 100, LimitTokens: 1000},
	} {
		got := ansi.Strip(strings.Join(tokenMonitorContext(tokenMonitorView{Context: stats}, 32, 6), "\n"))
		if strings.Contains(got, "%") || strings.ContainsAny(got, "▰▱") || !strings.Contains(got, "— / 1k") {
			t.Errorf("partial context rendered as complete: stats=%#v\n%s", stats, got)
		}
	}
}

func TestTokenMonitorContextAcceptsExplicitTotalAndKnownZero(t *testing.T) {
	for _, stats := range []loop.ContextStats{
		{Known: true, TotalKnown: true, UsedTokens: 100, LimitTokens: 1000},
		{Known: true, TotalKnown: true, LimitTokens: 1000},
		{Known: true, InputKnown: true, OutputKnown: true, LimitTokens: 1000},
	} {
		got := ansi.Strip(strings.Join(tokenMonitorContext(tokenMonitorView{Context: stats}, 32, 6), "\n"))
		if !strings.Contains(got, "%") || !strings.Contains(got, "▱") {
			t.Errorf("known capacity lost: stats=%#v\n%s", stats, got)
		}
	}
}
